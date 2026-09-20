package capture

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

type Manager struct {
	cfg            config.Config
	cfgMu          sync.RWMutex
	configPath     string
	restartCapture chan struct{}
	ready          atomic.Bool
	sourceReady    atomic.Bool
}

func New(cfg config.Config) *Manager {
	return &Manager{cfg: cfg, restartCapture: make(chan struct{}, 1)}
}

func (m *Manager) Ready() bool { return m.ready.Load() }

func (m *Manager) SetConfigPath(path string) {
	m.cfgMu.Lock()
	m.configPath = path
	m.cfgMu.Unlock()
}

func (m *Manager) CurrentConfig() config.Config {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.cfg
}

func (m *Manager) UpdateConfig(next config.Config, persist bool) error {
	if err := next.Validate(); err != nil {
		return err
	}

	m.cfgMu.RLock()
	path := m.configPath
	m.cfgMu.RUnlock()
	if persist {
		if path == "" {
			return errors.New("config path is not set")
		}
		if err := persistVideoConfig(path, next); err != nil {
			return err
		}
	}

	m.cfgMu.Lock()
	m.cfg.FPS = next.FPS
	m.cfg.Width = next.Width
	m.cfg.Height = next.Height
	m.cfg.VideoBitrate = next.VideoBitrate
	m.cfg.GOPSeconds = next.GOPSeconds
	m.cfg.SubstreamFPS = next.SubstreamFPS
	m.cfg.SubstreamWidth = next.SubstreamWidth
	m.cfg.SubstreamHeight = next.SubstreamHeight
	m.cfg.SubstreamBitrate = next.SubstreamBitrate
	m.cfgMu.Unlock()

	select {
	case m.restartCapture <- struct{}{}:
	default:
	}
	return nil
}

func (m *Manager) Preflight(ctx context.Context) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("desktop capture is supported on Windows only")
	}
	if err := os.MkdirAll(m.cfg.RecordingAbsPath(), 0o750); err != nil {
		return fmt.Errorf("create recording directory: %w", err)
	}
	if err := m.prepareOSD(); err != nil {
		return fmt.Errorf("prepare OSD: %w", err)
	}
	if err := commandCheck(ctx, m.cfg.FFmpegPath, "-version"); err != nil {
		return fmt.Errorf("ffmpeg preflight: %w", err)
	}
	if err := ffmpegFilterCheck(ctx, m.cfg.FFmpegPath, "drawtext"); err != nil {
		return fmt.Errorf("ffmpeg OSD preflight: %w", err)
	}
	if err := commandCheck(ctx, m.cfg.MediaMTXPath, "--version"); err != nil {
		return fmt.Errorf("mediamtx preflight: %w", err)
	}
	if err := commandCheck(ctx, m.cfg.MediaMTXPath, "--validate-conf="+m.cfg.MediaMTXConfig); err != nil {
		return fmt.Errorf("mediamtx config validation: %w", err)
	}
	return nil
}

func commandCheck(parent context.Context, path, arg string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, arg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return fmt.Errorf("%s %s failed: %w: %s", path, arg, err, msg)
	}
	return nil
}

func ffmpegFilterCheck(parent context.Context, path, filter string) error {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-hide_banner", "-filters").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s -filters failed: %w", path, err)
	}
	if !strings.Contains(string(out), " "+filter+" ") {
		return fmt.Errorf("required FFmpeg filter %q is unavailable", filter)
	}
	return nil
}

