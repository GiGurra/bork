package driver

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/std"
	"golang.org/x/mod/modfile"
)

func TestDepsHelper(t *testing.T) {
	proxy := t.TempDir()
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	publish := func(path, version, mod, source string) {
		t.Helper()
		base := filepath.Join(proxy, filepath.FromSlash(path), "@v")
		write(filepath.Join(base, version+".mod"), []byte(mod))
		write(filepath.Join(base, version+".info"), []byte(fmt.Sprintf(`{"Version":%q,"Time":"2026-01-01T00:00:00Z"}`, version)))
		var data bytes.Buffer
		z := zip.NewWriter(&data)
		for name, content := range map[string]string{"go.mod": mod, "dep.go": source} {
			f, err := z.Create(path + "@" + version + "/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(base, version+".zip"), data.Bytes())
	}
	publish("example.com/indirect", "v1.0.0", "module example.com/indirect\ngo 1.22\n", "package indirect\nfunc Value() int { return 7 }\n")
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		publish("example.com/direct", version, "module example.com/direct\ngo 1.22\nrequire example.com/indirect v1.0.0\n", "package direct\nimport \"example.com/indirect\"\nfunc Value() int { return indirect.Value() }\n")
	}
	t.Setenv("GOPROXY", "file://"+filepath.ToSlash(proxy))
	t.Setenv("GOSUMDB", "off")
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	t.Cleanup(func() {
		_ = filepath.WalkDir(cache, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return err
		})
	})
	// The helper must ignore unrelated Go workspace and modfile flags.
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "missing.work"))
	t.Setenv("GOFLAGS", "-modfile=missing.mod")
	root := t.TempDir()
	write(filepath.Join(root, ModFile), []byte("module example.com/app\nunsafe \"example.com/app\"\n"))
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	if err := Deps(sub, "init", nil); err != nil {
		t.Fatal(err)
	}
	if len(read("bork.sum")) != 0 {
		t.Fatal("initial checksums should be empty")
	}
	initial := read("go.mod")
	if err := Deps(root, "init", nil); err == nil {
		t.Fatal("init must refuse overwrite")
	}
	if !bytes.Equal(initial, read("go.mod")) {
		t.Fatal("init overwrote manifest")
	}
	// Replacements preserve file permissions and do not require truncating the
	// original manifest (which can be read-only in a writable directory).
	if err := os.Chmod(filepath.Join(root, "go.mod"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v1.0.0", "v1.1.0"} {
		if err := Deps(sub, "get", []string{"example.com/direct@" + version}); err != nil {
			t.Fatal(err)
		}
		st, err := os.Stat(filepath.Join(root, "go.mod"))
		if err != nil || st.Mode().Perm() != 0o444 {
			t.Fatalf("manifest permissions changed: %v", err)
		}
		manifest := read("go.mod")
		parsed, err := modfile.Parse("go.mod", manifest, nil)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, dep := range parsed.Require {
			switch dep.Mod.Path {
			case "example.com/direct":
				found[dep.Mod.Path] = dep.Mod.Version == version
			case "example.com/indirect":
				found[dep.Mod.Path] = dep.Mod.Version == "v1.0.0"
			}
		}
		if !found["example.com/direct"] || !found["example.com/indirect"] {
			t.Fatalf("requirements: %s", manifest)
		}
		if _, _, err := std.GoModuleFiles(nil, std.GoDependencyManifest{Name: root, Mod: manifest, Sum: read("bork.sum")}); err != nil {
			t.Fatal(err)
		}
	}
	beforeMod, beforeSum := read("go.mod"), read("bork.sum")
	beforeBork := read(ModFile)
	if err := Deps(root, "get", []string{"example.com/missing@v1.0.0"}); err == nil {
		t.Fatal("missing dependency should fail")
	}
	if !bytes.Equal(beforeMod, read("go.mod")) || !bytes.Equal(beforeSum, read("bork.sum")) || !bytes.Equal(beforeBork, read(ModFile)) {
		t.Fatal("failed get modified manifests")
	}
	if err := os.Remove(filepath.Join(root, "bork.sum")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	if err := Deps(sub, "download", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := std.GoModuleFiles(nil, std.GoDependencyManifest{Name: root, Mod: read("go.mod"), Sum: read("bork.sum")}); err != nil {
		t.Fatal(err)
	}
	// Both check and build consume exactly the helper's pinned module graph.
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	write(filepath.Join(root, "main.bork"), []byte("fn Value(): Int unsafe go \"example.com/direct.Value\"\nfn main() uses io { println(Value()) }\n"))
	if _, _, err := Check(root); err != nil {
		t.Fatal(err)
	}
	if err := Build(root, filepath.Join(t.TempDir(), "app")); err != nil {
		t.Fatal(err)
	}
	// A manual Go update must not become the bork project's source of truth.
	if err := os.Chmod(filepath.Join(root, "go.mod"), 0o644); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, "go.mod"), bytes.ReplaceAll(read("go.mod"), []byte("v1.1.0"), []byte("v1.0.0")))
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "run bork deps download") {
		t.Fatalf("manifest drift: %v", err)
	}
	if err := Deps(root, "download", nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBork, read(ModFile)) || !bytes.Equal(beforeMod, read("go.mod")) {
		t.Fatal("repair did not preserve authoritative requirements")
	}
	// Existing manifests remain usable, and explicit migration preserves their
	// requirements, checksums, comments, and unsafe grants without a go.sum.
	legacy := t.TempDir()
	write(filepath.Join(legacy, ModFile), []byte("// library\nmodule example.com/legacy\nunsafe \"example.com/legacy\"\n"))
	write(filepath.Join(legacy, "go-deps.mod"), []byte("module descriptive\ngo 1.22\nrequire example.com/direct v1.1.0\n"))
	write(filepath.Join(legacy, "go-deps.sum"), read("bork.sum"))
	if err := Deps(legacy, "download", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "bork.sum")); !os.IsNotExist(err) {
		t.Fatal("legacy download silently migrated manifests")
	}
	if err := Deps(legacy, "migrate", nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go-deps.mod", "go-deps.sum", "go.sum"} {
		if _, err := os.Stat(filepath.Join(legacy, name)); !os.IsNotExist(err) {
			t.Fatalf("unexpected %s after migration: %v", name, err)
		}
	}
	legacyMod, err := findModule(legacy)
	if err != nil || len(legacyMod.requirements) == 0 || !legacyMod.unsafe["example.com/legacy"] {
		t.Fatalf("migration lost metadata: %+v, %v", legacyMod, err)
	}
	deps, err := moduleDependencies(legacyMod, diskSources{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := std.GoModuleFiles(nil, deps...); err != nil {
		t.Fatal(err)
	}
	if err := Deps(root, "get", []string{"example.com/direct@none"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(read("go.mod")), "example.com/direct") {
		t.Fatal("dependency not removed")
	}
}

func TestDepsHelperErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := Deps(root, "init", nil); err == nil || !strings.Contains(err.Error(), "bork.mod") {
		t.Fatalf("no module: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ModFile), []byte("module example.com/app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"get", "unknown"} {
		if err := Deps(root, action, nil); err == nil {
			t.Fatalf("%s should fail", action)
		}
	}
	for _, query := range []string{"-u", "../local", "toolchain@latest", "go@latest"} {
		if err := Deps(root, "get", []string{query}); err == nil {
			t.Fatalf("%q should fail", query)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go-deps.sum"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	data := []byte("module example.com/app\ngo 1.22\nreplace example.com/unused => ../unused\n")
	if err := os.WriteFile(filepath.Join(root, "go-deps.mod"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Deps(root, "download", nil); err == nil || !strings.Contains(err.Error(), "support only") {
		t.Fatalf("unsupported directive: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "go-deps.mod"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("validation failure modified manifest")
	}
}
