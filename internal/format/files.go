package format

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Files formats .bork files and recursively visits directories. An empty path
// list means the current directory. In check mode no files are written. The
// returned paths identify files that differ from the canonical format.
func Files(paths []string, check bool) ([]string, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	seen := map[string]bool{}
	var files []string
	fromDirectory := map[string]bool{}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if !info.Mode().IsRegular() || !strings.HasSuffix(path, ".bork") {
				return nil, fmt.Errorf("%s: expected a regular .bork file or directory", path)
			}
			files = append(files, path)
			continue
		}
		err = filepath.WalkDir(path, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() && p != path && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "vendor") {
				return filepath.SkipDir
			}
			if entry.Type().IsRegular() && strings.HasSuffix(p, ".bork") {
				files = append(files, p)
				fromDirectory[p] = true
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(files)
	// Invalid files found in directories are reported together while the other
	// files are formatted. Explicit-file errors abort before any writes.
	type edit struct {
		path string
		src  []byte
	}
	var edits []edit
	var changed []string
	var failures []error
	for _, path := range files {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		formatted, err := Source(path, src)
		if err != nil {
			if fromDirectory[path] {
				failures = append(failures, err)
				continue
			}
			return nil, err
		}
		if !bytes.Equal(src, formatted) {
			changed = append(changed, path)
			edits = append(edits, edit{path, formatted})
		}
	}
	if !check {
		for _, e := range edits {
			if err := os.WriteFile(e.path, e.src, 0o644); err != nil {
				return changed, err
			}
		}
	}
	return changed, errors.Join(failures...)
}
