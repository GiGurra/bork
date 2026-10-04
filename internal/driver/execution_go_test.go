package driver

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecutionGoContentAndMembership(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "input.go")
	if err := os.WriteFile(file, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := newGoExecutionCapture()
	if err := capture.file(file); err != nil {
		t.Fatal(err)
	}
	if _, err := capture.directory(root); err != nil {
		t.Fatal(err)
	}
	inputs := capture.finish()
	if !inputs.current() {
		t.Fatal("fresh inventory invalid")
	}
	stat, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after!"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("equal-mtime edit qualified")
	}
	if err := os.WriteFile(file, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if !inputs.current() {
		t.Fatal("restored contents invalid")
	}
	if err := os.WriteFile(filepath.Join(root, "new_linux.go"), []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("new selected/search member qualified")
	}
}
func TestExecutionGoInputBudgetsAndOwnership(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "input")
	if err := os.WriteFile(file, []byte("bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := newGoExecutionCapture()
	capture.remaining = 4
	if err := capture.file(file); err == nil {
		t.Fatal("over-budget file retained")
	}
	if err := capture.reservePath(string([]byte{0xff})); err == nil {
		t.Fatal("non-UTF-8 metadata could collide under JSON")
	}
	if len(capture.inputs.Files) != 0 {
		t.Fatal("failed read published")
	}
	capture = newGoExecutionCapture()
	if err := capture.file(file); err != nil {
		t.Fatal(err)
	}
	owned := capture.finish()
	clone := owned.clone()
	clone.Files[0].Digest = sha256.Sum256([]byte("mutated"))
	if owned.identity() == clone.identity() {
		t.Fatal("mutation not reflected in owned identity")
	}
	if !owned.current() || clone.current() {
		t.Fatal("inventory clone not independently owned")
	}
	capture.metadata = goExecutionMetadataLimit
	if err := capture.file(filepath.Join(root, "second")); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("metadata limit not checked before read")
	}
}
func TestExecutionGoDiscoveryPolicy(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	root := filepath.Join(t.TempDir(), "sdk")
	base := []goExecutionPackage{{ImportPath: "runtime", Name: "runtime", Dir: filepath.Join(root, "src", "runtime"), Standard: true, Goroot: true, GoFiles: []string{"runtime.go"}}, {ImportPath: "generated", Name: "main", Dir: stage, Imports: []string{"runtime"}, GoFiles: []string{"main.go"}}}
	encode := func(packages []goExecutionPackage) []byte {
		var data []byte
		for _, pkg := range packages {
			item, err := json.Marshal(pkg)
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, item...)
			data = append(data, '\n')
		}
		return data
	}
	if _, err := decodeGoExecutionPackages(encode(base), stage, root); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func([]goExecutionPackage)
	}{
		{"foreign", func(p []goExecutionPackage) { p[0].Standard = false }},
		{"syso", func(p []goExecutionPackage) { p[0].SysoFiles = []string{"foreign.syso"} }},
		{"additional generated source", func(p []goExecutionPackage) { p[1].GoFiles = append(p[1].GoFiles, "init.go") }},
		{"cgo", func(p []goExecutionPackage) { p[0].CgoFiles = []string{"foreign.go"} }},
		{"missing import", func(p []goExecutionPackage) { p[1].Imports = []string{"missing"} }},
		{"escaped file", func(p []goExecutionPackage) { p[0].GoFiles = []string{"../outside.go"} }},
		{"escaped root", func(p []goExecutionPackage) { p[0].Dir = filepath.Dir(root) }},
		{"duplicate", func(p []goExecutionPackage) { p[0].ImportPath = p[1].ImportPath }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := append([]goExecutionPackage(nil), base...)
			tc.change(p)
			if _, err := decodeGoExecutionPackages(encode(p), stage, root); err == nil {
				t.Fatal("unsupported discovery qualified")
			}
		})
	}
	changed := append([]goExecutionPackage(nil), base...)
	changed[0].GoFiles = []string{"added.go"}
	if sameGoExecutionPackages(base, changed) {
		t.Fatal("changed selected files undiscovered")
	}
	if _, err := decodeGoExecutionPackages([]byte("{}garbage"), stage, root); err == nil {
		t.Fatal("malformed discovery accepted")
	}
}
func TestExecutionGoNativeClosure(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("initial native inventory supports Linux amd64")
	}
	savedHook := goModuleHook
	goModuleHook = nil
	t.Cleanup(func() { goModuleHook = savedHook })
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOENV", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOEXPERIMENT", "")
	t.Setenv("GOAMD64", "v1")
	t.Setenv("GOCACHEPROG", "")
	t.Setenv("GO_EXTLINK_ENABLED", "0")
	t.Setenv("GOFIPS140", "off")
	t.Setenv("GO111MODULE", "on")
	ctx := captureGoContext()
	if ctx.err != nil || !supportedGoVersion(ctx.values["GOVERSION"]) {
		t.Skip("requires supported native Go launcher")
	}
	root := t.TempDir()
	program := []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(true)}\n")
	module := &goModuleInputs{mod: []byte("module example.com/evaluation\n\ngo 1.26\n")}
	if err := os.WriteFile(filepath.Join(root, "main.go"), program, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := module.write(root); err != nil {
		t.Fatal(err)
	}
	stage := goExecutionStage{Root: root, Mode: "predicate", Program: program, Module: module}
	begin := time.Now()
	inventory, decline := captureGoExecution(ctx, stage)
	if decline != "" {
		t.Fatalf("native closure declined: %s", decline)
	}
	var contentBytes int64
	for _, file := range inventory.Inputs.Files {
		contentBytes += file.Size
	}
	t.Logf("capture: %s, %d packages, %d files, %d directories, %d content bytes", time.Since(begin), len(inventory.Packages), len(inventory.Inputs.Files), len(inventory.Inputs.Directories), contentBytes)
	if inventory.Invocation.Mode != "predicate" || inventory.Version != goExecutionInventoryVersion || len(inventory.Packages) < 2 || len(inventory.Inputs.Files) < 4 {
		t.Fatal("incomplete inventory")
	}
	if !inventory.current() {
		t.Fatal("unchanged closure not current")
	}
	savedMode := inventory.Invocation.Mode
	inventory.Invocation.Mode = "unknown"
	if inventory.current() {
		t.Fatal("mutated invocation mode qualified")
	}
	inventory.Invocation.Mode = savedMode
	savedArgs := inventory.Invocation.BuildArgs
	inventory.Invocation.BuildArgs = append(append([]string(nil), savedArgs...), "-race")
	if inventory.current() {
		t.Fatal("mutated build argv qualified")
	}
	inventory.Invocation.BuildArgs = savedArgs
	originalEnv := ctx.env
	ctx.env = append(append([]string(nil), ctx.env...), "GOOS=unknown")
	if !inventory.current() {
		t.Fatal("owned invocation retained mutable context environment")
	}
	ctx.env = originalEnv
	for _, setting := range []string{"GOCACHEPROG=external-helper", "GO_EXTLINK_ENABLED=1"} {
		modified := *ctx
		modified.processEnv = append(append([]string(nil), ctx.processEnv...), setting)
		if _, err := goExecutionEnvelope(&modified, stage); err == nil {
			t.Fatalf("external helper qualified: %s", setting)
		}
	}

	changed := *ctx
	changed.values = make(map[string]string, len(ctx.values))
	for k, v := range ctx.values {
		changed.values[k] = v
	}
	changed.values["GOARCH"] = "unknown"
	if _, err := goExecutionEnvelope(&changed, stage); err == nil {
		t.Fatal("cross target qualified")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filepath.Join(root, "main.go"), 0644); err != nil {
			t.Fatal(err)
		}
		if inventory.current() {
			t.Fatal("file-mode change qualified")
		}
	}
}

