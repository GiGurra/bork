package driver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildInventoryTracksGoDependencyEdits(t *testing.T) {
	if !cacheTrimSupported() {
		t.Skip("persistent build receipts require Linux or macOS")
	}
	root := t.TempDir()
	dependency := filepath.Join(root, "dep")
	program := filepath.Join(root, "program")
	for _, dir := range []string{dependency, program} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dependency, "go.mod"): "module example.com/dep\ngo 1.26\n",
		filepath.Join(dependency, "dep.go"): "package dep\nfunc Value() int { return 1 }\n",
		filepath.Join(program, "go.mod"):    "module example.com/program\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(program, "main.go"):   "package main\nimport \"example.com/dep\"\nfunc main() { println(dep.Value()) }\n",
	}
	for path, source := range files {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=0"}})
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	for _, change := range []string{"content", "membership", "module"} {
		t.Run(change, func(t *testing.T) {
			inventory := captureBuildInventory(program, ctx)
			if inventory == nil || !inventory.current() {
				t.Fatal("no current dependency inventory")
			}
			var path, content string
			switch change {
			case "content":
				path, content = filepath.Join(dependency, "dep.go"), "package dep\nfunc Value() int { return 2 }\n"
			case "membership":
				path, content = filepath.Join(dependency, "extra.go"), "package dep\n"
			case "module":
				path, content = filepath.Join(dependency, "go.mod"), "module example.com/dep\ngo 1.26\n// changed\n"
			}
			before, err := os.Stat(path)
			if err != nil && change != "membership" {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if before != nil {
				if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
					t.Fatal(err)
				}
			}
			if inventory.current() {
				t.Fatal("changed dependency inventory remained current")
			}
		})
	}
}
