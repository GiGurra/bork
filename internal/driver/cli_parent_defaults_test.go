package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIParentDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
pred Healthy(value: Db) { value.host != "blocked" || value.port == 443 }
type Db = { host: String = "leaf", port: Int = 1, enabled: Bool, label: Option[String] = .None } where Healthy derive(codec.Decode)
type Required = { user: String, enabled: Bool } derive(codec.Decode)
type Layer = { db: Db = .{enabled:false} } derive(codec.Decode)
type OptionalLayer = {name:String="leaf-name",db:Option[Db]=.Some(.{host:"inner-parent",port:999,enabled:true})}derive(codec.Decode)
type Options = {
 db: Db = .{host:"parent",port:443,enabled:true,label:.Some("label")} codec { name:"database", aliases:["oldDb"] }
 some: Option[Db] = .Some(.{host:"optional",port:80,enabled:true})
 none: Option[Db] = .None
 required: Required = .{user:"parent-user",enabled:true}
 layer: Layer = .{db:.{host:"deep",port:42,enabled:true}}
 nested:OptionalLayer=.{name:"outer-parent",db:.Some(.{host:"outer-db",port:443,enabled:true})}
} derive(codec.Decode)
type Leaf = { action: String = "show" } derive(codec.Decode)
fn suggest(request:cli.CompletionRequest,s:Scope):cli.Suggestions|cli.Error {
 match(request.partial.Get[String]("db.host")) {
  value:String => cli.Suggestions{choices:[.{value:value}]}
  _:cli.Missing => cli.Suggestions{choices:[.{value:"missing"}]}
  error:codec.DecodeError => cli.Error{errors:[error]}
 }
}
fn main() {
 args=process.Args()
 mode=args.head().getOr("normal")
 args=args.drop(1)
 if(mode=="shared") {
  one=cli.Subcommand[Options]("one","One",(options,s)=>println(options))
  two=cli.Subcommand[Options]("two","Two",(options,s)=>println(options))
  if(args.isEmpty()) {
   println(cli.Dispatch("app","Shared",["one","--database-host","changed","--layer-db-port","99"],[one,two]))
   println(cli.Dispatch("app","Shared",["two"],[one,two]))
  }else{println(cli.Dispatch("app","Shared",args,[one,two]))}
  return
 }
 if(mode=="root") {
  child=cli.RootSubcommand[Options,Leaf]("leaf","Leaf",(root,leaf,s)=>println(root))
  println(cli.DispatchRoot[Options]("app","Root",args,[child]))
  return
 }
 files:List[String]=if(mode=="config"){["base.json","overlay.json"]}else if(mode=="null"){["null.json"]}else if(mode=="later"){["first-null.json","later-object.json"]}else if(mode=="deepnull"){["deep-null.json"]}else{[]}
 match(cli.ParseWith[Options]("app","Defaults",args,configFiles:files,settings:.{autoEnv:true,envPrefix:"BORK_PARENT"},completions:[.{field:"db.host",suggest:suggest}])) {
  help:cli.Help=>{println(help.text);eprintln(help.diagnostics)}
  other=>println(other)
 }
}`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"base.json":         `{"oldDb":{"host":"base","port":80},"some":{"host":"configured"}}`,
		"overlay.json":      `{"database":{"host":"config"}}`,
		"null.json":         `{"some":null,"database":null,"required":null}`,
		"first-null.json":   `{"database":null}`,
		"later-object.json": `{"database":{"host":"reactivated"}}`,
		"deep-null.json":    `{"nested":null}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mode              string
		args, env, want, absent []string
	}{
		{"baseline", "normal", nil, nil, []string{`host: "parent", port: 443, enabled: true`, `host: "optional", port: 80, enabled: true`, `none: Option.None`, `user: "parent-user", enabled: true`, `host: "deep", port: 42, enabled: true`}, nil},
		{"child overlay", "normal", []string{"--database-port", "80"}, nil, []string{`host: "parent", port: 80, enabled: true`, `Option.Some("label")`}, nil},
		{"required sibling preserved", "normal", []string{"--required-enabled=false"}, nil, []string{`user: "parent-user", enabled: false`}, nil},
		{"optional some overlay", "normal", []string{"--some-port", "81"}, nil, []string{`host: "optional", port: 81, enabled: true`}, nil},
		{"optional none activation", "normal", []string{"--none-port", "81"}, nil, []string{`host: "leaf", port: 81, enabled: false`}, nil},
		{"deep inherited default", "normal", []string{"--layer-db-port", "43"}, nil, []string{`host: "deep", port: 43, enabled: true`}, nil},
		{"alias overlay", "normal", []string{"--old-db-host", "alias"}, nil, []string{`host: "alias", port: 443, enabled: true`}, nil},
		{"config overlays baseline", "config", nil, nil, []string{`host: "config", port: 443, enabled: true`, `host: "configured", port: 80, enabled: true`}, nil},
		{"env overlays config", "config", nil, []string{"BORK_PARENT_DATABASE_HOST=env"}, []string{`host: "env", port: 443, enabled: true`}, nil},
		{"cli overlays env", "config", []string{"--database-host", "cli"}, []string{"BORK_PARENT_DATABASE_HOST=env"}, []string{`host: "cli", port: 443, enabled: true`}, nil},
		{"later object retains null reset", "later", nil, nil, []string{`host: "reactivated", port: 1, enabled: false`}, nil},
		{"ancestor reset suppresses optional record default", "deepnull", []string{"--nested-name", "reactivated"}, nil, []string{`name: "reactivated", db: Option.None`}, []string{`inner-parent`, `outer-db`}},
		{"ancestor reset reactivated descendant uses leaf defaults", "deepnull", []string{"--nested-db-host", "reactivated"}, nil, []string{`name: "leaf-name", db: Option.Some(Db { host: "reactivated", port: 1, enabled: false`}, nil},
		{"parent fact", "normal", []string{"--database-host", "blocked", "--database-port", "80"}, nil, []string{"Healthy"}, nil},
		{"null clears baseline", "null", []string{"--database-host", "reactivated", "--required-user", "fresh"}, nil, []string{`host: "reactivated", port: 1, enabled: false`, `some: Option.None`, `user: "fresh", enabled: false`}, nil},
		{"null removes required sibling", "null", []string{"--database-host", "reactivated", "--required-enabled"}, nil, []string{".required.user", "is missing"}, nil},
		{"optional null reactivated", "null", []string{"--database-host", "fresh", "--required-user", "fresh", "--some-host", "fresh"}, nil, []string{`host: "fresh", port: 1, enabled: false`}, []string{`port: 80`}},
		{"completion omits baseline", "normal", []string{"__completeNoDesc", "--database-host", ""}, nil, []string{"missing"}, []string{"parent\n"}},
		{"shared schema remains immutable", "shared", nil, nil, []string{`host: "changed"`, `host: "parent", port: 443, enabled: true`, `host: "deep", port: 42, enabled: true`}, nil},
		{"shared options one", "shared", []string{"one", "--database-host", "one"}, nil, []string{`host: "one", port: 443, enabled: true`}, nil},
		{"shared options two", "shared", []string{"two", "--database-port", "80"}, nil, []string{`host: "parent", port: 80, enabled: true`}, nil},
		{"root options", "root", []string{"--database-host", "root", "leaf"}, nil, []string{`host: "root", port: 443, enabled: true`}, nil},
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
