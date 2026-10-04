package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCacheTrimDetached(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("platform uses temporary staging")
	}
	root := t.TempDir()
	exe := filepath.Join(root, "bork")
	if output, err := exec.Command("go", "build", "-ldflags=-X github.com/GiGurra/bork/internal/driver.cacheTestGate=enabled", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	telemetry := filepath.Join(root, "telemetry")
	if err := os.Mkdir(telemetry, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(telemetry, "mode"), []byte("off\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_TELEMETRY_DIR", telemetry)
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", "")
	t.Setenv("BORK_TEST_CACHE_TRIM", "on")
	t.Setenv("BORK_TEST_CACHE_TRIM_TIMEOUT_MS", "")
	source := filepath.Join(root, "main.bork")
	if err := os.WriteFile(source, []byte("fn main(){}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"on", "off", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			cache := filepath.Join(t.TempDir(), "cache")
			key := fmt.Sprintf("%x", sha256.Sum256([]byte("cold staging fixture")))
			oldStage := filepath.Join(cache, "stage", "v3", key[:2], key)
			if err := os.MkdirAll(filepath.Join(oldStage, "tree"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(oldStage, "tree", "main.go"), []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(oldStage, "used"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-7 * 24 * time.Hour)
			if err := os.Chtimes(filepath.Join(oldStage, "used"), old, old); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BORKCACHE", cache)
			t.Setenv("BORK_CACHE", "on")
			if mode == "off" {
				t.Setenv("BORK_CACHE", "off")
			}
			if mode == "timeout" {
				t.Setenv("BORK_TEST_CACHE_TRIM_TIMEOUT_MS", "150")
			}
			notifyRead, notifyWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = notifyRead.Close(); _ = notifyWrite.Close() }()
			barrierRead, barrierWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = barrierRead.Close(); _ = barrierWrite.Close() }()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, exe, "build", source, "-o", filepath.Join(t.TempDir(), "program"))
			command.ExtraFiles = []*os.File{notifyWrite, barrierRead}
			command.Env = append(os.Environ(), "BORK_TEST_CACHE_TRIM_NOTIFY_FD=3", "BORK_TEST_CACHE_TRIM_BARRIER_FD=4")
			command.WaitDelay = time.Second
			output, err := command.CombinedOutput()
			_ = notifyWrite.Close()
			_ = barrierRead.Close()
			if err != nil {
				t.Fatalf("parent must finish while worker is blocked: %v\n%s", err, output)
			}
			if _, err := os.Stat(oldStage); err != nil {
				t.Fatal("worker did not wait for barrier", err)
			}
			if mode == "on" {
				if _, err := barrierWrite.Write([]byte{1}); err != nil {
					t.Fatal(err)
				}
			}
			type completion struct {
				value byte
				err   error
			}
			result := make(chan completion, 1)
			go func() {
				var status [1]byte
				_, err := io.ReadFull(notifyRead, status[:])
				result <- completion{status[0], err}
			}()
			select {
			case status := <-result:
				if mode == "on" {
					if status.err != nil || status.value != 1 {
						t.Fatal("worker failed", status)
					}
					if _, err := os.Stat(oldStage); !os.IsNotExist(err) {
						t.Fatal("old staging not removed", err)
					}
				} else {
					if status.err != io.EOF {
						t.Fatal("disabled/timed-out worker unexpectedly completed", status)
					}
					if _, err := os.Stat(oldStage); err != nil {
						t.Fatal("disabled/timed-out worker changed cache", err)
					}
				}
			case <-time.After(4 * time.Second):
				t.Fatal("maintenance completion was not bounded")
			}
		})
	}
}
