package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIParentDefaultsConstrainedAliases(t *testing.T) {
	t.Parallel()
	dir := validatorFixture(t, `import "bork/cli"
import "bork/codec"
import "bork/enum"
import "bork/process"
use codec.Defaults
type Color = sealed { Red, Other(String) codec {fallback:true} } derive(enum.Enum,codec.Decode)
type KnownColor = Color where enum.Known derive(codec.Decode)
type Config = {port:Int, color:KnownColor} derive(codec.Decode)
pred Valid(value:Config) {value.port>0}
type ValidConfig = Config where Valid
derive codec.Decode for ValidConfig
type Options = {db:ValidConfig=.{port:443,color:.Red}}derive(codec.Decode)
fn main(){println(cli.ParseWith[Options]("app","Aliases",process.Args(),configFiles:["config.json"]))}`)
	exe, err := buildFixtureOutput(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, config string
		args, want   []string
	}{
		{"baseline", "{}", nil, []string{"port: 443", "color: Color.Red"}},
		{"overlay", "{}", []string{"--db-port", "8443"}, []string{"port: 8443", "color: Color.Red"}},
		{"parent fact", "{}", []string{"--db-port", "0"}, []string{"Valid"}},
		{"enum fact", `{"db":{"color":"purple"}}`, nil, []string{"Known"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(runDir, "config.json"), []byte(tt.config), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = runDir
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
