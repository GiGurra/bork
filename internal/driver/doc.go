package driver

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/apidoc"
	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

type DocOptions struct{ All, HTML bool }
type docTarget struct {
	path, directory             string
	owner                       module
	version                     string
	external, standard, builtin bool
}

// Doc returns a complete document only after every requested package checks.
func Doc(path string, opts DocOptions) ([]byte, error) {
	if path == "" {
		path = "."
	}
	inventory := newSourceSnapshot()
	inventory.localDependencies = true
	targets, err := docTargets(path, opts.All, inventory)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no Bork packages in %s", path)
	}
	settings := []string{"GOPACKAGESDRIVER=off"}
	ctx := captureCachedGoContext(nil, settings)
	var packages []apidoc.Package
	var programs []*EditorAnalysis
	for _, target := range targets {
		var loaded *loadedSources
		var goModule *goModuleInputs
		if !target.external && !target.standard && !target.builtin {
			loaded, goModule, err = loadCompilationInputsFrom(target.directory, nil, func() *sourceSnapshot {
				snapshot := newSourceSnapshot()
				snapshot.localDependencies = true
				return snapshot
			})
		} else {
			loaded, goModule, err = loadDocTarget(target, inventory)
		}
		if err != nil {
			return nil, err
		}
		for _, f := range loaded.Files {
			if f.Script {
				return nil, fmt.Errorf("bork doc accepts packages, not executable scripts")
			}
		}
		usage := &goUsage{}
		program, err := checkLoadedProgramTracked(loaded, goModule, ctx, captureEmbedsSnapshot, usage, nil)
		if err != nil {
			return nil, err
		}
		api := check.API(program.info, program.files, target.path)
		if target.builtin {
			api = check.BuiltinAPI(program.info, program.files)
		}
		if api == nil {
			return nil, fmt.Errorf("no checked API for %s", target.path)
		}
		for i := range api.Declarations {
			pos := &api.Declarations[i].Position
			if target.builtin {
				continue
			}
			if target.standard {
				pos.File = strings.TrimPrefix(pos.File, target.path+"/")
			} else {
				abs, err := filepath.Abs(pos.File)
				if err != nil {
					return nil, err
				}
				rel, err := filepath.Rel(target.owner.root, abs)
				if err != nil {
					return nil, err
				}
				pos.File = filepath.ToSlash(rel)
			}
		}
		packages = append(packages, apidoc.Package{API: api, Module: target.owner.path, Version: target.version})
		programs = append(programs, &EditorAnalysis{program: program, usage: usage})
	}
	if !inventory.current() {
		return nil, fmt.Errorf("documentation inputs changed; retry bork doc")
	}
	for _, analysis := range programs {
		program := analysis.program
		if !program.inputs.current() || !program.assets.current() || !docNamesCurrent(analysis, ctx) {
			return nil, fmt.Errorf("documentation inputs changed; retry bork doc")
		}
	}
	if current := captureCachedGoContext(nil, settings); current.namespace != ctx.namespace {
		return nil, fmt.Errorf("go configuration changed; retry bork doc")
	}
	if opts.HTML {
		return apidoc.HTML(packages), nil
	}
	return apidoc.Markdown(packages), nil
}

// Documentation rechecks external package names in its captured module. The
// editor cache additionally requires standard names, since external metadata
// cannot qualify for reuse across requests.
func docNamesCurrent(a *EditorAnalysis, context *goContext) bool {
	for _, input := range a.usage.names {
		if input.inputs != nil {
			if !input.inputs.current() {
				return false
			}
			continue
		}
		usage := &goUsage{}
		names := (goPackages{module: a.program.module, context: context, usage: usage}).Names(input.paths)
		if !maps.Equal(names, input.names) {
			return false
		}
	}
	return true
}

