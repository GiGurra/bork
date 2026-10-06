package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIPersistentRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
pred Positive(value: Int) { value > 0 }
pred RootValid(value: Root) { value.region != "blocked" }
pred LeafValid(value: Leaf) { value.name != "blocked" }
type Root = {
 config: Option[String]
 region: String = "west"
 count: Int where Positive = 1
 verbose: Bool
 token: Option[String]
 trace: Bool
 tags: List[String] = []
} where RootValid derive (codec.Decode)
type Leaf = {
 config: Option[String]
 name: String
 replicas: Int where Positive = 2
 files: List[String] = []
} where LeafValid derive (codec.Decode)
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 count = match (request.partial.Get[Int]("count")) {
  _: cli.Missing => "missing"
  value: Int => toString(value)
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
 }
 cli.Suggestions { choices: [.{ value: s"${count}-region" }] }
}
fn main() {
 args = process.Args()
 mode = args.head().getOr("normal")
 args = args.drop(1)
 baseRootFlags: List[cli.Flag] = [
  .{ field: "config", long: cli.Mapping.Named { name: "root-config" }, configFile: true },
  .{ field: "region", env: "BORK_ROOT_REGION" },
  .{ field: "token", long: cli.Mapping.Disabled, env: "BORK_ROOT_TOKEN" },
  .{ field: "trace", hidden: true }
 ]
 rootFlags = if (mode == "positional") { baseRootFlags.concat([.{ field: "count", positional: true }]) } else if (mode == "short") { baseRootFlags.map(flag => if (flag.field == "region") { flag.copy(short: "r") } else { flag }) } else if (mode == "standalone-warning") { baseRootFlags.map(flag => if (flag.field == "region") { flag.copy(deprecated: "use --zone") } else { flag }) } else { baseRootFlags }
 baseLeafFlags: List[cli.Flag] = [
  .{ field: "config", long: cli.Mapping.Named { name: "leaf-config" }, configFile: true },
  .{ field: "name", env: "BORK_LEAF_NAME" },
  .{ field: "files", positional: true }
 ]
 leafFlags = if (mode == "long") { baseLeafFlags.concat([.{ field: "replicas", long: cli.Mapping.Named { name: "region" } }]) } else { baseLeafFlags }
 files = if (mode == "badfiles") { ["missing.json"] } else { [] }
 leaf = cli.RootSubcommand[Root, Leaf]("leaf", "Leaf", (root, leaf, s) => {
  println(s"HANDLER ${root.region}/${root.count}/${root.verbose}/${root.token}/${leaf.name}/${leaf.replicas}/${leaf.files}/${root.tags}")
 }, flags: leafFlags, configFiles: files, settings: .{ autoShort: true })
 commands = [cli.RootGroup[Root]("group", "Group", [leaf])]
 standalone: Option[(Root, Scope) uses io + net + clock + random + state => Ok] = if (mode == "standalone" || mode == "standalone-warning") {
  Option.Some((root, s) => { println(s"ROOT ${root.region}/${root.count}") })
 } else { Option.None }
 if (mode == "reuse") {
  for (arguments in [["--region", "east", "group", "leaf", "--name", "first"], ["group", "leaf", "--name", "second"]]) {
   println(cli.DispatchRoot[Root]("app", "Root", arguments, commands, flags: rootFlags))
  }
  return
 }
 result = cli.DispatchRoot[Root]("app", "Root", args, commands, flags: rootFlags, configFiles: files, settings: .{ autoShort: true, completion: mode != "disabled" },
  completions: [.{ field: "region", suggest: suggest }], run: standalone)
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
		name, mode, root, leaf, env    string
		args, want, absent, stderrWant []string
	}{
		{name: "default", args: []string{"group", "leaf", "--name", "api"}, want: []string{"HANDLER west/1/false/Option.None/api/2/[]", "Ok"}},
		{name: "before", args: []string{"--region", "east", "group", "leaf", "--name", "api"}, want: []string{"HANDLER east/1"}},
		{name: "after", args: []string{"group", "leaf", "--name", "api", "--region", "east", "--verbose", "a,b", "two words"}, want: []string{"HANDLER east/1/true", `["a,b", "two words"]`}},
		{name: "root-list", args: []string{"group", "leaf", "--name", "api", "--tags", "one", "--tags", "two"}, want: []string{`/api/2/[]/["one", "two"]`}},
		{name: "reuse", mode: "reuse", want: []string{"HANDLER east/1/false/Option.None/first", "HANDLER west/1/false/Option.None/second"}},
		{name: "automatic-short", args: []string{"group", "leaf", "-n", "api", "-r", "5"}, want: []string{"HANDLER west/1", "/api/5/[]"}},
		{name: "explicit-short-conflict", mode: "short", args: []string{"group", "leaf", "--name", "api"}, want: []string{"short flag -r conflicts with a root flag"}, absent: []string{"HANDLER"}},
		{name: "long-conflict", mode: "long", args: []string{"--help"}, want: []string{"flag --region conflicts with a root flag"}, absent: []string{"HANDLER"}},
		{name: "root-positionals", mode: "positional", args: []string{"--help"}, want: []string{"root fields cannot be positional"}, absent: []string{"HANDLER"}},
		{name: "root-help", mode: "badfiles", args: []string{"--help"}, want: []string{"--region string", "--count int", "BORK_ROOT_TOKEN string", "group"}, absent: []string{"missing.json", "--trace", "HANDLER"}},
		{name: "child-help", mode: "badfiles", args: []string{"group", "leaf", "--help"}, want: []string{"Global Flags:", "--count int", "--region string", "BORK_ROOT_TOKEN string"}, absent: []string{"missing.json", "--trace", "HANDLER"}},
		{name: "routing-root", args: []string{}, want: []string{"Usage:", "group"}, absent: []string{"HANDLER", "ROOT "}},
		{name: "standalone", mode: "standalone", args: []string{"--region", "east"}, want: []string{"ROOT east/1", "Ok"}, absent: []string{"HANDLER"}},
		{name: "standalone-warning", mode: "standalone-warning", args: []string{"--region", "east"}, want: []string{"ROOT east/1", "Ok"}, stderrWant: []string{"Flag --region has been deprecated, use --zone"}},
		{name: "standalone-warning-error", mode: "standalone-warning", args: []string{"--region", "east", "--count", "0"}, want: []string{".root.count", "Flag --region has been deprecated, use --zone"}, absent: []string{"ROOT "}},
		{name: "standalone-child", mode: "standalone", args: []string{"group", "leaf", "--name", "api"}, want: []string{"HANDLER"}, absent: []string{"ROOT "}},
		{name: "field-errors", args: []string{"--count", "0", "group", "leaf", "--name", "api", "--replicas", "0"}, want: []string{".root.count", ".replicas"}, absent: []string{"HANDLER"}},
		{name: "record-errors", args: []string{"--region", "blocked", "group", "leaf", "--name", "blocked"}, want: []string{".root", "RootValid", "LeafValid"}, absent: []string{"HANDLER"}},
		{name: "env", env: "env", args: []string{"group", "leaf", "--name", "api"}, want: []string{"HANDLER env/1"}},
		{name: "config", root: `{"region":"config","count":4}`, leaf: `{"name":"configured","replicas":3}`, args: []string{"--root-config", "root.json", "group", "leaf", "--leaf-config", "leaf.json"}, want: []string{"HANDLER config/4", "/configured/3"}},
		{name: "precedence", env: "env", root: `{"region":"config"}`, leaf: `{"name":"configured"}`, args: []string{"--root-config", "root.json", "group", "leaf", "--leaf-config", "leaf.json", "--region", "flag", "--name", "api"}, want: []string{"HANDLER flag/1", "/api/2"}},
		{name: "completion-partial", args: []string{"__completeNoDesc", "group", "leaf", "--region", ""}, want: []string{"missing-region", ":4"}, absent: []string{"HANDLER"}},
		{name: "completion-input", args: []string{"__completeNoDesc", "group", "leaf", "--count", "3", "--region", ""}, want: []string{"3-region", ":4"}, absent: []string{"HANDLER"}},
		{name: "completion-disabled", mode: "disabled", args: []string{"__complete", "group", "leaf", "--region", ""}, want: []string{"unknown command"}, absent: []string{"HANDLER"}},
		{name: "completion-script", mode: "badfiles", args: []string{"completion", "bash"}, want: []string{"bash completion"}, absent: []string{"missing.json", "HANDLER"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cwd := t.TempDir()
			for name, value := range map[string]string{"root.json": tt.root, "leaf.json": tt.leaf} {
				if value != "" {
					if err := os.WriteFile(filepath.Join(cwd, name), []byte(value), 0o644); err != nil {
						t.Fatal(err)
					}
				}
			}
			mode := tt.mode
			if mode == "" {
				mode = "normal"
			}
			cmd := exec.Command(exe, append([]string{mode}, tt.args...)...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "BORK_ROOT_REGION="+tt.env, "BORK_ROOT_TOKEN=", "BORK_LEAF_NAME=")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			out := stdout.String() + stderr.String()
			for _, want := range tt.stderrWant {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q: %s", want, stderr.String())
				}
			}
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range tt.want {
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
