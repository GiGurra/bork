// Package driver runs the compiler pipeline: load sources, parse, check,
// generate Go, and build with the Go toolchain.
package driver

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/syntax"
)

// DiagError is returned when the bork program has compile errors.
type DiagError struct {
	Diags *diag.List
}

func (e *DiagError) Error() string { return e.Diags.Error() }

// Sources lists the .bork files for path: the file itself, or every
// .bork file directly inside a directory (one directory = one package).
func Sources(path string) ([]string, error) { return sourceFiles(path, diskSources{}) }

func sourceFiles(path string, reader sourceReader) ([]string, error) {
	isDir, err := reader.isDirectory(path)
	if err != nil {
		return nil, err
	}
	if !isDir {
		if filepath.Ext(path) != ".bork" {
			return nil, fmt.Errorf("%s is not a .bork file", path)
		}
		return []string{path}, nil
	}
	entries, err := reader.directory(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.directory && filepath.Ext(e.name) == ".bork" {
			files = append(files, filepath.Join(path, e.name))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .bork files in %s", path)
	}
	sort.Strings(files)
	return files, nil
}

// Check parses and type-checks the package at path, and the packages
// it imports. The files start with the prelude, then the package's own.
func Check(path string) ([]*syntax.File, *check.Info, error) {
	return checkObserved(path, nil)
}

type compiledProgram struct {
	files   []*syntax.File
	info    *check.Info
	inputs  *sourceSnapshot
	module  *goModuleInputs
	assets  *embedSnapshot
	context *goContext
}

func checkObserved(path string, observe func(string)) ([]*syntax.File, *check.Info, error) {
	program, err := checkProgramObserved(path, observe)
	if err != nil {
		return nil, nil, err
	}
	return program.files, program.info, nil
}

func checkProgramObserved(path string, observe func(string)) (*compiledProgram, error) {
	loaded, module, err := loadCompilationInputs(path, observe)
	if err != nil {
		return nil, err
	}
	return checkLoadedProgramObserved(loaded, module, captureGoContext(), captureEmbedsSnapshot, observe)
}

// Rebuild semantic state from independently parsed captured inputs. Callers own
// the parsed files; immutable source/module/context snapshots may be shared.
func checkLoadedProgramObserved(loaded *loadedSources, module *goModuleInputs, context *goContext, captureAssets func(*check.Info, *diag.List, *sourceSnapshot) *embedSnapshot, observe func(string)) (*compiledProgram, error) {
	files, root, diags := loaded.Files, loaded.Root, loaded.Diags
	phase(observe, "check")
	info := check.ProgramObserved(files, root, diags, goPackages{files: files, module: module, context: context}, observe)
	if diags.Len() > 0 {
		return nil, &DiagError{Diags: diags}
	}
	phase(observe, "embeds")
	assets := captureAssets(info, diags, loaded.Inputs)
	if diags.Len() > 0 {
		return nil, &DiagError{Diags: diags}
	}
	phase(observe, "effects")
	check.CheckEffects(files, info, diags)
	if diags.Len() > 0 {
		return nil, &DiagError{Diags: diags}
	}
	phase(observe, "lifetimes")
	check.Lifetimes(files, info, diags)
	if diags.Len() > 0 {
		return nil, &DiagError{Diags: diags}
	}
	phase(observe, "facts")
	check.Facts(files, info, diags, evaluatorWithContext(files, info, module, context))
	if diags.Len() > 0 {
		return nil, &DiagError{Diags: diags}
	}
	return &compiledProgram{files: files, info: info, inputs: loaded.Inputs, module: module, assets: assets, context: context}, nil
}

// evaluator runs predicates on constants at compile time, by building
// and running a small program made from the package's own code.
func evaluator(path string, files []*syntax.File, info *check.Info) check.Evaluator {
	// Legacy helper callers get their own one-shot module capture. Compilation
	// requests pass their existing capture through evaluatorWithModule.
	module, err := captureGoModule(files, diskSources{})
	if err != nil {
		return func([]check.Query) ([]bool, error) { return nil, err }
	}
	return evaluatorWithModule(files, info, module)
}

func evaluatorWithModule(files []*syntax.File, info *check.Info, module *goModuleInputs) check.Evaluator {
	return evaluatorWithContext(files, info, module, captureGoContext())
}

func evaluatorWithContext(files []*syntax.File, info *check.Info, module *goModuleInputs, context *goContext) check.Evaluator {
	return func(queries []check.Query) ([]bool, error) {
		goSrc, err := gen.EvalProgram(files, info, queries)
		if err != nil {
			return nil, err
		}
		dir, err := os.MkdirTemp("", "bork-eval-*")
		if err != nil {
			return nil, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		exe := filepath.Join(dir, "eval")
		if err := buildGoWithContext(files, goSrc, exe, module, context, info.Embeds...); err != nil {
			return nil, err
		}
		var stderr strings.Builder
		cmd := exec.Command(exe)
		cmd.Env = slices.Clone(context.processEnv)
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("a predicate failed: %s", strings.TrimSpace(stderr.String()))
		}
		lines := strings.Fields(string(out))
		if len(lines) != len(queries) {
			return nil, fmt.Errorf("expected %d results, got %q", len(queries), out)
		}
		results := make([]bool, len(lines))
		for i, l := range lines {
			results[i] = l == "true"
		}
		return results, nil
	}
}

// Emit compiles the package at path to Go source. A program must have
// a main function.
func Emit(path string) ([]byte, error) {
	_, _, goSrc, err := emit(path)
	return goSrc, err
}

func emit(path string) ([]*syntax.File, *check.Info, []byte, error) {
	return emitObserved(path, nil)
}

func emitObserved(path string, observe func(string)) ([]*syntax.File, *check.Info, []byte, error) {
	program, source, err := emitProgramObserved(path, observe)
	if err != nil {
		return nil, nil, nil, err
	}
	return program.files, program.info, source, nil
}

func emitProgramObserved(path string, observe func(string)) (*compiledProgram, []byte, error) {
	program, err := checkProgramObserved(path, observe)
	if err != nil {
		return nil, nil, err
	}
	files, info := program.files, program.info
	if _, ok := info.Funcs["main"]; !ok {
		diags := &diag.List{}
		diags.AddCode(packagePos(files), "package.no-main", "package has no main function (add `fn main() { ... }`)")
		return nil, nil, &DiagError{Diags: diags}
	}
	phase(observe, "generate")
	goSrc, err := gen.Package(files, info)
	return program, goSrc, err
}

// Build compiles the package at path into an executable at out.
func Build(path, out string) error {
	program, goSrc, err := emitProgramObserved(path, nil)
	if err != nil {
		return err
	}
	return buildGoWithContext(program.files, goSrc, out, program.module, program.context, program.info.Embeds...)
}

// buildGo builds generated Go source (for the given bork files) into an
// executable at out.
func buildGo(files []*syntax.File, goSrc []byte, out string, embeds ...*check.Embedded) error {
	module, err := captureGoModule(files, diskSources{})
	if err != nil {
		return err
	}
	return buildGoWithModule(files, goSrc, out, module, embeds...)
}

func buildGoWithModule(files []*syntax.File, goSrc []byte, out string, module *goModuleInputs, embeds ...*check.Embedded) error {
	return buildGoWithContext(files, goSrc, out, module, captureGoContext(), embeds...)
}

func buildGoWithContext(files []*syntax.File, goSrc []byte, out string, module *goModuleInputs, context *goContext, embeds ...*check.Embedded) error {
	if context.err != nil {
		return fmt.Errorf("determining Go build configuration (is Go installed?): %w", context.err)
	}
	absOut, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "bork-build-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), goSrc, 0o644); err != nil {
		return err
	}
	if err := stageEmbeds(dir, embeds); err != nil {
		return err
	}
	pinned, err := module.write(dir)
	if err != nil {
		return err
	}
	cmd := context.command("build", "-mod=readonly", "-buildvcs=false", "-o", absOut, ".")
	cmd.Dir = dir
	cmd.Env = append(cmd.Env, "GOWORK=off", "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if diags := unsafeGoErrors(sourcePaths(files), dir, string(output)); diags != nil {
				return &DiagError{Diags: diags}
			}
			if pinned {
				return fmt.Errorf("building generated program with pinned Go dependencies failed (offline builds need the modules in Go's cache):\n%s", strings.TrimSpace(string(output)))
			}
			return fmt.Errorf("go build failed on the generated code (this is a bork compiler bug):\n%s", strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("running go build (is Go installed?): %w", err)
	}
	return nil
}

