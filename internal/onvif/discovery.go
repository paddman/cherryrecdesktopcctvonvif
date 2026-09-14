package onvif

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

var messageIDPattern = regexp.MustCompile(`<[^>]*MessageID[^>]*>([^<]+)</[^>]*MessageID>`)

type discoveryState struct {
	instanceID uint64
	sequence   atomic.Uint64
}

func (s *Server) RunDiscovery(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:3702")
	if err != nil {
		return err
	}
	iface := interfaceForIP(s.ip)
	conn, err := net.ListenMulticastUDP("udp4", iface, addr)
	if err != nil {
		return fmt.Errorf("listen WS-Discovery multicast: %w", err)
	}
	defer conn.Close()
	_ = conn.SetReadBuffer(256 * 1024)
	state := discoveryState{instanceID: uint64(time.Now().Unix())}
	log.Printf("ONVIF WS-Discovery listening on 239.255.255.250:3702 interface=%s", interfaceName(iface))

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	buf := make([]byte, 64*1024)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		msg := string(buf[:n])
		relatesTo := ""
		if m := messageIDPattern.FindStringSubmatch(msg); len(m) == 2 {
			relatesTo = m[1]
		}
		var response string
		switch {
		case strings.Contains(msg, "Probe"):
			response = s.probeMatches(relatesTo, state.instanceID, state.sequence.Add(1))
		case strings.Contains(msg, "Resolve") && strings.Contains(msg, s.uuid):
			response = s.resolveMatches(relatesTo, state.instanceID, state.sequence.Add(1))
		default:
			continue
		}
		if _, err := conn.WriteToUDP([]byte(response), remote); err != nil {
			log.Printf("WS-Discovery response error remote=%s err=%v", remote, err)
		}
	}
}

func (s *Server) probeMatches(relatesTo string, instanceID, messageNumber uint64) string {
	return s.discoveryEnvelope("ProbeMatches", relatesTo, instanceID, messageNumber,
		`<d:ProbeMatches><d:ProbeMatch>`+s.discoveryMatch()+`</d:ProbeMatch></d:ProbeMatches>`)
}

func (s *Server) resolveMatches(relatesTo string, instanceID, messageNumber uint64) string {
	return s.discoveryEnvelope("ResolveMatches", relatesTo, instanceID, messageNumber,
		`<d:ResolveMatches><d:ResolveMatch>`+s.discoveryMatch()+`</d:ResolveMatch></d:ResolveMatches>`)
}

func (s *Server) discoveryEnvelope(action, relatesTo string, instanceID, messageNumber uint64, body string) string {
	rel := ""
	if relatesTo != "" {
		rel = `<a:RelatesTo>` + xmlEsc(relatesTo) + `</a:RelatesTo>`
	}
	messageID := fmt.Sprintf("urn:uuid:%s-%d", s.uuid, messageNumber)
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">` +
		`<e:Header><a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/` + action + `</a:Action><a:MessageID>` + messageID + `</a:MessageID>` + rel +
		`<a:To>http://www.w3.org/2005/08/addressing/anonymous</a:To><d:AppSequence InstanceId="` + fmt.Sprint(instanceID) + `" MessageNumber="` + fmt.Sprint(messageNumber) + `"/></e:Header>` +
		`<e:Body>` + body + `</e:Body></e:Envelope>`
}

func (s *Server) discoveryMatch() string {
	name := strings.ReplaceAll(s.cfg.DeviceName, " ", "_")
	return `<a:EndpointReference><a:Address>urn:uuid:` + s.uuid + `</a:Address></a:EndpointReference>` +
		`<d:Types>dn:NetworkVideoTransmitter</d:Types>` +
		`<d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/name/` + xmlEsc(name) + `</d:Scopes>` +
		`<d:XAddrs>` + xmlEsc(s.DeviceURL()) + `</d:XAddrs><d:MetadataVersion>1</d:MetadataVersion>`
}

func stableUUID(seed string) string {
	sum := sha256.Sum256([]byte("cherry-desktop-cctv:" + seed))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func interfaceForIP(ip string) *net.Interface {
	target := net.ParseIP(ip)
	if target == nil {
		return nil
	}
	ifaces, _ := net.Interfaces()
	for i := range ifaces {
		addrs, _ := ifaces[i].Addrs()
		for _, addr := range addrs {
			var candidate net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				candidate = v.IP
			case *net.IPAddr:
				candidate = v.IP
			}
			if candidate != nil && candidate.Equal(target) {
				return &ifaces[i]
			}
		}
	}
	return nil
}

func interfaceName(iface *net.Interface) string {
	if iface == nil {
		return "auto"
	}
	return iface.Name
}
