package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIYamlConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
use codec.Defaults
pred validPort(port: Int) { port > 0 && port < 65536 }
type Nested = { enabled: Bool } derive (codec.Decode)
type Options = {
 config: Option[String]
 name: String
 port: Int where validPort = 8080
 tags: List[String] = ["default"]
 nested: Nested = Nested { enabled: true }
 secret: String = "hidden"
} derive (codec.Decode)
fn main() {
 println(cli.Run[Options]("app", "YAML config", (options, s) => { println(options) },
 [cli.Flag { field: "config", configFile: true, env: "BORK_CLI_YAML_FILE" },
 cli.Flag { field: "name", positional: true, env: "BORK_CLI_YAML_NAME" },
 cli.Flag { field: "port", env: "BORK_CLI_YAML_PORT" },
 cli.Flag { field: "secret", config: false }], ["base.yaml", "override.json"]))
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
		name, base, overlay, selected, selectedFile, envFile, envName, envPort string
		args, want, absent                                                     []string
	}{
		{name: "mixed-overlay", base: "name: base\nport: 80\ntags: [a, b]\nnested:\n  enabled: false\n", overlay: `{"name":"overlay","tags":["replacement"]}`, want: []string{`name: "overlay"`, "port: 80", `tags: ["replacement"]`, "enabled: false", "Ok"}},
		{name: "yml-selector", base: "name: base\nport: 80\n", overlay: `{}`, selectedFile: "selected.yml", selected: "name: selected\nport: 81\n", args: []string{"--config", "selected.yml"}, want: []string{`name: "selected"`, "port: 81", "Ok"}},
		{name: "yaml-selector-env", base: "name: base\n", overlay: `{}`, selectedFile: "selected.yaml", selected: "name: selected\n", envFile: "selected.yaml", want: []string{`name: "selected"`, "Ok"}},
		{name: "uppercase-extension", base: "name: base\n", overlay: `{}`, selectedFile: "selected.YAML", selected: "name: selected\n", args: []string{"--config", "selected.YAML"}, want: []string{`name: "selected"`, "Ok"}},
		{name: "env-precedence", base: "name: base\nport: 80\n", overlay: `{}`, selectedFile: "selected.yml", selected: "name: selected\nport: 81\n", args: []string{"--config", "selected.yml"}, envName: "env", envPort: "82", want: []string{`name: "env"`, "port: 82", "Ok"}},
		{name: "cli-precedence", base: "name: base\nport: 80\n", overlay: `{}`, envName: "env", envPort: "82", args: []string{"--port", "83", "positional"}, want: []string{`name: "positional"`, "port: 83", "Ok"}},
		{name: "overridden-invalid", base: "name: base\nport: 0\n", overlay: `{}`, args: []string{"--port", "443"}, want: []string{"port: 443", "Ok"}},
		{name: "unknown-field", base: "naem: typo\n", overlay: `{}`, want: []string{"unknown CLI config field", "naem", "base.yaml"}, absent: []string{"Ok"}},
		{name: "disabled-field", base: "name: base\nsecret: blocked\n", overlay: `{}`, want: []string{"CLI config field", "secret", "disabled"}, absent: []string{"Ok"}},
		{name: "invalid-fact", base: "name: base\nport: 0\n", overlay: `{}`, want: []string{".port", "must satisfy value > 0"}, absent: []string{"Ok"}},
		{name: "wrong-type", base: "name: base\nport: bad\n", overlay: `{}`, want: []string{".port", "expected a whole number"}, absent: []string{"Ok"}},
		{name: "invalid-yaml", base: "name: [\n", overlay: `{}`, want: []string{"base.yaml", "YAML line", "column"}, absent: []string{"Ok"}},
		{name: "non-object", base: "- item\n", overlay: `{}`, want: []string{"CLI config must be a YAML object"}, absent: []string{"Ok"}},
		{name: "multiple-documents", base: "name: base\n---\nname: second\n", overlay: `{}`, want: []string{"base.yaml", "Error {"}, absent: []string{"Ok"}},
		{name: "unknown-extension-json", base: "name: base\n", overlay: `{}`, selectedFile: "selected.conf", selected: `{"name":"json"}`, args: []string{"--config", "selected.conf"}, want: []string{`name: "json"`, "Ok"}},
		{name: "unknown-extension-rejects-yaml", base: "name: base\n", overlay: `{}`, selectedFile: "selected.conf", selected: "name: yaml\n", args: []string{"--config", "selected.conf"}, want: []string{"selected.conf", "Error {"}, absent: []string{"Ok"}},
		{name: "help-no-read", base: "name: [\n", overlay: `{`, args: []string{"--config", "missing.yaml", "--help"}, want: []string{"Usage:", "--config", "Ok"}, absent: []string{"Error {", "Options {"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			files := map[string]string{"base.yaml": tt.base, "override.json": tt.overlay}
			if tt.selectedFile != "" {
				files[tt.selectedFile] = tt.selected
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_CLI_YAML_FILE="+tt.envFile, "BORK_CLI_YAML_NAME="+tt.envName, "BORK_CLI_YAML_PORT="+tt.envPort)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(string(out), absent) {
					t.Errorf("unexpected %q in:\n%s", absent, out)
				}
			}
		})
	}
}
