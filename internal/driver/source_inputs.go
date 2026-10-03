package driver

import (
	"crypto/sha256"
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"sync"
)

// sourceReader describes only the filesystem data source loading observes.
// It deliberately excludes Go metadata, assets and execution inputs: a source
// snapshot alone is not a semantic compilation-cache key.
type sourceReader interface {
	readFile(string) ([]byte, error)
	isDirectory(string) (bool, error)
	directory(string) ([]sourceEntry, error)
	absolute(string) (string, error)
	workingDirectory() (string, error)
}

type sourceEntry struct {
	name      string
	directory bool
}

type diskSources struct{}

func (diskSources) readFile(name string) ([]byte, error) { return os.ReadFile(name) }
func (diskSources) isDirectory(name string) (bool, error) {
	info, err := os.Stat(name)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}
func (diskSources) directory(name string) ([]sourceEntry, error) {
	entries, err := os.ReadDir(name)
	if err != nil {
		return nil, err
	}
	out := make([]sourceEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, sourceEntry{entry.Name(), entry.IsDir()})
	}
	return out, nil
}
func (diskSources) absolute(name string) (string, error) { return filepath.Abs(name) }
func (diskSources) workingDirectory() (string, error)    { return os.Getwd() }

type sourceReadKey struct{ kind, path string }
type sourceRead struct {
	data      []byte
	entries   []sourceEntry
	directory bool
	err       error
}

// sourceSnapshot owns frozen reads for one source-load attempt. The loader gets
// copies so it cannot mutate recorded bytes or membership. Missing module files
// and failed lookups are records too, not just successful source reads.
type sourceSnapshot struct {
	mu     sync.Mutex
	disk   sourceReader
	cwd    string
	cwdErr error
	reads  map[sourceReadKey]sourceRead
}

func newSourceSnapshot() *sourceSnapshot {
	disk := diskSources{}
	cwd, err := disk.workingDirectory()
	return &sourceSnapshot{disk: disk, cwd: cwd, cwdErr: err, reads: map[sourceReadKey]sourceRead{}}
}
func (s *sourceSnapshot) workingDirectory() (string, error) { return s.cwd, s.cwdErr }
func (s *sourceSnapshot) absolute(name string) (string, error) {
	if filepath.IsAbs(name) {
		return filepath.Clean(name), nil
	}
	if windowsSourceOperand(name) {
		return filepath.Abs(name)
	}
	if s.cwdErr != nil {
		return "", s.cwdErr
	}
	return filepath.Join(s.cwd, name), nil
}

// Preserve path components for IO: cleaning link/../file changes Unix symlink
// resolution and can hide nonexistent intermediate components. Module identity
// still uses absolute(), matching the existing lexical normalization there.
func (s *sourceSnapshot) readPath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return name, nil
	}
	if s.cwdErr != nil {
		return "", s.cwdErr
	}
	if windowsSourceOperand(name) {
		return filepath.Abs(name) // resolve drive-relative/rooted Windows operands
	}
	return s.cwd + string(filepath.Separator) + name, nil
}

// Windows rooted/drive-relative paths use the OS drive context, rather than
// simple concatenation with cwd. No Session reuse is enabled here; a future
// replayable compilation context must capture that drive context too.
func windowsSourceOperand(name string) bool {
	return filepath.VolumeName(name) != "" || runtime.GOOS == "windows" && len(name) != 0 && (name[0] == '/' || name[0] == '\\')
}
func (s *sourceSnapshot) read(kind, name string) (sourceRead, error) {
	absolute, err := s.readPath(name)
	if err != nil {
		return sourceRead{}, err
	}
	key := sourceReadKey{kind, absolute}
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.reads[key]; ok {
		return value, nil
	}
	value := readSource(s.disk, key)
	s.reads[key] = value
	return value, nil
}
func readSource(disk sourceReader, key sourceReadKey) sourceRead {
	var out sourceRead
	switch key.kind {
	case "file":
		out.data, out.err = disk.readFile(key.path)
	case "stat":
		out.directory, out.err = disk.isDirectory(key.path)
	case "directory":
		out.entries, out.err = disk.directory(key.path)
	}
	return out
}
func sourcePathError(err error, name string) error {
	if pe, ok := err.(*fs.PathError); ok {
		copy := *pe
		copy.Path = name
		return &copy
	}
	return err
}
func (s *sourceSnapshot) readFile(name string) ([]byte, error) {
	value, err := s.read("file", name)
	if err != nil {
		return nil, err
	}
	return slices.Clone(value.data), sourcePathError(value.err, name)
}
func (s *sourceSnapshot) isDirectory(name string) (bool, error) {
	value, err := s.read("stat", name)
	if err != nil {
		return false, err
	}
	return value.directory, sourcePathError(value.err, name)
}
func (s *sourceSnapshot) directory(name string) ([]sourceEntry, error) {
	value, err := s.read("directory", name)
	if err != nil {
		return nil, err
	}
	return slices.Clone(value.entries), sourcePathError(value.err, name)
}

// sourceDependency is the inventory of reads needed to replay loading. It is
// intentionally not the semantic import/interface dependency manifest.
type sourceDependency struct {
	Kind   string
	Path   string
	Digest [sha256.Size]byte
}

func (s *sourceSnapshot) dependencies() []sourceDependency {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sourceDependency, 0, len(s.reads))
	for key, value := range s.reads {
		out = append(out, sourceDependency{key.kind, key.path, sourceReadDigest(value)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out
}
func sourceReadDigest(value sourceRead) [sha256.Size]byte {
	hash := sha256.New()
	add := func(data []byte) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(data)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(data)
	}
	if value.err != nil {
		add([]byte{1})
		add([]byte(value.err.Error()))
	} else {
		add([]byte{0})
	}
	add(value.data)
	if value.directory {
		add([]byte{1})
	} else {
		add([]byte{0})
	}
	for _, entry := range value.entries {
		add([]byte(entry.name))
		if entry.directory {
			add([]byte{1})
		} else {
			add([]byte{0})
		}
	}
	var out [sha256.Size]byte
	copy(out[:], hash.Sum(nil))
	return out
}

// current compares actual bytes/membership, never mtimes or sizes. Source
// loading uses it to detect edits during capture. Later Session publication must
// validate this inventory together with its other compiler-input inventories.
func (s *sourceSnapshot) current() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cwd, err := s.disk.workingDirectory()
	if cwd != s.cwd || errorText(err) != errorText(s.cwdErr) {
		return false
	}
	for key, value := range s.reads {
		if sourceReadDigest(readSource(s.disk, key)) != sourceReadDigest(value) {
			return false
		}
	}
	return true
}
func errorText(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}
