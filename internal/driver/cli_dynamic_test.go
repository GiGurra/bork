package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIDynamicCompletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
import "bork/process"
import "bork/fs"
import "bork/encoding"
use codec.Defaults
pred validPort(port: Int) { port > 0 }
type Options = {
 config: Option[String]
 namespace: String
 port: Int where validPort = 8080
 region: String = "west"
 verbose: Bool
 tags: List[String] = []
 optional: Option[String]
 resource: Option[String]
 inspect: Option[String]
 targets: List[String] = []
} derive (codec.Decode)
fn complete(request: cli.CompletionRequest, s: Scope) uses io: cli.Suggestions | cli.Error {
 onClose(s, () => { _ = fs.Write("callback.txt", encoding.Utf8("closed")) })
 if (request.field == "inspect") {
  port = match (request.partial.Get[Int]("port")) {
   _: cli.Missing => "missing-port"
   error: codec.DecodeError => s"invalid-port:${error.path}"
   value: Int => s"port:${value}"
  }
  region = match (request.partial.Get[String]("region")) {
   _: cli.Missing => "missing-region"
   _: codec.DecodeError => "invalid-region"
   value: String => s"region:${value}"
  }
  verbose = match (request.partial.Get[Bool]("verbose")) {
   _: cli.Missing => "missing-verbose"
   _: codec.DecodeError => "invalid-verbose"
   value: Bool => s"verbose:${value}"
  }
  tags = match (request.partial.Get[List[String]]("tags")) {
   _: cli.Missing => "missing-tags"
   _: codec.DecodeError => "invalid-tags"
   values: List[String] => s"tags:${values.length()}"
  }
  optional = match (request.partial.Get[Option[String]]("optional")) {
   _: cli.Missing => "missing-optional"
   _: codec.DecodeError => "invalid-optional"
   option: Option[String] => match (option) {
    Option.None => "null-optional"
    Option.Some(value) => s"optional:${value}"
   }
  }
  mismatch = match (request.partial.Get[Int]("namespace")) {
   _: cli.Missing => "missing-namespace"
   error: codec.DecodeError => s"mismatch:${error.path}"
   _: Int => "unexpected-number"
  }
  unknown = match (request.partial.Get[String]("unknown")) {
   error: codec.DecodeError => s"unknown:${error.path}"
   _ => "unexpected-unknown"
  }
  cli.Suggestions { choices: [.{ value: port }, .{ value: region }, .{ value: verbose }, .{ value: tags }, .{ value: optional }, .{ value: mismatch }, .{ value: unknown }] }
 } else {
  namespace = match (request.partial.Get[String]("namespace")) {
   _: cli.Missing => "default"
   error: codec.DecodeError => { return cli.Error { errors: [error] } }
   value: String => value
  }
  cli.Suggestions { choices: [.{ value: s"${namespace}-web", description: "Web service" }, .{ value: s"${namespace}-worker" }, .{ value: s"${namespace}-web", description: "Duplicate" }], keepOrder: true }
 }
}
fn main() {
 flags: List[cli.Flag] = [
  .{ field: "config", configFile: true, env: "BORK_DYNAMIC_CONFIG" }
  .{ field: "namespace", short: "n", env: "BORK_DYNAMIC_NAMESPACE" }
  .{ field: "tags", env: "BORK_DYNAMIC_TAGS" }
  .{ field: "resource", choices: [.{ value: "stale" }] }
  .{ field: "targets", positional: true }
 ]
 completions: List[cli.Completion] = [.{ field: "resource", suggest: complete }, .{ field: "inspect", suggest: complete }, .{ field: "targets", suggest: complete }]
 leaf = cli.SubcommandWith[Options]("deploy", "Deploy", (options, s) => {
  onClose(s, () => { _ = fs.Write("handler.txt", encoding.Utf8("closed")) })
  println(options)
 }, completions: completions, flags: flags, configFiles: ["base.json"])
 match (cli.RunCommands("app", "", [cli.Group("cluster", "", [leaf])])) {
  error: cli.Error => { println(error); process.ExitNow(1) }
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
		name                          string
		args                          []string
		base, config, namespace, tags string
		want                          string
		exact                         bool
		callback, handler, failure    bool
	}{
		{name: "missing-defaults", args: []string{"__complete", "cluster", "deploy", "--inspect", ""}, base: `{}`, want: "missing-port\nmissing-region\nmissing-verbose\nmissing-tags\nmissing-optional\nmissing-namespace\nunknown:.unknown\n:4\n", exact: true, callback: true},
		{name: "partial-bool", args: []string{"__complete", "cluster", "deploy", "--verbose", "--inspect", "verbose"}, base: `{}`, want: "verbose:true\n:4\n", exact: true, callback: true},
		{name: "partial-flags", args: []string{"__complete", "cluster", "deploy", "-n", "prod", "--resource", "p"}, base: `{}`, want: "prod-web\tWeb service\nprod-worker\n:36\n", exact: true, callback: true},
		{name: "assignment", args: []string{"__complete", "cluster", "deploy", "--namespace=prod", "--resource=prod-w"}, base: `{}`, want: "prod-web\tWeb service\nprod-worker\n:36\n", exact: true, callback: true},
		{name: "partial-env", args: []string{"__completeNoDesc", "cluster", "deploy", "--resource", ""}, base: `{}`, namespace: "env", want: "env-web\nenv-worker\n:36\n", exact: true, callback: true},
		{name: "partial-config", args: []string{"__complete", "cluster", "deploy", "--resource", ""}, base: `{"namespace":"base"}`, config: `{"namespace":"file"}`, want: "file-web\tWeb service\nfile-worker\n:36\n", exact: true, callback: true},
		{name: "partial-precedence", args: []string{"__complete", "cluster", "deploy", "--namespace", "cli", "--resource", ""}, base: `{"namespace":"base"}`, config: `{"namespace":"file"}`, namespace: "env", want: "cli-web\tWeb service\ncli-worker\n:36\n", exact: true, callback: true},
		{name: "unrelated-malformed-number", args: []string{"__complete", "cluster", "deploy", "--port", "bad", "--resource", ""}, base: `{}`, want: "default-web\tWeb service\ndefault-worker\n:36\n", exact: true, callback: true},
		{name: "independent-fact", args: []string{"__complete", "cluster", "deploy", "--port", "0", "--inspect", "invalid"}, base: `{}`, want: "invalid-port:.port\n:4\n", exact: true, callback: true},
		{name: "requested-mismatch", args: []string{"__complete", "cluster", "deploy", "-n", "dev", "--inspect", "mismatch"}, base: `{}`, want: "mismatch:.namespace\n:4\n", exact: true, callback: true},
		{name: "repeated-list", args: []string{"__complete", "cluster", "deploy", "--tags", "a", "--tags", "a", "--inspect", "tags"}, base: `{}`, want: "tags:2\n:4\n", exact: true, callback: true},
		{name: "env-list", args: []string{"__complete", "cluster", "deploy", "--inspect", "tags"}, base: `{}`, tags: "a,b", want: "tags:2\n:4\n", exact: true, callback: true},
		{name: "config-null", args: []string{"__complete", "cluster", "deploy", "--inspect", "null"}, base: `{"optional":null}`, want: "null-optional\n:4\n", exact: true, callback: true},
		{name: "positional", args: []string{"__complete", "cluster", "deploy", "--", "d"}, base: `{}`, want: "default-web\tWeb service\ndefault-worker\n:36\n", exact: true, callback: true},
		{name: "repeat-positional", args: []string{"__complete", "cluster", "deploy", "one", "d"}, base: `{}`, want: "default-web\tWeb service\ndefault-worker\n:36\n", exact: true, callback: true},
		{name: "bad-config", args: []string{"__complete", "cluster", "deploy", "--resource", ""}, base: `bad`, want: ":1\n", exact: true},
		{name: "unknown-config", args: []string{"__complete", "cluster", "deploy", "--resource", ""}, base: `{"unknown":1}`, want: ":1\n", exact: true},
		{name: "callback-error", args: []string{"__complete", "cluster", "deploy", "--resource", ""}, base: `{"namespace":false}`, want: ":1\n", exact: true, callback: true},
		{name: "help-no-config", args: []string{"cluster", "deploy", "--help"}, want: "Deploy"},
		{name: "script-no-config", args: []string{"completion", "bash"}, want: "__complete"},
		{name: "normal-handler", args: []string{"cluster", "deploy", "-n", "dev"}, base: `{}`, want: `namespace: "dev"`, handler: true},
		{name: "normal-validation", args: []string{"cluster", "deploy", "-n", "dev", "--port", "0"}, base: `{}`, want: "must be validPort", failure: true},
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
			configPath := ""
			if tt.config != "" {
				configPath = "config.json"
				if err := os.WriteFile(filepath.Join(dir, configPath), []byte(tt.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_DYNAMIC_NAMESPACE="+tt.namespace, "BORK_DYNAMIC_TAGS="+tt.tags, "BORK_DYNAMIC_CONFIG="+configPath)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if (err != nil) != tt.failure {
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, out, &stderr)
			}
			if tt.exact && string(out) != tt.want || !tt.exact && !strings.Contains(string(out), tt.want) {
				t.Errorf("stdout = %q, want %q; stderr: %s", out, tt.want, &stderr)
			}
			if strings.Contains(string(out), "stale") {
				t.Errorf("dynamic choices should replace static choices: %s", out)
			}
			for file, want := range map[string]bool{"callback.txt": tt.callback, "handler.txt": tt.handler} {
				data, err := os.ReadFile(filepath.Join(dir, file))
				if (err == nil) != want || want && string(data) != "closed" {
					t.Errorf("%s finalizer: data=%q err=%v, want=%v", file, data, err, want)
				}
			}
			if tt.want == ":1\n" && stderr.Len() == 0 {
				t.Error("completion failure lost its diagnostics")
			}
		})
	}
}

