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
type Db = { host: String = "localhost" codec { aliases: ["hostname"] }, port: Int where Positive = 5432 } derive (codec.Decode, codec.Encode)
type Credentials = { user: String, enabled: Bool } derive (codec.Decode, codec.Encode)
type Empty = {} derive (codec.Decode, codec.Encode)
type Groups = { credentials: Option[Credentials], empty: Empty } derive (codec.Decode, codec.Encode)
type Options = { db: Db codec { name: "database", aliases: ["oldDb"] }, optional: Option[Db] } derive (codec.Decode, codec.Encode)
type Defaulted = { db: Db = Db {} } derive (codec.Decode)
type Whole = { db: Db = Db {} cli { flatten: false } } derive (codec.Decode, codec.Encode)
fn main() {
 args = process.Args()
 mode = args.head().getOr("normal")
 args = args.drop(1)
 if (mode == "groups") {
  match (cli.Parse[Groups]("app", "Groups", args)) {
   value: Groups => println(json.Encode(value))
   other => println(other)
  }
  return
 }
 if (mode == "defaulted") { println(cli.Parse[Defaulted]("app", "Nested", args, configFiles: ["missing.json"])); return }
 if (mode == "whole") { println(cli.Parse[Whole]("app", "Nested", args)); return }
 match (cli.Parse[Options]("app", "Nested", args, configFiles: if (mode == "config") { ["config.json"] } else { [] }, settings: .{ autoEnv: true, envPrefix: "BORK_NESTED" })) {
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
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mode              string
		args, env, want, absent []string
	}{
		{"optional required group absent", "groups", nil, nil, []string{`"credentials":null`, `"empty":{}`}, nil},
		{"optional required group complete", "groups", []string{"--credentials-user", "Ada"}, nil, []string{`"credentials":{"user":"Ada","enabled":false}`}, nil},
		{"optional group missing required leaf", "groups", []string{"--credentials-enabled"}, nil, []string{`.credentials.user`, `is missing`}, nil},
		{"leaf defaults", "normal", nil, nil, []string{`"database":{"host":"localhost","port":5432}`, `"optional":null`}, nil},
		{"nested cli", "normal", []string{"--database-host", "cli", "--database-port", "80"}, nil, []string{`"database":{"host":"cli","port":80}`}, nil},
		{"optional group activation", "normal", []string{"--optional-host", "optional"}, nil, []string{`"optional":{"host":"optional","port":5432}`}, nil},
		{"nested aliases", "normal", []string{"--old-db-hostname", "alias"}, nil, []string{`"host":"alias"`}, nil},
		{"nested env", "normal", nil, []string{"BORK_NESTED_DATABASE_HOST=env"}, []string{`"host":"env"`}, nil},
		{"nested config aliases", "config", nil, nil, []string{`"database":{"host":"config","port":80}`}, nil},
		{"child override", "config", []string{"--database-host", "cli"}, nil, []string{`"database":{"host":"cli","port":80}`}, nil},
		{"leaf fact", "normal", []string{"--database-port", "0"}, nil, []string{".database.port", "Positive"}, nil},
		{"parent default rejected before reads", "defaulted", []string{"--help"}, nil, []string{"parent default", "flatten: false"}, []string{"missing.json"}},
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
