package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCLIConfigDump(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults

type Db = { port: Int = 5432 } derive (codec.Decode, codec.Encode)
type Options = {
 port: Int = 8080 codec { name: "http_port" }
 verbose: Bool = false
 empty: String = ""
 tags: List[String] = []
 maybe: Option[String]
 db: Db
} derive (codec.Decode, codec.Encode)
type Broken = {}
instance BrokenEncode: codec.Encode[Broken] {
 fn encode(value: Broken): codec.Value {
  codec.Value.Object { fields: [codec.Field { name: "bad", value: codec.Value.Number { text: "broken" } }] }
 }
}
fn main() {
 println(cli.DumpFile("kept.json", Broken {}))
 match (cli.Parse[Options]("app", "Dump", ["--http-port", "9000"], [cli.Flag { field: "port", env: "BORK_DUMP_PORT" }], ["base.json"])) {
  value: Options => {
   println(cli.Dump(value))
   println(cli.Dump(value, .Yaml))
   for (path in process.Args()) {
    match (cli.DumpFile(path, value)) {
     Ok => println(cli.Parse[Options]("app", "Read", [], configFiles: [path]))
     error: cli.Error => println(error)
    }
   }
  }
  error: cli.Error => println(error)
  help: cli.Help => println(help.text)
 }
 println(cli.Dump(42))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte(`{"http_port":8081,"tags":["config"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resolved.json"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "kept.json"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths := []string{"resolved.json", "resolved.yaml", "resolved.YML", "extensionless"}
	args := append([]string{}, paths...)
	args = append(args, "missing/config.json", "", ".")
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BORK_DUMP_PORT=8082")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{`"http_port": 9000`, "http_port: 9000", "requires an encoded object", "no such file or directory", "is a directory", `tags: ["config"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if got := strings.Count(text, "Options { port: 9000, verbose: false"); got != len(paths) {
		t.Errorf("expected %d round trips, got %d:\n%s", len(paths), got, out)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "kept.json")); err != nil || string(data) != "keep" {
		t.Fatalf("encoding failure modified destination: %q, %v", data, err)
	}
	for _, path := range paths {
		if runtime.GOOS != "windows" {
			info, err := os.Stat(filepath.Join(dir, path))
			if err != nil {
				t.Fatal(err)
			}
			wantMode := os.FileMode(0o600)
			if path == "resolved.json" {
				wantMode = 0o640
			}
			if info.Mode().Perm() != wantMode {
				t.Errorf("%s mode = %o, want %o", path, info.Mode().Perm(), wantMode)
			}
		}
		data, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(path, "yaml") || strings.HasSuffix(path, "YML") {
			if !strings.Contains(string(data), "http_port: 9000") {
				t.Errorf("not YAML: %s", data)
			}
		} else if !strings.Contains(string(data), `"http_port": 9000`) {
			t.Errorf("not JSON: %s", data)
		}
	}
}
