package driver

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

const goStageSchema = 2
const goStageInventoryLimit = 1 << 16

type goStageMetadata struct {
	Schema    int               `json:"schema"`
	Program   string            `json:"program"`
	Mode      string            `json:"mode"`
	Namespace [sha256.Size]byte `json:"namespace"`
	policy    *cacheLimits
}
type goStageEntry struct {
	nodes     int
	path, key string
	bytes     int64
	used      time.Time
	absent    bool
}

func stageGoStable(base, key string, source []byte, module *goModuleInputs, embeds []*check.Embedded, metadata goStageMetadata) (string, bool, func(), error) {
	if _, ok := cacheHexDigest(key); !ok || metadata.Schema != goStageSchema || !validReceiptPath(metadata.Program) || !filepath.IsAbs(metadata.Program) || metadata.Mode == "" {
		return "", false, nil, errInvalidCacheArtifact
	}
	files, pinned, bytes, err := goStageFiles(source, module, embeds)
	if err != nil {
		return "", false, nil, err
	}
	meta, err := json.Marshal(metadata)
	if err != nil || len(meta) > 16<<10 {
		return "", false, nil, errCacheArtifactBudget
	}
	bytes += int64(len(meta))
	nodes, err := goStageNodes(files)
	if err != nil {
		return "", false, nil, err
	}
	store := cacheStore{root: base, policy: metadata.policy}
	if bytes > store.limits().stageBytes || nodes > store.limits().stageNodes {
		return "", false, nil, errCacheArtifactBudget
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", false, nil, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", false, nil, err
	}
	if err := ensureStageDirectory(root, filepath.Join("locks", "stage-v2"), 0700); err != nil {
		_ = root.Close()
		return "", false, nil, err
	}
	slot, err := store.lock(root, strings.TrimPrefix(goStageLockPath(base, key), base+string(filepath.Separator)))
	if err != nil {
		_ = root.Close()
		return "", false, nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = slot.Close()
			_ = root.Close()
		}
	}()
	mutation, err := store.lock(root, "mutation.lock")
	if err != nil {
		return "", false, nil, err
	}
	defer func() { _ = mutation.Close() }()
	if err := validateStageDirectory(root, filepath.Join("stage", "v2", key)); err != nil {
		return "", false, nil, err
	}
	if err := removeAbandonedGoStage(root, key); err != nil {
		return "", false, nil, err
	}
	if err := reserveGoStage(root, store, key, bytes, nodes); err != nil {
		return "", false, nil, err
	}
	entry := filepath.Join("stage", "v2", key)
	if err := ensureStageDirectory(root, entry, 0700); err != nil {
		return "", false, nil, err
	}
	pending := filepath.Join(entry, "new-"+rand.Text())
	if err := root.Mkdir(pending, 0700); err != nil {
		return "", false, nil, err
	}
	defer func() { _ = root.RemoveAll(pending) }()
	for name, data := range files {
		path := filepath.Join(pending, name)
		if err := ensureStageDirectory(root, filepath.Dir(path), 0755); err != nil {
			return "", false, nil, err
		}
		file, err := openCacheFile(root, path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return "", false, nil, err
		}
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return "", false, nil, writeErr
		}
		if closeErr != nil {
			return "", false, nil, closeErr
		}
	}
	tree, previous := filepath.Join(entry, "tree"), filepath.Join(entry, "previous")
	if err := root.RemoveAll(previous); err != nil {
		return "", false, nil, err
	}
	old := true
	if err := root.Rename(tree, previous); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, nil, err
		}
		old = false
	}
	if err := root.Rename(pending, tree); err != nil {
		if old {
			_ = root.Rename(previous, tree)
		}
		return "", false, nil, err
	}
	if err := root.RemoveAll(previous); err != nil {
		return "", false, nil, err
	}
	metaFile, err := openCacheFile(root, filepath.Join(entry, "metadata.json"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return "", false, nil, err
	}
	_, writeErr := metaFile.Write(meta)
	closeErr := metaFile.Close()
	if writeErr != nil {
		return "", false, nil, writeErr
	}
	if closeErr != nil {
		return "", false, nil, closeErr
	}
	success = true
	release := func() { _ = slot.Close(); _ = root.Close() }
	return filepath.Join(base, tree), pinned, release, nil
}

