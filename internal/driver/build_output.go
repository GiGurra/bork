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
func buildOutput(path, out string, supplied ...BuildOptions) (executable string, cleanup func(), stable bool, err error) {
	timings := newCacheTestTimings()
	defer timings.finish()
	timings.phase("executable-recipe")
	options, err := resolveBuildOptions(supplied)
	if err != nil {
		return "", nil, false, err
	}
	request := recipeRequest(path, out)
	if recipe := lookupExecutableRecipe(request, options); recipe != nil {
		return recipe.Executable, func() {}, true, nil
	}
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
		requested := strings.TrimPrefix(path, scriptRequestPrefix)
		identity, stageErr := filepath.Abs(requested)
		if stageErr != nil {
			return stageErr
		}
		identity, stageErr = filepath.EvalSymlinks(identity)
		if stageErr != nil {
			return stageErr
		}
		mode := "program:" + identity
		if strings.HasPrefix(path, scriptRequestPrefix) {
			mode = scriptRequestPrefix + identity
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
		var broadBefore *buildInventory
		if stable && cacheCLIState != nil && !cacheDisabled() {
			broadBefore = captureBuildInventoryMode(dir, program.context, true)
		}
		var buildErr error
		var certified buildExecutableIdentity
		if options.Rebuild || broadBefore != nil && broadBefore.RequireFreshObjects {
			certified, buildErr = buildAtomicOutput(program, executable, dir, pinned, nil, "-a")
		} else {
			buildErr = buildWithReceipt(program, executable, dir, pinned, stable, files)
			if receipt := readBuildReceipt(filepath.Join(filepath.Dir(dir), fmtBuildReceiptName(executable))); receipt != nil {
				certified = receipt.Output
			}
		}
		if buildErr == nil && stable {
			writeExecutableRecipe(captureExecutableRecipe(request, executable, dir, program, certified, broadBefore))
		}
		timings.phase("publish-compiler-result")
		return buildErr
	}
	var program *compiledProgram
	var source []byte
	program, source, err = emitProgramObserved(path, nil)
	if err == nil {
		err = build(program, source)
	}
	if err != nil && cleanup != nil {
		cleanup()
		cleanup = nil
	}
	return
}

// Seed a private Go target with a hard link to a verified existing executable. Go reads
// its build ID and leaves an up-to-date target alone. When it rebuilds, Go
// replaces the link; we then atomically publish the completed regular file.
// This preserves the old inode on hits and never truncates a running executable.
func buildAtomicOutput(program *compiledProgram, out, dir string, pinned bool, seed *buildExecutableIdentity, options ...string) (buildExecutableIdentity, error) {
	var zero buildExecutableIdentity
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return zero, buildStagedGoOptions(program.files, out, dir, pinned, program.context, nil, options...)
	}
	if info, err := os.Stat(out); err == nil && !info.Mode().IsRegular() {
		// Preserve Go's handling of output directories and devices such as
		// /dev/null; an atomic rename must never replace a device node.
		return zero, buildStagedGoOptions(program.files, out, dir, pinned, program.context, nil, options...)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0755); err != nil {
		return zero, err
	}
	file, err := os.CreateTemp(filepath.Dir(out), ".bork-build-*")
	if err != nil {
		return zero, err
	}
	pending := file.Name()
	_ = file.Close()
	_ = os.Remove(pending)
	defer func() { _ = os.Remove(pending) }()
	if seed != nil {
		if err := os.Link(out, pending); err == nil {
			// Another stage may replace the shared output between validation and
			// linking. The private inode must match the independently recorded bytes.
			if actual, err := readBuildExecutable(pending); err != nil || actual != *seed {
				_ = os.Remove(pending)
			}
		}
	}
	if err := buildStagedGoOptions(program.files, pending, dir, pinned, program.context, nil, options...); err != nil {
		return zero, err
	}
	identity, err := readBuildExecutable(pending)
	if err != nil {
		return zero, err
	}
	// Certify the private artifact before publication. Different stages can
	// publish to the same -o, so hashing the shared path afterward is unsafe.
	info, err := os.Stat(pending)
	if err != nil {
		return zero, err
	}
	if current, err := os.Stat(out); err == nil && os.SameFile(info, current) {
		return identity, nil
	}
	return identity, os.Rename(pending, out)
}
