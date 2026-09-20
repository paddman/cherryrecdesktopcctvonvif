package capture

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	gortspliburl "github.com/bluenviron/gortsplib/v4/pkg/url"
	"github.com/pion/rtp"
)

const metadataClockRate = 90000

type metadataRTPState struct {
	ssrc     uint32
	sequence uint16
}

func newMetadataRTPState() (metadataRTPState, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return metadataRTPState{}, err
	}
	return metadataRTPState{
		ssrc:     binary.BigEndian.Uint32(raw[:4]),
		sequence: binary.BigEndian.Uint16(raw[4:]),
	}, nil
}

func (s *metadataRTPState) packet(payloadType uint8, now time.Time, payload []byte) *rtp.Packet {
	s.sequence++
	timestamp := uint32((uint64(now.UnixNano()) * metadataClockRate) / uint64(time.Second))
	return &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    payloadType,
			SequenceNumber: s.sequence,
			Timestamp:      timestamp,
			SSRC:           s.ssrc,
			Marker:         true,
		},
		Payload: payload,
	}
}

func newMetadataMedia(payloadType uint8, control string) *description.Media {
	return &description.Media{
		Type:    description.MediaTypeApplication,
		Control: control,
		Formats: []format.Format{&format.Generic{
			PayloadTyp: payloadType,
			RTPMa:      "vnd.onvif.metadata/90000",
			ClockRat:   metadataClockRate,
		}},
	}
}

func (m *Manager) superviseMetadataRelay(ctx context.Context, sourcePath, destinationPath string, primary bool) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := m.runMetadataRelay(ctx, sourcePath, destinationPath, primary)
		if primary {
			m.ready.Store(false)
		}
		if ctx.Err() != nil {
			return
		}
		log.Printf("metadata relay path=%s source=%s exited: %v", destinationPath, sourcePath, err)
		if !sleepContext(ctx, backoff) {
			return
		}
		backoff = minDuration(backoff*2, 30*time.Second)
	}
}

