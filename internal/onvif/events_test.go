package onvif

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestEventPropertiesAdvertisePullPointAndVideoLoss(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/events_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tev:GetEventProperties xmlns:tev="http://www.onvif.org/ver10/events/wsdl"/></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.eventsService(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, "tns1:VideoSource") || !strings.Contains(got, "VideoLoss") {
		t.Fatalf("unexpected event properties code=%d body=%s", rr.Code, got)
	}

	capReq := httptest.NewRequest(http.MethodPost, "/onvif/events_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tev:GetServiceCapabilities xmlns:tev="http://www.onvif.org/ver10/events/wsdl"/></s:Body></s:Envelope>`))
	capRR := httptest.NewRecorder()
	s.eventsService(capRR, capReq)
	capBody, _ := io.ReadAll(capRR.Result().Body)
	if capRR.Code != http.StatusOK || !strings.Contains(string(capBody), `WSPullPointSupport="true"`) {
		t.Fatalf("pull-point capability missing code=%d body=%s", capRR.Code, string(capBody))
	}
}

func TestPullPointSynchronizationReturnsVideoLossProperty(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return false })
	createReq := httptest.NewRequest(http.MethodPost, "/onvif/events_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tev:CreatePullPointSubscription xmlns:tev="http://www.onvif.org/ver10/events/wsdl"><tev:InitialTerminationTime>PT2M</tev:InitialTerminationTime></tev:CreatePullPointSubscription></s:Body></s:Envelope>`))
	createRR := httptest.NewRecorder()
	s.eventsService(createRR, createReq)
	createBody, _ := io.ReadAll(createRR.Result().Body)
	got := string(createBody)
	if createRR.Code != http.StatusOK {
		t.Fatalf("create pull point failed code=%d body=%s", createRR.Code, got)
	}
	match := regexp.MustCompile(`/onvif/pullpoint/([a-f0-9]+)`).FindStringSubmatch(got)
	if len(match) != 2 {
		t.Fatalf("pull-point URL missing: %s", got)
	}
	token := match[1]
	path := "/onvif/pullpoint/" + token

	syncReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tev:SetSynchronizationPoint xmlns:tev="http://www.onvif.org/ver10/events/wsdl"/></s:Body></s:Envelope>`))
	syncRR := httptest.NewRecorder()
	s.pullPointService(syncRR, syncReq)
	if syncRR.Code != http.StatusOK {
		body, _ := io.ReadAll(syncRR.Result().Body)
		t.Fatalf("sync point failed code=%d body=%s", syncRR.Code, string(body))
	}

	pullReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tev:PullMessages xmlns:tev="http://www.onvif.org/ver10/events/wsdl"><tev:Timeout>PT0.1S</tev:Timeout><tev:MessageLimit>10</tev:MessageLimit></tev:PullMessages></s:Body></s:Envelope>`))
	pullRR := httptest.NewRecorder()
	s.pullPointService(pullRR, pullReq)
	pullBody, _ := io.ReadAll(pullRR.Result().Body)
	pulled := string(pullBody)
	if pullRR.Code != http.StatusOK || !strings.Contains(pulled, "VideoLoss") || !strings.Contains(pulled, `PropertyOperation="Initialized"`) || !strings.Contains(pulled, `Name="State" Value="true"`) {
		t.Fatalf("unexpected synchronized event code=%d body=%s", pullRR.Code, pulled)
	}
}

func TestISODurationParsing(t *testing.T) {
	if got := parseISODuration("PT1M30S"); got.Seconds() != 90 {
		t.Fatalf("expected 90 seconds, got %v", got)
	}
	if got := parsePullTimeout("PT120S"); got != maxPullTimeout {
		t.Fatalf("expected timeout cap %v, got %v", maxPullTimeout, got)
	}
}
