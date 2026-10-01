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
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gen"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

// DiagError is returned when the bork program has compile errors.
type DiagError struct {
	Diags *diag.List
}

func (e *DiagError) Error() string { return e.Diags.Error() }

// Sources lists the .bork files for path: the file itself, or every
// .bork file directly inside a directory (one directory = one package).
func Sources(path string) ([]string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		if filepath.Ext(path) != ".bork" {
			return nil, fmt.Errorf("%s is not a .bork file", path)
		}
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".bork" {
			files = append(files, filepath.Join(path, e.Name()))
		}
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .bork files in %s", path)
	}
	sort.Strings(files)
	return files, nil
}

// Check parses and type-checks the package at path.
func Check(path string) ([]*syntax.File, *check.Info, error) {
	paths, err := Sources(path)
	if err != nil {
		return nil, nil, err
	}
	diags := &diag.List{}
	files := []*syntax.File{prelude.Parse(diags)}
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, syntax.Parse(p, src, diags))
	}
	if diags.Len() > 0 {
		// Report syntax errors before attempting to type-check.
		return nil, nil, &DiagError{Diags: diags}
	}
	info := check.Package(files, diags)
	if diags.Len() > 0 {
		return nil, nil, &DiagError{Diags: diags}
	}
	check.Facts(files, info, diags, evaluator(path, files, info))
	if diags.Len() > 0 {
		return nil, nil, &DiagError{Diags: diags}
	}
	return files, info, nil
}

// evaluator runs predicates on constants at compile time, by building
// and running a small program made from the package's own code.
func evaluator(path string, files []*syntax.File, info *check.Info) check.Evaluator {
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
		if err := buildGo(path, goSrc, exe); err != nil {
			return nil, err
		}
		var stderr strings.Builder
		cmd := exec.Command(exe)
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
	files, info, err := Check(path)
	if err != nil {
		return nil, err
	}
	if _, ok := info.Funcs["main"]; !ok {
		diags := &diag.List{}
		diags.Add(diag.Pos{File: files[1].Path, Line: 1, Col: 1}, "package has no main function (add `fn main() { ... }`)")
		return nil, &DiagError{Diags: diags}
	}
	return gen.Package(files, info)
}

// Build compiles the package at path into an executable at out.
func Build(path, out string) error {
	goSrc, err := Emit(path)
	if err != nil {
		return err
	}
	return buildGo(path, goSrc, out)
}

// buildGo builds generated Go source (for the package at path) into an
// executable at out.
func buildGo(path string, goSrc []byte, out string) error {
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
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module borkprogram\n\ngo 1.22\n"), 0o644); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", absOut, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			if diags := unsafeGoErrors(path, dir, string(output)); diags != nil {
				return &DiagError{Diags: diags}
			}
			return fmt.Errorf("go build failed on the generated code (this is a bork compiler bug):\n%s", strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("running go build (is Go installed?): %w", err)
	}
	return nil
}

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

// Test builds the package's tests in test mode and runs them, with the
// report going to stdout. It returns the exit code: 0 if every test
// passed.
func Test(path string, stdout io.Writer) (int, error) {
	files, info, err := Check(path)
	if err != nil {
		return 1, err
	}
	if len(info.Tests) == 0 && len(info.Rules) == 0 {
		diags := &diag.List{}
		diags.Add(diag.Pos{File: files[1].Path, Line: 1, Col: 1}, "package has no tests or rules (add `test \"name\" { ... }`)")
		return 1, &DiagError{Diags: diags}
	}
	goSrc, err := gen.Tests(files, info)
	if err != nil {
		return 1, err
	}
	dir, err := os.MkdirTemp("", "bork-test-*")
	if err != nil {
		return 1, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	exe := filepath.Join(dir, "tests")
	if err := buildGo(path, goSrc, exe); err != nil {
		return 1, err
	}
	cmd := exec.Command(exe)
	cmd.Stdout, cmd.Stderr = stdout, os.Stderr
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
func EmitTests(path string) ([]byte, error) {
	files, info, err := Check(path)
	if err != nil {
		return nil, err
	}
	return gen.Tests(files, info)
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
func unsafeGoErrors(path, buildDir, output string) *diag.List {
	paths, _ := Sources(path)
	paths = append(paths, prelude.Path)
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
		diags.Add(diag.Pos{File: file, Line: ln, Col: col}, "in unsafe go block: %s", m[4])
	}
	if diags.Len() == 0 {
		return nil
	}
	return diags
}
