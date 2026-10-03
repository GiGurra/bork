package driver

import (
	"errors"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GiGurra/bork/internal/syntax"
	"golang.org/x/tools/go/packages"
)

// goPackages loads the Go packages that bindings name, with go/packages,
// in a module like the one the build uses, so the checker sees the Go
// code that the build compiles. Types come from export data, through
// Go's build cache.
type goPackages struct {
	files   []*syntax.File
	module  *goModuleInputs
	context *goContext
}

func (gp goPackages) Load(paths []string) (map[string]*types.Package, map[string]error) {
	pkgs := map[string]*types.Package{}
	errs := map[string]error{}
	fail := func(err error) (map[string]*types.Package, map[string]error) {
		for _, p := range paths {
			errs[p] = err
		}
		return pkgs, errs
	}
	ctx := gp.goContext()
	if ctx.err != nil {
		return fail(fmt.Errorf("determining Go target: %w", ctx.err))
	}
	if ctx.driverErr != nil {
		return fail(fmt.Errorf("loading Go metadata: %w", ctx.driverErr))
	}
	arch := ctx.values["GOARCH"]
	sizes := types.SizesFor("gc", arch)
	if sizes == nil || sizes.Sizeof(types.Typ[types.Int]) != 8 {
		return fail(fmt.Errorf("bindings require a 64-bit Go target; GOARCH=%s is not supported", arch))
	}
	dir, err := os.MkdirTemp("", "bork-gotypes-*")
	if err != nil {
		return fail(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if _, err := gp.writeModule(dir); err != nil {
		return fail(err)
	}
	env, err := ctx.driverEnv(dir)
	if err != nil {
		return fail(err)
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedTypes,
		Dir:  dir,
		Env:  env,
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

// Standard package names do not depend on a user's module. Discover only the
// imports a program needs and reuse their names; external names are loaded in
// the pinned module on each check.
type standardGoNameKey struct {
	namespace [32]byte
	path      string
}

var standardGoNames sync.Map // configuration namespace + import path -> package name

func (gp goPackages) Names(paths []string) map[string]string {
	names := map[string]string{}
	ctx := gp.goContext()
	if ctx.err != nil || ctx.driverErr != nil {
		return names
	}
	cache := ctx.namesCache
	var missing []string
	for _, path := range paths {
		if name, ok := standardGoNames.Load(standardGoNameKey{ctx.namespace, path}); cache && ok {
			names[path] = name.(string)
		} else {
			missing = append(missing, path)
		}
	}
	if len(missing) == 0 {
		return names
	}
	dir, err := os.MkdirTemp("", "bork-gonames-*")
	if err != nil {
		return names
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if _, err := gp.writeModule(dir); err != nil {
		return names
	}
	env, err := ctx.driverEnv(dir)
	if err != nil {
		return names
	}
	loaded, err := packages.Load(&packages.Config{
		Mode: packages.NeedName | packages.NeedFiles, Dir: dir,
		Env: env,
	}, missing...)
	if err != nil {
		return names
	}
	for _, pkg := range loaded {
		if len(pkg.Errors) == 0 {
			names[pkg.PkgPath] = pkg.Name
			if cache && ctx.standardPackage(pkg) {
				standardGoNames.Store(standardGoNameKey{ctx.namespace, pkg.PkgPath}, pkg.Name)
			}
		}
	}
	return names
}

func (gp goPackages) goContext() *goContext {
	if gp.context != nil {
		return gp.context
	}
	return captureGoContext()
}

func (gp goPackages) writeModule(dir string) (bool, error) {
	if gp.module != nil {
		return gp.module.write(dir)
	}
	return writeGoModule(dir, gp.files)
}

// The external-driver wire schema omits Module, including for our builtin
// metadata bridge. Positively prove standard origin from Go file locations;
// missing module metadata alone is never a proof of standard-library origin.
func (ctx *goContext) standardPackage(pkg *packages.Package) bool {
	first, _, _ := strings.Cut(pkg.PkgPath, "/")
	if strings.Contains(first, ".") || len(pkg.GoFiles) == 0 || ctx.values["GOROOT"] == "" {
		return false
	}
	dir := filepath.Join(ctx.values["GOROOT"], "src", filepath.FromSlash(pkg.PkgPath))
	for _, file := range pkg.GoFiles {
		if filepath.Dir(file) != dir {
			return false
		}
	}
	return true
}