func (m *Manager) Run(ctx context.Context, advertiseIP string) error {
	if err := m.Preflight(ctx); err != nil {
		return err
	}
	encoder, err := m.selectEncoder(ctx)
	if err != nil {
		return err
	}
	log.Printf("capture encoder=%s main=%dx%d@%dfps substream=%t sub=%dx%d@%dfps", encoder, m.cfg.Width, m.cfg.Height, m.cfg.FPS, m.cfg.SubstreamEnabled, m.cfg.SubstreamWidth, m.cfg.SubstreamHeight, m.cfg.SubstreamFPS)

	go m.storageQuotaLoop(ctx)
	go m.superviseMediaMTX(ctx)

	if err := waitTCP(ctx, fmt.Sprintf("127.0.0.1:%d", m.cfg.RTSPPort), 20*time.Second); err != nil {
		return fmt.Errorf("MediaMTX readiness: %w", err)
	}

	if m.cfg.MetadataEnabled {
		go m.superviseMetadataRelay(ctx, m.rawPath(m.cfg.RTSPPath), m.cfg.RTSPPath, true)
		if m.cfg.SubstreamEnabled {
			go m.superviseMetadataRelay(ctx, m.rawPath(m.cfg.SubstreamPath), m.cfg.SubstreamPath, false)
		}
	}

	m.superviseFFmpeg(ctx, encoder, advertiseIP)
	return nil
}

func (m *Manager) superviseMediaMTX(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		cmd := exec.CommandContext(ctx, m.cfg.MediaMTXPath, m.cfg.MediaMTXConfig)
		cmd.Stdout, cmd.Stderr = log.Writer(), log.Writer()
		cmd.Env = append(os.Environ(), m.mediaMTXEnv()...)
		if err := cmd.Start(); err != nil {
			log.Printf("MediaMTX start failed: %v", err)
		} else {
			log.Printf("MediaMTX started pid=%d", cmd.Process.Pid)
			started := time.Now()
			err := cmd.Wait()
			if ctx.Err() != nil {
				return
			}
			log.Printf("MediaMTX exited: %v", err)
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
		}
		if !sleepContext(ctx, backoff) {
			return
		}
		backoff = minDuration(backoff*2, 30*time.Second)
	}
}

func (m *Manager) superviseFFmpeg(ctx context.Context, encoder, advertiseIP string) {
	backoff := time.Second
	for ctx.Err() == nil {
		m.sourceReady.Store(false)
		m.ready.Store(false)
		cfg := m.CurrentConfig()
		args := m.ffmpegArgsWithConfig(cfg, encoder)
		cmd := exec.CommandContext(ctx, cfg.FFmpegPath, args...)
		cmd.Stdout, cmd.Stderr = log.Writer(), log.Writer()
		if err := cmd.Start(); err != nil {
			log.Printf("FFmpeg start failed: %v", err)
		} else {
			m.sourceReady.Store(true)
			if !cfg.MetadataEnabled {
				m.ready.Store(true)
			}
			log.Printf("screen capture source online rtsp://127.0.0.1:%d/%s public=rtsp://%s:%d/%s metadata=%t",
				cfg.RTSPPort, m.publishPath(cfg.RTSPPath), advertiseIP, cfg.RTSPPort, cfg.RTSPPath, cfg.MetadataEnabled)
			started := time.Now()
			waitCh := make(chan error, 1)
			go func() { waitCh <- cmd.Wait() }()

			restarted := false
			var waitErr error
			select {
			case <-ctx.Done():
				_ = cmd.Process.Kill()
				waitErr = <-waitCh
			case <-m.restartCapture:
				restarted = true
				log.Printf("video encoder configuration changed; restarting FFmpeg capture")
				_ = cmd.Process.Kill()
				waitErr = <-waitCh
			case waitErr = <-waitCh:
			}

			m.sourceReady.Store(false)
			m.ready.Store(false)
			if ctx.Err() != nil {
				return
			}
			if restarted {
				backoff = time.Second
				continue
			}
			log.Printf("FFmpeg exited: %v", waitErr)
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
		}
		if !sleepContext(ctx, backoff) {
			return
		}
		backoff = minDuration(backoff*2, 30*time.Second)
	}
}

func (m *Manager) ffmpegArgs(encoder string) []string {
	return m.ffmpegArgsWithConfig(m.CurrentConfig(), encoder)
}

