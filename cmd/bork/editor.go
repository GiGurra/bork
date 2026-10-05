package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/mod/semver"
)

const extensionID = "gigurra.bork"

var editorCLIs = []string{"code", "cursor", "codium"}

type editorRelease struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func editorCommand() *cobra.Command {
	root := &cobra.Command{Use: "editor", Short: "install editor support"}
	install := &cobra.Command{Use: "install", Short: "install an editor extension"}
	var editor string
	vscode := &cobra.Command{
		Use: "vscode", Short: "install the verified release VSIX in VS Code, Cursor, or VSCodium", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cli, err := findEditor(editor)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			client := &http.Client{Timeout: 45 * time.Second}
			path, tag, cleanup, err := downloadVSIX(ctx, client, "https://api.github.com/repos/GiGurra/bork/releases", version())
			if err != nil {
				return err
			}
			defer cleanup()
			run := exec.CommandContext(ctx, cli, "--install-extension", path, "--force")
			run.Stdout, run.Stderr = cmd.OutOrStdout(), cmd.ErrOrStderr()
			if err := run.Run(); err != nil {
				return fmt.Errorf("editor install with %s failed: %w", cli, err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Installed %s from bork %s (%s) using %s\n", extensionID, tag, filepath.Base(path), cli)
			return err
		},
	}
	vscode.Flags().StringVar(&editor, "editor", "", "editor CLI command or path (default: first available code, cursor, codium)")
	install.AddCommand(vscode)
	root.AddCommand(install)
	return root
}

func findEditor(requested string) (string, error) {
	if requested != "" {
		path, err := exec.LookPath(requested)
		if err != nil {
			return "", fmt.Errorf("editor CLI %q is unavailable: %w", requested, err)
		}
		return path, nil
	}
	for _, name := range editorCLIs {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no editor CLI on PATH; enable code, cursor, or codium in your editor, or use --editor <command>")
}

func releaseRequest(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "bork-release-installer")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("cannot download release: %w; check your network connection and GitHub access", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("release download %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if int64(len(body)) > limit {
		return nil, resp.StatusCode, fmt.Errorf("release download exceeds %d bytes", limit)
	}
	return body, resp.StatusCode, nil
}

func downloadVSIX(ctx context.Context, client *http.Client, api, current string) (string, string, func(), error) {
	queries := []string{"latest"}
	if semver.IsValid(current) {
		queries = append([]string{"tags/" + current}, queries...)
	}
	for index, query := range queries {
		data, status, err := releaseRequest(ctx, client, api+"/"+query, 4<<20)
		if status == http.StatusNotFound && index+1 < len(queries) {
			continue
		}
		if err != nil {
			return "", "", nil, err
		}
		var release editorRelease
		if err := json.Unmarshal(data, &release); err != nil {
			return "", "", nil, fmt.Errorf("invalid editor release metadata: %w", err)
		}
		var assetName, assetURL, checksumURL string
		count := 0
		for _, asset := range release.Assets {
			if strings.HasSuffix(asset.Name, ".vsix") {
				assetName, assetURL = asset.Name, asset.URL
				count++
			}
			if asset.Name == "checksums.txt" {
				checksumURL = asset.URL
			}
		}
		if count == 0 && index+1 < len(queries) {
			continue
		}
		if count != 1 || checksumURL == "" {
			return "", "", nil, fmt.Errorf("bork release %s must contain one VSIX and checksums.txt", release.Tag)
		}
		if filepath.Base(assetName) != assetName || strings.ContainsAny(assetName, "\\/") {
			return "", "", nil, fmt.Errorf("invalid VSIX asset name %q", assetName)
		}
		hashes, _, err := releaseRequest(ctx, client, checksumURL, 1<<20)
		if err != nil {
			return "", "", nil, err
		}
		expected := ""
		for _, line := range strings.Split(string(hashes), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == assetName {
				if expected != "" {
					return "", "", nil, fmt.Errorf("duplicate checksum for %s", assetName)
				}
				expected = fields[0]
			}
		}
		digest, err := hex.DecodeString(expected)
		if err != nil || len(digest) != sha256.Size {
			return "", "", nil, fmt.Errorf("release checksum for %s is missing or invalid", assetName)
		}
		vsix, _, err := releaseRequest(ctx, client, assetURL, 32<<20)
		if err != nil {
			return "", "", nil, err
		}
		actual := sha256.Sum256(vsix)
		if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
			return "", "", nil, fmt.Errorf("checksum verification failed for %s; extension was not installed", assetName)
		}
		dir, err := os.MkdirTemp("", "bork-editor-")
		if err != nil {
			return "", "", nil, err
		}
		cleanup := func() { _ = os.RemoveAll(dir) }
		path := filepath.Join(dir, assetName)
		if err := os.WriteFile(path, vsix, 0600); err != nil {
			cleanup()
			return "", "", nil, err
		}
		return path, release.Tag, cleanup, nil
	}
	return "", "", nil, fmt.Errorf("no release VSIX is available")
}

// Offer an explicit refresh after upgrading; querying installed extensions is local.
func offerEditorRefresh(ctx context.Context, out io.Writer, compiler string) {
	command := "\"" + compiler + "\""
	if runtime.GOOS != "windows" {
		command = "'" + strings.ReplaceAll(compiler, "'", "'\"'\"'") + "'"
	}
	for _, name := range editorCLIs {
		cli, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		data, err := exec.CommandContext(queryCtx, cli, "--list-extensions").Output()
		cancel()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.EqualFold(strings.TrimSpace(line), extensionID) {
				_, _ = fmt.Fprintf(out, "Refresh the installed Bork extension: %s editor install vscode --editor %s\n", command, name)
				break
			}
		}
	}
}
