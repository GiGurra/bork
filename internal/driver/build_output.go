package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

// An empty output selects the program executable next to its stable Go stage.
// The stage lock serializes builders; execution itself never holds that lock.
func buildOutput(path, out string) (executable string, cleanup func(), stable bool, err error) {
	build := func(program *compiledProgram, source []byte) error {
		if program.context.err != nil {
			return fmt.Errorf("determining Go build configuration (is Go installed?): %w", program.context.err)
		}
		var embeds []*check.Embedded
		if program.info != nil {
			embeds = program.info.Embeds
		}
		mode := "program"
		if strings.HasPrefix(path, scriptRequestPrefix) {
			mode = path
		}
		dir, pinned, release, stageErr := stageGo(program.files, source, program.module, program.context, mode, embeds)
		if stageErr != nil {
			return stageErr
		}
		stable = filepath.Base(dir) == "tree"
		executable = out
		if executable == "" {
			executable = filepath.Join(dir, "program")
			if stable {
				executable = filepath.Join(filepath.Dir(dir), "program")
			}
			if runtime.GOOS == "windows" {
				executable += ".exe"
			}
		}
		executable, stageErr = filepath.Abs(executable)
		if stageErr != nil {
			release()
			return stageErr
		}
		// Temporary staging must survive until the program has finished.
		cleanup = func() {}
		if stable || out != "" {
			defer release()
		} else {
			cleanup = release
		}
		return buildAtomicOutput(program, executable, dir, pinned)
	}
	if cacheCLIState != nil && !cacheDisabled() {
		err = compileBuild(path, build)
	} else {
		var program *compiledProgram
		var source []byte
		program, source, err = emitProgramObserved(path, nil)
		if err == nil {
			err = build(program, source)
		}
	}
	if err != nil && cleanup != nil {
		cleanup()
		cleanup = nil
	}
	return
}

// Seed a private Go target with a symlink to the existing executable. Go reads
// its build ID and leaves an up-to-date target alone. When it rebuilds, Go
// replaces the symlink; we then atomically publish the completed regular file.
// This preserves the old inode on hits and never truncates a running executable.
func buildAtomicOutput(program *compiledProgram, out, dir string, pinned bool) error {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return buildStagedGoObserved(program.files, out, dir, pinned, program.context, nil)
	}
	file, err := os.CreateTemp(filepath.Dir(out), ".bork-build-*")
	if err != nil {
		return err
	}
	pending := file.Name()
	_ = file.Close()
	_ = os.Remove(pending)
	defer func() { _ = os.Remove(pending) }()
	if info, err := os.Stat(out); err == nil && info.Mode().IsRegular() {
		if err := os.Symlink(out, pending); err != nil {
			return err
		}
	}
	if err := buildStagedGoObserved(program.files, pending, dir, pinned, program.context, nil); err != nil {
		return err
	}
	info, err := os.Lstat(pending)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return os.Rename(pending, out)
}
