package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GiGurra/bork/internal/toolenv"
)

func TestUpdateEligibility(t *testing.T) {
	if !updateEligible("build", false, true, true, "", "v0.4.0", "on") {
		t.Fatal("interactive build should be eligible")
	}
	for _, name := range []string{"env", "lsp", "emit", "describe", "completion", "vscode", "upgrade", "run", "script"} {
		if updateEligible(name, false, true, true, "", "v0.4.0", "on") {
			t.Fatalf("machine/program command %s is eligible", name)
		}
	}
	for _, test := range []struct {
		json, stdout, stderr bool
		ci, current, setting string
	}{
		{true, true, true, "", "v0.4.0", "on"}, {false, false, true, "", "v0.4.0", "on"}, {false, true, false, "", "v0.4.0", "on"}, {false, true, true, "1", "v0.4.0", "on"}, {false, true, true, "", "dev", "on"},
		{false, true, true, "", "v0.0.0-20261004120000-abcdef123456", "on"}, {false, true, true, "", "v0.4.0", "off"},
	} {
		if updateEligible("check", test.json, test.stdout, test.stderr, test.ci, test.current, test.setting) {
			t.Fatalf("suppression failed: %+v", test)
		}
	}
	if runUpdateWorker([]string{"bork", "version"}) {
		t.Fatal("ordinary CLI treated as update worker")
	}
}

func TestDailyUpdateClaimsAndNotice(t *testing.T) {
	dir := t.TempDir()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if claimUpdate(filepath.Join(dir, "check-2026-10-04")) {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("daily admission count %d", admitted.Load())
	}
	if !claimUpdate(filepath.Join(dir, "check-2026-10-05")) {
		t.Fatal("next day not admitted")
	}
	checked := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	writeUpdateResult(dir, updateResult{Version: "v0.5.0", Checked: checked})
	var out bytes.Buffer
	printCachedUpdate(dir, "2026-10-05", "v0.4.0", &out)
	if out.Len() != 0 {
		t.Fatalf("stale check produced notice: %s", out.String())
	}
	printCachedUpdate(dir, "2026-10-04", "v0.5.0", &out)
	if out.Len() != 0 {
		t.Fatalf("current version produced notice: %s", out.String())
	}
	printCachedUpdate(dir, "2026-10-04", "v0.4.0", &out)
	if !strings.Contains(out.String(), "v0.5.0") || !strings.Contains(out.String(), "run bork upgrade") {
		t.Fatalf("notice: %s", out.String())
	}
	first := out.String()
	printCachedUpdate(dir, "2026-10-04", "v0.4.0", &out)
	if out.String() != first {
		t.Fatal("notice printed twice")
	}
}

func TestProxyLatestVersion(t *testing.T) {
	for _, test := range []struct {
		status     int
		body, want string
	}{
		{200, `{"Version":"v0.4.2"}`, "v0.4.2"}, {200, `{"Version":"nonsense"}`, ""}, {200, `not json`, ""}, {503, `{"Version":"v0.4.2"}`, ""},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/github.com/!gi!gurra/bork/@latest" {
				t.Errorf("wrong module proxy path: %s", r.URL.Path)
			}
			w.WriteHeader(test.status)
			_, _ = fmt.Fprint(w, test.body)
		}))
		got := latestProxyVersion(context.Background(), server.Client(), server.URL)
		server.Close()
		if got != test.want {
			t.Fatalf("latest %s, want %s", got, test.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := latestProxyVersion(ctx, http.DefaultClient, "https://proxy.golang.org"); got != "" {
		t.Fatal("offline query succeeded")
	}
	for _, proxy := range []string{"off", "direct", "file:///tmp/proxy"} {
		if got := latestProxyVersion(context.Background(), http.DefaultClient, proxy); got != "" {
			t.Fatal("non-HTTP proxy succeeded")
		}
	}
}

func TestProxyFallbackPolicy(t *testing.T) {
	var requests atomic.Int32
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `{"Version":"v0.6.0"}`)
	}))
	defer good.Close()
	var status atomic.Int32
	status.Store(503)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(status.Load())) }))
	defer bad.Close()
	if got := latestProxyVersion(context.Background(), http.DefaultClient, bad.URL+","+good.URL); got != "" || requests.Load() != 0 {
		t.Fatal("comma fell back on server failure")
	}
	if got := latestProxyVersion(context.Background(), http.DefaultClient, bad.URL+"|"+good.URL); got != "v0.6.0" {
		t.Fatal("pipe did not fall back")
	}
	if got := latestProxyVersion(context.Background(), http.DefaultClient, "file:///missing-proxy|"+good.URL); got != "v0.6.0" {
		t.Fatal("pipe did not fall back from unsupported local proxy")
	}
	status.Store(404)
	if got := latestProxyVersion(context.Background(), http.DefaultClient, bad.URL+","+good.URL); got != "v0.6.0" {
		t.Fatal("comma did not fall back on missing module")
	}
}

func TestUpdateWorkerCachesAndSettings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, `{"Version":"v0.6.0"}`) }))
	defer server.Close()
	t.Setenv("GOPROXY", server.URL)
	dir := t.TempDir()
	if !runUpdateWorker([]string{"bork", updateWorkerArg, dir}) {
		t.Fatal("worker argument not recognized")
	}
	data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
	if err != nil || !strings.Contains(string(data), "v0.6.0") {
		t.Fatalf("worker cache %s %v", data, err)
	}
	t.Setenv("BORKUPDATECHECK", "off")
	value, err := toolenv.Value("BORKUPDATECHECK")
	if err != nil || value != "off" {
		t.Fatalf("optout %s %v", value, err)
	}
	t.Setenv("BORKUPDATECHECK", "invalid")
	if _, err := toolenv.Value("BORKUPDATECHECK"); err == nil {
		t.Fatal("accepted invalid setting")
	}
}

func TestUpdateDetachedLifecycle(t *testing.T) {
	if os.Getenv("BORK_UPDATE_PARENT_HELPER") == "1" {
		if !startUpdateWorker(os.Getenv("BORK_UPDATE_CHILD"), os.Getenv("BORK_UPDATE_DIR")) {
			t.Fatal("child failed to start")
		}
		return
	}
	exe := cliExecutable(t, false)
	dir := t.TempDir()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
			_, _ = fmt.Fprint(w, `{"Version":"v0.6.0"}`)
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	parent := exec.Command(helper, "-test.run=^TestUpdateDetachedLifecycle$")
	parent.Env = append(os.Environ(), "BORK_UPDATE_PARENT_HELPER=1", "BORK_UPDATE_CHILD="+exe, "BORK_UPDATE_DIR="+dir, "GOPROXY="+server.URL)
	if out, err := parent.CombinedOutput(); err != nil {
		t.Fatalf("parent failed: %v %s", err, out)
	}
	// Only let the network request complete after its launching parent exits.
	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(dir, "latest.json"))
		if err == nil && strings.Contains(string(data), "v0.6.0") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("detached worker did not publish after its parent exited")
}

func TestUpdateWorkerTimeout(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done(); close(cancelled) }))
	defer server.Close()
	exe := cliExecutable(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	cmd := exec.CommandContext(ctx, exe, updateWorkerArg, dir)
	cmd.Env = append(os.Environ(), "GOPROXY="+server.URL)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) != 0 {
		t.Fatalf("timeout should be quiet: %v %s", err, out)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("worker did not cancel network request")
	}
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); !os.IsNotExist(err) {
		t.Fatalf("timed out worker wrote result: %v", err)
	}
}
