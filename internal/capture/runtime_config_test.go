package capture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/paddman/cherryrecdesktopcctvonvif/internal/config"
)

func TestUpdateConfigPersistsOnlyVideoFieldsAndSignalsRestart(t *testing.T) {
	cfg := config.Default()
	cfg.InsecureNoAuth = true
	m := New(cfg)

	path := filepath.Join(t.TempDir(), "config.json")
	initial := map[string]any{
		"device_name": "Cherry Desktop CCTV",
		"onvif_password": "do-not-touch-this",
		"custom_field": "keep-me",
		"fps": 15,
		"width": 1920,
		"height": 1080,
	}
	b, _ := json.Marshal(initial)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	m.SetConfigPath(path)

	next := cfg
	next.FPS = 25
	next.Width = 1280
	next.Height = 720
	next.VideoBitrate = "3000k"
	next.GOPSeconds = 3
	if err := m.UpdateConfig(next, true); err != nil {
		t.Fatal(err)
	}

	select {
	case <-m.restartCapture:
	default:
		t.Fatal("capture restart was not signaled")
	}
	if got := m.CurrentConfig(); got.FPS != 25 || got.Width != 1280 || got.GOPSeconds != 3 {
		t.Fatalf("runtime config not updated: %+v", got)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["onvif_password"] != "do-not-touch-this" || saved["custom_field"] != "keep-me" {
		t.Fatalf("non-video fields were modified: %#v", saved)
	}
	if saved["fps"].(float64) != 25 || saved["width"].(float64) != 1280 || saved["gop_seconds"].(float64) != 3 {
		t.Fatalf("video fields not persisted: %#v", saved)
	}
}
