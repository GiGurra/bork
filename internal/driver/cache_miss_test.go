package driver

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCacheMissSkipsBypassInventories(t *testing.T) {
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main() { println(comptime{1}) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reached := false
	src, _, artifact, err := compileCacheMiss(path, true, func(phase string) {
		if phase == "eligible-inventory" {
			reached = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if artifact != nil || reached {
		t.Fatal("evaluator bypass paid for inventories")
	}
	clean, err := Emit(path)
	if err != nil || !bytes.Equal(clean, src) {
		t.Fatalf("bypass output differs: %v", err)
	}
}

func TestCacheMissDeclinesChangesDuringLateCapture(t *testing.T) {
	for _, change := range []string{"environment", "source"} {
		t.Run(change, func(t *testing.T) {
			_ = receiptGoContext(t)
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte("fn main() {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			changed := false
			src, _, artifact, err := compileCacheMiss(path, true, func(phase string) {
				if phase != "eligible-inventory" {
					return
				}
				changed = true
				if change == "environment" {
					t.Setenv("BORK_CACHE_MISS_CHANGED", "yes")
				} else {
					if err := os.WriteFile(path, []byte("fn main() { println(1) }\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if !changed || len(src) == 0 || artifact != nil {
				t.Fatal("changed late inventory certified result")
			}
		})
	}
}

func TestCacheMissEligibleArtifact(t *testing.T) {
	_ = receiptGoContext(t)
	path := filepath.Join(t.TempDir(), "main.bork")
	if err := os.WriteFile(path, []byte("fn main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	src, _, artifact, err := compileCacheMiss(path, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil {
		t.Fatal("pure program ineligible")
	}
	if artifact.context.validation == nil || len(artifact.names) == 0 {
		t.Fatal("eligible result lacks late inventory")
	}
	for _, input := range artifact.names {
		if input.inputs == nil || !input.standard {
			t.Fatal("names not independently re-proven")
		}
	}
	if !bytes.Equal(src, artifact.goSrc) {
		t.Fatal("late proof changed output")
	}
}

func TestCacheMissOtherBypassesSkipInventories(t *testing.T) {
	for _, kind := range []string{"assets", "export-data", "go-flags", "custom-driver"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("GOPACKAGESDRIVER", "off")
			t.Setenv("GOTOOLCHAIN", "local")
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte("fn main() {}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "assets":
				path = "../../examples/embed"
			case "export-data":
				path = "../../testdata/cases/go_user_deps"
			case "go-flags":
				t.Setenv("GOFLAGS", "-tags=cachebypass")
			case "custom-driver":
				if runtime.GOOS == "windows" {
					t.Skip("shell package driver")
				}
				driver := filepath.Join(t.TempDir(), "driver")
				if err := os.WriteFile(driver, []byte("#!/bin/sh\nprintf '%s\\n' '{\"NotHandled\":true}'\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GOPACKAGESDRIVER", driver)
			}
			reached := false
			_, _, artifact, err := compileCacheMiss(path, true, func(phase string) {
				if phase == "eligible-inventory" {
					reached = true
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			if artifact != nil || reached {
				t.Fatalf("%s bypass paid for inventory", kind)
			}
		})
	}
}
