package toolenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(dir, "cache"))
	for _, name := range Names() {
		t.Setenv(name, "")
	}
}

func TestSettingsPrecedenceAndPersistence(t *testing.T) {
	isolate(t)
	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	setting, err := Lookup("BORKCACHE")
	base, _ := os.UserCacheDir()
	if err != nil || setting != (Setting{filepath.Join(base, "bork"), "default"}) {
		t.Fatalf("default: %+v, %v", setting, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("reading default created config: %v", err)
	}
	saved := filepath.Join(t.TempDir(), "saved cache")
	bin := filepath.Join(t.TempDir(), "bin")
	if err := Update([]string{"BORKCACHE=" + saved, "BORKBIN=" + bin, "BORK_CACHE=off"}, nil); err != nil {
		t.Fatal(err)
	}
	setting, err = Lookup("BORKCACHE")
	if err != nil || setting != (Setting{saved, "config"}) {
		t.Fatalf("saved: %+v, %v", setting, err)
	}
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("BORKCACHE", explicit)
	setting, err = Lookup("BORKCACHE")
	if err != nil || setting != (Setting{explicit, "environment"}) {
		t.Fatalf("environment: %+v, %v", setting, err)
	}
	if err := Update(nil, []string{"BORKCACHE"}); err != nil {
		t.Fatal(err)
	}
	if value, err := Value("BORKCACHE"); err != nil || value != explicit {
		t.Fatalf("unset modified environment: %q %v", value, err)
	}
	t.Setenv("BORKCACHE", "")
	if value, err := Value("BORKCACHE"); err != nil || value != filepath.Join(base, "bork") {
		t.Fatalf("unset did not restore default: %q %v", value, err)
	}
	if value, err := Value("BORKBIN"); err != nil || value != bin {
		t.Fatalf("unrelated setting lost: %q %v", value, err)
	}
	if value, err := Value("BORK_CACHE"); err != nil || value != "off" {
		t.Fatalf("cache toggle lost: %q %v", value, err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatalf("settings permissions: %v %v", st, err)
		}
	}
}

func TestSettingsRejectInvalidEditsWithoutRewriting(t *testing.T) {
	isolate(t)
	if err := Update([]string{"BORK_CACHE=off"}, nil); err != nil {
		t.Fatal(err)
	}
	path, _ := ConfigPath()
	before, _ := os.ReadFile(path)
	for _, assignments := range [][]string{{"UNKNOWN=on"}, {"BORKCACHE=relative"}, {"BORKBIN=relative"}, {"BORK_CACHE=yes"}, {"BORK_CACHE=on", "BORKBIN=relative"}, {"BORKCACHE"}, {"BORKCACHE=/tmp\nbad"}} {
		if err := Update(assignments, nil); err == nil {
			t.Fatalf("accepted %q", assignments)
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatalf("invalid edit rewrote settings: %s", after)
		}
	}
	if err := Update(nil, []string{"BORK_CACHE", "UNKNOWN"}); err == nil {
		t.Fatal("accepted unknown unset")
	}
	t.Setenv("BORK_CACHE", "invalid")
	if _, err := Lookup("BORK_CACHE"); err == nil {
		t.Fatal("accepted invalid environment toggle")
	}
	if _, err := Lookup("UNKNOWN"); err == nil {
		t.Fatal("accepted unknown lookup")
	}
}

func TestSettingsMalformedAndUnknownKeys(t *testing.T) {
	isolate(t)
	path, _ := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"{", "null", "[]", `{"BORK_CACHE":42}`} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Lookup("BORK_CACHE"); err == nil {
			t.Fatalf("accepted malformed settings %s", bad)
		}
		if err := Update([]string{"BORK_CACHE=on"}, nil); err == nil {
			t.Fatalf("overwrote malformed settings %s", bad)
		}
	}
	t.Setenv("BORK_CACHE", "off")
	if setting, err := Lookup("BORK_CACHE"); err != nil || setting.Source != "environment" {
		t.Fatalf("explicit environment should win: %+v %v", setting, err)
	}
	if err := os.WriteFile(path, []byte(`{"FUTURE_SETTING":"keep","BORK_CACHE":"off"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Update([]string{"BORK_CACHE=on"}, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil || values["FUTURE_SETTING"] != "keep" {
		t.Fatalf("lost future setting: %s %v", data, err)
	}
}

func TestGoBinDefaults(t *testing.T) {
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	for _, tt := range []struct{ gobin, gopath, want string }{{first, second, first}, {"", first, filepath.Join(first, "bin")}, {"", strings.Join([]string{first, second}, string(os.PathListSeparator)), filepath.Join(first, "bin")}} {
		value, err := goBin(tt.gobin, tt.gopath)
		if err != nil || value != tt.want {
			t.Fatalf("goBin(%q,%q): %q %v", tt.gobin, tt.gopath, value, err)
		}
	}
	if _, err := goBin("", ""); err == nil {
		t.Fatal("accepted empty GOPATH")
	}
}

func TestBorkBinUsesEffectiveGoSettings(t *testing.T) {
	isolate(t)
	config := filepath.Join(t.TempDir(), "goenv")
	first, second := filepath.Join(t.TempDir(), "first"), filepath.Join(t.TempDir(), "second")
	t.Setenv("GOENV", config)
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "")
	t.Setenv("GOTOOLCHAIN", "local")
	write := func(gobin string) {
		t.Helper()
		data := "GOBIN=" + gobin + "\nGOPATH=" + strings.Join([]string{first, second}, string(os.PathListSeparator)) + "\n"
		if err := os.WriteFile(config, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(second)
	if setting, err := Lookup("BORKBIN"); err != nil || setting != (Setting{second, "default"}) {
		t.Fatalf("persisted GOBIN: %+v %v", setting, err)
	}
	write("")
	if value, err := Value("BORKBIN"); err != nil || value != filepath.Join(first, "bin") {
		t.Fatalf("first GOPATH entry: %q %v", value, err)
	}
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv("BORKBIN", explicit)
	t.Setenv("PATH", t.TempDir())
	if value, err := Value("BORKBIN"); err != nil || value != explicit {
		t.Fatalf("explicit setting should not need Go: %q %v", value, err)
	}
}
