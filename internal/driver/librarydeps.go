package driver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/gotoolchain"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
	"golang.org/x/mod/sumdb/dirhash"
)

type downloadedModule struct {
	Path, Version, Dir, GoMod, Sum, GoModSum, Error string
}

type libraryModule struct {
	module
	version        string
	unsafePackages []string
}

type libraryGraph struct {
	libraries map[string]libraryModule
	mod, sum  []byte
}

func dependenciesLocalOnly(reader sourceReader) bool {
	snapshot, ok := reader.(*sourceSnapshot)
	return ok && snapshot.localDependencies
}

func dependencyGoWithSettings(dir string, settings []string, args ...string) ([]byte, error) {
	env := append(os.Environ(), "GOWORK=off", "GO111MODULE=on", "GOFLAGS=")
	local := false
	for _, setting := range settings {
		if setting == "GOTOOLCHAIN=local" {
			local = true
			continue
		}
		env = append(env, setting)
	}
	_, env, tool, err := gotoolchain.Query("go", dir, env)
	if err != nil {
		return nil, err
	}
	if local {
		env = append(env, "GOTOOLCHAIN=local")
	}
	cmd := exec.Command(tool, args...)
	cmd.Dir, cmd.Env = dir, env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go %s: %w\n%s\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func checksumEntries(data []byte) map[string]string {
	entries := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 {
			entries[fields[0]+" "+fields[1]] = fields[2]
		}
	}
	return entries
}

// Resolve using Go, expanding source-only bork modules into root requirements
// before Go's graph pruning can hide their outgoing dependencies.
func resolveLibraryGraph(dir string, reader sourceReader, pinned []byte, readonly bool) (*libraryGraph, error) {
	pins := checksumEntries(pinned)
	for range 128 {
		initial, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			return nil, err
		}
		initialManifest, err := modfile.Parse("go.mod", initial, nil)
		if err != nil {
			return nil, err
		}
		if len(initialManifest.Require) == 0 {
			sums, err := os.ReadFile(filepath.Join(dir, "go.sum"))
			return &libraryGraph{libraries: map[string]libraryModule{}, mod: initial, sum: sums}, err
		}
		var settings []string
		if dependenciesLocalOnly(reader) {
			settings = []string{"GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local"}
		}
		out, err := dependencyGoWithSettings(dir, settings, "mod", "download", "-json", "all")
		if err != nil {
			if dependenciesLocalOnly(reader) {
				return nil, fmt.Errorf("dependencies are not cached; run bork deps download: %w", err)
			}
			return nil, err
		}
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err != nil {
			return nil, err
		}
		manifest, err := modfile.Parse("go.mod", data, nil)
		if err != nil {
			return nil, err
		}
		required := map[string]string{}
		for _, dep := range manifest.Require {
			required[dep.Mod.Path] = dep.Mod.Version
		}
		graph := &libraryGraph{libraries: map[string]libraryModule{}}
		changed := false
		selected := map[string]string{}
		decoder := json.NewDecoder(bytes.NewReader(out))
		for {
			var dep downloadedModule
			if err := decoder.Decode(&dep); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				return nil, fmt.Errorf("decode downloaded modules: %w", err)
			}
			if dep.Error != "" || dep.Dir == "" {
				return nil, fmt.Errorf("download %s@%s: %s", dep.Path, dep.Version, dep.Error)
			}
			selected[dep.Path] = dep.Version
			if readonly && (pins[dep.Path+" "+dep.Version] != dep.Sum || pins[dep.Path+" "+dep.Version+"/go.mod"] != dep.GoModSum || dep.Sum == "" || dep.GoModSum == "") {
				return nil, fmt.Errorf("missing or conflicting pinned checksums for %s@%s; run bork deps download", dep.Path, dep.Version)
			}
			marker := filepath.Join(dep.Dir, ModFile)
			text, err := reader.readFile(marker)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, err
			}
			mod, err := parseModFile(string(text))
			if err != nil {
				return nil, fmt.Errorf("%s@%s: %w", dep.Path, dep.Version, err)
			}
			if mod.path != dep.Path {
				return nil, fmt.Errorf("%s@%s: bork.mod module %s does not match the Go module path", dep.Path, dep.Version, mod.path)
			}
			mod.root = dep.Dir
			for path := range mod.unsafe {
				if path != mod.path && !strings.HasPrefix(path, mod.path+"/") {
					return nil, fmt.Errorf("%s@%s: unsafe grant %s is outside its own module", dep.Path, dep.Version, path)
				}
			}
			files, err := moduleFileInventory(mod.root, reader)
			if err != nil {
				return nil, err
			}
			hash, err := hashModuleFiles(dep, files, reader)
			if err != nil {
				return nil, err
			}
			if dep.Sum == "" || hash != dep.Sum {
				return nil, fmt.Errorf("%s@%s: extracted library content differs from its pinned checksum; restore the Go module cache", dep.Path, dep.Version)
			}
			goData, err := reader.readFile(filepath.Join(mod.root, "go.mod"))
			if err != nil {
				return nil, err
			}
			if err := checkGeneratedModule(mod, goData); err != nil {
				return nil, fmt.Errorf("library %s@%s: %w", dep.Path, dep.Version, err)
			}
			imports, err := standardImports(files, reader)
			if err != nil {
				return nil, err
			}
			stdMod, _, err := std.GoModuleFiles(imports)
			if err != nil {
				return nil, err
			}
			stdManifest, err := modfile.Parse("std dependencies", stdMod, nil)
			if err != nil {
				return nil, err
			}
			declared := map[string]string{}
			for _, requirement := range mod.requirements {
				declared[requirement.Path] = requirement.Version
			}
			for _, requirement := range stdManifest.Require {
				if semver.Compare(declared[requirement.Mod.Path], requirement.Mod.Version) < 0 {
					return nil, fmt.Errorf("library %s@%s does not declare std dependency %s@%s; its author must run bork deps download before publishing", dep.Path, dep.Version, requirement.Mod.Path, requirement.Mod.Version)
				}
			}
			unsafePackages, err := libraryUnsafePackages(mod, files, reader)
			if err != nil {
				return nil, err
			}
			graph.libraries[dep.Path] = libraryModule{module: mod, version: dep.Version, unsafePackages: unsafePackages}
			if required[dep.Path] != dep.Version {
				if err := manifest.AddRequire(dep.Path, dep.Version); err != nil {
					return nil, err
				}
				changed = true
			}
		}
		// Persist every selected requirement, so all compiler stages start from
		// the same expanded graph even when they have no Go package imports.
		for path, version := range selected {
			if required[path] != version {
				if err := manifest.AddRequire(path, version); err != nil {
					return nil, err
				}
				changed = true
			}
		}
		manifest.DropToolchainStmt()
		manifest.SortBlocks()
		graph.mod, err = manifest.Format()
		if err != nil {
			return nil, err
		}
		if changed {
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), graph.mod, 0o644); err != nil {
				return nil, err
			}
			continue
		}
		graph.sum, err = os.ReadFile(filepath.Join(dir, "go.sum"))
		return graph, err
	}
	return nil, errors.New("bork dependency graph did not converge after 128 expansion steps")
}

