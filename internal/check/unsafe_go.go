package check

import (
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	gotypes "go/types"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// The unsafe go check: an unsafe go body that uses a Go name with an
// obvious effect must declare that effect. It is a check of honesty,
// not a proof (Go code can reach the world in many other ways), and it
// applies to user packages: the prelude and the standard library are
// written to the same rules by hand.

// goEffect is what using a name of a Go package does: effect, unless
// the name is one of pure (or matches purePrefix).
type goEffect struct {
	effect Effects
	what   string // as in "reads the clock"
	// only lists the names that have the effect; when empty, every
	// name but those of pure does.
	only         []string
	pure         []string
	purePrefixes []string
}

var goEffects = map[string]goEffect{
	"os": {effect: EffIO, what: "works with the process, files, or the environment",
		pure:         []string{"PathError", "LinkError", "SyscallError", "FileMode", "FileInfo", "DirEntry", "ModeDir", "ModePerm"},
		purePrefixes: []string{"Err", "Is"}},
	"os/exec":   {effect: EffIO, what: "runs programs"},
	"os/signal": {effect: EffIO, what: "handles signals"},
	"syscall":   {effect: EffIO, what: "makes system calls"},
	"io/ioutil": {effect: EffIO, what: "reads or writes files"},
	"fmt": {effect: EffIO, what: "prints or scans",
		only: []string{"Print", "Printf", "Println", "Fprint", "Fprintf", "Fprintln", "Scan", "Scanf", "Scanln", "Fscan", "Fscanf", "Fscanln"}},
	"net": {effect: EffNet, what: "uses the network"},
	"time": {effect: EffClock, what: "reads the clock or waits",
		only: []string{"Now", "Since", "Until", "Sleep", "After", "AfterFunc", "Tick", "NewTimer", "NewTicker"}},
	"math/rand":    {effect: EffRandom, what: "makes random numbers"},
	"math/rand/v2": {effect: EffRandom, what: "makes random numbers"},
	"crypto/rand":  {effect: EffRandom, what: "makes random numbers"},
	"sync":         {effect: EffState, what: "shares state between goroutines"},
	"sync/atomic":  {effect: EffState, what: "shares state between goroutines"},
}

// pureNet lists the packages under net/ that only parse and format.
var pureNet = map[string]bool{"net/url": true, "net/netip": true, "net/mail": true}

func goEffectOf(importPath string) (goEffect, bool) {
	if e, ok := goEffects[importPath]; ok {
		return e, true
	}
	if strings.HasPrefix(importPath, "net/") && !pureNet[importPath] {
		return goEffects["net"], true
	}
	return goEffect{}, false
}

func (e goEffect) has(name string) bool {
	if len(e.only) > 0 {
		for _, n := range e.only {
			if n == name {
				return true
			}
		}
		return false
	}
	for _, n := range e.pure {
		if n == name {
			return false
		}
	}
	for _, p := range e.purePrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// goPackageName is the name a Go import is used by: its last element,
// or the one before a major version (math/rand/v2 is rand).
func goPackageName(importPath string) string {
	name := path.Base(importPath)
	if majorVersion.MatchString(name) {
		name = path.Base(path.Dir(importPath))
	}
	return name
}

// goUse is a use of an effect in an unsafe go body.
type goUse struct {
	effect Effects
	pos    diag.Pos
	text   string // "it calls time.Now, which reads the clock"
}

// goEffectUses finds the uses of effects in an unsafe go body. The Go
// imports of every body end up in one generated file, so a package
// name counts even if another body imports it. funcs finds the bork
// functions the body can call by their names; params are the names of
// the function's parameters, which the body sees as locals.
func goEffectUses(gc *syntax.GoCode, params map[string]bool, funcs func(name string) *Func, imports map[string]string) []goUse {
	const prefix = "package p\n\nfunc _() {"
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "", prefix+gc.Body+"}\n", 0)
	if err != nil {
		return nil // the parser reported it
	}
	pkgs := map[string]string{} // name -> import path
	for imp := range goEffects {
		if _, ok := pkgs[goPackageName(imp)]; !ok {
			pkgs[goPackageName(imp)] = imp
		}
	}
	for _, imp := range gc.Imports {
		pkgs[goPackageName(imp)] = imp
	}
	for name, imp := range imports {
		pkgs[name] = imp
	}
	at := func(p gotoken.Pos) diag.Pos {
		pos := fset.Position(p)
		line, col := pos.Line-3, pos.Column-1
		if line == 0 {
			col -= len("func _() {")
			return diag.Pos{File: gc.Pos.File, Line: gc.Pos.Line, Col: gc.Pos.Col + 1 + col}
		}
		return diag.Pos{File: gc.Pos.File, Line: gc.Pos.Line + line, Col: col + 1}
	}
	// Names that are not references: fields and struct literal keys.
	notRefs := map[*ast.Ident]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			notRefs[n.Sel] = true
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok {
				notRefs[id] = true
			}
		}
		return true
	})
	var uses []goUse
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.Ident:
			// A bork function, unless the body declares the name.
			if n.Obj != nil || notRefs[n] || params[n.Name] || gotypes.Universe.Lookup(n.Name) != nil {
				return true
			}
			if fn := funcs(n.Name); fn != nil && fn.Effects&^EffOpen != 0 {
				uses = append(uses, goUse{fn.Effects &^ EffOpen, at(n.Pos()), "it calls " + n.Name})
			}
		case *ast.SelectorExpr:
			id, ok := n.X.(*ast.Ident)
			if !ok || id.Obj != nil || params[id.Name] {
				return true
			}
			imp, ok := pkgs[id.Name]
			if !ok {
				return true
			}
			if e, ok := goEffectOf(imp); ok && e.has(n.Sel.Name) {
				uses = append(uses, goUse{e.effect, at(n.Pos()), "it uses " + id.Name + "." + n.Sel.Name + ", which " + e.what})
			}
			return false // not a bork function
		case *ast.GoStmt:
			uses = append(uses, goUse{EffState, at(n.Pos()), "it starts a goroutine"})
		case *ast.SendStmt:
			uses = append(uses, goUse{EffState, at(n.Pos()), "it sends on a channel"})
		case *ast.UnaryExpr:
			if n.Op == gotoken.ARROW {
				uses = append(uses, goUse{EffState, at(n.Pos()), "it receives from a channel"})
			}
		case *ast.SelectStmt:
			uses = append(uses, goUse{EffState, at(n.Pos()), "it waits on channels"})
		}
		return true
	})
	return uses
}

