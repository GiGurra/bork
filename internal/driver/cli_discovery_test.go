package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIConfigDiscovery(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/process"
fn main() {
 println(cli.FindConfig("app", process.Args()))
 println(cli.FindConfig("../app", process.Args()))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		files    []string
		dirs     []string
		want     string
		defaults bool
	}{
		{name: "missing", want: "None"},
		{name: "json-first", files: []string{"cwd/app.yml", "cwd/app.yaml", "cwd/app.json"}, want: "cwd/app.json"},
		{name: "yaml-first", files: []string{"cwd/app.yml", "cwd/app.yaml"}, want: "cwd/app.yaml"},
		{name: "yml", files: []string{"cwd/app.yml"}, want: "cwd/app.yml"},
		{name: "root-priority", files: []string{"cwd/app.yml", "home/.config/app/config.json"}, want: "cwd/app.yml"},
		{name: "user-config", files: []string{"home/.config/app/config.yaml", "system/config.json"}, want: "home/.config/app/config.yaml"},
		{name: "system-config", files: []string{"system/config.yml"}, want: "system/config.yml"},
		{name: "skip-directory", dirs: []string{"cwd/app.json"}, files: []string{"cwd/app.yaml"}, want: "cwd/app.yaml"},
		{name: "default-cwd", defaults: true, files: []string{"cwd/app.json", "home/.config/app/config.json"}, want: "app.json"},
		{name: "default-home", defaults: true, files: []string{"home/.config/app/config.yaml"}, want: "home/.config/app/config.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"cwd", "home", "system"} {
				if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range tt.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{filepath.Join(dir, "cwd"), filepath.Join(dir, "home/.config/app"), filepath.Join(dir, "system")}
			if tt.defaults {
				args = nil
			}
			cmd := exec.Command(exe, args...)
			cmd.Dir = filepath.Join(dir, "cwd")
			cmd.Env = append(os.Environ(), "HOME="+filepath.Join(dir, "home"))
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			want := tt.want
			if want != "None" && want != "app.json" {
				want = filepath.Join(dir, want)
			}
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if !strings.Contains(lines[0], want) {
				t.Errorf("want %q, got %s", want, out)
			}
			if !strings.Contains(string(out), "config app name must be a nonempty file name") {
				t.Errorf("missing invalid-name error: %s", out)
			}
		})
	}
	// A path component that is a regular file yields ENOTDIR, not no-match.
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, filepath.Join(path, "subdir"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "not a directory") {
		t.Errorf("missing stat error: %s", out)
	}
}
