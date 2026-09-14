package config

import (
    "encoding/json"
    "errors"
    "os"
)

type Config struct {
    DeviceName       string `json:"device_name"`
    Manufacturer     string `json:"manufacturer"`
    Model            string `json:"model"`
    SerialNumber     string `json:"serial_number"`
    FirmwareVersion  string `json:"firmware_version"`
    AdvertiseIP      string `json:"advertise_ip"`
    ONVIFPort        int    `json:"onvif_port"`
    RTSPPort         int    `json:"rtsp_port"`
    RTSPPath         string `json:"rtsp_path"`
    FPS              int    `json:"fps"`
    VideoBitrate     string `json:"video_bitrate"`
    RecordingEnabled bool   `json:"recording_enabled"`
    RecordingDir     string `json:"recording_dir"`
    SegmentSeconds   int    `json:"segment_seconds"`
    RetentionDays    int    `json:"retention_days"`
    FFmpegPath       string `json:"ffmpeg_path"`
    MediaMTXPath     string `json:"mediamtx_path"`
    MediaMTXConfig   string `json:"mediamtx_config"`
}

func Default() Config {
    return Config{
        DeviceName: "Cherry Desktop CCTV", Manufacturer: "Cherry", Model: "DesktopScreen-ONVIF",
        SerialNumber: "CHERRY-SCREEN-001", FirmwareVersion: "0.1.0", ONVIFPort: 8088,
        RTSPPort: 8554, RTSPPath: "screen", FPS: 15, VideoBitrate: "4000k",
        RecordingEnabled: true, RecordingDir: "recordings", SegmentSeconds: 300,
        RetentionDays: 7, FFmpegPath: "ffmpeg.exe", MediaMTXPath: "mediamtx.exe",
        MediaMTXConfig: "mediamtx.yml",
    }
}

func Load(path string) (Config, error) {
    cfg := Default()
    if path == "" {
        return cfg, nil
    }
    b, err := os.ReadFile(path)
    if errors.Is(err, os.ErrNotExist) {
        return cfg, nil
    }
    if err != nil {
        return cfg, err
    }
    if err := json.Unmarshal(b, &cfg); err != nil {
        return cfg, err
    }
    if cfg.ONVIFPort == 0 || cfg.RTSPPort == 0 || cfg.FPS <= 0 || cfg.SegmentSeconds <= 0 {
        return cfg, errors.New("invalid ports/fps/segment_seconds in config")
    }
    return cfg, nil
}
