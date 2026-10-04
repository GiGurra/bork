package driver

import (
	"crypto/sha256"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

type goNameReceipt struct {
	Schema      int                          `json:"schema"`
	Paths       []string                     `json:"paths"`
	Names       map[string]string            `json:"names"`
	Directories *sourceReceipt               `json:"directories"`
	Files       map[string][sha256.Size]byte `json:"files"`
}

func (input goNameInput) receipt() (*goNameReceipt, error) {
	if !input.standard || input.inputs == nil || !input.inputs.current() {
		return nil, errUnsupportedGoReceipt
	}
	return input.receiptSnapshot()
}

// receiptSnapshot clones the metadata observations and checks their shape. Its
// owner separately validates this inventory and shared context before publication.
func (input goNameInput) receiptSnapshot() (*goNameReceipt, error) {
	if !input.standard || input.inputs == nil {
		return nil, errUnsupportedGoReceipt
	}
	directories, err := input.inputs.directories.receipt()
	if err != nil {
		return nil, err
	}
	out := &goNameReceipt{Schema: goReceiptSchema, Paths: slices.Clone(input.paths), Names: maps.Clone(input.names), Directories: directories, Files: maps.Clone(input.inputs.files)}
	if _, err := out.restore(input.inputs.context); err != nil {
		return nil, err
	}
	return out, nil
}

// restore retains the positive standard-origin proof's complete immediate file
// inventory. The caller validates current() together with its config receipt.
func (r *goNameReceipt) restore(context *goContextValidation) (*goNameInput, error) {
	if r == nil || r.Schema != goReceiptSchema || context == nil || r.Directories == nil {
		return nil, errUnsupportedGoReceipt
	}
	directories, err := r.Directories.snapshot()
	if err != nil {
		return nil, err
	}
	expected := map[string]bool{}
	wantedDirectories := map[string]bool{}
	for _, path := range r.Paths {
		first, _, _ := strings.Cut(path, "/")
		if path == "" || !utf8.ValidString(path) || strings.Contains(first, ".") || filepath.ToSlash(filepath.Clean(path)) != path || strings.ContainsAny(path, "*\\") || filepath.IsAbs(path) {
			return nil, errUnsupportedGoReceipt
		}
		name, ok := r.Names[path]
		if !ok || name == "" || !utf8.ValidString(name) {
			return nil, errUnsupportedGoReceipt
		}
		dir := filepath.Join(context.root, "src", filepath.FromSlash(path))
		wantedDirectories[dir] = true
		observation, ok := directories.reads[sourceReadKey{"directory", dir}]
		if !ok || observation.err != nil {
			return nil, errUnsupportedGoReceipt
		}
		for _, entry := range observation.entries {
			if !entry.directory {
				expected[filepath.Join(dir, entry.name)] = true
			}
		}
	}
	if len(directories.reads) != len(wantedDirectories) || len(expected) != len(r.Files) {
		return nil, errUnsupportedGoReceipt
	}
	for path := range r.Files {
		if !expected[path] {
			return nil, errUnsupportedGoReceipt
		}
	}
	for path := range r.Names {
		if !slices.Contains(r.Paths, path) {
			return nil, errUnsupportedGoReceipt
		}
	}
	validation := &goNameValidation{context: context, directories: directories, files: maps.Clone(r.Files)}
	return &goNameInput{paths: slices.Clone(r.Paths), names: maps.Clone(r.Names), standard: true, inputs: validation}, nil
}