// goBindUses is what a binding's Go function obviously does.
func goBindUses(b *syntax.GoBind) []goUse {
	i := strings.LastIndex(b.Name, ".")
	if i < 0 {
		return nil
	}
	if e, ok := goEffectOf(b.Name[:i]); ok && e.has(b.Name[i+1:]) {
		return []goUse{{e.effect, b.Pos, "it calls " + goPackageName(b.Name[:i]) + b.Name[i:] + ", which " + e.what}}
	}
	return nil
}

// checkUnsafeGo checks that fn, implemented in Go, declares what its
// body obviously does. The function values it gives may do it instead
// (an Atom's currentFn: () uses state => T), so what their types allow
// counts too.
func checkUnsafeGo(fn *Func, info *Info, diags *diag.List) {
	allowed := fn.Effects | effectsWithin(fn.Result, map[Type]bool{})
	var missing Effects
	var first *goUse
	funcs := func(name string) *Func {
		// An imported package's body sees its own functions by their
		// bork names (see gen.packageAliases); every body sees the
		// root package's and the prelude's.
		if fn.Pkg != nil && fn.Pkg.GoPrefix != "" {
			if f := fn.Pkg.Funcs[name]; f != nil {
				return f
			}
		}
		return info.Funcs[name]
	}
	params := map[string]bool{}
	for _, p := range fn.Decl.Params {
		params[p.Name] = true
	}
	var uses []goUse
	if fn.Decl.GoBind != nil {
		uses = goBindUses(fn.Decl.GoBind)
	} else {
		uses = goEffectUses(fn.Decl.GoBody, params, funcs, info.GoImportNames[fn.Decl.GoBody])
	}
	for _, u := range uses {
		if u.effect&^allowed&^missing != 0 {
			if first == nil {
				first = &u
			}
			missing |= u.effect &^ allowed
		}
	}
	if missing == 0 {
		return
	}
	fd := fn.Decl
	what := "unsafe go body"
	if fd.GoBind != nil {
		what = "Go binding"
	}
	diags.AddCode(first.pos, "effect.unsafe-go", "%s's %s uses %s (%s), but its signature allows %s; declare it: uses %s", fd.Name, what, missing, first.text, allowedText(fn.Effects), fn.Effects&^EffOpen|missing)
	diags.Suggest(first.pos, "effect.unsafe-go", first.pos, usesFix(fd, fn.Effects&^EffOpen|missing))
}

