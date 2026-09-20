package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var rtspPathRE = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

type Config struct {
	DeviceName          string `json:"device_name"`
	Manufacturer        string `json:"manufacturer"`
	Model               string `json:"model"`
	SerialNumber        string `json:"serial_number"`
	FirmwareVersion     string `json:"firmware_version"`
	AdvertiseIP         string `json:"advertise_ip"`
	ONVIFPort           int    `json:"onvif_port"`
	RTSPPort            int    `json:"rtsp_port"`
	RTSPPath            string `json:"rtsp_path"`
	SubstreamEnabled    bool   `json:"substream_enabled"`
	SubstreamPath       string `json:"substream_path"`
	SubstreamFPS        int    `json:"substream_fps"`
	SubstreamWidth      int    `json:"substream_width"`
	SubstreamHeight     int    `json:"substream_height"`
	SubstreamBitrate    string `json:"substream_bitrate"`
	SnapshotRefreshMS   int    `json:"snapshot_refresh_ms"`
	OSDTextFile         string `json:"osd_text_file"`
	OSDFontFile         string `json:"osd_font_file"`
	OSDFontSize         int    `json:"osd_font_size"`
	MetadataEnabled     bool   `json:"metadata_enabled"`
	MetadataIntervalMS  int    `json:"metadata_interval_ms"`
	MetadataPayloadType int    `json:"metadata_payload_type"`
	FPS                 int    `json:"fps"`
	Width               int    `json:"width"`
	Height              int    `json:"height"`
	OffsetX             int    `json:"offset_x"`
	OffsetY             int    `json:"offset_y"`
	VideoBitrate        string `json:"video_bitrate"`
	GOPSeconds          int    `json:"gop_seconds"`
	Encoder             string `json:"encoder"`
	RecordingEnabled    bool   `json:"recording_enabled"`
	RecordingDir        string `json:"recording_dir"`
	SegmentSeconds      int    `json:"segment_seconds"`
	RetentionDays       int    `json:"retention_days"`
	MaxRecordingGB      int64  `json:"max_recording_gb"`
	FFmpegPath          string `json:"ffmpeg_path"`
	MediaMTXPath        string `json:"mediamtx_path"`
	MediaMTXConfig      string `json:"mediamtx_config"`
	LogFile             string `json:"log_file"`
	LogMaxMB            int64  `json:"log_max_mb"`
	LogBackups          int    `json:"log_backups"`

	ONVIFUsername    string `json:"onvif_username"`
	ONVIFPassword    string `json:"onvif_password,omitempty"`
	ONVIFPasswordEnv string `json:"onvif_password_env"`
	RTSPUsername     string `json:"rtsp_username"`
	RTSPPassword     string `json:"rtsp_password,omitempty"`
	RTSPPasswordEnv  string `json:"rtsp_password_env"`
	AuthRealm        string `json:"auth_realm"`
	InsecureNoAuth   bool   `json:"insecure_allow_no_auth"`
}

func Default() Config {
	return Config{
		DeviceName: "Cherry Desktop CCTV", Manufacturer: "Cherry", Model: "DesktopScreen-ONVIF",
		SerialNumber: "CHERRY-SCREEN-001", FirmwareVersion: "1.0.0", ONVIFPort: 8088,
		RTSPPort: 8554, RTSPPath: "screen", SubstreamEnabled: true, SubstreamPath: "screen_sub",
		SubstreamFPS: 10, SubstreamWidth: 640, SubstreamHeight: 360, SubstreamBitrate: "800k",
		SnapshotRefreshMS: 1000, OSDTextFile: "osd.txt",
		OSDFontFile: "C:\\Windows\\Fonts\\arial.ttf", OSDFontSize: 24,
		MetadataEnabled: true, MetadataIntervalMS: 1000, MetadataPayloadType: 107,
		FPS: 15, Width: 1920, Height: 1080,
		VideoBitrate: "4000k", GOPSeconds: 2, Encoder: "auto",
		RecordingEnabled: true, RecordingDir: "recordings", SegmentSeconds: 300,
		RetentionDays: 7, MaxRecordingGB: 500, FFmpegPath: "ffmpeg.exe",
		MediaMTXPath: "mediamtx.exe", MediaMTXConfig: "mediamtx.yml",
		LogFile: "logs/cherrycctv.log", LogMaxMB: 20, LogBackups: 5,
		ONVIFUsername: "admin", ONVIFPasswordEnv: "CHERRY_ONVIF_PASSWORD",
		RTSPPasswordEnv: "CHERRY_RTSP_PASSWORD", AuthRealm: "Cherry Desktop CCTV",
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return cfg, err
		}
		if err == nil {
			if err := json.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse config: %w", err)
			}
		}
	}
	cfg.applyEnv()
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	if c.ONVIFPasswordEnv != "" {
		if v, ok := os.LookupEnv(c.ONVIFPasswordEnv); ok {
			c.ONVIFPassword = v
		}
	}
	if c.RTSPPasswordEnv != "" {
		if v, ok := os.LookupEnv(c.RTSPPasswordEnv); ok {
			c.RTSPPassword = v
		}
	}
	if c.RTSPUsername == "" {
		c.RTSPUsername = c.ONVIFUsername
	}
	if c.RTSPPassword == "" {
		c.RTSPPassword = c.ONVIFPassword
	}
}

