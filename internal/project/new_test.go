package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewPaths(t *testing.T) {
	for _, name := range []string{"nested/project", "with spaces", "Ada's project", "-starter"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := New(path, "default", "example.com/project"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(path, "main.bork")); err != nil {
				t.Fatal(err)
			}
			if err := New(path, "default", "example.com/project"); err == nil {
				t.Fatal("replaced existing directory")
			}
		})
	}
	t.Run("existing file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "project")
		if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := New(path, "default", ""); err == nil {
			t.Fatal("replaced existing file")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "keep" {
			t.Fatalf("file changed: %s (%v)", data, err)
		}
	})
}

func TestNextSteps(t *testing.T) {
	for path, want := range map[string]string{
		"-starter": "cd './-starter'", "-": "cd './-'", "with spaces": "cd './with spaces'", "Ada's": "cd './Ada'\"'\"'s'",
	} {
		steps := NextSteps(path, "default")
		if !strings.Contains(steps, want) {
			t.Fatalf("steps for %q: %s", path, steps)
		}
	}
	if strings.Contains(NextSteps("library", "lib"), "bork run") {
		t.Fatal("library has no main")
	}
}
