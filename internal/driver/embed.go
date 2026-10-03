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

func captureEmbeds(info *check.Info, diags *diag.List) {
	for i, request := range info.Embeds {
		files, err := captureEmbed(request)
		if err != nil {
			diags.AddCode(request.Pos, "embed.asset", "cannot embed %q: %v", request.Path, err)
			continue
		}
		for j := range files {
			files[j].StagePath = fmt.Sprintf("_bork_embed/e%df%d.bin", i, j)
		}
		request.Files = files
	}
}

func captureEmbed(request *check.Embedded) ([]check.EmbeddedFile, error) {
	name := request.Path
	if name == "." || !fs.ValidPath(name) || strings.ContainsAny(name, "\\:\x00") {
		return nil, fmt.Errorf("path must be relative to the source package, without parent segments, backslashes, colons or NULs")
	}
	root, err := os.OpenRoot(filepath.Dir(request.Pos.File))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// Reject symlinks in every component, including directory ancestors.
	parts := strings.Split(name, "/")
	for i := range parts {
		entry, err := root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return nil, err
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symbolic links are not supported")
		}
	}
	entry, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if request.Kind != "Directory" {
		if !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("expected a regular file")
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return nil, err
		}
		if request.Kind == "ReadString" && !utf8.Valid(data) {
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
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("%s: only regular files and directories can be embedded", current)
		}
		data, err := root.ReadFile(current)
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