// writeGoModule writes the go.mod (and go.sum) of a generated program
// into dir: the Go modules imported standard packages and the user module need.
// It reports whether there are any.
func writeGoModule(dir string, files []*syntax.File) (bool, error) {
	module, err := captureGoModule(files, diskSources{})
	if err != nil {
		return false, err
	}
	return module.write(dir)
}

// goModuleHook lets tests change the go.mod of generated programs, to
// add a local Go module for bindings to call.
var goModuleHook func(goMod []byte) []byte

// Run builds the package at path into a temporary executable and runs
// it with the given arguments. It returns the program's exit code.
func Run(path string, args []string) (int, error) {
	dir, err := os.MkdirTemp("", "bork-run-*")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	exe := filepath.Join(dir, "program")
	if err := Build(path, exe); err != nil {
		return 1, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// TestOptions are the options of bork test.
type TestOptions struct {
	// Update writes the snapshots that assertSnapshot finds missing or
	// different, instead of failing.
	Update bool
	// AutoProperties property-tests the functions whose promises are
	// trusted (unsafe go, or trust in their body), calling them on
	// generated arguments. It is opt-in until effects tell which
	// functions are pure: random arguments could make others do IO.
	AutoProperties bool
	// Seed, if not 0, is the seed of every property test, instead of
	// one from its name. Cases, if not 0, is how many cases each runs.
	Seed  int64
	Cases int
	// Hermetic fails, without running it, every test that can reach the
	// network (a function doing net in Go code) with no mock in force.
	Hermetic bool
	// Parallel, if more than 1, is how many tests run at a time. Each
	// runs on goroutines of its own, and the report keeps their order.
	Parallel int
}

// SnapshotDir is where the tests of the package at path keep their
// snapshots: a snapshots directory next to its sources.
func SnapshotDir(path string) string {
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		path = filepath.Dir(path)
	}
	return filepath.Join(path, "snapshots")
}

// Test builds the package's tests in test mode and runs them, with the
// report going to stdout. It returns the exit code: 0 if every test
// passed.
func Test(path string, stdout io.Writer, opts TestOptions) (int, error) {
	program, err := checkProgramObserved(path, nil)
	if err != nil {
		return 1, err
	}
	files, info := program.files, program.info
	if len(info.Tests) == 0 && len(info.Rules) == 0 && !opts.AutoProperties {
		diags := &diag.List{}
		diags.AddCode(packagePos(files), "package.no-tests", "package has no tests or rules (add `test \"name\" { ... }`)")
		return 1, &DiagError{Diags: diags}
	}
	goSrc, err := gen.TestsWith(files, info, gen.TestOptions{AutoProperties: opts.AutoProperties, Hermetic: opts.Hermetic})
	if err != nil {
		var diags *diag.List
		if errors.As(err, &diags) {
			return 1, &DiagError{Diags: diags}
		}
		return 1, err
	}
	dir, err := os.MkdirTemp("", "bork-test-*")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	exe := filepath.Join(dir, "tests")
	if err := buildGoWithContext(files, goSrc, exe, program.module, program.context, info.Embeds...); err != nil {
		return 1, err
	}
	cmd := exec.Command(exe)
	cmd.Stdout, cmd.Stderr = stdout, os.Stderr
	update := ""
	if opts.Update {
		update = "1"
	}
	seed, cases := "", ""
	if opts.Seed != 0 {
		seed = strconv.FormatInt(opts.Seed, 10)
	}
	if opts.Cases != 0 {
		cases = strconv.Itoa(opts.Cases)
	}
	parallel := ""
	if opts.Parallel > 1 {
		parallel = strconv.Itoa(opts.Parallel)
	}
	cmd.Env = append(os.Environ(), "BORK_SNAPSHOTS="+SnapshotDir(path), "BORK_UPDATE_SNAPSHOTS="+update, "BORK_SEED="+seed, "BORK_CASES="+cases, "BORK_PARALLEL="+parallel)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// EmitTests is Emit for the test program (see Test).
func EmitTests(path string, opts TestOptions) ([]byte, error) {
	files, info, err := Check(path)
	if err != nil {
		return nil, err
	}
	return gen.Tests(files, info, opts.AutoProperties)
}

// DefaultOutput is the executable name `bork build` uses when none is
// given: the file name without .bork, or the directory's name.
func DefaultOutput(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	base := filepath.Base(abs)
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return base
}

var goErrorLine = regexp.MustCompile(`^(.+\.bork):(\d+):(\d+): (.*)$`)

// unsafeGoErrors turns Go compiler errors located in .bork files (in
// `unsafe go` code, which the generated Go maps back to its source)
// into bork diagnostics. It returns nil if there are none.
func unsafeGoErrors(paths []string, buildDir, output string) *diag.List {
	original := map[string]string{}
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			original[abs] = p
		}
	}
	diags := &diag.List{}
	for _, line := range strings.Split(output, "\n") {
		m := goErrorLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		file := m[1]
		if !filepath.IsAbs(file) {
			file = filepath.Join(buildDir, file)
		}
		if p, ok := original[filepath.Clean(file)]; ok {
			file = p
		}
		ln, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		diags.AddCode(diag.Pos{File: file, Line: ln, Col: col}, "go.error", "in unsafe go block: %s", m[4])
	}
	if diags.Len() == 0 {
		return nil
	}
	return diags
}

// phase keeps observation out of the normal compiler path.
func phase(observe func(string), name string) {
	if observe != nil {
		observe(name)
	}
}
