package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/GiGurra/bork/internal/gen"
)

// DelveVersion pins the optional debugger independently of compiler releases.
const DelveVersion = "v1.27.2"

// BuildDebug retains generated source next to out, and disables optimization and
// inlining so Delve can inspect source variables and step through bork statements.
func BuildDebug(path, out string) error {
	program, err := checkProgramObserved(path, nil)
	if err != nil {
		return err
	}
	if err := program.requireMain(); err != nil {
		return err
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	dir := absOut + ".bork-debug"
	source, debugMap, err := gen.DebugPackageMap(program.files, program.info, filepath.Join(dir, "main.go"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	pinned, err := writeGoStage(dir, source, program.module, program.info.Embeds, program.context.moduleHook)
	if err != nil {
		return err
	}
	metadata, err := json.MarshalIndent(debugMap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "debug-map.json"), append(metadata, '\n'), 0644); err != nil {
		return err
	}
	return buildStagedGoOptions(program.files, absOut, dir, pinned, program.context, nil, "-gcflags=all=-N -l")
}

func delveCachePath() (string, error) {
	base, err := cacheRootDir()
	if err != nil {
		return "", err
	}
	name := "dlv"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(base, "tools", "delve", DelveVersion, name), nil
}

// FindDelve honors an explicit executable, then the pinned installation, then PATH.
func FindDelve(explicit string) (string, error) {
	if explicit != "" {
		return exec.LookPath(explicit)
	}
	cached, err := delveCachePath()
	if err != nil {
		return "", err
	}
	if executable, err := exec.LookPath(cached); err == nil {
		return executable, nil
	}
	if executable, err := exec.LookPath("dlv"); err == nil {
		return executable, nil
	}
	return "", fmt.Errorf("debugger not installed; run `bork debug setup` to install the pinned debugger %s into BORKCACHE", DelveVersion)
}

// SetupDelve installs the pinned debugger using Go, and prints its executable path.
func SetupDelve(ctx context.Context, output io.Writer) error {
	binary, err := delveCachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		return err
	}
	cmd := captureGoContext().command("install", "github.com/go-delve/delve/cmd/dlv@"+DelveVersion)
	cmd = commandContext(ctx, cmd)
	cmd.Dir = filepath.Dir(binary)
	cmd.Env = append(cmd.Env, "GOBIN="+filepath.Dir(binary), "GOFLAGS=", "GOWORK=off", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	configureEvaluationProcess(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installing debugger: %w", err)
	}
	_, err = fmt.Fprintln(output, binary)
	return err
}

// DebugDAP relays DAP to Delve on a loopback TCP listener. The adapter prints
// its actual listener address, including the selected port when listen ends in :0.
func DebugDAP(ctx context.Context, explicit, listen string, stdout, stderr io.Writer) error {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("debug listener: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("debug listener must use a loopback IP address")
	}
	binary, err := FindDelve(explicit)
	if err != nil {
		return err
	}
	return debugDAPRelay(ctx, binary, listen, stdout, stderr)
}

func commandContext(ctx context.Context, original *exec.Cmd) *exec.Cmd {
	cmd := exec.CommandContext(ctx, original.Path, original.Args[1:]...)
	cmd.Env = original.Env
	return cmd
}
