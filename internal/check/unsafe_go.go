package check

import (
	"go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"path"
	"regexp"
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

// goEffectUses finds the uses of effects in an unsafe go body.
func goEffectUses(gc *syntax.GoCode) []goUse {
	const prefix = "package p\n\nfunc _() {"
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, "", prefix+gc.Body+"}\n", 0)
	if err != nil {
		return nil // the parser reported it
	}
	pkgs := map[string]string{} // name -> import path
	for _, imp := range gc.Imports {
		pkgs[goPackageName(imp)] = imp
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
	var uses []goUse
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			id, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			imp, ok := pkgs[id.Name]
			if !ok {
				return true
			}
			if e, ok := goEffectOf(imp); ok && e.has(n.Sel.Name) {
				uses = append(uses, goUse{e.effect, at(n.Pos()), "it uses " + id.Name + "." + n.Sel.Name + ", which " + e.what})
			}
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

// checkUnsafeGo checks that fn, implemented in Go, declares what its
// body obviously does. The function values it gives may do it instead
// (an Atom's currentFn: () uses state => T), so what their types allow
// counts too.
func checkUnsafeGo(fn *Func, diags *diag.List) {
	allowed := fn.Effects | effectsWithin(fn.Result, map[Type]bool{})
	var missing Effects
	var first *goUse
	for _, u := range goEffectUses(fn.Decl.GoBody) {
		if u.effect&allowed == 0 && missing&u.effect == 0 {
			missing |= u.effect
			if first == nil {
				first = &u
			}
		}
	}
	if missing == 0 {
		return
	}
	fd := fn.Decl
	diags.AddCode(first.pos, "effect.unsafe-go", "%s's unsafe go body uses %s (%s), but its signature allows %s; declare it: uses %s", fd.Name, missing, first.text, allowedText(fn.Effects), fn.Effects&^EffOpen|missing)
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