func moduleFileInventory(root string, reader sourceReader) ([]string, error) {
	var files []string
	var walk func(string) error
	walk = func(dir string) error {
		entries, err := reader.directory(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.name)
			if entry.directory {
				if err := walk(path); err != nil {
					return err
				}
			} else {
				files = append(files, path)
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func hashModuleFiles(dep downloadedModule, files []string, reader sourceReader) (string, error) {
	paths := make([]string, 0, len(files))
	originals := map[string]string{}
	for _, file := range files {
		rel, err := filepath.Rel(dep.Dir, file)
		if err != nil {
			return "", err
		}
		name := dep.Path + "@" + dep.Version + "/" + filepath.ToSlash(rel)
		paths = append(paths, name)
		originals[name] = file
	}
	return dirhash.Hash1(paths, func(name string) (io.ReadCloser, error) {
		data, err := reader.readFile(originals[name])
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

func compileLibraryGraph(files []*syntax.File, reader sourceReader) (*libraryGraph, error) {
	data, sums, err := programGoModuleFrom(files, reader)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "bork-library-graph-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), data, 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), sums, 0o644); err != nil {
		return nil, err
	}
	return resolveLibraryGraph(dir, reader, sums, true)
}

func standardImports(files []string, reader sourceReader) ([]string, error) {
	var imports []string
	for _, path := range files {
		if filepath.Ext(path) != ".bork" {
			continue
		}
		data, err := reader.readFile(path)
		if err != nil {
			return nil, err
		}
		file := syntax.Parse(path, data, &diag.List{})
		for _, imp := range file.Imports {
			if strings.HasPrefix(imp.Path, std.Prefix) {
				imports = append(imports, imp.Path)
			}
		}
	}
	return imports, nil
}

func sameLibraryVersions(a, b *libraryGraph) bool {
	if len(a.libraries) != len(b.libraries) {
		return false
	}
	for path, mod := range a.libraries {
		other, ok := b.libraries[path]
		if !ok || mod.version != other.version || mod.root != other.root {
			return false
		}
	}
	return true
}

func (graph *libraryGraph) packageDirectory(importPath string, reader sourceReader) (module, string, error) {
	var owner module
	dir := ""
	paths := make([]string, 0, len(graph.libraries))
	for path := range graph.libraries {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if importPath != path && !strings.HasPrefix(importPath, path+"/") {
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(importPath, path), "/")
		if rel != "" && (strings.Contains(rel, "\\") || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || strings.HasPrefix(rel, "../")) {
			return module{}, "", fmt.Errorf("invalid package path %s", importPath)
		}
		mod := graph.libraries[path]
		candidate := filepath.Join(mod.root, filepath.FromSlash(rel))
		if _, err := sourceFiles(candidate, reader); err != nil {
			continue
		}
		if dir != "" {
			return owner, dir, fmt.Errorf("package is provided by both %s and %s", owner.path, mod.path)
		}
		owner, dir = mod.module, candidate
	}
	if dir == "" {
		return module{}, "", fmt.Errorf("no selected bork library provides this package; add its module with bork deps get <module>@<version>")
	}
	return owner, dir, nil
}

func hasLibrarySources(files []*syntax.File, reader sourceReader) (bool, error) {
	root := ""
	for _, file := range files {
		if file.Prelude || strings.HasPrefix(file.Package, std.Prefix) {
			continue
		}
		mod, err := findModuleFrom(filepath.Dir(file.Path), reader)
		if err != nil {
			return false, err
		}
		if root == "" {
			root = mod.root
		}
		if mod.root != root {
			return true, nil
		}
	}
	return false, nil
}

func projectStandardImports(root string, reader sourceReader) ([]string, error) {
	var files []string
	var walk func(string) error
	walk = func(dir string) error {
		entries, err := reader.directory(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.name, ".") || entry.name == "vendor" {
				continue
			}
			path := filepath.Join(dir, entry.name)
			if entry.directory {
				if _, err := reader.readFile(filepath.Join(path, ModFile)); err == nil {
					continue
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if _, err := reader.readFile(filepath.Join(path, "go.mod")); err == nil {
					continue
				} else if !errors.Is(err, os.ErrNotExist) {
					return err
				}
				if err := walk(path); err != nil {
					return err
				}
			} else if filepath.Ext(path) == ".bork" {
				files = append(files, path)
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return standardImports(files, reader)
}

func addProjectStandardDependencies(dir, root string, reader sourceReader) error {
	imports, err := projectStandardImports(root, reader)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(filepath.Join(dir, "go.sum"))
	if err != nil {
		return err
	}
	manifest, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return err
	}
	merged, mergedSums, err := std.GoModuleFiles(imports, std.GoDependencyManifest{Name: root, Mod: data, Sum: sums})
	if err != nil {
		return err
	}
	combined, err := modfile.Parse("go.mod", merged, nil)
	if err != nil {
		return err
	}
	if err := combined.AddModuleStmt(manifest.Module.Mod.Path); err != nil {
		return err
	}
	merged, err = combined.Format()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), merged, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "go.sum"), mergedSums, 0o644)
}

func reportUnsafeLibraries(out io.Writer, graph *libraryGraph, previous map[string]string) error {
	paths := make([]string, 0, len(graph.libraries))
	for path, mod := range graph.libraries {
		if len(mod.unsafePackages) != 0 && previous[path] != mod.version {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		mod := graph.libraries[path]
		if _, err := fmt.Fprintf(out, "Dependency %s@%s declares unsafe Go packages: %s\n", path, mod.version, strings.Join(mod.unsafePackages, ", ")); err != nil {
			return err
		}
	}
	return nil
}

func libraryUnsafePackages(mod module, files []string, reader sourceReader) ([]string, error) {
	packages := map[string]bool{}
	for _, path := range files {
		if filepath.Ext(path) != ".bork" {
			continue
		}
		data, err := reader.readFile(path)
		if err != nil {
			return nil, err
		}
		file := syntax.Parse(path, data, &diag.List{})
		for _, fn := range file.Funcs {
			if fn.IsGo() {
				packages[mod.importPath(filepath.Dir(path))] = true
				break
			}
		}
	}
	paths := make([]string, 0, len(packages))
	for path := range packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}
