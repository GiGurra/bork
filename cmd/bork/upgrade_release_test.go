package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func compilerTestArchive(t *testing.T, goos string, payload []byte) []byte {
	t.Helper()
	var data bytes.Buffer
	if goos == "windows" {
		writer := zip.NewWriter(&data)
		file, err := writer.Create("bork.exe")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		compressed := gzip.NewWriter(&data)
		writer := tar.NewWriter(compressed)
		if err := writer.WriteHeader(&tar.Header{Name: "bork", Mode: 0755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(payload); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return data.Bytes()
}

func TestUpgradeRelease(t *testing.T) {
	for _, scenario := range []string{"success", "bad-checksum", "missing-checksum", "missing-asset", "up-to-date", "bad-tag"} {
		t.Run(scenario, func(t *testing.T) {
			tag := "v0.4.0"
			if scenario == "up-to-date" {
				releaseVersion = tag
				t.Cleanup(func() { releaseVersion = "" })
			}
			archive := compilerTestArchive(t, runtime.GOOS, []byte("verified compiler"))
			checksum := fmt.Sprintf("%x", sha256.Sum256(archive))
			if scenario == "bad-checksum" {
				checksum = strings.Repeat("0", 64)
			}
			asset := compilerArchiveName(tag, runtime.GOOS, runtime.GOARCH)
			var server *httptest.Server
			downloads := 0
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest", "/tags/v0.4.0":
					assets := fmt.Sprintf(`{"name":%q,"browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}`, asset, server.URL+"/archive", server.URL+"/checksums")
					if scenario == "missing-checksum" {
						assets = fmt.Sprintf(`{"name":%q,"browser_download_url":%q}`, asset, server.URL+"/archive")
					}
					if scenario == "missing-asset" {
						assets = ""
					}
					if scenario == "bad-tag" {
						tag = "garbage"
					}
					_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, tag, assets)
				case "/archive":
					downloads++
					_, _ = w.Write(archive)
				case "/checksums":
					_, _ = fmt.Fprintf(w, "%s  %s\n", checksum, asset)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			bin := t.TempDir()
			target := filepath.Join(bin, "bork")
			if runtime.GOOS == "windows" {
				target += ".exe"
			}
			if err := os.WriteFile(target, []byte("old compiler"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("BORKBIN", bin)
			t.Setenv("PATH", t.TempDir()) // Successful prebuilt installs require no Go.
			var output bytes.Buffer
			command := upgradeCommandWithClient(server.Client(), server.URL)
			command.SetOut(&output)
			command.SetErr(&output)
			command.SetArgs(nil)
			err := command.Execute()
			data, readErr := os.ReadFile(target)
			if readErr != nil {
				t.Fatal(readErr)
			}
			switch scenario {
			case "success":
				if err != nil || string(data) != "verified compiler" || !strings.Contains(output.String(), "Verifying checksum") || !strings.Contains(output.String(), "Downloading") || !strings.Contains(output.String(), "Installing to") {
					t.Fatalf("upgrade: %v; data %q; output %s", err, data, &output)
				}
			case "up-to-date":
				if err != nil || downloads != 0 || !strings.Contains(output.String(), "already up to date") || string(data) != "old compiler" {
					t.Fatalf("already current: %v; %s", err, &output)
				}
			case "missing-asset":
				if err == nil || !strings.Contains(err.Error(), "requires Go on PATH") || !strings.Contains(output.String(), "falling back to source") {
					t.Fatalf("fallback: %v; %s", err, &output)
				}
			default:
				if err == nil || string(data) != "old compiler" || strings.Contains(output.String(), "Finding Go") {
					t.Fatalf("integrity failure: %v; data %q; output %s", err, data, &output)
				}
			}
			if strings.ContainsAny(output.String(), "\r\033") {
				t.Fatalf("non-TTY output contains animation: %q", output.String())
			}
			leftovers, err := filepath.Glob(filepath.Join(bin, ".bork-upgrade-*"))
			if err != nil || len(leftovers) > 0 {
				t.Fatalf("staging leftovers: %v %v", leftovers, err)
			}
		})
	}
}

func TestCompilerReleaseFallback(t *testing.T) {
	for _, status := range []int{404, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		_, err := compilerRelease(context.Background(), server.Client(), server.URL, "v0.4.0")
		server.Close()
		if err == nil || releaseUnavailable(err) != (status == 404) {
			t.Fatalf("HTTP %d: %v", status, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := compilerRelease(ctx, http.DefaultClient, "https://example.invalid", "latest")
	if err == nil {
		t.Fatal("canceled request succeeded")
	}
}

func TestExtractCompiler(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		stage := t.TempDir()
		if err := extractCompiler(compilerTestArchive(t, goos, []byte("compiler")), goos, stage); err != nil {
			t.Fatal(err)
		}
		name := "bork"
		if goos == "windows" {
			name += ".exe"
		}
		info, err := os.Stat(filepath.Join(stage, name))
		if err != nil || info.Size() != 8 {
			t.Fatalf("extracted executable: %v", err)
		}
	}
	if err := extractCompiler([]byte("invalid"), "linux", t.TempDir()); err == nil {
		t.Fatal("invalid archive succeeded")
	}
	var data bytes.Buffer
	compressed := gzip.NewWriter(&data)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{Name: "bork", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractCompiler(data.Bytes(), "linux", t.TempDir()); err == nil {
		t.Fatal("symlink executable accepted")
	}
}

func TestHomebrewInstall(t *testing.T) {
	for _, path := range []string{"/opt/homebrew/Cellar/bork/0.4.0/bin/bork", "/usr/local/Cellar/bork/0.4.0/bin/bork", "/home/linuxbrew/.linuxbrew/Cellar/bork/0.4.0/bin/bork"} {
		if !homebrewInstall(path) {
			t.Fatalf("missed Homebrew path %s", path)
		}
	}
	for _, path := range []string{"/usr/local/bin/bork", "/opt/homebrew/Cellar/bork-other/bin/bork"} {
		if homebrewInstall(path) {
			t.Fatalf("false Homebrew path %s", path)
		}
	}
	prefix := t.TempDir()
	t.Setenv("HOMEBREW_PREFIX", prefix)
	executable := filepath.Join(prefix, "Cellar", "bork", "0.4.0", "bin", "bork")
	if err := os.MkdirAll(filepath.Dir(executable), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(prefix, "bork")
	if err := os.Symlink(executable, link); err != nil {
		t.Skip(err)
	}
	if !homebrewInstall(link) {
		t.Fatal("missed Homebrew symlink")
	}
}
