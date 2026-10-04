package main

import (
	"bytes"
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

func TestEditorReleaseDownload(t *testing.T) {
	for _, scenario := range []struct {
		name, current             string
		missing, noAsset, badHash bool
		want                      string
	}{
		{name: "matching", current: "v0.4.2", want: "v0.4.2"},
		{name: "missing tag fallback", current: "v0.4.1", missing: true, want: "v0.5.0"},
		{name: "old release fallback", current: "v0.4.1", noAsset: true, want: "v0.5.0"},
		{name: "development latest", current: "dev", want: "v0.5.0"},
		{name: "bad hash never falls back", current: "v0.4.2", badHash: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			payload := []byte("a packaged VSIX")
			checksum := fmt.Sprintf("%x", sha256.Sum256(payload))
			if scenario.badHash {
				checksum = strings.Repeat("0", 64)
			}
			var requests []string
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				switch {
				case strings.HasPrefix(r.URL.Path, "/tags/") || r.URL.Path == "/latest":
					tag := strings.TrimPrefix(r.URL.Path, "/tags/")
					if r.URL.Path == "/latest" {
						tag = "v0.5.0"
					} else if scenario.missing {
						w.WriteHeader(404)
						return
					} else if scenario.noAsset {
						_, _ = fmt.Fprint(w, `{"tag_name":"v0.4.1","assets":[]}`)
						return
					}
					_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":"bork.vsix","browser_download_url":%q},{"name":"checksums.txt","browser_download_url":%q}]}`, tag, server.URL+"/bork.vsix", server.URL+"/checksums.txt")
				case r.URL.Path == "/checksums.txt":
					_, _ = fmt.Fprintf(w, "%s  bork.vsix\n", checksum)
				case r.URL.Path == "/bork.vsix":
					_, _ = w.Write(payload)
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			path, tag, cleanup, err := downloadVSIX(context.Background(), server.Client(), server.URL, scenario.current)
			if scenario.badHash {
				if err == nil || !strings.Contains(err.Error(), "checksum verification failed") || strings.Contains(strings.Join(requests, ","), "/latest") {
					t.Fatalf("path %s err %v requests %v", path, err, requests)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tag != scenario.want {
				t.Fatalf("tag %s, want %s", tag, scenario.want)
			}
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("download %q: %v", data, err)
			}
			cleanup()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("cleanup: %v", err)
			}
		})
	}
}

func TestEditorOfflineAndMetadata(t *testing.T) {
	for _, body := range []string{`not json`, `{"tag_name":"v0.4.2","assets":[]}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, body) }))
		_, _, _, err := downloadVSIX(context.Background(), server.Client(), server.URL, "dev")
		server.Close()
		if err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := downloadVSIX(ctx, http.DefaultClient, "https://api.github.com/repos/GiGurra/bork/releases", "dev")
	if err == nil || !strings.Contains(err.Error(), "network connection") {
		t.Fatalf("offline error: %v", err)
	}
}

func TestEditorCLIAndRefreshOffer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake editor uses shell script")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if _, err := findEditor(""); err == nil || !strings.Contains(err.Error(), "no editor CLI") {
		t.Fatalf("missing editor: %v", err)
	}
	if _, err := findEditor("missing"); err == nil {
		t.Fatal("accepted missing requested editor")
	}
	for _, name := range []string{"cursor", "codium"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nif [ \"$1\" = --list-extensions ]; then echo GiGurra.bork; fi\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	path, err := findEditor("")
	if err != nil || filepath.Base(path) != "cursor" {
		t.Fatalf("auto editor: %s %v", path, err)
	}
	path, err = findEditor("codium")
	if err != nil || filepath.Base(path) != "codium" {
		t.Fatalf("explicit editor: %s %v", path, err)
	}
	var output bytes.Buffer
	offerEditorRefresh(context.Background(), &output)
	if !strings.Contains(output.String(), "--editor cursor") || !strings.Contains(output.String(), "--editor codium") {
		t.Fatalf("offer: %s", output.String())
	}
	cmd := editorCommand()
	cmd.SetArgs([]string{"install", "vscode"})
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	// Invalid editor selection fails before any release network request.
	cmd.SetArgs([]string{"install", "vscode", "--editor", "missing"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("missing editor command succeeded")
	}
}
