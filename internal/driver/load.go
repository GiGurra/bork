package driver

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/manifest"
	"github.com/GiGurra/bork/internal/prelude"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
	gomodule "golang.org/x/mod/module"
)

// ModFile is the file at a module's root that names the module, and
// the packages allowed to contain unsafe go:
//
//	module example.com/shop
//	unsafe "example.com/shop/ffi"
const ModFile = "bork.mod"

// module is the module a package belongs to.
type module struct {
	root    string // the directory holding bork.mod
	path    string // the module path; "" without a bork.mod
	version string // minimum compiler requirement, if declared
	// unsafe holds the packages allowed to contain unsafe go.
	unsafe       map[string]bool
	requirements []gomodule.Version
}

// findModule finds the module of the package in dir: the nearest
// bork.mod in dir or above it. Without one, the package is on its own,
// and cannot import other packages.
func findModule(dir string) (module, error) { return findModuleFrom(dir, diskSources{}) }

func findModuleFrom(dir string, reader sourceReader) (module, error) {
	abs, err := reader.absolute(dir)
	if err != nil {
		return module{}, err
	}
	for d := abs; ; d = filepath.Dir(d) {
		data, err := reader.readFile(filepath.Join(d, ModFile))
		if err == nil {
			mod, err := parseModFile(string(data))
			if err != nil {
				return module{}, fmt.Errorf("%s: %w", filepath.Join(d, ModFile), err)
			}
			mod.root = d
			return mod, nil
		}
		if filepath.Dir(d) == d {
			return module{root: abs}, nil
		}
	}
}

func parseModFile(text string) (module, error) {
	var mod module
	required := map[string]bool{}
	for _, line := range strings.Split(text, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		switch {
		case mod.path == "" && len(fields) == 2 && fields[0] == "module":
			if fields[1] == "bork" || strings.HasPrefix(fields[1], std.Prefix) {
				return module{}, fmt.Errorf("module path %s is reserved for the standard library", fields[1])
			}
			mod.path = fields[1]
		case mod.path == "":
			return module{}, fmt.Errorf("expected `module <path>`, found %q", line)
		case len(fields) == 2 && fields[0] == "bork":
			if mod.version != "" {
				return module{}, fmt.Errorf("duplicate bork version directive")
			}
			version, err := manifest.ParseVersion(fields[1])
			if err != nil {
				return module{}, err
			}
			mod.version = version.Minimum
		case len(fields) == 2 && fields[0] == "unsafe":
			path, err := strconv.Unquote(fields[1])
			if err != nil {
				return module{}, fmt.Errorf("expected `unsafe \"<package path>\"`, found %q", line)
			}
			if mod.unsafe == nil {
				mod.unsafe = map[string]bool{}
			}
			mod.unsafe[path] = true
		case len(fields) == 3 && fields[0] == "require":
			dep := gomodule.Version{Path: fields[1], Version: fields[2]}
			if err := gomodule.Check(dep.Path, dep.Version); err != nil {
				return module{}, fmt.Errorf("invalid requirement: %w", err)
			}
			if gomodule.CanonicalVersion(dep.Version) != dep.Version {
				return module{}, fmt.Errorf("require %s needs a canonical pinned version", dep.Path)
			}
			if required[dep.Path] {
				return module{}, fmt.Errorf("require %s is declared more than once", dep.Path)
			}
			required[dep.Path] = true
			mod.requirements = append(mod.requirements, dep)
		default:
			return module{}, fmt.Errorf("expected `unsafe \"<package path>\"` or `require <module> <version>`, found %q", line)
		}
	}
	if mod.path == "" {
		return module{}, fmt.Errorf("expected `module <path>`")
	}
	return mod, nil
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
	scriptPath string
	mod        module
	diags      *diag.List
	inputs     sourceReader
	// files holds the parsed files: the prelude, the root package, and
	// then the packages it imports.
	files []*syntax.File
	// state is 1 while a package's imports are being loaded, and 2 once
	// it is done.
	state map[string]int
	stack []string
}

type loadedSources struct {
	Files  []*syntax.File
	Root   string
	Diags  *diag.List
	Inputs *sourceSnapshot
}

// load parses the package at path (a directory, or a single .bork file)
// and every package it imports. It returns the files, starting with the
// prelude and the root package's, and the root's import path.
func load(path string) ([]*syntax.File, string, *diag.List, error) {
	loaded, err := loadSnapshot(path)
	if err != nil {
		return nil, "", nil, err
	}
	return loaded.Files, loaded.Root, loaded.Diags, nil
}

func loadSnapshot(path string) (*loadedSources, error) {
	return loadStableSources(path, newSourceSnapshot)
}

func loadStableSources(path string, capture func() *sourceSnapshot) (*loadedSources, error) {
	for range 2 {
		inputs := capture()
		files, root, diags, err := loadFrom(path, inputs)
		if !inputs.current() {
			continue
		}
		return &loadedSources{files, root, diags, inputs}, err
	}
	return nil, fmt.Errorf("source inputs changed while loading; retry the command")
}

