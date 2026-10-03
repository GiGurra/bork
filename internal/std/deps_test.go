package std

import (
	"strings"
	"testing"
	"testing/fstest"
)

func manifest(version string) fstest.MapFS {
	return fstest.MapFS{
		"one/go-deps.mod": {Data: []byte("module bork/one\ngo 1.24.0\nrequire example.com/lib " + version + "\n")},
		"one/go-deps.sum": {Data: []byte("example.com/lib " + version + " h1:zip\nexample.com/lib " + version + "/go.mod h1:mod\n")},
	}
}

func TestGoModuleFiles(t *testing.T) {
	sources := manifest("v1.0.0")
	sources["two/go-deps.mod"] = &fstest.MapFile{Data: []byte("module bork/two\ngo 1.23.0\nrequire example.com/lib v1.1.0\n")}
	sources["two/go-deps.sum"] = &fstest.MapFile{Data: []byte("example.com/lib v1.1.0 h1:zip2\nexample.com/lib v1.1.0/go.mod h1:mod2\n")}
	mod, sum, err := goModuleFiles(sources, []string{"user/package", "bork/one", "bork/two", "bork/one"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "go 1.24.0") || !strings.Contains(string(mod), "example.com/lib v1.1.0") || strings.Contains(string(mod), "require example.com/lib v1.0.0") {
		t.Fatalf("unexpected go.mod:\n%s", mod)
	}
	if strings.Count(string(sum), "v1.0.0 h1:zip") != 1 || !strings.Contains(string(sum), "v1.1.0 h1:zip2") {
		t.Fatalf("unexpected go.sum:\n%s", sum)
	}
	reversedMod, reversedSum, err := goModuleFiles(sources, []string{"bork/two", "bork/one"})
	if err != nil || string(reversedMod) != string(mod) || string(reversedSum) != string(sum) {
		t.Fatal("module output depends on import order", err)
	}
	mod, sum, err = goModuleFiles(sources, []string{"bork/missing", "user/package"})
	if err != nil || len(sum) != 0 || strings.Contains(string(mod), "require") {
		t.Fatalf("unimported dependencies included: %s %s %v", mod, sum, err)
	}
}

func TestGoModuleDeclarationErrors(t *testing.T) {
	tests := []struct{ name, mod, sum, want string }{
		{"replace", "module bork/one\ngo 1.22\nreplace example.com/lib => ../lib\n", "", "support only"},
		{"unpinned", "module bork/one\ngo 1.22\nrequire example.com/lib latest\n", "", "version"},
		{"missing-checksum", "module bork/one\ngo 1.22\nrequire example.com/lib v1.0.0\n", "", "missing pinned"},
		{"invalid-checksum", "module bork/one\ngo 1.22\n", "broken\n", "invalid go.sum"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sources := fstest.MapFS{"one/go-deps.mod": {Data: []byte(tt.mod)}, "one/go-deps.sum": {Data: []byte(tt.sum)}}
			_, _, err := goModuleFiles(sources, []string{"bork/one"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want %q", err, tt.want)
			}
		})
	}
	sources := manifest("v1.0.0")
	sources["two/go-deps.mod"] = sources["one/go-deps.mod"]
	sources["two/go-deps.sum"] = &fstest.MapFile{Data: []byte("example.com/lib v1.0.0 h1:other\n")}
	if _, _, err := goModuleFiles(sources, []string{"bork/one", "bork/two"}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("expected checksum conflict, got %v", err)
	}
	delete(sources, "one/go-deps.sum")
	if _, _, err := goModuleFiles(sources, []string{"bork/one"}); err == nil || !strings.Contains(err.Error(), "read pinned") {
		t.Fatalf("expected missing go.sum, got %v", err)
	}
}

func TestUserAndStandardGoDependencies(t *testing.T) {
	sources := manifest("v1.0.0")
	user := GoDependencyManifest{Name: "user", Mod: []byte("module example.com/app\ngo 1.26\nrequire example.com/lib v1.2.0\n"), Sum: []byte("example.com/lib v1.2.0 h1:user\nexample.com/lib v1.2.0/go.mod h1:usermod\n")}
	mod, sum, err := goModuleFiles(sources, []string{"bork/one"}, user)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mod), "example.com/lib v1.2.0") || !strings.Contains(string(mod), "go 1.26") || !strings.Contains(string(sum), "v1.0.0 h1:zip") || !strings.Contains(string(sum), "v1.2.0 h1:user") {
		t.Fatalf("merge: %s %s", mod, sum)
	}
	user.Mod = []byte("module example.com/app\nrequire example.com/lib v0.9.0\n")
	user.Sum = []byte("example.com/lib v0.9.0 h1:lower\nexample.com/lib v0.9.0/go.mod h1:lowermod\n")
	mod, _, err = goModuleFiles(sources, []string{"bork/one"}, user)
	if err != nil || !strings.Contains(string(mod), "example.com/lib v1.0.0") {
		t.Fatalf("lower user version: %s %v", mod, err)
	}
	user.Sum = []byte("example.com/lib v1.0.0 h1:conflict\n")
	if _, _, err := goModuleFiles(sources, []string{"bork/one"}, user); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("checksum conflict: %v", err)
	}
}
