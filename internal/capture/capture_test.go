package capture

import (
	"strings"
	"testing"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

func TestFFmpegUsesSingleCaptureAndNoPublisherSecret(t *testing.T) {
	c := config.Default()
	c.InsecureNoAuth = true
	m := New(c)
	args := m.ffmpegArgs("libx264")
	joined := strings.Join(args, " ")
	if strings.Count(joined, "-f gdigrab") != 1 {
		t.Fatalf("expected one gdigrab input: %s", joined)
	}
	if strings.Contains(joined, "@127.0.0.1") {
		t.Fatalf("publisher secret must not be embedded in command line: %s", joined)
	}
}

func TestMediaMTXEnvironmentEnforcesDigest(t *testing.T) {
	c := config.Default()
	c.InsecureNoAuth = true
	c.RTSPUsername = "viewer"
	m := New(c)
	env := strings.Join(m.mediaMTXEnv(), "\n")
	if !strings.Contains(env, "MTX_RTSPAUTHMETHODS=digest") {
		t.Fatal("RTSP digest auth is not enforced")
	}
	if !strings.Contains(env, "RECORD=true") {
		t.Fatal("recording should be delegated to MediaMTX")
	}
	if !strings.Contains(env, "MTX_AUTHINTERNALUSERS_0_IPS=127.0.0.1,::1") {
		t.Fatal("publisher must be restricted to loopback")
	}
	if !strings.Contains(env, "MTX_AUTHINTERNALUSERS_0_PERMISSIONS_1_ACTION=read") {
		t.Fatal("internal snapshot reader must be loopback-only and credential-free")
	}
}
