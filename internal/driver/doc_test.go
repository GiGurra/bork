package driver

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocPublicAPI(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "docs", "api")
	for _, html := range []bool{false, true} {
		out, err := Doc(root, DocOptions{HTML: html})
		if err != nil {
			t.Fatal(err)
		}
		ext := ".md"
		if html {
			ext = ".html"
		}
		compare(t, filepath.Join("..", "..", "testdata", "docs", "api"+ext), string(out))
		for _, hidden := range []string{"PRIVATE_FUNCTION_BODY", "secret: String", "Item.secret", "func _", "<script>literal"} {
			if bytes.Contains(out, []byte(hidden)) {
				t.Fatalf("private or unsafe content %q escaped into docs", hidden)
			}
		}
	}
	out, err := Doc(filepath.Join(root, "api.bork"), DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	whole, err := Doc(root, DocOptions{})
	if err != nil || !bytes.Equal(out, whole) {
		t.Fatalf("file target did not document its package: %v", err)
	}
}

func TestDocModuleBoundaries(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/all\n")
	writeFixtureFile(t, root, "z/lib.bork", "fn Z(): Int { 1 }\n")
	writeFixtureFile(t, root, "a/lib.bork", "fn A(): Int { 2 }\n")
	writeFixtureFile(t, root, "nested/bork.mod", "module example.com/nested\n")
	writeFixtureFile(t, root, "nested/lib.bork", "fn Nested(): Int { 3 }\n")
	writeFixtureFile(t, root, ".hidden/lib.bork", "broken\n")
	out, err := Doc(root, DocOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if strings.Contains(text, "Nested") || strings.Contains(text, "hidden") || strings.Index(text, "## example.com/all/a") > strings.Index(text, "## example.com/all/z") {
		t.Fatalf("module membership/order: %s", text)
	}
	writeFixtureFile(t, root, "z/lib.bork", "fn Z(): Int { \"broken\" }\n")
	if out, err := Doc(root, DocOptions{All: true}); err == nil || len(out) != 0 {
		t.Fatalf("partial failed document: %q, %v", out, err)
	}
}

func TestDocPinnedLibrariesOffline(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/doclib", "v1.0.0", "module example.com/doclib\n", map[string]string{"api/lib.bork": "// Value is documented from a selected library.\nfn Value(): Int { 13 }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/docapp\n")
	if err := Deps(root, "get", []string{"example.com/doclib@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	t.Setenv("GOPROXY", "off")
	out, err := Doc("example.com/doclib/api", DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("v1.0.0")) || !bytes.Contains(out, []byte("Value is documented")) || bytes.Contains(out, []byte(proxy.cache)) {
		t.Fatalf("library metadata: %s", out)
	}
	all, err := Doc("example.com/doclib", DocOptions{All: true})
	if err != nil || !bytes.Equal(out, all) {
		t.Fatalf("rootless module API: %v", err)
	}
	t.Setenv("GOMODCACHE", t.TempDir())
	if _, err := Doc("example.com/doclib/api", DocOptions{}); err == nil || !strings.Contains(err.Error(), "run bork deps download") {
		t.Fatalf("cached-only docs: %v", err)
	}
}

func TestDocStandardAndEmptyPackages(t *testing.T) {
	out, err := Doc("bork/http", DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("type Status = Int where ValidStatus")) || !bytes.Contains(out, []byte("uses net + clock")) {
		t.Fatal("standard API lost facts/effects")
	}
	if _, err := Doc("bork/http", DocOptions{All: true}); err == nil {
		t.Fatal("standard --all accepted")
	}
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", "fn main() {}\n")
	out, err = Doc(root, DocOptions{})
	if err != nil || !bytes.Contains(out, []byte("No exported declarations")) {
		t.Fatalf("empty API: %q, %v", out, err)
	}
	writeFixtureFile(t, root, "main.bork", "#!/usr/bin/env -S bork script\nprintln(1)\n")
	if _, err := Doc(root, DocOptions{}); err == nil || !strings.Contains(err.Error(), "not executable scripts") {
		t.Fatalf("script docs: %v", err)
	}
}

func TestDocUnsafeAPIAndTestBodies(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/unsafeapi\nunsafe \"example.com/unsafeapi\"\n")
	writeFixtureFile(t, root, "lib.bork", "// Native is a trusted boundary.\nfn Native(value: Int): Int unsafe go { return value }\n// Limit is a documented lazy value.\nlazy Limit: Int = 9\n")
	writeFixtureFile(t, root, "lib_test.bork", "test \"PRIVATE_TEST_BODY\" { assert(true) }\n")
	out, err := Doc(root, DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range []string{"This package contains unsafe Go", "fn Native(value: Int): Int unsafe go", "lazy Limit: Int", "Limit is a documented lazy value."} {
		if !bytes.Contains(out, []byte(phrase)) {
			t.Fatalf("missing API %q: %s", phrase, out)
		}
	}
	for _, phrase := range []string{"return value", "PRIVATE_TEST_BODY"} {
		if bytes.Contains(out, []byte(phrase)) {
			t.Fatalf("body leaked %q: %s", phrase, out)
		}
	}
}

func TestDocPositionalVariants(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", "type Reply[T] = sealed { Found(T, String), Named { value: T }, Gone }\n")
	out, err := Doc(root, DocOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Found(T, String)", "Named {", "Gone"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
}

func TestDocStandardPackagesWithGoDependencies(t *testing.T) {
	for _, path := range []string{"bork/cli", "bork/crypto", "bork/sql", "bork/uuid", "bork/yaml"} {
		t.Run(path, func(t *testing.T) {
			out, err := Doc(path, DocOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(out, []byte("## "+path)) {
				t.Fatalf("missing package heading: %s", out)
			}
		})
	}
}

func TestDocExternalGoNamesCurrent(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module example.com/docnames\ngo 1.23\n")
	writeFixtureFile(t, root, "names.go", "package original\n")
	module := &goModuleInputs{mod: []byte(fmt.Sprintf("module borkdoc\ngo 1.23\nrequire example.com/docnames v0.0.0\nreplace example.com/docnames => %q\n", filepath.ToSlash(root)))}
	context := captureGoContextWithOptions(goContextOptions{settings: []string{"GO111MODULE=on", "GOPACKAGESDRIVER=off"}})
	usage := &goUsage{}
	names := (goPackages{module: module, context: context, usage: usage}).Names([]string{"fmt", "example.com/docnames"})
	if names["fmt"] != "fmt" || names["example.com/docnames"] != "original" || len(usage.names) != 1 || usage.names[0].standard {
		t.Fatalf("external name capture: %v, %+v", names, usage.names)
	}
	// A mixed standard/external group has no receipt. Validation must reload
	// its standard names too, rather than accepting a cached package name.
	key := standardGoNameKey{context.namespace, "fmt"}
	previous, cached := standardGoNames.Load(key)
	standardGoNames.Store(key, "staleName")
	defer func() {
		if cached {
			standardGoNames.Store(key, previous)
		} else {
			standardGoNames.Delete(key)
		}
	}()
	analysis := &EditorAnalysis{program: &compiledProgram{module: module}, usage: usage}
	if !docNamesCurrent(analysis, context) {
		t.Fatal("unchanged external Go names rejected")
	}
	if editorNamesCurrent(analysis, context) {
		t.Fatal("external Go names qualified for editor cache reuse")
	}
	writeFixtureFile(t, root, "names.go", "package changed\n")
	if docNamesCurrent(analysis, context) {
		t.Fatal("changed external Go names accepted")
	}
}

func TestDocBuiltin(t *testing.T) {
	for _, html := range []bool{false, true} {
		out, err := Doc("builtin", DocOptions{HTML: html})
		if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		for _, want := range []string{"builtin", "println", "eprintln", "toInt8", "String", "List[T]", "Map[K, V]", "Option", "Seq", "Bytes", "Channel", "Task", "Atom", "Scope", "Mock", "prepend", "onClose", "scopeOf", "waitFor", "uses io", "where", "= []", "prelude/"} {
			if !strings.Contains(text, want) {
				t.Errorf("missing builtin API %q (html=%v)", want, html)
			}
		}
		for _, hidden := range []string{"compilerSelect", "compilerCallerLocation", "SelectHandle", "TaskHandle", "ChannelHandle", "_bork", "unsafe Go", "## bork/"} {
			if strings.Contains(text, hidden) {
				t.Errorf("internal API %q leaked (html=%v)", hidden, html)
			}
		}
		again, err := Doc("builtin", DocOptions{HTML: html})
		if err != nil || !bytes.Equal(out, again) {
			t.Fatalf("builtin output is not deterministic: %v", err)
		}
	}
	if _, err := Doc("builtin", DocOptions{All: true}); err == nil {
		t.Fatal("builtin --all accepted")
	}
}

func TestDocBuiltinOutsideProject(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(old) }()
	if _, err := Doc("builtin", DocOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ModFile, []byte("invalid manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Doc("builtin", DocOptions{}); err != nil {
		t.Fatalf("caller manifest affected embedded API: %v", err)
	}
}
