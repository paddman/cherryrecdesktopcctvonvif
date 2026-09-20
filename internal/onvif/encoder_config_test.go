package onvif

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

type fakeVideoController struct {
	cfg     config.Config
	persist bool
	updates int
}

func (f *fakeVideoController) CurrentConfig() config.Config { return f.cfg }
func (f *fakeVideoController) UpdateConfig(next config.Config, persist bool) error {
	f.cfg = next
	f.persist = persist
	f.updates++
	return nil
}

func TestSetVideoEncoderConfigurationMedia1AppliesRuntimeConfig(t *testing.T) {
	cfg := testConfig()
	controller := &fakeVideoController{cfg: cfg}
	s := New(cfg, "10.0.0.10", func() bool { return true })
	s.SetVideoConfigController(controller)

	body := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body><trt:SetVideoEncoderConfiguration><trt:Configuration token="screen_encoder"><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>1280</tt:Width><tt:Height>720</tt:Height></tt:Resolution><tt:RateControl><tt:FrameRateLimit>20</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval><tt:BitrateLimit>2500</tt:BitrateLimit></tt:RateControl><tt:H264><tt:GovLength>40</tt:GovLength></tt:H264></trt:Configuration><trt:ForcePersistence>true</trt:ForcePersistence></trt:SetVideoEncoderConfiguration></s:Body></s:Envelope>`
	req := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.media(rr, req)
	if rr.Code != http.StatusOK {
		resp, _ := io.ReadAll(rr.Result().Body)
		t.Fatalf("set encoder failed code=%d body=%s", rr.Code, string(resp))
	}
	if controller.updates != 1 || !controller.persist {
		t.Fatalf("update was not persisted: %+v", controller)
	}
	if controller.cfg.Width != 1280 || controller.cfg.Height != 720 || controller.cfg.FPS != 20 || controller.cfg.VideoBitrate != "2500k" || controller.cfg.GOPSeconds != 2 {
		t.Fatalf("unexpected runtime config: %+v", controller.cfg)
	}

	getReq := httptest.NewRequest(http.MethodPost, "/onvif/media_service", strings.NewReader(`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><trt:GetVideoEncoderConfiguration xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ConfigurationToken>screen_encoder</trt:ConfigurationToken></trt:GetVideoEncoderConfiguration></s:Body></s:Envelope>`))
	getRR := httptest.NewRecorder()
	s.media(getRR, getReq)
	resp, _ := io.ReadAll(getRR.Result().Body)
	got := string(resp)
	for _, want := range []string{"<tt:Width>1280</tt:Width>", "<tt:Height>720</tt:Height>", "<tt:FrameRateLimit>20</tt:FrameRateLimit>", "<tt:BitrateLimit>2500</tt:BitrateLimit>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("updated configuration missing %q: %s", want, got)
		}
	}
}

func TestSetVideoEncoderConfigurationMedia2Substream(t *testing.T) {
	cfg := testConfig()
	controller := &fakeVideoController{cfg: cfg}
	s := New(cfg, "10.0.0.10", func() bool { return true })
	s.SetVideoConfigController(controller)

	body := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tr2="http://www.onvif.org/ver20/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body><tr2:SetVideoEncoderConfiguration><tr2:Configuration token="screen_encoder_sub"><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>960</tt:Width><tt:Height>540</tt:Height></tt:Resolution><tt:RateControl><tt:FrameRateLimit>12</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval><tt:BitrateLimit>1200</tt:BitrateLimit></tt:RateControl><tt:GovLength>24</tt:GovLength></tr2:Configuration><tr2:ForcePersistence>false</tr2:ForcePersistence></tr2:SetVideoEncoderConfiguration></s:Body></s:Envelope>`
	req := httptest.NewRequest(http.MethodPost, "/onvif/media2_service", strings.NewReader(body))
	rr := httptest.NewRecorder()
	s.media2(rr, req)
	if rr.Code != http.StatusOK {
		resp, _ := io.ReadAll(rr.Result().Body)
		t.Fatalf("set Media2 encoder failed code=%d body=%s", rr.Code, string(resp))
	}
	if controller.persist {
		t.Fatal("ForcePersistence=false must not persist")
	}
	if controller.cfg.SubstreamWidth != 960 || controller.cfg.SubstreamHeight != 540 || controller.cfg.SubstreamFPS != 12 || controller.cfg.SubstreamBitrate != "1200k" {
		t.Fatalf("unexpected substream config: %+v", controller.cfg)
	}
}
