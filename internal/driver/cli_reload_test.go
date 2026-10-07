package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIConfigReload(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
use codec.Defaults
pred Positive(value: Int) { value > 0 }
type Options = { port: Int where Positive = 8080, tags: List[String] = [] } derive (codec.Decode)
fn write(text: String) uses io unsafe go {
 import "os"
 if err := os.WriteFile("config.json", []byte(text), 0600); err != nil { panic(err) }
}
fn environment(value: String) uses io unsafe go {
 import "os"
 if err := os.Setenv("BORK_RELOAD_PORT", value); err != nil { panic(err) }
}
fn reload(args: List[String] = []) uses io: cli.Resolved[Options] | cli.Error | cli.Help {
 cli.Reload[Options]("app", "Reload", args, [cli.Flag { field: "port", env: "BORK_RELOAD_PORT" }], ["config.json"])
}
fn main() {
 environment("")
 write("{\"port\":8081,\"tags\":[\"first\"]}")
 match (cli.ParseResolved[Options]("app", "Reload", [], [cli.Flag { field: "port", env: "BORK_RELOAD_PORT" }], ["config.json"])) {
  first: cli.Resolved[Options] => {
   println(s"first=${first.value}")
   write("{\"port\":8082,\"tags\":[\"second\"]}")
   println(s"file=${reload()}")
   environment("8083")
   println(s"env=${reload()}")
   environment("0")
   println(s"invalid-env=${reload()}")
   write("{\"port\":0,\"tags\":[\"third\"]}")
   println(s"flag=${reload(["--port","8084"])}")
   environment("")
   println(s"invalid-file=${reload()}")
   println(s"old=${first.value}")
   println(s"old-source=${first.Source("port")}")
   write("invalid")
   println(s"help=${reload(["--help"])}")
   println(s"malformed=${reload()}")
   println(s"still-old=${first.value}")
  }
  error: cli.Error => println(error)
  help: cli.Help => println(help.text)
 }
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bork.mod"), []byte("module reloadtest\nunsafe \"reloadtest\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{
		`first=Options { port: 8081, tags: ["first"] }`,
		`file=Resolved { value: Options { port: 8082, tags: ["second"] }`,
		`env=Resolved { value: Options { port: 8083`,
		`Source.Env { name: "BORK_RELOAD_PORT" }`,
		`invalid-env=Error`, `must be Positive`,
		`flag=Resolved { value: Options { port: 8084, tags: ["third"] }`,
		`Source.Flag { name: "port" }`, `invalid-file=Error`,
		`old=Options { port: 8081, tags: ["first"] }`,
		`old-source=Option.Some(Source.Config { path: "config.json" })`,
		`help=Help { text: "Reload`, `malformed=Error`,
		`still-old=Options { port: 8081, tags: ["first"] }`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}
