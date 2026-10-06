package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLITimeCodecs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := `import "bork/cli"
import "bork/codec"
import "bork/time"
use codec.Defaults
use time.Codecs
pred Positive(value: time.Duration) { value.nanos > 0 }
type Options = {
 config: Option[String]
 timeout: time.Duration where Positive = time.Duration { nanos: 1000000000 }
 at: time.Instant
 optional: Option[time.Duration]
 delays: List[time.Duration] = []
} derive (codec.Decode)
fn main() {
 println(cli.Run[Options]("app", "Time inputs", (options, s) => {
  println(time.FormatDuration(options.timeout))
  println(time.Format(options.at, time.RFC3339()))
  println(options.optional.map(time.FormatDuration))
  println(options.delays.map(time.FormatDuration))
 }, [cli.Flag { field: "config", configFile: true },
 cli.Flag { field: "timeout", env: "BORK_CLI_TIME_TIMEOUT" },
 cli.Flag { field: "at", env: "BORK_CLI_TIME_AT" }]))
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
		name, config, timeout, at string
		args, want, absent        []string
	}{
		{name: "default", args: []string{"--at", "1970-01-01T00:00:00Z"}, want: []string{"1s\n1970-01-01T00:00:00Z\nOption.None\n[]\nOk"}},
		{name: "cli", args: []string{"--timeout", "1h2m3s", "--at", "1970-01-01T01:00:00.123456789+01:00", "--optional=-250ms", "--delays", "10ms", "--delays", "20ms"}, want: []string{"1h2m3s\n1970-01-01T00:00:00.123456789Z", `Option.Some("-250ms")`, `["10ms", "20ms"]`, "Ok"}},
		{name: "env", timeout: "2s", at: "1970-01-01T00:00:00Z", want: []string{"2s\n1970-01-01T00:00:00Z", "Ok"}},
		{name: "config", config: `{"timeout":"3s","at":"1970-01-01T00:00:00Z","optional":"5ms","delays":["1s","2s"]}`, args: []string{"--config", "config.json"}, want: []string{"3s\n1970-01-01T00:00:00Z", `Option.Some("5ms")`, `["1s", "2s"]`, "Ok"}},
		{name: "precedence", config: `{"timeout":"3s","at":"1970-01-01T00:00:00Z"}`, timeout: "4s", at: "1970-01-01T01:00:00Z", args: []string{"--config", "config.json", "--timeout", "5s"}, want: []string{"5s\n1970-01-01T01:00:00Z", "Ok"}},
		{name: "invalid-duration", args: []string{"--timeout", "bad", "--at", "1970-01-01T00:00:00Z"}, want: []string{".timeout", "invalid duration"}, absent: []string{"Ok"}},
		{name: "overflow-duration", args: []string{"--timeout", "9223372036854775808ns", "--at", "1970-01-01T00:00:00Z"}, want: []string{".timeout", "invalid duration"}, absent: []string{"Ok"}},
		{name: "invalid-instant", args: []string{"--at", "bad"}, want: []string{".at", "Error {"}, absent: []string{"Ok"}},
		{name: "instant-range", args: []string{"--at", "2300-01-01T00:00:00Z"}, want: []string{".at", "outside the Unix nanosecond range"}, absent: []string{"Ok"}},
		{name: "positive-constraint", args: []string{"--timeout=-1s", "--at", "1970-01-01T00:00:00Z"}, want: []string{".timeout", "Positive"}, absent: []string{"Ok"}},
		{name: "invalid-list", args: []string{"--at", "1970-01-01T00:00:00Z", "--delays", "bad"}, want: []string{".delays[0]", "invalid duration"}, absent: []string{"Ok"}},
		{name: "numeric-config", config: `{"timeout":1,"at":0}`, args: []string{"--config", "config.json"}, want: []string{".timeout", "expected a duration string", ".at", "expected an RFC3339 instant string"}, absent: []string{"Ok"}},
		{name: "help", args: []string{"--help"}, want: []string{"--timeout string", "--at string", "--delays", "Ok"}, absent: []string{"Error {"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.config != "" {
				if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tt.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "BORK_CLI_TIME_TIMEOUT="+tt.timeout, "BORK_CLI_TIME_AT="+tt.at)
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
