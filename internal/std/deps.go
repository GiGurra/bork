package std

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// GoModuleFiles combines the pinned module declarations for imported standard
// packages. Packages without go-deps.mod have no third-party Go dependencies.
func GoModuleFiles(importPaths []string) (mod, sum []byte, err error) {
	return goModuleFiles(files, importPaths)
}

func goModuleFiles(sources fs.FS, importPaths []string) ([]byte, []byte, error) {
	requirements := map[string]string{}
	checksums := map[string]string{}
	seen := map[string]bool{}
	goVersion := "1.22"
	for _, importPath := range importPaths {
		dir, standard := strings.CutPrefix(importPath, Prefix)
		if !standard || seen[dir] {
			continue
		}
		seen[dir] = true
		manifestPath := path.Join(dir, "go-deps.mod")
		data, err := fs.ReadFile(sources, manifestPath)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		manifest, err := modfile.Parse(manifestPath, data, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", importPath, err)
		}
		if len(manifest.Replace) != 0 || len(manifest.Exclude) != 0 || len(manifest.Retract) != 0 || manifest.Toolchain != nil || len(manifest.Godebug) != 0 || len(manifest.Tool) != 0 || len(manifest.Ignore) != 0 {
			return nil, nil, fmt.Errorf("%s: std Go dependencies support only module, go, and pinned require declarations", importPath)
		}
		if manifest.Go != nil && semver.Compare("v"+manifest.Go.Version, "v"+goVersion) > 0 {
			goVersion = manifest.Go.Version
		}
		for _, requirement := range manifest.Require {
			dep := requirement.Mod
			if err := module.Check(dep.Path, dep.Version); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", importPath, err)
			}
			if module.CanonicalVersion(dep.Version) != dep.Version {
				return nil, nil, fmt.Errorf("%s: dependency %s must use a canonical pinned version", importPath, dep.Path)
			}
			if old := requirements[dep.Path]; old == "" || semver.Compare(dep.Version, old) > 0 {
				requirements[dep.Path] = dep.Version
			}
		}
		data, err = fs.ReadFile(sources, path.Join(dir, "go-deps.sum"))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: read pinned Go checksums: %w", importPath, err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			if len(fields) != 3 || !strings.HasPrefix(fields[2], "h1:") {
				return nil, nil, fmt.Errorf("%s: invalid go.sum entry: %s", importPath, line)
			}
			key := fields[0] + " " + fields[1]
			if old := checksums[key]; old != "" && old != fields[2] {
				return nil, nil, fmt.Errorf("%s: conflicting Go checksum for %s", importPath, key)
			}
			checksums[key] = fields[2]
		}
	}
	manifest := &modfile.File{}
	if err := manifest.AddModuleStmt("borkprogram"); err != nil {
		return nil, nil, err
	}
	if err := manifest.AddGoStmt(goVersion); err != nil {
		return nil, nil, err
	}
	paths := make([]string, 0, len(requirements))
	for p := range requirements {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		version := requirements[p]
		if checksums[p+" "+version] == "" || checksums[p+" "+version+"/go.mod"] == "" {
			return nil, nil, fmt.Errorf("missing pinned Go checksums for %s %s", p, version)
		}
		if err := manifest.AddRequire(p, version); err != nil {
			return nil, nil, err
		}
	}
	mod, err := manifest.Format()
	if err != nil {
		return nil, nil, err
	}
	keys := make([]string, 0, len(checksums))
	for key := range checksums {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var sum strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&sum, "%s %s\n", key, checksums[key])
	}
	return mod, []byte(sum.String()), nil
}
