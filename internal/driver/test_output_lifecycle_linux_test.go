package driver

import (
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureOutputLifecycle(t *testing.T) {
	// Environment changes must remain sequential, including this parent.
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	source := t.TempDir()
	path := filepath.Join(source, "main.bork")
	native := captureGoContext()
	if native.err != nil {
		t.Fatal(native.err)
	}
	var retained string
	for _, tc := range []struct{ name, source, prepare string }{
		{"first build", "fn main(){println(1)}", ""},
		{"unchanged build executes again", "fn main(){println(1)}", ""},
		{"changed source", "fn main(){println(2)}", ""},
		{"removed target", "fn main(){println(2)}", "remove"},
		{"interrupted target", "fn main(){println(2)}", "partial"},
		{"changed environment", "fn main(){println(2)}", "environment"},
		{"changed launcher", "fn main(){println(2)}", "launcher"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Simulate leftovers from an earlier killed test process before locking.
			if tc.prepare == "partial" {
				if err := os.WriteFile(retained, []byte("interrupted link output"), 0700); err != nil {
					t.Fatal(err)
				}
			}

			// Fixed fixture identity even as this test changes source/tool inputs.
			base := filepath.Join(cache, "bork", "test-outputs-v1")
			out, ok := persistentFixtureOutput(t, base, "lifecycle fixture")
			if !ok {
				t.Fatal("persistent target unavailable")
			}
			if retained != "" && out != retained {
				t.Fatal("input change allocated another output slot")
			}
			retained = out
			switch tc.prepare {
			case "remove":
				if err := os.Remove(out); err != nil {
					t.Fatal(err)
				}
			case "environment":
				cgo := "0"
				if native.values["CGO_ENABLED"] == "0" {
					if _, err := exec.LookPath("gcc"); err != nil {
						t.Skip("CGO policy change needs a native C compiler")
					}
					cgo = "1"
				}
				t.Setenv("CGO_ENABLED", cgo)
			case "launcher":
				shim := t.TempDir()
				quoted := "'" + strings.ReplaceAll(native.tool, "'", "'\"'\"'") + "'"
				if err := os.WriteFile(filepath.Join(shim, "go"), []byte("#!/bin/sh\nif [ \"$1\" = build ]; then shift; exec "+quoted+" build -ldflags=-s=false \"$@\"; fi\nexec "+quoted+" \"$@\"\n"), 0700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			if err := Build(source, out); err != nil {
				t.Fatal(err)
			}
			metadata, err := buildinfo.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			settings := map[string]string{}
			for _, setting := range metadata.Settings {
				settings[setting.Key] = setting.Value
			}
			if tc.prepare == "environment" && settings["CGO_ENABLED"] != os.Getenv("CGO_ENABLED") {
				t.Fatalf("stale environment build metadata: %v", settings)
			}
			if tc.prepare == "launcher" && settings["-ldflags"] != "-s=false" {
				t.Fatalf("stale launcher build metadata: %v", settings)
			}
			info, err := os.Stat(out)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				t.Fatalf("fresh runnable target: %v %v", info, err)
			}
			result, err := exec.Command(out).CombinedOutput()
			want := "2\n"
			if strings.Contains(tc.source, "println(1)") {
				want = "1\n"
			}
			if err != nil || string(result) != want {
				t.Fatalf("fresh execution: %v %q; want %q", err, result, want)
			}
			root, err := os.OpenRoot(filepath.Dir(out))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			if lock, err := (cacheStore{root: filepath.Dir(out)}).tryLock(root, "LOCK"); err == nil {
				_ = lock.Close()
				t.Fatal("slot unlocked before execution cleanup")
			}
		})
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("successful output not retained: %v", err)
		}
	}
	base := filepath.Join(cache, "bork", "test-outputs-v1")
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 {
		t.Fatalf("slot count grew with inputs: %d %v", len(entries), err)
	}
	root, err := os.OpenRoot(filepath.Dir(retained))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	lock, err := (cacheStore{root: filepath.Dir(retained)}).tryLock(root, "LOCK")
	if err != nil {
		t.Fatal("slot remained locked after cleanup", err)
	}
	_ = lock.Close()
}

func TestFixtureOutputFailureAndFallback(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	source := t.TempDir()
	path := filepath.Join(source, "main.bork")
	// Same leaf makes two builds against the same slot; failed replacement removes
	// the old executable, and a repaired source still gets a fresh successful build.
	if err := os.WriteFile(path, []byte("fn main(){println(1)}"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := buildFixtureOutput(t, source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fn main(){undefinedName()}"), 0600); err != nil {
		t.Fatal(err)
	}
	// Do not acquire the slot again while this leaf holds it.
	if err := buildFixtureAt(source, out); err == nil {
		t.Fatal("malformed source accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("failed replacement retained target: %v", err)
	}
	if err := os.WriteFile(path, []byte("fn main(){println(2)}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Build(source, out); err != nil {
		t.Fatal(err)
	}
	result, err := exec.Command(out).CombinedOutput()
	if err != nil || string(result) != "2\n" {
		t.Fatalf("repaired source: %v %q", err, result)
	}
	t.Run("unavailable root", func(t *testing.T) {
		blocked := filepath.Join(t.TempDir(), "blocked")
		if err := os.WriteFile(blocked, nil, 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_CACHE_HOME", blocked)
		fallback, err := buildFixtureOutput(t, source)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(fallback, blocked) {
			t.Fatal("unavailable cache used")
		}
		result, err := exec.Command(fallback).CombinedOutput()
		if err != nil || string(result) != "2\n" {
			t.Fatalf("temporary fallback: %v %q", err, result)
		}
	})
}

func TestFixtureOutputRejectsAliases(t *testing.T) {
	base := filepath.Join(t.TempDir(), "outputs")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("untouched"), 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(outside), alias); err != nil {
		t.Fatal(err)
	}
	if _, ok := persistentFixtureOutput(t, alias, "fixture"); ok {
		t.Fatal("aliased root accepted")
	}
	slot := filepath.Join(base, fmt.Sprintf("%x", sha256.Sum256([]byte("fixture"))))
	if err := os.MkdirAll(slot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(slot, "program")); err != nil {
		t.Fatal(err)
	}
	if _, ok := persistentFixtureOutput(t, base, "fixture"); ok {
		t.Fatal("aliased target accepted")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("alias destination changed: %v %q", err, data)
	}
}
