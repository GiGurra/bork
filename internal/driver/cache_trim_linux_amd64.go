//go:build linux && amd64

package driver

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const cacheTrimInterval = 24 * time.Hour
const cacheTrimAge = 5*24*time.Hour + cacheUseInterval
const cacheTrimStateLimit = 256 << 10
const cacheTrimWorkerTime = 250 * time.Millisecond

type cacheTrimFrame struct {
	Path   string
	Offset int64
}
type cacheTrimState struct {
	Version           int
	Cutoff, Completed time.Time
	Layer             int
	Scan              []cacheTrimFrame
	Delete            string
	Removal           []cacheTrimFrame
}
type cacheTrimReport struct {
	Steps, Removed int
	Complete       bool
}

var cacheTrimLayers = [...]string{"results/v2", "stage/v3", "indexes/v1"}

// runCacheTrim is private maintenance, never called by lookup or publication.
// Linux directory cookies avoid rescanning a growing prefix between bounded
// workers. Concurrent directory changes may postpone an entry until another
// daily cycle; cookies and progress are retention hints, never semantic evidence.
func runCacheTrim(ctx context.Context, base string, now time.Time, steps int) (cacheTrimReport, error) {
	var report cacheTrimReport
	ctx, cancel := context.WithTimeout(ctx, cacheTrimWorkerTime)
	defer cancel()
	if steps <= 0 || steps > 4096 {
		return report, errInvalidCacheArtifact
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return report, err
	}
	defer func() { _ = root.Close() }()
	store := cacheStore{root: base}
	worker, err := store.tryLock(root, "trim.lock")
	if err != nil {
		return report, nil
	}
	defer func() { _ = worker.Close() }()
	state := readCacheTrimState(root)
	if !validCacheTrimState(state) || state.Completed.After(now.Add(cacheTrimInterval)) || state.Cutoff.After(now.Add(-cacheTrimAge)) {
		state = cacheTrimState{Version: 1}
	}
	if !state.Completed.IsZero() && now.Sub(state.Completed) < cacheTrimInterval {
		report.Complete = true
		return report, nil
	}
	if !state.Completed.IsZero() {
		state = cacheTrimState{Version: 1}
	}
	if state.Cutoff.IsZero() {
		state.Cutoff = now.Add(-cacheTrimAge)
	}
	for report.Steps < steps && ctx.Err() == nil {
		report.Steps++
		if state.Delete != "" {
			if !trimCacheCandidate(root, store, &state, &report) {
				break
			}
			continue
		}
		if state.Layer >= len(cacheTrimLayers) {
			state.Completed = now
			state.Cutoff = time.Time{}
			report.Complete = true
			break
		}
		if len(state.Scan) == 0 {
			state.Scan = []cacheTrimFrame{{Path: cacheTrimLayers[state.Layer]}}
		}
		frame := &state.Scan[len(state.Scan)-1]
		name, offset, err := nextCacheTrimName(root, *frame)
		if err != nil || name == "" {
			state.Scan = state.Scan[:len(state.Scan)-1]
			if len(state.Scan) == 0 {
				state.Layer++
			}
			continue
		}
		frame.Offset = offset
		path := filepath.Join(frame.Path, name)
		dir, slot, ok := cacheTrimPath(path)
		if !ok {
			continue
		}
		if dir {
			state.Scan = append(state.Scan, cacheTrimFrame{Path: path})
			continue
		}
		// Candidate validation and mutation are deferred to the locked removal step.
		_ = slot
		state.Delete = path
	}
	if err := writeCacheTrimState(root, store, state); err != nil {
		return report, err
	}
	return report, ctx.Err()
}

