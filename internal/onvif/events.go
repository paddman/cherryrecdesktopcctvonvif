package onvif

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxPullPoints    = 32
	maxEventQueue    = 64
	defaultEventTTL  = time.Minute
	maxEventTTL      = time.Hour
	maxPullTimeout   = 60 * time.Second
	defaultPullWait  = 5 * time.Second
	defaultPullLimit = 10
)

var isoDurationRE = regexp.MustCompile(`^PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+(?:\.\d+)?)S)?$`)

type eventNotification struct {
	when      time.Time
	videoLoss bool
	operation string
}

type eventSubscription struct {
	token   string
	expires time.Time
	queue   []eventNotification
	signal  chan struct{}
}

type eventBroker struct {
	mu        sync.Mutex
	subs      map[string]*eventSubscription
	ready     func() bool
	lastReady bool
}

func newEventBroker(ready func() bool) *eventBroker {
	return &eventBroker{
		subs:      make(map[string]*eventSubscription),
		ready:     ready,
		lastReady: ready(),
	}
}

func (b *eventBroker) run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := b.ready()
			b.mu.Lock()
			b.cleanupLocked(time.Now().UTC())
			if current != b.lastReady {
				b.lastReady = current
				b.enqueueAllLocked(eventNotification{
					when:      time.Now().UTC(),
					videoLoss: !current,
					operation: "Changed",
				})
			}
			b.mu.Unlock()
		}
	}
}

func (b *eventBroker) create(ttl time.Duration) (*eventSubscription, error) {
	now := time.Now().UTC()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupLocked(now)
	if len(b.subs) >= maxPullPoints {
		return nil, fmt.Errorf("maximum pull-point subscriptions reached")
	}
	token, err := randomEventToken()
	if err != nil {
		return nil, err
	}
	sub := &eventSubscription{
		token:   token,
		expires: now.Add(ttl),
		signal:  make(chan struct{}, 1),
	}
	b.subs[token] = sub
	copySub := *sub
	return &copySub, nil
}

func (b *eventBroker) renew(token string, ttl time.Duration) (time.Time, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupLocked(time.Now().UTC())
	sub, ok := b.subs[token]
	if !ok {
		return time.Time{}, false
	}
	sub.expires = time.Now().UTC().Add(ttl)
	return sub.expires, true
}

func (b *eventBroker) unsubscribe(token string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.subs[token]; !ok {
		return false
	}
	delete(b.subs, token)
	return true
}

func (b *eventBroker) synchronizationPoint(token string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cleanupLocked(time.Now().UTC())
	sub, ok := b.subs[token]
	if !ok {
		return false
	}
	b.enqueueLocked(sub, eventNotification{
		when:      time.Now().UTC(),
		videoLoss: !b.ready(),
		operation: "Initialized",
	})
	return true
}

func (b *eventBroker) pull(ctx context.Context, token string, timeout time.Duration, limit int) ([]eventNotification, time.Time, bool) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	for {
		b.mu.Lock()
		b.cleanupLocked(time.Now().UTC())
		sub, ok := b.subs[token]
		if !ok {
			b.mu.Unlock()
			return nil, time.Time{}, false
		}
		if len(sub.queue) > 0 {
			if limit > len(sub.queue) {
				limit = len(sub.queue)
			}
			out := append([]eventNotification(nil), sub.queue[:limit]...)
			sub.queue = append([]eventNotification(nil), sub.queue[limit:]...)
			expires := sub.expires
			b.mu.Unlock()
			return out, expires, true
		}
		signal := sub.signal
		expires := sub.expires
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil, expires, true
		case <-deadline.C:
			return nil, expires, true
		case <-signal:
		}
	}
}

func (b *eventBroker) enqueueAllLocked(event eventNotification) {
	for _, sub := range b.subs {
		b.enqueueLocked(sub, event)
	}
}

func (b *eventBroker) enqueueLocked(sub *eventSubscription, event eventNotification) {
	if len(sub.queue) >= maxEventQueue {
		sub.queue = append([]eventNotification(nil), sub.queue[len(sub.queue)-maxEventQueue+1:]...)
	}
	sub.queue = append(sub.queue, event)
	select {
	case sub.signal <- struct{}{}:
	default:
	}
}

