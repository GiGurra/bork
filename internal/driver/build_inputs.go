package driver

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
)

const buildFileLimit = 16 << 20
const buildTotalLimit = 64 << 20

type buildReadKey struct{ root, kind, path string }
type buildComponent struct {
	path string
	info fs.FileInfo
}
type buildRead struct {
	value          embedRead
	components     []buildComponent
	limit          int
	failedContents [sha256.Size]byte
}

// buildSnapshot is a private rooted-input inventory, separate from ordinary
// loader reads. It owns bytes and checks component identity, negative reads,
// errors and contents. It is not a complete evaluator/toolchain cache key.
type buildSnapshot struct {
	reads map[buildReadKey]buildRead
}

func readBuildInput(key buildReadKey) buildRead { return readBuildInputLimit(key, buildFileLimit) }

func readBuildInputLimit(key buildReadKey, limit int) (out buildRead) {
	out.limit = limit
	fail := func(err error) buildRead {
		out.value.err = err
		// Invalid input bytes are evidence, not frozen evaluator data. Retain
		// their digest so many oversized/invalid files cannot fill the snapshot.
		if len(out.value.contents) > 0 {
			out.failedContents = sha256.Sum256(out.value.contents[0].data)
			out.value.contents = nil
		}
		return out
	}
	if key.path == "." || !fs.ValidPath(key.path) || strings.ContainsAny(key.path, "\\:\x00") {
		return fail(fmt.Errorf("path must be relative to the module root, without parent segments, backslashes, colons or NULs"))
	}
	observe := func(path string, info fs.FileInfo) {
		out.components = append(out.components, buildComponent{path, info})
		out.value.entries = append(out.value.entries, embedEntry{path, info.Mode()})
	}
	// Established module-root ancestors may be symlinks (for example /tmp on
	// macOS). Capture their identities and the resolved root identity; OpenRoot
	// confines operands, whose components below must contain no symlinks.
	for current := key.root; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return fail(err)
		}
		observe(current, info)
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return fail(fmt.Errorf("module root ancestor is not a directory"))
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	resolvedRoot, err := os.Stat(key.root)
	if err != nil {
		return fail(err)
	}
	observe(filepath.Join(key.root, "."), resolvedRoot)
	root, err := os.OpenRoot(key.root)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = root.Close() }()
	rootInfo, err := root.Stat(".")
	if err != nil {
		return fail(err)
	}
	if !os.SameFile(rootInfo, resolvedRoot) {
		return fail(fmt.Errorf("module root changed while opening"))
	}
	parts := strings.Split(key.path, "/")
	for i := range parts {
		component := strings.Join(parts[:i+1], "/")
		info, err := root.Lstat(component)
		if err != nil {
			return fail(err)
		}
		observe(component, info)
		if info.Mode()&os.ModeSymlink != 0 {
			return fail(fmt.Errorf("symbolic links are not supported"))
		}
		if i < len(parts)-1 && !info.IsDir() {
			return fail(fmt.Errorf("path component is not a directory"))
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return fail(fmt.Errorf("expected a regular file"))
		}
	}
	file, err := openBuildFile(root, key.path)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = file.Close() }()
	// Verify the descriptor is still the regular file inspected above.
	info, err := file.Stat()
	if err != nil {
		return fail(err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(info, out.components[len(out.components)-1].info) {
		return fail(fmt.Errorf("build input changed while opening"))
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	out.value.contents = append(out.value.contents, embedContents{key.path, data})
	if err != nil {
		return fail(err)
	}
	if len(data) > limit && limit < buildFileLimit {
		return fail(fmt.Errorf("captured build inputs exceed 64 MiB"))
	}
	if len(data) > buildFileLimit {
		return fail(fmt.Errorf("build input exceeds 16 MiB"))
	}
	if key.kind == "ReadString" && !utf8.Valid(data) {
		return fail(fmt.Errorf("file is not valid UTF-8; use build.ReadBytes for binary data"))
	}
	out.value.files = []check.EmbeddedFile{{Data: data}}
	return out
}

func (s *buildSnapshot) current() bool {
	for key, old := range s.reads {
		now := readBuildInputLimit(key, old.limit)
		if buildReadDigest(now) != buildReadDigest(old) || len(now.components) != len(old.components) {
			return false
		}
		for i, before := range old.components {
			after := now.components[i]
			if before.path != after.path || !os.SameFile(before.info, after.info) {
				return false
			}
		}
	}
	return true
}

func buildReadDigest(value buildRead) [sha256.Size]byte {
	base := embedReadDigest(value.value)
	return sha256.Sum256(append(base[:], value.failedContents[:]...))
}

func (s *buildSnapshot) dependencies() []sourceDependency {
	var out []sourceDependency
	for key, value := range s.reads {
		out = append(out, sourceDependency{Kind: "build." + key.kind, Path: filepath.Join(key.root, key.path), Digest: buildReadDigest(value)})
	}
	slices.SortFunc(out, func(a, b sourceDependency) int {
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

// frozen reads only the owned capture. A semantic replay must not discover new
// files or reopen disk; current() remains a separate publication/reuse check.
func (s *buildSnapshot) frozen(key buildReadKey) ([]byte, error) {
	value, ok := s.reads[key]
	if !ok {
		return nil, fmt.Errorf("build input is absent from the captured inventory")
	}
	if value.value.err != nil {
		return nil, value.value.err
	}
	return value.value.files[0].Data, nil
}

func replayBuildInputs(inputs *buildSnapshot, info *check.Info, diags *diag.List, sources *sourceSnapshot) *buildSnapshot {
	published := map[string][]byte{}
	for _, request := range info.BuildReads {
		mod, err := findModuleFrom(filepath.Dir(request.Pos.File), sources)
		var data []byte
		if err == nil {
			data, err = inputs.frozen(buildReadKey{mod.root, request.Kind, request.Path})
		}
		if err != nil {
			diags.AddCode(request.Pos, "build.input", "cannot read build input %q: %v", request.Path, err)
			continue
		}
		identity := filepath.Join(mod.root, request.Path)
		owned, ok := published[identity]
		if !ok {
			owned = slices.Clone(data)
			published[identity] = owned
		}
		request.Data = owned
		request.Captured = true
	}
	return inputs
}

func captureBuildInputs(info *check.Info, diags *diag.List, sources *sourceSnapshot) *buildSnapshot {
	sources.mu.Lock()
	existing := sources.rooted
	sources.mu.Unlock()
	if existing != nil {
		return replayBuildInputs(existing, info, diags, sources)
	}
	var attempted *buildSnapshot
	for range 2 {
		inputs := &buildSnapshot{reads: map[buildReadKey]buildRead{}}
		attempted = inputs
		keys := make([]buildReadKey, len(info.BuildReads))
		failures := make([]error, len(keys))
		total := 0
		counted := map[string]bool{}
		for i, request := range info.BuildReads {
			mod, err := findModuleFrom(filepath.Dir(request.Pos.File), sources)
			if err != nil {
				failures[i] = err
				continue
			}
			key := buildReadKey{mod.root, request.Kind, request.Path}
			keys[i] = key
			if _, ok := inputs.reads[key]; !ok {
				identity := filepath.Join(key.root, key.path)
				limit := buildFileLimit
				if !counted[identity] {
					limit = min(limit, buildTotalLimit-total)
				}
				value := readBuildInputLimit(key, limit)
				inputs.reads[key] = value
				if value.value.err == nil && !counted[identity] {
					total += len(value.value.files[0].Data)
					counted[identity] = true
				}
			}
			failures[i] = inputs.reads[key].value.err
		}
		if !inputs.current() || !sources.current() {
			continue
		}
		published := map[string][]byte{}
		for i, request := range info.BuildReads {
			if err := failures[i]; err != nil {
				diags.AddCode(request.Pos, "build.input", "cannot read build input %q: %v", request.Path, err)
				continue
			}
			key := keys[i]
			data := inputs.reads[key].value.files[0].Data
			identity := filepath.Join(key.root, key.path)
			owned, ok := published[identity]
			if !ok {
				owned = slices.Clone(data)
				published[identity] = owned
			}
			request.Data = owned
			request.Captured = true
		}
		return inputs
	}
	for _, request := range info.BuildReads {
		diags.AddCode(request.Pos, "build.input", "build inputs changed while loading; retry the command")
	}
	return attempted
}
