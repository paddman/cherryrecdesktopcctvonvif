package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type RotatingWriter struct {
	mu      sync.Mutex
	path    string
	maxSize int64
	backups int
	file    *os.File
	size    int64
}

func New(path string, maxMB int64, backups int) (*RotatingWriter, error) {
	if maxMB <= 0 {
		maxMB = 20
	}
	if backups < 1 {
		backups = 5
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	w := &RotatingWriter{path: path, maxSize: maxMB * 1024 * 1024, backups: backups}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, fmt.Errorf("log writer is closed")
	}
	if w.size+int64(len(p)) > w.maxSize {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.file = f
	w.size = info.Size()
	return nil
}

func (w *RotatingWriter) rotate() error {
	if err := w.file.Close(); err != nil {
		return err
	}
	w.file = nil
	for i := w.backups - 1; i >= 1; i-- {
		oldName := fmt.Sprintf("%s.%d", w.path, i)
		newName := fmt.Sprintf("%s.%d", w.path, i+1)
		_ = os.Remove(newName)
		if _, err := os.Stat(oldName); err == nil {
			_ = os.Rename(oldName, newName)
		}
	}
	_ = os.Remove(w.path + ".1")
	if _, err := os.Stat(w.path); err == nil {
		if err := os.Rename(w.path, w.path+".1"); err != nil {
			return err
		}
	}
	return w.open()
}

var _ io.WriteCloser = (*RotatingWriter)(nil)
