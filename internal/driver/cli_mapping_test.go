package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIMapping(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
// Short flags must reserve explicit names on later fields.
type Options = {
 // Original host documentation.
 serverHost: String = "localhost"
 // Hidden secret documentation.
 secret: String = "secret-default"
 verbose: Bool
 oldPort: Int = 80
 envOnly: String = "env-default"
 noConfig: String = "config-default"
 httpPort: Int = 8080
} derive (Decode)
fn main() {
 println(cli.Run[Options]("app", "Mapping example", (options, s) => { println(options) }, flags: [
  .{ field: "serverHost", description: Option.Some { value: "Custom host documentation." } },
  .{ field: "secret", long: cli.Mapping.Named { name: "secret" }, shortName: cli.Mapping.Disabled, envName: cli.Mapping.Disabled, hidden: true },
  .{ field: "verbose", envName: cli.Mapping.Disabled },
  .{ field: "oldPort", long: cli.Mapping.Named { name: "legacy" }, deprecated: "use --svc-http-port instead" },
  .{ field: "envOnly", long: cli.Mapping.Disabled, envName: cli.Mapping.Named { name: "BORK_MAPPING_EXACT" } },
  .{ field: "noConfig", config: false },
  .{ field: "httpPort", short: "s" }
 ], configFiles: ["base.json"], settings: .{ autoEnv: true, autoShort: true, flagPrefix: "svc", envPrefix: "BORK_MAPPING" }))
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
		name, config       string
		args, want, absent []string
		warning            bool
	}{
		{name: "env-defaults", args: []string{"--svc-verbose"}, want: []string{`serverHost: "env-host"`, `envOnly: "exact-env"`, `secret: "secret-default"`, "httpPort: 8080", "Ok"}},
		{name: "cli-overrides", args: []string{"--svc-server-host", "cli-host", "--svc-verbose=false", "-s", "443", "--secret", "cli-secret"}, want: []string{`serverHost: "cli-host"`, "httpPort: 443", "verbose: false", `secret: "cli-secret"`}},
		{name: "deprecated", args: []string{"--svc-verbose", "-l", "81"}, want: []string{"oldPort: 81"}, warning: true},
		{name: "deprecated-help", args: []string{"--legacy", "81", "--help"}, want: []string{"Usage:", "Ok"}, absent: []string{"Options {"}, warning: true},
		{name: "deprecated-decode-error", args: []string{"--svc-verbose", "--legacy", "bad"}, want: []string{".oldPort", "Error {", "use --svc-http-port instead"}, absent: []string{"Options {", "Ok"}},
		{name: "deprecated-syntax-error", args: []string{"--legacy", "81", "--unknown"}, want: []string{"unknown flag", "Error {", "use --svc-http-port instead"}, absent: []string{"Options {", "Ok"}},
		{name: "required-bool", want: []string{".verbose", "is missing", "Error {"}, absent: []string{"Options {", "Ok"}},
		{name: "disabled-long", args: []string{"--svc-verbose", "--svc-env-only", "value"}, want: []string{"unknown flag", "Error {"}, absent: []string{"Options {", "Ok"}},
		{name: "config-disabled", config: `{"noConfig":null}`, args: []string{"--svc-verbose"}, want: []string{`CLI config field`, "noConfig", "disabled", "Error {"}, absent: []string{"Options {", "Ok"}},
		{name: "config-precedence", config: `{"serverHost":"file-host","httpPort":81}`, args: []string{"--svc-verbose"}, want: []string{`serverHost: "env-host"`, "httpPort: 81"}},
		{name: "help", config: `{`, args: []string{"--help"}, want: []string{"Custom host documentation.", "--svc-server-host", "--svc-verbose", "(required)", "default 8080", "BORK_MAPPING_SVC_SERVER_HOST", "Ok"}, absent: []string{"Original host documentation.", "Hidden secret documentation.", "--secret", "--legacy", "--svc-env-only", "Error {"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			config := tt.config
			if config == "" {
				config = `{}`
			}
			if err := os.WriteFile(filepath.Join(dir, "base.json"), []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_MAPPING_SVC_SERVER_HOST=env-host", "BORK_MAPPING_EXACT=exact-env", "BORK_MAPPING_SECRET=ignored", "BORK_MAPPING_SVC_VERBOSE=true")
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s\n%s", err, &stdout, &stderr)
			}
			for _, want := range tt.want {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("missing %q in:\n%s", want, &stdout)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(stdout.String(), absent) {
					t.Errorf("unexpected %q in:\n%s", absent, &stdout)
				}
			}
			if tt.warning {
				if !strings.Contains(stderr.String(), "use --svc-http-port instead") || strings.Contains(stdout.String(), "deprecated") {
					t.Errorf("warning transport: stdout=%s stderr=%s", &stdout, &stderr)
				}
			} else if stderr.Len() != 0 {
				t.Errorf("unexpected stderr: %s", &stderr)
			}
		})
	}
}

func TestCLIMappingMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
type Options = { alpha: String = "a", another: String = "b", beta: String = "c" } derive (Decode)
type ScalarPositional = { inputFile: String = "input.txt" } derive (Decode)
type ListPositional = { inputFiles: List[String] = [] } derive (Decode)
fn describe(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec {
 field.copy(description: s"${field.field}: previous=${previous.length()}")
}
fn prefix(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec {
 field.copy(long: cli.Mapping.Named { name: s"custom-${field.field}" })
}
fn rename(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec { field.copy(field: "other") }
fn collision(previous: List[cli.FieldSpec], field: cli.FieldSpec): cli.FieldSpec { field.copy(env: cli.Mapping.Named { name: "SAME" }) }
fn main() {
 println(cli.Parse[Options]("auto", "", ["-a", "short", "-b", "explicit"], [.{ field: "beta", short: "b" }], settings: .{ autoShort: true }))
 println(cli.Parse[Options]("disabled", "", [], settings: .{ autoLong: false, autoShort: true }))
 println(cli.Parse[Options]("disabled-env", "", [], [.{ field: "alpha", envName: cli.Mapping.Disabled }], settings: .{ autoEnv: true, envPrefix: "BORK_MAPPING_DISABLED" }))
 println(cli.Parse[Options]("custom", "", ["--custom-alpha", "enriched"], settings: .{ enrichers: [prefix, describe] }))
 println(cli.Parse[Options]("custom-help", "", ["--help"], settings: .{ enrichers: [prefix, describe] }))
 println(cli.Parse[Options]("suppressed-doc", "", ["--help"], [.{ field: "alpha", description: Option.Some { value: "" } }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", short: "x", shortName: cli.Mapping.Disabled }], configFiles: ["missing.json"]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", env: "EXACT", envName: cli.Mapping.Disabled }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", long: cli.Mapping.Disabled, short: "x" }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", long: cli.Mapping.Named { name: "" } }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", long: cli.Mapping.Named { name: "bad name" } }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", envName: cli.Mapping.Named { name: "BAD=ENV" } }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", long: cli.Mapping.Named { name: "help" } }]))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", long: cli.Mapping.Named { name: "same" } }, .{ field: "beta", long: cli.Mapping.Named { name: "same" } }]))
 println(cli.Parse[Options]("bad", "", [], settings: .{ enrichers: [rename] }))
 println(cli.Parse[Options]("bad", "", [], settings: .{ enrichers: [collision] }))
 println(cli.Parse[Options]("bad", "", [], [.{ field: "alpha", positional: true, short: "x" }]))
 println(cli.Parse[ScalarPositional]("scalar", "", ["--help"], [.{ field: "inputFile", positional: true }]))
 println(cli.Parse[ListPositional]("list", "", ["--help"], [.{ field: "inputFiles", positional: true }]))
 println(cli.ParseDetailed[Options]("warning", "", ["--alpha", "old"], [.{ field: "alpha", deprecated: "use --beta" }]))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), "BORK_MAPPING_DISABLED_ALPHA=ignored", "BORK_MAPPING_DISABLED_BETA=env-beta")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	for _, want := range []string{`alpha: "short"`, `beta: "explicit"`, `alpha: "a"`, `beta: "env-beta"`, `alpha: "enriched"`, "alpha: previous=0", "beta: previous=2", "short and shortName cannot both be specified", "env and envName cannot both be specified", "short flag requires an enabled long flag", "long flag must contain", "environment variable must be an identifier", "flag name is reserved for help", "flag name duplicates alpha", "enricher must preserve field identity", "environment variable duplicates alpha", "scalar [input-file]", "list [input-files...]", "Parsed {", "warnings:", "use --beta"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "bork-field-") {
		t.Errorf("internal field name leaked into positional help:\n%s", out)
	}
	if strings.Contains(string(out), "missing.json") {
		t.Errorf("metadata validation read config before failing:\n%s", out)
	}
}

func TestCLIEnricherEffects(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
fn noisy(previous: List[cli.FieldSpec], field: cli.FieldSpec) uses io: cli.FieldSpec {
 println(field.field)
 field
}
fn main() { settings: cli.Settings = .{ enrichers: [noisy] } }
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Check(root); err == nil || !strings.Contains(err.Error(), "io") {
		t.Fatalf("effectful enricher must be rejected: %v", err)
	}
}

func TestCLISubcommandWarnings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
type Options = { oldPort: Int = 80 } derive (Decode)
fn main() {
 println(cli.RunCommands("app", "", [cli.Subcommand[Options]("serve", "", (options, s) => { println(s"handler ${options.oldPort}") }, flags: [.{ field: "oldPort", deprecated: "use the new port" }])]))
}
`
	if err := os.WriteFile(filepath.Join(root, "main.bork"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "app")
	if err := Build(root, exe); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name        string
		args        []string
		handler     bool
		stderrWarn  bool
		stdoutError bool
	}{
		{name: "success", args: []string{"serve", "--old-port", "81"}, handler: true, stderrWarn: true},
		{name: "help", args: []string{"serve", "--old-port", "81", "--help"}, stderrWarn: true},
		{name: "failure", args: []string{"serve", "--old-port", "bad"}, stdoutError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(exe, tt.args...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("run: %v\n%s\n%s", err, &stdout, &stderr)
			}
			if strings.Contains(stdout.String(), "handler") != tt.handler {
				t.Errorf("handler invocation: %s", &stdout)
			}
			if strings.Contains(stderr.String(), "use the new port") != tt.stderrWarn {
				t.Errorf("stderr warning: %s", &stderr)
			}
			if strings.Contains(stdout.String(), "Error {") != tt.stdoutError || strings.Contains(stdout.String(), "use the new port") != tt.stdoutError {
				t.Errorf("stdout warning/error: %s", &stdout)
			}
		})
	}
}
