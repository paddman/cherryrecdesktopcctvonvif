package onvif

import (
	"fmt"
	"net/http"
)

const metadataConfigurationToken = "screen_metadata"

func (s *Server) metadataConfigurationXML(tag string) string {
	return fmt.Sprintf(`<%s token="%s"><tt:Name>ONVIF Event Metadata</tt:Name><tt:UseCount>%d</tt:UseCount><tt:PTZStatus><tt:Status>false</tt:Status><tt:Position>false</tt:Position></tt:PTZStatus><tt:Events/><tt:Analytics>false</tt:Analytics><tt:Multicast><tt:Address><tt:Type>IPv4</tt:Type><tt:IPv4Address>0.0.0.0</tt:IPv4Address></tt:Address><tt:Port>0</tt:Port><tt:TTL>1</tt:TTL><tt:AutoStart>false</tt:AutoStart></tt:Multicast><tt:SessionTimeout>PT60S</tt:SessionTimeout></%s>`, tag, metadataConfigurationToken, s.profileCount(), tag)
}

func (s *Server) metadataConfigurationOptionsXML(tag string) string {
	return `<` + tag + `><tt:PTZStatusFilterOptions><tt:PanTiltStatusSupported>false</tt:PanTiltStatusSupported><tt:ZoomStatusSupported>false</tt:ZoomStatusSupported><tt:PanTiltPositionSupported>false</tt:PanTiltPositionSupported><tt:ZoomPositionSupported>false</tt:ZoomPositionSupported></tt:PTZStatusFilterOptions></` + tag + `>`
}

func (s *Server) handleMedia1Metadata(w http.ResponseWriter, action string, body []byte) bool {
	switch action {
	case "GetMetadataConfigurations":
		if !s.cfg.MetadataEnabled {
			s.soap(w, "<trt:GetMetadataConfigurationsResponse/>")
			return true
		}
		s.soap(w, "<trt:GetMetadataConfigurationsResponse>"+s.metadataConfigurationXML("trt:Configurations")+"</trt:GetMetadataConfigurationsResponse>")
		return true

	case "GetMetadataConfiguration":
		if !s.cfg.MetadataEnabled {
			s.fault(w, "ter:NoConfig", "Metadata streaming is disabled")
			return true
		}
		token := elementText(body, "ConfigurationToken")
		if token != metadataConfigurationToken {
			s.fault(w, "ter:NoConfig", "Unknown metadata configuration: "+token)
			return true
		}
		s.soap(w, "<trt:GetMetadataConfigurationResponse>"+s.metadataConfigurationXML("trt:Configuration")+"</trt:GetMetadataConfigurationResponse>")
		return true

	case "GetMetadataConfigurationOptions":
		if !s.cfg.MetadataEnabled {
			s.fault(w, "ter:NoConfig", "Metadata streaming is disabled")
			return true
		}
		if !s.validMetadataRequest(body) {
			s.fault(w, "ter:NoConfig", "Unknown metadata configuration or profile")
			return true
		}
		s.soap(w, "<trt:GetMetadataConfigurationOptionsResponse>"+s.metadataConfigurationOptionsXML("trt:Options")+"</trt:GetMetadataConfigurationOptionsResponse>")
		return true
	}
	return false
}

func (s *Server) handleMedia2Metadata(w http.ResponseWriter, action string, body []byte) bool {
	switch action {
	case "GetMetadataConfigurations":
		if !s.cfg.MetadataEnabled {
			s.soapMedia2(w, "<tr2:GetMetadataConfigurationsResponse/>")
			return true
		}
		if !s.validMetadataRequest(body) {
			s.fault(w, "ter:NoConfig", "Unknown metadata configuration or profile")
			return true
		}
		s.soapMedia2(w, "<tr2:GetMetadataConfigurationsResponse>"+s.metadataConfigurationXML("tr2:Configurations")+"</tr2:GetMetadataConfigurationsResponse>")
		return true

	case "GetMetadataConfigurationOptions":
		if !s.cfg.MetadataEnabled {
			s.fault(w, "ter:NoConfig", "Metadata streaming is disabled")
			return true
		}
		if !s.validMetadataRequest(body) {
			s.fault(w, "ter:NoConfig", "Unknown metadata configuration or profile")
			return true
		}
		s.soapMedia2(w, "<tr2:GetMetadataConfigurationOptionsResponse>"+s.metadataConfigurationOptionsXML("tr2:Options")+"</tr2:GetMetadataConfigurationOptionsResponse>")
		return true
	}
	return false
}

func (s *Server) validMetadataRequest(body []byte) bool {
	configToken := elementText(body, "ConfigurationToken")
	if configToken != "" && configToken != metadataConfigurationToken {
		return false
	}
	profileToken := elementText(body, "ProfileToken")
	if profileToken != "" {
		if _, ok := s.streamURLForProfile(profileToken); !ok {
			return false
		}
	}
	return true
}
