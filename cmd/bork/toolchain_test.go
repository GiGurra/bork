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

	"github.com/GiGurra/bork/internal/testutil"
	"github.com/GiGurra/bork/internal/toolchain"
	"github.com/spf13/cobra"
)

func TestToolchainCLI(t *testing.T) {
	exe := cliExecutable(t, false)
	fixture := testutil.Compiler(t, "v0.4.2")
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	// Seed the exact-version installation using its real module metadata.
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(cache, "toolchains", runtime.GOOS+"-"+runtime.GOARCH, "v0.4.2", filepath.Base(fixture))
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := toolchain.Ensure(context.Background(), cache, "v0.4.2", io.Discard); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORKTOOLCHAIN", "v0.4.2")
	t.Setenv(toolchain.SelectedEnv, "")
	t.Setenv(toolchain.ReasonEnv, "")
	cmd := exec.Command(exe, "run", "program.bork", "--", "hello")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "args=run program.bork -- hello") || !strings.Contains(string(out), "selected=v0.4.2") || !strings.Contains(string(out), "selected by BORKTOOLCHAIN=v0.4.2") {
		t.Fatalf("switch: %v\n%s", err, out)
	}
	t.Setenv("FIXTURE_EXIT", "7")
	cmd = exec.Command(exe, "check", "program.bork")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || cmd.ProcessState.ExitCode() != 7 {
		t.Fatalf("child exit: %v\n%s", err, out)
	}
	t.Setenv("BORKTOOLCHAIN", "local")
	cmd = exec.Command(exe, "version")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "bork dev (local compiler)") {
		t.Fatalf("local version: %v\n%s", err, out)
	}
	cmd = exec.Command(exe, "env", "BORKVERSION", "BORKTOOLCHAIN")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "BORKVERSION=\"dev\" # local compiler") || !strings.Contains(string(out), "BORKTOOLCHAIN=\"local\"") {
		t.Fatalf("local env: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "bork.mod"), []byte("module example.com/app\nbork 0.4\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(exe, "version")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "selected by "+filepath.Join(dir, "bork.mod")) {
		t.Fatalf("project reason: %v\n%s", err, out)
	}
	oldCompiler := filepath.Join(dir, "old-bork"+filepath.Ext(exe))
	build := exec.Command(cliFixture.tool, "build", "-ldflags=-X main.releaseVersion=v0.3.9", "-o", oldCompiler, ".")
	build.Dir, build.Env = cliFixture.source, cliFixture.env
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build old compiler: %v\n%s", err, out)
	}
	cmd = exec.Command(oldCompiler, "version")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "BORKTOOLCHAIN=local") || !strings.Contains(string(out), "requires bork v0.4.0") {
		t.Fatalf("local minimum: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(cache, "toolchains", runtime.GOOS+"-"+runtime.GOARCH, "v0.4.version"), []byte("v0.4.2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORKTOOLCHAIN", "auto")
	t.Setenv("FIXTURE_EXIT", "")
	cmd = exec.Command(oldCompiler, "version")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), "fixture v0.4.2") || !strings.Contains(string(out), "selected by "+filepath.Join(dir, "bork.mod")) {
		t.Fatalf("automatic minimum switch: %v\n%s", err, out)
	}
	t.Setenv(toolchain.SelectedEnv, "v0.9.0")
	cmd = exec.Command(exe, "version")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "switching loop") {
		t.Fatalf("reentry guard: %v\n%s", err, out)
	}
}

func TestToolchainTarget(t *testing.T) {
	cmd := &cobra.Command{Use: "describe"}
	path, enabled := toolchainTarget(cmd, []string{`C:\project\main.bork:2:10`})
	if !enabled || path != `C:\project\main.bork` {
		t.Fatalf("describe target: %s, %t", path, enabled)
	}
	cmd = envCommand()
	if err := cmd.Flags().Set("write", "true"); err != nil {
		t.Fatal(err)
	}
	if _, enabled := toolchainTarget(cmd, nil); enabled {
		t.Fatal("writing settings must stay local")
	}
	cmd = &cobra.Command{Use: "get"}
	cmd.Flags().String("path", "/project", "")
	parent := &cobra.Command{Use: "deps"}
	parent.AddCommand(cmd)
	if path, enabled := toolchainTarget(cmd, []string{"example.com/lib@latest"}); !enabled || path != "/project" {
		t.Fatalf("deps target: %s, %t", path, enabled)
	}
}
