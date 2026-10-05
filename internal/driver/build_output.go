package driver

import (
	"debug/buildinfo"
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
	timings := newCacheTestTimings()
	defer timings.finish()
	timings.phase("compile-or-lookup")
	build := func(program *compiledProgram, source []byte) error {
		timings.phase("stage")
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
		files, _, stageErr := goStageFiles(source, program.module, embeds, program.context.moduleHook)
		if stageErr != nil {
			return stageErr
		}
		timings.phase("executable")
		buildErr := buildWithReceipt(program, executable, dir, pinned, stable, files)
		timings.phase("publish-compiler-result")
		return buildErr
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
	if info, err := os.Stat(out); err == nil && !info.Mode().IsRegular() {
		// Preserve Go's handling of output directories and devices such as
		// /dev/null; an atomic rename must never replace a device node.
		return buildStagedGoObserved(program.files, out, dir, pinned, program.context, nil)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(out), ".bork-build-*")
	if err != nil {
		return err
	}
	pending := file.Name()
	_ = file.Close()
	_ = os.Remove(pending)
	defer func() { _ = os.Remove(pending) }()
	if _, err := buildinfo.ReadFile(out); err == nil {
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
