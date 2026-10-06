package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIWireNamesAndAliases(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
pred Positive(value: Int) { value > 0 }
type Options = {
 HTTPPort: Int where Positive = 80 codec { name: "listen_port", aliases: ["old-port"] }
 enabled: Bool codec { name: "verbose_flag", aliases: ["debug"] }
 labels: List[String] = [] codec { aliases: ["tags"] }
 userName: String = "guest"
} codec { naming: codec.Naming.Snake } derive (codec.Decode)
type Collision = { HTTPPort: Int = 80 codec { aliases: ["http_port"] } } derive (codec.Decode)
type Empty = {} derive (codec.Decode)
type Child = { value: String = "child" codec { name: "old-port" } } derive (codec.Decode)
fn enrich(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec {
 if (field.field == "HTTPPort") {
  field.copy(long: cli.Mapping.Named { name: "enriched" }, aliases: cli.FieldAliases { long: ["compat"], env: ["BORK_ALIAS_EXACT"] })
 } else { field }
}
fn versionAlias(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec {
 if (field.field == "HTTPPort") { field.copy(aliases: cli.FieldAliases { long: ["version"] }) } else { field }
}
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 value = match (request.partial.Get[Int]("HTTPPort")) {
  n: Int => toString(n)
  _: cli.Missing => "missing"
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
 }
 count = match (request.partial.Get[List[String]]("labels")) { labels: List[String] => toString(labels.length()) + "-", _: cli.Missing => "", error: codec.DecodeError => { return cli.Error { errors: [error] } } }
 cli.Suggestions { choices: [.{ value: count + value + "-port" }] }
}
fn main() {
 args = process.Args()
 mode = args.head().getOr("basic")
 args = args.drop(1)
 flags: List[cli.Flag] = if (mode == "named" || mode == "enriched") {
  [.{ field: "HTTPPort", long: cli.Mapping.Named { name: "exact" }, envName: cli.Mapping.Named { name: "BORK_NAMING_EXACT" } }]
 } else if (mode == "disabled") {
  [.{ field: "HTTPPort", long: cli.Mapping.Disabled, envName: cli.Mapping.Disabled }]
 } else if (mode == "env-only") { [.{ field: "HTTPPort", long: cli.Mapping.Disabled }]
 } else if (mode == "deprecated") { [.{ field: "HTTPPort", deprecated: "use a new option" }]
 } else if (mode == "positional") { [.{ field: "HTTPPort", positional: true }] } else { [] }
 settings: cli.Settings = .{
  version: if (mode == "version-alias") { "1.2.3" } else { "" },
  autoEnv: true,
  autoShort: true,
  flagPrefix: if (mode == "named") { "p" } else { "" },
  envPrefix: if (mode == "unprefixed") { "" } else { "BORK_NAMING" },
  enrichers: if (mode == "enriched") { [enrich] } else if (mode == "version-alias") { [versionAlias] } else { [] }
 }
 if (mode == "collision") { println(cli.Parse[Collision]("app", "Naming", args, configFiles: ["missing.json"], settings: settings)); return }
 if (mode == "env-collision") { println(cli.Parse[Collision]("app", "Naming", args, flags: [.{ field: "HTTPPort", long: cli.Mapping.Disabled }], configFiles: ["missing.json"], settings: settings)); return }
 if (mode == "completion") {
  command = cli.SubcommandWith[Options]("go", "Naming", (value, s) => { println("HANDLER") }, settings: settings, completions: [.{ field: "HTTPPort", suggest: suggest }])
  println(cli.Dispatch("app", "Naming", args, [command])); return
 }
 if (mode == "root") {
  command = cli.RootSubcommand[Options, Empty]("go", "Naming", (root, child, s) => { println(root) })
  println(cli.DispatchRoot[Options]("app", "Naming", args, [command], settings: settings)); return
 }
 if (mode == "root-collision") {
  command = cli.RootSubcommand[Options, Child]("go", "Naming", (root, child, s) => { println("HANDLER") })
  println(cli.DispatchRoot[Options]("app", "Naming", args, [command], settings: settings)); return
 }
 files = if (mode == "yaml") { ["config.yaml"] } else if (mode == "overlay") { ["base.json", "config.json"] } else { ["config.json"] }
 println(cli.Parse[Options]("app", "Naming", args, flags: flags, configFiles: files, settings: settings))
}`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, mode, config string
		args, want, absent []string
		env                map[string]string
	}{
		{name: "version-alias-reserved", mode: "version-alias", want: []string{"flag --version is reserved"}, absent: []string{"Options {"}},
		{name: "canonical", args: []string{"--listen-port", "3", "--verbose-flag"}, want: []string{"HTTPPort: 3", "enabled: true"}},
		{name: "aliases", args: []string{"--old-port", "4", "--debug", "--tags", "one", "--tags", "two"}, want: []string{"HTTPPort: 4", "enabled: true", `labels: ["one", "two"]`}},
		{name: "bool-false", args: []string{"--debug=false"}, want: []string{"enabled: false"}},
		{name: "flag-conflict", args: []string{"--old-port", "4", "--listen-port", "3"}, want: []string{"same field"}, absent: []string{"Options {"}},
		{name: "flag-conflict-reverse", args: []string{"--listen-port", "3", "--old-port", "4"}, want: []string{"same field"}, absent: []string{"Options {"}},
		{name: "alias-validation", args: []string{"--old-port", "0"}, want: []string{".listen_port", "Positive"}},
		{name: "canonical-env", env: map[string]string{"BORK_NAMING_LISTEN_PORT": "5"}, want: []string{"HTTPPort: 5"}},
		{name: "alias-env", env: map[string]string{"BORK_NAMING_OLD_PORT": "6"}, want: []string{"HTTPPort: 6"}},
		{name: "env-conflict", env: map[string]string{"BORK_NAMING_LISTEN_PORT": "5", "BORK_NAMING_OLD_PORT": "6"}, want: []string{"same field"}, absent: []string{"Options {"}},
		{name: "empty-env-absent", env: map[string]string{"BORK_NAMING_LISTEN_PORT": "", "BORK_NAMING_OLD_PORT": "6"}, want: []string{"HTTPPort: 6"}},
		{name: "cli-over-env", args: []string{"--old-port", "7"}, env: map[string]string{"BORK_NAMING_LISTEN_PORT": "5", "BORK_NAMING_OLD_PORT": "6"}, want: []string{"HTTPPort: 7"}},
		{name: "unprefixed-alias-disabled", mode: "unprefixed", env: map[string]string{"OLD_PORT": "6"}, want: []string{"HTTPPort: 80"}},
		{name: "wire-config", config: `{"listen_port":8,"user_name":"config"}`, want: []string{"HTTPPort: 8", `userName: "config"`}},
		{name: "alias-config", config: `{"old-port":9}`, want: []string{"HTTPPort: 9"}},
		{name: "alias-config-path", config: `{"old-port":0}`, want: []string{".old-port", "Positive"}},
		{name: "config-conflict", config: `{"old-port":9,"listen_port":8}`, want: []string{"same field"}, absent: []string{"Options {"}},
		{name: "source-config-rejected", config: `{"HTTPPort":8}`, want: []string{"unknown CLI config field"}},
		{name: "named-exact", mode: "named", args: []string{"--exact", "10"}, want: []string{"HTTPPort: 10"}},
		{name: "named-prefixed-alias", mode: "named", args: []string{"--p-old-port", "11"}, want: []string{"HTTPPort: 11"}},
		{name: "named-env-alias", mode: "named", env: map[string]string{"BORK_NAMING_P_OLD_PORT": "12"}, want: []string{"HTTPPort: 12"}},
		{name: "enricher-alias", mode: "enriched", args: []string{"--compat", "13"}, want: []string{"HTTPPort: 13"}},
		{name: "enricher-canonical", mode: "enriched", args: []string{"--enriched", "14"}, want: []string{"HTTPPort: 14"}},
		{name: "enricher-env", mode: "enriched", env: map[string]string{"BORK_ALIAS_EXACT": "15"}, want: []string{"HTTPPort: 15"}},
		{name: "disabled-long", mode: "disabled", args: []string{"--old-port", "4"}, want: []string{"unknown flag"}},
		{name: "disabled-env", mode: "disabled", env: map[string]string{"BORK_NAMING_OLD_PORT": "6"}, want: []string{"HTTPPort: 80"}},
		{name: "aliases-hidden", args: []string{"--help"}, config: `{`, want: []string{"--listen-port", "--verbose-flag", "Usage:"}, absent: []string{"--old-port", "--debug", "--tags", "Error {"}},
		{name: "collision-before-files", mode: "collision", args: []string{"--help"}, want: []string{"alias flag name duplicates"}, absent: []string{"missing.json"}},
		{name: "positional-override", mode: "positional", args: []string{"--help"}, want: []string{"listen-port"}},
		{name: "completion-source-identity", mode: "completion", args: []string{"__completeNoDesc", "go", "--old-port", "16", "--listen-port", ""}, want: []string{"16-port", ":4"}, absent: []string{"HANDLER", "same field"}},
		{name: "root-alias", mode: "root", args: []string{"go", "--old-port", "17", "--debug"}, want: []string{"HTTPPort: 17", "enabled: true"}},
		{name: "env-only-alias", mode: "env-only", env: map[string]string{"BORK_NAMING_OLD_PORT": "18"}, want: []string{"HTTPPort: 18"}},
		{name: "alias-no-deprecation", mode: "deprecated", args: []string{"--old-port", "19"}, want: []string{"HTTPPort: 19"}, absent: []string{"deprecated", "use a new option"}},
		{name: "alias-no-short", args: []string{"-o", "3"}, want: []string{"unknown shorthand"}},
		{name: "yaml-alias", mode: "yaml", config: "old-port: 20\n", want: []string{"HTTPPort: 20"}},
		{name: "yaml-conflict", mode: "yaml", config: "old-port: 20\nlisten_port: 21\n", want: []string{"same field"}},
		{name: "overlay-alias", mode: "overlay", config: `{"old-port":22}`, want: []string{"HTTPPort: 22"}},
		{name: "env-collision-before-files", mode: "env-collision", args: []string{"--help"}, want: []string{"alias environment variable duplicates"}, absent: []string{"missing.json"}},
		{name: "named-config-unchanged", mode: "named", config: `{"old-port":23}`, want: []string{"HTTPPort: 23"}},
		{name: "enricher-config-unchanged", mode: "enriched", config: `{"old-port":24}`, want: []string{"HTTPPort: 24"}},
		{name: "completion-alias-list-once", mode: "completion", args: []string{"__completeNoDesc", "go", "--tags", "one", "--old-port", "16", "--listen-port", ""}, want: []string{"1-16-port", ":4"}, absent: []string{"2-16-port", "HANDLER"}},
		{name: "root-alias-collision", mode: "root-collision", args: []string{"--help"}, want: []string{"flag --old-port conflicts with a root flag"}, absent: []string{"HANDLER"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cwd := t.TempDir()
			config := tc.config
			if config == "" {
				config = "{}"
			}
			if err := os.WriteFile(filepath.Join(cwd, map[bool]string{true: "config.yaml", false: "config.json"}[tc.mode == "yaml"]), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.mode == "overlay" {
				if err := os.WriteFile(filepath.Join(cwd, "base.json"), []byte(`{"listen_port":21}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			mode := tc.mode
			if mode == "" {
				mode = "basic"
			}
			cmd := exec.Command(exe, append([]string{mode}, tc.args...)...)
			cmd.Dir = cwd
			names := []string{"BORK_NAMING_LISTEN_PORT", "BORK_NAMING_OLD_PORT", "BORK_NAMING_VERBOSE_FLAG", "BORK_NAMING_DEBUG", "BORK_NAMING_LABELS", "BORK_NAMING_TAGS", "BORK_NAMING_USER_NAME", "BORK_NAMING_EXACT", "BORK_NAMING_P_OLD_PORT", "BORK_NAMING_P_VERBOSE_FLAG", "BORK_NAMING_P_DEBUG", "BORK_NAMING_P_LABELS", "BORK_NAMING_P_TAGS", "BORK_NAMING_P_USER_NAME", "BORK_ALIAS_EXACT", "LISTEN_PORT", "VERBOSE_FLAG", "LABELS", "USER_NAME", "OLD_PORT"}
			cmd.Env = os.Environ()
			for _, name := range names {
				cmd.Env = append(cmd.Env, name+"="+tc.env[name])
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(string(out), absent) {
					t.Errorf("unexpected %q:\n%s", absent, out)
				}
			}
		})
	}
}
