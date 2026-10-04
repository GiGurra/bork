package driver

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

const goExecutionInventoryVersion = 1
const goExecutionFileLimit = 100000
const goExecutionEntryLimit = 16384
const goExecutionContentLimit int64 = 512 << 20
const goExecutionMetadataLimit = 8 << 20

// These owned filesystem observations are one part of an execution closure.
// They do not certify source selection, generated initialization or execution.
// Directory membership and path resolution are validated independently of bytes.
type goExecutionFile struct {
	Path, Resolved string
	Mode           os.FileMode
	Size           int64
	Digest         [sha256.Size]byte
}
type goExecutionDirectory struct {
	Path, Resolved string
	Mode           os.FileMode
	Digest         [sha256.Size]byte
}
type goExecutionInputs struct {
	Files       []goExecutionFile
	Directories []goExecutionDirectory
}
type goExecutionCapture struct {
	entries           map[string][]goExecutionEntry
	includeSteps      int
	digests           map[string][sha256.Size]byte
	assemblyRemaining int64
	inputs            goExecutionInputs
	files             map[string]bool
	directories       map[string]bool
	remaining         int64
	metadata          int
}

func newGoExecutionCapture() *goExecutionCapture {
	return &goExecutionCapture{entries: map[string][]goExecutionEntry{}, files: map[string]bool{}, directories: map[string]bool{}, digests: map[string][sha256.Size]byte{}, remaining: goExecutionContentLimit, assemblyRemaining: goExecutionAssemblyTotalLimit}
}
func (c *goExecutionCapture) reservePath(path string) error {
	if !utf8.ValidString(path) {
		return errors.New("unsupported non-UTF-8 Go execution metadata")
	}
	if len(path) > goExecutionMetadataLimit-c.metadata {
		return errors.New("go execution metadata budget exceeded")
	}
	c.metadata += len(path)
	return nil
}
func readGoExecutionFile(path string, remaining *int64) (goExecutionFile, error) {
	var out goExecutionFile
	if !filepath.IsAbs(path) {
		return out, errors.New("nonabsolute Go execution input")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out, err
	}
	pre, err := os.Stat(path)
	if err != nil {
		return out, err
	}
	if !pre.Mode().IsRegular() {
		return out, errors.New("unsupported go execution file type")
	}
	file, err := openGoExecutionFile(path)
	if err != nil {
		return out, err
	}
	defer func() { _ = file.Close() }()
	before, err := file.Stat()
	if err != nil {
		return out, err
	}
	if !before.Mode().IsRegular() || before.Size() < 0 || before.Size() > *remaining {
		return out, errors.New("go execution file type or content budget exceeded")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(file, *remaining+1))
	if err != nil {
		return out, err
	}
	if n > *remaining {
		return out, errors.New("go execution content budget exceeded")
	}
	*remaining -= n
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || after.Mode() != before.Mode() || n != before.Size() {
		return out, errors.New("go execution input changed while reading")
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(after, current) {
		return out, errors.New("go execution input replaced while reading")
	}
	out = goExecutionFile{Path: path, Resolved: resolved, Mode: before.Mode(), Size: n}
	copy(out.Digest[:], h.Sum(nil))
	return out, nil
}

type goExecutionEntry struct {
	Name, Link string
	Mode       os.FileMode
}

func readGoExecutionDirectory(path string) (goExecutionDirectory, []goExecutionEntry, error) {
	var out goExecutionDirectory
	if !filepath.IsAbs(path) {
		return out, nil, errors.New("nonabsolute Go execution directory")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return out, nil, err
	}
	pre, err := os.Stat(path)
	if err != nil || !pre.IsDir() {
		return out, nil, errors.New("unsupported go execution directory type")
	}
	dir, err := openGoExecutionFile(path)
	if err != nil {
		return out, nil, err
	}
	defer func() { _ = dir.Close() }()
	before, err := dir.Stat()
	if err != nil || !before.IsDir() {
		return out, nil, errors.New("invalid Go execution directory")
	}
	var entries []goExecutionEntry
	metadata := 0
	for {
		batch, readErr := dir.ReadDir(256)
		if len(entries)+len(batch) > goExecutionEntryLimit {
			return out, nil, errors.New("go execution membership budget exceeded")
		}
		for _, entry := range batch {
			info, err := entry.Info()
			if err != nil {
				return out, nil, err
			}
			item := goExecutionEntry{Name: entry.Name(), Mode: info.Mode()}
			if info.Mode()&os.ModeSymlink != 0 {
				item.Link, err = os.Readlink(filepath.Join(path, item.Name))
				if err != nil {
					return out, nil, err
				}
			}
			if !utf8.ValidString(item.Name) || !utf8.ValidString(item.Link) {
				return out, nil, errors.New("unsupported non-UTF-8 Go execution membership")
			}
			size := len(item.Name) + len(item.Link) + 32
			if size > goExecutionMetadataLimit-metadata {
				return out, nil, errors.New("go execution membership metadata budget exceeded")
			}
			metadata += size
			entries = append(entries, item)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return out, nil, readErr
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	encoded, err := json.Marshal(entries)
	if err != nil {
		return out, nil, err
	}
	current, err := os.Stat(path)
	if err != nil || !os.SameFile(before, current) {
		return out, nil, errors.New("go execution directory replaced while reading")
	}
	out = goExecutionDirectory{Path: path, Resolved: resolved, Mode: before.Mode(), Digest: sha256.Sum256(encoded)}
	return out, entries, nil
}
func (c *goExecutionCapture) file(path string) error {
	path = filepath.Clean(path)
	if c.files[path] {
		return nil
	}
	if len(c.files) >= goExecutionFileLimit {
		return errors.New("go execution file count exceeded")
	}
	if err := c.reservePath(path); err != nil {
		return err
	}
	input, err := readGoExecutionFile(path, &c.remaining)
	if err != nil {
		return err
	}
	if err := c.reservePath(input.Resolved); err != nil {
		return err
	}
	c.files[path] = true
	c.digests[path] = input.Digest
	c.inputs.Files = append(c.inputs.Files, input)
	return nil
}
func (c *goExecutionCapture) directory(path string) ([]goExecutionEntry, error) {
	path = filepath.Clean(path)
	if c.directories[path] {
		return c.entries[path], nil
	}
	if len(c.directories) >= goExecutionFileLimit {
		return nil, errors.New("go execution directory count exceeded")
	}

	input, entries, err := readGoExecutionDirectory(path)
	if err != nil {
		return nil, err
	}
	if !c.directories[path] {
		if len(c.directories) >= goExecutionFileLimit {
			return nil, errors.New("go execution directory count exceeded")
		}
		if err := c.reservePath(path); err != nil {
			return nil, err
		}
		if err := c.reservePath(input.Resolved); err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if err := c.reservePath(entry.Name); err != nil {
				return nil, err
			}
			if err := c.reservePath(entry.Link); err != nil {
				return nil, err
			}
			if 32 > goExecutionMetadataLimit-c.metadata {
				return nil, errors.New("go execution membership metadata budget exceeded")
			}
			c.metadata += 32
		}
		c.directories[path] = true
		c.entries[path] = entries
		c.inputs.Directories = append(c.inputs.Directories, input)
	}
	return entries, nil
}

// ancestors records search membership through the declared root. File resolution
// remains independently validated, including symlinked root ancestors.
func (c *goExecutionCapture) ancestors(path, root string) error {
	if !withinGoExecutionRoot(path, root) {
		return errors.New("go execution path outside root")
	}
	for {
		if _, err := c.directory(path); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		path = filepath.Dir(path)
	}
}
func withinGoExecutionRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (c *goExecutionCapture) finish() goExecutionInputs {
	sort.Slice(c.inputs.Files, func(i, j int) bool { return c.inputs.Files[i].Path < c.inputs.Files[j].Path })
	sort.Slice(c.inputs.Directories, func(i, j int) bool { return c.inputs.Directories[i].Path < c.inputs.Directories[j].Path })
	return c.inputs
}
func (inputs *goExecutionInputs) current() bool {
	if inputs == nil || len(inputs.Files) == 0 || len(inputs.Files) > goExecutionFileLimit || len(inputs.Directories) > goExecutionFileLimit {
		return false
	}
	remaining := goExecutionContentLimit
	for _, before := range inputs.Files {
		after, err := readGoExecutionFile(before.Path, &remaining)
		if err != nil || before != after {
			return false
		}
	}
	for _, before := range inputs.Directories {
		after, _, err := readGoExecutionDirectory(before.Path)
		if err != nil || before != after {
			return false
		}
	}
	return true
}
func (inputs *goExecutionInputs) identity() [sha256.Size]byte {
	encoded, _ := json.Marshal(struct {
		Version int
		Inputs  *goExecutionInputs
	}{goExecutionInventoryVersion, inputs})
	return sha256.Sum256(encoded)
}
func (inputs *goExecutionInputs) clone() goExecutionInputs {
	return goExecutionInputs{Files: slices.Clone(inputs.Files), Directories: slices.Clone(inputs.Directories)}
}
func (c *goExecutionCapture) packageDirectory(path, root string) error {
	rootResolved, rootErr := filepath.EvalSymlinks(root)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || rootErr != nil || !withinGoExecutionRoot(resolved, rootResolved) {
		return fmt.Errorf("go package path outside selected root: %s", path)
	}
	entries, err := c.directory(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Mode.IsRegular() || entry.Mode&os.ModeSymlink != 0 {
			file := filepath.Join(path, entry.Name)
			target, err := filepath.EvalSymlinks(file)
			if err != nil || !withinGoExecutionRoot(target, rootResolved) {
				return errors.New("go package input outside selected root")
			}
			info, err := os.Stat(file)
			if err != nil {
				return err
			}
			if info.IsDir() {
				continue
			}
			if err := c.file(file); err != nil {
				return err
			}
		} else if !entry.Mode.IsDir() {
			return errors.New("unsupported Go package input type")
		}
	}
	return c.ancestors(path, root)
}
