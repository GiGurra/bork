package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIBoolDefaultFalse(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
import "bork/process"
use codec.Defaults
pred IsTrue(value: Bool) { value }
pred Either(options: Together) { options.left || options.right }
type Options = {
 config: Option[String]
 verbose: Bool
 enabled: Bool = true
 disabled: Bool = false
 optional: Option[Bool]
} derive (codec.Decode)
type Checked = { required: Bool where IsTrue } derive (codec.Decode)
type Together = { left: Bool, right: Bool } where Either derive (codec.Decode)
fn main() {
 arguments = process.Args()
 if (arguments.head() == Option.Some("checked")) {
  println(cli.Parse[Checked]("app", "", arguments.drop(1)))
 } else if (arguments.head() == Option.Some("together")) {
  println(cli.Parse[Together]("app", "", arguments.drop(1)))
 } else if (arguments.head() == Option.Some("decoder")) {
  println(codec.decode[Options](codec.Value.Object { fields: [] }))
 } else {
  println(cli.Run[Options]("app", "Bool switches", (options, s) => { println(options) },
   [cli.Flag { field: "verbose", env: "BORK_CLI_BOOL_VERBOSE" }, cli.Flag { field: "config", configFile: true }]))
 }
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
		name, env, config  string
		args, want, absent []string
	}{
		{name: "absent", want: []string{"verbose: false", "enabled: true", "disabled: false", "optional: Option.None", "Ok"}},
		{name: "switch", args: []string{"--verbose"}, want: []string{"verbose: true", "Ok"}},
		{name: "explicit-false", args: []string{"--verbose=false", "--enabled=false", "--optional=false"}, want: []string{"verbose: false", "enabled: false", "optional: Option.Some(false)", "Ok"}},
		{name: "config", config: `{"verbose":true}`, args: []string{"--config", "config.json"}, want: []string{"verbose: true", "Ok"}},
		{name: "env", env: "true", want: []string{"verbose: true", "Ok"}},
		{name: "env-over-config", env: "false", config: `{"verbose":true}`, args: []string{"--config", "config.json"}, want: []string{"verbose: false", "Ok"}},
		{name: "cli-over-env-config", env: "false", config: `{"verbose":false}`, args: []string{"--config", "config.json", "--verbose"}, want: []string{"verbose: true", "Ok"}},
		{name: "config-null", config: `{"verbose":null}`, args: []string{"--config", "config.json"}, want: []string{".verbose", "expected a boolean"}, absent: []string{"Ok"}},
		{name: "bad-env", env: "invalid", want: []string{"Error {"}, absent: []string{"Options {", "Ok"}},
		{name: "help", args: []string{"--help"}, want: []string{"--verbose", "default false", "default true", "Ok"}, absent: []string{"required", "Options {"}},
		{name: "checked-false", args: []string{"checked"}, want: []string{".required", "IsTrue"}, absent: []string{"is missing", "Checked {"}},
		{name: "checked-true", args: []string{"checked", "--required"}, want: []string{"required: true", "Checked {"}},
		{name: "record-fact", args: []string{"together"}, want: []string{"must be Either"}, absent: []string{"is missing", "Together {"}},
		{name: "record-valid", args: []string{"together", "--left"}, want: []string{"left: true", "right: false", "Together {"}},
		{name: "decoder-unchanged", args: []string{"decoder"}, want: []string{".verbose", "is missing"}, absent: []string{"Options {"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_CLI_BOOL_VERBOSE="+tt.env)
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
