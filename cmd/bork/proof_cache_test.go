package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPersistentProofCLIParity(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native cache platform required")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("CGO_ENABLED", "0")
	root := t.TempDir()
	cache, probe := filepath.Join(root, "cache"), filepath.Join(root, "probe")
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
	t.Setenv("BORK_TEST_PROOF_CACHE_PROBE", probe)
	program := filepath.Join(root, "program")
	if err := os.Mkdir(program, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, "bork.mod"), []byte("module example.com/proofs\nunsafe \"example.com/proofs\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(program, "main.bork")
	source := `fn twice(n:Int):Int{n+n}
pred Positive(n:Int){twice(n)>0}
fn main(){n:Int where Positive=1;println(n)}`
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exe := cliExecutable(t, true)
	run := func(clean bool, args ...string) ([]byte, error) {
		t.Helper()
		command := exec.Command(exe, args...)
		command.Env = os.Environ()
		if clean {
			command.Env = append(command.Env, "BORK_CACHE=off")
		}
		return command.CombinedOutput()
	}
	parity := func(status string, wantError bool, args ...string) {
		t.Helper()
		if err := os.WriteFile(probe, nil, 0600); err != nil {
			t.Fatal(err)
		}
		got, err := run(false, args...)
		want, cleanErr := run(true, args...)
		if (err != nil) != wantError || (cleanErr != nil) != wantError || !bytes.Equal(got, want) {
			t.Fatalf("parity: reuse %v %s; clean %v %s", err, got, cleanErr, want)
		}
		data, err := os.ReadFile(probe)
		if err != nil || string(data) != status {
			t.Fatalf("proof cache: %v %q, want %q", err, data, status)
		}
	}
	write(source)
	parity("published\n", false, "emit", program)
	write(source + "\n// unrelated edit\n")
	parity("hit\n", false, "emit", program)
	parity("hit\n", false, "check", "--json", program)
	parity("hit\n", false, "build", program, "-o", filepath.Join(root, "program.exe"))

	// Reachable helper and argument edits must change the key, even when the
	// source mtime is preserved. False results still use today's diagnostic site.
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(source, "n+n", "n-n", 1)
	write(bad)
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	parity("published\n", true, "check", "--json", program)
	write("\n" + bad)
	parity("hit\n", true, "check", "--json", program)
	write(strings.Replace(source, "Positive=1", "Positive=-1", 1))
	parity("published\n", true, "check", program)

	// Corruption replaces the sidecar through ordinary execution.
	if err := filepath.WalkDir(cache, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() == "proof-v1.json" {
			return os.WriteFile(path, []byte("corrupt"), 0600)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	parity("published\n", true, "check", program)
	t.Setenv("BORK_TEST_PROOF_ENV", "changed")
	parity("published\n", true, "check", program)
	parity("hit\n", true, "check", program)
	if out, err := run(false, "clean", "--all"); err != nil {
		t.Fatalf("clean: %v %s", err, out)
	}
	parity("published\n", true, "check", program)

	// Unsafe functions remain fresh; their external inputs never become proofs.
	write(`pred Positive(n:Int) unsafe go{return n > 0}
fn main(){n:Int where Positive=1;println(n)}`)
	parity("", false, "emit", program)
	parity("", false, "emit", program)
	write(source)
	unavailable := filepath.Join(root, "unavailable-cache")
	if err := os.WriteFile(unavailable, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BORKCACHE", unavailable)
	parity("", false, "emit", program)
}

func TestPersistentProofExamples(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native cache platform required")
	}
	t.Setenv("GOPACKAGESDRIVER", "off")
	t.Setenv("CGO_ENABLED", "0")
	root := t.TempDir()
	cache, probe := filepath.Join(root, "cache"), filepath.Join(root, "probe")
	t.Setenv("BORKCACHE", cache)
	t.Setenv("BORK_TEST_DISK_CACHE_DIRECTORY", cache)
	t.Setenv("BORK_TEST_PROOF_CACHE_PROBE", probe)
	exe := cliExecutable(t, true)
	for _, name := range []string{"config", "http_server"} {
		t.Run(name, func(t *testing.T) {
			program := filepath.Join(root, name)
			files := []string{"main.bork"}
			if name == "config" {
				files = append(files, "bork.mod", "settings/settings.bork")
			}
			for _, file := range files {
				data, err := os.ReadFile(filepath.Join("../../examples", name, file))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(program, file)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			fresh, err := exec.Command(exe, "emit", program).CombinedOutput()
			if err != nil {
				t.Fatalf("fresh: %v %s", err, fresh)
			}
			file, err := os.OpenFile(filepath.Join(program, "main.bork"), os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			_, writeErr := file.WriteString("\n// unrelated edit\n")
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				t.Fatalf("edit: %v %v", writeErr, closeErr)
			}
			if err := os.WriteFile(probe, nil, 0600); err != nil {
				t.Fatal(err)
			}
			reused, err := exec.Command(exe, "emit", program).CombinedOutput()
			if err != nil || !bytes.Equal(fresh, reused) {
				t.Fatalf("emission parity: %v", err)
			}
			if data, err := os.ReadFile(probe); err != nil || string(data) != "hit\n" {
				t.Fatalf("missing proof hit: %v %q", err, data)
			}
		})
	}
}
