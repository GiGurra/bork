package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/driver"
)

func TestCheckWatchJSONAndManualSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGHUP is a Unix retrigger")
	}
	dir := t.TempDir()
	exe := cliExecutable(t, false)
	path := filepath.Join(dir, "main.bork")
	if err := os.WriteFile(path, []byte("fn main() { println(missing) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "check", "--watch", "--json", path)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	decoder := json.NewDecoder(stdout)
	read := func(status string) driver.WatchResult {
		t.Helper()
		var result driver.WatchResult
		if err := decoder.Decode(&result); err != nil {
			t.Fatalf("decode watch: %v", err)
		}
		if result.SchemaVersion != 1 || result.Status != status || result.Diagnostics == nil {
			t.Fatalf("bad watch result: %+v", result)
		}
		return result
	}
	first := read("error")
	if len(first.Diagnostics) != 1 || first.Diagnostics[0].Code != "type.error" {
		t.Fatalf("lost diagnostics: %+v", first)
	}
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := read("ok")
	if second.RequestID <= first.RequestID {
		t.Fatal("request IDs did not advance")
	}
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	third := read("ok")
	if third.RequestID <= second.RequestID {
		t.Fatal("manual retrigger did not advance request")
	}
	if err := cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watch stop: %v", err)
	}
}

func TestWatchStopsOnOutputFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := runCheckWatch(ctx, path, true, failingWatchOutput{}); err == nil {
		t.Fatal("output error ignored")
	}
}

type failingWatchOutput struct{}

func (failingWatchOutput) Write([]byte) (int, error) {
	return 0, fmt.Errorf("%w: closed watch output", io.ErrClosedPipe)
}