func (m *Manager) ffmpegArgsWithConfig(cfg config.Config, encoder string) []string {
	mainURL := fmt.Sprintf("rtsp://127.0.0.1:%d/%s", cfg.RTSPPort, m.publishPath(cfg.RTSPPath))
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-f", "gdigrab", "-draw_mouse", "1", "-framerate", strconv.Itoa(cfg.FPS),
		"-offset_x", strconv.Itoa(cfg.OffsetX), "-offset_y", strconv.Itoa(cfg.OffsetY),
		"-video_size", fmt.Sprintf("%dx%d", cfg.Width, cfg.Height), "-i", "desktop",
	}
	mainGOP := strconv.Itoa(cfg.FPS * cfg.GOPSeconds)
	osd := m.drawtextFilter()

	if !cfg.SubstreamEnabled {
		args = append(args, "-vf", osd, "-map", "0:v:0", "-an")
		args = append(args, encoderArgs(encoder, cfg.VideoBitrate)...)
		args = append(args,
			"-pix_fmt", "yuv420p", "-g", mainGOP, "-keyint_min", mainGOP,
			"-rtsp_transport", "tcp", "-f", "rtsp", mainURL,
		)
		return args
	}

	subURL := fmt.Sprintf("rtsp://127.0.0.1:%d/%s", cfg.RTSPPort, m.publishPath(cfg.SubstreamPath))
	filter := fmt.Sprintf("[0:v]%s[osd];[osd]split=2[main][sub];[sub]scale=%d:%d:flags=bicubic[subout]", osd, cfg.SubstreamWidth, cfg.SubstreamHeight)
	subGOP := strconv.Itoa(cfg.SubstreamFPS * cfg.GOPSeconds)

	args = append(args, "-filter_complex", filter)

	args = append(args, "-map", "[main]", "-an")
	args = append(args, encoderArgs(encoder, cfg.VideoBitrate)...)
	args = append(args,
		"-pix_fmt", "yuv420p", "-g", mainGOP, "-keyint_min", mainGOP,
		"-rtsp_transport", "tcp", "-f", "rtsp", mainURL,
	)

	args = append(args, "-map", "[subout]", "-an", "-r", strconv.Itoa(cfg.SubstreamFPS))
	args = append(args, encoderArgs(encoder, cfg.SubstreamBitrate)...)
	args = append(args,
		"-pix_fmt", "yuv420p", "-g", subGOP, "-keyint_min", subGOP,
		"-rtsp_transport", "tcp", "-f", "rtsp", subURL,
	)
	return args
}

