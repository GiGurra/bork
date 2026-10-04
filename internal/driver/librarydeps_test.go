package driver

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gomodule "golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

type libraryProxy struct {
	t           *testing.T
	root, cache string
}

func newLibraryProxy(t *testing.T) *libraryProxy {
	t.Helper()
	proxy := &libraryProxy{t: t, root: t.TempDir(), cache: t.TempDir()}
	t.Setenv("GOPROXY", "file://"+filepath.ToSlash(proxy.root))
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOMODCACHE", proxy.cache)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOFLAGS", "")
	t.Cleanup(func() {
		_ = filepath.WalkDir(proxy.cache, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return err
		})
	})
	return proxy
}

func (proxy *libraryProxy) publish(path, version, borkMod string, sources map[string]string) {
	proxy.t.Helper()
	mod, err := parseModFile(borkMod)
	if err != nil {
		proxy.t.Fatal(err)
	}
	goMod, err := generatedModule(mod, "")
	if err != nil {
		proxy.t.Fatal(err)
	}
	proxy.publishFiles(path, version, string(goMod), borkMod, sources)
}

func (proxy *libraryProxy) publishFiles(path, version, goMod, borkMod string, sources map[string]string) {
	proxy.t.Helper()
	dir := proxy.t.TempDir()
	writeFixtureFile(proxy.t, dir, "go.mod", goMod)
	if borkMod != "" {
		writeFixtureFile(proxy.t, dir, ModFile, borkMod)
	}
	for name, source := range sources {
		writeFixtureFile(proxy.t, dir, name, source)
	}
	var archive bytes.Buffer
	if err := modzip.CreateFromDir(&archive, gomodule.Version{Path: path, Version: version}, dir); err != nil {
		proxy.t.Fatal(err)
	}
	escapedPath, err := gomodule.EscapePath(path)
	if err != nil {
		proxy.t.Fatal(err)
	}
	base := filepath.Join(proxy.root, filepath.FromSlash(escapedPath), "@v")
	writeFixtureFile(proxy.t, base, version+".mod", goMod)
	writeFixtureFile(proxy.t, base, version+".info", fmt.Sprintf(`{"Version":%q,"Time":"2026-01-01T00:00:00Z"}`, version))
	writeFixtureFile(proxy.t, base, version+".zip", archive.String())
	old, _ := os.ReadFile(filepath.Join(base, "list"))
	writeFixtureFile(proxy.t, base, "list", string(old)+version+"\n")
}

func writeFixtureFile(t *testing.T, root, path, data string) {
	t.Helper()
	name := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryDependencies(t *testing.T) {
	proxy := newLibraryProxy(t)
	for _, release := range []struct{ version, value string }{{"v1.0.0", "7"}, {"v1.1.0", "9"}} {
		proxy.publish("example.com/c", release.version,
			"module example.com/c\nunsafe \"example.com/c/api\"\n",
			map[string]string{"api/api.bork": "fn Value(): Int unsafe go { return " + release.value + " }\n"})
	}
	for _, node := range []struct{ path, dependency, version string }{
		{"b", "c", "v1.0.0"}, {"e", "c", "v1.1.0"},
		{"a", "b", "v1.0.0"}, {"d", "e", "v1.0.0"},
	} {
		proxy.publish("example.com/"+node.path, "v1.0.0",
			"module example.com/"+node.path+"\nrequire example.com/"+node.dependency+" "+node.version+"\n",
			map[string]string{"api/api.bork": "import \"example.com/" + node.dependency + "/api\"\nfn Value(): Int { api.Value() }\n"})
	}
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import a \"example.com/a/api\"\nimport d \"example.com/d/api\"\nfn main() uses io { println(a.Value() + d.Value()) }\n")
	var report bytes.Buffer
	if err := DepsWithOutput(root, "get", []string{"example.com/a@v1.0.0", "example.com/d@v1.0.0"}, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "example.com/c@v1.1.0 declares unsafe Go packages: example.com/c/api") {
		t.Fatalf("transitive unsafe report: %s", report.String())
	}
	mod, err := findModule(root)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, dep := range mod.requirements {
		found[dep.Path] = dep.Version
	}
	if len(found) != 5 || found["example.com/c"] != "v1.1.0" {
		t.Fatalf("deep diamond graph: %v", found)
	}
	before := map[string]string{}
	for _, name := range []string{ModFile, "go.mod", "bork.sum"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = string(data)
	}
	report.Reset()
	if err := DepsWithOutput(root, "download", nil, &report); err != nil {
		t.Fatal(err)
	}
	if report.Len() != 0 {
		t.Fatalf("unchanged unsafe library reported again: %s", report.String())
	}
	t.Setenv("GOPROXY", "off")
	if _, _, err := Check(root); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "consumer")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "18\n" {
		t.Fatalf("selected library execution: %q, %v", out, err)
	}
	for name, text := range before {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != text {
			t.Fatalf("compilation changed %s: %v", name, err)
		}
	}
	if err := Deps(root, "get", []string{"example.com/missing@v1.0.0"}); err == nil {
		t.Fatal("offline missing dependency succeeded")
	}
	for name, text := range before {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != text {
			t.Fatalf("failed resolution changed %s: %v", name, err)
		}
	}
}

