package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpgrade(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Go uses a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	fakeGo := filepath.Join(dir, "go")
	log := filepath.Join(dir, "args")
	fixture, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n/usr/bin/printf '%s\\n' \"$@\" \"$GOBIN\" \"$GOOS\" \"$GOARCH\" > \"$UPGRADE_LOG\"\nif [ \"$UPGRADE_FAIL\" = 1 ]; then echo 'proxy unavailable' >&2; exit 1; fi\n/bin/mkdir -p \"$GOBIN\"\n/bin/cp \"$UPGRADE_FIXTURE\" \"$GOBIN/bork\"\n"
	if err := os.WriteFile(fakeGo, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("BORKBIN", bin)
	t.Setenv("UPGRADE_LOG", log)
	t.Setenv("UPGRADE_FIXTURE", fixture)
	t.Setenv("GOOS", "not-the-host")
	t.Setenv("GOARCH", "not-the-host")
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := upgradeCommand()
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return output.String(), err
	}
	for _, requested := range []string{"", "v0.4.0", "v0.4.0-rc.1"} {
		args := []string{}
		if requested != "" {
			args = append(args, requested)
		} else {
			requested = "latest"
		}
		out, err := run(args...)
		if err != nil || !strings.Contains(out, "bork dev -> dev") || !strings.Contains(out, "not on PATH") || !strings.Contains(out, "Running bork from") {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		data, err := os.ReadFile(log)
		want := "install\ngithub.com/GiGurra/bork/cmd/bork@" + requested + "\n" + bin + "\n" + runtime.GOOS + "\n" + runtime.GOARCH + "\n"
		if err != nil || string(data) != want {
			t.Fatalf("go invocation: %q, want %q (%v)", data, want, err)
		}
	}
	for _, args := range [][]string{{"nonsense"}, {"v0.4.0", "extra"}} {
		if out, err := run(args...); err == nil {
			t.Fatalf("invalid arguments succeeded: %v: %s", args, out)
		}
	}
	t.Setenv("UPGRADE_FAIL", "1")
	before, err := os.ReadFile(filepath.Join(bin, "bork"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := run()
	if err == nil || !strings.Contains(err.Error(), "offline") || !strings.Contains(out, "proxy unavailable") {
		t.Fatalf("failed install: %v\n%s", err, out)
	}
	after, err := os.ReadFile(filepath.Join(bin, "bork"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed install changed existing binary: %v", err)
	}
	t.Setenv("PATH", bin)
	if _, err := run(); err == nil || !strings.Contains(err.Error(), "requires Go on PATH") {
		t.Fatalf("missing Go: %v", err)
	}
}

func TestSamePath(t *testing.T) {
	dir := t.TempDir()
	if !samePath(dir, filepath.Join(dir, ".")) || samePath(dir, filepath.Join(dir, "absent")) {
		t.Fatal("path comparison")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skip(err)
	}
	if !samePath(dir, link) {
		t.Fatal("symlink should refer to the same location")
	}
}
