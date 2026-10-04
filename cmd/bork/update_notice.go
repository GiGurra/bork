package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/toolenv"
	"github.com/spf13/cobra"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
	"golang.org/x/term"
)

const updateWorkerArg = "--bork-update-check"

type updateResult struct {
	Version string    `json:"version"`
	Checked time.Time `json:"checked"`
}

func updateEligible(name string, jsonOutput, stdoutTTY, stderrTTY bool, ci, current, setting string) bool {
	if jsonOutput || !stdoutTTY || !stderrTTY || ci != "" || setting != "on" || (!semver.IsValid(current) || module.IsPseudoVersion(current)) {
		return false
	}
	switch name {
	case "check", "build", "install", "fmt", "new", "version":
		return true
	}
	return false
}

// Called only after a successful CLI command. Network work runs in a detached
// process; this path only reads a small cache record and claims today's work.
func maybeUpdateNotice(cmd *cobra.Command) {
	setting, err := toolenv.Value("BORKUPDATECHECK")
	if err != nil {
		return
	}
	asJSON, _ := cmd.Flags().GetBool("json")
	if !updateEligible(cmd.Name(), asJSON, term.IsTerminal(int(os.Stdout.Fd())), term.IsTerminal(int(os.Stderr.Fd())), os.Getenv("CI"), version(), setting) {
		return
	}
	cache, err := toolenv.Value("BORKCACHE")
	if err != nil {
		return
	}
	dir := filepath.Join(cache, "updates")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return
	}
	day := time.Now().UTC().Format("2006-01-02")
	printCachedUpdate(dir, day, version(), cmd.ErrOrStderr())
	if !claimUpdate(filepath.Join(dir, "check-"+day)) {
		return
	}
	image, err := os.Executable()
	if err != nil {
		return
	}
	startUpdateWorker(image, dir)
}

func startUpdateWorker(image, dir string) bool {
	worker := exec.Command(image, updateWorkerArg, dir)
	detachUpdateWorker(worker)
	if err := worker.Start(); err != nil {
		return false
	}
	go func() { _ = worker.Wait() }()
	return true
}

func claimUpdate(path string) bool {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func printCachedUpdate(dir, day, current string, out io.Writer) {
	file, err := os.Open(filepath.Join(dir, "latest.json"))
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	var result updateResult
	if json.NewDecoder(io.LimitReader(file, 65536)).Decode(&result) != nil || result.Checked.UTC().Format("2006-01-02") != day || !semver.IsValid(result.Version) || semver.Compare(result.Version, current) <= 0 {
		return
	}
	if !claimUpdate(filepath.Join(dir, "shown-"+day+"-"+current)) {
		return
	}
	_, _ = fmt.Fprintf(out, "A newer bork is available (%s); run bork upgrade.\n", result.Version)
}

func runUpdateWorker(args []string) bool {
	if len(args) != 3 || args[1] != updateWorkerArg {
		return false
	}
	dir := args[2]
	if !filepath.IsAbs(dir) {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	proxy := os.Getenv("GOPROXY")
	if proxy == "" {
		query := exec.CommandContext(ctx, "go", "env", "GOPROXY")
		query.WaitDelay = time.Second
		query.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GO111MODULE=off", "GOWORK=off")
		data, err := query.Output()
		if err != nil {
			return true
		}
		proxy = strings.TrimSpace(string(data))
	}
	latest := latestProxyVersion(ctx, http.DefaultClient, proxy)
	if latest != "" {
		writeUpdateResult(dir, updateResult{Version: latest, Checked: time.Now().UTC()})
	}
	// Claims are tiny, but retain only the last few days of them.
	entries, _ := os.ReadDir(dir)
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "check-") && !strings.HasPrefix(entry.Name(), "shown-") {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return true
}

func latestProxyVersion(ctx context.Context, client *http.Client, proxy string) string {
	for proxy != "" {
		index := strings.IndexAny(proxy, ",|")
		entry, next, separator := proxy, "", byte(0)
		if index >= 0 {
			entry, next, separator = proxy[:index], proxy[index+1:], proxy[index]
		}
		entry = strings.TrimSpace(entry)
		if entry == "off" || entry == "direct" {
			return ""
		}
		if !strings.HasPrefix(entry, "https://") && !strings.HasPrefix(entry, "http://") {
			if separator == '|' {
				proxy = next
				continue
			}
			return ""
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(entry, "/")+"/github.com/!gi!gurra/bork/@latest", nil)
		status := 0
		if err == nil {
			req.Header.Set("User-Agent", "bork-update-check")
			response, err := client.Do(req)
			if err == nil {
				status = response.StatusCode
				var result struct{ Version string }
				decodeErr := json.NewDecoder(io.LimitReader(response.Body, 65536)).Decode(&result)
				_ = response.Body.Close()
				if status == http.StatusOK && decodeErr == nil && semver.IsValid(result.Version) && semver.Canonical(result.Version) == result.Version {
					return result.Version
				}
			}
		}
		if separator != '|' && status != http.StatusNotFound && status != http.StatusGone {
			return ""
		}
		proxy = next
	}
	return ""
}

func writeUpdateResult(dir string, result updateResult) {
	data, err := json.Marshal(result)
	if err != nil {
		return
	}
	file, err := os.CreateTemp(dir, ".latest-")
	if err != nil {
		return
	}
	name := file.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return
	}
	if file.Close() != nil {
		return
	}
	_ = os.Rename(name, filepath.Join(dir, "latest.json"))
}
