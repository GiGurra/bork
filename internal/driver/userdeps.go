package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
	"golang.org/x/mod/modfile"
	gomodule "golang.org/x/mod/module"
)

// User manifests live beside bork.mod, so every package in a module sees the
// same dependencies in type checking, predicate evaluation, and builds.
func userGoDependencies(files []*syntax.File) ([]std.GoDependencyManifest, error) {
	return userGoDependenciesFrom(files, diskSources{})
}

func userGoDependenciesFrom(files []*syntax.File, reader sourceReader) ([]std.GoDependencyManifest, error) {
	for _, f := range files {
		if f.Prelude || strings.HasPrefix(f.Package, std.Prefix) || f.Path == "" {
			continue
		}
		mod, err := findModuleFrom(filepath.Dir(f.Path), reader)
		if err != nil {
			return nil, err
		}
		if mod.path == "" {
			if f.Script {
				header := readScriptHeader(f, false, &diag.List{})
				if len(header.requirements) != 0 {
					return scriptGoDependencies(f, header.requirements, reader)
				}
			}
			continue
		}
		return moduleDependencies(mod, reader)
	}
	return nil, nil
}

// Validate and merge manifests without running Go, including check-only programs.
func programGoModuleFrom(files []*syntax.File, reader sourceReader) ([]byte, []byte, error) {
	var importPaths []string
	for _, file := range files {
		importPaths = append(importPaths, file.Package)
	}
	user, err := userGoDependenciesFrom(files, reader)
	if err != nil {
		return nil, nil, err
	}
	goMod, goSum, err := std.GoModuleFiles(importPaths, user...)
	if err != nil {
		return nil, nil, fmt.Errorf("go dependencies: %w", err)
	}
	return goMod, goSum, nil
}

// goModuleInputs owns the merged module bytes for one compilation request.
// Every metadata load and generated build stages copies of these same bytes.
type goModuleInputs struct {
	mod []byte
	sum []byte
}

func captureGoModule(files []*syntax.File, reader sourceReader) (*goModuleInputs, error) {
	mod, sum, err := programGoModuleFrom(files, reader)
	if err != nil {
		return nil, err
	}
	libraries, err := hasLibrarySources(files, reader)
	if err != nil {
		return nil, err
	}
	if libraries {
		graph, err := compileLibraryGraph(files, reader)
		if err != nil {
			return nil, err
		}
		mod, sum = graph.mod, graph.sum
	}
	mod, err = addLocalGoModule(mod, files, reader)
	if err != nil {
		return nil, err
	}
	return &goModuleInputs{slices.Clone(mod), slices.Clone(sum)}, nil
}

func (inputs *goModuleInputs) write(dir string, hook goModuleHookFunc) (bool, error) {
	mod := slices.Clone(inputs.mod)
	if hook != nil {
		mod = hook(mod)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0o644); err != nil {
		return false, err
	}
	if len(inputs.sum) != 0 {
		if err := os.WriteFile(filepath.Join(dir, "go.sum"), inputs.sum, 0o644); err != nil {
			return false, err
		}
	}
	return len(inputs.sum) != 0, nil
}

// Generated compilation modules can bind to helpers in the author's own
// repository without publishing that repository first. This temporary replace
// never enters either committed manifest.
func addLocalGoModule(data []byte, files []*syntax.File, reader sourceReader) ([]byte, error) {
	for _, file := range files {
		if file.Prelude || strings.HasPrefix(file.Package, std.Prefix) {
			continue
		}
		owner, err := findModuleFrom(filepath.Dir(file.Path), reader)
		if err != nil {
			return nil, err
		}
		if owner.path == "" {
			return data, nil
		}
		marker, err := reader.readFile(filepath.Join(owner.root, "go.mod"))
		if os.IsNotExist(err) {
			return data, nil
		}
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(string(marker), generatedModuleHeader) {
			return data, nil
		}
		manifest, err := modfile.Parse("compiler module", data, nil)
		if err != nil {
			return nil, err
		}
		_, major, ok := gomodule.SplitPathVersion(owner.path)
		if !ok {
			return nil, fmt.Errorf("invalid module path %s", owner.path)
		}
		version := "v0.0.0"
		if major != "" {
			version = strings.TrimLeft(major, "/.") + ".0.0"
		}
		if err := manifest.AddRequire(owner.path, version); err != nil {
			return nil, err
		}
		if err := manifest.AddReplace(owner.path, "", owner.root, ""); err != nil {
			return nil, err
		}
		return manifest.Format()
	}
	return data, nil
}