func TestCLIDynamicMetadataAndProtocol(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
use codec.Defaults
type Options = { value: Option[String] } derive (codec.Decode)
fn invalid(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error { cli.Suggestions { choices: [.{ value: "bad\nvalue" }] } }
fn main() {
 completion = cli.Completion { field: "value", suggest: invalid }
 println(cli.ParseDetailedWith[Options]("app", "", ["--value", "accepted"], completions: [completion], flags: [.{ field: "value", deprecated: "use replacement" }]))
 println(cli.ParseWith[Options]("app", "", ["--help"], completions: [completion, completion]))
 println(cli.ParseWith[Options]("app", "", ["--help"], completions: [completion.copy(field: "unknown")]))
 println(cli.ParseWith[Options]("app", "", ["--help"], completions: [completion], settings: .{ autoLong: false }))
 match (cli.ParseWith[Options]("app", "", ["__complete", "--value", ""], completions: [completion])) {
  help: cli.Help => { assert(help.text == ":1\n"); assert(help.diagnostics.contains("completion choices must be nonempty")); println("captured-error") }
  other => { panic(s"Expected error protocol, got ${other}") }
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
	var stderr bytes.Buffer
	cmd := exec.Command(exe)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s\n%s", err, out, &stderr)
	}
	for _, want := range []string{"Parsed {", "accepted", "warnings:", "use replacement", "duplicate CLI completion field", "unknown CLI completion field", "completion field needs an enabled flag or positional", "captured-error"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("explicit ParseWith leaked stderr: %s", &stderr)
	}
}

func TestCLIDynamicEffects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
use codec.Defaults
type Options = {} derive (codec.Decode)
fn partial() uses io: Options | cli.Error | cli.Help { cli.ParseWith[Options]("app", "", []) }
fn main() {}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "net") || !strings.Contains(err.Error(), "state") {
		t.Fatalf("ParseWith must charge closed callback effects: %v", err)
	}
}

