package main

import (
	"debug/buildinfo"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/toolenv"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

func upgradeCommand() *cobra.Command {
	return upgradeCommandWithClient(&http.Client{Timeout: 45 * time.Second}, "https://api.github.com/repos/GiGurra/bork/releases")
}

func upgradeCommandWithClient(client *http.Client, api string) *cobra.Command {
	var fromSource bool
	command := &cobra.Command{
		Use: "upgrade [version]", Short: "install the latest or requested bork version in BORKBIN",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requested := "latest"
			if len(args) != 0 {
				requested = args[0]
			}
			if requested != "latest" && (!semver.IsValid(requested) || semver.Canonical(requested) != requested) {
				return fmt.Errorf("upgrade: expected latest or a version such as v0.4.0, got %q", requested)
			}
			progress := newUpgradeProgress(cmd.ErrOrStderr())
			current, err := os.Executable()
			if err != nil {
				return err
			}
			if homebrewInstall(current) {
				return fmt.Errorf("upgrade: this compiler is managed by Homebrew; run brew upgrade bork")
			}
			var release editorRelease
			if !fromSource {
				err = progress.step("Checking release version (you have "+version()+")", func() error {
					var lookupErr error
					release, lookupErr = compilerRelease(cmd.Context(), client, api, requested)
					return lookupErr
				})
				if err != nil {
					if !releaseUnavailable(err) {
						return err
					}
					progress.line("Prebuilt release unavailable: " + err.Error() + "; falling back to source")
				} else {
					requested = release.Tag
					if requested == version() {
						_, err := fmt.Fprintf(cmd.OutOrStdout(), "bork %s is already up to date\n", requested)
						return err
					}
					progress.line("Selected bork " + requested)
				}
			}
			var bin string
			err = progress.step("Resolving BORKBIN (local go env; no SDK download)", func() error {
				var lookupErr error
				bin, lookupErr = toolenv.Value("BORKBIN")
				return lookupErr
			})
			if err != nil {
				return err
			}
			suffix := ""
			if runtime.GOOS == "windows" {
				suffix = ".exe"
			}
			target := filepath.Join(bin, "bork"+suffix)
			if homebrewInstall(target) {
				return fmt.Errorf("upgrade: target is managed by Homebrew; run brew upgrade bork")
			}
			if !samePath(current, target) {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "Running bork from %s; upgrade installs %s. Use that executable for the new version.\n", current, target); err != nil {
					return err
				}
			}
			onPath := false
			for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
				if samePath(dir, bin) {
					onPath = true
					break
				}
			}
			if !onPath {
				if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: BORKBIN (%s) is not on PATH; add it to run the installed bork.\n", bin); err != nil {
					return err
				}
			}
			if err := os.MkdirAll(bin, 0755); err != nil {
				return err
			}
			stage, err := os.MkdirTemp(bin, ".bork-upgrade-")
			if err != nil {
				return err
			}
			defer func() { _ = os.RemoveAll(stage) }()
			stagedBinary := filepath.Join(stage, "bork"+suffix)
			newVersion := requested
			installed := false
			if !fromSource && release.Tag != "" {
				installed, err = downloadCompiler(cmd.Context(), client, release, stage, progress)
				if err != nil {
					if !releaseUnavailable(err) {
						return err
					}
					progress.line("Prebuilt download unavailable: " + err.Error() + "; falling back to source")
				} else if !installed {
					progress.line("No prebuilt archive for this platform; falling back to source")
				}
			}
			if !installed {
				progress.line("Finding Go on PATH")
				goPath, err := exec.LookPath("go")
				if err != nil {
					return fmt.Errorf("upgrade requires Go on PATH; install Go from https://go.dev/dl/: %w", err)
				}
				install := exec.CommandContext(cmd.Context(), goPath, "install", "github.com/GiGurra/bork/cmd/bork@"+requested)
				install.Env = append(os.Environ(), "GOBIN="+stage, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
				install.Stdout, install.Stderr = progress.writer(cmd.OutOrStdout()), progress.writer(cmd.ErrOrStderr())
				err = progress.step("Resolving modules and building from source with go install (this can take a minute)", install.Run)
				if err != nil {
					return fmt.Errorf("upgrade to %s failed: %w; check the Go output above and network/module proxy access (offline installs require cached modules)", requested, err)
				}
				err = progress.step("Checking staged compiler version", func() error {
					info, err := buildinfo.ReadFile(stagedBinary)
					if err != nil {
						return fmt.Errorf("upgrade cannot read the staged compiler version: %w", err)
					}
					newVersion = info.Main.Version
					if newVersion == "" || newVersion == "(devel)" {
						newVersion = "dev"
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
			// Go can copy directly over GOBIN rather than rename (for example
			// from its build cache). Stage on the destination filesystem so
			// publishing never exposes a partial executable.
			if err := progress.step("Installing to "+target, func() error { return publishUpgrade(stagedBinary, target) }); err != nil {
				return fmt.Errorf("upgrade cannot replace %s: %w", target, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "bork %s -> %s (%s)\n", version(), newVersion, target)
			if err == nil {
				offerEditorRefresh(cmd.Context(), cmd.OutOrStdout(), target)
			}
			return err
		},
	}
	command.Flags().BoolVar(&fromSource, "from-source", false, "build with go install instead of downloading a release binary")
	return command
}

func publishUpgrade(staged, target string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(staged, target)
	}
	// Windows permits renaming a running executable, but not overwriting it.
	// Keep the old image aside until publication succeeds, restoring it on
	// failure. A running old image may prevent deleting its backup until exit.
	backup, err := os.CreateTemp(filepath.Dir(target), ".bork-old-*.exe")
	if err != nil {
		return err
	}
	name := backup.Name()
	if err := backup.Close(); err != nil {
		return err
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	if err := os.Rename(target, name); err != nil {
		if os.IsNotExist(err) {
			return os.Rename(staged, target)
		}
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		if restoreErr := os.Rename(name, target); restoreErr != nil {
			return fmt.Errorf("%w (previous compiler remains at %s; restoring it failed: %v)", err, name, restoreErr)
		}
		return err
	}
	_ = os.Remove(name)
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	if runtime.GOOS == "windows" && strings.EqualFold(aa, bb) || aa == bb {
		return true
	}
	ai, errA := os.Stat(aa)
	bi, errB := os.Stat(bb)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}
