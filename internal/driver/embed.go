package driver

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

func captureEmbedsSnapshot(info *check.Info, diags *diag.List, sources *sourceSnapshot) *embedSnapshot {
	return captureEmbedsFrom(info, diags, sources, newEmbedSnapshot)
}

func captureEmbedsFrom(info *check.Info, diags *diag.List, sources *sourceSnapshot, capture func(*sourceSnapshot) *embedSnapshot) *embedSnapshot {
	for range 2 {
		inputs := capture(sources)
		captured := make([][]check.EmbeddedFile, len(info.Embeds))
		failures := make([]error, len(info.Embeds))
		for i, request := range info.Embeds {
			captured[i], failures[i] = inputs.capture(request)
		}
		if !inputs.current() {
			continue
		}
		for i, request := range info.Embeds {
			if err := failures[i]; err != nil {
				diags.AddCode(request.Pos, "embed.asset", "cannot embed %q: %v", request.Path, err)
				continue
			}
			for j := range captured[i] {
				captured[i][j].StagePath = fmt.Sprintf("_bork_embed/e%df%d.bin", i, j)
			}
			request.Files = captured[i]
		}
		return inputs
	}
	for _, request := range info.Embeds {
		diags.AddCode(request.Pos, "embed.asset", "embedded asset inputs changed while loading; retry the command")
	}
	return nil
}

func captureEmbed(request *check.Embedded) ([]check.EmbeddedFile, error) {
	return captureEmbedFrom(filepath.Dir(request.Pos.File), request.Kind, request.Path, nil, nil)
}

func captureEmbedFrom(base, kind, name string, observe func(string, fs.FileMode), observeData func(string, []byte)) ([]check.EmbeddedFile, error) {
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return nil, fmt.Errorf("path must be relative to the source package, without parent segments, backslashes, colons or NULs")
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// Reject symlinks in every component, including directory ancestors.
	parts := strings.Split(name, "/")
	for i := range parts {
		component := strings.Join(parts[:i+1], "/")
		entry, err := root.Lstat(component)
		if err != nil {
			return nil, err
		}
		if observe != nil {
			observe(component, entry.Mode().Type())
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symbolic links are not supported")
		}
	}
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if kind != "Directory" {
		if !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("expected a regular file")
		}
		data, err := root.ReadFile(name)
		if observeData != nil {
			observeData(name, data)
		}
		if err != nil {
			return nil, err
		}
		if kind == "ReadString" && !utf8.Valid(data) {
			return nil, fmt.Errorf("file is not valid UTF-8; use embed.ReadBytes for binary data")
		}
		return []check.EmbeddedFile{{Data: data}}, nil
	}
	if !entry.IsDir() {
		return nil, fmt.Errorf("expected a directory")
	}
	var files []check.EmbeddedFile
	err = fs.WalkDir(root.FS(), name, func(current string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if observe != nil {
			observe(current, entry.Type())
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s: only regular files and directories can be embedded", current)
		}
		data, err := root.ReadFile(current)
		if observeData != nil {
			observeData(current, data)
		}
		if err != nil {
			return err
		}
		files = append(files, check.EmbeddedFile{Name: strings.TrimPrefix(current, name+"/"), Data: data})
		return nil
	})
	slices.SortFunc(files, func(a, b check.EmbeddedFile) int { return strings.Compare(a.Name, b.Name) })
	return files, err
}

func stageEmbeds(dir string, requests []*check.Embedded) error {
	for _, request := range requests {
		for _, file := range request.Files {
			name := filepath.Join(dir, filepath.FromSlash(file.StagePath))
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(name, file.Data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
