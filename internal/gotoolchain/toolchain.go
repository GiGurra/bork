// Package gotoolchain applies the compiler's minimum Go version to subprocesses.
package gotoolchain

import (
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"go/version"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const Minimum = "go1.26.0"

// SelectedTool resolves a switched official SDK. Wrappers keep their own
// executable and remain outside the installed-SDK fast cache contract.
func SelectedTool(tool string, values map[string]string) string {
	path, err := exec.LookPath(tool)
	if err != nil {
		return tool
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "cmd/go" || info.GoVersion == values["GOVERSION"] {
		return tool
	}
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(values["GOROOT"], "bin", name)
}

// Setting preserves explicit constraints and raises automatic defaults only
// when the selected toolchain is too old. Go owns downloading and discovery.
func Setting(current, setting string) (string, error) {
	if version.Compare(current, Minimum) >= 0 {
		return setting, nil
	}
	if version.Compare(current, "go1.21.0") < 0 {
		return "", fmt.Errorf("bork requires %s or newer; install Go 1.21+ to enable automatic toolchain switching (found %s)", Minimum, current)
	}
	if setting == "auto" || strings.HasSuffix(setting, "+auto") {
		return Minimum + "+auto", nil
	}
	if setting == "path" || strings.HasSuffix(setting, "+path") {
		return Minimum + "+path", nil
	}
	return "", fmt.Errorf("bork requires %s or newer, found %s with GOTOOLCHAIN=%s; install a newer Go or enable switching with GOTOOLCHAIN=auto", Minimum, current, setting)
}

// Query returns effective settings for the toolchain that will run Bork's Go
// subprocesses. It honors process and saved GOENV settings via Go itself.
func Query(tool, dir string, env []string) (map[string]string, []string, error) {
	query := func(settings []string) (map[string]string, error) {
		cmd := exec.Command(tool, "env", "-json")
		cmd.Dir, cmd.Env = dir, settings
		data, err := cmd.Output()
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("discover Go toolchain (bork needs %s): %w: %s; check GOTOOLCHAIN and network access for toolchain downloads", Minimum, err, strings.TrimSpace(string(exit.Stderr)))
			}
			return nil, err
		}
		var values map[string]string
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, err
		}
		return values, nil
	}
	// Go's download-based switching requires sumdb even for a cached SDK.
	// Cached-only callers resolve an already installed SDK and invoke it local.
	if envValue(env, "GOPROXY") == "off" && envValue(env, "GOSUMDB") == "off" {
		originalDir := dir
		neutral, err := os.MkdirTemp("", "bork-go-env-")
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = os.RemoveAll(neutral) }()
		dir = neutral
		bundled, err := query(append(append([]string(nil), env...), "GOTOOLCHAIN=local", "GO111MODULE=off", "GOWORK=off"))
		if err != nil {
			return nil, nil, err
		}
		policy := envValue(env, "GOTOOLCHAIN")
		if policy == "" && bundled["GOENV"] != "off" {
			policy = savedSetting(bundled["GOENV"], "GOTOOLCHAIN")
		}
		if policy == "" {
			policy = savedSetting(filepath.Join(bundled["GOROOT"], "go.env"), "GOTOOLCHAIN")
		}
		if policy == "" {
			policy = "local"
		}
		base, _, _ := strings.Cut(policy, "+")
		if base == "local" || base == "auto" || base == "path" {
			base = bundled["GOVERSION"]
		}
		if version.Compare(base, Minimum) < 0 {
			setting, err := Setting(base, policy)
			if err != nil {
				return nil, nil, err
			}
			base, _, _ = strings.Cut(setting, "+")
		}
		if base != bundled["GOVERSION"] {
			candidate, err := exec.LookPath(base)
			if err != nil {
				name := "go"
				if runtime.GOOS == "windows" {
					name += ".exe"
				}
				candidate = filepath.Join(bundled["GOMODCACHE"], "golang.org", "toolchain@v0.0.1-"+base+"."+bundled["GOHOSTOS"]+"-"+bundled["GOHOSTARCH"], "bin", name)
				if _, err := os.Stat(candidate); err != nil {
					return nil, nil, fmt.Errorf("offline Go SDK %s is not cached; run a build with GOTOOLCHAIN=auto while online: %w", base, err)
				}
			}
			tool = candidate
			env = append(append([]string(nil), env...), "GOROOT=")
		}
		dir = originalDir
		env = append(append([]string(nil), env...), "GOTOOLCHAIN=local")
	}
	values, err := query(env)
	if err != nil {
		return nil, nil, err
	}
	setting, err := Setting(values["GOVERSION"], values["GOTOOLCHAIN"])
	if err != nil {
		return nil, nil, err
	}
	if setting != values["GOTOOLCHAIN"] {
		env = append(append([]string(nil), env...), "GOTOOLCHAIN="+setting)
		values, err = query(env)
		if err != nil {
			return nil, nil, err
		}
		if version.Compare(values["GOVERSION"], Minimum) < 0 {
			return nil, nil, fmt.Errorf("go toolchain switching selected %s; bork requires %s", values["GOVERSION"], Minimum)
		}
	}
	return values, env, nil
}

func envValue(env []string, key string) string {
	for index := len(env) - 1; index >= 0; index-- {
		if value, found := strings.CutPrefix(env[index], key+"="); found {
			return value
		}
	}
	return ""
}

func savedSetting(path, key string) string {
	data, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(data), "\n") {
		if value, found := strings.CutPrefix(line, key+"="); found {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
