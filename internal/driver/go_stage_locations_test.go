package driver

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

func TestStableStageMappedSourceLocations(t *testing.T) {
	requireStageLock(t)
	mapped := filepath.Join(t.TempDir(), "mapped.bork")
	if err := os.WriteFile(mapped, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	files := []*syntax.File{{Path: mapped, Package: "main"}}
	module, err := captureGoModule(files, diskSources{})
	if err != nil {
		t.Fatal(err)
	}
	source := []byte(fmt.Sprintf(`package main
import ("fmt"; "runtime")
func generated() { _, file, line, _ := runtime.Caller(0); fmt.Printf("generated=%%s:%%d\n", file, line) }
func mapped() { /*line %s:17:3*/ _, file, line, _ := runtime.Caller(0); fmt.Printf("mapped=%%s:%%d\n", file, line); panic("mapped panic") }
func main() { generated(); mapped() }
`, mapped))
	ctx := captureGoContext()
	// Tests use a private writable staging cache; the caller's cache may be
	// read-only and correctly choose temporary production fallback instead.
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", t.TempDir())
	} else {
		t.Setenv("XDG_CACHE_HOME", t.TempDir())
	}
	exe := filepath.Join(t.TempDir(), "program")
	if err := buildGoWithContext(files, source, exe, module, ctx); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "mapped="+mapped+":17") || !strings.Contains(string(output), "\t"+mapped+":17") {
		t.Fatalf("lost mapped caller/panic path: %s (%v)", output, err)
	}
	first, _, _ := strings.Cut(string(output), "\n")
	generated := strings.TrimSuffix(strings.TrimPrefix(first, "generated="), ":3")
	if !filepath.IsAbs(generated) || filepath.Base(generated) != "main.go" {
		t.Fatalf("lost absolute generated file: %s", generated)
	}
	if _, err := os.Stat(generated); err != nil {
		t.Fatalf("generated source unavailable after build: %v", err)
	}
	if runtime.GOOS != "linux" {
		return
	}
	file, err := elf.Open(exe)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, err := file.DWARF()
	if err != nil {
		t.Fatal(err)
	}
	reader := data.Reader()
	for {
		entry, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if entry == nil {
			break
		}
		if entry.Tag != dwarf.TagCompileUnit {
			continue
		}
		lines, err := data.LineReader(entry)
		if err != nil {
			t.Fatal(err)
		}
		if lines == nil {
			continue
		}
		for {
			var line dwarf.LineEntry
			err := lines.Next(&line)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if line.File != nil && line.File.Name == mapped && line.Line == 17 {
				return
			}
		}
	}
	t.Fatal("mapped Bork path/line absent from DWARF")
}
