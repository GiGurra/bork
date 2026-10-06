package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLICollectionModes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/json"
import "bork/process"
use codec.Defaults
use cli.FieldTagsEncode
type Leaf = { name: String } derive (codec.Decode)
pred Positive(value: Int) { value > 0 }
type PositiveInt = Int where Positive
pred PositivePorts(value: Option[Map[String, Int]]) { value.map(ports => ports.entries().all(entry => entry.value > 0)).getOr(true) }
type Options = {
 tags: List[String] = [] cli { collection: cli.Collection.Csv } codec { aliases: ["oldTags"] }
 counts: List[PositiveInt] = []
 labels: Option[Map[String, String]] cli { collection: cli.Collection.KeyValue } codec { aliases: ["oldLabels"] }
 ports: Option[Map[String, Int]] where PositivePorts
 switches: Option[Map[String, Bool]]
 complex: Option[Map[String, List[String]]]
} derive (codec.Decode, codec.Encode)
type Renamed = { ports: Option[Map[String, Int]] codec { name: "listeners" } cli { collection: cli.Collection.KeyValue } } derive (codec.Decode)
fn namedSuggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 match (request.partial.Get[Option[Map[String, Int]]]("ports")) {
  error: codec.DecodeError => cli.Error { errors: [error] }
  _ => cli.Suggestions { choices: [.{ value: "missing" }] }
 }
}
fn ignore(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 cli.Suggestions { choices: [.{ value: "okay" }] }
}
fn suggest(request: cli.CompletionRequest, s: Scope): cli.Suggestions | cli.Error {
 tags = match (request.partial.Get[List[String]]("tags")) {
  value: List[String] => json.Encode(value)
  _: cli.Missing => "missing"
  error: codec.DecodeError => { return cli.Error { errors: [error] } }
 }
 cli.Suggestions { choices: [.{ value: tags }] }
}
fn main() {
 args = process.Args()
 mode = args.head().getOr("normal")
 args = args.drop(1)
 enrich: cli.Enricher = (previous, field) => {
  if (mode == "repeat") { field.copy(collection: cli.Collection.Repeat) } else if (mode == "tagged") { field } else if (field.field == "tags" || field.field == "counts") { field.copy(collection: cli.Collection.Csv) } else if (field.field == "labels" || field.field == "ports" || field.field == "switches" || mode == "badmap" && field.field == "complex" || mode == "badlist" && field.field == "complex") {
   field.copy(collection: if (mode == "badlist") { cli.Collection.Csv } else { cli.Collection.KeyValue })
  } else { field }
 }
 flags: List[cli.Flag] = if (mode == "position") { [.{ field: "tags", positional: true }] } else if (mode == "mapposition") { [.{ field: "labels", positional: true }] } else { [] }
 if (mode == "renamed") {
  match (cli.ParseWith[Renamed]("app", "Renamed", args, completions: [.{ field: "ports", suggest: namedSuggest }])) {
   help: cli.Help => { println(help.text); eprintln(help.diagnostics) }
   other => println(other)
  }
  return
 }
 if (mode == "root") {
  command = cli.RootSubcommand[Options, Leaf]("leaf", "Leaf", (root, leaf, s) => { println(json.Encode(root)); println(leaf.name) })
  println(cli.DispatchRoot[Options]("app", "Root", args, [command], settings: .{ enrichers: [enrich] }))
  return
 }
 result = cli.ParseWith[Options]("app", "Collections", args, flags: flags, configFiles: if (mode == "config") { ["config.json"] } else if (mode == "badmap" || mode == "badlist") { ["missing.json"] } else { [] },
  settings: .{ enrichers: [enrich], autoEnv: true, envPrefix: "BORK_COLLECTION" }, completions: [.{ field: "counts", suggest: suggest }, .{ field: "labels", suggest: ignore }])
 match (result) {
  options: Options => println(json.Encode(options))
  help: cli.Help => { println(help.text); eprintln(help.diagnostics) }
  error: cli.Error => println(error.Render("app"))
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"tags":["config"],"labels":{"a":"config"},"ports":{"http":80}}`), 0o644); err != nil {
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
		{"persistent collections before and after path", "root", []string{"--tags", "one,two", "leaf", "--labels", "a=one,b=two", "--name", "Ada"}, nil, []string{`"tags":["one","two"]`, `"labels":{"a":"one","b":"two"}`, "Ada"}, nil},
		{"collection help labels", "normal", []string{"--help"}, nil, []string{"--labels key=value", "--ports key=value", "--switches key=value"}, nil},
		{"inherited collection help labels", "root", []string{"leaf", "--help"}, nil, []string{"--labels key=value", "--ports key=value"}, nil},
		{"typed tags drive collection modes", "tagged", []string{"--tags", "one,two", "--labels", "a=one,b=two"}, nil, []string{`"tags":["one","two"]`, `"labels":{"a":"one","b":"two"}`}, nil},
		{"alias cli collection", "tagged", []string{"--old-tags", "one,two", "--old-labels", "a=one,b=two"}, nil, []string{`"tags":["one","two"]`, `"labels":{"a":"one","b":"two"}`}, nil},
		{"quoted alias env collection", "tagged", nil, []string{`BORK_COLLECTION_OLD_TAGS="one,two",three`, `BORK_COLLECTION_OLD_LABELS="a=one,two",b=three`}, []string{`"tags":["one,two","three"]`, `"labels":{"a":"one,two","b":"three"}`}, nil},
		{"csv repeated", "normal", []string{"--tags", "one,two", "--tags", "three"}, nil, []string{`"tags":["one","two","three"]`}, nil},
		{"csv quoted commas", "normal", []string{"--tags", `"one,two",three`}, nil, []string{`"tags":["one,two","three"]`}, nil},
		{"empty csv", "normal", []string{"--tags", ""}, nil, []string{`"tags":[]`}, nil},
		{"numeric csv", "normal", []string{"--counts", "1,2", "--counts", "3"}, nil, []string{`"counts":[1,2,3]`}, nil},
		{"value facts", "normal", []string{"--counts", "1,-2"}, nil, []string{"counts", "Positive"}, nil},
		{"literal repeat default", "repeat", []string{"--tags", "one,two"}, nil, []string{`"tags":["one,two"]`}, nil},
		{"map string repeat", "normal", []string{"--labels", "a=one,b=two", "--labels", "c=three=four"}, nil, []string{`"labels":{"a":"one","b":"two","c":"three=four"}`}, nil},
		{"map number bool", "normal", []string{"--ports", "http=80,https=443", "--switches", "trace=true,debug=false"}, nil, []string{`"ports":{"http":80,"https":443}`, `"switches":{"trace":true,"debug":false}`}, nil},
		{"duplicate map within flag", "normal", []string{"--labels", "a=one,a=two"}, nil, []string{"duplicate map key", "a"}, nil},
		{"duplicate map across flags", "normal", []string{"--labels", "a=one", "--labels", "a=two"}, nil, []string{"duplicate map key", "a"}, nil},
		{"malformed pair", "normal", []string{"--labels", "no-equals"}, nil, []string{"key=value"}, nil},
		{"empty key", "normal", []string{"--labels", "=value"}, nil, []string{"nonempty key"}, nil},
		{"empty value", "normal", []string{"--labels", "key="}, nil, []string{`"labels":{"key":""}`}, nil},
		{"empty map provided", "normal", []string{"--labels", ""}, nil, []string{`"labels":{}`}, nil},
		{"map value fact", "normal", []string{"--ports", "http=0"}, nil, []string{"ports", "Positive"}, nil},
		{"quoted csv env", "normal", nil, []string{`BORK_COLLECTION_TAGS="one,two",three`, `BORK_COLLECTION_LABELS="a=one,two",b=three`}, []string{`"tags":["one,two","three"]`, `"labels":{"a":"one,two","b":"three"}`}, nil},
		{"multiline flag rejected", "normal", []string{"--labels", "a=one\nb=two"}, nil, []string{"CSV input must contain one record"}, nil},
		{"multiline env rejected", "normal", nil, []string{"BORK_COLLECTION_TAGS=one\ntwo"}, []string{"CSV input must contain one record"}, nil},
		{"csv env", "normal", nil, []string{"BORK_COLLECTION_TAGS=one,two", "BORK_COLLECTION_LABELS=a=env,b=two"}, []string{`"tags":["one","two"]`, `"labels":{"a":"env","b":"two"}`}, nil},
		{"cli over env", "normal", []string{"--labels", "a=cli"}, []string{"BORK_COLLECTION_LABELS=broken"}, []string{`"labels":{"a":"cli"}`}, []string{"key=value"}},
		{"config objects unchanged", "config", nil, nil, []string{`"tags":["config"]`, `"labels":{"a":"config"}`, `"ports":{"http":80}`}, nil},
		{"config whole field override", "config", []string{"--labels", "b=cli"}, nil, []string{`"labels":{"b":"cli"}`}, []string{`"a":"config"`}},
		{"csv positionals", "position", []string{`"one,two",three`, "four,five"}, nil, []string{`"tags":["one,two","three","four","five"]`}, nil},
		{"map positionals", "mapposition", []string{"a=one,b=two", "c=three"}, nil, []string{`"labels":{"a":"one","b":"two","c":"three"}`}, nil},
		{"complex map remains json", "normal", []string{"--complex", `{"a":["one","two"]}`}, nil, []string{`"complex":{"a":["one","two"]}`}, nil},
		{"incompatible map before reads", "badmap", []string{"--help"}, nil, []string{"KeyValue collection requires"}, []string{"missing.json"}},
		{"incompatible csv before reads", "badlist", []string{"--help"}, nil, []string{"Csv collection requires"}, []string{"missing.json"}},
		{"partial csv values", "normal", []string{"__completeNoDesc", "--tags", "one,two", "--counts", ""}, nil, []string{`["one","two"]`}, nil},
		{"renamed map error path", "renamed", []string{"--listeners", "a=1,a=2"}, nil, []string{".listeners", "duplicate map key"}, []string{".ports"}},
		{"renamed map partial error retained", "renamed", []string{"__completeNoDesc", "--listeners", "a=1,a=2", "--listeners", ""}, nil, []string{".listeners", "duplicate map key", ":1"}, []string{"missing"}},
		{"unrelated csv failure allows completer", "normal", []string{"__completeNoDesc", "--tags", `"bad`, "--labels", ""}, nil, []string{"okay"}, []string{"invalid CSV"}},
		{"requested csv failure retained", "normal", []string{"__completeNoDesc", "--tags", `"bad`, "--counts", ""}, nil, []string{"invalid CSV", ":1"}, nil},
		{"partial missing", "normal", []string{"__completeNoDesc", "--counts", ""}, nil, []string{"missing"}, nil},
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
			text := string(out)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("output %q lacks %q", text, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(text, absent) {
					t.Fatalf("output %q includes %q", text, absent)
				}
			}
		})
	}
}
