package driver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func TestScriptChecks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source, want string }{
		{"statements", "x=1\nprintln(x)\nfn double(n:Int):Int{n*2}\nprintln(double(x))", ""},
		{"explicit package lazy", "lazy X=1\nfn read():Int{X}\nprintln(read())", ""},
		{"function capture", "x=1\nfn read():Int{x}\nprintln(read())", "use lazy x = ..."},
		{"function capture before binding", "fn read():Int{x}\nx=1\nprintln(read())", "script local"},
		{"callback capture", "f:(Int)=>Int=n=>n+1\nfn read():Int{f(1)}\nprintln(read())", "script local"},
		{"main conflict", "fn main(){}", "implicit main"},
		{"lazy effect", "lazy X={println(1);1}\nprintln(X)", "pure initializer"},
		{"no unsafe opt-in", "fn read():String unsafe go \"os.Getwd\"\nprintln(read())", "unsafe go"},
		{"unsafe opt-in", "// bork:unsafe\nfn read()uses io:String|GoError unsafe go \"os.Getwd\"\nprintln(read())", ""},
		{"bad version", "// bork:require github.com/google/uuid latest\nprintln(1)", "canonical pinned version"},
		{"late directive", "println(1)\n// bork:unsafe", "before imports and declarations"},
		{"unknown directive", "// bork:unknown\nprintln(1)", "expected // bork:require"},
		{"duplicate dependency", "// bork:require github.com/google/uuid v1.6.0\n// bork:require github.com/google/uuid v1.6.0\nprintln(1)", "more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := fixtureDir(t)
			path := filepath.Join(root, "script.bork")
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(scriptRequestPrefix + path)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

func TestScriptProjectDirectives(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"#!/usr/bin/env -S bork script\n// bork:unsafe\nprintln(1)",
		"// bork:require github.com/google/uuid v1.6.0\nfn main(){}",
	} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ModFile), []byte("module example.com/project\n"), 0600); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "main.bork")
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := Check(path)
		if err == nil || !strings.Contains(err.Error(), "not allowed in bork.mod projects") {
			t.Fatalf("%v", err)
		}
	}
}

func TestScriptBuildArgsAndDescribe(t *testing.T) {
	t.Parallel()
	root := fixtureDir(t)
	path := filepath.Join(root, "script.bork")
	source := "#!/usr/bin/env -S bork script\nimport \"bork/process\"\n\nx=41\nprintln(x+1)\nprintln(process.Args())\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "script")
	if err := Build(path, exe); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe, "hello", "--flag").CombinedOutput()
	if err != nil || string(output) != "42\n[\"hello\", \"--flag\"]\n" {
		t.Fatalf("%s: %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.bork"), []byte("#!/usr/bin/env -S bork script\nprintln(99)"), 0600); err != nil {
		t.Fatal(err)
	}
	description, err := Describe(path+":4:1", "")
	if err != nil || description == nil || description.Type != "Int" {
		t.Fatalf("%+v: %v", description, err)
	}
}

