package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

// User manifests live beside bork.mod, so every package in a module sees the
// same dependencies in type checking, predicate evaluation, and builds.
func userGoDependencies(files []*syntax.File) ([]std.GoDependencyManifest, error) {
	for _, f := range files {
		if f.Prelude || strings.HasPrefix(f.Package, std.Prefix) || f.Path == "" {
			continue
		}
		mod, err := findModule(filepath.Dir(f.Path))
		if err != nil {
			return nil, err
		}
		if mod.path == "" {
			continue
		}
		manifestPath := filepath.Join(mod.root, "go-deps.mod")
		data, err := os.ReadFile(manifestPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", manifestPath, err)
		}
		sumPath := filepath.Join(mod.root, "go-deps.sum")
		sum, err := os.ReadFile(sumPath)
		if err != nil {
			return nil, fmt.Errorf("%s: read pinned Go checksums: %w", sumPath, err)
		}
		return []std.GoDependencyManifest{{Name: mod.root, Mod: data, Sum: sum}}, nil
	}
	return nil, nil
}

// Validate and merge manifests without running Go, including check-only programs.
func programGoModule(files []*syntax.File) ([]byte, []byte, error) {
	var importPaths []string
	for _, file := range files {
		importPaths = append(importPaths, file.Package)
	}
	user, err := userGoDependencies(files)
	if err != nil {
		return nil, nil, err
	}
	goMod, goSum, err := std.GoModuleFiles(importPaths, user...)
	if err != nil {
		return nil, nil, fmt.Errorf("go dependencies: %w", err)
	}
	return goMod, goSum, nil
}
