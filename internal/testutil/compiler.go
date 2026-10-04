// Package testutil provides isolated compiler fixtures for toolchain tests.
package testutil

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Compiler builds a tiny released module through a local file proxy, giving it
// real Go module version metadata without accessing the network.
func Compiler(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	proxy := filepath.Join(dir, "proxy")
	module := "github.com/GiGurra/bork"
	escaped := "github.com/!gi!gurra/bork"
	mod := []byte("module " + module + "\n\ngo 1.26\n")
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for name, data := range map[string][]byte{
		"go.mod": mod,
		"cmd/bork/main.go": []byte(fmt.Sprintf(`package main
import ("fmt"; "os"; "strings")
func main() { fmt.Printf("fixture %s | args=%%s | selected=%%s | reason=%%s\n", strings.Join(os.Args[1:], " "), os.Getenv("BORK_TOOLCHAIN_SELECTED"), os.Getenv("BORK_TOOLCHAIN_REASON")); if os.Getenv("FIXTURE_EXIT") == "7" { os.Exit(7) } }
`, version)),
	} {
		file, err := writer.Create(module + "@" + version + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	versions := filepath.Join(proxy, escaped, "@v")
	if err := os.MkdirAll(versions, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		version + ".mod":  mod,
		version + ".info": []byte(fmt.Sprintf(`{"Version":%q,"Time":"2026-01-01T00:00:00Z"}`, version)),
		version + ".zip":  archive.Bytes(),
		"list":            []byte(version + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(versions, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(dir, "bin")
	proxyPath := filepath.ToSlash(proxy)
	if !strings.HasPrefix(proxyPath, "/") {
		proxyPath = "/" + proxyPath
	}
	proxyURL := (&url.URL{Scheme: "file", Path: proxyPath}).String()
	cmd := exec.Command("go", "install", module+"/cmd/bork@"+version)
	cmd.Env = append(os.Environ(), "GOBIN="+bin, "GOPROXY="+proxyURL, "GOSUMDB=off", "GOTOOLCHAIN=local", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH, "GOFLAGS=-modcacherw", "GOWORK=off", "GOMODCACHE="+filepath.Join(dir, "modcache"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build release fixture: %v\n%s", err, output)
	}
	name := "bork"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(bin, name)
}
