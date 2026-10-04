package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAutomaticCacheCLI(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("result publication image evidence currently Linux-only")
	}
	root := t.TempDir()
	exe := filepath.Join(root, "bork")
	if output, err := exec.Command("go", "build", "-ldflags=-X github.com/GiGurra/bork/internal/driver.cacheTestGate=enabled", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	t.Setenv("BORK_TEST_CACHE_PRODUCTION", "1")
	t.Setenv("BORK_TEST_CACHE_PUBLISH", "on")
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", "")
	t.Setenv("BORK_TEST_DISK_CACHE_BACKGROUND", "")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOPACKAGESDRIVER", "off")
	for _, mode := range []string{"check", "emit", "build", "off"} {
		t.Run(mode, func(t *testing.T) {
			sourceRoot := t.TempDir()
			source := filepath.Join(sourceRoot, "main.bork")
			if err := os.WriteFile(source, []byte("fn main(){println(42)}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(t.TempDir(), "cache")
			t.Setenv("BORKCACHE", cache)
			t.Setenv("BORK_CACHE", "on")
			if mode == "off" {
				t.Setenv("BORK_CACHE", "off")
			}
			probe := filepath.Join(t.TempDir(), "probe")
			t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
			notifyR, notifyW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = notifyR.Close(); _ = notifyW.Close() }()
			barrierR, barrierW, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = barrierR.Close(); _ = barrierW.Close() }()
			t.Setenv("BORK_TEST_CACHE_PUBLISH_NOTIFY_FD", "3")
			t.Setenv("BORK_TEST_CACHE_PUBLISH_BARRIER_FD", "4")

			outputPath := filepath.Join(t.TempDir(), "program")
			commandName := mode
			if mode == "off" {
				commandName = "check"
			}
			args := []string{commandName, source}
			if mode == "build" {
				args = append(args, "-o", outputPath)
			}
			invoke := func() []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, exe, args...)
				command.ExtraFiles = []*os.File{notifyW, barrierR}
				command.Env = os.Environ()
				command.WaitDelay = time.Second
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("CLI: %v\n%s", err, output)
				}
				return output
			}
			first := invoke()
			if mode == "off" {
				if data, err := os.ReadFile(probe); err == nil {
					t.Fatalf("disabled cache used result path: %s", data)
				}
				if _, err := os.Stat(filepath.Join(cache, "jobs")); !os.IsNotExist(err) {
					t.Fatal("disabled cache queued publication", err)
				}
				return
			}
			data, err := os.ReadFile(probe)
			if err != nil || string(data) != "queued\n" {
				t.Fatalf("automatic miss did not queue: %q %v", data, err)
			}
			if _, err := barrierW.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				var status [1]byte
				_, err := io.ReadFull(notifyR, status[:])
				if err == nil && status[0] != 1 {
					err = io.ErrUnexpectedEOF
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal("publisher failed", err)
				}
			case <-time.After(12 * time.Second):
				t.Fatal("publisher completion not bounded")
			}
			if mode == "build" {
				if err := os.Remove(outputPath); err != nil {
					t.Fatal(err)
				}
			}
			second := invoke()
			if string(first) != string(second) {
				t.Fatal("cached diagnostics/emission differ")
			}
			data, err = os.ReadFile(probe)
			if err != nil || string(data) != "queued\nhit\n" {
				t.Fatalf("automatic repeat missed: %q %v", data, err)
			}
			if mode == "build" {
				output, err := exec.Command(outputPath).CombinedOutput()
				if err != nil || strings.TrimSpace(string(output)) != "42" {
					t.Fatalf("hit did not rebuild a fresh executable: %q %v", output, err)
				}
			}
		})
	}
}
