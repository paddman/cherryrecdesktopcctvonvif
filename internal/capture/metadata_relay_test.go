package capture

import (
	"strings"
	"testing"
	"time"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

func TestMetadataRelayUsesRawCapturePathsAndPublicOutputPaths(t *testing.T) {
	c := config.Default()
	c.InsecureNoAuth = true
	m := New(c)

	args := strings.Join(m.ffmpegArgs("libx264"), " ")
	if !strings.Contains(args, "rtsp://127.0.0.1:8554/screen_raw") {
		t.Fatalf("main capture must publish to raw path: %s", args)
	}
	if !strings.Contains(args, "rtsp://127.0.0.1:8554/screen_sub_raw") {
		t.Fatalf("sub capture must publish to raw path: %s", args)
	}

	env := strings.Join(m.mediaMTXEnv(), "\n")
	for _, want := range []string{
		"MTX_PATHS_SCREEN_RAW_SOURCE=publisher",
		"MTX_PATHS_SCREEN_SOURCE=publisher",
		"MTX_PATHS_SCREEN_SUB_RAW_SOURCE=publisher",
		"MTX_PATHS_SCREEN_SUB_SOURCE=publisher",
		"MTX_PATHS_SCREEN_RAW_RECORD=true",
		"MTX_PATHS_SCREEN_RECORD=false",
		"MTX_AUTHINTERNALUSERS_1_PERMISSIONS_0_PATH=screen",
		"MTX_AUTHINTERNALUSERS_1_PERMISSIONS_1_PATH=screen_sub",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("MediaMTX metadata relay environment missing %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "MTX_AUTHINTERNALUSERS_1_PERMISSIONS_0_PATH=screen_raw") {
		t.Fatalf("external reader must not have raw-path access:\n%s", env)
	}
}

func TestMetadataDisabledKeepsLegacyPaths(t *testing.T) {
	c := config.Default()
	c.InsecureNoAuth = true
	c.MetadataEnabled = false
	m := New(c)

	args := strings.Join(m.ffmpegArgs("libx264"), " ")
	if strings.Contains(args, "screen_raw") || strings.Contains(args, "screen_sub_raw") {
		t.Fatalf("metadata-disabled mode must keep legacy public publish paths: %s", args)
	}
	if !strings.Contains(args, "rtsp://127.0.0.1:8554/screen") {
		t.Fatalf("legacy main path missing: %s", args)
	}
}

func TestMetadataRTPPacketUsesONVIFTransportRules(t *testing.T) {
	state := metadataRTPState{ssrc: 0x11223344, sequence: 9}
	now := time.Unix(1700000000, 250000000).UTC()
	payload := []byte("<tt:MetaDataStream/>")

	pkt := state.packet(107, now, payload)
	if pkt.Version != 2 || pkt.PayloadType != 107 || !pkt.Marker {
		t.Fatalf("unexpected RTP header: %+v", pkt.Header)
	}
	if pkt.SSRC != 0x11223344 || pkt.SequenceNumber != 10 {
		t.Fatalf("unexpected RTP identity: %+v", pkt.Header)
	}
	wantTimestamp := uint32((uint64(now.UnixNano()) * metadataClockRate) / uint64(time.Second))
	if pkt.Timestamp != wantTimestamp {
		t.Fatalf("unexpected 90kHz timestamp got=%d want=%d", pkt.Timestamp, wantTimestamp)
	}
	if string(pkt.Payload) != string(payload) {
		t.Fatalf("unexpected RTP payload: %q", string(pkt.Payload))
	}
}

func TestMetadataDocumentEncodesPropertyAndHeartbeat(t *testing.T) {
	now := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
	initialized := string(metadataDocument(now, true, "Initialized", "SERIAL<&"))
	for _, want := range []string{
		"<tt:MetaDataStream",
		"<tt:Event>",
		"tns1:VideoSource/VideoLoss",
		`PropertyOperation="Initialized"`,
		`Name="State" Value="true"`,
		"SERIAL&lt;&amp;",
	} {
		if !strings.Contains(initialized, want) {
			t.Fatalf("initialized metadata missing %q: %s", want, initialized)
		}
	}

	heartbeat := string(metadataDocument(now, false, "", "SERIAL"))
	if !strings.Contains(heartbeat, "<tt:MetaDataStream") || strings.Contains(heartbeat, "<tt:Event>") {
		t.Fatalf("heartbeat must be a closed empty metadata document: %s", heartbeat)
	}
	if !strings.HasSuffix(heartbeat, "/>") && !strings.HasSuffix(heartbeat, "</tt:MetaDataStream>") {
		t.Fatalf("metadata heartbeat must close the XML document: %s", heartbeat)
	}
}