func (b *eventBroker) cleanupLocked(now time.Time) {
	for token, sub := range b.subs {
		if !sub.expires.After(now) {
			delete(b.subs, token)
		}
	}
}

func randomEventToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Server) EventURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/events_service", s.ip, s.cfg.ONVIFPort)
}

func (s *Server) pullPointURL(token string) string {
	return fmt.Sprintf("http://%s:%d/onvif/pullpoint/%s", s.ip, s.cfg.ONVIFPort, token)
}

func (s *Server) eventsService(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAuthorizedSOAP(w, r)
	if !ok {
		return
	}
	switch action := soapAction(body); action {
	case "GetServiceCapabilities":
		s.eventSOAP(w, fmt.Sprintf(`<tev:GetServiceCapabilitiesResponse><tev:Capabilities WSSubscriptionPolicySupport="false" WSPullPointSupport="true" WSPausableSubscriptionManagerInterfaceSupport="false" MaxNotificationProducers="1" MaxPullPoints="%d" PersistentNotificationStorage="false"/></tev:GetServiceCapabilitiesResponse>`, maxPullPoints))
	case "GetEventProperties":
		s.eventSOAP(w, `<tev:GetEventPropertiesResponse><tev:TopicNamespaceLocation>http://www.onvif.org/onvif/ver10/topics/topicns.xml</tev:TopicNamespaceLocation><tev:FixedTopicSet>true</tev:FixedTopicSet><wstop:TopicSet><tns1:VideoSource><tns1:VideoLoss wstop:topic="true"><tt:MessageDescription IsProperty="true"><tt:Source><tt:SimpleItemDescription Name="VideoSourceConfigurationToken" Type="tt:ReferenceToken"/></tt:Source><tt:Data><tt:SimpleItemDescription Name="State" Type="xs:boolean"/></tt:Data></tt:MessageDescription></tns1:VideoLoss></tns1:VideoSource></wstop:TopicSet><tev:TopicExpressionDialect>http://docs.oasis-open.org/wsn/t-1/TopicExpression/Concrete</tev:TopicExpressionDialect><tev:TopicExpressionDialect>http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet</tev:TopicExpressionDialect><tev:MessageContentFilterDialect></tev:MessageContentFilterDialect><tev:MessageContentSchemaLocation>http://www.onvif.org/onvif/ver10/schema/onvif.xsd</tev:MessageContentSchemaLocation></tev:GetEventPropertiesResponse>`)
	case "CreatePullPointSubscription":
		ttl := parseTerminationDuration(elementText(body, "InitialTerminationTime"), defaultEventTTL)
		sub, err := s.events.create(ttl)
		if err != nil {
			s.fault(w, "ter:Action", err.Error())
			return
		}
		now := time.Now().UTC()
		s.eventSOAP(w, fmt.Sprintf(`<tev:CreatePullPointSubscriptionResponse><tev:SubscriptionReference><wsa5:Address>%s</wsa5:Address></tev:SubscriptionReference><wsnt:CurrentTime>%s</wsnt:CurrentTime><wsnt:TerminationTime>%s</wsnt:TerminationTime></tev:CreatePullPointSubscriptionResponse>`, xmlEsc(s.pullPointURL(sub.token)), now.Format(time.RFC3339Nano), sub.expires.Format(time.RFC3339Nano)))
	default:
		s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF event action: "+action)
	}
}