func (m *Manager) prepareOSD() error {
	textPath := m.cfg.OSDTextAbsPath()
	if err := os.MkdirAll(filepath.Dir(textPath), 0o750); err != nil {
		return err
	}
	if _, err := os.Stat(textPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(textPath, nil, 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(m.cfg.OSDFontFile); err != nil {
		return fmt.Errorf("OSD font file %s: %w", m.cfg.OSDFontFile, err)
	}
	return nil
}

func (m *Manager) drawtextFilter() string {
	font := ffmpegFilterPath(m.cfg.OSDFontFile)
	text := ffmpegFilterPath(m.cfg.OSDTextAbsPath())
	return fmt.Sprintf("drawtext=fontfile='%s':textfile='%s':reload=1:fontsize=%d:fontcolor=white:box=1:boxcolor=black@0.55:boxborderw=6:x=16:y=16", font, text, m.cfg.OSDFontSize)
}

func ffmpegFilterPath(path string) string {
	path = filepath.ToSlash(path)
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.ReplaceAll(path, ":", "\\:")
	path = strings.ReplaceAll(path, "'", "\\'")
	return path
}

func encoderArgs(encoder, bitrate string) []string {
	switch encoder {
	case "h264_nvenc":
		return []string{"-c:v", encoder, "-preset", "p4", "-tune", "ll", "-rc", "cbr", "-b:v", bitrate, "-maxrate", bitrate}
	case "h264_qsv":
		return []string{"-c:v", encoder, "-preset", "veryfast", "-b:v", bitrate, "-maxrate", bitrate}
	case "h264_amf":
		return []string{"-c:v", encoder, "-usage", "lowlatency", "-quality", "balanced", "-b:v", bitrate, "-maxrate", bitrate}
	default:
		return []string{"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency", "-b:v", bitrate, "-maxrate", bitrate}
	}
}

func (m *Manager) selectEncoder(ctx context.Context) (string, error) {
	if m.cfg.Encoder != "auto" {
		if err := m.probeEncoder(ctx, m.cfg.Encoder); err != nil {
			return "", fmt.Errorf("configured encoder %s is unavailable: %w", m.cfg.Encoder, err)
		}
		return m.cfg.Encoder, nil
	}
	for _, enc := range []string{"h264_nvenc", "h264_qsv", "h264_amf", "libx264"} {
		if err := m.probeEncoder(ctx, enc); err == nil {
			return enc, nil
		}
	}
	return "", errors.New("no usable H.264 encoder found")
}

func (m *Manager) probeEncoder(parent context.Context, encoder string) error {
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=black:s=128x72:r=1", "-frames:v", "1"}
	args = append(args, encoderArgs(encoder, "500k")...)
	args = append(args, "-f", "null", "-")
	return exec.CommandContext(ctx, m.cfg.FFmpegPath, args...).Run()
}

func (m *Manager) publishPath(publicPath string) string {
	if !m.cfg.MetadataEnabled {
		return publicPath
	}
	return m.rawPath(publicPath)
}

func (m *Manager) rawPath(publicPath string) string {
	return publicPath + "_raw"
}

func pathEnvKey(path string) string {
	return strings.ToUpper(path)
}

func appendPermission(env []string, userIndex, permissionIndex int, action, path string) []string {
	prefix := fmt.Sprintf("MTX_AUTHINTERNALUSERS_%d_PERMISSIONS_%d_", userIndex, permissionIndex)
	return append(env,
		prefix+"ACTION="+action,
		prefix+"PATH="+path,
	)
}

func (m *Manager) mediaMTXEnv() []string {
	env := []string{
		"MTX_RTSPADDRESS=:" + strconv.Itoa(m.cfg.RTSPPort),
		"MTX_RTSPTRANSPORTS=tcp,udp",
		"MTX_RTSPAUTHMETHODS=digest",
		"MTX_AUTHINTERNALUSERS_0_USER=any",
		"MTX_AUTHINTERNALUSERS_0_PASS=",
		"MTX_AUTHINTERNALUSERS_0_IPS=127.0.0.1,::1",
		"MTX_AUTHINTERNALUSERS_1_USER=" + m.cfg.RTSPUsername,
		"MTX_AUTHINTERNALUSERS_1_PASS=" + m.cfg.RTSPPassword,
	}

	internalPermissions := 0
	addInternal := func(action, path string) {
		env = appendPermission(env, 0, internalPermissions, action, path)
		internalPermissions++
	}
	externalPermissions := 0
	addExternalRead := func(path string) {
		env = appendPermission(env, 1, externalPermissions, "read", path)
		externalPermissions++
	}

	publicPaths := []string{m.cfg.RTSPPath}
	if m.cfg.SubstreamEnabled {
		publicPaths = append(publicPaths, m.cfg.SubstreamPath)
	}

	for _, publicPath := range publicPaths {
		sourcePath := m.publishPath(publicPath)
		addInternal("publish", sourcePath)
		addInternal("read", sourcePath)
		if m.cfg.MetadataEnabled {
			addInternal("publish", publicPath)
			addInternal("read", publicPath)
		}
		addExternalRead(publicPath)

		env = append(env, "MTX_PATHS_"+pathEnvKey(sourcePath)+"_SOURCE=publisher")
		if m.cfg.MetadataEnabled {
			env = append(env,
				"MTX_PATHS_"+pathEnvKey(publicPath)+"_SOURCE=publisher",
				"MTX_PATHS_"+pathEnvKey(publicPath)+"_RECORD=false",
			)
		}
	}

	if m.cfg.InsecureNoAuth {
		env = append(env,
			"MTX_AUTHINTERNALUSERS_1_USER=any",
			"MTX_AUTHINTERNALUSERS_1_PASS=",
		)
	}

	recordPathName := m.publishPath(m.cfg.RTSPPath)
	recordKey := pathEnvKey(recordPathName)
	if m.cfg.RecordingEnabled {
		recordPath := filepath.Join(m.cfg.RecordingAbsPath(), m.cfg.RTSPPath, "%Y-%m-%d_%H-%M-%S-%f")
		env = append(env,
			"MTX_PATHS_"+recordKey+"_RECORD=true",
			"MTX_PATHS_"+recordKey+"_RECORDPATH="+recordPath,
			"MTX_PATHS_"+recordKey+"_RECORDFORMAT=fmp4",
			"MTX_PATHS_"+recordKey+"_RECORDPARTDURATION=1s",
			"MTX_PATHS_"+recordKey+"_RECORDSEGMENTDURATION="+strconv.Itoa(m.cfg.SegmentSeconds)+"s",
			"MTX_PATHS_"+recordKey+"_RECORDDELETEAFTER="+strconv.Itoa(m.cfg.RetentionDays*24)+"h",
		)
	} else {
		env = append(env, "MTX_PATHS_"+recordKey+"_RECORD=false")
	}

	if m.cfg.SubstreamEnabled {
		env = append(env, "MTX_PATHS_"+pathEnvKey(m.publishPath(m.cfg.SubstreamPath))+"_RECORD=false")
	}
	return env
}

func (m *Manager) storageQuotaLoop(ctx context.Context) {
	if !m.cfg.RecordingEnabled || m.cfg.MaxRecordingGB <= 0 {
		return
	}
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	m.enforceQuota()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.enforceQuota()
		}
	}
}

