package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/gen"
)

func TestEmbedSnapshot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	data := bytes.Repeat([]byte{0, 255, 65}, 1<<20)
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("large.bin", data)
	write("main.bork", []byte(`import "bork/embed"
fn main() { println(embed.ReadBytes("large.bin").length()) }
`))
	files, info, err := Check(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "large.bin")); err != nil {
		t.Fatal(err)
	}
	source, err := gen.Package(files, info)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(source, []byte("//go:embed _bork_embed/")) || !bytes.Contains(source, []byte(`<- "large.bin"`)) {
		t.Fatal("generated source lacks embed directive or staging manifest")
	}
	if len(source) >= len(data)/2 {
		t.Fatalf("asset inflated generated source: %d bytes for %d data bytes", len(source), len(data))
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := buildGo(files, source, exe, info.Embeds...); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "3145728\n" {
		t.Fatalf("snapshot output: %s", out)
	}
}

func TestEmbedSymlinks(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "asset"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "regular"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"outside":     outside,
		"inside":      "assets",
		"assets/link": filepath.Join(outside, "asset"),
	} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	for _, call := range []string{
		`embed.ReadBytes("outside/asset")`,
		`embed.ReadBytes("inside/regular")`,
		`embed.ReadBytes("assets/link")`,
		`embed.Directory("assets")`,
	} {
		t.Run(call, func(t *testing.T) {
			source := `import "bork/embed"
fn main() { println(` + call + `) }
`
			if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(dir)
			if err == nil || (!strings.Contains(err.Error(), "symbolic links") && !strings.Contains(err.Error(), "only regular files")) {
				t.Fatalf("expected symlink diagnostic, got %v", err)
			}
		})
	}
}

func TestEmbedEmptyDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(`import "bork/embed"
fn main() { println(embed.Directory("empty").Paths()) }
`), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).Output()
	if err != nil || string(out) != "[]\n" {
		t.Fatalf("empty snapshot: %s, %v", out, err)
	}
}
