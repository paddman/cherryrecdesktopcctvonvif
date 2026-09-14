package onvif

import (
    "fmt"
    "io"
    "log"
    "net/http"
    "strings"
    "time"

    "github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

type Server struct {
    cfg config.Config
    ip  string
}

func New(cfg config.Config, ip string) *Server { return &Server{cfg: cfg, ip: ip} }

func (s *Server) Addr() string { return fmt.Sprintf(":%d", s.cfg.ONVIFPort) }
func (s *Server) DeviceURL() string { return fmt.Sprintf("http://%s:%d/onvif/device_service", s.ip, s.cfg.ONVIFPort) }
func (s *Server) MediaURL() string { return fmt.Sprintf("http://%s:%d/onvif/media_service", s.ip, s.cfg.ONVIFPort) }
func (s *Server) StreamURL() string { return fmt.Sprintf("rtsp://%s:%d/%s", s.ip, s.cfg.RTSPPort, s.cfg.RTSPPath) }

func (s *Server) Run() error {
    mux := http.NewServeMux()
    mux.HandleFunc("/onvif/device_service", s.device)
    mux.HandleFunc("/onvif/media_service", s.media)
    mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK); _, _ = w.Write([]byte("ok")) })
    log.Printf("ONVIF HTTP service listening on %s", s.Addr())
    return http.ListenAndServe(s.Addr(), mux)
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
    q := string(body)
    switch {
    case strings.Contains(q, "GetDeviceInformation"):
        s.soap(w, fmt.Sprintf(`<tds:GetDeviceInformationResponse><tds:Manufacturer>%s</tds:Manufacturer><tds:Model>%s</tds:Model><tds:FirmwareVersion>%s</tds:FirmwareVersion><tds:SerialNumber>%s</tds:SerialNumber><tds:HardwareId>desktop-screen</tds:HardwareId></tds:GetDeviceInformationResponse>`, xmlEsc(s.cfg.Manufacturer), xmlEsc(s.cfg.Model), xmlEsc(s.cfg.FirmwareVersion), xmlEsc(s.cfg.SerialNumber)))
    case strings.Contains(q, "GetSystemDateAndTime"):
        now := time.Now().UTC()
        s.soap(w, fmt.Sprintf(`<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime><tt:DateTimeType>NTP</tt:DateTimeType><tt:DaylightSavings>false</tt:DaylightSavings><tt:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time><tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tt:UTCDateTime></tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>`, now.Hour(), now.Minute(), now.Second(), now.Year(), int(now.Month()), now.Day()))
    case strings.Contains(q, "GetCapabilities"):
        s.soap(w, fmt.Sprintf(`<tds:GetCapabilitiesResponse><tds:Capabilities><tt:Device><tt:XAddr>%s</tt:XAddr></tt:Device><tt:Media><tt:XAddr>%s</tt:XAddr><tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast><tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media></tds:Capabilities></tds:GetCapabilitiesResponse>`, s.DeviceURL(), s.MediaURL()))
    case strings.Contains(q, "GetServices"):
        s.soap(w, fmt.Sprintf(`<tds:GetServicesResponse><tds:Service><tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service><tds:Service><tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service></tds:GetServicesResponse>`, s.DeviceURL(), s.MediaURL()))
    default:
        s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF device action")
    }
}

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
    body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
    q := string(body)
    switch {
    case strings.Contains(q, "GetProfiles"):
        s.soap(w, `<trt:GetProfilesResponse><trt:Profiles fixed="true" token="screen_profile"><tt:Name>Desktop Screen</tt:Name><tt:VideoSourceConfiguration token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>1</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="0" y="0" width="1920" height="1080"/></tt:VideoSourceConfiguration><tt:VideoEncoderConfiguration token="screen_encoder"><tt:Name>H264 Desktop</tt:Name><tt:UseCount>1</tt:UseCount><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>1920</tt:Width><tt:Height>1080</tt:Height></tt:Resolution><tt:Quality>5</tt:Quality><tt:RateControl><tt:FrameRateLimit>15</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval><tt:BitrateLimit>4000</tt:BitrateLimit></tt:RateControl><tt:H264><tt:GovLength>30</tt:GovLength><tt:H264Profile>High</tt:H264Profile></tt:H264><tt:Multicast><tt:Address><tt:Type>IPv4</tt:Type><tt:IPv4Address>0.0.0.0</tt:IPv4Address></tt:Address><tt:Port>0</tt:Port><tt:TTL>1</tt:TTL><tt:AutoStart>false</tt:AutoStart></tt:Multicast><tt:SessionTimeout>PT60S</tt:SessionTimeout></tt:VideoEncoderConfiguration></trt:Profiles></trt:GetProfilesResponse>`)
    case strings.Contains(q, "GetProfile"):
        s.soap(w, `<trt:GetProfileResponse><trt:Profile fixed="true" token="screen_profile"><tt:Name>Desktop Screen</tt:Name></trt:Profile></trt:GetProfileResponse>`)
    case strings.Contains(q, "GetStreamUri"):
        s.soap(w, fmt.Sprintf(`<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT60S</tt:Timeout></trt:MediaUri></trt:GetStreamUriResponse>`, s.StreamURL()))
    default:
        s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF media action")
    }
}

func (s *Server) soap(w http.ResponseWriter, inner string) {
    w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
    _, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>%s</s:Body></s:Envelope>`, inner)
}

func (s *Server) fault(w http.ResponseWriter, code, reason string) {
    w.WriteHeader(http.StatusInternalServerError)
    s.soap(w, fmt.Sprintf(`<s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>%s</s:Value></s:Subcode></s:Code><s:Reason><s:Text xml:lang="en">%s</s:Text></s:Reason></s:Fault>`, code, xmlEsc(reason)))
}

func xmlEsc(v string) string {
    r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
    return r.Replace(v)
}