type diskFile struct {
	path string
	size int64
	mod  time.Time
}

func (m *Manager) enforceQuota() {
	root := m.cfg.RecordingAbsPath()
	var files []diskFile
	var total int64
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		files = append(files, diskFile{path: path, size: info.Size(), mod: info.ModTime()})
		return nil
	})
	limit := m.cfg.MaxRecordingGB * 1024 * 1024 * 1024
	if total <= limit {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	protectRecent := time.Now().Add(-2 * time.Duration(m.cfg.SegmentSeconds) * time.Second)
	for _, f := range files {
		if total <= limit {
			break
		}
		if f.mod.After(protectRecent) {
			continue
		}
		if err := os.Remove(f.path); err == nil {
			total -= f.size
			log.Printf("recording quota cleanup removed=%s", f.path)
		}
	}
}

func waitTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if !sleepContext(ctx, 250*time.Millisecond) {
			return ctx.Err()
		}
	}
	return fmt.Errorf("timeout waiting for %s", address)
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func persistVideoConfig(path string, cfg config.Config) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config for persistence: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("parse config for persistence: %w", err)
	}
	doc["fps"] = cfg.FPS
	doc["width"] = cfg.Width
	doc["height"] = cfg.Height
	doc["video_bitrate"] = cfg.VideoBitrate
	doc["gop_seconds"] = cfg.GOPSeconds
	doc["substream_fps"] = cfg.SubstreamFPS
	doc["substream_width"] = cfg.SubstreamWidth
	doc["substream_height"] = cfg.SubstreamHeight
	doc["substream_bitrate"] = cfg.SubstreamBitrate
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("persist video config: %w", err)
	}
	return nil
}
