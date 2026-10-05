package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIIndexedPositionals(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
import "bork/process"
use codec.Defaults
pred positive(n: Int) { n > 0 }
type Options = { targets: List[String] = [], count: Int where positive, region: String } derive (codec.Decode)
type Numbers = { values: List[List[Int]], head: Int } derive (codec.Decode)
type Scalars = { second: String, first: String } derive (codec.Decode)
fn targets(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 region = match (request.partial.Get[String]("region")) {
  value: String => value
  _: cli.Missing => "default"
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
 }
 count = match (request.partial.Get[Int]("count")) {
  value: Int => value
  _: cli.Missing => 1
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
 }
 cli.Suggestions { choices: [.{ value: s"${region}-${count}-web" }, .{ value: s"${region}-${count}-worker" }] }
}
fn main() {
 deploy = cli.SubcommandWith[Options]("deploy", "Deploy", (options, s) => { println(options) }, completions: [.{ field: "targets", suggest: targets }], flags: [
  .{ field: "targets", position: Option.Some(2) }
  .{ field: "count", position: Option.Some(1), choices: [.{ value: "1" }, .{ value: "2" }] }
  .{ field: "region", position: Option.Some(0), env: "BORK_POSITIONAL_REGION", choices: [.{ value: "dev" }, .{ value: "prod" }] }
 ], configFiles: ["base.json"])
 numbers = cli.Subcommand[Numbers]("numbers", "JSON elements", (options, s) => { println(options) }, flags: [.{ field: "values", position: Option.Some(1) }, .{ field: "head", position: Option.Some(0) }])
 scalars = cli.Subcommand[Scalars]("scalars", "Two scalars", (options, s) => { println(options) }, flags: [.{ field: "first", position: Option.Some(0), choices: [.{ value: "alpha" }] }, .{ field: "second", position: Option.Some(1), choices: [.{ value: "beta" }] }])
 match (cli.RunCommands("app", "", [deploy, numbers, scalars])) {
  error: cli.Error => { println(error); process.Exit(1) }
  Ok => {}
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
		name            string
		args            []string
		base, env, want string
		exact, failure  bool
	}{
		{name: "declaration-reordered", args: []string{"deploy", "dev", "2", "one", "two"}, base: `{}`, want: `Options { targets: ["one", "two"], count: 2, region: "dev" }`},
		{name: "literal-list-elements", args: []string{"deploy", "dev", "2", "one,two", " spaced ", "", "\"quoted\""}, base: `{}`, want: `targets: ["one,two", " spaced ", "", "\"quoted\""]`},
		{name: "json-list-elements", args: []string{"numbers", "7", "[1,2]", "[]", "[3,4]"}, want: `Numbers { values: [[1, 2], [], [3, 4]], head: 7 }`},
		{name: "config-fills-omitted", args: []string{"deploy"}, base: `{"region":"file","count":3,"targets":["configured"]}`, want: `Options { targets: ["configured"], count: 3, region: "file" }`},
		{name: "positional-env-config", args: []string{"deploy"}, base: `{"count":3}`, env: "env", want: `count: 3, region: "env"`},
		{name: "cli-over-env-config", args: []string{"deploy", "cli", "4", "provided"}, base: `{"region":"file","count":3,"targets":["configured"]}`, env: "env", want: `targets: ["provided"], count: 4, region: "cli"`},
		{name: "missing-required", args: []string{"deploy"}, base: `{}`, want: "is missing", failure: true},
		{name: "fact-error", args: []string{"deploy", "dev", "0"}, base: `{}`, want: "must be positive", failure: true},
		{name: "scalar-excess", args: []string{"scalars", "alpha", "beta", "extra"}, want: "received 3", failure: true},
		{name: "help-order", args: []string{"deploy", "--help"}, want: "app deploy [region] [count] [targets...]"},
		{name: "first-suggestions", args: []string{"__completeNoDesc", "deploy", "p"}, want: "prod\n:4\n", exact: true},
		{name: "second-suggestions", args: []string{"__completeNoDesc", "deploy", "dev", ""}, want: "1\n2\n:4\n", exact: true},
		{name: "list-suggestions", args: []string{"__completeNoDesc", "deploy", "dev", "2", ""}, base: `{}`, want: "dev-2-web\ndev-2-worker\n:4\n", exact: true},
		{name: "repeated-list-suggestions", args: []string{"__completeNoDesc", "deploy", "dev", "2", "one", "dev-2-wo"}, base: `{}`, want: "dev-2-worker\n:4\n", exact: true},
		{name: "after-double-dash", args: []string{"__completeNoDesc", "deploy", "--", "dev", "2", "dev-2-w"}, base: `{}`, want: "dev-2-web\ndev-2-worker\n:4\n", exact: true},
		{name: "second-scalar", args: []string{"__completeNoDesc", "scalars", "alpha", "b"}, want: "beta\n:4\n", exact: true},
		{name: "scalar-exhaustion", args: []string{"__completeNoDesc", "scalars", "alpha", "beta", ""}, want: ":4\n", exact: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.base != "" {
				if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte(tt.base), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_POSITIONAL_REGION="+tt.env)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if (err != nil) != tt.failure {
				t.Fatalf("run: %v\n%s\n%s", err, out, &stderr)
			}
			if tt.exact && string(out) != tt.want || !tt.exact && !strings.Contains(string(out), tt.want) {
				t.Errorf("stdout=%q, want %q; stderr=%s", out, tt.want, &stderr)
			}
		})
	}
}

func TestCLIPositionalMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
use codec.Defaults
type Scalars = { a: String, b: String } derive (codec.Decode)
type Optional = { a: String = "default", b: String } derive (codec.Decode)
type Lists = { a: List[String], b: List[String] } derive (codec.Decode)
type Collision = { fooBar: String, fooBAR: String } derive (codec.Decode)
fn main() {
 println(cli.Parse[Collision]("app", "", ["first", "second"], flags: [.{ field: "fooBar", position: Option.Some(0) }, .{ field: "fooBAR", position: Option.Some(1) }], configFiles: ["missing.json"]))
 println(cli.Parse[Scalars]("app", "", ["--help"], flags: [.{ field: "a", position: Option.Some(-1) }]))
 println(cli.Parse[Scalars]("app", "", ["--help"], flags: [.{ field: "a", position: Option.Some(1) }]))
 println(cli.Parse[Scalars]("app", "", ["--help"], flags: [.{ field: "a", position: Option.Some(0) }, .{ field: "b", position: Option.Some(0) }]))
 println(cli.Parse[Scalars]("app", "", ["--help"], flags: [.{ field: "a", positional: true, position: Option.Some(0) }]))
 println(cli.Parse[Scalars]("app", "", ["--help"], flags: [.{ field: "a", positional: true }, .{ field: "b", position: Option.Some(0) }]))
 println(cli.Parse[Scalars]("app", "", ["--help"], flags: [.{ field: "a", position: Option.Some(0), short: "a" }]))
 println(cli.Parse[Optional]("app", "", ["--help"], flags: [.{ field: "a", position: Option.Some(0) }, .{ field: "b", position: Option.Some(1) }]))
 println(cli.Parse[Lists]("app", "", ["--help"], flags: [.{ field: "a", position: Option.Some(0) }, .{ field: "b", position: Option.Some(1) }]))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{"positional name duplicates fooBar", "position must be nonnegative", "positions must be contiguous", "position duplicates a", "position and positional cannot both", "cannot mix positional shorthand", "short flag requires an enabled long flag", "required positional cannot follow optional/default", "a List positional must be last"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "missing.json") {
		t.Errorf("positional collision must precede config access:\n%s", out)
	}
	if strings.Contains(string(out), "Help {") {
		t.Errorf("metadata errors should precede help:\n%s", out)
	}
}

