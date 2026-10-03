package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRun(t *testing.T) {
	root := t.TempDir()
	source := `import "bork/cli"
pred validPort(n: Int) { n > 0 && n < 65536 }
pred nonempty(s: String) { s.byteLength() > 0 }
type Options = { name: String where nonempty, port: Int where validPort = 8080, verbose: Bool = true } derive (Decode)
fn main() {
 result = cli.Run[Options]("app", "Example", (options, s) => {
   onClose(s, () => { println("closed") })
   println(options)
 }, [cli.Flag { field: "name", env: "BORK_CLI_TEST_NAME" }, cli.Flag { field: "port", short: "p" }])
 println(result)
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, env          string
		args, want, absent []string
	}{
		{name: "env-defaults", env: "Ada", want: []string{`name: "Ada"`, "port: 8080", "verbose: true", "closed\nUnit"}},
		{name: "cli-precedence", env: "Env", args: []string{"--name", "CLI", "-p", "443", "--verbose=false"}, want: []string{`name: "CLI"`, "port: 443", "verbose: false", "closed\nUnit"}, absent: []string{`name: "Env"`}},
		{name: "empty-env", args: []string{"--port", "0"}, want: []string{".name", "is missing", ".port", "must be validPort"}, absent: []string{"closed", "Unit"}},
		{name: "help", env: "Ada", args: []string{"--help"}, want: []string{"Example", "Usage:", "--name", "default 8080", "Unit"}, absent: []string{"Options {", "closed"}},
		{name: "bad-flag", args: []string{"--missing"}, want: []string{"Error {", "unknown flag"}, absent: []string{"closed", "Unit"}},
		{name: "unexpected-positional", env: "Ada", args: []string{"unexpected"}, want: []string{"Error {"}, absent: []string{"closed", "Unit"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(exe, tt.args...)
			cmd.Env = append(os.Environ(), "BORK_CLI_TEST_NAME="+tt.env)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q in:\n%s", want, out)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(string(out), absent) {
					t.Errorf("unexpected %q in:\n%s", absent, out)
				}
			}
		})
	}
}
