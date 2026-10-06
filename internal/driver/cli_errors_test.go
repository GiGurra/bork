package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCLIErrorRendering(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
use codec.Defaults
pred consistent(o: Options) { !o.json || !o.yaml }
type Options = { json: Bool = false, yaml: Bool = false } where consistent derive (codec.Decode)
fn main() {
 collected = cli.Error { errors: [
  .{ path: ".port", message: "is missing" },
  .{ path: ".tags[1]", message: "expected an integer" },
  .{ path: "", message: "must be consistent" }
 ] }
 println(collected.Render("app serve"))
 println(cli.Error { errors: [] }.Render("app"))
 match (cli.Parse[Options]("app", "", ["--json", "--yaml"])) {
  error: cli.Error => { println(error.Render("app")) }
  other => { panic(s"Expected an error, got ${other}") }
 }
 match (cli.Parse[Options]("app", "", ["--missing"])) {
  error: cli.Error => { println(error.Render("app")) }
  other => { panic(s"Expected an error, got ${other}") }
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
	want := "Error: .port: is missing\n" +
		"Error: .tags[1]: expected an integer\n" +
		"Error: must be consistent\n\n" +
		"Try 'app serve --help' for usage.\n" +
		"Error: invalid command-line options\n\n" +
		"Try 'app --help' for usage.\n" +
		"Error: must be consistent\n\n" +
		"Try 'app --help' for usage.\n" +
		"Error: app: unknown flag: --missing\n\n" +
		"Try 'app --help' for usage.\n"
	if string(out) != want {
		t.Errorf("output:\n%s\nwant:\n%s", out, want)
	}
}
