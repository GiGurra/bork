package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIVersion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
type Options = { name: String } derive (codec.Decode)
type Global = { region: String } derive (codec.Decode)
type VersionField = { version: String } derive (codec.Decode)
fn main() {
 args = process.Args()
 mode = args.head().getOr("parse")
 args = args.drop(1)
 settings = cli.Settings { version: "1.2.3", completion: false }
 result: Options | VersionField | Ok | cli.Error | cli.Help = if (mode == "run") {
  cli.Run[Options]("app", "Version", (options, s) => { println("HANDLER") }, configFiles: ["missing.json"], settings: settings)
 } else if (mode == "manual") {
  command = cli.Command { name: "manual", description: "Manual", version: "manual-v", execute: (displayName, arguments, s) => { println(s"HANDLER ${arguments}") } }
  cli.Dispatch("app", "Root", args, [command])
 } else if (mode == "parse") {
  cli.Parse[Options]("app", "Version", args, configFiles: ["missing.json"], settings: settings)
 } else if (mode == "with") {
  cli.ParseWith[Options]("app", "Version", args, configFiles: ["missing.json"], settings: settings)
 } else if (mode == "collision") {
  cli.Parse[VersionField]("app", "Version", args, settings: settings)
 } else if (mode == "renamed-collision") {
  cli.Parse[Options]("app", "Version", args, flags: [.{ field: "name", long: cli.Mapping.Named { name: "version" } }], settings: settings)
 } else if (mode == "ordinary") {
  cli.Parse[VersionField]("app", "Version", args)
 } else if (mode == "root") {
  commands = [cli.RootSubcommand[Global, Options]("leaf", "Leaf", (root, leaf, s) => { println("HANDLER") }, configFiles: ["missing-leaf.json"], settings: .{ version: "leaf-v" })]
  cli.DispatchRoot[Global]("app", "Root", args, commands, configFiles: ["missing-root.json"], settings: settings)
 } else {
  leaf = cli.SubcommandWith[Options]("leaf", "Leaf", (options, s) => { println("HANDLER") }, configFiles: ["missing.json"], settings: if (mode == "noinherit") { .{} } else { .{ version: "leaf-v" } })
  group = cli.Group("group", "Group", [leaf.copy(aliases: ["l"])]).copy(version: "group-v")
  cli.Dispatch("app", "Root", args, [group], settings: settings)
 }
 match (result) {
  help: cli.Help => { println(help.text); eprintln(help.diagnostics) }
  other => { println(other) }
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mode   string
		args         []string
		want, absent string
	}{
		{"run no sources", "run", []string{"--version"}, "app version 1.2.3\n", "HANDLER"},
		{"manual metadata", "manual", []string{"manual", "--version"}, "app manual version manual-v\n", "HANDLER"},
		{"parse no sources", "parse", []string{"--version"}, "app version 1.2.3\n", "missing"},
		{"with no sources", "with", []string{"--version"}, "app version 1.2.3\n", "missing"},
		{"tree root", "tree", []string{"--version"}, "app version 1.2.3\n", "HANDLER"},
		{"group metadata", "tree", []string{"group", "--version"}, "app group version group-v\n", "HANDLER"},
		{"leaf settings", "tree", []string{"group", "leaf", "--version"}, "app group leaf version leaf-v\n", "missing"},
		{"alias uses canonical path", "tree", []string{"group", "l", "--version"}, "app group leaf version leaf-v\n", "HANDLER"},
		{"typed root no sources", "root", []string{"--version"}, "app version 1.2.3\n", "missing"},
		{"typed root leaf no sources", "root", []string{"leaf", "--version"}, "app leaf version leaf-v\n", "missing"},
		{"renamed field collision", "renamed-collision", []string{"--version"}, "reserved", "version 1.2.3"},
		{"manual bool literal", "manual", []string{"manual", "--version=1"}, "app manual version manual-v\n", "HANDLER"},
		{"manual false does execute", "manual", []string{"manual", "--version=false"}, "HANDLER", "version manual-v"},
		{"manual raw flags preserved", "manual", []string{"manual", "--unknown", "value", "--version=false"}, "[\"--unknown\", \"value\", \"--version=false\"]", "version manual-v"},
		{"manual terminator preserves raw args", "manual", []string{"manual", "--", "--version"}, "[\"--\", \"--version\"]", "version manual-v"},
		{"no automatic version shorthand", "parse", []string{"-v"}, "unknown shorthand", "version 1.2.3"},
		{"reserved field", "collision", []string{"--version"}, "reserved", "version 1.2.3"},
		{"ordinary version field", "ordinary", []string{"--version", "input"}, "input", "reserved"},
		{"no automatic inheritance", "noinherit", []string{"group", "leaf", "--version"}, "unknown flag", "version 1.2.3"},
		{"help advertises version", "parse", []string{"--help"}, "--version", "missing"},
		{"false retains normal validation", "parse", []string{"--version=false"}, "missing.json", "version 1.2.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			output, err := exec.Command(exe, append([]string{tt.mode}, tt.args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), tt.want) {
				t.Fatalf("output %q lacks %q", output, tt.want)
			}
			if tt.absent != "" && strings.Contains(string(output), tt.absent) {
				t.Fatalf("output %q includes %q", output, tt.absent)
			}
		})
	}
}
