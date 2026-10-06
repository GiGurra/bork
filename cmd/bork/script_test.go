package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScriptCLIAndCache(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("result cache platform")
	}
	exe := cliExecutable(t, true)
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", t.TempDir())
	t.Setenv("BORK_TEST_DISK_CACHE_BACKGROUND", "")
	t.Setenv("BORK_TEST_CACHE_PRODUCTION", "")
	t.Setenv("BORK_CACHE", "on")
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	probe := filepath.Join(t.TempDir(), "probe")
	t.Setenv("BORK_TEST_DISK_CACHE_PROBE", probe)
	root := t.TempDir()
	path := filepath.Join(root, "hello.bork")
	// This file has no shebang: the command alone must select script mode.
	source := "import \"bork/process\"\nx=42\nprintln(x)\nprintln(process.Args())\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for _, arg := range []string{"first", "second"} {
		out, err := exec.Command(exe, "script", path, "--", arg).CombinedOutput()
		want := "42\n[\"" + arg + "\"]\n"
		if err != nil || string(out) != want {
			t.Fatalf("script: %s: %v", out, err)
		}
	}
	data, err := os.ReadFile(probe)
	if err != nil || !strings.Contains(string(data), "hit\n") {
		t.Fatalf("no warm script result-cache hit: %s: %v", data, err)
	}
	out, err := exec.Command(exe, "check", path).CombinedOutput()
	if err == nil {
		t.Fatalf("ordinary mode reused script result: %s", out)
	}
	if err := os.WriteFile(path, []byte("#!/usr/bin/env -S bork script\nprintln(99)\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command(exe, "run", path).CombinedOutput()
	if err != nil || string(out) != "99\n" {
		t.Fatalf("shebang run: %s: %v", out, err)
	}
	out, err = exec.Command(exe, "script", root).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "single .bork file") {
		t.Fatalf("directory script: %s: %v", out, err)
	}
}

func TestScriptMainCommands(t *testing.T) {
	exe := cliExecutable(t, false)
	root := t.TempDir()
	path := filepath.Join(root, "main.bork")
	source := `#!/usr/bin/env -S bork script
import "bork/process"
lazy Greeting = "hello"
type Message = { text: String }
fn message(name: String): Message { Message { text: s"$Greeting, $name" } }
fn main() {
 println(message(process.Args().head().getOr("world")).text)
}
test "helper" { assert(message("Ada").text == "hello, Ada") }
`
	if err := os.WriteFile(path, []byte(source), 0700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"check", path}, {"fmt", path}, {"fmt", "--check", path}, {"test", path}} {
		out, err := exec.Command(exe, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
	}
	built := filepath.Join(root, "app")
	if out, err := exec.Command(exe, "build", path, "-o", built).CombinedOutput(); err != nil {
		t.Fatalf("build: %s: %v", out, err)
	}
	for _, args := range [][]string{{"script", path, "Ada"}, {"script", path, "--", "Ada"}, {"script", "--fast", path, "Ada"}, {"run", path, "--", "Ada"}} {
		out, err := exec.Command(exe, args...).CombinedOutput()
		if err != nil || string(out) != "hello, Ada\n" {
			t.Fatalf("%v: %s: %v", args, out, err)
		}
	}
	if out, err := exec.Command(built, "Ada").CombinedOutput(); err != nil || string(out) != "hello, Ada\n" {
		t.Fatalf("built: %s: %v", out, err)
	}
	// Explicit script mode also accepts main without a shebang.
	if err := os.WriteFile(path, []byte(strings.TrimPrefix(source, "#!/usr/bin/env -S bork script\n")), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(exe, "script", path, "Ada").CombinedOutput(); err != nil || string(out) != "hello, Ada\n" {
		t.Fatalf("no shebang: %s: %v", out, err)
	}
}

func TestScriptCLIFlagsAndShebang(t *testing.T) {
	exe := cliExecutable(t, false)
	for _, main := range []bool{false, true} {
		t.Run(map[bool]string{false: "top-level", true: "main"}[main], func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "cli.bork")
			declarations := `import "bork/cli"
import "bork/codec"
use codec.Defaults
type Options = { config: Option[String], name: String = "world" } derive (codec.Decode)
`
			body := `println(cli.Run[Options]("greet", "Greeting script", (options, s) => { println(s"Hello ${options.name}") }, flags: [.{ field: "config", configFile: true }]))`
			if main {
				body = "fn main() {\n" + body + "\n}"
			}
			source := "#!/usr/bin/env -S bork script\n" + declarations + body + "\n"
			if err := os.WriteFile(path, []byte(source), 0700); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "config.json")
			if err := os.WriteFile(config, []byte(`{"name":"file"}`), 0600); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"script", path, "--name", "Ada"}, {"script", path, "--", "--name", "Ada"}, {"script", path, "--config", config, "--name", "Ada"}} {
				out, err := exec.Command(exe, args...).CombinedOutput()
				if err != nil || string(out) != "Hello Ada\nOk\n" {
					t.Fatalf("%v: %s: %v", args, out, err)
				}
			}
			for _, flag := range []string{"--help", "--version", "--fast", "--rebuild"} {
				out, err := exec.Command(exe, "script", path, flag).CombinedOutput()
				if err != nil {
					t.Fatalf("script flag %s: %s: %v", flag, out, err)
				}
				if flag == "--help" {
					if !strings.Contains(string(out), "Greeting script") || strings.Contains(string(out), "Hello") {
						t.Fatalf("script help: %s", out)
					}
				} else if !strings.Contains(string(out), "unknown flag") {
					t.Fatalf("compiler consumed script flag %s: %s", flag, out)
				}
			}
			if runtime.GOOS == "windows" {
				return
			}
			// Exercise env -S and the real executable-file path with unseparated flags.
			bin := t.TempDir()
			if err := os.Symlink(exe, filepath.Join(bin, "bork")); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(path, "--config", config)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != "Hello file\nOk\n" {
				t.Fatalf("shebang: %s: %v", out, err)
			}
		})
	}
}
