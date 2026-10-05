package driver

import (
	"bytes"
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

func TestBuildReceiptChecksumCoversBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.json")
	receipt := &buildReceipt{Schema: 1, Declined: true, Output: buildExecutableIdentity{Mode: 0755}}
	writeBuildReceipt(path, receipt)
	if readBuildReceipt(path) == nil {
		t.Fatal("receipt did not roundtrip")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.ReplaceAll(data, []byte(`"Mode":493`), []byte(`"Mode":420`))
	if bytes.Equal(data, changed) {
		t.Fatal("receipt body was not changed")
	}
	if err := os.WriteFile(path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if readBuildReceipt(path) != nil {
		t.Fatal("corrupt receipt passed integrity validation")
	}
}

func TestBuildInventoryCanonicalStage(t *testing.T) {
	if !cacheTrimSupported() {
		t.Skip("persistent build receipts require Linux or macOS")
	}
	root := t.TempDir()
	stage, alias := filepath.Join(root, "tree"), filepath.Join(root, "alias")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(stage, alias); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{"go.mod": "module example.com/stage\ngo 1.26\n", "main.go": "package main\nfunc main() { println(1) }\n"} {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=0"}})
	if ctx.err != nil {
		t.Fatal(ctx.err)
	}
	inventory := captureBuildInventory(alias, ctx)
	if inventory == nil || !inventory.current() || len(inventory.Files) != 0 || len(inventory.Directories) != 0 {
		t.Fatalf("stage alias was recorded as an external dependency: %+v", inventory)
	}
}
