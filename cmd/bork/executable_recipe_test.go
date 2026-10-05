//go:build unix

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutableRecipeAssetsAndKnownBuildInputs(t *testing.T) {
	for _, fixture := range []struct{ name, source string }{
		{"embed", "import \"bork/embed\"\nfn main(){println(embed.ReadString(\"input.txt\"))}\n"},
		{"comptime", "import \"bork/build\"\nfn main(){println(comptime{build.ReadString(\"input.txt\")})}\n"},
		{"pure-comptime", "fn main(){println(comptime{1+2})}\n"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			exe, _, probe := buildReceiptCLI(t)
			t.Setenv("CGO_ENABLED", "0")
			root := t.TempDir()
			for name, text := range map[string]string{"bork.mod": "module example.com/recipe\n", "main.bork": fixture.source, "input.txt": "first"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			run := func() []byte {
				t.Helper()
				output, err := exec.Command(exe, "run", root).CombinedOutput()
				if err != nil {
					t.Fatalf("run: %s: %v", output, err)
				}
				return output
			}
			initial := run()
			if commands, err := os.ReadFile(probe); err != nil || bytes.Contains(commands, []byte(" -a ")) {
				t.Fatalf("checked inputs forced fresh Go objects: %s: %v", commands, err)
			}
			if err := os.Truncate(probe, 0); err != nil {
				t.Fatal(err)
			}
			if output := run(); !bytes.Equal(initial, output) {
				t.Fatalf("warm output changed or warned: %s", output)
			}
			if output, err := os.ReadFile(probe); err != nil || len(output) != 0 {
				t.Fatalf("warm Go commands: %s: %v", output, err)
			}
			if fixture.name != "pure-comptime" {
				if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("second"), 0600); err != nil {
					t.Fatal(err)
				}
				if output := run(); !strings.Contains(string(output), "second") {
					t.Fatalf("input edit not observed: %s", output)
				}
			}
		})
	}
}

