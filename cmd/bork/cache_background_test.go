//go:build linux || darwin

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiskCacheBackgroundPublication(t *testing.T) {
	version, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(version), "go1.26") && !strings.HasPrefix(string(version), "go1.27") {
		t.Skip("unsupported inventory version")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("BORK_TEST_DISK_CACHE_BACKGROUND", "1")
	t.Setenv("BORK_TEST_CACHE_PUBLISH", "on")
	t.Setenv("BORK_TEST_CACHE_PUBLISH_NOTIFY_FD", "3")
	t.Setenv("BORK_TEST_CACHE_PUBLISH_BARRIER_FD", "4")
	root := t.TempDir()
	exe := cliExecutable(t, true)
	program := filepath.Join(root, "main.bork")
	original := []byte("fn main(){ println(1) }\n")
	if err := os.WriteFile(program, original, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", "")
	clean, err := exec.Command(exe, "emit", program).CombinedOutput()
	if err != nil {
		t.Fatalf("clean: %v\n%s", err, clean)
	}
	probe := filepath.Join(root, "probe")
	t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
	for _, scenario := range []string{"publish", "source-change", "timeout", "unwritable", "off"} {
		t.Run(scenario, func(t *testing.T) {
			if err := os.WriteFile(program, original, 0600); err != nil {
				t.Fatal(err)
			}
			cache := filepath.Join(root, scenario)
			t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
			if scenario == "unwritable" {
				if err := os.WriteFile(cache, []byte("file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "off" {
				t.Setenv("BORK_CACHE", "off")
			}
			if scenario == "timeout" {
				t.Setenv("BORK_TEST_CACHE_PUBLISH_TIMEOUT_MS", "1000")
			}
			_ = os.Remove(probe)
			notifyRead, notifyWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = notifyRead.Close() }()
			barrierRead, barrierWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = barrierWrite.Close() }()
			cmd := exec.Command(exe, "emit", program)
			cmd.ExtraFiles = []*os.File{notifyWrite, barrierRead}
			output, err := cmd.CombinedOutput()
			_ = notifyWrite.Close()
			_ = barrierRead.Close()
			if err != nil || !bytes.Equal(clean, output) {
				t.Fatalf("parent parity: %v\n%s", err, output)
			}
			// The parent has exited while its child's explicit barrier remains closed.
			status, err := os.ReadFile(probe)
			if scenario == "off" {
				if !os.IsNotExist(err) {
					t.Fatalf("opt-out probe: %q %v", status, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if scenario == "publish" || scenario == "source-change" || scenario == "timeout" {
				if string(status) != "queued\n" {
					t.Fatalf("queue status: %q", status)
				}
			} else if scenario == "unwritable" && string(status) != "publish-skip\n" {
				t.Fatalf("unwritable status: %q", status)
			}
			if scenario == "publish" {
				// The first worker holds this request's admission slot at the
				// barrier. A concurrent miss must return without spawning another.
				if err := os.Truncate(probe, 0); err != nil {
					t.Fatal(err)
				}
				second, err := exec.Command(exe, "emit", program).CombinedOutput()
				if err != nil || !bytes.Equal(second, clean) {
					t.Fatalf("busy-slot output: %v", err)
				}
				busy, err := os.ReadFile(probe)
				if err != nil || string(busy) != "publish-skip\n" {
					t.Fatalf("busy-slot status: %q %v", busy, err)
				}
			}
			if scenario == "source-change" {
				if err := os.WriteFile(program, []byte("fn main(){ println(2) }\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario != "timeout" {
				if _, err := barrierWrite.Write([]byte{1}); err != nil && scenario != "off" && scenario != "unwritable" {
					t.Fatal(err)
				}
			}
			if err := notifyRead.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			var completion [1]byte
			_, err = io.ReadFull(notifyRead, completion[:])
			switch scenario {
			case "publish":
				if err != nil || completion[0] != 1 {
					t.Fatalf("publication: %v %v", completion, err)
				}
			case "source-change":
				if err != nil || completion[0] != 0 {
					t.Fatalf("changed-input decline: %v %v", completion, err)
				}
			default:
				if err != io.EOF {
					t.Fatalf("no surviving worker: %v", err)
				}
			}
			if scenario == "publish" {
				_ = os.Truncate(probe, 0)
				out, err := exec.Command(exe, "emit", program).CombinedOutput()
				if err != nil || !bytes.Equal(out, clean) {
					t.Fatalf("hit parity: %v", err)
				}
				status, err := os.ReadFile(probe)
				if err != nil || string(status) != "hit\n" {
					t.Fatalf("fresh process hit: %q %v", status, err)
				}
			}
		})
	}
}
