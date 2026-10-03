package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISubcommands(t *testing.T) {
	root := t.TempDir()
	source := `import "bork/cli"
pred validPort(n: Int) { n > 0 && n < 65536 }
type Serve = {
 config: Option[String]
 // Host to serve.
 host: String
 // Listening port.
 port: Int where validPort = 8080
} derive (Decode)
type Echo = { words: List[String] } derive (Decode)
fn commands(): List[cli.Command] {
 [cli.Subcommand[Serve]("serve", "Serve a host", (options, s) => {
    sleep(0)
    onClose(s, () => { println("serve closed") })
    println(options)
  }, [cli.Flag { field: "port", short: "p", env: "BORK_SUBCOMMAND_PORT" }, cli.Flag { field: "config", configFile: true }], configFiles: ["base.json"]),
  cli.Subcommand[Echo]("echo", "Echo words", (options, s) => {
    onClose(s, () => { println("echo closed") })
    println(options.words)
  }, [cli.Flag { field: "words", positional: true }])]
}
fn main() { println(cli.RunCommands("app", "Example commands", commands())) }
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, env, base, config string
		args, want, absent      []string
	}{
		{name: "serve-default", args: []string{"serve", "--host", "localhost"}, want: []string{`host: "localhost"`, "port: 8080", "serve closed\nUnit"}, absent: []string{"echo closed"}},
		{name: "command-base-file", base: `{"host":"file","port":8081}`, args: []string{"serve"}, want: []string{`host: "file"`, "port: 8081", "serve closed"}},
		{name: "command-selected-file", base: `{"host":"base"}`, config: `{"host":"selected","port":8082}`, args: []string{"serve", "--config", "config.json"}, want: []string{`host: "selected"`, "port: 8082", "serve closed"}},
		{name: "serve-env", env: "9090", args: []string{"serve", "--host", "localhost"}, want: []string{"port: 9090", "serve closed"}},
		{name: "serve-flags", env: "9090", args: []string{"serve", "--host", "localhost", "-p", "443"}, want: []string{"port: 443", "serve closed"}},
		{name: "echo", args: []string{"echo", "one", "two"}, want: []string{`["one", "two"]`, "echo closed\nUnit"}, absent: []string{"serve closed"}},
		{name: "validation", args: []string{"serve", "--port", "0"}, want: []string{".host", "is missing", ".port", "must be validPort"}, absent: []string{"closed", "Unit"}},
		{name: "bad-flag", args: []string{"serve", "--missing"}, want: []string{"unknown flag", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "unknown-command", args: []string{"missing"}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "unknown-help", args: []string{"help", "missing"}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "prefixed-unknown-help", args: []string{"--help=false", "help", "missing"}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "prefixed-extra-help", args: []string{"-h=false", "help", "serve", "missing"}, want: []string{"help accepts at most one command name", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "extra-help", args: []string{"help", "serve", "missing"}, want: []string{"help accepts at most one command name", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "hidden-completion", args: []string{"__complete", ""}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "completion-after-unknown-value", args: []string{"--unknown", "value", "__complete", ""}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit", ":0"}},
		{name: "completion-no-desc-after-unknown-value", args: []string{"-x", "value", "__completeNoDesc", ""}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit", ":0"}},
		{name: "hidden-completion-no-desc", args: []string{"__completeNoDesc", ""}, want: []string{"unknown command", "Error {"}, absent: []string{"closed", "Unit"}},
		{name: "no-command", want: []string{"Example commands", "serve", "echo", "Unit"}, absent: []string{"closed", "Serve {"}},
		{name: "root-help", args: []string{"--help"}, want: []string{"Example commands", "serve", "echo", "Unit"}, absent: []string{"closed", "Serve {"}},
		{name: "leaf-help", args: []string{"serve", "--help"}, want: []string{"Serve a host", "app serve", "--host", "--port", "Host to serve.", "default 8080", "Unit"}, absent: []string{"closed", "Serve {"}},
		{name: "help-command", args: []string{"help", "serve"}, want: []string{"Serve a host", "--host", "--port", "default 8080", "Unit"}, absent: []string{"closed", "Serve {"}},
		{name: "echo-help", args: []string{"echo", "--help"}, want: []string{"Echo words", "Unit"}, absent: []string{"closed", "--port"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			base := tt.base
			if base == "" {
				base = `{}`
			}
			for name, data := range map[string]string{"base.json": base, "config.json": tt.config} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_SUBCOMMAND_PORT="+tt.env)
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

func TestCLISubcommandMetadata(t *testing.T) {
	root := t.TempDir()
	source := `import "bork/cli"
type Options = {} derive (Decode)
fn command(name: String): cli.Command { cli.Subcommand[Options](name, "", (options, s) => { println("handler") }) }
fn main() {
 println(cli.Dispatch("app", "", [], []))
 println(cli.Dispatch("app", "", [], [command("same"), command("same")]))
 println(cli.Dispatch("app", "", [], [command("bad name"), command("-flag"), command("help")]))
 println(cli.Dispatch("app", "", ["ok", "--help"], [command("ok")]))
 println(cli.Dispatch("app", "", ["bad"], [cli.Subcommand[Options]("bad", "", (options, s) => { println("handler") }, flags: [cli.Flag { field: "missing" }])]))
 println(cli.Dispatch("app", "", ["ok"], [command("ok")]))
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
	for _, want := range []string{"at least one subcommand", "duplicate subcommand name same", "commands[0].name", "commands[1].name", "subcommand name help is reserved", "Help {", "unknown CLI field", "handler\nUnit"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(string(out), "handler") != 1 {
		t.Fatalf("unexpected handler dispatch:\n%s", out)
	}
}

func TestCLISubcommandEffects(t *testing.T) {
	root := t.TempDir()
	source := `import "bork/cli"
fn partial(commands: List[cli.Command]) uses io: Unit | cli.Error | cli.Help { cli.Dispatch("app", "", [], commands) }
fn main() {}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "net") || !strings.Contains(err.Error(), "state") {
		t.Fatalf("dispatch must charge closed effects: %v", err)
	}
}
