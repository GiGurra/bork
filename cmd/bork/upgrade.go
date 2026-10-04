package main

import (
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GiGurra/bork/internal/toolenv"
	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

func upgradeCommand() *cobra.Command {
	return &cobra.Command{
		Use: "upgrade [version]", Short: "install the latest or requested bork version in BORKBIN",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requested := "latest"
			if len(args) != 0 {
				requested = args[0]
			}
			if requested != "latest" && (!semver.IsValid(requested) || semver.Canonical(requested) == "") {
				return fmt.Errorf("upgrade: expected latest or a version such as v0.4.0, got %q", requested)
			}
			goPath, err := exec.LookPath("go")
			if err != nil {
				return fmt.Errorf("upgrade requires Go on PATH; install Go from https://go.dev/dl/: %w", err)
			}
			bin, err := toolenv.Value("BORKBIN")
			if err != nil {
				return err
			}
			suffix := ""
			if runtime.GOOS == "windows" {
				suffix = ".exe"
			}
			target := filepath.Join(bin, "bork"+suffix)
			current, err := os.Executable()
			if err != nil {
				return err
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
			install := exec.CommandContext(cmd.Context(), goPath, "install", "github.com/GiGurra/bork/cmd/bork@"+requested)
			// An upgrade always builds a runnable host compiler, even when the
			// caller's Go environment is configured for cross compilation.
			install.Env = append(os.Environ(), "GOBIN="+bin, "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
			install.Stdout, install.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := install.Run(); err != nil {
				return fmt.Errorf("upgrade to %s failed: %w; check the Go output above and network/module proxy access (offline installs require cached modules)", requested, err)
			}
			info, err := buildinfo.ReadFile(target)
			if err != nil {
				return fmt.Errorf("upgrade installed %s but cannot read its version: %w", target, err)
			}
			newVersion := info.Main.Version
			if newVersion == "" || newVersion == "(devel)" {
				newVersion = "dev"
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "bork %s -> %s (%s)\n", version(), newVersion, target)
			return err
		},
	}
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
