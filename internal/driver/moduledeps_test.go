package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

func TestBorkModuleRequirements(t *testing.T) {
	t.Parallel()
	mod, err := parseModFile("module example.com/app\nrequire example.com/lib/v2 v2.1.0 // pinned\nunsafe \"example.com/app/ffi\"\n")
	if err != nil || len(mod.requirements) != 1 || mod.requirements[0].Version != "v2.1.0" || !mod.unsafe["example.com/app/ffi"] {
		t.Fatalf("requirements and grants: %+v, %v", mod, err)
	}
	for _, declaration := range []string{
		"require example.com/lib latest",
		"require example.com/lib v1",
		"require example.com/lib v2.0.0",
		"require example.com/lib v1.0.0\nrequire example.com/lib v1.1.0",
		"require example.com/lib",
	} {
		if _, err := parseModFile("module example.com/app\n" + declaration + "\n"); err == nil {
			t.Fatalf("accepted %q", declaration)
		}
	}
}

func TestGeneratedModuleDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(ModFile, "module example.com/app\nrequire example.com/lib v1.0.0\n")
	files := []*syntax.File{{Package: "example.com/app", Path: filepath.Join(root, "main.bork")}}
	for _, data := range []string{
		"",
		generatedModuleHeader + "module example.com/other\ngo 1.26\nrequire example.com/lib v1.0.0\n",
		generatedModuleHeader + "module example.com/app\ngo 1.26\nrequire example.com/lib v1.1.0\n",
		generatedModuleHeader + "module example.com/app\ngo 1.26\nrequire example.com/lib v1.0.0\nreplace example.com/lib => ../lib\n",
		generatedModuleHeader + "module example.com/app\ngo 1.26\n",
	} {
		write("go.mod", data)
		if _, err := userGoDependencies(files); err == nil || !strings.Contains(err.Error(), "run bork deps download") {
			t.Fatalf("drift %q: %v", data, err)
		}
	}
	// Formatting and the Go-only indirect annotation do not change meaning.
	write("go.mod", generatedModuleHeader+"module example.com/app\n\ngo 1.26\n\nrequire (\n example.com/lib v1.0.0 // indirect\n)\n")
	if _, err := userGoDependencies(files); err == nil || !strings.Contains(err.Error(), "bork.sum") {
		t.Fatalf("missing sums: %v", err)
	}
	write("bork.sum", "")
	if _, err := userGoDependencies(files); err != nil {
		t.Fatal(err)
	}
	write("go-deps.mod", "module example.com/app\ngo 1.26\n")
	if _, err := userGoDependencies(files); err == nil || !strings.Contains(err.Error(), "cannot be mixed") {
		t.Fatalf("ambiguous manifests: %v", err)
	}
}

func TestDepsProtectsHandwrittenGoModule(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for name, data := range map[string]string{ModFile: "module example.com/app\n", "go.mod": "module example.com/existing\ngo 1.26\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range []string{"init", "download", "get"} {
		var queries []string
		if action == "get" {
			queries = []string{"example.com/lib@v1.0.0"}
		}
		if err := Deps(root, action, queries); err == nil || !strings.Contains(err.Error(), "not generated") {
			t.Fatalf("%s overwrote Go project: %v", action, err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.Contains(string(data), "example.com/existing") {
		t.Fatal("Go module changed")
	}
}

func TestDependencyMigrationRecovery(t *testing.T) {
	t.Parallel()
	for _, leftover := range []string{"both", "sum only", "conflict"} {
		t.Run(leftover, func(t *testing.T) {
			root := t.TempDir()
			mod, err := parseModFile("module example.com/app\nrequire example.com/lib v1.0.0\n")
			if err != nil {
				t.Fatal(err)
			}
			data, err := generatedModule(mod, "")
			if err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				ModFile:       "module example.com/app\nrequire example.com/lib v1.0.0\n",
				"go.mod":      string(data),
				"bork.sum":    "example.com/lib v1.0.0 h1:content\nexample.com/lib v1.0.0/go.mod h1:metadata\n",
				"go-deps.sum": "old sums\n",
			}
			if leftover != "sum only" {
				version := "v1.0.0"
				if leftover == "conflict" {
					version = "v1.1.0"
				}
				files["go-deps.mod"] = "module descriptive\ngo 1.22\nrequire example.com/lib " + version + "\n"
			}
			for name, text := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			err = Deps(root, "migrate", nil)
			if leftover == "conflict" {
				if err == nil || !strings.Contains(err.Error(), "differ") {
					t.Fatalf("conflicting migration: %v", err)
				}
				if _, err := os.Stat(filepath.Join(root, "go-deps.mod")); err != nil {
					t.Fatal("conflicting manifest was removed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"go-deps.mod", "go-deps.sum"} {
				if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
					t.Fatalf("leftover %s: %v", name, err)
				}
			}
		})
	}
}
