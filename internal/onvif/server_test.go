package onvif

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
