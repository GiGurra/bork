package driver

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/std"
	"golang.org/x/mod/modfile"
)

// Deps manages user Go manifests in the nearest bork module. Go runs in a
// temporary module so failed resolution cannot rewrite the user's manifests.
func Deps(path, action string, packages []string) error {
	switch action {
	case "init", "download":
		if len(packages) != 0 {
			return fmt.Errorf("deps %s takes no package arguments", action)
		}
	case "get":
		if len(packages) == 0 {
			return errors.New("deps get needs at least one Go package or module")
		}
		for _, p := range packages {
			if strings.HasPrefix(p, "-") || strings.HasPrefix(p, ".") || strings.ContainsAny(p, "\\ \t\r\n") || p == "go" || p == "toolchain" || strings.HasPrefix(p, "go@") || strings.HasPrefix(p, "toolchain@") {
				return fmt.Errorf("invalid Go dependency query %q: use a package or module path, optionally followed by @version", p)
			}
		}
	default:
		return fmt.Errorf("unknown deps action %q", action)
	}
	mod, err := findModule(path)
	if err != nil {
		return err
	}
	if mod.path == "" {
		return errors.New("deps needs a bork.mod in the directory or a parent")
	}
	modPath := filepath.Join(mod.root, "go-deps.mod")
	sumPath := filepath.Join(mod.root, "go-deps.sum")
	data, err := os.ReadFile(modPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if action == "init" {
		for _, name := range []string{modPath, sumPath} {
			if _, err := os.Lstat(name); err == nil {
				return fmt.Errorf("%s already exists; use deps get or deps download", name)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	if len(data) == 0 && action == "download" {
		return errors.New("go-deps.mod is missing or empty; use deps init or deps get first")
	}
	if len(data) != 0 {
		manifest, err := modfile.Parse(modPath, data, nil)
		if err != nil {
			return err
		}
		if manifest.Toolchain != nil {
			return errors.New("go dependencies support only module, go, and pinned require declarations")
		}
	}
	dir, err := os.MkdirTemp("", "bork-deps-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	run := func(args ...string) error {
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOWORK=off", "GO111MODULE=on", "GOFLAGS=")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if len(data) == 0 {
		if err := run("mod", "init", mod.path); err != nil {
			return err
		}
	} else if err := os.WriteFile(filepath.Join(dir, "go.mod"), data, 0o644); err != nil {
		return err
	}
	sums, err := os.ReadFile(sumPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sums, 0o644); err != nil {
		return err
	}
	if action == "get" {
		if err := run(append([]string{"get"}, packages...)...); err != nil {
			return err
		}
	}
	if action != "init" {
		// Download every requirement, including indirect ones, to pin both the
		// module and go.mod hashes required by check and build.
		if err := run("mod", "download", "all"); err != nil {
			return err
		}
	}
	data, err = os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	manifest, err := modfile.Parse(modPath, data, nil)
	if err != nil {
		return err
	}
	// Go may add a toolchain suggestion when upgrading dependencies. Generated
	// programs use the go directive instead, so do not persist that suggestion.
	manifest.DropToolchainStmt()
	data, err = manifest.Format()
	if err != nil {
		return err
	}
	sums, err = os.ReadFile(filepath.Join(dir, "go.sum"))
	if err != nil {
		return err
	}
	if _, _, err := std.GoModuleFiles(nil, std.GoDependencyManifest{Name: mod.root, Mod: data, Sum: sums}); err != nil {
		return fmt.Errorf("go dependencies: %w", err)
	}
	if err := os.WriteFile(sumPath, sums, 0o644); err != nil {
		return err
	}
	return os.WriteFile(modPath, data, 0o644)
}
