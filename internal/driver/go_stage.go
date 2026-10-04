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

// goStageCacheDir lets the test harness select writable staging without changing
// the captured subprocess environment or Go build cache. Set it before tests run.
var goStageCacheDir = os.UserCacheDir

// stageGo gives Go a stable package directory without reusing semantic results.
// The lock remains held until the caller has finished running Go. Unsupported
// platforms, unavailable caches and failed publication use temporary staging.
func stageGo(files []*syntax.File, source []byte, module *goModuleInputs, context *goContext, mode string, embeds []*check.Embedded) (string, bool, func(), error) {
	if base, err := cacheRootDir(); err == nil && !cacheDisabled() {
		if root, err := goStageProgramRoot(files); err == nil {
			key := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%x", root, mode, context.namespace)))
			if dir, pinned, release, err := stageGoStable(base, fmt.Sprintf("%x", key), source, module, embeds, goStageMetadata{Schema: goStageSchema, Program: root, Mode: mode, Namespace: context.namespace}); err == nil {
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

// The mapping is part of staging schema v3 and remains stable across clients.
func goStageLockPath(base, key string) string {
	slot := sha256.Sum256([]byte(key))
	return filepath.Join(base, "locks", "stage-v3", fmt.Sprintf("%02x.lock", slot[0]))
}
