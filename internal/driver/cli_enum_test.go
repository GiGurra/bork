package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIEnumChoices(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/json"
import "bork/process"
use codec.Defaults
type Mode = sealed {
 // Run quietly.
 Quiet codec { name: "quiet", aliases: ["q"] },
 // Verbose output.
 // Includes diagnostics.
 Loud codec { name: "LOUD", aliases: ["verbose"] },
 Other(String) codec { fallback: true }
} derive (codec.Decode, codec.Encode)
pred QuietOnly(value: Mode) { value is Mode.Quiet }
type Options = {
 level: Mode = Mode.Quiet
 optional: Option[Mode]
 levels: List[Mode] = []
 checked: Mode where QuietOnly = Mode.Quiet
 fallback: Mode = Mode.Other("future-default")
} derive (codec.Decode, codec.Encode)
type RenamedOptions = { level: Mode = Mode.Quiet codec { name: "severity" }, optional: Option[Mode] } derive (codec.Decode)
type NullableOptions = { groups: List[List[Option[Mode]]] = [] } derive (codec.Decode, codec.Encode)
type NestedOptions = { groups: List[List[Mode]] = [] } derive (codec.Decode, codec.Encode)
type Leaf = { name: String = "leaf" } derive (codec.Decode)
type Opaque = sealed { Alpha, Beta } derive (codec.Encode)
instance opaqueDecode: codec.Decode[Opaque] {
 metadata codec.FieldSchema = codec.FieldSchema {kind: "string", optional: false}
 fn decode(input: codec.Value): Opaque | codec.DecodeError { Opaque.Alpha }
}
type OpaqueOptions = { value: Opaque } derive (codec.Decode, codec.Encode)
type Advertised = { value: String } derive (codec.Encode)
instance advertisedDecode: codec.Decode[Advertised] {
 metadata codec.FieldSchema = codec.FieldSchema {kind: "string", optional: false, variants: [codec.VariantSchema {name: "one", doc: "One value", aliases: ["alias"]}]}
 fn decode(input: codec.Value): Advertised | codec.DecodeError {
  match (input) { codec.Value.String {value} => Advertised {value: value}, _ => codec.DecodeError {path: "", message: "expected string"} }
 }
}
type AdvertisedOptions = { value: Advertised } derive (codec.Decode, codec.Encode)
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[Mode]("level")) {
  value: Mode => cli.Suggestions { choices: [.{value: match (json.Encode(value)) { text: String => text; error: json.JsonError => toString(error) }}] }
  _: cli.Missing => cli.Suggestions { choices: [.{value: "missing"}] }
  error: codec.DecodeError => cli.Error {errors: [error]}
 }
}
fn nestedSuggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[List[List[Mode]]]("groups")) {
  value: List[List[Mode]] => cli.Suggestions {choices:[.{value:match (json.Encode(value)) { text: String => text; error: json.JsonError => toString(error) }}]}
  _: cli.Missing => cli.Suggestions {choices:[.{value:"missing"}]}
  error: codec.DecodeError => cli.Error {errors:[error]}
 }
}
fn main() {
 args = process.Args()
 mode = args.head().getOr("normal")
 args = args.drop(1)
 if (mode == "renamed" || mode == "renamedpartial") {
  match (cli.ParseWith[RenamedOptions]("app", "Renamed", args, completions:[.{field:"optional", suggest:suggest}])) { help: cli.Help => {println(help.text); eprintln(help.diagnostics)}, error: cli.Error => println(error.Render("app")), other => println(other) }
  return
 }
 if (mode == "nullable") {
  match (cli.Parse[NullableOptions]("app", "Nullable", args)) { value: NullableOptions => println(json.Encode(value)), other => println(other) }
  return
 }
 if (mode == "nestedpartial") {
  match (cli.ParseWith[NestedOptions]("app", "Nested partial", args, completions:[.{field:"groups", suggest:nestedSuggest}])) { help: cli.Help => {println(help.text); eprintln(help.diagnostics)}, other => println(other) }
  return
 }
 if (mode == "nested") {
  match (cli.Parse[NestedOptions]("app", "Nested", args)) { value: NestedOptions => println(json.Encode(value)), other => println(other) }
  return
 }
 if (mode == "opaque") { println(cli.Parse[OpaqueOptions]("app", "Opaque", args)); return }
 if (mode == "advertised") { println(cli.Parse[AdvertisedOptions]("app", "Advertised", args)); return }
 if (mode == "partial") {
  match (cli.ParseWith[Options]("app", "Partial", args, completions: [.{field:"optional", suggest:suggest}])) { help: cli.Help => {println(help.text); eprintln(help.diagnostics)}, other => println(other) }
  return
 }
 if (mode == "root") {
  command = cli.RootSubcommand[Options, Leaf]("leaf", "Leaf", (root, leaf, s) => println(json.Encode(root)))
  println(cli.DispatchRoot[Options]("app", "Root", args, [command]))
  return
 }
 flags: List[cli.Flag] = if (mode == "pos") { [.{field:"level", positional:true}] } else if (mode == "poslist") { [.{field:"levels", positional:true}] } else if (mode == "envhelp") { [.{field:"level", long:cli.Mapping.Disabled, env:"BORK_ENUM_LEVEL"}] } else { [] }
 files: List[String] = if (mode == "config") {["config.json"]} else if (mode == "yaml") {["config.yaml"]} else {[]}
 match (cli.Parse[Options]("app", "Enum choices", args, flags:flags, configFiles:files, settings:.{autoEnv:true,envPrefix:"BORK_ENUM", enrichers: if (mode == "relax") {[(previous, field) => field.copy(choices: [], strictChoices: false)]} else {[]}})) {
  value: Options => println(json.Encode(value))
  error: cli.Error => println(error.Render("app"))
  help: cli.Help => println(help.text)
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"level":"future","optional":"new","levels":["unknown","quiet"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("level: future\noptional: new\nlevels: [unknown, quiet]\n"), 0o644); err != nil {
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
		{"renamed enum unknown path", "renamed", []string{"--severity", "future"}, nil, []string{".severity", "must be one of"}, []string{".level"}},
		{"renamed enum partial unknown retained", "renamedpartial", []string{"__completeNoDesc", "--severity", "future", "--optional", ""}, nil, []string{".severity", "must be one of", ":1"}, []string{"missing"}},
		{"renamed enum partial alias", "renamedpartial", []string{"__completeNoDesc", "--severity", "verbose", "--optional", ""}, nil, []string{`"LOUD"`}, nil},
		{"nested enum object canonical", "nested", []string{"--groups", `[{"type":"quiet"}]`}, nil, []string{`"groups":[["quiet"]]`}, nil},
		{"nested enum object alias", "nested", []string{"--groups", `[{"type":"verbose"}]`}, nil, []string{`"groups":[["LOUD"]]`}, nil},
		{"nested enum object unknown", "nested", []string{"--groups", `[{"type":"future"}]`}, nil, []string{".groups[0][0].type", "must be one of"}, nil},
		{"nested enum object partial unknown", "nestedpartial", []string{"__completeNoDesc", "--groups", `[{"type":"future"}]`, "--groups", ""}, nil, []string{".groups[0][0].type", "must be one of", ":1"}, nil},
		{"nested enum object partial alias", "nestedpartial", []string{"__completeNoDesc", "--groups", `[{"type":"verbose"}]`, "--groups", ""}, nil, []string{`[["LOUD"]]`}, nil},
		{"nested nullable enum leaves", "nullable", []string{"--groups", `[null,"quiet"]`}, nil, []string{`"groups":[[null,"quiet"]]`}, nil},
		{"nested enum list names", "nested", []string{"--groups", `["quiet","verbose"]`}, nil, []string{`"groups":[["quiet","LOUD"]]`}, nil},
		{"nested enum list unknown index", "nested", []string{"--groups", `["future"]`}, nil, []string{".groups[0][0]", "must be one of"}, nil},
		{"nested enum list completion no bare names", "nested", []string{"__completeNoDesc", "--groups", ""}, nil, []string{":4"}, []string{"quiet\n", "LOUD\n"}},
		{"quoted json scalar no longer bare enum", "normal", []string{"--level", `"quiet"`}, nil, []string{".level", "must be one of"}, nil},
		{"canonical bare names", "normal", []string{"--level", "LOUD", "--optional", "quiet"}, nil, []string{`"level":"LOUD"`, `"optional":"quiet"`}, nil},
		{"exact aliases", "normal", []string{"--level", "verbose", "--optional", "q"}, nil, []string{`"level":"LOUD"`, `"optional":"quiet"`}, nil},
		{"case sensitive names", "normal", []string{"--level", "loud"}, nil, []string{".level", "must be one of: quiet, LOUD"}, nil},
		{"enum cannot relaxed by enricher", "relax", []string{"--level", "future"}, nil, []string{".level", "must be one of"}, nil},
		{"unknown fallback rejected cli", "normal", []string{"--level", "future"}, nil, []string{".level", "must be one of"}, nil},
		{"unknown fallback rejected env", "normal", nil, []string{"BORK_ENUM_LEVEL=future"}, []string{".level", "must be one of"}, nil},
		{"env enum list aliases", "normal", nil, []string{"BORK_ENUM_LEVELS=quiet,verbose"}, []string{`"levels":["quiet","LOUD"]`}, nil},
		{"env aliases", "normal", nil, []string{"BORK_ENUM_LEVEL=verbose", "BORK_ENUM_OPTIONAL=q"}, []string{`"level":"LOUD"`, `"optional":"quiet"`}, nil},
		{"list enums repeated", "normal", []string{"--levels", "quiet", "--levels", "verbose"}, nil, []string{`"levels":["quiet","LOUD"]`}, nil},
		{"list enum unknown index", "normal", []string{"--levels", "quiet", "--levels", "future"}, nil, []string{".levels[1]", "must be one of"}, nil},
		{"positional enum alias", "pos", []string{"verbose"}, nil, []string{`"level":"LOUD"`}, nil},
		{"positional unknown", "pos", []string{"future"}, nil, []string{".level", "must be one of"}, nil},
		{"list positional enum", "poslist", []string{"quiet", "verbose"}, nil, []string{`"levels":["quiet","LOUD"]`}, nil},
		{"config fallback preserved", "config", nil, nil, []string{`"level":"future"`, `"optional":"new"`, `"levels":["unknown","quiet"]`}, nil},
		{"yaml fallback preserved", "yaml", nil, nil, []string{`"level":"future"`, `"optional":"new"`}, nil},
		{"default fallback preserved", "normal", nil, nil, []string{`"fallback":"future-default"`}, nil},
		{"cli over config", "config", []string{"--level", "verbose"}, nil, []string{`"level":"LOUD"`}, nil},
		{"field facts", "normal", []string{"--checked", "LOUD"}, nil, []string{".checked", "QuietOnly"}, nil},
		{"help canonical choices", "normal", []string{"--help"}, nil, []string{"choices quiet, LOUD"}, []string{"choices q,", "choices verbose,", "choices Other"}},
		{"env only choices", "envhelp", []string{"--help"}, nil, []string{"BORK_ENUM_LEVEL string", "choices quiet, LOUD"}, nil},
		{"completion canonical docs", "normal", []string{"__complete", "--level", ""}, nil, []string{"quiet\tRun quietly.", "LOUD\tVerbose output. Includes diagnostics."}, []string{"verbose\t", "Other\t"}},
		{"list completion canonical docs", "normal", []string{"__complete", "--levels", ""}, nil, []string{"quiet\tRun quietly.", "LOUD\tVerbose output. Includes diagnostics."}, []string{"verbose\t"}},
		{"positional completion canonical docs", "pos", []string{"__complete", ""}, nil, []string{"quiet\tRun quietly.", "LOUD\tVerbose output. Includes diagnostics."}, []string{"verbose\t"}},
		{"completion prefix", "normal", []string{"__completeNoDesc", "--level", "L"}, nil, []string{"LOUD"}, []string{"quiet\n"}},
		{"partial supplied alias", "partial", []string{"__completeNoDesc", "--level", "verbose", "--optional", ""}, nil, []string{`"LOUD"`}, nil},
		{"partial unknown retained", "partial", []string{"__completeNoDesc", "--level", "future", "--optional", ""}, nil, []string{".level", "must be one of", ":1"}, nil},
		{"partial omitted stays missing", "partial", []string{"__completeNoDesc", "--optional", ""}, nil, []string{"missing"}, nil},
		{"persistent enum flags", "root", []string{"--level", "verbose", "leaf"}, nil, []string{`"level":"LOUD"`}, nil},
		{"persistent enum unknown", "root", []string{"leaf", "--level", "future"}, nil, []string{".level", "must be one of"}, nil},
		{"custom enum decoder opaque", "opaque", []string{"--value", "anything"}, nil, []string{"Opaque.Alpha"}, nil},
		{"custom advertised alias accepted", "advertised", []string{"--value", "alias"}, nil, []string{`value: "alias"`}, nil},
		{"custom advertised names restrict", "advertised", []string{"--value", "anything"}, nil, []string{"must be one of: one"}, nil},
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
