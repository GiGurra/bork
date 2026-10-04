package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/toolenv"
)

func TestEnvAndInstallCLI(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "bork")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	// Keep Go's cache and HOME intact while isolating only bork settings.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	for _, name := range toolenv.Names() {
		t.Setenv(name, "")
	}
	run := func(success bool, args ...string) string {
		t.Helper()
		out, err := exec.Command(exe, args...).CombinedOutput()
		if (err == nil) != success {
			t.Fatalf("bork %q: %v\n%s", args, err, out)
		}
		return string(out)
	}
	bin := filepath.Join(dir, "installed")
	cache := filepath.Join(dir, "cache")
	run(true, "env", "-w", "BORKBIN="+bin, "BORKCACHE="+cache, "BORK_CACHE=off")
	var values map[string]toolenv.Setting
	out := run(true, "env", "-json")
	if err := json.Unmarshal([]byte(out), &values); err != nil {
		t.Fatalf("env JSON: %v\n%s", err, out)
	}
	if values["BORKBIN"] != (toolenv.Setting{Value: bin, Source: "config"}) || values["BORK_CACHE"].Value != "off" {
		t.Fatalf("env settings: %+v", values)
	}
	if out := run(true, "env", "BORKBIN"); !strings.Contains(out, bin) || !strings.Contains(out, "# config") {
		t.Fatalf("env text: %s", out)
	}
	run(false, "env", "-w", "BORKBIN=relative")
	run(false, "env", "-w", "-u", "BORKBIN")
	run(false, "env", "--json", "-w", "BORK_CACHE=on")
	run(false, "env", "UNKNOWN")
	run(false, "env", "-u")
	program := filepath.Join(dir, "hello.bork")
	if err := os.WriteFile(program, []byte("fn main() { println(\"installed\") }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(true, "install", program)
	installed := filepath.Join(bin, "hello")
	output, err := exec.Command(installed).CombinedOutput()
	if err != nil || string(output) != "installed\n" {
		t.Fatalf("installed binary: %v %s", err, output)
	}

	packageDir := filepath.Join(dir, "directory-app")
	if err := os.Mkdir(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(program)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDir, "main.bork"), source, 0600); err != nil {
		t.Fatal(err)
	}
	defaultInstall := exec.Command(exe, "install")
	defaultInstall.Dir = packageDir
	if output, err := defaultInstall.CombinedOutput(); err != nil {
		t.Fatalf("install default path: %v %s", err, output)
	}
	if output, err := exec.Command(filepath.Join(bin, "directory-app")).CombinedOutput(); err != nil || string(output) != "installed\n" {
		t.Fatalf("installed directory: %v %s", err, output)
	}
	if err := os.Remove(filepath.Join(bin, "directory-app")); err != nil {
		t.Fatal(err)
	}

	// A saved root must be used by clean even while cache reuse is off.
	legacy := filepath.Join(cache, "stage", "v1", strings.Repeat("a", 64), "tree", "main.go")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	run(true, "clean", "--all")
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("clean ignored saved BORKCACHE: %v", err)
	}
	before, err := os.ReadFile(installed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(program, []byte("fn main() { missing() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(false, "install", program)
	after, err := os.ReadFile(installed)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed install replaced executable: %v", err)
	}
	entries, err := os.ReadDir(bin)
	if err != nil || len(entries) != 1 {
		t.Fatalf("installation debris: %v %v", entries, err)
	}
	t.Setenv("BORKBIN", filepath.Join(dir, "environment-bin"))
	if out := run(true, "env", "-w", "BORKBIN="+bin); !strings.Contains(out, "overridden by the environment") {
		t.Fatalf("missing override warning: %s", out)
	}
	run(true, "env", "-u", "BORKBIN")
	out = run(true, "env", "--json", "BORKBIN")
	if err := json.Unmarshal([]byte(out), &values); err != nil || values["BORKBIN"].Source != "environment" {
		t.Fatalf("unset lost environment: %s %v", out, err)
	}
}