func TestCLICommandDeprecation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
import "bork/process"
use codec.Defaults
type Options = { value: String = "default" } derive (codec.Decode)
fn main() {
 old = cli.Subcommand[Options]("old", "Obsolete leaf", (options, s) => { onClose(s, () => { println("closed") }); println("handler") }).copy(deprecated: "choose current")
 current = cli.Subcommand[Options]("current", "Current leaf", (options, s) => {})
 manual = cli.Command { name: "manual", description: "Obsolete manual leaf", deprecated: "choose current", execute: (name, arguments, s) => {
  if (arguments.filter(argument => argument == "--help").length() > 0) { cli.Help { text: "Manual help\n", diagnostics: "Manual diagnostic\n" } } else if (arguments.filter(argument => argument == "--bad").length() > 0) { cli.Error { errors: [codec.DecodeError { path: name, message: "manual error" }] } } else { println("manual-handler") }
 } }
 legacy = cli.Group("legacy", "Obsolete group", [current]).copy(deprecated: "choose current")
 match (cli.RunCommands("app", "", [old, current, manual, legacy])) {
  error: cli.Error => { println(error); process.Exit(1) }
  Ok => {}
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
		name         string
		args         []string
		want, absent []string
		diagnostics  string
		failure      bool
	}{
		{name: "root-help", args: []string{"--help"}, want: []string{"Current leaf"}, absent: []string{"Obsolete"}},
		{name: "root-completion", args: []string{"__completeNoDesc", ""}, want: []string{"current\n"}, absent: []string{"old\n", "manual\n", "legacy\n"}, diagnostics: "Completion ended with directive: ShellCompDirectiveNoFileComp\n"},
		{name: "native-success", args: []string{"old"}, want: []string{"handler\nclosed\n"}, absent: []string{"deprecated"}, diagnostics: "Command \"old\" is deprecated, choose current\n"},
		{name: "help-command", args: []string{"help", "old"}, want: []string{"Obsolete leaf"}, absent: []string{"deprecated", "handler", "closed"}},
		{name: "native-help", args: []string{"old", "--help"}, want: []string{"Obsolete leaf"}, absent: []string{"deprecated", "handler", "closed"}, diagnostics: "Command \"old\" is deprecated, choose current\n"},
		{name: "native-error", args: []string{"old", "--bad"}, want: []string{"unknown flag", "choose current"}, absent: []string{"handler", "closed"}, failure: true},
		{name: "group-help", args: []string{"legacy", "--help"}, want: []string{"Obsolete group"}, absent: []string{"deprecated"}, diagnostics: "Command \"legacy\" is deprecated, choose current\n"},
		{name: "manual-success", args: []string{"manual"}, want: []string{"manual-handler\n"}, absent: []string{"deprecated"}, diagnostics: "Command \"manual\" is deprecated, choose current\n"},
		{name: "manual-help", args: []string{"manual", "--help"}, want: []string{"Manual help\n"}, absent: []string{"deprecated", "manual-handler"}, diagnostics: "Command \"manual\" is deprecated, choose current\nManual diagnostic\n"},
		{name: "manual-error", args: []string{"manual", "--bad"}, want: []string{"manual error", "choose current"}, absent: []string{"manual-handler"}, failure: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(exe, tt.args...)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if (err != nil) != tt.failure {
				t.Fatalf("run: %v\n%s\n%s", err, out, &stderr)
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
			if stderr.String() != tt.diagnostics {
				t.Errorf("stderr=%q, want %q", &stderr, tt.diagnostics)
			}
		})
	}
	cmd := exec.Command(exe, "old")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "Command \"old\" is deprecated, choose current\nhandler\nclosed\n" {
		t.Fatalf("warning must precede handler and cleanup: %s", out)
	}
}

func TestCLIManualDeprecationOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
fn main() {
 command = cli.Command { name: "manual", description: "", deprecated: "use typed command", execute: (name, arguments, s) => { println("manual-handler") } }
 _ = cli.RunCommands("app", "", [command])
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe, "manual").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if string(out) != "manual-handler\nCommand \"manual\" is deprecated, use typed command\n" {
		t.Fatalf("manual callback warning order: %s", out)
	}
}
