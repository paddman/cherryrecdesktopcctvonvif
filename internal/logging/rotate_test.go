package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.log")
	w, err := New(path, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	payload := strings.Repeat("x", 700*1024)
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
}
