package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/syntax"
)

// ModFile is the file at a module's root that names the module:
//
//	module example.com/shop
const ModFile = "bork.mod"

// module is the module a package belongs to.
type module struct {
	root string // the directory holding bork.mod
	path string // the module path; "" without a bork.mod
}

// findModule finds the module of the package in dir: the nearest
// bork.mod in dir or above it. Without one, the package is on its own,
// and cannot import other packages.
func findModule(dir string) (module, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return module{}, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		data, err := os.ReadFile(filepath.Join(d, ModFile))
		if err == nil {
			path, err := parseModFile(string(data))
			if err != nil {
				return module{}, fmt.Errorf("%s: %w", filepath.Join(d, ModFile), err)
			}
			return module{root: d, path: path}, nil
		}
		if filepath.Dir(d) == d {
			return module{root: abs}, nil
		}
	}
}

func parseModFile(text string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
		return "", fmt.Errorf("expected `module <path>`, found %q", line)
	}
	return "", fmt.Errorf("expected `module <path>`")
}

// importPath is the import path of the package in dir.
func (m module) importPath(dir string) string {
	if m.path == "" {
		return "main"
	}
	abs, _ := filepath.Abs(dir)
	rel, err := filepath.Rel(m.root, abs)
	if err != nil || rel == "." {
		return m.path
	}
	return m.path + "/" + filepath.ToSlash(rel)
}

// loader loads a package and the packages it imports.
type loader struct {
	mod   module
	diags *diag.List
	// files holds the parsed files: the prelude, the root package, and
	// then the packages it imports.
	files []*syntax.File
	// state is 1 while a package's imports are being loaded, and 2 once
	// it is done.
	state map[string]int
	stack []string
}

// load parses the package at path (a directory, or a single .bork file)
// and every package it imports. It returns the files, starting with the
// prelude and the root package's, and the root's import path.
func load(path string) ([]*syntax.File, string, *diag.List, error) {
	diags := &diag.List{}
	paths, err := Sources(path)
	if err != nil {
		return nil, "", nil, err
	}
	dir := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		dir = filepath.Dir(path)
	}
	mod, err := findModule(dir)
	if err != nil {
		return nil, "", nil, err
	}
	l := &loader{mod: mod, diags: diags, state: map[string]int{}}
	l.files = append(l.files, prelude.Parse(diags))
	root := mod.importPath(dir)
	if err := l.loadPackage(root, paths); err != nil {
		return nil, "", nil, err
	}
	return l.files, root, diags, nil
}

func (l *loader) loadPackage(importPath string, paths []string) error {
	l.state[importPath] = 1
	l.stack = append(l.stack, importPath)
	var files []*syntax.File
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		f := syntax.Parse(p, src, l.diags)
		f.Package = importPath
		files = append(files, f)
	}
	l.files = append(l.files, files...)
	for _, f := range files {
		for _, imp := range f.Imports {
			if err := l.loadImport(imp); err != nil {
				return err
			}
		}
	}
	l.stack = l.stack[:len(l.stack)-1]
	l.state[importPath] = 2
	return nil
}

func (l *loader) loadImport(imp *syntax.Import) error {
	switch l.state[imp.Path] {
	case 2:
		return nil
	case 1:
		cycle := imp.Path
		for i := len(l.stack) - 1; i >= 0 && l.stack[i] != imp.Path; i-- {
			cycle = l.stack[i] + " imports " + cycle
		}
		l.diags.Add(imp.Pos, "import cycle: %s imports %s (packages cannot import each other in a circle)", imp.Path, cycle)
		return nil
	}
	if l.mod.path == "" {
		l.diags.Add(imp.Pos, "cannot import %s: imports need a module (add a %s file to the module's root directory, with a line `module example.com/name`)", imp.Path, ModFile)
		return nil
	}
	rel, ok := strings.CutPrefix(imp.Path, l.mod.path+"/")
	if !ok {
		l.diags.Add(imp.Pos, "cannot import %s: only packages of module %s can be imported", imp.Path, l.mod.path)
		return nil
	}
	dir := filepath.Join(l.mod.root, filepath.FromSlash(rel))
	// Paths in messages are relative to the working directory, if it can.
	if wd, err := os.Getwd(); err == nil {
		if r, err := filepath.Rel(wd, dir); err == nil {
			dir = r
		}
	}
	paths, err := Sources(dir)
	if err != nil {
		l.diags.Add(imp.Pos, "cannot import %s: %v", imp.Path, err)
		return nil
	}
	name := imp.Path[strings.LastIndex(imp.Path, "/")+1:]
	if !validName(name) {
		l.diags.Add(imp.Pos, "cannot import %s: a package's directory name must be a valid name, like money or http_util", imp.Path)
		return nil
	}
	return l.loadPackage(imp.Path, paths)
}

func validName(s string) bool {
	if s == "" || !isLetter(rune(s[0])) {
		return false
	}
	for _, r := range s {
		if !isLetter(r) && !isDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

func isLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func isDigit(r rune) bool  { return r >= '0' && r <= '9' }

// sourcePaths lists the paths of files, for mapping Go errors back.
func sourcePaths(files []*syntax.File) []string {
	var out []string
	for _, f := range files {
		if f.Prelude {
			out = append(out, prelude.Path)
		} else {
			out = append(out, f.Path)
		}
	}
	return out
}