func readCacheTrimState(root *os.Root) cacheTrimState {
	var state cacheTrimState
	file, err := openCacheFile(root, "trim.json", os.O_RDONLY, 0)
	if err != nil {
		return state
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, cacheTrimStateLimit+1))
	if err != nil || len(data) > cacheTrimStateLimit {
		return state
	}
	if json.Unmarshal(data, &state) != nil {
		return cacheTrimState{}
	}
	return state
}
func validCacheTrimState(state cacheTrimState) bool {
	if state.Version != 1 || state.Layer < 0 || state.Layer > len(cacheTrimLayers) || len(state.Scan) > 4 || len(state.Removal) > 2048 {
		return false
	}
	for _, frame := range state.Scan {
		dir, _, ok := cacheTrimPath(frame.Path)
		if !ok || !dir || frame.Offset < 0 || state.Layer >= len(cacheTrimLayers) || (frame.Path != cacheTrimLayers[state.Layer] && !strings.HasPrefix(frame.Path, cacheTrimLayers[state.Layer]+"/")) {
			return false
		}
	}
	if state.Delete == "" {
		return len(state.Removal) == 0
	}
	dir, _, ok := cacheTrimPath(state.Delete)
	if !ok || dir {
		return false
	}
	if !strings.HasPrefix(state.Delete, "stage/v3/") && len(state.Removal) != 0 {
		return false
	}
	for index, frame := range state.Removal {
		if frame.Offset < 0 {
			return false
		}
		if index == 0 {
			if frame.Path != "" {
				return false
			}
			continue
		}
		raw, err := hex.DecodeString(frame.Path)
		name := string(raw)
		if err != nil || len(raw) == 0 || len(raw) > 255 || hex.EncodeToString(raw) != frame.Path || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
			return false
		}
	}
	return len(cacheTrimRemovalPath(&state)) <= 4096
}
func writeCacheTrimState(root *os.Root, store cacheStore, state cacheTrimState) error {
	data, err := json.Marshal(state)
	if err != nil || len(data) > cacheTrimStateLimit {
		return errInvalidCacheArtifact
	}
	lock, err := store.tryLock(root, "mutation.lock")
	if err != nil {
		return nil
	}
	defer func() { _ = lock.Close() }()
	// A fixed temporary name is safe under the permanent maintenance lock.
	if err := root.Remove("trim.json.next"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := openCacheFile(root, "trim.json.next", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove("trim.json.next") }()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Rename("trim.json.next", "trim.json")
}

// cacheTrimPath recognizes only current sharded layouts and their own lock pool.
// Explicit clean, not automatic maintenance, owns legacy versions.
func cacheTrimPath(path string) (directory bool, slot string, ok bool) {
	if filepath.Clean(path) != path {
		return false, "", false
	}
	parts := strings.Split(path, "/")
	switch {
	case len(parts) >= 2 && parts[0] == "results" && parts[1] == "v2":
		if len(parts) == 2 {
			return true, "", true
		}
		if !cacheShard(parts[2]) {
			return false, "", false
		}
		if len(parts) == 3 {
			return true, "", true
		}
		ns, valid := cacheHexDigest(parts[3])
		if !valid || parts[3][:2] != parts[2] {
			return false, "", false
		}
		if len(parts) == 4 {
			return true, "", true
		}
		if !cacheShard(parts[4]) {
			return false, "", false
		}
		if len(parts) == 5 {
			return true, "", true
		}
		if len(parts) != 6 || !strings.HasSuffix(parts[5], ".json") {
			return false, "", false
		}
		key, valid := cacheHexDigest(strings.TrimSuffix(parts[5], ".json"))
		if !valid || parts[5][:2] != parts[4] {
			return false, "", false
		}
		return false, (cacheStore{namespace: ns}).lockName(key), true
	case len(parts) >= 2 && parts[0] == "stage" && parts[1] == "v3":
		if len(parts) == 2 {
			return true, "", true
		}
		if !cacheShard(parts[2]) {
			return false, "", false
		}
		if len(parts) == 3 {
			return true, "", true
		}
		if len(parts) != 4 {
			return false, "", false
		}
		_, valid := cacheHexDigest(parts[3])
		if !valid || parts[3][:2] != parts[2] {
			return false, "", false
		}
		return false, goStageLockPath("", parts[3]), true
	case len(parts) >= 2 && parts[0] == "indexes" && parts[1] == "v1":
		if len(parts) == 2 {
			return true, "", true
		}
		if !cacheShard(parts[2]) {
			return false, "", false
		}
		if len(parts) == 3 {
			return true, "", true
		}
		if len(parts) != 4 || !strings.HasSuffix(parts[3], ".json") {
			return false, "", false
		}
		_, valid := cacheHexDigest(strings.TrimSuffix(parts[3], ".json"))
		if !valid || parts[3][:2] != parts[2] {
			return false, "", false
		}
		return false, "", true
	}
	return false, "", false
}

// Read one getdents64 record. Its d_off cookie, unlike an entry count, resumes
// without listing or skipping an unbounded directory prefix. This first private
// engine supports Linux amd64; unsupported detached platforms will skip trim.
func nextCacheTrimName(root *os.Root, frame cacheTrimFrame) (string, int64, error) {
	if err := validateStageDirectory(root, frame.Path); err != nil {
		return "", 0, err
	}
	file, err := root.Open(frame.Path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(frame.Offset, io.SeekStart); err != nil {
		return "", 0, err
	}
	var data [512]byte
	for {
		n, err := syscall.ReadDirent(int(file.Fd()), data[:])
		if err != nil || n == 0 {
			return "", 0, err
		}
		if n < 20 {
			return "", 0, errInvalidCacheArtifact
		}
		length := int(binary.LittleEndian.Uint16(data[16:18]))
		if length < 20 || length > n {
			return "", 0, errInvalidCacheArtifact
		}
		offset := int64(binary.LittleEndian.Uint64(data[8:16]))
		if offset <= frame.Offset {
			return "", 0, errInvalidCacheArtifact
		}
		raw := data[19:length]
		end := 0
		for end < len(raw) && raw[end] != 0 {
			end++
		}
		name := string(raw[:end])
		if name != "." && name != ".." {
			return name, offset, nil
		}
		// The kernel may have returned several records. Seek to the consumed cookie
		// rather than silently dropping buffered entries following dot names.
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return "", 0, err
		}
		frame.Offset = offset
	}
}
func clearCacheTrimRemoval(state *cacheTrimState) { state.Delete = ""; state.Removal = nil }
func trimCacheCandidate(root *os.Root, store cacheStore, state *cacheTrimState, report *cacheTrimReport) bool {
	_, slotName, ok := cacheTrimPath(state.Delete)
	if !ok {
		clearCacheTrimRemoval(state)
		return true
	}
	if err := validateStageDirectory(root, filepath.Dir(state.Delete)); err != nil {
		clearCacheTrimRemoval(state)
		return true
	}
	var slot *os.File
	var err error
	if slotName != "" {
		if err := validateStageDirectory(root, filepath.Dir(slotName)); err != nil {
			clearCacheTrimRemoval(state)
			return true
		}
		slot, err = store.tryLock(root, slotName)
		if err != nil {
			clearCacheTrimRemoval(state)
			return true
		}
		defer func() { _ = slot.Close() }()
	}
	mutation, err := store.tryLock(root, "mutation.lock")
	if err != nil {
		return false
	}
	defer func() { _ = mutation.Close() }()
	info, err := root.Lstat(state.Delete)
	if err != nil {
		clearCacheTrimRemoval(state)
		return true
	}
	stage := strings.HasPrefix(state.Delete, "stage/v3/")
	if stage {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			clearCacheTrimRemoval(state)
			return true
		}
		info, err = root.Lstat(filepath.Join(state.Delete, "used"))
		if errors.Is(err, os.ErrNotExist) {
			// Sharded v3 publishers preceded use markers. Establish an old marker
			// only from recognizable publication evidence, while holding SLOT+MUTATION.
			stamp, ok := cacheTrimStagePublication(root, state.Delete)
			if !ok || !stamp.Before(state.Cutoff) || markCacheUse(root, filepath.Join(state.Delete, "used"), stamp, true) != nil {
				clearCacheTrimRemoval(state)
				return true
			}
			info, err = root.Lstat(filepath.Join(state.Delete, "used"))
		}
	} else if !info.Mode().IsRegular() {
		clearCacheTrimRemoval(state)
		return true
	}
	if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(state.Cutoff) {
		clearCacheTrimRemoval(state)
		return true
	}
	if !stage {
		if root.Remove(state.Delete) == nil {
			report.Removed++
		}
		clearCacheTrimRemoval(state)
		return true
	}
	if len(state.Removal) == 0 {
		state.Removal = []cacheTrimFrame{{}}
	}
	frame := &state.Removal[len(state.Removal)-1]
	framePath := cacheTrimRemovalPath(state)
	name, offset, err := nextCacheTrimName(root, cacheTrimFrame{Path: framePath, Offset: frame.Offset})
	if err != nil {
		clearCacheTrimRemoval(state)
		return true
	}
	if name == "" {
		if framePath == state.Delete {
			// Keep the marker until all descendants are gone, so resumed workers can
			// recheck age after a fresh stage publication instead of deleting its tree.
			if err := root.Remove(filepath.Join(state.Delete, "used")); err != nil {
				clearCacheTrimRemoval(state)
				return true
			}
		}
		if err := root.Remove(framePath); err != nil {
			// Deleting entries or concurrent changes can invalidate directory cookies.
			// Revisit the remaining names, with every retry charged to the worker budget.
			if framePath == state.Delete {
				_ = markCacheUse(root, filepath.Join(state.Delete, "used"), info.ModTime(), true)
			}
			frame.Offset = 0
			return true
		}
		state.Removal = state.Removal[:len(state.Removal)-1]
		if len(state.Removal) == 0 {
			report.Removed++
			clearCacheTrimRemoval(state)
		}
		return true
	}
	frame.Offset = offset
	if framePath == state.Delete && name == "used" {
		return true
	}
	path := filepath.Join(framePath, name)
	info, err = root.Lstat(path)
	if err != nil {
		return true
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		if len(state.Removal) >= 2048 || len(path) > 4096 {
			clearCacheTrimRemoval(state)
			return true
		}
		state.Removal = append(state.Removal, cacheTrimFrame{Path: hex.EncodeToString([]byte(name))})
		return true
	}
	_ = root.Remove(path) // Symlinks are unlinked; their target is never traversed.
	return true
}

