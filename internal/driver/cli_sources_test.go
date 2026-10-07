package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIValueSources(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
use cli.FieldTagsEncode
pred Positive(value: Int) { value > 0 }
type Db = { port: Int where Positive = 5432 } derive (codec.Decode)
type Options = {
 config: Option[String]
 name: String = "default"
 port: Int where Positive = 8080 codec { aliases: ["legacyPort"] }
 verbose: Bool
 tags: List[String] = ["default"]
 maybe: Option[String]
 db: Db codec { name: "database", aliases: ["oldDb"] }
 optional: Option[Db]
 whole: Db = Db {} cli { flatten: false }
} derive (codec.Decode)
type Ordered = { normal: Int = 1, second: String, first: String } derive (codec.Decode)
fn main() {
 match (cli.ParseResolved[Ordered]("ordered", "Indexed", ["one", "two"], [
  cli.Flag { field: "first", position: .Some(0) },
  cli.Flag { field: "second", position: .Some(1) }
 ])) {
  resolved: cli.Resolved[Ordered] => println(s"order=${resolved.sources.map(entry => entry.field).join(",")}")
  error: cli.Error => println(error)
  help: cli.Help => println(help.text)
 }
 result = cli.ParseResolved[Options]("app", "Sources", process.Args(), [
  cli.Flag { field: "config", configFile: true },
  cli.Flag { field: "name", env: "BORK_SOURCE_NAME", positional: true },
  cli.Flag { field: "port", env: "BORK_SOURCE_PORT", short: "p" },
  cli.Flag { field: "verbose", deprecated: "use log level" }
 ], ["base.json", "override.yaml"], settings: .{ envPrefix: "BORK_SOURCE" })
 match (result) {
  resolved: cli.Resolved[Options] => {
   println(resolved.value)
   println(s"warnings=${resolved.warnings}")
   for (field in ["config", "name", "port", "verbose", "tags", "maybe", "db.port", "optional.port", "whole", "unknown"]) {
    println(s"${field}=${resolved.Source(field)}")
   }
  }
  error: cli.Error => println(error)
  help: cli.Help => println(help.text)
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
		name, base, overlay, selected string
		args, env, want, absent       []string
	}{
		{name: "defaults", want: []string{"port=Option.Some(Source.Default)", "verbose=Option.Some(Source.Default)", "maybe=Option.Some(Source.Absent)", "db.port=Option.Some(Source.Default)", "optional.port=Option.Some(Source.Absent)", "whole=Option.Some(Source.Default)", "unknown=Option.None"}},
		{name: "config", base: `{"port":80,"verbose":true,"maybe":null,"database":{"port":81},"tags":[]}`, want: []string{`port=Option.Some(Source.Config { path: "base.json" })`, `maybe=Option.Some(Source.Config { path: "base.json" })`, `db.port=Option.Some(Source.Config { path: "base.json" })`, "tags: []"}},
		{name: "overlay", base: `{"port":80}`, overlay: "port: 81\n", want: []string{"port: 81", `port=Option.Some(Source.Config { path: "override.yaml" })`}},
		{name: "selected", selected: `{"port":82}`, args: []string{"--config", "selected.json"}, want: []string{"port: 82", `port=Option.Some(Source.Config { path: "selected.json" })`, `config=Option.Some(Source.Flag { name: "config" })`}},
		{name: "env", base: `{"port":80}`, env: []string{"BORK_SOURCE_PORT=83"}, want: []string{"port: 83", `port=Option.Some(Source.Env { name: "BORK_SOURCE_PORT" })`}},
		{name: "flag", env: []string{"BORK_SOURCE_PORT=83"}, args: []string{"-p", "84", "--verbose=false", "cli"}, want: []string{"port: 84", `port=Option.Some(Source.Flag { name: "port" })`, `name=Option.Some(Source.Flag { name: "name" })`, `verbose=Option.Some(Source.Flag { name: "verbose" })`}},
		{name: "env-alias", env: []string{"BORK_SOURCE_LEGACY_PORT=88"}, want: []string{`port=Option.Some(Source.Env { name: "BORK_SOURCE_LEGACY_PORT" })`}},
		{name: "flag-alias", args: []string{"--legacy-port", "89"}, want: []string{`port=Option.Some(Source.Flag { name: "port" })`}},
		{name: "warnings", args: []string{"--verbose"}, want: []string{"use log level", `verbose=Option.Some(Source.Flag { name: "verbose" })`}},
		{name: "empty-env", env: []string{"BORK_SOURCE_PORT="}, want: []string{"port=Option.Some(Source.Default)"}},
		{name: "invalid-overridden", base: `{"port":0}`, args: []string{"--port", "85"}, want: []string{"port: 85", `port=Option.Some(Source.Flag { name: "port" })`}},
		{name: "nested-alias", base: `{"oldDb":{"port":86}}`, want: []string{`db.port=Option.Some(Source.Config { path: "base.json" })`, "port: 86"}},
		{name: "optional-active", base: `{"optional":{"port":87}}`, want: []string{`optional.port=Option.Some(Source.Config { path: "base.json" })`}},
		{name: "optional-null", base: `{"optional":{"port":87}}`, overlay: "optional: null\n", want: []string{"optional.port=Option.Some(Source.Absent)"}},
		{name: "validation-error", args: []string{"--port", "0"}, want: []string{"must be Positive"}, absent: []string{"port=Option.Some"}},
		{name: "help", base: "invalid", args: []string{"--help"}, want: []string{"Sources", "Usage:"}, absent: []string{"port=Option.Some", "Error {"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			base, overlay := tt.base, tt.overlay
			if base == "" {
				base = "{}"
			}
			if overlay == "" {
				overlay = "{}"
			}
			for name, data := range map[string]string{"base.json": base, "override.yaml": overlay, "selected.json": tt.selected} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_SOURCE_PORT=", "BORK_SOURCE_NAME=", "BORK_SOURCE_LEGACY_PORT=")
			cmd.Env = append(cmd.Env, tt.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range append(tt.want, "order=normal,second,first") {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(string(out), absent) {
					t.Errorf("unexpected %q:\n%s", absent, out)
				}
			}
		})
	}
}
