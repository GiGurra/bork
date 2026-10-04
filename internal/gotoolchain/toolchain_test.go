package gotoolchain

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMinimumSetting(t *testing.T) {
	for _, test := range []struct {
		current, setting, want string
		fails                  bool
	}{
		{"go1.27.1", "auto", "auto", false},
		{"go1.26.0", "local", "local", false},
		{"go1.21.13", "auto", "go1.26.0+auto", false},
		{"go1.25.0", "go1.25.0+auto", "go1.26.0+auto", false},
		{"go1.25.0", "path", "go1.26.0+path", false},
		{"go1.25.0", "local", "", true},
		{"go1.25.0", "go1.25.0", "", true},
		{"go1.26rc1", "local", "", true},
		{"go1.20.14", "auto", "", true},
	} {
		got, err := Setting(test.current, test.setting)
		if got != test.want || (err != nil) != test.fails {
			t.Fatalf("Setting(%q,%q) = %q, %v", test.current, test.setting, got, err)
		}
	}
}

func TestQuerySwitchesOlderAutomaticGo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Go uses a shell script")
	}
	tool := filepath.Join(t.TempDir(), "go")
	script := `#!/bin/sh
if [ "$GOTOOLCHAIN" = go1.26.0+auto ]; then
  if [ "$SWITCH_FAIL" = yes ]; then echo 'proxy offline' >&2; exit 1; fi
  echo '{"GOVERSION":"go1.26.0","GOTOOLCHAIN":"go1.26.0+auto","GOROOT":"/downloaded/sdk"}'
else
  echo '{"GOVERSION":"go1.21.13","GOTOOLCHAIN":"auto","GOROOT":"/old/sdk"}'
fi
`
	if err := os.WriteFile(tool, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	values, env, _, err := Query(tool, "", []string{"GOTOOLCHAIN=auto"})
	if err != nil || values["GOROOT"] != "/downloaded/sdk" || values["GOVERSION"] != Minimum || env[len(env)-1] != "GOTOOLCHAIN=go1.26.0+auto" {
		t.Fatalf("values %v env %v err %v", values, env, err)
	}
	_, _, _, err = Query(tool, "", []string{"GOTOOLCHAIN=auto", "SWITCH_FAIL=yes"})
	if err == nil || !strings.Contains(err.Error(), "proxy offline") || !strings.Contains(err.Error(), "network access") {
		t.Fatalf("download failure: %v", err)
	}
}

func TestQueryOfflineCachedSDK(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Go uses shell scripts")
	}
	dir := t.TempDir()
	root, cache := filepath.Join(dir, "bundled"), filepath.Join(dir, "modules")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.env"), []byte("GOTOOLCHAIN=auto\n"), 0600); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(dir, "go")
	old := fmt.Sprintf("#!/bin/sh\necho '%s'\n", fmt.Sprintf(`{"GOVERSION":"go1.21.13","GOTOOLCHAIN":"local","GOROOT":%q,"GOENV":"off","GOMODCACHE":%q,"GOHOSTOS":"linux","GOHOSTARCH":"amd64"}`, root, cache))
	if err := os.WriteFile(tool, []byte(old), 0755); err != nil {
		t.Fatal(err)
	}
	env := []string{"GOPROXY=off", "GOSUMDB=off"}
	_, _, _, err := Query(tool, "", env)
	if err == nil || !strings.Contains(err.Error(), "offline Go SDK go1.26.0 is not cached") {
		t.Fatalf("cold offline: %v", err)
	}
	sdk := filepath.Join(cache, "golang.org", "toolchain@v0.0.1-go1.26.0.linux-amd64", "bin", "go")
	if err := os.MkdirAll(filepath.Dir(sdk), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sdk, []byte("#!/bin/sh\necho '{\"GOVERSION\":\"go1.26.0\",\"GOTOOLCHAIN\":\"local\"}'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	values, gotEnv, selected, err := Query(tool, "", env)
	if err != nil || values["GOVERSION"] != Minimum || selected != sdk || envValue(gotEnv, "GOTOOLCHAIN") != "local" {
		t.Fatalf("cached SDK: %v env %v error %v", values, gotEnv, err)
	}
	_, _, _, err = Query(tool, "", append(env, "GOTOOLCHAIN=path"))
	if err == nil || !strings.Contains(err.Error(), "is not on PATH") {
		t.Fatalf("path policy accepted module cache SDK: %v", err)
	}
	_, _, _, err = Query(tool, "", append(env, "GOTOOLCHAIN=local"))
	if err == nil || !strings.Contains(err.Error(), "GOTOOLCHAIN=local") {
		t.Fatalf("explicit local bypassed: %v", err)
	}
}