func (c Config) Validate() error {
	if c.ONVIFPort < 1 || c.ONVIFPort > 65535 || c.RTSPPort < 1 || c.RTSPPort > 65535 {
		return errors.New("onvif_port and rtsp_port must be between 1 and 65535")
	}
	if c.ONVIFPort == c.RTSPPort {
		return errors.New("onvif_port and rtsp_port must be different")
	}
	if c.FPS < 1 || c.FPS > 120 {
		return errors.New("fps must be between 1 and 120")
	}
	if c.Width < 64 || c.Height < 64 || c.Width%2 != 0 || c.Height%2 != 0 {
		return errors.New("width and height must be even integers >= 64")
	}
	if c.GOPSeconds < 1 || c.GOPSeconds > 10 {
		return errors.New("gop_seconds must be between 1 and 10")
	}
	if c.SegmentSeconds < 10 {
		return errors.New("segment_seconds must be at least 10")
	}
	if c.RetentionDays < 0 || c.MaxRecordingGB < 0 {
		return errors.New("retention_days and max_recording_gb cannot be negative")
	}
	if !rtspPathRE.MatchString(c.RTSPPath) {
		return errors.New("rtsp_path may contain only letters, digits and underscore")
	}
	if c.SubstreamEnabled {
		if !rtspPathRE.MatchString(c.SubstreamPath) {
			return errors.New("substream_path may contain only letters, digits and underscore")
		}
		if c.SubstreamPath == c.RTSPPath {
			return errors.New("substream_path must differ from rtsp_path")
		}
		if c.SubstreamFPS < 1 || c.SubstreamFPS > c.FPS {
			return errors.New("substream_fps must be between 1 and fps")
		}
		if c.SubstreamWidth < 64 || c.SubstreamHeight < 64 || c.SubstreamWidth%2 != 0 || c.SubstreamHeight%2 != 0 {
			return errors.New("substream_width and substream_height must be even integers >= 64")
		}
		if c.SubstreamWidth > c.Width || c.SubstreamHeight > c.Height {
			return errors.New("substream dimensions cannot exceed main stream dimensions")
		}
		if strings.TrimSpace(c.SubstreamBitrate) == "" {
			return errors.New("substream_bitrate is required when substream is enabled")
		}
	}
	if c.SnapshotRefreshMS < 250 || c.SnapshotRefreshMS > 60000 {
		return errors.New("snapshot_refresh_ms must be between 250 and 60000")
	}
	if strings.TrimSpace(c.OSDTextFile) == "" {
		return errors.New("osd_text_file is required")
	}
	if strings.TrimSpace(c.OSDFontFile) == "" {
		return errors.New("osd_font_file is required")
	}
	if c.OSDFontSize < 8 || c.OSDFontSize > 96 {
		return errors.New("osd_font_size must be between 8 and 96")
	}
	if c.MetadataEnabled {
		if c.MetadataIntervalMS < 250 || c.MetadataIntervalMS > 5000 {
			return errors.New("metadata_interval_ms must be between 250 and 5000")
		}
		if c.MetadataPayloadType < 96 || c.MetadataPayloadType > 127 {
			return errors.New("metadata_payload_type must be between 96 and 127")
		}
	}
	switch c.Encoder {
	case "auto", "libx264", "h264_nvenc", "h264_qsv", "h264_amf":
	default:
		return fmt.Errorf("unsupported encoder %q", c.Encoder)
	}
	if c.AdvertiseIP != "" && net.ParseIP(c.AdvertiseIP) == nil {
		return errors.New("advertise_ip is not a valid IP address")
	}
	if strings.TrimSpace(c.DeviceName) == "" || strings.TrimSpace(c.SerialNumber) == "" {
		return errors.New("device_name and serial_number are required")
	}
	if strings.TrimSpace(c.VideoBitrate) == "" {
		return errors.New("video_bitrate is required")
	}
	if c.RecordingEnabled && strings.TrimSpace(c.RecordingDir) == "" {
		return errors.New("recording_dir is required when recording is enabled")
	}
	if strings.TrimSpace(c.LogFile) == "" || c.LogMaxMB < 1 || c.LogBackups < 1 {
		return errors.New("log_file is required and log_max_mb/log_backups must be >= 1")
	}
	if c.InsecureNoAuth {
		return nil
	}
	if c.ONVIFUsername == "" || c.ONVIFPassword == "" {
		return fmt.Errorf("ONVIF authentication is required; set onvif_username and %s (or onvif_password)", c.ONVIFPasswordEnv)
	}
	if c.RTSPUsername == "" || c.RTSPPassword == "" {
		return fmt.Errorf("RTSP authentication is required; set rtsp_username and %s (or rtsp_password)", c.RTSPPasswordEnv)
	}
	if len(c.ONVIFPassword) < 12 || len(c.RTSPPassword) < 12 {
		return errors.New("production passwords must be at least 12 characters")
	}
	if c.AuthRealm == "" {
		return errors.New("auth_realm is required")
	}
	return nil
}

func (c Config) RecordingAbsPath() string {
	p, err := filepath.Abs(c.RecordingDir)
	if err != nil {
		return c.RecordingDir
	}
	return p
}

func (c Config) OSDTextAbsPath() string {
	p, err := filepath.Abs(c.OSDTextFile)
	if err != nil {
		return c.OSDTextFile
	}
	return p
}
