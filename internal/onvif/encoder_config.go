package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

type VideoConfigController interface {
	CurrentConfig() config.Config
	UpdateConfig(config.Config, bool) error
}

type videoEncoderConfigurationRequest struct {
	Token      string `xml:"token,attr"`
	Encoding   string `xml:"Encoding"`
	Resolution struct {
		Width  int `xml:"Width"`
		Height int `xml:"Height"`
	} `xml:"Resolution"`
	RateControl struct {
		FrameRateLimit   int `xml:"FrameRateLimit"`
		EncodingInterval int `xml:"EncodingInterval"`
		BitrateLimit     int `xml:"BitrateLimit"`
	} `xml:"RateControl"`
	H264 struct {
		GovLength int `xml:"GovLength"`
	} `xml:"H264"`
	GovLength int `xml:"GovLength"`
}

func (s *Server) setVideoEncoderConfiguration(w http.ResponseWriter, body []byte, media2 bool) {
	if s.videoController == nil {
		s.fault(w, "ter:ActionNotSupported", "Runtime video configuration is unavailable")
		return
	}

	req, err := parseVideoEncoderConfiguration(body)
	if err != nil {
		s.fault(w, "ter:ConfigModify", err.Error())
		return
	}

	current := s.videoController.CurrentConfig()
	next := current
	isSub := false
	switch req.Token {
	case "screen_encoder":
	case "screen_encoder_sub":
		if !current.SubstreamEnabled {
			s.fault(w, "ter:NoConfig", "Substream is disabled")
			return
		}
		isSub = true
	default:
		s.fault(w, "ter:NoConfig", "Unknown video encoder configuration: "+req.Token)
		return
	}

	if req.Encoding != "" && !strings.EqualFold(req.Encoding, "H264") {
		s.fault(w, "ter:ConfigModify", "Only H264 encoding is supported")
		return
	}

	width, height, fps, bitrate := current.Width, current.Height, current.FPS, bitrateKbps(current.VideoBitrate)
	if isSub {
		width, height, fps, bitrate = current.SubstreamWidth, current.SubstreamHeight, current.SubstreamFPS, bitrateKbps(current.SubstreamBitrate)
	}

	if req.Resolution.Width != 0 {
		width = req.Resolution.Width
	}
	if req.Resolution.Height != 0 {
		height = req.Resolution.Height
	}
	if req.RateControl.FrameRateLimit != 0 {
		fps = req.RateControl.FrameRateLimit
	}
	if req.RateControl.BitrateLimit != 0 {
		bitrate = req.RateControl.BitrateLimit
	}
	if req.RateControl.EncodingInterval != 0 && req.RateControl.EncodingInterval != 1 {
		s.fault(w, "ter:ConfigModify", "EncodingInterval must be 1")
		return
	}

	gov := req.GovLength
	if gov == 0 {
		gov = req.H264.GovLength
	}
	if gov != 0 {
		if fps < 1 || gov%fps != 0 {
			s.fault(w, "ter:ConfigModify", "GovLength must be a whole number of seconds at the selected frame rate")
			return
		}
		next.GOPSeconds = gov / fps
	}

	if width < 64 || height < 64 || width%2 != 0 || height%2 != 0 {
		s.fault(w, "ter:ConfigModify", "Resolution must use even dimensions of at least 64 pixels")
		return
	}
	if fps < 1 || fps > 120 {
		s.fault(w, "ter:ConfigModify", "FrameRateLimit must be between 1 and 120")
		return
	}
	if bitrate < 64 || bitrate > 200000 {
		s.fault(w, "ter:ConfigModify", "BitrateLimit must be between 64 and 200000 kbit/s")
		return
	}

	if isSub {
		next.SubstreamWidth = width
		next.SubstreamHeight = height
		next.SubstreamFPS = fps
		next.SubstreamBitrate = strconv.Itoa(bitrate) + "k"
	} else {
		next.Width = width
		next.Height = height
		next.FPS = fps
		next.VideoBitrate = strconv.Itoa(bitrate) + "k"
	}

	persist := strings.EqualFold(elementText(body, "ForcePersistence"), "true")
	if err := s.videoController.UpdateConfig(next, persist); err != nil {
		s.fault(w, "ter:ConfigModify", err.Error())
		return
	}

	if media2 {
		s.soapMedia2(w, "<tr2:SetVideoEncoderConfigurationResponse/>")
	} else {
		s.soap(w, "<trt:SetVideoEncoderConfigurationResponse/>")
	}
}

func parseVideoEncoderConfiguration(body []byte) (videoEncoderConfigurationRequest, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return videoEncoderConfigurationRequest{}, fmt.Errorf("video encoder configuration missing")
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "Configuration" {
			continue
		}
		var cfg videoEncoderConfigurationRequest
		if err := dec.DecodeElement(&cfg, &start); err != nil {
			return videoEncoderConfigurationRequest{}, fmt.Errorf("invalid video encoder configuration: %w", err)
		}
		if cfg.Token == "" {
			return videoEncoderConfigurationRequest{}, fmt.Errorf("configuration token is required")
		}
		return cfg, nil
	}
}
