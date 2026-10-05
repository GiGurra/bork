package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIStaticCompletion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/process"
type Options = {
 config: String = "missing.json"
 namespace: String
 mode: String = "dev"
 secret: Option[String]
 old: Option[String]
 file: Option[String]
 directory: Option[String]
 targets: List[String] = []
} derive (Decode)
fn main() {
 leaf = cli.Subcommand[Options]("deploy", "Deploy resources", (options, s) => {
  onClose(s, () => { println("closed") })
  println(options)
 }, [
  cli.Flag { field: "config", configFile: true }
  cli.Flag { field: "namespace", short: "n", choices: [.{ value: "dev", description: "Development" }, .{ value: "prod", description: "Production" }, .{ value: "dev", description: "Duplicate" }] }
  cli.Flag { field: "mode", env: "BORK_COMPLETION_MODE", choices: [.{ value: "dev" }, .{ value: "prod" }], strictChoices: true }
  cli.Flag { field: "secret", hidden: true, choices: [.{ value: "hidden-value" }] }
  cli.Flag { field: "old", deprecated: "use namespace", choices: [.{ value: "old-value" }] }
  cli.Flag { field: "file", files: true }
  cli.Flag { field: "directory", directories: true }
  cli.Flag { field: "targets", positional: true, choices: [.{ value: "alpha", description: "First" }, .{ value: "beta", description: "Second" }], keepOrder: true }
 ]).copy(aliases: ["d"], longDescription: "Deploy to the selected cluster.", examples: "app cluster deploy --namespace dev")
 group = cli.Group("cluster", "Manage clusters", [leaf]).copy(aliases: ["k"])
 hidden = cli.Subcommand[Options]("secret", "Hidden command", (options, s) => {}).copy(hidden: true)
 match (cli.RunCommands("app", "Manage resources", [group, hidden])) {
  error: cli.Error => { println(error); process.Exit(1) }
  Ok => {}
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
	tests := []struct {
		name         string
		args         []string
		want, absent []string
		exact        string
		config, env  string
		failure      bool
	}{
		{name: "root-help", args: []string{"--help"}, want: []string{"Manage resources", "cluster", "completion"}, absent: []string{"Hidden command"}},
		{name: "group-help", args: []string{"k", "--help"}, want: []string{"Manage clusters", "deploy"}},
		{name: "nested-help", args: []string{"help", "k", "d"}, want: []string{"Deploy to the selected cluster.", "app cluster deploy", "--namespace", "Examples:"}, absent: []string{"--secret", "--old"}},
		{name: "root-complete", args: []string{"__complete", ""}, want: []string{"cluster\tManage clusters", ":4"}, absent: []string{"secret\t"}},
		{name: "group-complete", args: []string{"__complete", "k", ""}, want: []string{"deploy\tDeploy resources", ":4"}},
		{name: "long-values", args: []string{"__complete", "cluster", "deploy", "--namespace", ""}, exact: "dev\tDevelopment\nprod\tProduction\n:4\n"},
		{name: "aliases-short-values", args: []string{"__complete", "k", "d", "-n", "p"}, exact: "prod\tProduction\n:4\n"},
		{name: "assignment-values", args: []string{"__complete", "cluster", "deploy", "--namespace=d"}, exact: "dev\tDevelopment\n:4\n"},
		{name: "no-descriptions", args: []string{"__completeNoDesc", "cluster", "deploy", "--namespace", ""}, exact: "dev\nprod\n:4\n"},
		{name: "flag-names", args: []string{"__complete", "cluster", "deploy", "--"}, want: []string{"--namespace", ":4"}, absent: []string{"--secret", "--old"}},
		{name: "hidden-values", args: []string{"__complete", "cluster", "deploy", "--secret", ""}, exact: ":4\n"},
		{name: "deprecated-values", args: []string{"__complete", "cluster", "deploy", "--old", ""}, exact: ":4\n"},
		{name: "positional-values", args: []string{"__complete", "cluster", "deploy", "--", "a"}, exact: "alpha\tFirst\n:36\n"},
		{name: "repeated-positionals", args: []string{"__complete", "cluster", "deploy", "alpha", "b"}, exact: "beta\tSecond\n:36\n"},
		{name: "file-directive", args: []string{"__complete", "cluster", "deploy", "--file", ""}, exact: ":0\n"},
		{name: "directory-directive", args: []string{"__complete", "cluster", "deploy", "--directory", ""}, exact: ":16\n"},
		{name: "protocol-error", args: []string{"__complete", "cluster", "deploy", "--unknown", "value", ""}, exact: ":0\n"},
		{name: "prefix-miss", args: []string{"__complete", "cluster", "deploy", "--namespace", "x"}, exact: ":4\n"},
		{name: "alias-dispatch", args: []string{"k", "d", "--namespace", "dev", "beta"}, config: `{}`, want: []string{`namespace: "dev"`, `targets: ["beta"]`, "closed"}},
		{name: "non-strict-choice", args: []string{"cluster", "deploy", "--namespace", "custom"}, config: `{}`, want: []string{`namespace: "custom"`}},
		{name: "strict-cli", args: []string{"cluster", "deploy", "--namespace", "dev", "--mode", "bad"}, config: `{}`, want: []string{".mode", "allowed choices"}, failure: true},
		{name: "strict-env", args: []string{"cluster", "deploy", "--namespace", "dev"}, config: `{}`, env: "bad", want: []string{".mode", "allowed choices"}, failure: true},
		{name: "strict-config", args: []string{"cluster", "deploy", "--namespace", "dev"}, config: `{"mode":"bad"}`, want: []string{".mode", "allowed choices"}, failure: true},
		{name: "strict-precedence", args: []string{"cluster", "deploy", "--namespace", "dev", "--mode", "prod"}, config: `{"mode":"bad"}`, env: "bad", want: []string{`mode: "prod"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "missing.json"), []byte(tt.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_COMPLETION_MODE="+tt.env)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if (err != nil) != tt.failure {
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, out, &stderr)
			}
			if tt.exact != "" && string(out) != tt.exact {
				t.Errorf("stdout = %q, want %q; stderr: %s", out, tt.exact, &stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %q in:\n%s\nstderr: %s", want, out, &stderr)
				}
			}
			for _, absent := range append(tt.absent, "hidden-value", "old-value") {
				if strings.Contains(string(out), absent) {
					t.Errorf("unexpected %q in:\n%s", absent, out)
				}
			}
			if tt.config == "" || tt.failure {
				for _, absent := range []string{"closed", "Options {"} {
					if strings.Contains(string(out), absent) {
						t.Errorf("unexpected handler %q in:\n%s", absent, out)
					}
				}
			}
		})
	}
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run("script-"+shell, func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(exe, "completion", shell)
			cmd.Dir = t.TempDir()
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("script: %v", err)
			}
			if !strings.Contains(string(out), "__complete") || !strings.Contains(string(out), "app") {
				t.Fatalf("incomplete script:\n%s", out)
			}
			for _, bad := range []string{"missing.json", "closed", "Options {"} {
				if strings.Contains(string(out), bad) {
					t.Errorf("script invoked handler/config: %s", out)
				}
			}
			if shell == "bash" {
				if bash, err := exec.LookPath("bash"); err == nil {
					check := exec.Command(bash, "-n")
					check.Stdin = bytes.NewReader(out)
					if output, err := check.CombinedOutput(); err != nil {
						t.Fatalf("bash syntax: %v\n%s", err, output)
					}
				}
			}
		})
	}
}

func TestCLICompletionMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
type Options = { mode: String = "dev", number: Int = 1, items: List[String] = ["dev"], optional: Option[List[String]] } derive (Decode)
fn main() {
 println(cli.Parse[Options]("app", "", ["--help"], flags: [.{ field: "mode", files: true, directories: true }]))
 println(cli.Parse[Options]("app", "", ["--help"], flags: [.{ field: "mode", choices: [.{ value: "bad\nvalue" }] }]))
 println(cli.Parse[Options]("app", "", ["--help"], flags: [.{ field: "number", choices: [.{ value: "1" }], strictChoices: true }]))
 println(cli.Parse[Options]("app", "", ["--help"], flags: [.{ field: "mode", strictChoices: true }]))
 println(cli.Parse[Options]("app", "", ["--help"], flags: [.{ field: "mode", choices: [.{ value: "prod" }], strictChoices: true }]))
 println(cli.Parse[Options]("app", "", ["--items", "prod", "--optional", "bad"], flags: [.{ field: "items", choices: [.{ value: "dev" }], strictChoices: true }, .{ field: "optional", choices: [.{ value: "dev" }], strictChoices: true }]))
 leaf = cli.Subcommand[Options]("deploy", "", (options, s) => { println("handler") }).copy(aliases: ["d"])
 println(cli.Dispatch("app", "", ["--help"], [cli.Group("cluster", "", [leaf, leaf.copy(name: "other")])]))
 println(cli.Dispatch("app", "", [], [cli.Group("empty", "", [])]))
 println(cli.Dispatch("app", "", [], [leaf.copy(aliases: ["completion"])]))
 println(cli.Dispatch("app", "", [], [leaf.copy(children: [leaf])]))
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
	for _, want := range []string{"choose files or directories", "completion choices must be nonempty", "strict choices require String", "strict choices require at least one", "default is not one", ".items[0]", ".optional[0]", "duplicate subcommand name d", "commands[0].children", "at least one subcommand", "subcommand name completion is reserved", "only a Group can have children"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "handler") || strings.Contains(string(out), "Help {") {
		t.Fatalf("metadata errors should precede help/handler:\n%s", out)
	}
}