// Removal frames retain each component once rather than quadratic full paths.
// Current generated staging is flat (main/module files and rewritten embed
// names). Explicit depth/path bounds cover it; malformed or future unsupported
// deeper trees retain conservative progress and remain removable by clean.
func cacheTrimRemovalPath(state *cacheTrimState) string {
	path := state.Delete
	for _, frame := range state.Removal[min(1, len(state.Removal)):] {
		raw, _ := hex.DecodeString(frame.Path)
		path = filepath.Join(path, string(raw))
	}
	return path
}
func cacheTrimStagePublication(root *os.Root, path string) (time.Time, bool) {
	var metadata goStageMetadata
	file, err := openCacheFile(root, filepath.Join(path, "metadata.json"), os.O_RDONLY, 0)
	if err != nil {
		return time.Time{}, false
	}
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	_ = file.Close()
	if err != nil || len(data) > 16<<10 || json.Unmarshal(data, &metadata) != nil || metadata.Schema != goStageSchema || !filepath.IsAbs(metadata.Program) || metadata.Mode == "" {
		return time.Time{}, false
	}
	var newest time.Time
	for _, name := range []string{path, filepath.Join(path, "metadata.json"), filepath.Join(path, "tree")} {
		info, err := root.Lstat(name)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return time.Time{}, false
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, true
}
