// Package toolchain selects and caches immutable compiler installations.
package toolchain

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/manifest"
	"golang.org/x/mod/semver"
)

const ReasonEnv = "BORK_TOOLCHAIN_REASON"
const SelectedEnv = "BORK_TOOLCHAIN_SELECTED"

type Selection struct {
	Version string
	Reason  string
	Query   string
}

func Requirement(path string) (manifest.Version, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return manifest.Version{}, "", err
	}
	info, err := os.Stat(abs)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return manifest.Version{}, "", err
	}
	if err == nil && !info.IsDir() || err != nil && strings.HasSuffix(abs, ".bork") {
		abs = filepath.Dir(abs)
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		file := filepath.Join(dir, "bork.mod")
		data, err := os.ReadFile(file)
		if err == nil {
			version, err := manifest.CompilerVersion(data)
			if err != nil {
				return version, file, fmt.Errorf("%s: %w", file, err)
			}
			return version, file, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return manifest.Version{}, file, err
		}
		if filepath.Dir(dir) == dir {
			return manifest.Version{}, "", nil
		}
	}
}

func Select(current, setting, path string) (Selection, error) {
	required, file, err := Requirement(path)
	if err != nil {
		return Selection{}, err
	}
	selection := Selection{Version: current, Reason: "local compiler"}
	if setting == "auto" || setting == "local" {
		if required.Query == "" {
			return selection, nil
		}
		selection.Reason = fmt.Sprintf("selected by %s: bork %s", file, strings.TrimPrefix(required.Query, "v"))
		// Development builds track unreleased main and remain local in auto mode.
		if current == "dev" || semver.Compare(current, required.Minimum) >= 0 {
			return selection, nil
		}
		if setting == "local" {
			return Selection{}, fmt.Errorf("%s requires bork %s or newer; running %s with BORKTOOLCHAIN=local", file, required.Minimum, current)
		}
		selection.Query = required.Query
		return selection, nil
	}
	override, err := manifest.ParseVersion(setting)
	if err != nil {
		return Selection{}, err
	}
	if override.Query != override.Minimum {
		return Selection{}, fmt.Errorf("BORKTOOLCHAIN must pin a full version such as v0.4.2")
	}
	if required.Minimum != "" && semver.Compare(override.Minimum, required.Minimum) < 0 {
		return Selection{}, fmt.Errorf("BORKTOOLCHAIN=%s is below %s requirement bork %s", setting, file, required.Minimum)
	}
	selection.Reason = "selected by BORKTOOLCHAIN=" + setting
	if current != override.Minimum {
		selection.Query = override.Query
	}
	return selection, nil
}

func compilerVersion(path string) (string, error) {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return "", err
	}
	if info.Main.Path != "github.com/GiGurra/bork" || !semver.IsValid(info.Main.Version) {
		return "", fmt.Errorf("%s is not a released bork compiler", path)
	}
	return info.Main.Version, nil
}

func cacheDir(root string) string {
	return filepath.Join(root, "toolchains", runtime.GOOS+"-"+runtime.GOARCH)
}

func executable(dir string) string {
	name := "bork"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// Ensure resolves a query once and publishes a verified, immutable compiler.
// Minor queries retain their first resolution until toolchain caches are cleaned.
func Ensure(ctx context.Context, root, query string, output io.Writer) (path, version string, err error) {
	parsed, err := manifest.ParseVersion(query)
	if err != nil {
		return "", "", err
	}
	dir := cacheDir(root)
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", "", err
	}
	release, err := acquire(ctx, filepath.Join(root, "toolchains.lock"))
	if err != nil {
		return "", "", err
	}
	defer release()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", "", err
	}
	resolved := query
	if query != parsed.Minimum {
		data, readErr := os.ReadFile(filepath.Join(dir, query+".version"))
		if readErr == nil {
			resolved = strings.TrimSpace(string(data))
			if !semver.IsValid(resolved) || !strings.HasPrefix(resolved, query+".") {
				return "", "", fmt.Errorf("invalid cached toolchain resolution for %s", query)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return "", "", readErr
		}
	}
	path = executable(filepath.Join(dir, resolved))
	if version, err := compilerVersion(path); err == nil {
		if version != resolved {
			return "", "", fmt.Errorf("cached compiler %s reports %s", path, version)
		}
		return path, version, nil
	} else if _, statErr := os.Stat(path); statErr == nil {
		return "", "", fmt.Errorf("cached compiler is invalid: %w; run bork clean --all to remove it", err)
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", "", fmt.Errorf("switching to bork %s requires Go on PATH: %w", query, err)
	}
	stage, err := os.MkdirTemp(dir, ".install-")
	if err != nil {
		return "", "", err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	cmd := exec.CommandContext(ctx, goPath, "install", "github.com/GiGurra/bork/cmd/bork@"+query)
	cmd.Env = append(os.Environ(), "GOBIN="+stage, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("cannot install bork %s: %w; check Go/module proxy access; offline switching requires a cached compiler or cached Go modules", query, err)
	}
	version, err = compilerVersion(executable(stage))
	if err != nil {
		return "", "", fmt.Errorf("verify downloaded compiler: %w", err)
	}
	if version != parsed.Minimum && (query == parsed.Minimum || !strings.HasPrefix(version, query+".") || semver.Compare(version, parsed.Minimum) < 0) {
		return "", "", fmt.Errorf("requested bork %s but Go installed %s", query, version)
	}
	destination := filepath.Join(dir, version)
	path = executable(destination)
	if existing, err := compilerVersion(path); err == nil && existing == version {
		// Another query can resolve to the same immutable version.
	} else if err := os.Rename(stage, destination); err != nil {
		return "", "", err
	}
	if query != parsed.Minimum {
		if err := os.WriteFile(filepath.Join(dir, query+".version"), []byte(version+"\n"), 0600); err != nil {
			return "", "", err
		}
	}
	return path, version, nil
}

func acquire(ctx context.Context, path string) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return nil, err
		}
		if err := tryLock(file); err == nil {
			return func() { _ = file.Close() }, nil
		} else if !lockBusy(err) {
			_ = file.Close()
			return nil, err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func Clean(ctx context.Context, root string) error {
	if _, err := os.Stat(filepath.Join(root, "toolchains")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	release, err := acquire(ctx, filepath.Join(root, "toolchains.lock"))
	if err != nil {
		return err
	}
	defer release()
	return os.RemoveAll(filepath.Join(root, "toolchains"))
}
