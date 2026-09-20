package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const osdToken = "osd_text_1"

type osdConfiguration struct {
	Token                         string `xml:"token,attr"`
	VideoSourceConfigurationToken string `xml:"VideoSourceConfigurationToken"`
	Type                          string `xml:"Type"`
	Position                      struct {
		Type string `xml:"Type"`
	} `xml:"Position"`
	TextString struct {
		Type      string `xml:"Type"`
		FontSize  int    `xml:"FontSize"`
		PlainText string `xml:"PlainText"`
	} `xml:"TextString"`
}

func (s *Server) handleMedia2OSD(w http.ResponseWriter, action string, body []byte) bool {
	switch action {
	case "GetOSDs":
		token := elementText(body, "OSDToken")
		text, err := s.readOSDText()
		if err != nil {
			s.fault(w, "ter:Action", "Unable to read OSD configuration")
			return true
		}
		if token != "" && token != osdToken {
			s.fault(w, "ter:NoConfig", "Unknown OSD token: "+token)
			return true
		}
		if text == "" {
			if token != "" {
				s.fault(w, "ter:NoConfig", "OSD does not exist")
				return true
			}
			s.soapMedia2(w, "<tr2:GetOSDsResponse/>")
			return true
		}
		s.soapMedia2(w, "<tr2:GetOSDsResponse>"+s.osdXML(text)+"</tr2:GetOSDsResponse>")
		return true

	case "GetOSDOptions":
		token := elementText(body, "ConfigurationToken")
		if token != "" && token != "screen_source" {
			s.fault(w, "ter:NoConfig", "Unknown video source configuration: "+token)
			return true
		}
		size := s.cfg.OSDFontSize
		s.soapMedia2(w, fmt.Sprintf(`<tr2:GetOSDOptionsResponse><tr2:Options><tt:MaximumNumberOfOSDs Total="1" PlainText="1"/><tt:Type>Text</tt:Type><tt:PositionOption>UpperLeft</tt:PositionOption><tt:TextOption><tt:Type>Plain</tt:Type><tt:FontSizeRange><tt:Min>%d</tt:Min><tt:Max>%d</tt:Max></tt:FontSizeRange></tt:TextOption></tr2:Options></tr2:GetOSDOptionsResponse>`, size, size))
		return true

	case "CreateOSD":
		current, err := s.readOSDText()
		if err != nil {
			s.fault(w, "ter:Action", "Unable to read OSD configuration")
			return true
		}
		if current != "" {
			s.fault(w, "ter:MaxOSDs", "Only one text OSD is supported")
			return true
		}
		cfg, err := parseOSDConfiguration(body)
		if err != nil {
			s.fault(w, "ter:ConfigModify", err.Error())
			return true
		}
		if err := s.validateOSD(cfg, false); err != nil {
			s.fault(w, "ter:ConfigModify", err.Error())
			return true
		}
		if err := s.writeOSDText(cfg.TextString.PlainText); err != nil {
			s.fault(w, "ter:Action", "Unable to persist OSD configuration")
			return true
		}
		s.soapMedia2(w, "<tr2:CreateOSDResponse><tr2:OSDToken>"+osdToken+"</tr2:OSDToken></tr2:CreateOSDResponse>")
		return true

	case "SetOSD":
		current, err := s.readOSDText()
		if err != nil {
			s.fault(w, "ter:Action", "Unable to read OSD configuration")
			return true
		}
		if current == "" {
			s.fault(w, "ter:NoConfig", "OSD does not exist")
			return true
		}
		cfg, err := parseOSDConfiguration(body)
		if err != nil {
			s.fault(w, "ter:ConfigModify", err.Error())
			return true
		}
		if err := s.validateOSD(cfg, true); err != nil {
			s.fault(w, "ter:ConfigModify", err.Error())
			return true
		}
		if err := s.writeOSDText(cfg.TextString.PlainText); err != nil {
			s.fault(w, "ter:Action", "Unable to persist OSD configuration")
			return true
		}
		s.soapMedia2(w, "<tr2:SetOSDResponse/>")
		return true

	case "DeleteOSD":
		token := elementText(body, "OSDToken")
		if token != osdToken {
			s.fault(w, "ter:NoConfig", "Unknown OSD token: "+token)
			return true
		}
		current, err := s.readOSDText()
		if err != nil {
			s.fault(w, "ter:Action", "Unable to read OSD configuration")
			return true
		}
		if current == "" {
			s.fault(w, "ter:NoConfig", "OSD does not exist")
			return true
		}
		if err := s.writeOSDText(""); err != nil {
			s.fault(w, "ter:Action", "Unable to persist OSD configuration")
			return true
		}
		s.soapMedia2(w, "<tr2:DeleteOSDResponse/>")
		return true
	}
	return false
}

func parseOSDConfiguration(body []byte) (osdConfiguration, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return osdConfiguration{}, fmt.Errorf("OSD configuration missing")
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "OSD" {
			continue
		}
		var cfg osdConfiguration
		if err := dec.DecodeElement(&cfg, &start); err != nil {
			return osdConfiguration{}, fmt.Errorf("invalid OSD configuration: %w", err)
		}
		return cfg, nil
	}
}

func (s *Server) validateOSD(cfg osdConfiguration, requireToken bool) error {
	if requireToken && cfg.Token != osdToken {
		return fmt.Errorf("unknown OSD token")
	}
	if cfg.VideoSourceConfigurationToken != "" && cfg.VideoSourceConfigurationToken != "screen_source" {
		return fmt.Errorf("unknown video source configuration")
	}
	if cfg.Type != "" && cfg.Type != "Text" {
		return fmt.Errorf("only text OSD is supported")
	}
	if cfg.Position.Type != "" && cfg.Position.Type != "UpperLeft" {
		return fmt.Errorf("only UpperLeft OSD position is supported")
	}
	if cfg.TextString.Type != "" && cfg.TextString.Type != "Plain" {
		return fmt.Errorf("only Plain OSD text is supported")
	}
	if cfg.TextString.FontSize != 0 && cfg.TextString.FontSize != s.cfg.OSDFontSize {
		return fmt.Errorf("unsupported OSD font size")
	}
	if len([]rune(cfg.TextString.PlainText)) > 256 {
		return fmt.Errorf("OSD text is limited to 256 characters")
	}
	if strings.ContainsRune(cfg.TextString.PlainText, '\x00') {
		return fmt.Errorf("OSD text contains a NUL character")
	}
	return nil
}

func (s *Server) osdXML(text string) string {
	return fmt.Sprintf(`<tr2:OSDs token="%s"><tt:VideoSourceConfigurationToken>screen_source</tt:VideoSourceConfigurationToken><tt:Type>Text</tt:Type><tt:Position><tt:Type>UpperLeft</tt:Type></tt:Position><tt:TextString><tt:Type>Plain</tt:Type><tt:FontSize>%d</tt:FontSize><tt:PlainText>%s</tt:PlainText></tt:TextString></tr2:OSDs>`, osdToken, s.cfg.OSDFontSize, xmlEsc(text))
}

func (s *Server) readOSDText() (string, error) {
	b, err := os.ReadFile(s.cfg.OSDTextAbsPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func (s *Server) writeOSDText(text string) error {
	path := s.cfg.OSDTextAbsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}
