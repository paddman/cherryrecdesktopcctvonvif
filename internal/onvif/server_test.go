package onvif

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

func testConfig() config.Config {
	c := config.Default()
	c.InsecureNoAuth = true
	c.Width = 2560
	c.Height = 1440
	c.FPS = 30
	c.VideoBitrate = "8m"
	return c
}

func TestGetProfilesUsesConfiguredGeometry(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, "<tt:Width>2560</tt:Width>") || !strings.Contains(got, "<tt:BitrateLimit>8000</tt:BitrateLimit>") {
		t.Fatalf("unexpected response code=%d body=%s", rr.Code, got)
	}
}

func TestReadyEndpointSemantics(t *testing.T) {
	s := New(testConfig(), "127.0.0.1", func() bool { return false })
	if s.ready() {
		t.Fatal("ready callback should report false")
	}
}

func TestStableUUID(t *testing.T) {
	a := stableUUID("SERIAL-1")
	b := stableUUID("SERIAL-1")
	c := stableUUID("SERIAL-2")
	if a != b || a == c || len(a) != 36 {
		t.Fatalf("bad stable UUIDs: %q %q %q", a, b, c)
	}
}

func TestSOAPAction(t *testing.T) {
	b := []byte(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Header><x:GetProfiles/></s:Header><s:Body><trt:GetStreamUri/></s:Body></s:Envelope>`)
	if got := soapAction(b); got != "GetStreamUri" {
		t.Fatalf("got %q", got)
	}
}

func TestGetProfilesIncludesSubstream(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, `token="screen_profile_sub"`) || !strings.Contains(got, "<tt:Width>640</tt:Width>") {
		t.Fatalf("substream profile missing code=%d body=%s", rr.Code, got)
	}
}

func TestSubstreamProfileReturnsSubstreamURI(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ProfileToken>screen_profile_sub</trt:ProfileToken></trt:GetStreamUri></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || !strings.Contains(string(body), "rtsp://10.0.0.10:8554/screen_sub") {
		t.Fatalf("unexpected substream URI code=%d body=%s", rr.Code, string(body))
	}
}

func TestMedia2ProfilesAndStreamURI(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	profilesReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetProfiles xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:Type>All</tr2:Type></tr2:GetProfiles></s:Body></s:Envelope>`))
	profilesRR := httptest.NewRecorder()
	s.media2(profilesRR, profilesReq)
	profilesBody, _ := io.ReadAll(profilesRR.Result().Body)
	if profilesRR.Code != http.StatusOK || !strings.Contains(string(profilesBody), "http://www.onvif.org/ver20/media/wsdl") || !strings.Contains(string(profilesBody), `token="screen_profile_sub"`) {
		t.Fatalf("unexpected Media2 profiles code=%d body=%s", profilesRR.Code, string(profilesBody))
	}

	streamReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetStreamUri xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:Protocol>RTSP</tr2:Protocol><tr2:ProfileToken>screen_profile_sub</tr2:ProfileToken></tr2:GetStreamUri></s:Body></s:Envelope>`))
	streamRR := httptest.NewRecorder()
	s.media2(streamRR, streamReq)
	streamBody, _ := io.ReadAll(streamRR.Result().Body)
	if streamRR.Code != http.StatusOK || !strings.Contains(string(streamBody), "rtsp://10.0.0.10:8554/screen_sub") {
		t.Fatalf("unexpected Media2 stream URI code=%d body=%s", streamRR.Code, string(streamBody))
	}
}

func TestSnapshotServesCachedJPEG(t *testing.T) {
	s := New(testConfig(), "127.0.0.1", func() bool { return true })
	s.snapshotJPEG = []byte{0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9}
	s.snapshotUpdated = time.Now()
	req := httptest.NewRequest(http.MethodGet, "/snapshot.jpg", nil)
	rr := httptest.NewRecorder()
	s.snapshot(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/jpeg" || len(body) != 6 {
		t.Fatalf("unexpected cached snapshot code=%d len=%d", rr.Code, len(body))
	}
}

func TestReadJPEGFrame(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader(string([]byte{0x00, 0xff, 0xd8, 0x11, 0x22, 0xff, 0xd9, 0x33})))
	frame, err := readJPEGFrame(reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(frame) != 6 || frame[0] != 0xff || frame[1] != 0xd8 || frame[4] != 0xff || frame[5] != 0xd9 {
		t.Fatalf("bad JPEG frame: %v", frame)
	}
}

func TestMedia2VideoSourceConfigurations(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetVideoSourceConfigurations xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:ProfileToken>screen_profile_sub</tr2:ProfileToken></tr2:GetVideoSourceConfigurations></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, `token="screen_source"`) || !strings.Contains(got, "<tt:UseCount>2</tt:UseCount>") {
		t.Fatalf("unexpected Media2 video source configuration code=%d body=%s", rr.Code, got)
	}
}

func TestMedia2EncoderInstancesSchema(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetVideoEncoderInstances xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:ConfigurationToken>screen_source</tr2:ConfigurationToken></tr2:GetVideoEncoderInstances></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, "<tr2:Encoding>H264</tr2:Encoding>") || !strings.Contains(got, "<tr2:Number>2</tr2:Number>") || !strings.Contains(got, "<tr2:Total>2</tr2:Total>") {
		t.Fatalf("unexpected Media2 encoder instances code=%d body=%s", rr.Code, got)
	}
}
