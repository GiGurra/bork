// Package modcache identifies shared Go dependency sources for editing tools.
package modcache

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Root uses Go's effective setting, including values saved by go env -w.
func Root() string {
	if path := os.Getenv("GOMODCACHE"); path != "" {
		return path
	}
	cmd := exec.Command("go", "env", "GOMODCACHE")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Contains also recognizes paths through symlinks into the module cache.
func Contains(root, path string) bool {
	if root == "" {
		return false
	}
	canonical := func(path string) string {
		abs, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			return resolved
		}
		return abs
	}
	rel, err := filepath.Rel(canonical(root), canonical(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
