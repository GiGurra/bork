package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

var cliFixture struct {
	dir, source string
	tool        string
	toolErr     error
	env         []string
	ordinary    cliFixtureBuild
	gated       cliFixtureBuild
}

type cliFixtureBuild struct {
	once sync.Once
	path string
	err  error
}

func TestMain(m *testing.M) {
	code := runCLITests(m)
	os.Exit(code)
}

func runCLITests(m *testing.M) (code int) {
	var err error
	cliFixture.dir, err = os.MkdirTemp("", "bork-cli-tests-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() {
		if err := os.RemoveAll(cliFixture.dir); err != nil {
			fmt.Fprintln(os.Stderr, err)
			code = 1
		}
	}()
	cliFixture.source, err = os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// Freeze the build environment before tests change their runtime settings.
	cliFixture.env = os.Environ()
	cliFixture.tool, cliFixture.toolErr = exec.LookPath("go")
	if cliFixture.toolErr == nil {
		cliFixture.tool, cliFixture.toolErr = filepath.Abs(cliFixture.tool)
	}
	return m.Run()
}

// One fresh native compiler per mode is enough for CLI assertions. Each call
// still executes a fresh process; no compiler survives this package's test run.
// Tests requiring distinct compiler images build their own executables.
func cliExecutable(t *testing.T, gated bool) string {
	t.Helper()
	if cliFixture.toolErr != nil {
		t.Fatal(cliFixture.toolErr)
	}
	build, name := &cliFixture.ordinary, "bork"
	if gated {
		build, name = &cliFixture.gated, "bork-test-cache"
	}
	build.once.Do(func() {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		build.path = filepath.Join(cliFixture.dir, name)
		args := []string{"build"}
		if gated {
			args = append(args, "-ldflags=-X github.com/GiGurra/bork/internal/driver.cacheTestGate=enabled")
		}
		args = append(args, "-o", build.path, ".")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, cliFixture.tool, args...)
		command.WaitDelay = time.Second
		command.Dir = cliFixture.source
		command.Env = append([]string(nil), cliFixture.env...)
		if output, err := command.CombinedOutput(); err != nil {
			build.err = fmt.Errorf("build CLI: %w\n%s", err, output)
		}
	})
	if build.err != nil {
		t.Fatal(build.err)
	}
	return build.path
}
