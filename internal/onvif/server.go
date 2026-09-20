package onvif

import (
	"bufio"
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
	"sync"
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

	snapshotMu      sync.RWMutex
	snapshotJPEG    []byte
	snapshotUpdated time.Time
	events          *eventBroker
}

func New(cfg config.Config, ip string, ready func() bool) *Server {
	if ready == nil {
		ready = func() bool { return true }
	}
	s := &Server{cfg: cfg, ip: ip, ready: ready, uuid: stableUUID(cfg.SerialNumber)}
	s.events = newEventBroker(ready)
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
func (s *Server) Media2URL() string {
	return fmt.Sprintf("http://%s:%d/onvif/media2_service", s.ip, s.cfg.ONVIFPort)
}
func (s *Server) SnapshotURL() string {
	return fmt.Sprintf("http://%s:%d/snapshot.jpg", s.ip, s.cfg.ONVIFPort)
}
func (s *Server) StreamURL() string {
	return fmt.Sprintf("rtsp://%s:%d/%s", s.ip, s.cfg.RTSPPort, s.cfg.RTSPPath)
}
func (s *Server) SubStreamURL() string {
	return fmt.Sprintf("rtsp://%s:%d/%s", s.ip, s.cfg.RTSPPort, s.cfg.SubstreamPath)
}

func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/onvif/device_service", s.device)
	mux.HandleFunc("/onvif/media_service", s.media)
	mux.HandleFunc("/onvif/media2_service", s.media2)
	mux.HandleFunc("/onvif/events_service", s.eventsService)
	mux.HandleFunc("/onvif/pullpoint/", s.pullPointService)
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

	go s.snapshotLoop(ctx)
	go s.events.run(ctx)

	srv := &http.Server{
		Addr:              s.Addr(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      70 * time.Second,
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
		s.soap(w, fmt.Sprintf(`<tds:GetCapabilitiesResponse><tds:Capabilities><tt:Device><tt:XAddr>%s</tt:XAddr></tt:Device><tt:Media><tt:XAddr>%s</tt:XAddr><tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast><tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media><tt:Events><tt:XAddr>%s</tt:XAddr><tt:WSSubscriptionPolicySupport>false</tt:WSSubscriptionPolicySupport><tt:WSPullPointSupport>true</tt:WSPullPointSupport><tt:WSPausableSubscriptionManagerInterfaceSupport>false</tt:WSPausableSubscriptionManagerInterfaceSupport></tt:Events></tds:Capabilities></tds:GetCapabilitiesResponse>`, s.DeviceURL(), s.MediaURL(), s.EventURL()))
	case "GetServices":
		s.soap(w, fmt.Sprintf(`<tds:GetServicesResponse><tds:Service><tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service><tds:Service><tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service><tds:Service><tds:Namespace>http://www.onvif.org/ver20/media/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service><tds:Service><tds:Namespace>http://www.onvif.org/ver10/events/wsdl</tds:Namespace><tds:XAddr>%s</tds:XAddr><tds:Version><tt:Major>2</tt:Major><tt:Minor>6</tt:Minor></tds:Version></tds:Service></tds:GetServicesResponse>`, s.DeviceURL(), s.MediaURL(), s.Media2URL(), s.EventURL()))
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
	switch action {
	case "GetProfiles":
		s.soap(w, `<trt:GetProfilesResponse>`+s.media1ProfilesXML()+`</trt:GetProfilesResponse>`)
	case "GetProfile":
		token := elementText(body, "ProfileToken")
		profile, ok := s.media1ProfileByToken(token)
		if !ok {
			s.fault(w, "ter:NoProfile", "Unknown media profile: "+token)
			return
		}
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
		inner := fmt.Sprintf(`<%s token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>%d</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="%d" y="%d" width="%d" height="%d"/></%s>`, cfgTag, s.profileCount(), s.cfg.OffsetX, s.cfg.OffsetY, s.cfg.Width, s.cfg.Height, cfgTag)
		s.soap(w, `<`+tag+`>`+inner+`</`+tag+`>`)
	case "GetVideoEncoderConfigurations":
		inner := s.encoderXML("trt:Configurations")
		if s.cfg.SubstreamEnabled {
			inner += s.subEncoderXML("trt:Configurations")
		}
		s.soap(w, `<trt:GetVideoEncoderConfigurationsResponse>`+inner+`</trt:GetVideoEncoderConfigurationsResponse>`)
	case "GetVideoEncoderConfiguration":
		token := elementText(body, "ConfigurationToken")
		inner := s.encoderXML("trt:Configuration")
		if token == "screen_encoder_sub" && s.cfg.SubstreamEnabled {
			inner = s.subEncoderXML("trt:Configuration")
		} else if token != "" && token != "screen_encoder" {
			s.fault(w, "ter:NoConfig", "Unknown video encoder configuration: "+token)
			return
		}
		s.soap(w, `<trt:GetVideoEncoderConfigurationResponse>`+inner+`</trt:GetVideoEncoderConfigurationResponse>`)
	case "GetVideoEncoderConfigurationOptions":
		width, height, fps := s.cfg.Width, s.cfg.Height, s.cfg.FPS
		if elementText(body, "ConfigurationToken") == "screen_encoder_sub" || elementText(body, "ProfileToken") == "screen_profile_sub" {
			if !s.cfg.SubstreamEnabled {
				s.fault(w, "ter:NoConfig", "Substream is disabled")
				return
			}
			width, height, fps = s.cfg.SubstreamWidth, s.cfg.SubstreamHeight, s.cfg.SubstreamFPS
		}
		s.soap(w, fmt.Sprintf(`<trt:GetVideoEncoderConfigurationOptionsResponse><trt:Options><tt:QualityRange><tt:Min>1</tt:Min><tt:Max>10</tt:Max></tt:QualityRange><tt:H264><tt:ResolutionsAvailable><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:ResolutionsAvailable><tt:GovLengthRange><tt:Min>%d</tt:Min><tt:Max>%d</tt:Max></tt:GovLengthRange><tt:FrameRateRange><tt:Min>1</tt:Min><tt:Max>%d</tt:Max></tt:FrameRateRange><tt:EncodingIntervalRange><tt:Min>1</tt:Min><tt:Max>1</tt:Max></tt:EncodingIntervalRange><tt:H264ProfilesSupported>High</tt:H264ProfilesSupported></tt:H264></trt:Options></trt:GetVideoEncoderConfigurationOptionsResponse>`, width, height, fps, fps*10, fps))
	case "GetStreamUri":
		uri, ok := s.streamURLForProfile(elementText(body, "ProfileToken"))
		if !ok {
			s.fault(w, "ter:NoProfile", "Unknown media profile")
			return
		}
		s.soap(w, fmt.Sprintf(`<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT60S</tt:Timeout></trt:MediaUri></trt:GetStreamUriResponse>`, xmlEsc(uri)))
	case "GetSnapshotUri":
		if _, ok := s.streamURLForProfile(elementText(body, "ProfileToken")); !ok {
			s.fault(w, "ter:NoProfile", "Unknown media profile")
			return
		}
		s.soap(w, fmt.Sprintf(`<trt:GetSnapshotUriResponse><trt:MediaUri><tt:Uri>%s</tt:Uri><tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot><tt:Timeout>PT60S</tt:Timeout></trt:MediaUri></trt:GetSnapshotUriResponse>`, xmlEsc(s.SnapshotURL())))
	case "GetGuaranteedNumberOfVideoEncoderInstances":
		total := s.profileCount()
		s.soap(w, fmt.Sprintf(`<trt:GetGuaranteedNumberOfVideoEncoderInstancesResponse><trt:TotalNumber>%d</trt:TotalNumber><trt:H264>%d</trt:H264></trt:GetGuaranteedNumberOfVideoEncoderInstancesResponse>`, total, total))
	case "GetServiceCapabilities":
		s.soap(w, `<trt:GetServiceCapabilitiesResponse><trt:Capabilities SnapshotUri="true" Rotation="false" VideoSourceMode="false" OSD="false" TemporaryOSDText="false" EXICompression="false"/></trt:GetServiceCapabilitiesResponse>`)
	default:
		s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF media action: "+action)
	}
}

func (s *Server) media2(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readAuthorizedSOAP(w, r)
	if !ok {
		return
	}
	action := soapAction(body)
	switch action {
	case "GetProfiles":
		token := elementText(body, "Token")
		includeConfig := elementText(body, "Type") != ""
		if token != "" {
			profile, ok := s.media2ProfileByToken(token, includeConfig)
			if !ok {
				s.fault(w, "ter:NoProfile", "Unknown Media2 profile: "+token)
				return
			}
			s.soapMedia2(w, `<tr2:GetProfilesResponse>`+profile+`</tr2:GetProfilesResponse>`)
			return
		}
		bodyXML := s.media2ProfileXML(false, includeConfig)
		if s.cfg.SubstreamEnabled {
			bodyXML += s.media2ProfileXML(true, includeConfig)
		}
		s.soapMedia2(w, `<tr2:GetProfilesResponse>`+bodyXML+`</tr2:GetProfilesResponse>`)
	case "GetStreamUri":
		uri, ok := s.streamURLForProfile(elementText(body, "ProfileToken"))
		if !ok {
			s.fault(w, "ter:NoProfile", "Unknown Media2 profile")
			return
		}
		protocol := elementText(body, "Protocol")
		if protocol != "" && protocol != "RTSP" && protocol != "RtspUnicast" {
			s.fault(w, "ter:InvalidStreamSetup", "Unsupported Media2 protocol: "+protocol)
			return
		}
		s.soapMedia2(w, fmt.Sprintf(`<tr2:GetStreamUriResponse><tr2:Uri>%s</tr2:Uri></tr2:GetStreamUriResponse>`, xmlEsc(uri)))
	case "GetSnapshotUri":
		if _, ok := s.streamURLForProfile(elementText(body, "ProfileToken")); !ok {
			s.fault(w, "ter:NoProfile", "Unknown Media2 profile")
			return
		}
		s.soapMedia2(w, fmt.Sprintf(`<tr2:GetSnapshotUriResponse><tr2:Uri>%s</tr2:Uri></tr2:GetSnapshotUriResponse>`, xmlEsc(s.SnapshotURL())))
	case "GetVideoSourceConfigurations":
		configurationToken := elementText(body, "ConfigurationToken")
		if configurationToken != "" && configurationToken != "screen_source" {
			s.fault(w, "ter:NoConfig", "Unknown video source configuration: "+configurationToken)
			return
		}
		profileToken := elementText(body, "ProfileToken")
		if profileToken != "" {
			if _, ok := s.streamURLForProfile(profileToken); !ok {
				s.fault(w, "ter:NoProfile", "Unknown Media2 profile: "+profileToken)
				return
			}
		}
		s.soapMedia2(w, fmt.Sprintf(`<tr2:GetVideoSourceConfigurationsResponse><tr2:Configurations token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>%d</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="%d" y="%d" width="%d" height="%d"/></tr2:Configurations></tr2:GetVideoSourceConfigurationsResponse>`, s.profileCount(), s.cfg.OffsetX, s.cfg.OffsetY, s.cfg.Width, s.cfg.Height))
	case "GetVideoEncoderConfigurations":
		xml := s.media2EncoderXML(false)
		if s.cfg.SubstreamEnabled {
			xml += s.media2EncoderXML(true)
		}
		s.soapMedia2(w, `<tr2:GetVideoEncoderConfigurationsResponse>`+xml+`</tr2:GetVideoEncoderConfigurationsResponse>`)
	case "GetVideoEncoderInstances":
		token := elementText(body, "ConfigurationToken")
		if token != "" && token != "screen_source" {
			s.fault(w, "ter:NoConfig", "Unknown video source configuration: "+token)
			return
		}
		total := s.profileCount()
		s.soapMedia2(w, fmt.Sprintf(`<tr2:GetVideoEncoderInstancesResponse><tr2:Info><tr2:Codec><tr2:Encoding>H264</tr2:Encoding><tr2:Number>%d</tr2:Number></tr2:Codec><tr2:Total>%d</tr2:Total></tr2:Info></tr2:GetVideoEncoderInstancesResponse>`, total, total))
	case "GetServiceCapabilities":
		s.soapMedia2(w, fmt.Sprintf(`<tr2:GetServiceCapabilitiesResponse><tr2:Capabilities MaximumNumberOfProfiles="%d" ConfigurationsSupported="VideoSource VideoEncoder" SnapshotUri="true" Rotation="false" VideoSourceMode="false" OSD="true" TemporaryOSDText="false" Mask="false" RTSPStreaming="true" SecureRTSPStreaming="false" RTPMulticast="false" RTP_RTSP_TCP="true" AutoStartMulticast="false" MultiTrackStreaming="false"/></tr2:GetServiceCapabilitiesResponse>`, s.profileCount()))
	default:
		if s.handleMedia2OSD(w, action, body) {
			return
		}
		s.fault(w, "ter:ActionNotSupported", "Unsupported ONVIF Media2 action: "+action)
	}
}

func (s *Server) media2ProfileByToken(token string, includeConfig bool) (string, bool) {
	switch token {
	case "screen_profile":
		return s.media2ProfileXML(false, includeConfig), true
	case "screen_profile_sub":
		if s.cfg.SubstreamEnabled {
			return s.media2ProfileXML(true, includeConfig), true
		}
	}
	return "", false
}

func (s *Server) media2ProfileXML(sub, includeConfig bool) string {
	token, name := "screen_profile", "Desktop Screen"
	if sub {
		token, name = "screen_profile_sub", "Desktop Screen Substream"
	}
	configs := ""
	if includeConfig {
		configs = `<tr2:Configurations><tr2:VideoSource token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>` + strconv.Itoa(s.profileCount()) + `</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="` + strconv.Itoa(s.cfg.OffsetX) + `" y="` + strconv.Itoa(s.cfg.OffsetY) + `" width="` + strconv.Itoa(s.cfg.Width) + `" height="` + strconv.Itoa(s.cfg.Height) + `"/></tr2:VideoSource>` + s.media2EncoderXMLForProfile(sub) + `</tr2:Configurations>`
	}
	return `<tr2:Profiles token="` + token + `" fixed="true"><tr2:Name>` + xmlEsc(name) + `</tr2:Name>` + configs + `</tr2:Profiles>`
}

func (s *Server) media2EncoderXMLForProfile(sub bool) string {
	token, name := "screen_encoder", "H264 Desktop"
	width, height, fps, bitrate := s.cfg.Width, s.cfg.Height, s.cfg.FPS, s.cfg.VideoBitrate
	if sub {
		token, name = "screen_encoder_sub", "H264 Desktop Substream"
		width, height, fps, bitrate = s.cfg.SubstreamWidth, s.cfg.SubstreamHeight, s.cfg.SubstreamFPS, s.cfg.SubstreamBitrate
	}
	return media2EncoderConfigXML("tr2:VideoEncoder", token, name, width, height, fps, bitrateKbps(bitrate), fps*s.cfg.GOPSeconds)
}

func (s *Server) media2EncoderXML(sub bool) string {
	token, name := "screen_encoder", "H264 Desktop"
	width, height, fps, bitrate := s.cfg.Width, s.cfg.Height, s.cfg.FPS, s.cfg.VideoBitrate
	if sub {
		token, name = "screen_encoder_sub", "H264 Desktop Substream"
		width, height, fps, bitrate = s.cfg.SubstreamWidth, s.cfg.SubstreamHeight, s.cfg.SubstreamFPS, s.cfg.SubstreamBitrate
	}
	return media2EncoderConfigXML("tr2:Configurations", token, name, width, height, fps, bitrateKbps(bitrate), fps*s.cfg.GOPSeconds)
}

func media2EncoderConfigXML(tag, token, name string, width, height, fps, bitrate, gop int) string {
	return fmt.Sprintf(`<%s token="%s"><tt:Name>%s</tt:Name><tt:UseCount>1</tt:UseCount><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution><tt:Quality>5</tt:Quality><tt:RateControl><tt:FrameRateLimit>%d</tt:FrameRateLimit><tt:BitrateLimit>%d</tt:BitrateLimit></tt:RateControl><tt:GovLength>%d</tt:GovLength><tt:Profile>High</tt:Profile><tt:Multicast><tt:Address><tt:Type>IPv4</tt:Type><tt:IPv4Address>0.0.0.0</tt:IPv4Address></tt:Address><tt:Port>0</tt:Port><tt:TTL>1</tt:TTL><tt:AutoStart>false</tt:AutoStart></tt:Multicast></%s>`, tag, token, xmlEsc(name), width, height, fps, bitrate, gop, tag)
}

func (s *Server) soapMedia2(w http.ResponseWriter, inner string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tr2="http://www.onvif.org/ver20/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>%s</s:Body></s:Envelope>`, inner)
}

func (s *Server) streamURLForProfile(token string) (string, bool) {
	switch token {
	case "", "screen_profile":
		return s.StreamURL(), true
	case "screen_profile_sub":
		if s.cfg.SubstreamEnabled {
			return s.SubStreamURL(), true
		}
	}
	return "", false
}

func elementText(body []byte, localName string) string {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != localName {
			continue
		}
		var value string
		if err := dec.DecodeElement(&value, &start); err != nil {
			return ""
		}
		return strings.TrimSpace(value)
	}
}

func (s *Server) snapshotLoop(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		if !s.ready() {
			if !waitSnapshot(ctx, time.Second) {
				return
			}
			continue
		}
		stream := &url.URL{Scheme: "rtsp", Host: fmt.Sprintf("127.0.0.1:%d", s.cfg.RTSPPort), Path: "/" + s.cfg.RTSPPath}
		rate := 1000.0 / float64(s.cfg.SnapshotRefreshMS)
		cmd := exec.CommandContext(ctx, s.cfg.FFmpegPath,
			"-hide_banner", "-loglevel", "error", "-nostdin",
			"-rtsp_transport", "tcp", "-i", stream.String(),
			"-vf", fmt.Sprintf("fps=%.3f", rate),
			"-q:v", "5", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
		)
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("snapshot worker pipe failed: %v", err)
			if !waitSnapshot(ctx, backoff) {
				return
			}
			continue
		}
		cmd.Stderr = log.Writer()
		if err := cmd.Start(); err != nil {
			log.Printf("snapshot worker start failed: %v", err)
			if !waitSnapshot(ctx, backoff) {
				return
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		log.Printf("snapshot cache worker started pid=%d refresh=%dms", cmd.Process.Pid, s.cfg.SnapshotRefreshMS)
		reader := bufio.NewReaderSize(stdout, 256*1024)
		frames := 0
		for ctx.Err() == nil {
			frame, err := readJPEGFrame(reader, 16<<20)
			if err != nil {
				break
			}
			s.snapshotMu.Lock()
			s.snapshotJPEG = append(s.snapshotJPEG[:0], frame...)
			s.snapshotUpdated = time.Now()
			s.snapshotMu.Unlock()
			frames++
			if frames == 1 {
				backoff = time.Second
			}
		}
		err = cmd.Wait()
		if ctx.Err() != nil {
			return
		}
		log.Printf("snapshot cache worker exited: %v", err)
		if !waitSnapshot(ctx, backoff) {
			return
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func readJPEGFrame(r *bufio.Reader, maxBytes int) ([]byte, error) {
	var frame bytes.Buffer
	var prev byte
	started := false
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if !started {
			if prev == 0xff && b == 0xd8 {
				frame.WriteByte(0xff)
				frame.WriteByte(0xd8)
				started = true
			}
			prev = b
			continue
		}
		frame.WriteByte(b)
		if frame.Len() > maxBytes {
			return nil, fmt.Errorf("snapshot JPEG exceeds %d bytes", maxBytes)
		}
		if prev == 0xff && b == 0xd9 {
			return frame.Bytes(), nil
		}
		prev = b
	}
}

func waitSnapshot(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
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
	s.snapshotMu.RLock()
	frame := append([]byte(nil), s.snapshotJPEG...)
	updated := s.snapshotUpdated
	s.snapshotMu.RUnlock()
	if len(frame) == 0 {
		http.Error(w, "snapshot warming up", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Cherry-Snapshot-Age", time.Since(updated).Round(time.Millisecond).String())
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(frame)
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

func (s *Server) profileCount() int {
	if s.cfg.SubstreamEnabled {
		return 2
	}
	return 1
}

func (s *Server) media1ProfilesXML() string {
	main := strings.Replace(s.profileXML(false), "<trt:Profile", "<trt:Profiles", 1)
	main = strings.Replace(main, "</trt:Profile>", "</trt:Profiles>", 1)
	if !s.cfg.SubstreamEnabled {
		return main
	}
	sub := strings.Replace(s.profileXML(true), "<trt:Profile", "<trt:Profiles", 1)
	sub = strings.Replace(sub, "</trt:Profile>", "</trt:Profiles>", 1)
	return main + sub
}

func (s *Server) media1ProfileByToken(token string) (string, bool) {
	if token == "" || token == "screen_profile" {
		return s.profileXML(false), true
	}
	if token == "screen_profile_sub" && s.cfg.SubstreamEnabled {
		return s.profileXML(true), true
	}
	return "", false
}

func (s *Server) profileXML(sub bool) string {
	profileToken, profileName := "screen_profile", "Desktop Screen"
	encoder := s.encoderXML("tt:VideoEncoderConfiguration")
	if sub {
		profileToken, profileName = "screen_profile_sub", "Desktop Screen Substream"
		encoder = s.subEncoderXML("tt:VideoEncoderConfiguration")
	}
	return fmt.Sprintf(`<trt:Profile fixed="true" token="%s"><tt:Name>%s</tt:Name><tt:VideoSourceConfiguration token="screen_source"><tt:Name>Desktop</tt:Name><tt:UseCount>%d</tt:UseCount><tt:SourceToken>desktop</tt:SourceToken><tt:Bounds x="%d" y="%d" width="%d" height="%d"/></tt:VideoSourceConfiguration>%s</trt:Profile>`, profileToken, profileName, s.profileCount(), s.cfg.OffsetX, s.cfg.OffsetY, s.cfg.Width, s.cfg.Height, encoder)
}

func (s *Server) encoderXML(tag string) string {
	return s.encoderXMLFor(tag, "screen_encoder", "H264 Desktop", s.cfg.Width, s.cfg.Height, s.cfg.FPS, s.cfg.VideoBitrate)
}

func (s *Server) subEncoderXML(tag string) string {
	return s.encoderXMLFor(tag, "screen_encoder_sub", "H264 Desktop Substream", s.cfg.SubstreamWidth, s.cfg.SubstreamHeight, s.cfg.SubstreamFPS, s.cfg.SubstreamBitrate)
}

func (s *Server) encoderXMLFor(tag, token, name string, width, height, fps int, bitrateValue string) string {
	bitrate := bitrateKbps(bitrateValue)
	return fmt.Sprintf(`<%s token="%s"><tt:Name>%s</tt:Name><tt:UseCount>1</tt:UseCount><tt:Encoding>H264</tt:Encoding><tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution><tt:Quality>5</tt:Quality><tt:RateControl><tt:FrameRateLimit>%d</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval><tt:BitrateLimit>%d</tt:BitrateLimit></tt:RateControl><tt:H264><tt:GovLength>%d</tt:GovLength><tt:H264Profile>High</tt:H264Profile></tt:H264><tt:Multicast><tt:Address><tt:Type>IPv4</tt:Type><tt:IPv4Address>0.0.0.0</tt:IPv4Address></tt:Address><tt:Port>0</tt:Port><tt:TTL>1</tt:TTL><tt:AutoStart>false</tt:AutoStart></tt:Multicast><tt:SessionTimeout>PT60S</tt:SessionTimeout></%s>`, tag, token, name, width, height, fps, bitrate, fps*s.cfg.GOPSeconds, tag)
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