func loadFrom(path string, reader sourceReader) ([]*syntax.File, string, *diag.List, error) {
	diags := &diag.List{}
	path, script := strings.CutPrefix(path, scriptRequestPrefix)
	paths, err := sourceFiles(path, reader)
	if err != nil {
		return nil, "", nil, err
	}
	dir := path
	if isDir, err := reader.isDirectory(path); script && err == nil && isDir {
		return nil, "", nil, fmt.Errorf("bork script needs a single .bork file, not a directory")
	} else if err == nil && !isDir {
		dir = filepath.Dir(path)
	}
	mod, err := findModuleFrom(dir, reader)
	if err != nil {
		return nil, "", nil, err
	}
	l := &loader{mod: mod, diags: diags, state: map[string]int{}, inputs: reader}
	if script {
		l.scriptPath = paths[0]
	}
	l.files = append(l.files, prelude.Parse(diags)...)
	absoluteDir, err := reader.absolute(dir)
	if err != nil {
		return nil, "", nil, err
	}
	root := mod.importPath(absoluteDir)
	if err := l.loadPackage(root, paths); err != nil {
		return nil, "", nil, err
	}
	return l.files, root, diags, nil
}

func (l *loader) sourceReader() sourceReader {
	if l.inputs != nil {
		return l.inputs
	}
	return diskSources{}
}

func (l *loader) loadPackage(importPath string, paths []string) error {
	var srcs [][]byte
	for _, p := range paths {
		src, err := l.sourceReader().readFile(p)
		if err != nil {
			return err
		}
		srcs = append(srcs, src)
	}
	return l.loadSources(importPath, paths, srcs)
}

func (l *loader) loadSources(importPath string, paths []string, srcs [][]byte) error {
	l.state[importPath] = 1
	l.stack = append(l.stack, importPath)
	var files []*syntax.File
	if len(paths) == 1 && paths[0] == l.scriptPath {
		files = []*syntax.File{syntax.ParseScript(paths[0], srcs[0], l.diags)}
	} else {
		files = syntax.ParseFiles(paths, srcs, strings.HasPrefix(importPath, std.Prefix), l.diags)
	}
	for _, f := range files {
		f.Package = importPath
	}
	l.files = append(l.files, files...)
	if !strings.HasPrefix(importPath, std.Prefix) {
		for _, file := range files {
			header := readScriptHeader(file, l.mod.path != "", l.diags)
			if file.Script && (len(paths) != 1 || len(l.stack) != 1) {
				l.diags.AddCode(diag.Pos{File: file.Path, Line: 1, Col: 1}, "script.single-file", "scripts must be compiled as a single root file; scripts cannot be imported as packages")
			}
			if header.unsafe {
				if l.mod.unsafe == nil {
					l.mod.unsafe = map[string]bool{}
				}
				l.mod.unsafe[importPath] = true
			}
		}
		l.checkUnsafe(importPath, files)
	}
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
		l.diags.AddCode(imp.Pos, "import.error", "import cycle: %s imports %s (packages cannot import each other in a circle)", imp.Path, cycle)
		return nil
	}
	if strings.HasPrefix(imp.Path, std.Prefix) {
		paths, srcs, ok := std.Sources(imp.Path)
		if !ok {
			l.diags.AddCode(imp.Pos, "import.error", "cannot import %s: there is no such standard package", imp.Path)
			return nil
		}
		return l.loadSources(imp.Path, paths, srcs)
	}
	if l.mod.path == "" {
		l.diags.AddCode(imp.Pos, "import.error", "cannot import %s: imports need a module (add a %s file to the module's root directory, with a line `module example.com/name`)", imp.Path, ModFile)
		return nil
	}
	rel, ok := strings.CutPrefix(imp.Path, l.mod.path+"/")
	if !ok {
		l.diags.AddCode(imp.Pos, "import.error", "cannot import %s: only packages of module %s can be imported", imp.Path, l.mod.path)
		return nil
	}
	dir := filepath.Join(l.mod.root, filepath.FromSlash(rel))
	// Paths in messages are relative to the working directory, if it can.
	if wd, err := l.sourceReader().workingDirectory(); err == nil {
		if r, err := filepath.Rel(wd, dir); err == nil {
			dir = r
		}
	}
	paths, err := sourceFiles(dir, l.sourceReader())
	if err != nil {
		l.diags.AddCode(imp.Pos, "import.error", "cannot import %s: %v", imp.Path, err)
		return nil
	}
	name := imp.Path[strings.LastIndex(imp.Path, "/")+1:]
	if !validName(name) {
		l.diags.AddCode(imp.Pos, "import.error", "cannot import %s: a package's directory name must be a valid name, like money or http_util", imp.Path)
		return nil
	}
	return l.loadPackage(imp.Path, paths)
}

// checkUnsafe reports unsafe go in a package that bork.mod does not
// allow to have it.
func (l *loader) checkUnsafe(importPath string, files []*syntax.File) {
	if l.mod.unsafe[importPath] {
		return
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if !fd.IsGo() {
				continue
			}
			if l.mod.path == "" {
				l.diags.AddCode(fd.Pos, "unsafe.not-allowed", "%s is implemented in unsafe go, which needs a %s that allows it: a file at the module's root with the lines `module <path>` and `unsafe \"<path>\"`", fd.Name, ModFile)
			} else {
				l.diags.AddCode(fd.Pos, "unsafe.not-allowed", "package %s has unsafe go (%s), but %s does not allow it; if it is meant to, add the line: unsafe %q", importPath, fd.Name, ModFile, importPath)
			}
			return
		}
	}
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

// packagePos locates the root package, which follows the prelude files.
func packagePos(files []*syntax.File) diag.Pos {
	for _, f := range files {
		if !f.Prelude {
			return diag.Pos{File: f.Path, Line: 1, Col: 1}
		}
	}
	return diag.Pos{Line: 1, Col: 1}
}

// sourcePaths lists the paths of files, for mapping Go errors back.
func sourcePaths(files []*syntax.File) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}
