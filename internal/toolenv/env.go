// Package toolenv resolves and persists the compiler's user settings.
package toolenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Names is the complete set of supported user settings, in display order.
func Names() []string { return []string{"BORKCACHE", "BORKBIN", "BORK_CACHE"} }

// Setting includes the origin of an effective value.
type Setting struct {
	Value  string `json:"value"`
	Source string `json:"source"`
}

func known(name string) bool {
	for _, key := range Names() {
		if name == key {
			return true
		}
	}
	return false
}

// ConfigPath is the per-user settings file. Reading settings never creates it.
func ConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "bork", "env.json"), nil
}

func readConfig() (map[string]string, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bork settings: %w", err)
	}
	var values map[string]string
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("read bork settings %s: %w", path, err)
	}
	if values == nil {
		return nil, fmt.Errorf("bork settings %s must be a JSON object", path)
	}
	return values, nil
}

func validate(name, value string) error {
	if !known(name) {
		return fmt.Errorf("unknown bork setting %q", name)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s contains a newline or NUL", name)
	}
	if value == "" {
		return nil
	}
	if name == "BORK_CACHE" {
		if value != "on" && value != "off" {
			return fmt.Errorf("BORK_CACHE must be on or off")
		}
	} else if !filepath.IsAbs(value) {
		return fmt.Errorf("%s must be an absolute path", name)
	}
	return nil
}

// Lookup resolves environment, then saved settings, then the platform default.
// Empty values select the next source. Explicit values are never relocated.
func Lookup(name string) (Setting, error) {
	if !known(name) {
		return Setting{}, fmt.Errorf("unknown bork setting %q", name)
	}
	if value := os.Getenv(name); value != "" {
		if err := validate(name, value); err != nil {
			return Setting{}, err
		}
		return Setting{value, "environment"}, nil
	}
	saved, err := readConfig()
	if err != nil {
		return Setting{}, err
	}
	if value := saved[name]; value != "" {
		if err := validate(name, value); err != nil {
			return Setting{}, err
		}
		return Setting{value, "config"}, nil
	}
	value, err := defaultValue(name)
	if err != nil {
		return Setting{}, err
	}
	if err := validate(name, value); err != nil {
		return Setting{}, err
	}
	return Setting{value, "default"}, nil
}

func Value(name string) (string, error) {
	setting, err := Lookup(name)
	return setting.Value, err
}

func defaultValue(name string) (string, error) {
	switch name {
	case "BORKCACHE":
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, "bork"), nil
	case "BORKBIN":
		// go env includes Go's persisted user settings, not just process variables.
		output, err := exec.Command("go", "env", "-json", "GOBIN", "GOPATH").Output()
		if err != nil {
			return "", fmt.Errorf("resolve BORKBIN with go env: %w", err)
		}
		var values map[string]string
		if err := json.Unmarshal(output, &values); err != nil {
			return "", err
		}
		return goBin(values["GOBIN"], values["GOPATH"])
	case "BORK_CACHE":
		return "on", nil
	}
	return "", fmt.Errorf("unknown bork setting %q", name)
}

func goBin(gobin, gopath string) (string, error) {
	if gobin != "" {
		return gobin, nil
	}
	paths := filepath.SplitList(gopath)
	if len(paths) == 0 || paths[0] == "" {
		return "", errors.New("go env GOPATH is empty")
	}
	return filepath.Join(paths[0], "bin"), nil
}

// Update validates every requested edit before atomically replacing the file.
// Unknown saved keys are retained for compatibility with newer compilers.
func Update(assignments, unset []string) error {
	edits := map[string]string{}
	for _, assignment := range assignments {
		name, value, ok := strings.Cut(assignment, "=")
		if !ok {
			return fmt.Errorf("expected VAR=value, got %q", assignment)
		}
		if err := validate(name, value); err != nil {
			return err
		}
		edits[name] = value
	}
	for _, name := range unset {
		if !known(name) {
			return fmt.Errorf("unknown bork setting %q", name)
		}
	}
	values, err := readConfig()
	if err != nil {
		return err
	}
	for name, value := range edits {
		values[name] = value
	}
	for _, name := range unset {
		delete(values, name)
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".env-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
