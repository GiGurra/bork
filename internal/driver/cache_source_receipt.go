package driver

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

const sourceReceiptSchema = 1

var errUnsupportedSourceReceipt = errors.New("source observation cannot be persisted")

// sourceReceipt is an owned encoding of loader observations, not a complete
// semantic cache key. Configuration, Go metadata, assets and executions need
// their own receipts before checked results can be reused.
type sourceReceipt struct {
	Schema   int                 `json:"schema"`
	Platform string              `json:"platform"`
	Cwd      string              `json:"cwd"`
	Reads    []sourceReceiptRead `json:"reads"`
}

type sourceReceiptRead struct {
	Kind      string                `json:"kind"`
	Path      string                `json:"path"`
	Data      []byte                `json:"data,omitempty"`
	Entries   []sourceReceiptEntry  `json:"entries,omitempty"`
	Directory bool                  `json:"directory,omitempty"`
	Missing   *sourceReceiptMissing `json:"missing,omitempty"`
}

type sourceReceiptEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory,omitempty"`
}

type sourceReceiptMissing struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Errno uint64 `json:"errno"`
}

func (s *sourceSnapshot) receipt() (*sourceReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cwdErr != nil || s.driveContext || s.rooted != nil {
		return nil, errUnsupportedSourceReceipt
	}
	out := &sourceReceipt{Schema: sourceReceiptSchema, Platform: runtime.GOOS, Cwd: s.cwd, Reads: make([]sourceReceiptRead, 0, len(s.reads))}
	for key, value := range s.reads {
		read := sourceReceiptRead{Kind: key.kind, Path: key.path, Data: slices.Clone(value.data), Directory: value.directory}
		for _, entry := range value.entries {
			read.Entries = append(read.Entries, sourceReceiptEntry{entry.name, entry.directory})
		}
		if value.err != nil {
			pathError, ok := value.err.(*fs.PathError)
			if !ok || !errors.Is(value.err, fs.ErrNotExist) {
				return nil, errUnsupportedSourceReceipt
			}
			errno, ok := pathError.Err.(syscall.Errno)
			if !ok || pathError.Path != key.path {
				return nil, errUnsupportedSourceReceipt
			}
			read.Missing = &sourceReceiptMissing{pathError.Op, pathError.Path, uint64(errno)}
		}
		out.Reads = append(out.Reads, read)
	}
	sort.Slice(out.Reads, func(i, j int) bool { return sourceReceiptLess(out.Reads[i], out.Reads[j]) })
	if _, err := out.snapshot(); err != nil {
		return nil, err
	}
	return out, nil
}

func sourceReceiptLess(a, b sourceReceiptRead) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Path < b.Path
}

// snapshot restores owned frozen reads. current() still rereads actual bytes
// and membership before any caller can treat the observations as current.
func (r *sourceReceipt) snapshot() (*sourceSnapshot, error) {
	if r == nil || r.Schema != sourceReceiptSchema || r.Platform != runtime.GOOS || !filepath.IsAbs(r.Cwd) || !validReceiptPath(r.Cwd) {
		return nil, errUnsupportedSourceReceipt
	}
	out := &sourceSnapshot{disk: diskSources{}, cwd: r.Cwd, reads: make(map[sourceReadKey]sourceRead, len(r.Reads))}
	for index, read := range r.Reads {
		if index != 0 && !sourceReceiptLess(r.Reads[index-1], read) {
			return nil, fmt.Errorf("duplicate or unordered source receipt: %w", errUnsupportedSourceReceipt)
		}
		if read.Path != "" && !filepath.IsAbs(read.Path) || !validReceiptPath(read.Path) {
			return nil, errUnsupportedSourceReceipt
		}
		value := sourceRead{data: slices.Clone(read.Data), directory: read.Directory}
		switch read.Kind {
		case "file":
			if len(read.Entries) != 0 || read.Directory {
				return nil, errUnsupportedSourceReceipt
			}
		case "stat":
			if len(read.Data) != 0 || len(read.Entries) != 0 {
				return nil, errUnsupportedSourceReceipt
			}
		case "directory":
			if len(read.Data) != 0 || read.Directory {
				return nil, errUnsupportedSourceReceipt
			}
		default:
			return nil, errUnsupportedSourceReceipt
		}
		for position, entry := range read.Entries {
			if entry.Name == "" || entry.Name == "." || entry.Name == ".." || containsSourceSeparator(entry.Name) || !validReceiptPath(entry.Name) || position != 0 && read.Entries[position-1].Name >= entry.Name {
				return nil, errUnsupportedSourceReceipt
			}
			value.entries = append(value.entries, sourceEntry{entry.Name, entry.Directory})
		}
		if read.Missing != nil {
			missing := read.Missing
			errno := syscall.Errno(missing.Errno)
			if uint64(errno) != missing.Errno || missing.Path != read.Path || missing.Op == "" || !validReceiptPath(missing.Op) || !validReceiptPath(missing.Path) || len(read.Data) != 0 || len(read.Entries) != 0 || read.Directory {
				return nil, errUnsupportedSourceReceipt
			}
			value.err = &fs.PathError{Op: missing.Op, Path: missing.Path, Err: errno}
			if !errors.Is(value.err, fs.ErrNotExist) {
				return nil, errUnsupportedSourceReceipt
			}
		}
		out.reads[sourceReadKey{read.Kind, read.Path}] = value
	}
	return out, nil
}

func containsSourceSeparator(name string) bool {
	for _, char := range name {
		if char == '/' || runtime.GOOS == "windows" && char == '\\' {
			return true
		}
	}
	return false
}

// JSON strings replace invalid UTF-8. Decline such paths instead of silently
// validating a different Unix filename after a serialization roundtrip.
func validReceiptPath(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
