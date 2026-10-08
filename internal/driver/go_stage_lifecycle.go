package driver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/GiGurra/bork/internal/check"
)

const goStageSchema = 3
const goStageInventoryLimit = 1 << 16

type goStageMetadata struct {
	Schema    int               `json:"schema"`
	Program   string            `json:"program"`
	Mode      string            `json:"mode"`
	Namespace [sha256.Size]byte `json:"namespace"`
}

func stageGoStable(base, key string, source []byte, module *goModuleInputs, embeds []*check.Embedded, metadata goStageMetadata) (string, bool, func(), error) {
	return stageGoStableWithHook(base, key, source, module, embeds, metadata, goModuleHook)
}

func stageGoStableWithHook(base, key string, source []byte, module *goModuleInputs, embeds []*check.Embedded, metadata goStageMetadata, hook goModuleHookFunc, contexts ...context.Context) (string, bool, func(), error) {
	return stageGoStableAtWithHook(base, key, source, module, embeds, metadata, time.Now(), hook, contexts...)
}

func stageGoStableAt(base, key string, source []byte, module *goModuleInputs, embeds []*check.Embedded, metadata goStageMetadata, now time.Time) (string, bool, func(), error) {
	return stageGoStableAtWithHook(base, key, source, module, embeds, metadata, now, goModuleHook)
}

func stageGoStableAtWithHook(base, key string, source []byte, module *goModuleInputs, embeds []*check.Embedded, metadata goStageMetadata, now time.Time, hook goModuleHookFunc, contexts ...context.Context) (string, bool, func(), error) {
	var ctx context.Context
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	if _, ok := cacheHexDigest(key); !ok || metadata.Schema != goStageSchema || !validReceiptPath(metadata.Program) || !filepath.IsAbs(metadata.Program) || metadata.Mode == "" {
		return "", false, nil, errInvalidCacheArtifact
	}
	files, pinned, err := goStageFiles(source, module, embeds, hook)
	if err != nil {
		return "", false, nil, err
	}
	meta, err := json.Marshal(metadata)
	if err != nil || len(meta) > 16<<10 {
		return "", false, nil, errCacheArtifactBudget
	}
	store := cacheStore{root: base}
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", false, nil, err
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return "", false, nil, err
	}
	if err := ensureStageDirectory(root, filepath.Join("locks", "stage-v3"), 0700); err != nil {
		_ = root.Close()
		return "", false, nil, err
	}
	slot, err := store.lockContext(ctx, root, strings.TrimPrefix(goStageLockPath(base, key), base+string(filepath.Separator)))
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
	mutation, err := store.lockContext(ctx, root, "mutation.lock")
	if err != nil {
		return "", false, nil, err
	}
	defer func() { _ = mutation.Close() }()
	if err := validateStageDirectory(root, goStageEntryPath(key)); err != nil {
		return "", false, nil, err
	}
	entry := goStageEntryPath(key)
	if err := ensureStageDirectory(root, entry, 0700); err != nil {
		return "", false, nil, err
	}
	pending := filepath.Join(entry, "next")
	// A fixed pending name avoids enumerating abandoned generations. SLOT
	// ownership proves no other publisher can be using these known paths.
	for _, name := range []string{"next", "previous"} {
		if err := root.RemoveAll(filepath.Join(entry, name)); err != nil {
			return "", false, nil, err
		}
	}
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
	// Tree replacement changes directory mtimes on every request; a separate
	// marker preserves hourly coalescing across edits and Go subprocess use.
	_ = markCacheUse(root, filepath.Join(entry, "used"), now, true)
	success = true
	release := func() { _ = slot.Close(); _ = root.Close() }
	return filepath.Join(base, tree), pinned, release, nil
}

func goStageFiles(source []byte, module *goModuleInputs, embeds []*check.Embedded, hook goModuleHookFunc) (map[string][]byte, bool, error) {
	files := map[string][]byte{"main.go": source}
	for _, request := range embeds {
		for _, file := range request.Files {
			path := filepath.FromSlash(file.StagePath)
			if !filepath.IsLocal(path) || path == "main.go" || path == "go.mod" || path == "go.sum" {
				return nil, false, errInvalidCacheArtifact
			}
			if len(files) >= goStageInventoryLimit {
				return nil, false, errCacheArtifactBudget
			}
			files[path] = file.Data
		}
	}
	mod := module.mod
	if hook != nil {
		mod = hook(slices.Clone(mod))
	}
	files["go.mod"] = mod
	if len(module.sum) > 0 {
		files["go.sum"] = module.sum
	}
	return files, len(module.sum) > 0, nil
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
			if err := root.Mkdir(partial, mode); err != nil && !errors.Is(err, os.ErrExist) {
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

func goStageEntryPath(key string) string {
	return filepath.Join("stage", "v3", key[:2], key)
}