func TestLibraryContentAuthentication(t *testing.T) {
	for _, target := range []string{"lib.bork", ModFile, "asset.txt"} {
		t.Run(target, func(t *testing.T) {
			proxy := newLibraryProxy(t)
			proxy.publish("example.com/lib", "v1.0.0", "module example.com/lib\n",
				map[string]string{"lib.bork": "fn Value(): Int { 7 }\n", "asset.txt": "original"})
			root := t.TempDir()
			writeFixtureFile(t, root, ModFile, "module example.com/app\n")
			writeFixtureFile(t, root, "main.bork", "import \"example.com/lib\"\nfn main() uses io { println(lib.Value()) }\n")
			if err := Deps(root, "get", []string{"example.com/lib@v1.0.0"}); err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(proxy.cache, "example.com/lib@v1.0.0", target)
			original, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, 0o644); err != nil {
				t.Fatal(err)
			}
			// There is no prior compiler receipt here: a cold compile must
			// authenticate source too, rather than trusting Go's .ziphash.
			changed := string(original) + "\n// changed\n"
			if target == ModFile {
				changed = string(original) + "unsafe \"example.com/lib\"\n"
			}
			if err := os.WriteFile(name, []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "extracted library content differs") {
				t.Fatalf("cold %s authentication: %v", target, err)
			}
			if err := os.WriteFile(name, original, 0o644); err != nil {
				t.Fatal(err)
			}
			session := NewSession()
			if _, err := session.Check(root); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := session.Check(root); err == nil || !strings.Contains(err.Error(), "extracted library content differs") {
				t.Fatalf("warm %s authentication: %v", target, err)
			}
		})
	}
}

func TestLibraryPackageBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, libraryMod, goMod, source, consumer, want string
	}{
		{name: "ungranted unsafe", libraryMod: "module example.com/lib\n", source: "fn Value(): Int unsafe go { return 7 }\n", consumer: "fn main() uses io { println(lib.Value()) }\n", want: "does not allow it"},
		{name: "foreign grant", libraryMod: "module example.com/lib\nunsafe \"example.com/app\"\n", source: "fn Value(): Int { 7 }\n", want: "outside its own module"},
		{name: "private name", libraryMod: "module example.com/lib\n", source: "fn hidden(): Int { 7 }\n", consumer: "fn main() uses io { println(lib.hidden()) }\n", want: "not exported"},
		{name: "marker mismatch", libraryMod: "module example.com/other\n", goMod: generatedModuleHeader + "module example.com/lib\ngo 1.26\n", source: "fn Value(): Int { 7 }\n", want: "does not match"},
		{name: "manifest drift", libraryMod: "module example.com/lib\n", goMod: generatedModuleHeader + "module example.com/lib\ngo 1.26\nrequire example.com/other v1.0.0\n", source: "fn Value(): Int { 7 }\n", want: "run bork deps download"},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy := newLibraryProxy(t)
			if test.goMod == "" {
				proxy.publish("example.com/lib", "v1.0.0", test.libraryMod, map[string]string{"lib.bork": test.source})
			} else {
				proxy.publishFiles("example.com/lib", "v1.0.0", test.goMod, test.libraryMod, map[string]string{"lib.bork": test.source})
				proxy.publishFiles("example.com/other", "v1.0.0", "module example.com/other\ngo 1.26\n", "", map[string]string{"other.go": "package other\n"})
			}
			root := t.TempDir()
			writeFixtureFile(t, root, ModFile, "module example.com/app\nunsafe \"example.com/lib\"\n")
			writeFixtureFile(t, root, "main.bork", "import \"example.com/lib\"\n"+test.consumer)
			err := Deps(root, "get", []string{"example.com/lib@v1.0.0"})
			if err == nil {
				_, _, err = Check(root)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s: %v, want %q", test.name, err, test.want)
			}
		})
	}
}