// effectsWithin is what the function types in t (its fields, elements,
// and type arguments) may use.
func effectsWithin(t Type, seen map[Type]bool) Effects {
	if seen[t] {
		return 0
	}
	seen[t] = true
	var effs Effects
	switch t := t.(type) {
	case *FuncType:
		effs = t.Effects &^ EffOpen
		effs |= effectsWithin(t.Result, seen)
	case *List:
		effs = effectsWithin(t.Elem, seen)
	case *Map:
		effs = effectsWithin(t.Key, seen) | effectsWithin(t.Value, seen)
	case *Union:
		for _, m := range t.Members {
			effs |= effectsWithin(m, seen)
		}
	case *Record:
		for _, f := range t.Fields {
			effs |= effectsWithin(f.Type, seen)
		}
	case *Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				effs |= effectsWithin(f.Type, seen)
			}
		}
	}
	return effs
}

// GoNames optionally supplies declared package names without loading their types.
// The driver provides it; checker-only tests may omit a Go environment.
type GoNames interface {
	Names(paths []string) map[string]string
}

// Aliases are file-local. Unaliased imports retain their shared namespace;
// collisions are diagnosed before generating Go.
func checkGoImports(files []*syntax.File, diags *diag.List, loader GoTypes) map[*syntax.GoCode]map[string]string {
	resolved := map[*syntax.GoCode]map[string]string{}
	paths := map[string]bool{}
	for _, file := range files {
		for _, fn := range file.Funcs {
			if fn.GoBody == nil {
				continue
			}
			for _, path := range fn.GoBody.Imports {
				if fn.GoBody.ImportAliases[path] == "" {
					paths[path] = true
				}
			}
		}
	}
	declared := map[string]string{}
	if names, ok := loader.(GoNames); ok {
		declared = names.Names(slices.Sorted(maps.Keys(paths)))
	}
	nameOf := func(path string) string {
		if name := declared[path]; name != "" {
			return name
		}
		return goPackageName(path)
	}
	shared := map[string]string{}
	for _, file := range files {
		names := map[string]string{}
		aliases := map[string]string{}
		for _, fn := range file.Funcs {
			if fn.GoBody == nil {
				continue
			}
			for _, path := range fn.GoBody.Imports {
				name := fn.GoBody.ImportAliases[path]
				explicit := name != ""
				if !explicit {
					name = nameOf(path)
				}
				previous, exists := names[name]
				localCollision := exists && previous != path
				if localCollision {
					diags.AddCode(fn.GoBody.Pos, "go.import-name", "Go packages %q and %q both use name %s in this file; give them distinct import aliases", previous, path, name)
				}
				names[name] = path
				if explicit {
					aliases[name] = path
				} else {
					if previous, exists := shared[name]; exists && previous != path && !localCollision {
						diags.AddCode(fn.GoBody.Pos, "go.import-name", "Go packages %q and %q both use name %s in generated Go; give one an explicit import alias", previous, path, name)
					}
					shared[name] = path
				}
			}
		}
		for _, fn := range file.Funcs {
			if fn.GoBody != nil {
				resolved[fn.GoBody] = aliases
			}
		}
	}
	for _, file := range files {
		for _, fn := range file.Funcs {
			if fn.GoBody == nil {
				continue
			}
			names := maps.Clone(shared)
			maps.Copy(names, resolved[fn.GoBody])
			resolved[fn.GoBody] = names
		}
	}
	return resolved
}
