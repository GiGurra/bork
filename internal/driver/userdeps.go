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
