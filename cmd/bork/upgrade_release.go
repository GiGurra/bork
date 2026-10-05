package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/semver"
	"golang.org/x/term"
)

var errNoCompilerRelease = errors.New("no matching compiler release")

func releaseUnavailable(err error) bool { return errors.Is(err, errNoCompilerRelease) }

func downloadUnavailable(status int, err error) bool {
	var networkError net.Error
	return status == 0 || status == http.StatusNotFound || errors.Is(err, io.ErrUnexpectedEOF) || errors.As(err, &networkError)
}

func compilerRelease(ctx context.Context, client *http.Client, api, requested string) (editorRelease, error) {
	query := "latest"
	if requested != "latest" {
		query = "tags/" + requested
	}
	data, status, err := releaseRequest(ctx, client, api+"/"+query, 4<<20)
	if err != nil {
		if ctx.Err() != nil {
			return editorRelease{}, ctx.Err()
		}
		// A missing release or unavailable network may still permit an offline
		// source install from Go's cache. Integrity errors never use this fallback.
		if downloadUnavailable(status, err) {
			return editorRelease{}, fmt.Errorf("%w: %v", errNoCompilerRelease, err)
		}
		return editorRelease{}, err
	}
	var release editorRelease
	if err := json.Unmarshal(data, &release); err != nil {
		return release, fmt.Errorf("invalid compiler release metadata: %w", err)
	}
	if !semver.IsValid(release.Tag) || semver.Canonical(release.Tag) != release.Tag || requested != "latest" && requested != release.Tag {
		return release, fmt.Errorf("invalid compiler release version %q", release.Tag)
	}
	return release, nil
}

func compilerArchiveName(tag, goos, goarch string) string {
	extension := ".tar.gz"
	if goos == "windows" {
		extension = ".zip"
	}
	return "bork_" + tag + "_" + goos + "_" + goarch + extension
}

func downloadCompiler(ctx context.Context, client *http.Client, release editorRelease, stage string, progress *upgradeProgress) (bool, error) {
	name := compilerArchiveName(release.Tag, runtime.GOOS, runtime.GOARCH)
	var archiveURL, checksumURL string
	for _, asset := range release.Assets {
		if asset.Name == name {
			if archiveURL != "" {
				return false, fmt.Errorf("duplicate compiler archive %s", name)
			}
			archiveURL = asset.URL
		}
		if asset.Name == "checksums.txt" {
			if checksumURL != "" {
				return false, fmt.Errorf("duplicate checksums.txt")
			}
			checksumURL = asset.URL
		}
	}
	if archiveURL == "" {
		return false, nil
	}
	if checksumURL == "" {
		return false, fmt.Errorf("release %s is missing checksums.txt", release.Tag)
	}
	var expected string
	err := progress.step("Downloading checksums.txt", func() error {
		data, status, err := releaseRequest(ctx, client, checksumURL, 1<<20)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if downloadUnavailable(status, err) {
				return fmt.Errorf("%w: %v", errNoCompilerRelease, err)
			}
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == name {
				if expected != "" {
					return fmt.Errorf("duplicate checksum for %s", name)
				}
				expected = fields[0]
			}
		}
		digest, err := hex.DecodeString(expected)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("release checksum for %s is missing or invalid", name)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	var archive bytes.Buffer
	err = progress.step("Downloading "+name, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "bork-upgrade")
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", errNoCompilerRelease, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: archive HTTP 404", errNoCompilerRelease)
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("compiler archive download: HTTP %d", resp.StatusCode)
		}
		count := &upgradeCounter{progress: progress}
		n, err := io.Copy(io.MultiWriter(&archive, count), io.LimitReader(resp.Body, (64<<20)+1))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: %v", errNoCompilerRelease, err)
		}
		if n > 64<<20 {
			return fmt.Errorf("compiler archive exceeds 64 MiB")
		}
		progress.line(fmt.Sprintf("Downloaded %d bytes", n))
		return nil
	})
	if err != nil {
		return false, err
	}
	err = progress.step("Verifying checksum", func() error {
		actual := sha256.Sum256(archive.Bytes())
		if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
			return fmt.Errorf("checksum verification failed for %s; compiler was not installed", name)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	err = progress.step("Extracting compiler", func() error { return extractCompiler(archive.Bytes(), runtime.GOOS, stage) })
	return err == nil, err
}

// Extract only the executable's exact root entry; never trust archive paths,
// links, permissions, or ancillary files supplied by the archive.
func extractCompiler(data []byte, goos, stage string) error {
	name := "bork"
	if goos == "windows" {
		name += ".exe"
	}
	var binary []byte
	read := func(reader io.Reader) error {
		if binary != nil {
			return fmt.Errorf("duplicate executable in compiler archive")
		}
		var err error
		binary, err = io.ReadAll(io.LimitReader(reader, (128<<20)+1))
		if err != nil {
			return err
		}
		if len(binary) == 0 || len(binary) > 128<<20 {
			return fmt.Errorf("invalid compiler executable size")
		}
		return nil
	}
	if goos == "windows" {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return err
		}
		for _, file := range archive.File {
			if file.Name != name {
				continue
			}
			if !file.Mode().IsRegular() {
				return fmt.Errorf("compiler archive executable must be a regular file")
			}
			reader, err := file.Open()
			if err != nil {
				return err
			}
			err = read(reader)
			closeErr := reader.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	} else {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer func() { _ = reader.Close() }()
		archive := tar.NewReader(io.LimitReader(reader, (128<<20)+1))
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if header.Name != name {
				continue
			}
			if header.Typeflag != tar.TypeReg {
				return fmt.Errorf("compiler archive executable must be a regular file")
			}
			if err := read(archive); err != nil {
				return err
			}
		}
	}
	if binary == nil {
		return fmt.Errorf("compiler archive is missing %s", name)
	}
	return os.WriteFile(filepath.Join(stage, name), binary, 0755)
}