func TestCLIDynamicRecordDecoders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/codec"
import "bork/cli"
use codec.Defaults
use cli.FieldTagsEncode
pred positive(port: Int) { port > 0 }
pred after(value: Int, start: Int) { value > start }
type Service = { port: Int where positive } derive (codec.Decode)
type Options = { service: Service cli { flatten: false }, start: Int, end: Int where after(start), suggest: Option[String] } derive (codec.Decode)
fn complete(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 service = match (request.partial.Get[Service]("service")) {
  value: Service => value
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
  _: cli.Missing => { return cli.Error { errors: [codec.DecodeError { path: ".service", message: "missing service" }] } }
 }
 end = match (request.partial.Get[Int]("end")) {
  value: Int => value
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
  _: cli.Missing => 0
 }
 cli.Suggestions { choices: [.{ value: s"${service.port}:${end}" }] }
}
fn main() {
 completions: List[cli.Completion] = [.{ field: "suggest", suggest: complete }]
 match (cli.ParseWith[Options]("app", "", ["__complete", "--service", "{\"port\":443}", "--end", "5", "--suggest", ""], completions: completions)) {
  help: cli.Help => { assert(help.text == "443:5\n:4\n"); println("partial-record") }
  other => { panic(s"Expected completion, got ${other}") }
 }
 match (cli.ParseWith[Options]("app", "", ["__complete", "--service", "{\"port\":0}", "--suggest", ""], completions: completions)) {
  help: cli.Help => { assert(help.text == ":1\n"); assert(help.diagnostics.contains(".service.port")); println("field-fact-error") }
  other => { panic(s"Expected completion error, got ${other}") }
 }
 match (cli.ParseWith[Options]("app", "", ["--service", "{\"port\":443}", "--start", "10", "--end", "5"], completions: completions)) {
  error: cli.Error => { assert(error.errors.filter(item => item.path == ".end").length() == 1); println("complete-record-error") }
  other => { panic(s"Expected relational error, got ${other}") }
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
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if string(out) != "partial-record\nfield-fact-error\ncomplete-record-error\n" {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestCLIDynamicTypeCheckWithoutGo(t *testing.T) {
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
use codec.Defaults
type Options = { value: String } derive (codec.Decode)
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[String]("value")) {
  value: String => cli.Suggestions { choices: [.{ value: value }] }
  _: cli.Missing => cli.Suggestions { choices: [] }
  error: codec.DecodeError => cli.Error { errors: [error] }
 }
}
fn main() { _ = cli.SubcommandWith[Options]("deploy", "", (options, s) => {}, completions: [.{ field: "value", suggest: suggest }]) }
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Check(root); err != nil {
		t.Fatalf("dynamic CLI checking without Go: %v", err)
	}
}

func TestCLIDynamicCapturedResources(t *testing.T) {
	t.Parallel()
	body := `file = match (fs.Open("catalog.txt", temporary)) {
  file: fs.File => file
  error: fs.Error => { panic(s"${error}") }
 }
 cli.SubcommandWith[Options]("deploy", "", (options, s) => {}, completions: [.{ field: "value", suggest: (request, s) => {
  _ = fs.ReadAllText(file)
  cli.Suggestions { choices: [] }
 } }])`
	for _, escapes := range []bool{false, true} {
		name := "within owner scope"
		main := "scope temporary { " + strings.Replace(body, " cli.SubcommandWith", " command = cli.SubcommandWith", 1) + "\n _ = cli.Dispatch(\"app\", \"\", [], [command]) }"
		if escapes {
			name = "after owner scope"
			main = "command = scope temporary { " + body + " }\n _ = cli.Dispatch(\"app\", \"\", [], [command])"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			source := "import \"bork/cli\"\nimport \"bork/codec\"\nimport \"bork/fs\"\nuse codec.Defaults\ntype Options = { value: String } derive (codec.Decode)\nfn main() { " + main + " }\n"
			if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err := Check(root)
			if escapes {
				if err == nil || !strings.Contains(err.Error(), "may be released") {
					t.Fatalf("escaped captured resource: %v", err)
				}
			} else if err != nil {
				t.Fatalf("live captured resource: %v", err)
			}
		})
	}
}
