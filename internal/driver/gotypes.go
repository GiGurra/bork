package driver

import (
	"errors"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// goMod is the module file of generated programs.
const goMod = "module borkprogram\n\ngo 1.22\n"

// goPackages loads the Go packages that bindings name, with go/packages,
// in a module like the one the build uses, so the checker sees the Go
// code that the build compiles. Types come from export data, through
// Go's build cache.
type goPackages struct{}

func (goPackages) Load(paths []string) (map[string]*types.Package, map[string]error) {
	pkgs := map[string]*types.Package{}
	errs := map[string]error{}
	fail := func(err error) (map[string]*types.Package, map[string]error) {
		for _, p := range paths {
			errs[p] = err
		}
		return pkgs, errs
	}
	dir, err := os.MkdirTemp("", "bork-gotypes-*")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fail(err)
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes,
		Dir:  dir,
		Env:  append(os.Environ(), "GOWORK=off", "GOFLAGS="),
	}
	loaded, err := packages.Load(cfg, paths...)
	if err != nil {
		return fail(err)
	}
	for _, p := range loaded {
		if len(p.Errors) > 0 {
			var msgs []string
			for _, e := range p.Errors {
				// Only the first line: Go's advice ("to add it: go
				// get ...") is not how bork programs get modules.
				msg, _, _ := strings.Cut(e.Msg, "\n")
				msgs = append(msgs, strings.TrimSuffix(msg, "; to add it:"))
			}
			errs[p.PkgPath] = errors.New(strings.Join(msgs, "; "))
			continue
		}
		pkgs[p.PkgPath] = p.Types
	}
	for _, p := range paths {
		if pkgs[p] == nil && errs[p] == nil {
			errs[p] = errors.New("not found")
		}
	}
	return pkgs, errs
}