func TestExecutionGoNestedAssemblyHeaders(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "src", "runtime")
	headerDir := filepath.Join(pkgDir, "cgo")
	includeDir := filepath.Join(root, "pkg", "include")
	for _, dir := range []string{headerDir, includeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	asm := filepath.Join(pkgDir, "asm_amd64.s")
	header := filepath.Join(headerDir, "abi_amd64.h")
	for path, contents := range map[string]string{asm: "#include \"cgo/abi_amd64.h\"\n#include \"go_asm.h\"\n", header: "#include \"textflag.h\"\n#define OFFSET 8\n", filepath.Join(includeDir, "textflag.h"): "#define FLAG 1\n"} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	capture := newGoExecutionCapture()
	if err := capture.packageDirectory(pkgDir, root); err != nil {
		t.Fatal(err)
	}
	if err := capture.assemblyIncludes(goExecutionPackage{Dir: pkgDir, SFiles: []string{"asm_amd64.s"}}, root); err != nil {
		t.Fatal(err)
	}
	if !capture.files[header] {
		t.Fatal("nested runtime/cgo header omitted")
	}
	inputs := capture.finish()
	if !inputs.current() {
		t.Fatal("nested header inventory invalid")
	}
	stat, err := os.Stat(header)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(header, []byte("#include \"textflag.h\"\n#define OFFSET 9\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(header, stat.ModTime(), stat.ModTime()); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("nested equal-mtime header edit qualified")
	}
}

func TestExecutionGoAssemblyNegativeSearch(t *testing.T) {
	root := t.TempDir()
	pkgDir := filepath.Join(root, "src", "runtime")
	includeDir := filepath.Join(root, "pkg", "include")
	for _, dir := range []string{pkgDir, includeDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	asm := filepath.Join(pkgDir, "asm.s")
	if err := os.WriteFile(asm, []byte("#include \"textflag.h\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includeDir, "textflag.h"), []byte("#define FLAG 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	capture := newGoExecutionCapture()
	if err := capture.assemblyIncludes(goExecutionPackage{Dir: pkgDir, SFiles: []string{"asm.s"}}, root); err != nil {
		t.Fatal(err)
	}
	inputs := capture.finish()
	if !inputs.current() {
		t.Fatal("initial include inventory invalid")
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "textflag.h"), []byte("#define FLAG 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inputs.current() {
		t.Fatal("new higher-priority include qualified")
	}
}

func TestExecutionGoAssemblyCommentAdjacentIncludes(t *testing.T) {
	for _, directive := range []string{"#include/**/\"nested/input.h\"\n", "#/**/include \"nested/input.h\"\n", "#include \"nested/input.h\" // comment\n"} {
		t.Run(directive, func(t *testing.T) {
			root := t.TempDir()
			pkgDir := filepath.Join(root, "src", "runtime")
			nested := filepath.Join(pkgDir, "nested")
			if err := os.MkdirAll(nested, 0700); err != nil {
				t.Fatal(err)
			}
			header := filepath.Join(nested, "input.h")
			if err := os.WriteFile(header, []byte("#define FLAG 1\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pkgDir, "asm.s"), []byte(directive), 0600); err != nil {
				t.Fatal(err)
			}
			capture := newGoExecutionCapture()
			if err := capture.assemblyIncludes(goExecutionPackage{Dir: pkgDir, SFiles: []string{"asm.s"}}, root); err != nil {
				t.Fatal(err)
			}
			if !capture.files[header] {
				t.Fatal("comment-adjacent header omitted")
			}
			inputs := capture.finish()
			if !inputs.current() {
				t.Fatal("initial include inventory invalid")
			}
			if err := os.WriteFile(header, []byte("#define FLAG 2\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if inputs.current() {
				t.Fatal("comment-adjacent header edit qualified")
			}
		})
	}
}
