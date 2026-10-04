//go:build linux

package driver

import (
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCachedTestLinks(t *testing.T) {
	native := captureGoContext()
	if goBuildCommandHook == nil {
		t.Skip("native executable cache helper unavailable")
	}
	t.Run("fresh executable and source positions", func(t *testing.T) {
		dir := t.TempDir()
		source := filepath.Join(dir, "main.go")
		if err := os.WriteFile(source, []byte("package main\nfunc main(){panic(\"fresh execution\")}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module link.test\n\ngo 1.24\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"first", "second"} {
			normal := linkContractBuild(native, dir, filepath.Join(dir, name))
			cached, retry, cleanup := goBuildCommandHook(native, "comptime", normal)
			if cached == nil {
				t.Fatal("supported native command declined")
			}
			originalArgs, originalEnv := slices.Clone(normal.Args), slices.Clone(normal.Env)
			output, err := cached.CombinedOutput()
			if err != nil {
				cleanup()
				t.Fatalf("cached command: %v: %s; retry=%v", err, output, retry(output))
			}
			cleanup()
			if !slices.Equal(originalArgs, normal.Args) || !slices.Equal(originalEnv, normal.Env) {
				t.Fatal("fallback command mutated")
			}
			info, err := os.Stat(normal.Args[5])
			if err != nil || info.Mode().Perm()&0111 == 0 {
				t.Fatalf("fresh executable: %v %v", info, err)
			}
			binary, err := elf.Open(normal.Args[5])
			if err != nil {
				t.Fatal(err)
			}
			if binary.Section(".debug_info") == nil {
				_ = binary.Close()
				t.Fatal("debug information omitted")
			}
			_ = binary.Close()
			output, err = exec.Command(normal.Args[5]).CombinedOutput()
			if err == nil || !strings.Contains(string(output), source+":2") || !strings.Contains(string(output), "fresh execution") {
				t.Fatalf("panic source position: %v: %s", err, output)
			}
		}
		assertNoCopyStatus(t, dir)
	})
	t.Run("compiler failure is not retried", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module link.test\n\ngo 1.24\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){copy_failed_diagnostic()}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		normal := linkContractBuild(native, dir, filepath.Join(dir, "program"))
		cached, retry, cleanup := goBuildCommandHook(native, "predicate", normal)
		if cached == nil {
			t.Fatal("supported native command declined")
		}
		output, err := cached.CombinedOutput()
		if err == nil || retry(output) || !strings.Contains(string(output), "undefined: copy_failed_diagnostic") {
			cleanup()
			t.Fatalf("compiler failure contract: %v: %s", err, output)
		}
		cleanup()
		assertNoCopyStatus(t, dir)
	})
	t.Run("copier failure allows ordinary build", func(t *testing.T) {
		dir := t.TempDir()
		copier := filepath.Join(dir, "copier")
		if err := os.WriteFile(copier, []byte("#!/bin/sh\nprintf copy-failed > \"$3\"\nexit 1\n"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module link.test\n\ngo 1.24\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		factory := cachedTestLinkFactory(native, copier)
		normal := linkContractBuild(native, dir, filepath.Join(dir, "program"))
		cached, retry, cleanup := factory(native, "test", normal)
		if cached == nil {
			t.Fatal("command declined")
		}
		output, err := cached.CombinedOutput()
		if err == nil || !retry(output) {
			cleanup()
			t.Fatalf("infrastructure failure contract: %v: %s", err, output)
		}
		output, err = normal.CombinedOutput()
		cleanup()
		if err != nil {
			t.Fatalf("plain fallback: %v: %s", err, output)
		}
		assertNoCopyStatus(t, dir)
	})
	t.Run("missing copier and unsupported commands", func(t *testing.T) {
		dir := t.TempDir()
		normal := linkContractBuild(native, dir, filepath.Join(dir, "program"))
		factory := cachedTestLinkFactory(native, filepath.Join(dir, "absent"))
		if cmd, _, _ := factory(native, "test", normal); cmd != nil {
			t.Fatal("missing copier accepted")
		}
		if cmd, _, _ := goBuildCommandHook(native, "program", normal); cmd != nil {
			t.Fatal("public Build intercepted")
		}
		deadline, cancel := context.WithCancel(context.Background())
		cancel()
		normal = exec.CommandContext(deadline, native.tool, "build", "-mod=readonly", "-buildvcs=false", "-o", filepath.Join(dir, "program"), ".")
		if cmd, _, _ := goBuildCommandHook(native, "test", normal); cmd != nil {
			t.Fatal("cancellation contract intercepted")
		}
		assertNoCopyStatus(t, dir)
	})
}

func linkContractBuild(native *goContext, dir, out string) *exec.Cmd {
	cmd := native.command("build", "-mod=readonly", "-buildvcs=false", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=")
	return cmd
}
func assertNoCopyStatus(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".copy-result-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("copy status leaked: %v %v", matches, err)
	}
}