func TestScriptSessionModeAndEdits(t *testing.T) {
	t.Parallel()
	root := fixtureDir(t)
	path := filepath.Join(root, "script.bork")
	if err := os.WriteFile(path, []byte("x=41\nprintln(x+1)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	session := NewSession()
	first, err := session.Emit(scriptRequestPrefix + path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := session.Emit(scriptRequestPrefix + path)
	if err != nil || !bytes.Equal(first, second) || session.Stats().Hits != 1 {
		t.Fatalf("%+v: %v", session.Stats(), err)
	}
	if _, err := session.Emit(path); err == nil {
		t.Fatal("normal request reused script artifact")
	}
	if err := os.WriteFile(path, []byte("x=99\nprintln(x+1)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	third, err := session.Emit(scriptRequestPrefix + path)
	if err != nil || bytes.Equal(first, third) {
		t.Fatalf("changed source reused old artifact: %v", err)
	}
}

func TestScriptHeaderOnly(t *testing.T) {
	file := syntax.ParseScript("script.bork", []byte("// bork:unsafe"), &diag.List{})
	diags := &diag.List{}
	if header := readScriptHeader(file, false, diags); !header.unsafe || diags.Len() != 0 {
		t.Fatalf("%+v: %s", header, diags.Error())
	}
}

func TestScriptImportAndMultiFileErrors(t *testing.T) {
	t.Parallel()
	root := fixtureDir(t)
	if err := os.WriteFile(filepath.Join(root, ModFile), []byte("module example.com/scripts\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "helper"), 0700); err != nil {
		t.Fatal(err)
	}
	sources := map[string]string{
		"main.bork":        "import \"example.com/scripts/helper\"\nfn main(){}",
		"helper/main.bork": "#!/usr/bin/env -S bork script\nprintln(1)",
	}
	for name, source := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := Check(root)
	if err == nil || !strings.Contains(err.Error(), "scripts cannot be imported") {
		t.Fatalf("%v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.bork"), []byte("#!/usr/bin/env -S bork script\nprintln(1)"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err = Check(root)
	if err == nil || !strings.Contains(err.Error(), "single root file") {
		t.Fatalf("%v", err)
	}
}

func TestScriptGoDependencies(t *testing.T) {
	// Isolate resolution storage while reusing Go's normal module cache, as the
	// ordinary dependency integration fixtures do.
	t.Setenv("BORKCACHE", t.TempDir())
	root := t.TempDir()
	path := filepath.Join(root, "script.bork")
	source := "#!/usr/bin/env -S bork script\n// bork:require github.com/google/uuid v1.6.0\n// bork:unsafe\nfn valid(text:String):Ok|GoError unsafe go \"github.com/google/uuid.Validate\"\nprintln(valid(\"00000000-0000-0000-0000-000000000001\"))\n"
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	loaded, module, err := loadCompilationInputs(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(module.mod, []byte("github.com/google/uuid v1.6.0")) || !bytes.Contains(module.sum, []byte("github.com/google/uuid v1.6.0 h1:")) {
		t.Fatalf("%s\n%s", module.mod, module.sum)
	}
	if !loaded.Inputs.current() {
		t.Fatal("resolution did not retry after publication")
	}
	// Captured module files are observed rather than recapturing mutable output.
	deps := loaded.Inputs.dependencies()
	var manifest string
	for _, dep := range deps {
		if strings.HasSuffix(dep.Path, "go-deps.mod") {
			manifest = dep.Path
			break
		}
	}
	if manifest == "" {
		t.Fatal("resolved manifest missing from input inventory")
	}
	exe := filepath.Join(t.TempDir(), "script")
	if err := Build(path, exe); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(output) != "Ok\n" {
		t.Fatalf("%s: %v", output, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("script directory was changed: %v: %v", entries, err)
	}
	if err := os.Remove(filepath.Join(filepath.Dir(manifest), "go-deps.sum")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(path); err != nil {
		t.Fatalf("incomplete dependency cache did not recover: %v", err)
	}
	if err := os.WriteFile(manifest, []byte("broken manifest"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(path); err != nil {
		t.Fatalf("corrupt dependency cache did not recover: %v", err)
	}
	if err := os.WriteFile(manifest, append(module.mod, []byte("// changed\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if loaded.Inputs.current() {
		t.Fatal("resolved manifest edit was missed")
	}
	cache, err := cacheRootDir()
	if err != nil {
		t.Fatal(err)
	}
	report, err := cleanCache(context.Background(), cache, sha256.Sum256([]byte("fixture")), false)
	if err != nil || report.Dependencies != 0 {
		t.Fatalf("namespace clean: %+v: %v", report, err)
	}
	if _, err := os.Stat(manifest); err != nil {
		t.Fatal("namespace clean removed shared dependency graph")
	}
	report, err = cleanCache(context.Background(), cache, [sha256.Size]byte{}, true)
	if err != nil || report.Dependencies != 1 {
		t.Fatalf("all clean: %+v: %v", report, err)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatalf("all clean retained script dependencies: %v", err)
	}
	// A resolver for another graph may be waiting on its own graph lock after
	// creating this shared parent. Cleaning must leave it available afterward.
	if info, err := os.Stat(filepath.Dir(filepath.Dir(manifest))); err != nil || !info.IsDir() {
		t.Fatalf("all clean removed parent needed by pending resolvers: %v", err)
	}
}

func TestScriptConcurrentDependencyResolution(t *testing.T) {
	t.Setenv("BORKCACHE", t.TempDir())
	path := filepath.Join(t.TempDir(), "script.bork")
	source := "#!/usr/bin/env -S bork script\n// bork:require github.com/google/uuid v1.6.0\nprintln(1)\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	type result struct {
		module *goModuleInputs
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for range 2 {
		go func() { <-start; _, module, err := loadCompilationInputs(path, nil); results <- result{module, err} }()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent resolution: %v; %v", first.err, second.err)
	}
	if !bytes.Equal(first.module.mod, second.module.mod) || !bytes.Equal(first.module.sum, second.module.sum) {
		t.Fatal("concurrent resolvers published different graphs")
	}
}
