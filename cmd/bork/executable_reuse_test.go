//go:build unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRunAndScriptReuseExecutable(t *testing.T) {
	exe := cliExecutable(t, false)
	cache := t.TempDir()
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORK_CACHE", "on")
	t.Setenv("BORKTOOLCHAIN", "local")
	root := t.TempDir()
	program := filepath.Join(root, "main.bork")
	script := filepath.Join(root, "script.bork")
	for path, source := range map[string]string{
		program: "fn main() uses io { println(42) }\n",
		script:  "#!/usr/bin/env -S bork script\nprintln(42)\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"run", program}, {"script", script}} {
		var before os.FileInfo
		var executable string
		for range 2 {
			out, err := exec.Command(exe, args...).CombinedOutput()
			if err != nil || string(out) != "42\n" {
				t.Fatalf("%v: %s: %v", args, out, err)
			}
			if before == nil {
				if err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if entry.Name() == "program" {
						info, err := entry.Info()
						if err != nil {
							return err
						}
						if executable == "" || info.ModTime().After(before.ModTime()) {
							executable, before = path, info
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if before == nil {
					t.Fatal("run did not publish a cached executable")
				}
			} else {
				after, err := os.Stat(executable)
				if err != nil || !os.SameFile(before, after) {
					t.Fatalf("unchanged program executable was replaced: %v", err)
				}
			}
		}
	}
}

func TestScriptExecPreservesPID(t *testing.T) {
	exe := cliExecutable(t, false)
	t.Setenv("BORKCACHE", t.TempDir())
	t.Setenv("BORKTOOLCHAIN", "local")
	source := filepath.Join(t.TempDir(), "pid.bork")
	text := "#!/usr/bin/env -S bork script\n// bork:unsafe\nfn pid() uses io: Int unsafe go {\nimport \"os\"\nreturn int64(os.Getpid())\n}\nprintln(pid())\n"
	if err := os.WriteFile(source, []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "script", source)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("script: %s: %v", output.String(), err)
	}
	if output.String() != strconv.Itoa(pid)+"\n" {
		t.Fatalf("program pid %q, want CLI pid %d", output.String(), pid)
	}
}

func TestSingleFilesReuseIndependentExecutables(t *testing.T) {
	exe := cliExecutable(t, false)
	cache, root := t.TempDir(), t.TempDir()
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORKTOOLCHAIN", "local")
	a, b := filepath.Join(root, "a.bork"), filepath.Join(root, "b.bork")
	for path, source := range map[string]string{
		a: "fn main() uses io { println(1) }\n",
		b: "fn main() uses io { println(2) }\n",
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(path, want string) {
		t.Helper()
		if out, err := exec.Command(exe, "run", path).CombinedOutput(); err != nil || string(out) != want {
			t.Fatalf("run %s: %s: %v", path, out, err)
		}
	}
	run(a, "1\n")
	paths, err := filepath.Glob(filepath.Join(cache, "stage", "v3", "*", "*", "program"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("cached executable: %v: %v", paths, err)
	}
	before, err := os.Stat(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	run(b, "2\n")
	run(a, "1\n")
	after, err := os.Stat(paths[0])
	if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		t.Fatalf("another file replaced the cached executable: %v", err)
	}
}

func TestConcurrentRunsAndRebuildWhileRunning(t *testing.T) {
	exe := cliExecutable(t, false)
	t.Setenv("BORKCACHE", t.TempDir())
	t.Setenv("BORKTOOLCHAIN", "local")
	source := filepath.Join(t.TempDir(), "main.bork")
	text := `import "bork/time"
fn main() uses io + clock + state {
  scope s {
    println("ready")
    _ = time.Sleep(s, time.Nanoseconds(3000000000))
    println(42)
  }
}
`
	if err := os.WriteFile(source, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var commands []*exec.Cmd
	var outputs []*bufio.Scanner
	for range 2 {
		cmd := exec.CommandContext(ctx, exe, "run", source)
		cmd.Stderr = os.Stderr
		pipe, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill() })
		scanner := bufio.NewScanner(pipe)
		if !scanner.Scan() || scanner.Text() != "ready" {
			t.Fatal("old program did not start")
		}
		commands, outputs = append(commands, cmd), append(outputs, scanner)
	}
	if err := os.WriteFile(source, []byte("fn main() uses io { println(99) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, exe, "run", source).CombinedOutput(); err != nil || string(out) != "99\n" {
		t.Fatalf("rebuild while running: %s: %v", out, err)
	}
	for i, cmd := range commands {
		if !outputs[i].Scan() || outputs[i].Text() != "42" {
			t.Fatal("rebuild changed the running program")
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBuildPreservesUnchangedExecutable(t *testing.T) {
	exe := cliExecutable(t, false)
	t.Setenv("BORKCACHE", t.TempDir())
	t.Setenv("BORKTOOLCHAIN", "local")
	root := t.TempDir()
	source, output := filepath.Join(root, "main.bork"), filepath.Join(root, "hello")
	if err := os.WriteFile(source, []byte("fn main() uses io { println(42) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	build := func() {
		t.Helper()
		if out, err := exec.Command(exe, "build", source, "-o", output).CombinedOutput(); err != nil {
			t.Fatalf("build: %s: %v", out, err)
		}
	}
	build()
	before, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	build()
	after, err := os.Stat(output)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("unchanged build executable was replaced: %v", err)
	}
	if err := os.WriteFile(source, []byte("fn main() uses io { println(99) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	build()
	out, err := exec.Command(output).CombinedOutput()
	if err != nil || string(out) != "99\n" {
		t.Fatalf("changed build: %s: %v", out, err)
	}
}
