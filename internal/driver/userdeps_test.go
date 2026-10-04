package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

func TestUserGoDependencies(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(ModFile, "module example.com/userdeps\nunsafe \"example.com/userdeps/ffi\"\n")
	manifest, err := os.ReadFile("../../testdata/cases/go_user_deps/go-deps.mod")
	if err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile("../../testdata/cases/go_user_deps/go-deps.sum")
	if err != nil {
		t.Fatal(err)
	}
	write("go-deps.mod", string(manifest))
	write("go-deps.sum", string(sums))
	sub := filepath.Join(root, "ffi")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write("main.bork", "import \"example.com/userdeps/ffi\"\nfn main() uses io { println(ffi.Valid(\"00000000-0000-0000-0000-000000000001\")) }\n")
	if err := os.WriteFile(filepath.Join(sub, "ffi.bork"), []byte("fn Valid(text: String): Ok | GoError unsafe go \"github.com/google/uuid.Validate\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOSUMDB", "unsupported.invalid")
	if _, _, err := Check(root); err != nil {
		t.Fatalf("offline checking from module cache: %v", err)
	}
	if err := Build(root, exe); err != nil {
		t.Fatalf("offline build from module cache: %v", err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "Ok\n" {
		t.Fatalf("program: %s %v", out, err)
	}
	t.Setenv("GOMODCACHE", t.TempDir())
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "github.com/google/uuid") {
		t.Fatalf("cold offline cache must fail at binding: %v", err)
	}
}

func TestUserGoManifestErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ModFile), []byte("module example.com/deps\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []*syntax.File{{Package: "example.com/deps", Path: filepath.Join(root, "main.bork")}}
	if user, err := userGoDependencies(files); err != nil || len(user) != 0 {
		t.Fatalf("absent manifest: %v %v", user, err)
	}
	if err := os.WriteFile(filepath.Join(root, "go-deps.mod"), []byte("require github.com/google/uuid v1.6.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := userGoDependencies(files); err == nil || !strings.Contains(err.Error(), "go-deps.sum") {
		t.Fatalf("missing checksum file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go-deps.sum"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGoModule(t.TempDir(), files); err == nil || !strings.Contains(err.Error(), "missing pinned Go checksums") {
		t.Fatalf("missing checksums: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go-deps.mod"), []byte("replace github.com/google/uuid => ../uuid\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeGoModule(t.TempDir(), files); err == nil || !strings.Contains(err.Error(), "support only") {
		t.Fatalf("replace: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte("fn main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "support only") {
		t.Fatalf("check-only program must validate its manifest: %v", err)
	}

}