func TestLibraryMajorVersionsAndUpgrade(t *testing.T) {
	proxy := newLibraryProxy(t)
	for _, release := range []struct{ version, value string }{{"v1.0.0", "7"}, {"v1.1.0", "9"}} {
		proxy.publish("example.com/lib", release.version, "module example.com/lib\n", map[string]string{"lib.bork": "fn Value(): Int { " + release.value + " }\n"})
	}
	proxy.publish("example.com/lib/v2", "v2.0.0", "module example.com/lib/v2\n", map[string]string{"lib.bork": "fn Value(): Int { 20 }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import one \"example.com/lib\"\nimport two \"example.com/lib/v2\"\nfn main() uses io { println(one.Value() + two.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/lib@v1.0.0", "example.com/lib/v2@v2.0.0"}); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	before, err := session.Emit(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := Deps(root, "get", []string{"example.com/lib@v1.1.0"}); err != nil {
		t.Fatal(err)
	}
	after, err := session.Emit(root)
	if err != nil || bytes.Equal(before, after) {
		t.Fatalf("upgrade did not change compilation: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "consumer")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "29\n" {
		t.Fatalf("major versions: %q, %v", out, err)
	}
}

func TestLibraryCrossModuleCycle(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/a", "v1.0.0", "module example.com/a\nrequire example.com/b v1.0.0\n", map[string]string{"lib.bork": "import \"example.com/b\"\nfn Value(): Int { b.Value() }\n"})
	proxy.publish("example.com/b", "v1.0.0", "module example.com/b\nrequire example.com/a v1.0.0\n", map[string]string{"lib.bork": "import \"example.com/a\"\nfn Value(): Int { a.Value() }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import \"example.com/a\"\nfn main() uses io { println(a.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/a@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "import cycle") {
		t.Fatalf("cross-module cycle: %v", err)
	}
}

func TestMixedLibraryGoHelper(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/lib", "v1.0.0", "module example.com/lib\nunsafe \"example.com/lib\"\n", map[string]string{
		"lib.bork":      "fn Value(): Int unsafe go \"example.com/lib/ffi.Value\"\n",
		"ffi/helper.go": "package ffi\nfunc Value() int { return 17 }\n",
	})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import \"example.com/lib\"\nfn main() uses io { println(lib.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/lib@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "consumer")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "17\n" {
		t.Fatalf("mixed Go library: %q, %v", out, err)
	}
}

func TestLibraryProviderAmbiguity(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/outer", "v1.0.0", "module example.com/outer\n", map[string]string{"sub/api/api.bork": "fn Value(): Int { 1 }\n"})
	proxy.publish("example.com/outer/sub", "v1.0.0", "module example.com/outer/sub\n", map[string]string{"api/api.bork": "fn Value(): Int { 2 }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import \"example.com/outer/sub/api\"\nfn main() uses io { println(api.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/outer@v1.0.0", "example.com/outer/sub@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "provided by both") {
		t.Fatalf("ambiguous package: %v", err)
	}
}

func TestLibraryMissingChecksumAndOfflineCache(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/lib", "v1.0.0", "module example.com/lib\n", map[string]string{"lib.bork": "fn Value(): Int { 7 }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import \"example.com/lib\"\nfn main() uses io { println(lib.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/lib@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(root, "bork.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "bork.sum", "")
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "missing pinned Go checksums") {
		t.Fatalf("missing pins: %v", err)
	}
	writeFixtureFile(t, root, "bork.sum", string(sums))
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOPROXY", "off")
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "GOPROXY=off") {
		t.Fatalf("empty offline cache: %v", err)
	}
}

func TestLibraryPublicationStandardDependencies(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/lib", "v1.0.0", "module example.com/lib\n", map[string]string{"lib.bork": "import \"bork/uuid\"\nfn Value(): String { uuid.New() }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	if err := Deps(root, "get", []string{"example.com/lib@v1.0.0"}); err == nil || !strings.Contains(err.Error(), "author must run bork deps download") {
		t.Fatalf("incomplete publication graph: %v", err)
	}
	mod, err := findModule(root)
	if err != nil || len(mod.requirements) != 0 {
		t.Fatalf("rejected publication changed requirements: %+v, %v", mod, err)
	}
}

func TestLibraryNestedConsumerModule(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/outer/sub", "v1.0.0", "module example.com/outer/sub\n", map[string]string{"api/api.bork": "fn Value(): Int { 7 }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/outer\n")
	writeFixtureFile(t, root, "main.bork", "import \"example.com/outer/sub/api\"\nfn main() uses io { println(api.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/outer/sub@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	if _, _, err := Check(root); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "sub/api/api.bork", "fn Value(): Int { 8 }\n")
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "provided by both") {
		t.Fatalf("local/library ambiguity: %v", err)
	}
}

func TestLibraryRootImportAlias(t *testing.T) {
	proxy := newLibraryProxy(t)
	proxy.publish("example.com/my-library", "v1.0.0", "module example.com/my-library\n", map[string]string{"lib.bork": "fn Value(): Int { 7 }\n"})
	root := t.TempDir()
	writeFixtureFile(t, root, ModFile, "module example.com/app\n")
	writeFixtureFile(t, root, "main.bork", "import lib \"example.com/my-library\"\nfn main() uses io { println(lib.Value()) }\n")
	if err := Deps(root, "get", []string{"example.com/my-library@v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	if err := Build(root, filepath.Join(t.TempDir(), "consumer")); err != nil {
		t.Fatal(err)
	}
}