func (m *Manager) runMetadataRelay(ctx context.Context, sourcePath, destinationPath string, primary bool) error {
	sourceURL, err := gortspliburl.Parse(fmt.Sprintf("rtsp://127.0.0.1:%d/%s", m.cfg.RTSPPort, sourcePath))
	if err != nil {
		return err
	}
	destinationURL, err := gortspliburl.Parse(fmt.Sprintf("rtsp://127.0.0.1:%d/%s", m.cfg.RTSPPort, destinationPath))
	if err != nil {
		return err
	}

	readerTransport := gortsplib.TransportTCP
	reader := &gortsplib.Client{Transport: &readerTransport}
	if err := reader.Start(sourceURL.Scheme, sourceURL.Host); err != nil {
		return fmt.Errorf("source connect: %w", err)
	}
	defer reader.Close()

	desc, _, err := reader.Describe(sourceURL)
	if err != nil {
		return fmt.Errorf("source describe: %w", err)
	}
	if len(desc.Medias) == 0 {
		return fmt.Errorf("source has no media")
	}
	if err := reader.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		return fmt.Errorf("source setup: %w", err)
	}

	payloadType := uint8(m.cfg.MetadataPayloadType)
	for _, media := range desc.Medias {
		for _, forma := range media.Formats {
			if forma.PayloadType() == payloadType {
				return fmt.Errorf("metadata payload type %d conflicts with source format", payloadType)
			}
		}
	}

	for i, media := range desc.Medias {
		media.Control = fmt.Sprintf("trackID=%d", i)
	}
	metadataMedia := newMetadataMedia(payloadType, fmt.Sprintf("trackID=%d", len(desc.Medias)))
	desc.Medias = append(desc.Medias, metadataMedia)

	publisherTransport := gortsplib.TransportTCP
	publisher := &gortsplib.Client{Transport: &publisherTransport}
	if err := publisher.Start(destinationURL.Scheme, destinationURL.Host); err != nil {
		return fmt.Errorf("destination connect: %w", err)
	}
	defer publisher.Close()

	if _, err := publisher.Announce(destinationURL, desc); err != nil {
		return fmt.Errorf("destination announce: %w", err)
	}
	if err := publisher.SetupAll(destinationURL, desc.Medias); err != nil {
		return fmt.Errorf("destination setup: %w", err)
	}
	if _, err := publisher.Record(); err != nil {
		return fmt.Errorf("destination record: %w", err)
	}

	var writeMu sync.Mutex
	errCh := make(chan error, 2)
	reportWriteError := func(err error) {
		if err == nil {
			return
		}
		select {
		case errCh <- err:
		default:
		}
	}

	reader.OnPacketRTPAny(func(media *description.Media, _ format.Format, packet *rtp.Packet) {
		writeMu.Lock()
		err := publisher.WritePacketRTP(media, packet)
		writeMu.Unlock()
		reportWriteError(err)
	})

	if _, err := reader.Play(nil); err != nil {
		return fmt.Errorf("source play: %w", err)
	}

	if primary {
		m.ready.Store(true)
		defer m.ready.Store(false)
	}
	log.Printf("metadata relay online source=%s destination=%s payload=%d clock=%d",
		sourcePath, destinationPath, payloadType, metadataClockRate)

	go func() {
		errCh <- reader.Wait()
	}()

	state, err := newMetadataRTPState()
	if err != nil {
		return fmt.Errorf("metadata RTP state: %w", err)
	}
	interval := time.Duration(m.cfg.MetadataIntervalMS) * time.Millisecond
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	first := true
	lastVideoLoss := !m.sourceReady.Load()
	sendMetadata := func(now time.Time) error {
		videoLoss := !m.sourceReady.Load()
		operation := ""
		if first {
			operation = "Initialized"
			first = false
		} else if videoLoss != lastVideoLoss {
			operation = "Changed"
		}
		lastVideoLoss = videoLoss

		payload := metadataDocument(now, videoLoss, operation, m.cfg.SerialNumber)
		if len(payload) > 1200 {
			return fmt.Errorf("metadata XML document too large for single RTP packet: %d bytes", len(payload))
		}
		packet := state.packet(payloadType, now, payload)
		writeMu.Lock()
		err := publisher.WritePacketRTP(metadataMedia, packet)
		writeMu.Unlock()
		return err
	}

	if err := sendMetadata(time.Now().UTC()); err != nil {
		return fmt.Errorf("initial metadata packet: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errCh:
			now := time.Now().UTC()
			lossPayload := metadataDocument(now, true, "Changed", m.cfg.SerialNumber)
			lossPacket := state.packet(payloadType, now, lossPayload)
			writeMu.Lock()
			_ = publisher.WritePacketRTP(metadataMedia, lossPacket)
			writeMu.Unlock()
			if err == nil {
				return fmt.Errorf("RTSP relay stopped")
			}
			return err
		case now := <-ticker.C:
			if err := sendMetadata(now.UTC()); err != nil {
				return fmt.Errorf("metadata packet: %w", err)
			}
		}
	}
}

func metadataDocument(now time.Time, videoLoss bool, operation, serial string) []byte {
	if operation == "" {
		return []byte(`<?xml version="1.0" encoding="UTF-8"?><tt:MetaDataStream xmlns:tt="http://www.onvif.org/ver10/schema"/>`)
	}
	state := "false"
	if videoLoss {
		state = "true"
	}
	producer := "urn:cherry-desktop-cctv:" + xmlEscapeMetadata(serial)
	return []byte(fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?><tt:MetaDataStream xmlns:tt="http://www.onvif.org/ver10/schema" xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2" xmlns:tns1="http://www.onvif.org/ver10/topics" xmlns:wsa5="http://www.w3.org/2005/08/addressing"><tt:Event><wsnt:NotificationMessage><wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:VideoSource/VideoLoss</wsnt:Topic><wsnt:ProducerReference><wsa5:Address>%s</wsa5:Address></wsnt:ProducerReference><wsnt:Message><tt:Message UtcTime="%s" PropertyOperation="%s"><tt:Source><tt:SimpleItem Name="VideoSourceConfigurationToken" Value="screen_source"/></tt:Source><tt:Data><tt:SimpleItem Name="State" Value="%s"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage></tt:Event></tt:MetaDataStream>`,
		producer,
		now.UTC().Format(time.RFC3339Nano),
		xmlEscapeMetadata(operation),
		state,
	))
}

func xmlEscapeMetadata(value string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	).Replace(value)
}
