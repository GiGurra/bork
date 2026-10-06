package driver

import (
	"os/exec"
	"strings"
	"testing"
)

func TestCLIHelpSchema(t *testing.T) {
	t.Parallel()
	source := `import "bork/codec"
import "bork/cli"
import "bork/process"
use codec.Defaults
use customDecoder
type Level = sealed { Info, Debug } derive (codec.Decode)
type Db = { host: String = "localhost", port: Int = 5432 } derive (codec.Decode)
type Box[T] = { value: T } derive (codec.Decode)
type Token = String
type Custom = { value: String }
instance customDecoder: codec.Decode[Custom] {
 metadata codec.FieldSchema = codec.FieldSchema { kind: "string", optional: false }
 fn decode(input: codec.Value): Custom | codec.DecodeError {
  match (input) {
   codec.Value.String { value } => Custom { value: value }
   _ => codec.DecodeError { path: "", message: "expected a string" }
  }
 }
}
type Options = {
 // Count items to process.
 count: Int = 3
 ratio: Float = 1.5
 character: Rune = '界'
 tags: List[String] = ["one", "two words"]
 ids: List[Int] = [1, 2]
 enabled: Bool = false
 level: Level = Level.Info
 db: Db = .{}
 box: Box[Int] = .{ value: 1 }
 config: Option[String] = Option.Some("missing.json")
 optional: Option[Int] = Option.None
 custom: Custom = .{ value: "default" }
 // API token.
 token: String
 // Hidden environment description.
 secret: String = "secret"
 oldToken: String = "legacy"
 // Keep --count string in this description.
 description: String = "text"
 literal: String = "String.keep"
 aliasLiteral: Token = "Token.keep"
} derive (codec.Decode)
fn flags(): List[cli.Flag] {
 [.{ field: "count", description: Option.Some("Count \u00603\u0060 items to process.") },
  .{ field: "config", configFile: true },
  .{ field: "token", long: cli.Mapping.Disabled, env: "BORK_HELP_TOKEN" },
  .{ field: "secret", long: cli.Mapping.Disabled, env: "BORK_HELP_SECRET", hidden: true },
  .{ field: "oldToken", long: cli.Mapping.Disabled, env: "BORK_HELP_OLD", deprecated: "use token instead" }]
}
fn main() {
 arguments = process.Args()
 if (arguments.head() == Option.Some("tree")) {
  match (cli.Dispatch("app", "", arguments.drop(1), [cli.Group("group", "Group", [cli.Subcommand[Options]("serve", "Serve", (options, s) => { println("handler") }, flags: flags()).copy(examples: "      --count 3 \\")])])) {
   help: cli.Help => { println(help.text) }
   other => { panic(s"Expected help, got ${other}") }
  }
 } else {
  match (cli.Parse[Options]("app", "Help schema", arguments, flags: flags())) {
   help: cli.Help => { println(help.text) }
   other => { panic(s"Expected help, got ${other}") }
  }
 }
}
`
	exe, err := buildFixtureOutput(t, validatorFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--help"},
		{"--config", "missing.json", "--help"},
		{"tree", "group", "serve", "--help"},
		{"tree", "help", "group", "serve"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := exec.Command(exe, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("help: %v\n%s", err, out)
			}
			text := string(out)
			if args[0] == "tree" && !strings.Contains(text, "Examples:\n      --count 3 \\") {
				t.Errorf("command example changed:\n%s", text)
			}
			for _, want := range []string{
				"--count int", "--ratio float", "--character rune", "--tags strings", "--ids ints",
				"--level string", "--db json", "--box json", "--custom string",
				`(default ["one", "two words"])`, "(default [1, 2])", "(default Info)",
				`(default { host: "localhost", port: 5432 })`, "(default { value: 1 })",
				`(default "missing.json")`, "(default unset)",
				"Environment variables:", "BORK_HELP_TOKEN string  API token. (required)",
				"Keep --count string in this description.",
				"(default String.keep)",
				"(default Token.keep)",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("missing %q in:\n%s", want, text)
				}
			}
			for _, absent := range []string{"handler", "BORK_HELP_SECRET", "BORK_HELP_OLD", "Hidden environment description", "default Level.Info", "default Db {", "default Box {"} {
				if strings.Contains(text, absent) {
					t.Errorf("unexpected %q in:\n%s", absent, text)
				}
			}
		})
	}
}
