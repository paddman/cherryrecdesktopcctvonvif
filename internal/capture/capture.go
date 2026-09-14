package capture

import (
    "context"
    "fmt"
    "log"
    "os"
    "os/exec"
    "path/filepath"
    "runtime"
    "strconv"
    "time"

    "github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

type Manager struct {
    cfg config.Config
}

func New(cfg config.Config) *Manager { return &Manager{cfg: cfg} }

func (m *Manager) Run(ctx context.Context, advertiseIP string) error {
    if runtime.GOOS != "windows" {
        return fmt.Errorf("desktop capture MVP currently supports Windows only")
    }

    if err := os.MkdirAll(m.cfg.RecordingDir, 0755); err != nil {
        return err
    }

    mediaCmd := exec.CommandContext(ctx, m.cfg.MediaMTXPath, m.cfg.MediaMTXConfig)
    mediaCmd.Stdout, mediaCmd.Stderr = os.Stdout, os.Stderr
    if err := mediaCmd.Start(); err != nil {
        return fmt.Errorf("start MediaMTX: %w", err)
    }
    log.Printf("MediaMTX started pid=%d", mediaCmd.Process.Pid)

    // Give RTSP server a moment to bind before FFmpeg publishes.
    select {
    case <-ctx.Done():
        return ctx.Err()
    case <-time.After(1200 * time.Millisecond):
    }

    streamURL := fmt.Sprintf("rtsp://127.0.0.1:%d/%s", m.cfg.RTSPPort, m.cfg.RTSPPath)
    streamArgs := []string{
        "-hide_banner", "-loglevel", "warning",
        "-f", "gdigrab", "-framerate", strconv.Itoa(m.cfg.FPS), "-i", "desktop",
        "-an", "-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
        "-pix_fmt", "yuv420p", "-b:v", m.cfg.VideoBitrate,
        "-g", strconv.Itoa(m.cfg.FPS * 2), "-keyint_min", strconv.Itoa(m.cfg.FPS * 2),
        "-rtsp_transport", "tcp", "-f", "rtsp", streamURL,
    }
    streamCmd := exec.CommandContext(ctx, m.cfg.FFmpegPath, streamArgs...)
    streamCmd.Stdout, streamCmd.Stderr = os.Stdout, os.Stderr
    if err := streamCmd.Start(); err != nil {
        _ = mediaCmd.Process.Kill()
        return fmt.Errorf("start FFmpeg RTSP publisher: %w", err)
    }
    log.Printf("screen stream started: rtsp://%s:%d/%s", advertiseIP, m.cfg.RTSPPort, m.cfg.RTSPPath)

    var recordCmd *exec.Cmd
    if m.cfg.RecordingEnabled {
        pattern := filepath.Join(m.cfg.RecordingDir, "screen_%Y%m%d_%H%M%S.mp4")
        recordArgs := []string{
            "-hide_banner", "-loglevel", "warning",
            "-f", "gdigrab", "-framerate", strconv.Itoa(m.cfg.FPS), "-i", "desktop",
            "-an", "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
            "-b:v", m.cfg.VideoBitrate,
            "-g", strconv.Itoa(m.cfg.FPS * 2),
            "-f", "segment", "-segment_time", strconv.Itoa(m.cfg.SegmentSeconds),
            "-reset_timestamps", "1", "-strftime", "1", pattern,
        }
        recordCmd = exec.CommandContext(ctx, m.cfg.FFmpegPath, recordArgs...)
        recordCmd.Stdout, recordCmd.Stderr = os.Stdout, os.Stderr
        if err := recordCmd.Start(); err != nil {
            _ = streamCmd.Process.Kill()
            _ = mediaCmd.Process.Kill()
            return fmt.Errorf("start FFmpeg recorder: %w", err)
        }
        log.Printf("continuous recording enabled dir=%s segment=%ds retention=%dd", m.cfg.RecordingDir, m.cfg.SegmentSeconds, m.cfg.RetentionDays)
        go m.retentionLoop(ctx)
    }

    <-ctx.Done()
    if recordCmd != nil && recordCmd.Process != nil { _ = recordCmd.Process.Kill() }
    if streamCmd.Process != nil { _ = streamCmd.Process.Kill() }
    if mediaCmd.Process != nil { _ = mediaCmd.Process.Kill() }
    return nil
}

func (m *Manager) retentionLoop(ctx context.Context) {
    if m.cfg.RetentionDays <= 0 { return }
    ticker := time.NewTicker(30 * time.Minute)
    defer ticker.Stop()
    cleanup := func() {
        cutoff := time.Now().Add(-time.Duration(m.cfg.RetentionDays) * 24 * time.Hour)
        entries, err := os.ReadDir(m.cfg.RecordingDir)
        if err != nil { return }
        for _, e := range entries {
            if e.IsDir() { continue }
            info, err := e.Info()
            if err == nil && info.ModTime().Before(cutoff) {
                _ = os.Remove(filepath.Join(m.cfg.RecordingDir, e.Name()))
            }
        }
    }
    cleanup()
    for {
        select {
        case <-ctx.Done(): return
        case <-ticker.C: cleanup()
        }
    }
}
