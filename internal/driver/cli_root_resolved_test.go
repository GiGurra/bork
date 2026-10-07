package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIResolvedRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/env"
import "bork/process"
use codec.Defaults
pred Positive(value: Int) { value > 0 }
pred RootValid(value: Root) { value.region != "blocked" }
pred LeafValid(value: Leaf) { value.name != "blocked" }
type Db = { port: Int where Positive = 5432, note: Option[String] } derive (codec.Decode)
type Root = {
 config: Option[String]
 region: String = "west" codec { aliases: ["zone"] }
 verbose: Bool
 db: Option[Db] = .Some(Db { note: .Some("baseline") }) codec { name: "database", aliases: ["oldDb"] }
} where RootValid derive (codec.Decode)
type Leaf = {
 config: Option[String]
 name: String = "api" codec { aliases: ["title"] }
 replicas: Int where Positive = 2
 files: List[String] = []
} where LeafValid derive (codec.Decode)
fn showRoot(root: cli.Resolved[Root]) uses io {
 println(s"ROOT ${root.value}")
 for (field in ["config", "region", "verbose", "db.port", "db.note", "database.port", "unknown"]) {
  println(s"ROOT ${field}=${root.Source(field)}")
 }
 println(s"ROOT-WARN ${root.warnings}")
}
fn showChild(child: cli.Resolved[Leaf]) uses io {
 println(s"CHILD ${child.value}")
 for (field in ["config", "name", "replicas", "files", "region"]) {
  println(s"CHILD ${field}=${child.Source(field)}")
 }
 println(s"CHILD-WARN ${child.warnings}")
}
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[String]("region")) {
  _: cli.Missing => cli.Suggestions { choices: [.{ value: "missing-region" }] }
  value: String => cli.Suggestions { choices: [.{ value: s"${value}-region" }] }
  error: codec.DecodeError => cli.Error { errors: [error] }
 }
}
fn main() {
 mode = env.Get("BORK_RR_MODE").getOr("normal")
 rootFlags: List[cli.Flag] = [
  .{ field: "config", long: .Named { name: "root-config" }, env: "BORK_RR_CONFIG", configFile: true },
  .{ field: "region", env: "BORK_RR_REGION", deprecated: if (mode == "warnings") { "use --zone" } else { "" } }
 ]
 childFlags: List[cli.Flag] = [
  .{ field: "config", long: .Named { name: "leaf-config" }, env: "BORK_RR_LEAF_CONFIG", configFile: true },
  .{ field: "name", env: "BORK_RR_NAME", deprecated: if (mode == "warnings") { "use --title" } else { "" } },
  .{ field: "files", positional: true }
 ]
 rootFiles = if (mode == "badfiles") { ["missing-root.json"] } else { ["root-base.json", "root-overlay.yaml"] }
 childFiles = if (mode == "badfiles") { ["missing-child.json"] } else { ["child-base.json"] }
 resolvedLeaf = cli.RootSubcommandResolved[Root, Leaf]("leaf", "Resolved leaf", (root, child, s) => {
  onClose(s, () => { println("CLOSED") })
  showRoot(root)
  showChild(child)
 }, flags: childFlags, configFiles: childFiles, settings: .{ envPrefix: "BORK_RR_LEAF" })
 legacyLeaf = cli.RootSubcommand[Root, Leaf]("leaf", "Legacy leaf", (root, child, s) => {
  println(s"LEGACY ${root.region}/${child.name}")
 }, flags: childFlags, configFiles: childFiles)
 commands = [cli.RootGroup[Root]("group", "Group", [if (mode == "legacy-leaf") { legacyLeaf } else { resolvedLeaf }])]
 standalone: Option[(cli.Resolved[Root], Scope) uses io + net + clock + random + state => Ok] = if (mode == "root" || mode == "run-root") {
  .Some((root, s) => { println("STANDALONE"); showRoot(root) })
 } else { .None }
 settings = cli.Settings { envPrefix: "BORK_RR", completion: mode != "disabled" }
 if (mode == "run" || mode == "run-root") {
  println(cli.RunRootResolved[Root]("app", "Resolved root", commands, flags: rootFlags, configFiles: rootFiles, settings: settings, run: standalone))
  return
 }
 if (mode == "legacy-entry") {
  println(cli.DispatchRoot[Root]("app", "Resolved root", process.Args(), commands, flags: rootFlags, configFiles: rootFiles, settings: settings))
  return
 }
 if (mode == "reuse") {
  for (arguments in [["--region", "east", "group", "leaf"], ["group", "leaf"]]) {
   println(cli.DispatchRootResolved[Root]("app", "Resolved root", arguments, commands, flags: rootFlags, configFiles: rootFiles, settings: settings))
  }
  return
 }
 result = cli.DispatchRootResolved[Root]("app", "Resolved root", process.Args(), if (mode == "root") { [] } else { commands }, flags: rootFlags, configFiles: rootFiles, settings: settings, completions: [.{ field: "region", suggest: suggest }], run: standalone)
 match (result) {
  help: cli.Help => { println(help.text); eprintln(help.diagnostics) }
  other => println(other)
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
		name, mode, rootBase, rootOverlay, childBase string
		args, env, want, absent, stderr              []string
	}{
		{name: "defaults", args: []string{"group", "leaf"}, want: []string{"ROOT region=Option.Some(Source.Default)", "ROOT verbose=Option.Some(Source.Default)", "ROOT db.port=Option.Some(Source.Default)", "ROOT db.note=Option.Some(Source.Default)", "ROOT unknown=Option.None", "ROOT database.port=Option.None", "CHILD name=Option.Some(Source.Default)", "CHILD region=Option.None", "CLOSED\nOk"}},
		{name: "before", args: []string{"--region", "east", "group", "leaf"}, want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`}},
		{name: "after-and-positionals", args: []string{"group", "leaf", "--region", "east", "--verbose=false", "one", "two words"}, want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`, `ROOT verbose=Option.Some(Source.Flag { name: "verbose" })`, `CHILD files=Option.Some(Source.Flag { name: "files" })`, `files: ["one", "two words"]`}},
		{name: "config-chains", rootBase: `{"region":"first"}`, rootOverlay: "region: last\ndatabase:\n  port: 6000\n", childBase: `{"name":"child"}`, args: []string{"group", "leaf"}, want: []string{`region: "last"`, `ROOT region=Option.Some(Source.Config { path: "root-overlay.yaml" })`, `ROOT db.port=Option.Some(Source.Config { path: "root-overlay.yaml" })`, `ROOT db.note=Option.Some(Source.Default)`, `CHILD name=Option.Some(Source.Config { path: "child-base.json" })`}},
		{name: "environment", rootBase: `{"region":"config"}`, childBase: `{"name":"config"}`, env: []string{"BORK_RR_REGION=env", "BORK_RR_NAME=env-child"}, args: []string{"group", "leaf"}, want: []string{`ROOT region=Option.Some(Source.Env { name: "BORK_RR_REGION" })`, `CHILD name=Option.Some(Source.Env { name: "BORK_RR_NAME" })`}},
		{name: "env-aliases", env: []string{"BORK_RR_ZONE=alias", "BORK_RR_LEAF_TITLE=child"}, args: []string{"group", "leaf"}, want: []string{`ROOT region=Option.Some(Source.Env { name: "BORK_RR_ZONE" })`, `CHILD name=Option.Some(Source.Env { name: "BORK_RR_LEAF_TITLE" })`}},
		{name: "flag-aliases", env: []string{"BORK_RR_REGION=env"}, args: []string{"--old-db-port", "6001", "group", "leaf", "--zone", "flag", "--title", "child"}, want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`, `ROOT db.port=Option.Some(Source.Flag { name: "database-port" })`, `CHILD name=Option.Some(Source.Flag { name: "name" })`}},
		{name: "selectors", args: []string{"--root-config", "root-selected.json", "group", "leaf", "--leaf-config", "leaf-selected.yml"}, want: []string{`ROOT config=Option.Some(Source.Flag { name: "root-config" })`, `ROOT region=Option.Some(Source.Config { path: "root-selected.json" })`, `CHILD config=Option.Some(Source.Flag { name: "leaf-config" })`, `CHILD name=Option.Some(Source.Config { path: "leaf-selected.yml" })`}},
		{name: "env-selector", env: []string{"BORK_RR_CONFIG=root-selected.json"}, args: []string{"group", "leaf"}, want: []string{`ROOT config=Option.Some(Source.Env { name: "BORK_RR_CONFIG" })`, `ROOT region=Option.Some(Source.Config { path: "root-selected.json" })`}},
		{name: "parent-null", rootBase: `{"database":null}`, args: []string{"group", "leaf"}, want: []string{"ROOT db.port=Option.Some(Source.Absent)", "ROOT db.note=Option.Some(Source.Absent)"}},
		{name: "null-then-reactivate", rootBase: `{"database":null}`, rootOverlay: "database:\n  port: 6002\n", args: []string{"group", "leaf"}, want: []string{`ROOT db.port=Option.Some(Source.Config { path: "root-overlay.yaml" })`, "ROOT db.note=Option.Some(Source.Absent)"}},
		{name: "root-only", mode: "root", args: []string{"--region", "alone"}, want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`, "Ok"}, absent: []string{"CHILD ", "CLOSED"}},
		{name: "run-root", mode: "run-root", args: []string{"--region", "alone"}, want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`, "Ok"}, absent: []string{"CHILD "}},
		{name: "run-root-child", mode: "run-root", args: []string{"group", "leaf"}, want: []string{"CHILD name=Option.Some(Source.Default)", "CLOSED\nOk"}, absent: []string{"STANDALONE"}},
		{name: "run-child", mode: "run", args: []string{"group", "leaf", "--name", "run"}, want: []string{`CHILD name=Option.Some(Source.Flag { name: "name" })`, "CLOSED\nOk"}},
		{name: "legacy-entry", mode: "legacy-entry", args: []string{"group", "leaf", "--region", "old"}, want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`}},
		{name: "legacy-leaf", mode: "legacy-leaf", args: []string{"group", "leaf"}, want: []string{"LEGACY west/api", "Ok"}, absent: []string{"ROOT ", "CHILD "}},
		{name: "reuse", mode: "reuse", want: []string{`ROOT region=Option.Some(Source.Flag { name: "region" })`, `ROOT region=Option.Some(Source.Default)`}},
		{name: "warnings", mode: "warnings", args: []string{"--region", "east", "group", "leaf", "--name", "child"}, want: []string{"ROOT-WARN [", "CHILD-WARN [", "use --zone", "use --title"}, stderr: []string{"Flag --region has been deprecated, use --zone", "Flag --name has been deprecated, use --title"}},
		{name: "field-errors", args: []string{"group", "leaf", "--database-port", "0", "--replicas", "0"}, want: []string{".root.database.port", ".replicas", "must be Positive"}, absent: []string{"ROOT ", "CHILD ", "CLOSED"}},
		{name: "record-errors", args: []string{"group", "leaf", "--region", "blocked", "--name", "blocked"}, want: []string{"RootValid", "LeafValid"}, absent: []string{"ROOT ", "CHILD "}},
		{name: "help", mode: "badfiles", args: []string{"group", "leaf", "--help"}, want: []string{"Global Flags:", "--region"}, absent: []string{"ROOT ", "CHILD ", "missing-root", "missing-child"}},
		{name: "routing-help", args: nil, want: []string{"Usage:", "group"}, absent: []string{"ROOT ", "CHILD "}},
		{name: "completion", args: []string{"__completeNoDesc", "group", "leaf", "--region", ""}, want: []string{"missing-region", ":4"}, absent: []string{"ROOT ", "CHILD "}},
		{name: "completion-disabled", mode: "disabled", args: []string{"__complete", "group", "leaf", "--region", ""}, want: []string{"unknown command"}, absent: []string{"ROOT ", "CHILD "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cwd := t.TempDir()
			files := map[string]string{"root-base.json": tt.rootBase, "root-overlay.yaml": tt.rootOverlay, "child-base.json": tt.childBase, "root-selected.json": `{"region":"selected"}`, "leaf-selected.yml": "name: selected-child\n"}
			for name, value := range files {
				if value == "" {
					value = "{}"
				}
				if err := os.WriteFile(filepath.Join(cwd, name), []byte(value), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = cwd
			cmd.Env = append(os.Environ(), "BORK_RR_MODE="+tt.mode, "BORK_RR_CONFIG=", "BORK_RR_LEAF_CONFIG=", "BORK_RR_REGION=", "BORK_RR_NAME=", "BORK_RR_ZONE=", "BORK_RR_LEAF_TITLE=")
			cmd.Env = append(cmd.Env, tt.env...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s\n%s", err, &stdout, &stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q:\n%s\nstderr:\n%s", want, &stdout, &stderr)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(stdout.String()+stderr.String(), absent) {
					t.Errorf("unexpected %q:\n%s\n%s", absent, &stdout, &stderr)
				}
			}
			if tt.mode == "warnings" {
				var lists []string
				for _, line := range strings.Split(stdout.String(), "\n") {
					for _, prefix := range []string{"ROOT-WARN ", "CHILD-WARN "} {
						if strings.HasPrefix(line, prefix) {
							list := strings.TrimPrefix(line, prefix)
							lists = append(lists, list)
							for _, warning := range tt.stderr {
								if !strings.Contains(list, warning) {
									t.Errorf("%s missing %q: %s", prefix, warning, list)
								}
							}
						}
					}
				}
				if len(lists) != 2 || lists[0] != lists[1] {
					t.Errorf("root and child warning lists differ: %v", lists)
				}
			}
			for _, warning := range tt.stderr {
				if got := strings.Count(stderr.String(), warning); got != 1 {
					t.Errorf("warning %q appeared %d times:\n%s", warning, got, &stderr)
				}
			}
		})
	}
}

func TestCLIResolvedRootTypeCheckWithoutGo(t *testing.T) {
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
use codec.Defaults
type Root = { region: String = "west" } derive (codec.Decode)
type Leaf = { name: String } derive (codec.Decode)
fn main() {
 commands = [cli.RootSubcommandResolved[Root, Leaf]("leaf", "Leaf", (root, leaf, s) => {
  println(root.Source("region"))
  println(leaf.value.name)
 })]
 println(cli.RunRootResolved[Root]("app", "Root", commands, run: .Some((root, s) => { println(root.Source("region")) })))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Check(root); err != nil {
		t.Fatalf("resolved root checking without Go: %v", err)
	}
}
