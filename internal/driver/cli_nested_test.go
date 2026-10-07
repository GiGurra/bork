package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLINestedRecords(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/json"
import "bork/process"
use codec.Defaults
use cli.FieldTagsEncode
pred Positive(value: Int) { value > 0 }
pred Healthy(value: Db) { value.host != "blocked" || value.port == 443 }
type Db = { host: String = "localhost" codec { aliases: ["hostname"] }, port: Int where Positive = 5432 } where Healthy derive (codec.Decode, codec.Encode)
type Credentials = { user: String, enabled: Bool } derive (codec.Decode, codec.Encode)
type Empty = {} derive (codec.Decode, codec.Encode)
type Outer = { sibling: String = "default", credentials: Option[Credentials] } derive (codec.Decode, codec.Encode)
type Deep = { outer: Option[Outer] } derive (codec.Decode, codec.Encode)
type Groups = { credentials: Option[Credentials], empty: Empty } derive (codec.Decode, codec.Encode)
type Options = { db: Db codec { name: "database", aliases: ["oldDb"] }, optional: Option[Db] } derive (codec.Decode, codec.Encode)
type Mode = sealed { Quiet, Loud codec { aliases: ["verbose"] }, Other(String) codec { fallback: true } } derive (codec.Decode, codec.Encode)
type EnumDb = { level: Mode = Mode.Quiet, levels: List[Mode] = [] cli { collection: cli.Collection.Csv } } derive (codec.Decode, codec.Encode)
type EnumOptions = { db: EnumDb codec { name: "database" } } derive (codec.Decode, codec.Encode)
fn enumSuggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[Mode]("db.level")) {
  value: Mode => cli.Suggestions {choices:[.{value:json.Encode(value)}]}
  _: cli.Missing => cli.Suggestions {choices:[.{value:"missing"}]}
  error: codec.DecodeError => cli.Error {errors:[error]}
 }
}
type StrictDb = { host: String = "localhost" } codec { unknown: codec.Unknown.Reject } derive (codec.Decode, codec.Encode)
type StrictOptions = { db: StrictDb } derive (codec.Decode, codec.Encode)
type Opaque = { host: String } derive (codec.Encode)
instance decodeOpaque: codec.Decode[Opaque] {
 metadata codec.FieldSchema = codec.FieldSchema {kind: "string", optional: false}
 fn decode(input: codec.Value): Opaque | codec.DecodeError {
  match (input) { codec.Value.String {value} => Opaque {host: value}, _ => codec.DecodeError {path: "", message: "expected opaque string"} }
 }
}
type OpaqueOptions = { opaque: Option[Opaque] } derive (codec.Decode, codec.Encode)
type Leaf = { action: String = "show" } derive (codec.Decode)
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[String]("db.host")) {
  value: String => cli.Suggestions { choices: [.{ value: value }] }
  _: cli.Missing => cli.Suggestions { choices: [.{ value: "missing" }] }
  error: codec.DecodeError => cli.Error { errors: [error] }
 }
}
type Defaulted = { db: Db = Db {} } derive (codec.Decode)
type Whole = { db: Db = Db {} cli { flatten: false } } derive (codec.Decode, codec.Encode)
fn main() {
 args = process.Args()
 mode = args.head().getOr("normal")
 args = args.drop(1)
 if (mode == "enum") {
  match (cli.ParseWith[EnumOptions]("app", "Enum", args, completions:[.{field:"db.level",suggest:enumSuggest}])) {
   help: cli.Help => {println(help.text);eprintln(help.diagnostics)}
   value: EnumOptions => println(json.Encode(value))
   error: cli.Error => println(error.Render("app"))
  }
  return
 }
 if (mode == "deep") {
  println(cli.Parse[Deep]("app", "Deep", args))
  return
 }
 if (mode == "strict") {
  println(cli.Parse[StrictOptions]("app", "Strict", args, configFiles: ["unknown.json"]))
  return
 }
 if (mode == "opaque") {
  match (cli.Parse[OpaqueOptions]("app", "Opaque", args)) { value: OpaqueOptions => println(json.Encode(value)), other => println(other) }
  return
 }
 if (mode == "root") {
  command = cli.RootSubcommand[Options, Leaf]("leaf", "Leaf", (root, leaf, s) => { println(json.Encode(root)); println(leaf.action) })
  println(cli.DispatchRoot[Options]("app", "Root", args, [command]))
  return
 }
 if (mode == "shared") {
  one = cli.Subcommand[Options]("one", "One", (options, s) => println(json.Encode(options)))
  two = cli.Subcommand[Options]("two", "Two", (options, s) => println(json.Encode(options)))
  println(cli.Dispatch("app", "Shared", args, [one, two]))
  return
 }
 if (mode == "complete") {
  match (cli.ParseWith[Options]("app", "Complete", args, completions: [.{field: "db.host", suggest: suggest}])) {
   help: cli.Help => { println(help.text); eprintln(help.diagnostics) }
   other => println(other)
  }
  return
 }
 if (mode == "override") {
  println(cli.Parse[Options]("app", "Override", args, flags: [.{field: "db.host", long: cli.Mapping.Named {name: "hostname"}}]))
  return
 }
 if (mode == "groups") {
  match (cli.Parse[Groups]("app", "Groups", args)) {
   value: Groups => println(json.Encode(value))
   other => println(other)
  }
  return
 }
 if (mode == "defaulted") { println(cli.Parse[Defaulted]("app", "Nested", args, configFiles: ["missing.json"])); return }
 if (mode == "whole") { println(cli.Parse[Whole]("app", "Nested", args)); return }
 match (cli.Parse[Options]("app", "Nested", args, configFiles: if (mode == "config") { ["config.json"] } else if (mode == "null") { ["null.json"] } else if (mode == "overlaynull") { ["config.json", "null.json"] } else if (mode == "ignore") { ["ignored.json"] } else { [] }, settings: .{ autoEnv: true, envPrefix: "BORK_NESTED" })) {
  value: Options => println(json.Encode(value))
  error: cli.Error => println(error.Render("app"))
  help: cli.Help => println(help.text)
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"oldDb":{"hostname":"config","port":80},"optional":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "null.json"), []byte(`{"database":null}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "unknown.json"), []byte(`{"db":{"unknown":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.json"), []byte(`{"database":{"unknown":1}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mode              string
		args, env, want, absent []string
	}{
		{"nested enum alias", "enum", []string{"--database-level", "verbose"}, nil, []string{`"level":"LOUD"`}, nil},
		{"nested enum unknown wire path", "enum", []string{"--database-level", "future"}, nil, []string{".database.level", "must be one of"}, nil},
		{"nested enum csv names", "enum", []string{"--database-levels", "QUIET,verbose"}, nil, []string{`"levels":["QUIET","LOUD"]`}, nil},
		{"nested enum csv unknown", "enum", []string{"--database-levels", "QUIET,future"}, nil, []string{".database.levels[1]", "must be one of"}, nil},
		{"nested enum partial unknown retained", "enum", []string{"__completeNoDesc", "--database-level", "future", "--database-level", ""}, nil, []string{".database.level", "must be one of", ":1"}, []string{"missing"}},
		{"optional required group absent", "groups", nil, nil, []string{`"credentials":null`, `"empty":{}`}, nil},
		{"optional required group complete", "groups", []string{"--credentials-user", "Ada"}, nil, []string{`"credentials":{"user":"Ada","enabled":false}`}, nil},
		{"optional group missing required leaf", "groups", []string{"--credentials-enabled"}, nil, []string{`.credentials.user`, `is missing`}, nil},
		{"nested unknown ignore remains decoder policy", "ignore", nil, nil, []string{`"database":{"host":"localhost","port":5432}`}, []string{"unknown CLI config"}},
		{"nested unknown reject remains decoder policy", "strict", nil, nil, []string{".db.unknown", "unknown"}, nil},
		{"deep optional help requirement conditional", "deep", []string{"--help"}, nil, []string{"required when outer.credentials is set"}, []string{"required when outer is set"}},
		{"deep optional sibling activation", "deep", []string{"--outer-sibling", "active"}, nil, []string{`sibling: "active"`, `credentials: Option.None`}, []string{"is missing"}},
		{"deep optional credentials activation", "deep", []string{"--outer-credentials-user", "Ada"}, nil, []string{`user: "Ada"`, `enabled: false`}, []string{"is missing"}},
		{"optional help requirement conditional", "groups", []string{"--help"}, nil, []string{"required when credentials is set"}, []string{"(required)"}},
		{"selected custom decoder remains opaque", "opaque", []string{"--opaque", "literal"}, nil, []string{`"opaque":{"host":"literal"}`}, nil},
		{"explicit dotted override", "override", []string{"--hostname", "renamed"}, nil, []string{"renamed"}, nil},
		{"shared option record one", "shared", []string{"one", "--database-host", "one"}, nil, []string{`"host":"one"`}, nil},
		{"shared option record two", "shared", []string{"two", "--database-host", "two"}, nil, []string{`"host":"two"`}, nil},
		{"persistent nested flags", "root", []string{"--database-host", "root", "leaf", "--optional-port", "80"}, nil, []string{`"host":"root"`, `"optional":{"host":"localhost","port":80}`, "show"}, nil},
		{"nested partial missing", "complete", []string{"__completeNoDesc", "--database-host", ""}, nil, []string{"missing"}, nil},
		{"nested partial supplied", "complete", []string{"__completeNoDesc", "--database-host", "supplied", "--database-host", ""}, nil, []string{"supplied"}, nil},
		{"leaf defaults", "normal", nil, nil, []string{`"database":{"host":"localhost","port":5432}`, `"optional":null`}, nil},
		{"nested cli", "normal", []string{"--database-host", "cli", "--database-port", "80"}, nil, []string{`"database":{"host":"cli","port":80}`}, nil},
		{"optional group activation", "normal", []string{"--optional-host", "optional"}, nil, []string{`"optional":{"host":"optional","port":5432}`}, nil},
		{"nested aliases", "normal", []string{"--old-db-hostname", "alias"}, nil, []string{`"host":"alias"`}, nil},
		{"nested env", "normal", nil, []string{"BORK_NESTED_DATABASE_HOST=env"}, []string{`"host":"env"`}, nil},
		{"required group null preserves decoder rejection", "null", nil, nil, []string{".database", "null"}, nil},
		{"later null overlay preserves decoder rejection", "overlaynull", nil, nil, []string{".database", "null"}, nil},
		{"child overrides null group", "null", []string{"--database-host", "cli"}, nil, []string{`"database":{"host":"cli","port":5432}`}, nil},
		{"nested config aliases", "config", nil, nil, []string{`"database":{"host":"config","port":80}`}, nil},
		{"child override", "config", []string{"--database-host", "cli"}, nil, []string{`"database":{"host":"cli","port":80}`}, nil},
		{"nested record fact", "normal", []string{"--database-host", "blocked", "--database-port", "80"}, nil, []string{".database", "Healthy"}, nil},
		{"leaf fact", "normal", []string{"--database-port", "0"}, nil, []string{".database.port", "Positive"}, nil},
		{"parent default help before reads", "defaulted", []string{"--help"}, nil, []string{"--db-host", "--db-port"}, []string{"missing.json", "parent default"}},
		{"whole json opt out", "whole", []string{"--db", `{"host":"whole","port":80}`}, nil, []string{"whole", "80"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(exe, append([]string{tt.mode}, tt.args...)...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), tt.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(out), want) {
					t.Fatalf("output %q lacks %q", out, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(string(out), absent) {
					t.Fatalf("output %q includes %q", out, absent)
				}
			}
		})
	}
}
