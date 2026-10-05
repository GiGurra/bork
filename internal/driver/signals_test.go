package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/GiGurra/bork/internal/format"
)

func TestSignalPlatformCompilation(t *testing.T) {
	dir := t.TempDir()
	source := `import "bork/signal"
import "bork/time"
fn main() {
 scope app {
  println(signal.Configure(app, grace: .Some {value: time.Nanoseconds(1)}))
  println(signal.Ignore(app,[.Interrupt]))
  println(signal.Subscribe(app,[.User1]))
  events = signal.MockSubscription(app)
  println(events.Emit(.Hangup))
  println(events.Next())
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _, generated, err := emit(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeGoModule(dir, files); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), generated, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"windows", "darwin"} {
		t.Run(target, func(t *testing.T) {
			cmd := exec.Command("go", "build", "-mod=readonly", "-o", filepath.Join(dir, target), ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "CGO_ENABLED=0", "GOARCH=amd64", "GOOS="+target)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s build: %v\n%s", target, err, out)
			}
		})
	}
}

// Std pages are outside TestDocSnippets' reader-page list. Keep this new
// package's complete examples compiling and formatted too.
func TestSignalDocumentation(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "std", "signal.md")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	page, err := parseDocPage(string(contents), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range page.blocks {
		if block.lang != "bork" {
			continue
		}
		file := filepath.Join(t.TempDir(), "main.bork")
		if err := os.WriteFile(file, []byte(block.source), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Check(file); err != nil {
			t.Fatalf("line %d: %v", block.line, err)
		}
		formatted, err := format.Source(file, []byte(block.source))
		if err != nil {
			t.Fatal(err)
		}
		if string(formatted) != block.source {
			t.Fatalf("line %d needs formatting:\n%s", block.line, formatted)
		}
	}
}
