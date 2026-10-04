package format

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilesPreserveModuleCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	path := filepath.Join(cache, "example.com/lib@v1.0.0", "lib.bork")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	source := "fn Value():Int{7}\n"
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Files([]string{path}, false); err == nil {
		t.Fatal("formatted downloaded source")
	}
	if _, err := Files([]string{cache}, false); err == nil {
		t.Fatal("formatted downloaded directory")
	}
	alias := filepath.Join(t.TempDir(), "linked.bork")
	if err := os.Symlink(path, alias); err == nil {
		if _, err := Files([]string{alias}, false); err == nil {
			t.Fatal("formatted cache through a symlink")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != source {
		t.Fatalf("downloaded source changed: %q, %v", data, err)
	}
}
