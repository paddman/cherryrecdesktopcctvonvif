package onvif

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"time"

	cherryAuth "github.com/paddman/cherryrecdesktopcctvonvif/internal/auth"
	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

type Server struct {
	cfg   config.Config
	ip    string
	auth  *cherryAuth.Authenticator
	ready func() bool
	uuid  string
}

func New(cfg config.Config, ip string, ready func() bool) *Server {
	if ready == nil {
		ready = func() bool { return true }
	}
	s := &Server{cfg: cfg, ip: ip, ready: ready, uuid: stableUUID(cfg.SerialNumber)}
	if !cfg.InsecureNoAuth {
		s.auth = cherryAuth.New(cfg.ONVIFUsername, cfg.ONVIFPassword, cfg.AuthRealm)
	}
	return s
}

func (s *Server) Addr() string { return fmt.Sprintf(":%d", s.cfg.ONVIFPort) }
func (s *Server) DeviceURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/device_service", s.ip, s.cfg.ONVIFPort)
}
func (s *Server) MediaURL() string {
	return fmt.Sprintf("http://%s:%d/onvif/media_service", s.ip, s.cfg.ONVIFPort)
}
func (s *Server) SnapshotURL() string {
	return fmt.Sprintf("http://%s:%d/snapshot.jpg", s.ip, s.cfg.ONVIFPort)
}
func (s *Server) StreamURL() string {
	return fmt.Sprintf("rtsp://%s:%d/%s", s.ip, s.cfg.RTSPPort, s.cfg.RTSPPath)
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/onvif/device_service", s.device)
	mux.HandleFunc("/onvif/media_service", s.media)
	mux.HandleFunc("/snapshot.jpg", s.snapshot)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if !s.ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("stream offline\n"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})

	srv := &http.Server{
		Addr:              s.Addr(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("ONVIF HTTP service listening on %s", s.Addr())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAuthorizedSOAP(w, r)
	if !ok {
		return
	}
	action := soapAction(body)
	switch action {
	case "GetDeviceInformation":
		s.soap(w, fmt.Sprintf(`<tds:GetDeviceInformationResponse><tds:Manufacturer>%s</tds:Manufacturer><tds:Model>%s</tds:Model><tds:FirmwareVersion>%s</tds:FirmwareVersion><tds:SerialNumber>%s</tds:SerialNumber><tds:HardwareId>desktop-screen</tds:HardwareId></tds:GetDeviceInformationResponse>`, xmlEsc(s.cfg.Manufacturer), xmlEsc(s.cfg.Model), xmlEsc(s.cfg.FirmwareVersion), xmlEsc(s.cfg.SerialNumber)))
	case "GetSystemDateAndTime":
		now := time.Now().UTC()
		s.soap(w, fmt.Sprintf(`<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime><tt:DateTimeType>NTP</tt:DateTimeType><tt:DaylightSavings>false</tt:DaylightSavings><tt:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time><tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tt:UTCDateTime></tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>`, now.Hour(), now.Minute(), now.Second(), now.Year(), int(now.Month()), now.Day()))
	case "GetCapabilities":
		s.soap(w, fmt.Sprintf(`<tds:GetCapabilitiesResponse><tds:Capabilities><tt:Device><tt:XAddr>%s</tt:XAddr></tt:Device><tt:Media><tt:XAddr>%s</tt:XAddr><tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast><tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media></tds:Capabilities></tds:GetCapabilitiesResponse>`, s.DeviceURL(), s.MediaURL()))
	case "GetServices":
		s.soap(w, fmt.Sprintf(`<tds:GetServicesResponse><tds:Service><tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service><tds:Service><tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service></tds:GetServicesResponse>`, s.DeviceURL(), s.MediaURL()))
	case "GetScopes":
		name := url.PathEscape(strings.ReplaceAll(s.cfg.DeviceName, " ", "_"))
		s.soap(w, fmt.Sprintf(`<tds:GetScopesResponse><tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/type/video_encoder</tt:ScopeItem></tds:Scopes><tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/Profile/Streaming</tt:ScopeItem></tds:Scopes><tds:Scopes><tt:ScopeDef>Configurable</tt:ScopeDef><tt:ScopeItem>onvif://www.onvif.org/name/%s</tt:ScopeItem></tds:Scopes></tds:GetScopesResponse>`, name))
	case "GetHostname":
		s.soap(w, fmt.Sprintf(`<tds:GetHostnameResponse><tds:HostnameInformation><tt:FromDHCP>false</tt:FromDHCP><tt:Name>%s</tt:Name></tds:HostnameInformation></tds:GetHostnameResponse>`, xmlEsc(s.cfg.DeviceName)))
	case "GetNetworkInterfaces":
		prefix := networkPrefix(s.ip)
		s.soap(w, fmt.Sprintf(`<tds:GetNetworkInterfacesResponse><tds:NetworkInterfaces token="eth0"><tt:Enabled>true</tt:Enabled><tt:Info><tt:Name>Cherry Desktop Network</tt:Name></tt:Info><tt:IPv4><tt:Enabled>true</tt:Enabled><tt:Config><tt:Manual><tt:Address>%s</tt:Address><tt:PrefixLength>%d</tt:PrefixLength></tt:Manual><tt:DHCP>false</tt:DHCP></tt:Config></tt:IPv4></tds:NetworkInterfaces></tds:GetNetworkInterfacesResponse>`, xmlEsc(s.ip), prefix))
	case "GetUsers":
		s.soap(w, fmt.Sprintf(`<tds:GetUsersResponse><tds:User><tt:Username>%s</tt:Username><tt:UserLevel>Administrator</tt:UserLevel></tds:User></tds:GetUsersResponse>`, xmlEsc(s.cfg.ONVIFUsername)))
	case "GetServiceCapabilities":
		s.soap(w, `<tds:GetServiceCapabilitiesResponse><tds:Capabilities><tds:Network IPFilter="false" ZeroConfiguration="false" IPVersion6="false" DynDNS="false" Dot11Configuration="false"/><tds:Security TLS1.0="false" TLS1.1="false" TLS1.2="false" OnboardKeyGeneration="false" AccessPolicyConfig="false" X.509Token="false" SAMLToken="false" KerberosToken="false" RELToken="false"/></tds:Capabilities></tds:GetServiceCapabilitiesResponse>`)
	default:
		s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF device action: "+action)
	}
}

func (s *Server) media(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAuthorizedSOAP(w, r)
	if !ok {
		return
	}
	action := soapAction(body)
	profile := s.profileXML()
	switch action {
	case "GetProfiles":
		profiles := strings.Replace(profile, `<trt:Profile`, `<trt:Profiles`, 1)
		profiles = strings.Replace(profiles, `</trt:Profile>`, `</trt:Profiles>`, 1)
		s.soap(w, `<trt:GetProfilesResponse>`+profiles+`</trt:GetProfilesResponse>`)
	case "GetProfile":
		s.soap(w, `<trt:GetProfileResponse>`+profile+`</trt:GetProfileResponse>`)
	case "GetVideoSources":
		s.soap(w, fmt.Sprintf(`<trt:GetVideoSourcesResponse><trt:VideoSources token="desktop"><tt:Framerate>%d</tt:Framerate><tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution></trt:VideoSources></trt:GetVideoSourcesResponse>`, s.cfg.FPS, s.cfg.Width, s.cfg.Height))
	case "GetVideoSourceConfigurations", "GetVideoSourceConfiguration":
		tag := "trt:GetVideoSourceConfigurationsResponse"
		if action == "GetVideoSourceConfiguration" {
			tag = "trt:GetVideoSourceConfigurationResponse"
		}
		cfgTag := "trt:Configurations"
		if action == "GetVideoSourceConfiguration" {
			cfgTag = "trt:Configuration"
		}
		inner := fmt.Sprintf(`<%s token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>1</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="%d" y="%d" width="%d" height="%d"/></%s>`, cfgTag, s.cfg.OffsetX, s.cfg.OffsetY, s.cfg.Width, s.cfg.Height, cfgTag)
		s.soap(w, `<`+tag+`>`+inner+`</`+tag+`>`)
	case "GetVideoEncoderConfigurations", "GetVideoEncoderConfiguration":
		tag := "trt:GetVideoEncoderConfigurationsResponse"
		cfgTag := "trt:Configurations"
		if action == "GetVideoEncoderConfiguration" {
			tag, cfgTag = "trt:GetVideoEncoderConfigurationResponse", "trt:Configuration"
		}
		s.soap(w, `<`+tag+`>`+s.encoderXML(cfgTag)+`</`+tag+`>`)
	case "GetVideoEncoderConfigurationOptions":
		s.soap(w, fmt.Sprintf(`<trt:GetVideoEncoderConfigurationOptionsResponse><trt:Options><tt:QualityRange><tt:Min>1</tt:Min><tt:Max>10</tt:Max></tt:QualityRange><tt:H264><tt:ResolutionsAvailable><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:ResolutionsAvailable><tt:GovLengthRange><tt:Min>%d</tt:Min><tt:Max>%d</tt:Max></tt:GovLengthRange><tt:FrameRateRange><tt:Min>1</tt:Min><tt:Max>%d</tt:Max></tt:FrameRateRange><tt:EncodingIntervalRange><tt:Min>1</tt:Min><tt:Max>1</tt:Max></tt:EncodingIntervalRange><tt:H264ProfilesSupported>High</tt:H264ProfilesSupported></tt:H264></trt:Options></trt:GetVideoEncoderConfigurationOptionsResponse>`, s.cfg.Width, s.cfg.Height, s.cfg.FPS, s.cfg.FPS*10, s.cfg.FPS))
	case "GetStreamUri":
		s.soap(w, fmt.Sprintf(`<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT60S</tt:Timeout></trt:MediaUri></trt:GetStreamUriResponse>`, xmlEsc(s.StreamURL())))
	case "GetSnapshotUri":
		s.soap(w, fmt.Sprintf(`<trt:GetSnapshotUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT60S</tt:Timeout></trt:MediaUri></trt:GetSnapshotUriResponse>`, xmlEsc(s.SnapshotURL())))
	case "GetGuaranteedNumberOfVideoEncoderInstances":
		s.soap(w, `<trt:GetGuaranteedNumberOfVideoEncoderInstancesResponse><trt:TotalNumber>1</trt:TotalNumber><trt:H264>1</trt:H264></trt:GetGuaranteedNumberOfVideoEncoderInstancesResponse>`)
	case "GetServiceCapabilities":
		s.soap(w, `<trt:GetServiceCapabilitiesResponse><trt:Capabilities SnapshotUri="true" Rotation="false" VideoSourceMode="false" OSD="false" TemporaryOSDText="false" EXICompression="false"/></trt:GetServiceCapabilitiesResponse>`)
	default:
		s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF media action: "+action)
	}
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.auth != nil {
		ok, stale := s.auth.VerifyHTTP(r)
		if !ok {
			s.auth.Challenge(w, stale)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	stream := &url.URL{Scheme: "rtsp", Host: fmt.Sprintf("127.0.0.1:%d", s.cfg.RTSPPort), Path: "/" + s.cfg.RTSPPath}
	cmd := exec.CommandContext(ctx, s.cfg.FFmpegPath, "-hide_banner", "-loglevel", "error", "-rtsp_transport", "tcp", "-i", stream.String(), "-frames:v", "1", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		http.Error(w, "snapshot unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes())
}

func (s *Server) readAuthorizedSOAP(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		http.Error(w, "invalid SOAP body", http.StatusBadRequest)
		return nil, false
	}
	if s.auth == nil {
		return body, true
	}
	if ok, stale := s.auth.VerifyHTTP(r); ok {
		return body, true
	} else if stale {
		s.auth.Challenge(w, true)
		return nil, false
	}
	if s.auth.VerifyWSSE(body, time.Now().UTC()) {
		return body, true
	}
	s.auth.Challenge(w, false)
	return nil, false
}

func (s *Server) profileXML() string {
	return fmt.Sprintf(`<trt:Profile fixed="true" token="screen_profile"><tt:Name>Desktop Screen</tt:Name><tt:VideoSourceConfiguration token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>1</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="%d" y="%d" width="%d" height="%d"/></tt:VideoSourceConfiguration>%s</trt:Profile>`, s.cfg.OffsetX, s.cfg.OffsetY, s.cfg.Width, s.cfg.Height, s.encoderXML("tt:VideoEncoderConfiguration"))
}

func (s *Server) encoderXML(tag string) string {
	bitrate := bitrateKbps(s.cfg.VideoBitrate)
	return fmt.Sprintf(`<%s token="screen_encoder"><tt:Name>H264 Desktop</tt:Name><tt:UseCount>1</tt:UseCount><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution><tt:Quality>5</tt:Quality><tt:RateControl><tt:FrameRateLimit>%d</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval><tt:BitrateLimit>%d</tt:BitrateLimit></tt:RateControl><tt:H264><tt:GovLength>%d</tt:GovLength><tt:H264Profile>High</tt:H264Profile></tt:H264><tt:Multicast><tt:Address><tt:Type>IPv4</tt:Type><tt:IPv4Address>0.0.0.0</tt:IPv4Address></tt:Address><tt:Port>0</tt:Port><tt:TTL>1</tt:TTL><tt:AutoStart>false</tt:AutoStart></tt:Multicast><tt:SessionTimeout>PT60S</tt:SessionTimeout></%s>`, tag, s.cfg.Width, s.cfg.Height, s.cfg.FPS, bitrate, s.cfg.FPS*s.cfg.GOPSeconds, tag)
}

func (s *Server) soap(w http.ResponseWriter, inner string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>%s</s:Body></s:Envelope>`, inner)
}

func (s *Server) fault(w http.ResponseWriter, code, reason string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:ter="http://www.onvif.org/ver10/error"><s:Body><s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>%s</s:Value></s:Subcode></s:Code><s:Reason><s:Text xml:lang="en">%s</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`, xmlEsc(code), xmlEsc(reason))
}

func soapAction(body []byte) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	inBody := false
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		switch v := tok.(type) {
		case xml.StartElement:
			if v.Name.Local == "Body" {
				inBody = true
				continue
			}
			if inBody {
				return v.Name.Local
			}
		case xml.EndElement:
			if v.Name.Local == "Body" {
				return ""
			}
		}
	}
}

func bitrateKbps(v string) int {
	v = strings.TrimSpace(strings.ToLower(v))
	mul := 1
	if strings.HasSuffix(v, "k") {
		v = strings.TrimSuffix(v, "k")
	} else if strings.HasSuffix(v, "m") {
		v = strings.TrimSuffix(v, "m")
		mul = 1000
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 4000
	}
	return n * mul
}

func networkPrefix(ip string) int {
	target := net.ParseIP(ip)
	if target == nil {
		return 24
	}
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		addrs, _ := iface.Addrs()
		for _, addr := range addrs {
			n, ok := addr.(*net.IPNet)
			if ok && n.IP.Equal(target) {
				ones, _ := n.Mask.Size()
				return ones
			}
		}
	}
	return 24
}

func xmlEsc(v string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(v)
}
