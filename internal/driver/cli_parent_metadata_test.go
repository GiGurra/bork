package driver

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCLIParentDefaultMetadata(t *testing.T) {
	t.Parallel()
	source := `import "bork/cli"
import "bork/codec"
use codec.Defaults
type Db={host:String="leaf",tags:List[String]=["leaf"]}derive(codec.Decode)
type Options={db:Db=.{host:"parent",tags:["parent"]}}derive(codec.Decode)
type Required={user:String}derive(codec.Decode)
type Positioned={lead:String="lead",db:Required=.{user:"parent"}}derive(codec.Decode)
type Invalid={db:Required=.{user:"parent"},tail:String}derive(codec.Decode)
fn main(){
 println(cli.Parse[Options]("app","Choices",[],flags:[.{field:"db.host",choices:[.{value:"parent"}],strictChoices:true},.{field:"db.tags",choices:[.{value:"parent"}],strictChoices:true}]))
 println(cli.Parse[Options]("app","Choices",["--help"],configFiles:["missing.json"],flags:[.{field:"db.host",choices:[.{value:"leaf"}],strictChoices:true}]))
 println(cli.Parse[Positioned]("app","Positions",[],flags:[.{field:"lead",position:.Some(0)},.{field:"db.user",position:.Some(1)}]))
 println(cli.Parse[Invalid]("app","Positions",["--help"],flags:[.{field:"db.user",position:.Some(0)},.{field:"tail",position:.Some(1)}]))
}`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`host: "parent", tags: ["parent"]`, `default is not one of the allowed choices`, `lead: "lead", db: Required { user: "parent" }`, `required positional cannot follow optional/default positional db.user`} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output %q lacks %q", out, want)
		}
	}
	if strings.Contains(string(out), "missing.json") {
		t.Fatalf("metadata failure read config: %s", out)
	}
}
