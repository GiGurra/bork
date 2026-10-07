package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIParentDefaultSources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
type Leaf = { required:String, maybe:Option[String], count:Int=1, enabled:Bool } derive(codec.Decode)
type Nested = { name:String="leaf", child:Option[Leaf]=.Some(.{required:"inner",maybe:.None,enabled:true}) } derive(codec.Decode)
type Options = {
 root:Leaf=.{required:"parent",maybe:.Some("parent"),count:9,enabled:true} codec {name:"database"}
 some:Option[Leaf]=.Some(.{required:"some",maybe:.None,enabled:true})
 none:Option[Leaf]=.None
 nested:Nested=.{name:"outer",child:.Some(.{required:"outer-child",maybe:.None,enabled:true})}
} derive(codec.Decode)
fn main() {
 match(cli.ParseResolved[Options]("app","Defaults",process.Args(),configFiles:["base.json","overlay.json"],settings:.{autoEnv:true,envPrefix:"BORK_PARENT_SOURCE"})) {
  resolved:cli.Resolved[Options]=>{
   println(resolved.value)
   for(field in ["root.required","root.maybe","root.count","root.enabled","some.required","some.maybe","none.required","none.count","nested.name","nested.child.required","nested.child.count"]) { println(s"${field}=${resolved.Source(field)}") }
  }
  other=>println(other)
 }
}`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, base, overlay string
		args, env, want     []string
	}{
		{name: "baseline", want: []string{"root.required=Option.Some(Source.Default)", "root.maybe=Option.Some(Source.Default)", "root.count=Option.Some(Source.Default)", "root.enabled=Option.Some(Source.Default)", "some.required=Option.Some(Source.Default)", "none.required=Option.Some(Source.Absent)", "none.count=Option.Some(Source.Absent)", "nested.child.required=Option.Some(Source.Default)"}},
		{name: "config", base: `{"database":{"required":"config"}}`, want: []string{`root.required=Option.Some(Source.Config { path: "base.json" })`, "root.maybe=Option.Some(Source.Default)"}},
		{name: "env", base: `{"database":{"required":"config"}}`, env: []string{"BORK_PARENT_SOURCE_DATABASE_REQUIRED=env"}, want: []string{`root.required=Option.Some(Source.Env { name: "BORK_PARENT_SOURCE_DATABASE_REQUIRED" })`, "root.maybe=Option.Some(Source.Default)"}},
		{name: "flag", base: `{"database":{"required":"config"}}`, env: []string{"BORK_PARENT_SOURCE_DATABASE_REQUIRED=env"}, args: []string{"--database-required", "flag"}, want: []string{`root.required=Option.Some(Source.Flag { name: "database-required" })`, "root.maybe=Option.Some(Source.Default)"}},
		{name: "reset", base: `{"database":null,"some":null}`, args: []string{"--database-required", "fresh"}, want: []string{"maybe: Option.None, count: 1, enabled: false", `root.required=Option.Some(Source.Flag { name: "database-required" })`, "root.maybe=Option.Some(Source.Absent)", "root.count=Option.Some(Source.Default)", "root.enabled=Option.Some(Source.Default)", "some.required=Option.Some(Source.Absent)", "some.maybe=Option.Some(Source.Absent)"}},
		{name: "persistent reset", base: `{"database":null}`, overlay: `{"database":{"required":"later"}}`, want: []string{"maybe: Option.None, count: 1, enabled: false", `root.required=Option.Some(Source.Config { path: "overlay.json" })`, "root.maybe=Option.Some(Source.Absent)", "root.count=Option.Some(Source.Default)", "root.enabled=Option.Some(Source.Default)"}},
		{name: "none activation", args: []string{"--none-required", "fresh"}, want: []string{`none.required=Option.Some(Source.Flag { name: "none-required" })`, "none.count=Option.Some(Source.Default)"}},
		{name: "ancestor reset", base: `{"nested":null}`, args: []string{"--nested-name", "fresh"}, want: []string{"child: Option.None", "nested.child.required=Option.Some(Source.Absent)", "nested.child.count=Option.Some(Source.Absent)"}},
		{name: "descendant activation after reset", base: `{"nested":null}`, args: []string{"--nested-child-required", "fresh"}, want: []string{"nested.name=Option.Some(Source.Default)", `nested.child.required=Option.Some(Source.Flag { name: "nested-child-required" })`, "nested.child.count=Option.Some(Source.Default)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runDir := t.TempDir()
			for name, data := range map[string]string{"base.json": tt.base, "overlay.json": tt.overlay} {
				if data == "" {
					data = "{}"
				}
				if err := os.WriteFile(filepath.Join(runDir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = runDir
			cmd.Env = append(os.Environ(), "BORK_PARENT_SOURCE_DATABASE_REQUIRED=")
			cmd.Env = append(cmd.Env, tt.env...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
		})
	}
}
