package onvif

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMedia1ProfileAdvertisesMetadataConfiguration(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, `<tt:MetadataConfiguration token="screen_metadata">`) || !strings.Contains(got, "<tt:Events/>") {
		t.Fatalf("Media1 metadata configuration missing code=%d body=%s", rr.Code, got)
	}
}

func TestMedia2ProfileAdvertisesMetadataConfiguration(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetProfiles xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:Type>All</tr2:Type></tr2:GetProfiles></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, `<tr2:Metadata token="screen_metadata">`) {
		t.Fatalf("Media2 metadata configuration missing code=%d body=%s", rr.Code, got)
	}
}

func TestMedia1MetadataConfigurationOperations(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })

	req := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetMetadataConfigurations xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || !strings.Contains(string(body), `token="screen_metadata"`) {
		t.Fatalf("GetMetadataConfigurations failed code=%d body=%s", rr.Code, string(body))
	}

	optReq := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetMetadataConfigurationOptions xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ConfigurationToken>screen_metadata</trt:ConfigurationToken><trt:ProfileToken>screen_profile</trt:ProfileToken></trt:GetMetadataConfigurationOptions></s:Body></s:Envelope>`))
	optRR := httptest.NewRecorder()
	s.media(optRR, optReq)
	optBody, _ := io.ReadAll(optRR.Result().Body)
	if optRR.Code != http.StatusOK || !strings.Contains(string(optBody), "PTZStatusFilterOptions") {
		t.Fatalf("GetMetadataConfigurationOptions failed code=%d body=%s", optRR.Code, string(optBody))
	}
}

func TestMedia2MetadataConfigurationOperationsAndCapabilities(t *testing.T) {
	s := New(testConfig(), "10.0.0.10", func() bool { return true })

	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetMetadataConfigurations xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:ProfileToken>screen_profile_sub</tr2:ProfileToken></tr2:GetMetadataConfigurations></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || !strings.Contains(string(body), `token="screen_metadata"`) {
		t.Fatalf("Media2 metadata configurations failed code=%d body=%s", rr.Code, string(body))
	}

	capReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetServiceCapabilities xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"/></s:Body></s:Envelope>`))
	capRR := httptest.NewRecorder()
	s.media2(capRR, capReq)
	capBody, _ := io.ReadAll(capRR.Result().Body)
	got := string(capBody)
	if capRR.Code != http.StatusOK || !strings.Contains(got, `ConfigurationsSupported="VideoSource VideoEncoder Metadata"`) || !strings.Contains(got, `MultiTrackStreaming="true"`) {
		t.Fatalf("Media2 metadata capabilities missing code=%d body=%s", capRR.Code, got)
	}
}

func TestMetadataDisabledIsNotAdvertised(t *testing.T) {
	cfg := testConfig()
	cfg.MetadataEnabled = false
	s := New(cfg, "10.0.0.10", func() bool { return true })

	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetServiceCapabilities xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"/></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || strings.Contains(got, "VideoEncoder Metadata") || !strings.Contains(got, `MultiTrackStreaming="false"`) {
		t.Fatalf("metadata-disabled capabilities are incorrect code=%d body=%s", rr.Code, got)
	}
}
