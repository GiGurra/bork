//go:build unix

package driver

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestExecutionGoFIFOReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "input")
	if err := os.WriteFile(path, []byte("regular"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := newGoExecutionCapture()
	if err := capture.file(path); err != nil {
		t.Fatal(err)
	}
	inputs := capture.finish()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	result := make(chan bool, 1)
	go func() { result <- inputs.current() }()
	select {
	case valid := <-result:
		if valid {
			t.Fatal("FIFO replacement qualified")
		}
	case <-time.After(time.Second):
		// Unblock a regressed blocking reader before reporting the failure.
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			_ = writer.Close()
		}
		t.Fatal("FIFO replacement blocked validation")
	}
}
