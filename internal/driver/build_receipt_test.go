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

func TestBuildInventoryNestedGoEmbed(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	for _, p := range []string{filepath.Join(dep, "assets"), stage} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):          "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):          "package dep\nimport \"embed\"\n//go:embed assets/*\nvar Assets embed.FS\n",
		filepath.Join(dep, "assets", "a.txt"): "a",
		filepath.Join(stage, "go.mod"):        "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):       "package main\nimport \"example.com/dep\"\nfunc main(){_,_=dep.Assets.ReadFile(\"assets/a.txt\")}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=0"}})
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("capture declined")
	}
	if err := os.WriteFile(filepath.Join(dep, "assets", "b.txt"), []byte("b"), 0600); err != nil {
		t.Fatal(err)
	}
	if inv.current() {
		t.Fatal("BUG: inventory still current after adding embedded asset")
	}
}

func TestBuildInventoryNestedCHeader(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	for _, p := range []string{filepath.Join(dep, "nested"), stage} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):            "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):            "package dep\n/*\n#include \"nested/value.h\"\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
		filepath.Join(dep, "nested", "value.h"): "#define VALUE 1\n",
		filepath.Join(stage, "go.mod"):          "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):         "package main\nimport \"example.com/dep\"\nfunc main(){println(dep.Value())}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=1"}})
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("capture declined")
	}
	if err := os.WriteFile(filepath.Join(dep, "nested", "value.h"), []byte("#define VALUE 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inv.current() {
		t.Fatal("BUG: inventory still current after changing package C header")
	}
}

func TestBuildInventoryPkgConfigInputs(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	headers := filepath.Join(root, "headers")
	for _, p := range []string{dep, stage, headers} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(root, "pkg-config")
	script := "#!/bin/sh\ncase \"$*\" in *--cflags*) echo '-I" + headers + "';; *--libs*) echo '';; esac\n"
	if err := os.WriteFile(config, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):      "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):      "package dep\n/*\n#cgo pkg-config: reviewheaders\n#include <value.h>\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
		filepath.Join(headers, "value.h"): "#define VALUE 1\n",
		filepath.Join(stage, "go.mod"):    "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):   "package main\nimport \"example.com/dep\"\nfunc main(){println(dep.Value())}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=1", "PKG_CONFIG=" + config}})
	cmd := ctx.command("build", ".")
	cmd.Dir = stage
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture build failed: %s: %v", out, err)
	}
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("BUG: valid pkg-config build declines broad recipe")
	}
}

func TestBuildInventoryImplicitCompilerFlags(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	for _, p := range []string{filepath.Join(dep, "nested"), stage} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):            "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):            "package dep\n/*\n#ifdef _REENTRANT\n#include \"nested/value.h\"\n#endif\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
		filepath.Join(dep, "nested", "value.h"): "#define VALUE 1\n",
		filepath.Join(stage, "go.mod"):          "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):         "package main\nimport \"example.com/dep\"\nfunc main(){println(dep.Value())}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=1"}})
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("capture declined")
	}
	if err := os.WriteFile(filepath.Join(dep, "nested", "value.h"), []byte("#define VALUE 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inv.current() {
		t.Fatal("BUG: inventory still current after changing package C header")
	}
}

func TestBuildInventoryHeaderShadowing(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	for _, p := range []string{dep, filepath.Join(root, "first"), filepath.Join(root, "second"), stage} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):             "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):             "package dep\n/*\n#cgo CFLAGS: -I${SRCDIR}/../first -I${SRCDIR}/../second\n#include <value.h>\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
		filepath.Join(root, "second", "value.h"): "#define VALUE 1\n",
		filepath.Join(stage, "go.mod"):           "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):          "package main\nimport \"example.com/dep\"\nfunc main(){println(dep.Value())}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=1"}})
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("capture declined")
	}
	if err := os.WriteFile(filepath.Join(root, "first", "value.h"), []byte("#define VALUE 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inv.current() {
		t.Fatal("BUG: inventory still current after changing package C header")
	}
}

func TestBuildInventoryJoinedSystemInclude(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	for _, p := range []string{dep, filepath.Join(root, "first"), filepath.Join(root, "second"), stage} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):             "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):             "package dep\n/*\n#cgo CFLAGS: -isystem${SRCDIR}/../first -isystem${SRCDIR}/../second\n#include <value.h>\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
		filepath.Join(root, "second", "value.h"): "#define VALUE 1\n",
		filepath.Join(stage, "go.mod"):           "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):          "package main\nimport \"example.com/dep\"\nfunc main(){println(dep.Value())}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=1"}})
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("capture declined")
	}
	if err := os.WriteFile(filepath.Join(root, "first", "value.h"), []byte("#define VALUE 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inv.current() {
		t.Fatal("BUG: inventory still current after changing package C header")
	}
}

func TestBuildInventorySymlinkInclude(t *testing.T) {
	root := t.TempDir()
	dep := filepath.Join(root, "dep")
	stage := filepath.Join(root, "stage")
	for _, p := range []string{dep, filepath.Join(root, "first"), filepath.Join(root, "second"), stage} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(dep, "go.mod"):             "module example.com/dep\ngo 1.26\n",
		filepath.Join(dep, "dep.go"):             "package dep\n/*\n#cgo CFLAGS: -I${SRCDIR}/../first -I${SRCDIR}/../second\n#include <value.h>\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
		filepath.Join(root, "second", "value.h"): "#define VALUE 1\n",
		filepath.Join(stage, "go.mod"):           "module example.com/stage\ngo 1.26\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n",
		filepath.Join(stage, "main.go"):          "package main\nimport \"example.com/dep\"\nfunc main(){println(dep.Value())}\n",
	}
	for p, s := range files {
		if err := os.WriteFile(p, []byte(s), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "actual"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "actual"), filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	ctx := captureGoContextWithOptions(goContextOptions{settings: []string{"CGO_ENABLED=1"}})
	inv := captureBuildInventoryMode(stage, ctx, true)
	if inv == nil {
		t.Fatal("capture declined")
	}
	if err := os.WriteFile(filepath.Join(root, "first", "value.h"), []byte("#define VALUE 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inv.current() {
		t.Fatal("BUG: inventory still current after changing package C header")
	}
}
