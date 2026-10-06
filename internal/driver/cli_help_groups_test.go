package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHelpGroups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
type Options = { name: String } derive (codec.Decode)
type Global = { region: String } derive (codec.Decode)
fn leaf(name: String): cli.Command {
 cli.Subcommand[Options](name, s"${name} description", (options, s) => { println("HANDLER") }, configFiles: ["missing.json"])
}
fn main() {
 args = process.Args()
 mode = args.head().getOr("normal")
 args = args.drop(1)
 baseGroups: List[cli.HelpGroup] = [.{ id: "other", title: "Other commands:" }, .{ id: "operations", title: "Operations:" }]
 groups: List[cli.HelpGroup] = if (mode == "auto" || mode == "plain") { [] } else if (mode == "duplicate") { baseGroups.concat([.{ id: "operations", title: "Again:" }]) } else if (mode == "empty") { [.{ id: "", title: "Empty:" }] } else if (mode == "title") { [.{ id: "ok", title: "Bad\nTitle" }] } else { baseGroups }
 category = if (mode == "invalid-child") { "bad id" } else if (mode == "plain") { "" } else { "operations" }
 commands = [leaf("zeta").copy(helpGroup: category, aliases: ["z"]), leaf("alpha").copy(helpGroup: if (mode == "plain") { "" } else { "other" }), leaf("plain"), leaf("secret").copy(helpGroup: category, hidden: true), leaf("old").copy(helpGroup: category, deprecated: "use zeta")]
 result = if (mode == "root") {
  bound = cli.RootSubcommand[Global, Options]("zeta", "Zeta", (root, options, s) => { println("HANDLER") }, configFiles: ["missing-leaf.json"])
  cli.DispatchRoot[Global]("app", "Root", args, [bound.copy(command: bound.command.copy(helpGroup: "operations"))], configFiles: ["missing-root.json"], settings: .{ helpGroups: groups })
 } else if (mode == "nested") {
  branch = cli.Group("group", "Branch", commands).copy(helpGroup: "branches", helpGroups: groups)
  cli.Dispatch("app", "Root", args, [branch])
 } else {
  cli.Dispatch("app", "Root", args, commands, settings: .{ helpGroups: groups })
 }
 match (result) {
  help: cli.Help => { println(help.text); eprintln(help.diagnostics) }
  other => println(other)
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(dir, exe); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, mode         string
		args, want, absent []string
		order              []string
	}{
		{"explicit order", "normal", []string{"--help"}, []string{"Operations:", "Other commands:", "Additional Commands:", "zeta", "alpha"}, []string{"secret description", "old description", "HANDLER", "missing.json"}, []string{"Other commands:", "Operations:", "Additional Commands:"}},
		{"automatic declaration order", "auto", []string{"--help"}, []string{"operations:", "other:"}, nil, []string{"operations:", "other:"}},
		{"unchanged ungrouped", "plain", []string{"--help"}, []string{"Available Commands:"}, []string{"Operations:", "Additional Commands:"}, nil},
		{"nested headings", "nested", []string{"group", "--help"}, []string{"Other commands:", "Operations:"}, []string{"HANDLER", "missing.json"}, nil},
		{"branch category", "nested", []string{"--help"}, []string{"branches:", "group"}, nil, nil},
		{"root options no reads", "root", []string{"--help"}, []string{"Operations:"}, []string{"HANDLER", "missing-root.json", "missing-leaf.json"}, nil},
		{"alias leaf help", "normal", []string{"z", "--help"}, []string{"zeta description"}, []string{"HANDLER", "missing.json"}, nil},
		{"duplicate metadata", "duplicate", []string{"--help"}, []string{"duplicate help group id"}, []string{"HANDLER"}, nil},
		{"invalid child metadata", "invalid-child", []string{"--help"}, []string{"invalid help group id"}, []string{"HANDLER"}, nil},
		{"empty id", "empty", []string{"--help"}, []string{"help group id must"}, nil, nil},
		{"multiline title", "title", []string{"--help"}, []string{"help group title must"}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := exec.Command(exe, append([]string{tt.mode}, tt.args...)...).CombinedOutput()
			if err != nil {
				t.Fatalf("run: %v\n%s", err, out)
			}
			text := string(out)
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Fatalf("output %q lacks %q", text, want)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(text, absent) {
					t.Fatalf("output %q includes %q", text, absent)
				}
			}
			previous := -1
			for _, heading := range tt.order {
				at := strings.Index(text, heading)
				if at <= previous {
					t.Fatalf("heading order %v in %q", tt.order, text)
				}
				previous = at
			}
		})
	}
}
