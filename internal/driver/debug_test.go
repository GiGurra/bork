package driver

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func TestDebugRemovalFixes(t *testing.T) {
	for _, expression := range []string{
		"2 * dbg(3 + 4)",
		"2 * dbg(dbg(3 + 4))",
		"2 * (dbg)(3 + 4)",
		"2 * ((dbg))(3 + 4)",
		"2 * dbg(\n 3 + 4,\n)",
		"2 * (3 + 4 |> dbg)",
		"2 * (3 + 4 |> dbg())",
		"2 * (dbg(3 + 4) |> dbg)",
	} {
		t.Run(expression, func(t *testing.T) {
			source := "fn main() { println(\"å\"); println(" + expression + ") }\n"
			path := filepath.Join(t.TempDir(), "main.bork")
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, info, err := Check(path)
			if err != nil {
				t.Fatal(err)
			}
			warnings := check.DebugWarnings(info)
			var edits []diag.TextEdit
			for _, warning := range warnings.Sorted() {
				if warning.Code != "debug.dbg" || warning.Severity != "warning" || len(warning.Fixes) != 1 {
					t.Fatalf("unexpected warning: %+v", warning)
				}
				edits = append(edits, warning.Fixes[0].Edits...)
			}
			if len(edits) == 0 {
				t.Fatal("missing removal fix")
			}
			offset := func(pos diag.Pos) int {
				lines := strings.SplitAfter(source, "\n")
				n := pos.Col - 1
				for _, line := range lines[:pos.Line-1] {
					n += len(line)
				}
				return n
			}
			sort.Slice(edits, func(i, j int) bool { return offset(edits[i].Start) > offset(edits[j].Start) })
			fixed := source
			for _, edit := range edits {
				fixed = fixed[:offset(edit.Start)] + edit.Replacement + fixed[offset(edit.End):]
			}
			if err := os.WriteFile(path, []byte(fixed), 0o644); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(t.TempDir(), "program")
			if err := Build(path, exe); err != nil {
				t.Fatalf("fix failed:\n%s\n%v", fixed, err)
			}
			out, err := exec.Command(exe).CombinedOutput()
			if err != nil || string(out) != "å\n14\n" {
				t.Fatalf("fix changed behavior: %s, %v", out, err)
			}
		})
	}
}

func TestTodoRuntime(t *testing.T) {
	for _, call := range []string{"todo()", "todo(\"implement it\")"} {
		t.Run(call, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "main.bork")
			source := "fn missing(): Int {\n " + call + "\n}\nfn main() { println(missing()) }\n"
			if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, info, err := Check(path)
			if err != nil {
				t.Fatal(err)
			}
			warnings := check.DebugWarnings(info).Sorted()
			if len(warnings) != 1 || warnings[0].Code != "debug.todo" || len(warnings[0].Fixes) != 0 {
				t.Fatalf("unexpected warnings: %+v", warnings)
			}
			data, err := json.Marshal(warnings[0])
			if err != nil || !strings.Contains(string(data), `"severity":"warning"`) {
				t.Fatalf("missing JSON warning severity: %s, %v", data, err)
			}
			exe := filepath.Join(t.TempDir(), "program")
			if err := Build(path, exe); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(exe).CombinedOutput()
			want := "panic: " + path + ":2: todo"
			if call != "todo()" {
				want += ": implement it"
			}
			if err == nil || !strings.HasPrefix(string(out), want+"\n") {
				t.Fatalf("expected located todo panic %q, got %s (%v)", want, out, err)
			}
		})
	}
}
