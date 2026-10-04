package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRun(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
pred validPort(n: Int) { n > 0 && n < 65536 }
pred nonempty(s: String) { s.byteLength() > 0 }
type Options = { name: String where nonempty, port: Int where validPort = 8080, verbose: Bool = true, character: Rune = 'å' } derive (Decode)
fn main() {
 result = cli.Run[Options]("app", "Example", (options, s) => {
   onClose(s, () => { println("closed") })
   println(options)
 }, [cli.Flag { field: "name", env: "BORK_CLI_TEST_NAME" }, cli.Flag { field: "port", short: "p" }])
 println(result)
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
		name, env          string
		args, want, absent []string
	}{
		{name: "env-defaults", env: "Ada", want: []string{`name: "Ada"`, "port: 8080", "verbose: true", "closed\nOk"}},
		{name: "cli-precedence", env: "Env", args: []string{"--name", "CLI", "-p", "443", "--verbose=false"}, want: []string{`name: "CLI"`, "port: 443", "verbose: false", "closed\nOk"}, absent: []string{`name: "Env"`}},
		{name: "empty-env", args: []string{"--port", "0"}, want: []string{".name", "is missing", ".port", "must satisfy value > 0 (validPort)"}, absent: []string{"closed", "Ok"}},
		{name: "help", env: "Ada", args: []string{"--help"}, want: []string{"Example", "Usage:", "--name", "default 8080", "default å", "Ok"}, absent: []string{"Options {", "closed"}},
		{name: "rune", env: "Ada", args: []string{"--character", "界"}, want: []string{"character: 界", "closed\nOk"}},
		{name: "invalid-rune", env: "Ada", args: []string{"--character", "ab"}, want: []string{"expected one Unicode scalar"}, absent: []string{"closed", "Ok"}},
		{name: "bad-flag", args: []string{"--missing"}, want: []string{"Error {", "unknown flag"}, absent: []string{"closed", "Ok"}},
		{name: "unexpected-positional", env: "Ada", args: []string{"unexpected"}, want: []string{"Error {"}, absent: []string{"closed", "Ok"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(exe, tt.args...)
			cmd.Env = append(os.Environ(), "BORK_CLI_TEST_NAME="+tt.env)
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

func TestCLIConfigFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
pred validPort(n: Int) { n > 0 && n < 65536 }
pred nonempty(s: String) { s.byteLength() > 0 }
type Nested = { enabled: Bool } derive (Decode)
type Options = {
 config: Option[String]
 name: String where nonempty
 port: Int where validPort = 8080
 verbose: Bool = true
 tags: List[String] = ["default"]
 nested: Nested = Nested { enabled: true }
} derive (Decode)
fn main() {
 println(cli.Run[Options]("app", "Example", (options, s) => {
   onClose(s, () => { println("closed") })
   println(options)
 }, [cli.Flag { field: "name", env: "BORK_CLI_CONFIG_NAME", positional: true }, cli.Flag { field: "port", env: "BORK_CLI_CONFIG_PORT" }, cli.Flag { field: "config", configFile: true, env: "BORK_CLI_CONFIG_FILE" }], ["base.json", "override.json"]))
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
		name, base, override, selected, envName, envPort, envFile string
		args, want, absent                                        []string
	}{
		{name: "files-defaults", base: `{"name":"base"}`, override: `{}`, want: []string{`name: "base"`, "port: 8080", "verbose: true", `tags: ["default"]`, "closed\nOk"}},
		{name: "file-overlay", base: `{"name":"base","port":80,"verbose":true,"tags":["a","b"]}`, override: `{"name":"overlay","verbose":false,"tags":[],"nested":{"enabled":false}}`, want: []string{`name: "overlay"`, "port: 80", "verbose: false", "tags: []", "enabled: false"}},
		{name: "env-precedence", base: `{"name":"base","port":80}`, override: `{"name":"overlay","port":81}`, envName: "env", envPort: "82", want: []string{`name: "env"`, "port: 82"}},
		{name: "cli-precedence", base: `{"name":"base","port":80}`, override: `{"name":"overlay","port":81}`, envName: "env", envPort: "82", args: []string{"--port", "83", "cli"}, want: []string{`name: "cli"`, "port: 83"}},
		{name: "selected-file", base: `{"name":"base","port":80}`, override: `{"name":"overlay","port":81}`, selected: `{"name":"selected","port":84}`, args: []string{"--config", "selected.json"}, want: []string{`name: "selected"`, "port: 84"}},
		{name: "selected-env", base: `{"name":"base"}`, override: `{}`, selected: `{"name":"selected"}`, envFile: "selected.json", want: []string{`name: "selected"`}},
		{name: "selector-cli-precedence", base: `{"name":"base"}`, override: `{}`, selected: `{"name":"selected"}`, envFile: "missing.json", args: []string{"--config", "selected.json"}, want: []string{`name: "selected"`}},
		{name: "final-validation", base: `{"name":"","port":0}`, override: `{}`, want: []string{".name", "must be nonempty", ".port", "must satisfy value > 0 (validPort)"}, absent: []string{"closed", "Ok"}},
		{name: "overridden-invalid", base: `{"name":"","port":0}`, override: `{}`, args: []string{"--port", "443", "cli"}, want: []string{`name: "cli"`, "port: 443", "closed\nOk"}},
		{name: "null-optional", base: `{"name":"base","config":null}`, override: `{}`, want: []string{"config: Option.None", "closed\nOk"}},
		{name: "wrong-type", base: `{"name":false,"port":"bad","tags":false}`, override: `{}`, want: []string{".name", ".port", ".tags"}, absent: []string{"closed", "Ok"}},
		{name: "unknown-key", base: `{"naem":"typo"}`, override: `{}`, want: []string{"unknown CLI config field", "naem"}, absent: []string{"closed", "Ok"}},
		{name: "bad-json", base: `{`, override: `{}`, want: []string{"Error {", "base.json"}, absent: []string{"closed", "Ok"}},
		{name: "non-object", base: `[]`, override: `{}`, want: []string{"CLI config must be a JSON object"}, absent: []string{"closed", "Ok"}},
		{name: "positional-precedence", base: `{"name":"base"}`, override: `{}`, envName: "env", args: []string{"positional"}, want: []string{`name: "positional"`}},
		{name: "selected-below-env", base: `{"name":"base"}`, override: `{}`, selected: `{"name":"selected","port":84}`, envName: "env", envPort: "85", args: []string{"--config", "selected.json"}, want: []string{`name: "env"`, "port: 85"}},
		{name: "file-no-recursion", base: `{"name":"base","config":"missing.json"}`, override: `{}`, want: []string{`name: "base"`, `value: "missing.json"`, "closed\nOk"}},
		{name: "null-default", base: `{"name":"base","port":null}`, override: `{}`, want: []string{".port", "Error {"}, absent: []string{"closed", "Ok"}},
		{name: "nested-unknown", base: `{"name":"base","nested":{"enabled":false,"enabeld":true}}`, override: `{}`, want: []string{"enabled: false", "closed\nOk"}},
		{name: "missing-selected", base: `{"name":"base"}`, override: `{}`, args: []string{"--config", "missing.json"}, want: []string{"missing.json", "Error {"}, absent: []string{"closed", "Ok"}},
		{name: "help-no-read", base: `{`, override: `{}`, args: []string{"--help"}, want: []string{"Usage:", "--config", "Ok"}, absent: []string{"Error {", "closed"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for name, data := range map[string]string{"base.json": tt.base, "override.json": tt.override, "selected.json": tt.selected} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_CLI_CONFIG_NAME="+tt.envName, "BORK_CLI_CONFIG_PORT="+tt.envPort, "BORK_CLI_CONFIG_FILE="+tt.envFile)
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

func TestCLIConfigSelectors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
pred selected(value: Option[String]) { match (value) { Option.Some { value: _ } => true, Option.None => false } }
type Constrained = { config: Option[String] where selected, name: String } derive (Decode)
type Defaults = { config: String = "chosen.json", name: String } derive (Decode)
type OptionalDefaults = { config: Option[String] = Option.Some { value: "chosen.json" }, name: String } derive (Decode)
type Bad = { config: Int = 0, other: String = "" } derive (Decode)
type RuneSelector = { config: Rune = 'a' } derive (Decode)
type OptionalRuneSelector = { config: Option[Rune] = Option.Some { value: 'a' } } derive (Decode)
type NestedOption = { config: Option[Option[String]] = Option.Some { value: Option.Some { value: "chosen.json" } }, name: String = "" } derive (Decode)
fn main() uses io {
 println(cli.Parse[RuneSelector]("app", "", [], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[OptionalRuneSelector]("app", "", [], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[Constrained]("app", "", ["--config", "chosen.json"], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[Defaults]("app", "", [], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[OptionalDefaults]("app", "", [], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[NestedOption]("app", "", [], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[Bad]("app", "", [], [cli.Flag { field: "config", configFile: true }]))
 println(cli.Parse[Bad]("app", "", [], [cli.Flag { field: "config", configFile: true }, cli.Flag { field: "other", configFile: true }]))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "chosen.json"), []byte(`{"name":"selected"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Count(string(out), "config-file field must be String or Option[String]") != 5 {
		t.Fatalf("selector type validation: %s", out)
	}
	for _, want := range []string{`Constrained { config: Option.Some`, `Defaults { config: "chosen.json", name: "selected" }`, `OptionalDefaults { config: Option.Some`, "config-file field must be String or Option[String]", "config-file field duplicates config"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}