func docTargets(path string, all bool, reader *sourceSnapshot) ([]docTarget, error) {
	if path == "builtin" {
		if all {
			return nil, fmt.Errorf("--all is not supported for builtin; it is one API")
		}
		return []docTarget{{path: path, builtin: true}}, nil
	}
	isDir, err := reader.isDirectory(path)
	if err == nil {
		directory := path
		if !isDir {
			if filepath.Ext(path) != ".bork" {
				return nil, fmt.Errorf("bork doc expects a package directory or .bork file")
			}
			directory = filepath.Dir(path)
		}
		owner, err := findModuleFrom(directory, reader)
		if err != nil {
			return nil, err
		}
		absolute, err := reader.absolute(directory)
		if err != nil {
			return nil, err
		}
		if owner.path == "" {
			owner.root = absolute
		}
		target := docTarget{path: owner.importPath(absolute), directory: absolute, owner: owner}
		if !all {
			return []docTarget{target}, nil
		}
		if owner.path == "" {
			return nil, fmt.Errorf("--all needs a bork.mod module")
		}
		return docPackageDirectories(target, reader)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if strings.HasPrefix(path, std.Prefix) {
		if all {
			return nil, fmt.Errorf("--all is not supported for standard package arguments; name one package")
		}
		if _, _, ok := std.Sources(path); !ok {
			return nil, fmt.Errorf("no standard package %s", path)
		}
		return []docTarget{{path: path, standard: true}}, nil
	}
	cwd, err := reader.workingDirectory()
	if err != nil {
		return nil, err
	}
	owner, err := findModuleFrom(cwd, reader)
	if err != nil {
		return nil, err
	}
	if owner.path == "" {
		return nil, fmt.Errorf("library documentation needs a project with pinned dependencies; run bork deps get <module>@<version>")
	}
	files := []*syntax.File{{Path: filepath.Join(cwd, "<bork-doc>"), Package: owner.path}}
	graph, err := compileLibraryGraph(files, reader)
	if err != nil {
		return nil, err
	}
	if all {
		if lib, ok := graph.libraries[path]; ok {
			return docPackageDirectories(docTarget{path: path, directory: lib.root, owner: lib.module, version: lib.version, external: true}, reader)
		}
	}
	dependency, directory, err := graph.packageDirectory(path, reader)
	if err != nil {
		return nil, err
	}
	target := docTarget{path: path, directory: directory, owner: dependency, version: graph.libraries[dependency.path].version, external: true}
	if all {
		return docPackageDirectories(target, reader)
	}
	return []docTarget{target}, nil
}

func docPackageDirectories(target docTarget, reader sourceReader) ([]docTarget, error) {
	var targets []docTarget
	var walk func(string) error
	walk = func(dir string) error {
		entries, err := reader.directory(dir)
		if err != nil {
			return err
		}
		if _, err := sourceFiles(dir, reader); err == nil {
			item := target
			item.directory = dir
			item.path = target.owner.importPath(dir)
			targets = append(targets, item)
		}
		for _, e := range entries {
			if !e.directory || strings.HasPrefix(e.name, ".") || e.name == "vendor" {
				continue
			}
			child := filepath.Join(dir, e.name)
			for _, name := range []string{ModFile, "go.mod"} {
				if _, err := reader.readFile(filepath.Join(child, name)); err == nil {
					child = ""
					break
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			if child != "" {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(target.directory); err != nil {
		return nil, err
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].path < targets[j].path })
	return targets, nil
}

func loadDocTarget(target docTarget, inputs *sourceSnapshot) (*loadedSources, *goModuleInputs, error) {
	diags := &diag.List{}
	var owner module
	if target.external {
		cwd, err := inputs.workingDirectory()
		if err != nil {
			return nil, nil, err
		}
		owner, err = findModuleFrom(cwd, inputs)
		if err != nil {
			return nil, nil, err
		}
	}
	l := &loader{mod: owner, owners: map[string]module{}, inputs: inputs, diags: diags, state: map[string]int{}}
	l.preludeFiles(diags)
	root := target.path
	if target.builtin {
		// A synthetic standard root keeps the caller's manifests out of the
		// embedded API check, while providing the checker a root package.
		root = std.Prefix + "builtin"
		file := syntax.Parse(root+"/<bork-doc>", []byte("fn main() {}\n"), diags)
		file.Package = root
		l.files = append(l.files, file)
	}
	if target.external {
		cwd, _ := inputs.workingDirectory()
		file := syntax.Parse(filepath.Join(cwd, "<bork-doc>"), []byte("fn main() {}\n"), diags)
		file.Package = owner.path
		l.files = append(l.files, file)
	}
	if !target.builtin {
		if err := l.loadImport(&syntax.Import{Path: target.path, Name: "documented"}); err != nil {
			return nil, nil, err
		}
	}
	if diags.Len() != 0 {
		return nil, nil, &DiagError{Diags: diags}
	}
	goModule, err := captureGoModule(l.files, inputs)
	if err != nil {
		return nil, nil, err
	}
	return &loadedSources{Files: l.files, Root: root, Diags: diags, Inputs: inputs}, goModule, nil
}
