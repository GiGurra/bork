package driver

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainResult(t *testing.T) {
	for _, script := range []bool{false, true} {
		t.Run(map[bool]string{false: "program", true: "script"}[script], func(t *testing.T) {
			root := t.TempDir()
			source := `import "bork/process"
import "bork/fs"
type Failure = { message: String }
instance failureShow: Show[Failure] { fn show(e: Failure): String { e.message } }
fn attempt(ok: Bool): Ok | Failure { if (ok) { Ok } else { Failure { message: "failed" } } }
fn main(): Ok | Failure | fs.Error | process.ExitCode {
 scope s {
  dir = fs.TempDir(s)?
  println(fs.DirectoryPath(dir))
  match (process.Args()) {
   ["ok"] => attempt(true)?
   ["code"] => return process.ExitCode { code: 23, message: "usage" }
   ["silent"] => return process.ExitCode { code: 255 }
   ["record"] => return Failure { message: "record" }
   _ => attempt(false)?
  }
 }
}
`
			if script {
				source = "#!/usr/bin/env -S bork script\n" + source
			}
			writeFixtureFile(t, root, "main.bork", source)
			exe := filepath.Join(t.TempDir(), "program")
			if err := Build(filepath.Join(root, "main.bork"), exe); err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				arg    string
				code   int
				stderr string
			}{
				{"ok", 0, ""}, {"fail", 1, "error: failed\n"}, {"record", 1, "error: record\n"}, {"code", 23, "error: usage\n"}, {"silent", 255, ""},
			} {
				t.Run(tc.arg, func(t *testing.T) {
					cmd := exec.Command(exe, tc.arg)
					var stdout, stderr bytes.Buffer
					cmd.Stdout, cmd.Stderr = &stdout, &stderr
					err := cmd.Run()
					code := 0
					if err != nil {
						var exit *exec.ExitError
						if !errors.As(err, &exit) {
							t.Fatal(err)
						}
						code = exit.ExitCode()
					}
					if code != tc.code || stderr.String() != tc.stderr {
						t.Fatalf("exit %d, stderr %q; want %d, %q", code, stderr.String(), tc.code, tc.stderr)
					}
					path := strings.TrimSpace(stdout.String())
					if path == "" {
						t.Fatal("missing temporary directory path")
					}
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("temporary directory survived main: %s (%v)", path, err)
					}
				})
			}
		})
	}
}

func TestMainResultChecks(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"Ok", `fn main(): Ok {}`, ""},
		{"union", `fn main(): Ok | String { "failure" }`, ""},
		{"success", `fn main(): Ok | String {}`, ""},
		{"value", `fn main(): Int { 1 }`, "main must"},
		{"wrong order", `fn main(): String | Ok { "failure" }`, "main must"},
		{"parameters", `fn main(n: Int) { println(n) }`, "main must"},
		{"zero code", `import "bork/process"` + "\n" + `fn main(): Ok | process.ExitCode { process.ExitCode { code: 0 } }`, "ValidExitCode"},
		{"large code", `import "bork/process"` + "\n" + `fn main(): Ok | process.ExitCode { process.ExitCode { code: 256 } }`, "ValidExitCode"},
		{"script top level", "#!/usr/bin/env -S bork script\nparseInt(\"x\")?", "ParseError"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixtureFile(t, root, "main.bork", tc.source+"\n")
			_, _, err := Check(filepath.Join(root, "main.bork"))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q; got %v", tc.want, err)
			}
		})
	}
}

func TestProcessExitRenameFix(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", "import \"bork/process\"\nfn main() { process.Exit(7) }\n")
	_, _, err := Check(root)
	var diagnostics *DiagError
	if !errors.As(err, &diagnostics) {
		t.Fatalf("expected diagnostics: %v", err)
	}
	found := false
	for _, d := range diagnostics.Diags.Sorted() {
		if d.Code == "migration.process-exit" && len(d.Fixes) > 0 && d.Fixes[0].Edits[0].Replacement == "Now" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing rename fix: %v", err)
	}
}

func TestMainResultWithoutScopes(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", `fn main(): Ok | String { "plain failure" }`+"\n")
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(exe)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || stdout.Len() != 0 || stderr.String() != "error: plain failure\n" {
		t.Fatalf("stdout %q, stderr %q, error %v", stdout.String(), stderr.String(), err)
	}
}

func TestProcessExitNowSkipsCleanup(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", `import "bork/fs"
import "bork/process"
fn main(): Ok | fs.Error {
 scope s {
  dir = fs.TempDir(s)?
  println(fs.DirectoryPath(dir))
  process.ExitNow(7)
 }
}
`)
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit: %v", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		t.Fatal("missing temporary directory path")
	}
	defer func() {
		if err := os.RemoveAll(path); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("ExitNow unexpectedly closed scope: %v", err)
	}
}

func TestMainResultTestMode(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "main.bork", `type Failure = sealed { Broken }
instance failureShow: Show[Failure] { fn show(e: Failure): String { "computed" } }
fn main(): Ok | Failure { comptime { Failure.Broken } }
test "entrypoint is not run" { assertEqual(1, 1) }
`)
	exe := filepath.Join(t.TempDir(), "program")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(exe).CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || string(output) != "error: computed\n" {
		t.Fatalf("normal program: %q (%v)", output, err)
	}
	var report bytes.Buffer
	if code, err := Test(root, &report, TestOptions{}); err != nil || code != 0 {
		t.Fatalf("test mode: code %d, %v\n%s", code, err, report.String())
	}
}