func TestExecutableRecipeExternalComptimeWarningAndRebuild(t *testing.T) {
	exe, _, probe := buildReceiptCLI(t)
	t.Setenv("CGO_ENABLED", "0")
	root := t.TempDir()
	foreign := filepath.Join(t.TempDir(), "external.txt")
	source := fmt.Sprintf(`fn readForeign(path:String):String unsafe go{
import "text/template"
t,err:=template.ParseFiles(path);if err!=nil{panic(err)};return t.Tree.Root.String()
}
fn main(){println(comptime{readForeign(%q)})}`, foreign)
	for name, text := range map[string]string{"bork.mod": "module example.com/recipe\nunsafe \"example.com/recipe\"\n", "main.bork": source} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeForeign := func(value string) {
		t.Helper()
		if err := os.WriteFile(foreign, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(flags ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(exe, append([]string{"run", root}, flags...)...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("run: %v: %s", err, stderr.String())
		}
		return stdout.String(), stderr.String()
	}
	writeForeign("first")
	run()
	writeForeign("second")
	if err := os.Truncate(probe, 0); err != nil {
		t.Fatal(err)
	}
	output, warning := run()
	if output != "first\n" || !strings.Contains(warning, "external state read by compile-time code") || !strings.Contains(warning, "--rebuild") {
		t.Fatalf("warm result/warning: %q / %q", output, warning)
	}
	if data, err := os.ReadFile(probe); err != nil || len(data) != 0 {
		t.Fatalf("warm Go commands: %s: %v", data, err)
	}
	if _, warning := run("--fast"); warning != "" {
		t.Fatalf("fast warned: %s", warning)
	}
	t.Setenv("BORKFAST", "1")
	if _, warning := run(); warning != "" {
		t.Fatalf("env fast warned: %s", warning)
	}
	t.Setenv("BORKFAST", "")
	output, warning = run("--rebuild")
	if output != "second\n" || warning != "" {
		t.Fatalf("rebuild: %q / %q", output, warning)
	}
	if data, err := os.ReadFile(probe); err != nil || !strings.Contains(string(data), "-a") {
		t.Fatalf("rebuild did not force Go: %s: %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(root, "bork.mod"), []byte("module example.com/recipe\nunsafe \"example.com/recipe\"\nfast\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run()
	if _, warning := run(); warning != "" {
		t.Fatalf("project fast warned: %s", warning)
	}
}

func TestExecutableRecipeCgoWarmWarning(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("C compiler unavailable")
	}
	exe, _, probe := buildReceiptCLI(t)
	t.Setenv("CGO_ENABLED", "1")
	root := t.TempDir()
	for name, text := range map[string]string{"bork.mod": "module example.com/recipe\nunsafe \"example.com/recipe\"\n", "main.bork": "fn handle():Int unsafe go{import \"runtime/cgo\"\nreturn int64(cgo.Handle(42))}\nfn main(){println(handle())}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func() string {
		t.Helper()
		output, err := exec.Command(exe, "run", root).CombinedOutput()
		if err != nil {
			t.Fatalf("run: %s: %v", output, err)
		}
		return string(output)
	}
	run()
	if err := os.Truncate(probe, 0); err != nil {
		t.Fatal(err)
	}
	output := run()
	if !strings.Contains(output, "system C headers and libraries") || !strings.Contains(output, "42") {
		t.Fatalf("cgo warm: %s", output)
	}
	if data, err := os.ReadFile(probe); err != nil || len(data) != 0 {
		t.Fatalf("warm cgo Go commands: %s: %v", data, err)
	}
}

func TestExecutableRecipeScriptUsesInlineGoDependency(t *testing.T) {
	exe, _, probe := buildReceiptCLI(t)
	t.Setenv("CGO_ENABLED", "0")
	source := filepath.Join(t.TempDir(), "deps.bork")
	text := `#!/usr/bin/env -S bork script --fast
// bork:require github.com/GiGurra/boa v1.0.31
// bork:unsafe
fn answer():Int unsafe go {
import "github.com/GiGurra/boa/pkg/boa"
var _ boa.NoParams
return 42
}
println(answer())
`
	if err := os.WriteFile(source, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		output, err := exec.Command(exe, "script", "--fast", source).CombinedOutput()
		if err != nil || string(output) != "42\n" {
			t.Fatalf("script: %s: %v", output, err)
		}
	}
	run()
	if err := os.Truncate(probe, 0); err != nil {
		t.Fatal(err)
	}
	run()
	if data, err := os.ReadFile(probe); err != nil || len(data) != 0 {
		t.Fatalf("warm inline dependencies invoked Go: %s: %v", data, err)
	}
}

func TestExecutableRecipeRebuildsChangedProjectCHeader(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("C compiler unavailable")
	}
	exe, _, probe := buildReceiptCLI(t)
	t.Setenv("CGO_ENABLED", "1")
	for _, expression := range []string{"value()", "comptime{value()}"} {
		t.Run(expression, func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{filepath.Join(root, "dep", "nested")} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"bork.mod":           "module example.com/recipe\nunsafe \"example.com/recipe\"\n",
				"go.mod":             "// Code generated by bork deps. DO NOT EDIT.\nmodule example.com/recipe\ngo 1.26\n",
				"main.bork":          "fn value():Int unsafe go{import \"example.com/recipe/dep\"\nreturn int64(dep.Value())}\nfn main(){println(" + expression + ")}\n",
				"dep/dep.go":         "package dep\n/*\n#include \"nested/value.h\"\n*/\nimport \"C\"\nfunc Value() int{return int(C.VALUE)}\n",
				"dep/nested/value.h": "#define VALUE 1\n",
			}
			for name, text := range files {
				if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
			}
			run := func(expected string) {
				t.Helper()
				cmd := exec.Command(exe, "run", "--fast", root)
				output, err := cmd.CombinedOutput()
				if err != nil || string(output) != expected+"\n" {
					t.Fatalf("program: %s: %v", output, err)
				}
			}
			run("1")
			if err := os.Truncate(probe, 0); err != nil {
				t.Fatal(err)
			}
			run("1")
			if data, err := os.ReadFile(probe); err != nil || len(data) != 0 {
				t.Fatalf("warm cgo invoked Go: %s: %v", data, err)
			}
			if err := os.WriteFile(filepath.Join(root, "dep", "nested", "value.h"), []byte("#define VALUE 2\n"), 0600); err != nil {
				t.Fatal(err)
			}
			run("2")
		})
	}
}