func goStageFiles(source []byte, module *goModuleInputs, embeds []*check.Embedded) (map[string][]byte, bool, int64, error) {
	files := map[string][]byte{"main.go": source}
	for _, request := range embeds {
		for _, file := range request.Files {
			path := filepath.FromSlash(file.StagePath)
			if !filepath.IsLocal(path) || path == "main.go" || path == "go.mod" || path == "go.sum" {
				return nil, false, 0, errInvalidCacheArtifact
			}
			if len(files) >= goStageInventoryLimit {
				return nil, false, 0, errCacheArtifactBudget
			}
			files[path] = file.Data
		}
	}
	mod := module.mod
	if goModuleHook != nil {
		mod = goModuleHook(slices.Clone(mod))
	}
	files["go.mod"] = mod
	if len(module.sum) > 0 {
		files["go.sum"] = module.sum
	}
	var bytes int64
	for _, data := range files {
		bytes += int64(len(data))
	}
	return files, len(module.sum) > 0, bytes, nil
}
func stageTreeBytes(root *os.Root, path string, remaining *int) (int64, error) {
	pending := []string{path}
	var bytes int64
	for len(pending) > 0 {
		path := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		*remaining--
		if *remaining < 0 {
			return 0, errCacheArtifactBudget
		}
		info, err := root.Lstat(path)
		if err != nil {
			return 0, err
		}
		if info.Mode().IsRegular() {
			bytes += info.Size()
			continue
		}
		if !info.IsDir() {
			return 0, errInvalidCacheArtifact
		}
		names, err := cacheDirectoryEntries(root, path, *remaining-len(pending))
		if err != nil {
			return 0, err
		}
		for _, name := range names {
			pending = append(pending, filepath.Join(path, name.Name()))
		}
	}
	return bytes, nil
}
func removeAbandonedGoStage(root *os.Root, key string) error {
	path := filepath.Join("stage", "v2", key)
	entries, err := cacheDirectoryEntries(root, path, cacheInventoryLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "previous" || strings.HasPrefix(entry.Name(), "new-") {
			if err := root.RemoveAll(filepath.Join(path, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
func stageInventory(root *os.Root) ([]goStageEntry, error) {
	names, err := cacheDirectoryEntries(root, filepath.Join("stage", "v2"), cacheInventoryLimit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	remaining := goStageInventoryLimit
	var entries []goStageEntry
	for _, name := range names {
		if _, ok := cacheHexDigest(name.Name()); !ok || !name.IsDir() {
			continue
		}
		path := filepath.Join("stage", "v2", name.Name())
		before := remaining
		size, err := stageTreeBytes(root, path, &remaining)
		if err != nil {
			return nil, err
		}
		info, err := root.Stat(path)
		if err != nil {
			return nil, err
		}
		entry := goStageEntry{path: path, key: name.Name(), nodes: before - remaining, bytes: size, used: info.ModTime()}
		file, err := openCacheFile(root, filepath.Join(path, "metadata.json"), os.O_RDONLY, 0)
		if err == nil {
			data, readErr := readBoundedStageMetadata(file)
			_ = file.Close()
			if readErr == nil {
				var metadata goStageMetadata
				if decodeStrictCacheJSON(data, &metadata) == nil && metadata.Schema == goStageSchema && filepath.IsAbs(metadata.Program) {
					if _, err := os.Stat(metadata.Program); errors.Is(err, os.ErrNotExist) {
						entry.absent = true
					}
				}
			}
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(a, b goStageEntry) int {
		if a.absent != b.absent {
			if a.absent {
				return -1
			}
			return 1
		}
		if a.used.Before(b.used) {
			return -1
		}
		if a.used.After(b.used) {
			return 1
		}
		return strings.Compare(a.path, b.path)
	})
	return entries, nil
}
func reserveGoStage(root *os.Root, store cacheStore, key string, bytes int64, nodes int) error {
	entries, err := stageInventory(root)
	if err != nil {
		return err
	}
	limits := store.limits()
	var used int64
	usedNodes := 0
	count := len(entries)
	added := 1
	for _, entry := range entries {
		used += entry.bytes
		usedNodes += entry.nodes
		if entry.key == key {
			added = 0
		}
	}
	for _, entry := range entries {
		if !entry.absent && used+bytes <= limits.stageBytes && count+added <= limits.entries && usedNodes+nodes <= limits.stageNodes {
			break
		}
		if entry.key == key {
			continue
		}
		name := strings.TrimPrefix(goStageLockPath(store.root, entry.key), store.root+string(filepath.Separator))
		if name == strings.TrimPrefix(goStageLockPath(store.root, key), store.root+string(filepath.Separator)) {
			continue
		}
		slot, err := store.tryLock(root, name)
		if err != nil {
			continue
		}
		err = root.RemoveAll(entry.path)
		_ = slot.Close()
		if err != nil {
			return err
		}
		used -= entry.bytes
		usedNodes -= entry.nodes
		count--
	}
	if bytes < 0 || used+bytes > limits.stageBytes || count+added > limits.entries || usedNodes+nodes > limits.stageNodes {
		return errCacheArtifactBudget
	}
	return nil
}

func readBoundedStageMetadata(file *os.File) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	if err != nil || len(data) > 16<<10 {
		return nil, errInvalidCacheArtifact
	}
	return data, nil
}

// Published tree paths must not alias another entry through a contained symlink.
// Root confinement alone protects the boundary, but does not prove entry locks.
func ensureStageDirectory(root *os.Root, path string, mode os.FileMode) error {
	if !filepath.IsLocal(path) {
		return errInvalidCacheArtifact
	}
	partial := ""
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		partial = filepath.Join(partial, component)
		info, err := root.Lstat(partial)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(partial, mode); err != nil {
				return err
			}
			info, err = root.Lstat(partial)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errInvalidCacheArtifact
		}
	}
	return nil
}

func validateStageDirectory(root *os.Root, path string) error {
	partial := ""
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		partial = filepath.Join(partial, component)
		info, err := root.Lstat(partial)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errInvalidCacheArtifact
		}
	}
	return nil
}

func goStageNodes(files map[string][]byte) (int, error) {
	nodes := map[string]bool{".": true, "tree": true, "metadata.json": true}
	for path := range files {
		path = filepath.Join("tree", path)
		nodes[path] = true
		for dir := filepath.Dir(path); dir != "."; dir = filepath.Dir(dir) {
			nodes[dir] = true
			if len(nodes) > goStageInventoryLimit {
				return 0, errCacheArtifactBudget
			}
		}
	}
	return len(nodes), nil
}
