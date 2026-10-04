package driver

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

// stageGo gives Go a stable package directory without reusing semantic results.
// The lock remains held until the caller has finished running Go. Unsupported
// platforms, unavailable caches and failed publication use temporary staging.
func stageGo(files []*syntax.File, source []byte, module *goModuleInputs, context *goContext, mode string, embeds []*check.Embedded) (string, bool, func(), error) {
	if base, err := os.UserCacheDir(); err == nil {
		if root, err := goStageProgramRoot(files); err == nil {
			key := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%x", root, mode, context.namespace)))
			if dir, pinned, release, err := stageGoStable(filepath.Join(base, "bork", "stage", "v1"), fmt.Sprintf("%x", key), source, module, embeds); err == nil {
				return dir, pinned, release, nil
			}
		}
	}
	dir, err := os.MkdirTemp("", "bork-build-*")
	if err != nil {
		return "", false, nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	pinned, err := writeGoStage(dir, source, module, embeds)
	if err != nil {
		cleanup()
		return "", false, nil, err
	}
	return dir, pinned, cleanup, nil
}

func goStageProgramRoot(files []*syntax.File) (string, error) {
	// Loading puts root-package files before imported packages. Use its directory
	// even for a module subpackage, so distinct programs do not contend unnecessarily.
	for _, file := range files {
		if file.Prelude || file.Path == "" || strings.HasPrefix(file.Package, std.Prefix) {
			continue
		}
		root, err := filepath.Abs(filepath.Dir(file.Path))
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(root)
	}
	return "", errors.New("no root source directory")
}

func writeGoStage(dir string, source []byte, module *goModuleInputs, embeds []*check.Embedded) (bool, error) {
	if err := os.WriteFile(filepath.Join(dir, "main.go"), source, 0o644); err != nil {
		return false, err
	}
	if err := stageEmbeds(dir, embeds); err != nil {
		return false, err
	}
	return module.write(dir)
}

func stageGoStable(base, key string, source []byte, module *goModuleInputs, embeds []*check.Embedded) (string, bool, func(), error) {
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", false, nil, err
	}
	// Keep a bounded lock pool outside replacement trees. Never unlink these
	// files while clients may use them: separate inodes would split a logical
	// lock. Hash collisions conservatively serialize otherwise unrelated builds.
	locks := filepath.Join(base, "locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		return "", false, nil, err
	}
	lock, err := lockGoStage(goStageLockPath(base, key))
	if err != nil {
		return "", false, nil, err
	}
	release := func() { _ = lock.Close() }
	success := false
	defer func() {
		if !success {
			release()
		}
	}()
	entry := filepath.Join(base, key)
	if err := os.MkdirAll(entry, 0o700); err != nil {
		return "", false, nil, err
	}
	// Remove only abandoned generations while holding the entry lock.
	entries, err := os.ReadDir(entry)
	if err != nil {
		return "", false, nil, err
	}
	for _, candidate := range entries {
		if strings.HasPrefix(candidate.Name(), "new-") {
			if err := os.RemoveAll(filepath.Join(entry, candidate.Name())); err != nil {
				return "", false, nil, err
			}
		}
	}
	pending, err := os.MkdirTemp(entry, "new-*")
	if err != nil {
		return "", false, nil, err
	}
	defer func() { _ = os.RemoveAll(pending) }()
	pinned, err := writeGoStage(pending, source, module, embeds)
	if err != nil {
		return "", false, nil, err
	}
	tree, previous := filepath.Join(entry, "tree"), filepath.Join(entry, "previous")
	if err := os.RemoveAll(previous); err != nil {
		return "", false, nil, err
	}
	hadTree := true
	if err := os.Rename(tree, previous); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", false, nil, err
		}
		hadTree = false
	}
	if err := os.Rename(pending, tree); err != nil {
		if hadTree {
			_ = os.Rename(previous, tree)
		}
		return "", false, nil, err
	}
	// Publication is complete before any Go subprocess sees the tree. A crashed
	// process releases its kernel lock; the next request publishes a fresh tree.
	_ = os.RemoveAll(previous)
	success = true
	return tree, pinned, release, nil
}

// The mapping is part of staging schema v1 and must remain stable across clients.
func goStageLockPath(base, key string) string {
	slot := sha256.Sum256([]byte(key))
	return filepath.Join(base, "locks", fmt.Sprintf("%02x.lock", slot[0]))
}