func (s *Server) pullPointService(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAuthorizedSOAP(w, r)
	if !ok {
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/onvif/pullpoint/")
	if token == "" || strings.Contains(token, "/") {
		s.fault(w, "ter:InvalidArgVal", "Invalid pull-point subscription")
		return
	}

	switch action := soapAction(body); action {
	case "PullMessages":
		timeout := parsePullTimeout(elementText(body, "Timeout"))
		limit := parseMessageLimit(elementText(body, "MessageLimit"))
		messages, expires, ok := s.events.pull(r.Context(), token, timeout, limit)
		if !ok {
			s.fault(w, "ter:InvalidArgVal", "Pull-point subscription does not exist or expired")
			return
		}
		var notifications strings.Builder
		for _, event := range messages {
			notifications.WriteString(s.notificationXML(event))
		}
		now := time.Now().UTC()
		s.eventSOAP(w, fmt.Sprintf(`<tev:PullMessagesResponse><tev:CurrentTime>%s</tev:CurrentTime><tev:TerminationTime>%s</tev:TerminationTime>%s</tev:PullMessagesResponse>`, now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano), notifications.String()))

	case "SetSynchronizationPoint":
		if !s.events.synchronizationPoint(token) {
			s.fault(w, "ter:InvalidArgVal", "Pull-point subscription does not exist or expired")
			return
		}
		s.eventSOAP(w, "<tev:SetSynchronizationPointResponse/>")

	case "Renew":
		ttl := parseTerminationDuration(elementText(body, "TerminationTime"), defaultEventTTL)
		expires, ok := s.events.renew(token, ttl)
		if !ok {
			s.fault(w, "ter:InvalidArgVal", "Pull-point subscription does not exist or expired")
			return
		}
		s.eventSOAP(w, fmt.Sprintf(`<wsnt:RenewResponse><wsnt:TerminationTime>%s</wsnt:TerminationTime><wsnt:CurrentTime>%s</wsnt:CurrentTime></wsnt:RenewResponse>`, expires.Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)))

	case "Unsubscribe":
		if !s.events.unsubscribe(token) {
			s.fault(w, "ter:InvalidArgVal", "Pull-point subscription does not exist or expired")
			return
		}
		s.eventSOAP(w, "<wsnt:UnsubscribeResponse/>")

	default:
		s.fault(w, "ter:ActionNotSupported", "Unsupported pull-point action: "+action)
	}
}

func (s *Server) eventSOAP(w http.ResponseWriter, inner string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tev="http://www.onvif.org/ver10/events/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema" xmlns:tns1="http://www.onvif.org/ver10/topics" xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2" xmlns:wstop="http://docs.oasis-open.org/wsn/t-1" xmlns:wsa5="http://www.w3.org/2005/08/addressing" xmlns:xs="http://www.w3.org/2001/XMLSchema"><s:Body>%s</s:Body></s:Envelope>`, inner)
}

func (s *Server) notificationXML(event eventNotification) string {
	state := "false"
	if event.videoLoss {
		state = "true"
	}
	return fmt.Sprintf(`<wsnt:NotificationMessage><wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:VideoSource/VideoLoss</wsnt:Topic><wsnt:ProducerReference><wsa5:Address>%s</wsa5:Address></wsnt:ProducerReference><wsnt:Message><tt:Message UtcTime="%s" PropertyOperation="%s"><tt:Source><tt:SimpleItem Name="VideoSourceConfigurationToken" Value="screen_source"/></tt:Source><tt:Data><tt:SimpleItem Name="State" Value="%s"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage>`, xmlEsc(s.EventURL()), event.when.UTC().Format(time.RFC3339Nano), xmlEsc(event.operation), state)
}

func parsePullTimeout(value string) time.Duration {
	d := parseISODuration(value)
	if d <= 0 {
		return defaultPullWait
	}
	if d > maxPullTimeout {
		return maxPullTimeout
	}
	return d
}

func parseMessageLimit(value string) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 {
		return defaultPullLimit
	}
	if n > 100 {
		return 100
	}
	return n
}

func parseTerminationDuration(value string, fallback time.Duration) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if d := parseISODuration(value); d > 0 {
		if d > maxEventTTL {
			return maxEventTTL
		}
		return d
	}
	if absolute, err := time.Parse(time.RFC3339, value); err == nil {
		d := time.Until(absolute)
		if d > 0 {
			if d > maxEventTTL {
				return maxEventTTL
			}
			return d
		}
	}
	return fallback
}

func parseISODuration(value string) time.Duration {
	value = strings.TrimSpace(value)
	match := isoDurationRE.FindStringSubmatch(value)
	if match == nil {
		return 0
	}
	var total time.Duration
	if match[1] != "" {
		hours, _ := strconv.Atoi(match[1])
		total += time.Duration(hours) * time.Hour
	}
	if match[2] != "" {
		minutes, _ := strconv.Atoi(match[2])
		total += time.Duration(minutes) * time.Minute
	}
	if match[3] != "" {
		seconds, _ := strconv.ParseFloat(match[3], 64)
		total += time.Duration(seconds * float64(time.Second))
	}
	return total
}
