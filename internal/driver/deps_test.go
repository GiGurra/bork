package driver

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

// Both drivers must compile in a generated module, independent of bork's own
// go.mod. A warm module cache must also support an explicitly offline build.
func TestStandardGoDependencies(t *testing.T) {
	t.Parallel()
	source := []byte(`package main
import (
 "database/sql"
 "fmt"
 _ "github.com/jackc/pgx/v5/stdlib"
 _ "modernc.org/sqlite"
)
func main() {
 db, err := sql.Open("sqlite", ":memory:")
 if err != nil { panic(err) }
 defer db.Close()
 var result int
 if err := db.QueryRow("SELECT 42").Scan(&result); err != nil { panic(err) }
 fmt.Println(result)
}
`)
	files := []*syntax.File{{Package: "bork/sql"}}
	exe := filepath.Join(t.TempDir(), "program")
	module, err := captureGoModule(files, diskSources{})
	if err != nil {
		t.Fatal(err)
	}
	if err := buildGoWithContext(files, source, exe, module, captureGoContext()); err != nil {
		t.Fatal(err)
	}
	offlineSettings := []string{"GOPROXY=off", "GOSUMDB=unsupported.invalid"}
	// This unconfigured checksum DB would fail if the build tried to consult
	// it. Shipped checksums must suffice independently of a cached default DB.
	offline := captureGoContextWithOptions(goContextOptions{settings: offlineSettings, moduleHook: goModuleHook})
	if err := buildGoWithContext(files, source, exe, module, offline); err != nil {
		t.Fatalf("warm cache offline build: %v", err)
	}
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil || string(out) != "42\n" {
		t.Fatalf("driver program: %s %v", out, err)
	}
	cold := captureGoContextWithOptions(goContextOptions{settings: append(offlineSettings, "GOMODCACHE="+t.TempDir()), moduleHook: goModuleHook})
	if err := buildGoWithContext(files, source, exe, module, cold); err == nil || !strings.Contains(err.Error(), "offline builds need the modules in Go's cache") {
		t.Fatalf("cold cache offline build: %v", err)
	}
}
