package driver

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"sync"

	"github.com/GiGurra/bork/internal/check"
)

type embedReadKey struct{ base, displayBase, kind, path string }
type embedEntry struct {
	path string
	mode fs.FileMode
}
type embedContents struct {
	path string
	data []byte
}
type embedRead struct {
	files    []check.EmbeddedFile
	entries  []embedEntry
	contents []embedContents
	err      error
}

// embedSnapshot freezes asset results and inventories observed component kinds,
// recursive directory membership (including empty directories), and file bytes.
// Each replay owns its output data; staging paths are assigned by the caller.
type embedSnapshot struct {
	mu      sync.Mutex
	sources *sourceSnapshot
	reads   map[embedReadKey]embedRead
	read    func(embedReadKey) embedRead
}

func newEmbedSnapshot(sources *sourceSnapshot) *embedSnapshot {
	return &embedSnapshot{sources: sources, reads: map[embedReadKey]embedRead{}, read: readEmbed}
}
func (s *embedSnapshot) capture(request *check.Embedded) ([]check.EmbeddedFile, error) {
	displayBase := filepath.Dir(request.Pos.File)
	base, err := s.sources.readPath(displayBase)
	if err != nil {
		return nil, err
	}
	key := embedReadKey{base, displayBase, request.Kind, request.Path}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.reads[key]
	if !ok {
		value = s.read(key)
		s.reads[key] = value
	}
	err = value.err
	if pe, ok := err.(*fs.PathError); ok {
		err = sourcePathError(err, pe.Path)
	}
	return cloneEmbeddedFiles(value.files), err
}
func readEmbed(key embedReadKey) embedRead {
	var value embedRead
	value.files, value.err = captureEmbedFrom(key.base, key.kind, key.path, func(path string, mode fs.FileMode) {
		value.entries = append(value.entries, embedEntry{path, mode})
	}, func(path string, data []byte) { value.contents = append(value.contents, embedContents{path, data}) })
	// OpenRoot reports the root operand; keep existing caller-facing paths.
	if value.err != nil {
		if pe, ok := value.err.(*fs.PathError); ok && pe.Path == key.base {
			value.err = sourcePathError(value.err, key.displayBase)
		}
	}
	return value
}
func cloneEmbeddedFiles(files []check.EmbeddedFile) []check.EmbeddedFile {
	out := slices.Clone(files)
	for i := range out {
		out[i].Data = slices.Clone(out[i].Data)
	}
	return out
}
func embedReadDigest(value embedRead) [sha256.Size]byte {
	hash := sha256.New()
	add := func(b []byte) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(b)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(b)
	}
	count := func(n uint64) { var data [8]byte; binary.BigEndian.PutUint64(data[:], n); add(data[:]) }
	if value.err != nil {
		add([]byte{1})
		add([]byte(value.err.Error()))
	} else {
		add([]byte{0})
	}
	count(uint64(len(value.entries)))
	for _, entry := range value.entries {
		add([]byte(entry.path))
		count(uint64(entry.mode))
	}
	count(uint64(len(value.contents)))
	for _, content := range value.contents {
		add([]byte(content.path))
		add(content.data)
	}
	count(uint64(len(value.files)))
	for _, file := range value.files {
		add([]byte(file.Name))
	}
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out
}
func (s *embedSnapshot) dependencies() []sourceDependency {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sourceDependency, 0, len(s.reads))
	for key, value := range s.reads {
		out = append(out, sourceDependency{Kind: "embed." + key.kind, Path: key.base + string(filepath.Separator) + key.path, Digest: embedReadDigest(value)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return bytes.Compare(out[i].Digest[:], out[j].Digest[:]) < 0
	})
	return out
}
func (s *embedSnapshot) current() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range s.reads {
		if embedReadDigest(s.read(key)) != embedReadDigest(value) {
			return false
		}
	}
	return true
}
