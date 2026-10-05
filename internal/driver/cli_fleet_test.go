package driver

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIFleetExample(t *testing.T) {
	t.Parallel()
	example := filepath.Join("..", "..", "examples", "cli_fleet")
	exe, err := buildFixtureOutput(t, example)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"catalog.json", "settings.json"} {
		data, err := os.ReadFile(filepath.Join(example, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "broken.json"), []byte(`{"resources":"wrong"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name              string
		args              []string
		env               []string
		want, diagnostic  string
		contains, failure bool
	}{
		{name: "config values reach typed handler", args: []string{"k", "r", "d", "--config", "settings.json", "apply", "web"}, want: "apply team: [\"web\"] (2 replicas)\nDeployment scope closed\n"},
		{name: "CLI overrides environment and config", args: []string{"k", "r", "d", "--config", "settings.json", "-n", "team", "-r", "3", "inspect", "web"}, env: []string{"FLEET_NAMESPACE=dev", "FLEET_REPLICAS=4"}, want: "inspect team: [\"web\"] (3 replicas)\nDeployment scope closed\n"},
		{name: "environment overrides config", args: []string{"k", "r", "d", "--config", "settings.json", "apply"}, env: []string{"FLEET_NAMESPACE=dev", "FLEET_REPLICAS=4"}, want: "apply dev: [] (4 replicas)\nDeployment scope closed\n"},
		{name: "config drives catalog completion", args: []string{"__complete", "k", "r", "d", "--config", "settings.json", "apply", "w"}, want: "web\tWeb service\nworker\tBackground worker\n:36\n"},
		{name: "CLI overrides env for completion", args: []string{"__completeNoDesc", "k", "r", "d", "--config", "settings.json", "-n", "team", "apply", "web", "w"}, env: []string{"FLEET_NAMESPACE=dev"}, want: "web\nworker\n:36\n"},
		{name: "env drives catalog completion", args: []string{"__completeNoDesc", "k", "r", "d", "--config", "settings.json", "apply", ""}, env: []string{"FLEET_NAMESPACE=dev"}, want: "sandbox\n:36\n"},
		{name: "omitted partial namespace uses explicit fallback", args: []string{"__completeNoDesc", "k", "r", "d", "apply", ""}, want: "sandbox\n:36\n"},
		{name: "unrelated invalid partial input does not block suggestions", args: []string{"__completeNoDesc", "k", "r", "d", "-r", "bad", "apply", ""}, want: "sandbox\n:36\n"},
		{name: "catalog IO error is protocol error", args: []string{"__completeNoDesc", "k", "r", "d", "--catalog", "missing.json", "apply", ""}, want: ":1\n", diagnostic: "catalog"},
		{name: "catalog schema error is protocol error", args: []string{"__completeNoDesc", "k", "r", "d", "--catalog", "broken.json", "apply", ""}, want: ":1\n", diagnostic: "resources"},
		{name: "help skips missing sources", args: []string{"k", "r", "d", "--catalog", "missing.json", "--config", "missing.json", "--help"}, want: "Must be positive.", contains: true},
		{name: "script generation skips all handlers and catalog", args: []string{"completion", "bash"}, want: "__start_fleet", contains: true},
		{name: "proven replica field rejects invalid invocation", args: []string{"k", "r", "d", "-r", "0", "apply"}, want: "replicas", contains: true, failure: true},
		{name: "strict action rejects invalid invocation", args: []string{"k", "r", "d", "unknown"}, want: "action", contains: true, failure: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(exe, tt.args...)
			cmd.Dir = root
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "FLEET_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, tt.env...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err := cmd.Run()
			if (err != nil) != tt.failure {
				t.Fatalf("run: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
			}
			if tt.contains {
				if !strings.Contains(stdout.String(), tt.want) {
					t.Fatalf("stdout %q lacks %q", stdout.String(), tt.want)
				}
				if strings.Contains(stdout.String(), "Deployment scope closed") {
					t.Fatal("help/script/failure ran the handler")
				}
			} else if stdout.String() != tt.want {
				t.Fatalf("stdout:\n%q\nwant:\n%q", stdout.String(), tt.want)
			}
			if tt.diagnostic == "" {
				if len(tt.args) > 0 && strings.HasPrefix(tt.args[0], "__complete") {
					if stderr.String() != "Completion ended with directive: ShellCompDirectiveNoFileComp, ShellCompDirectiveKeepOrder\n" {
						t.Fatalf("unexpected completion diagnostics: %s", &stderr)
					}
				} else if stderr.Len() != 0 {
					t.Fatalf("unexpected diagnostics: %s", &stderr)
				}
			} else if !strings.Contains(stderr.String(), tt.diagnostic) {
				t.Fatalf("diagnostics %q lack %q", stderr.String(), tt.diagnostic)
			}
		})
	}
}
