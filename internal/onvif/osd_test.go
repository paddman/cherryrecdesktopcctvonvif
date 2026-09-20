package onvif

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMedia2OSDLifecyclePersistsText(t *testing.T) {
	cfg := testConfig()
	cfg.OSDTextFile = filepath.Join(t.TempDir(), "osd.txt")
	s := New(cfg, "10.0.0.10", func() bool { return true })

	create := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tr2="http://www.onvif.org/ver20/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body><tr2:CreateOSD><tr2:OSD><tt:VideoSourceConfigurationToken>screen_source</tt:VideoSourceConfigurationToken><tt:Type>Text</tt:Type><tt:Position><tt:Type>UpperLeft</tt:Type></tt:Position><tt:TextString><tt:Type>Plain</tt:Type><tt:FontSize>24</tt:FontSize><tt:PlainText>Cherry CCTV</tt:PlainText></tt:TextString></tr2:OSD></tr2:CreateOSD></s:Body></s:Envelope>`
	createReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(create))
	createRR := httptest.NewRecorder()
	s.media2(createRR, createReq)
	createBody, _ := io.ReadAll(createRR.Result().Body)
	if createRR.Code != http.StatusOK || !strings.Contains(string(createBody), "<tr2:OSDToken>"+osdToken+"</tr2:OSDToken>") {
		t.Fatalf("create OSD failed code=%d body=%s", createRR.Code, string(createBody))
	}
	stored, err := os.ReadFile(cfg.OSDTextFile)
	if err != nil || string(stored) != "Cherry CCTV" {
		t.Fatalf("OSD text not persisted got=%q err=%v", string(stored), err)
	}

	getReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetOSDs xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"/></s:Body></s:Envelope>`))
	getRR := httptest.NewRecorder()
	s.media2(getRR, getReq)
	getBody, _ := io.ReadAll(getRR.Result().Body)
	if getRR.Code != http.StatusOK || !strings.Contains(string(getBody), "Cherry CCTV") || !strings.Contains(string(getBody), `token="`+osdToken+`"`) {
		t.Fatalf("get OSD failed code=%d body=%s", getRR.Code, string(getBody))
	}

	set := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tr2="http://www.onvif.org/ver20/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body><tr2:SetOSD><tr2:OSD token="osd_text_1"><tt:VideoSourceConfigurationToken>screen_source</tt:VideoSourceConfigurationToken><tt:Type>Text</tt:Type><tt:Position><tt:Type>UpperLeft</tt:Type></tt:Position><tt:TextString><tt:Type>Plain</tt:Type><tt:FontSize>24</tt:FontSize><tt:PlainText>CYRVOR</tt:PlainText></tt:TextString></tr2:OSD></tr2:SetOSD></s:Body></s:Envelope>`
	setReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(set))
	setRR := httptest.NewRecorder()
	s.media2(setRR, setReq)
	if setRR.Code != http.StatusOK {
		body, _ := io.ReadAll(setRR.Result().Body)
		t.Fatalf("set OSD failed code=%d body=%s", setRR.Code, string(body))
	}
	stored, err = os.ReadFile(cfg.OSDTextFile)
	if err != nil || string(stored) != "CYRVOR" {
		t.Fatalf("updated OSD text not persisted got=%q err=%v", string(stored), err)
	}

	deleteReq := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:DeleteOSD xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:OSDToken>osd_text_1</tr2:OSDToken></tr2:DeleteOSD></s:Body></s:Envelope>`))
	deleteRR := httptest.NewRecorder()
	s.media2(deleteRR, deleteReq)
	if deleteRR.Code != http.StatusOK {
		body, _ := io.ReadAll(deleteRR.Result().Body)
		t.Fatalf("delete OSD failed code=%d body=%s", deleteRR.Code, string(body))
	}
	stored, err = os.ReadFile(cfg.OSDTextFile)
	if err != nil || len(stored) != 0 {
		t.Fatalf("deleted OSD should leave an empty render file len=%d err=%v", len(stored), err)
	}
}

func TestMedia2OSDOptionsMatchRenderer(t *testing.T) {
	cfg := testConfig()
	cfg.OSDTextFile = filepath.Join(t.TempDir(), "osd.txt")
	s := New(cfg, "10.0.0.10", func() bool { return true })
	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><tr2:GetOSDOptions xmlns:tr2="http://www.onvif.org/ver20/media/wsdl"><tr2:ConfigurationToken>screen_source</tr2:ConfigurationToken></tr2:GetOSDOptions></s:Body></s:Envelope>`))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	got := string(body)
	if rr.Code != http.StatusOK || !strings.Contains(got, `PlainText="1"`) || !strings.Contains(got, "<tt:PositionOption>UpperLeft</tt:PositionOption>") || !strings.Contains(got, "<tt:Min>24</tt:Min><tt:Max>24</tt:Max>") {
		t.Fatalf("unexpected OSD options code=%d body=%s", rr.Code, got)
	}
}