func homebrewInstall(current string) bool {
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		resolved = current
	}
	prefixes := []string{"/opt/homebrew", "/usr/local", "/home/linuxbrew/.linuxbrew"}
	if prefix := os.Getenv("HOMEBREW_PREFIX"); prefix != "" {
		prefixes = append(prefixes, prefix)
	}
	for _, prefix := range prefixes {
		for _, path := range []string{current, resolved} {
			relative, err := filepath.Rel(filepath.Join(prefix, "Cellar", "bork"), path)
			if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				return true
			}
		}
	}
	return false
}

type upgradeProgress struct {
	out   io.Writer
	tty   bool
	mu    sync.Mutex
	bytes atomic.Int64
}

func newUpgradeProgress(out io.Writer) *upgradeProgress {
	file, ok := out.(*os.File)
	return &upgradeProgress{out: out, tty: ok && term.IsTerminal(int(file.Fd())) && os.Getenv("CI") == "" && os.Getenv("TERM") != "dumb"}
}

func (p *upgradeProgress) line(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.tty {
		_, _ = fmt.Fprint(p.out, "\r\033[2K")
	}
	_, _ = fmt.Fprintln(p.out, message)
}

func (p *upgradeProgress) step(message string, work func() error) error {
	p.bytes.Store(0)
	p.line(message + "...")
	start := time.Now()
	done := make(chan struct{})
	stopped := make(chan struct{})
	if p.tty {
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()
			frames := "|/-\\"
			index := 0
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					p.mu.Lock()
					_, _ = fmt.Fprintf(p.out, "\r\033[2K%c %s (%s", frames[index%len(frames)], message, time.Since(start).Round(time.Second))
					if n := p.bytes.Load(); n > 0 {
						_, _ = fmt.Fprintf(p.out, ", %d bytes", n)
					}
					_, _ = fmt.Fprint(p.out, ")")
					p.mu.Unlock()
					index++
				}
			}
		}()
	}
	err := work()
	if p.tty {
		close(done)
		<-stopped
		status := "done"
		if err != nil {
			status = "failed"
		}
		p.line(fmt.Sprintf("%s: %s (%s)", message, status, time.Since(start).Round(time.Second)))
	}
	return err
}

type upgradeCounter struct{ progress *upgradeProgress }

func (c *upgradeCounter) Write(data []byte) (int, error) {
	c.progress.bytes.Add(int64(len(data)))
	return len(data), nil
}

type upgradeOutput struct {
	progress *upgradeProgress
	out      io.Writer
}

func (p *upgradeProgress) writer(out io.Writer) io.Writer { return &upgradeOutput{p, out} }
func (w *upgradeOutput) Write(data []byte) (int, error) {
	w.progress.mu.Lock()
	defer w.progress.mu.Unlock()
	if w.progress.tty {
		_, _ = fmt.Fprint(w.progress.out, "\r\033[2K")
	}
	return w.out.Write(data)
}
