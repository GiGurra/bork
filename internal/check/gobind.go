package check

import (
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Bindings: `fn Getenv(key: String): String unsafe go "os.Getenv"` calls
// a Go function directly. The checker checks the bork signature against
// the Go function's, as go/types sees it, and records how the values
// convert at the boundary (see the Go interop proposal in
// docs/requirements.md). The generator writes the wrapper.

// GoTypes gives the checker the Go packages that bindings name. The
// driver loads them with go/packages; tests can give fakes.
type GoTypes interface {
	// Load returns the packages at the given import paths, and the
	// error of each that could not be loaded.
	Load(paths []string) (map[string]*types.Package, map[string]error)
}

// GoResultShape is the form of a bound Go function's results.
type GoResultShape int

const (
	GoNoResult   GoResultShape = iota // f()
	GoValue                           // f() T
	GoErrorOnly                       // f() error
	GoValueError                      // f() (T, error)
	GoValueOk                         // f() (T, bool)
)

// GoBinding is a checked binding of a bork function to a Go function.
type GoBinding struct {
	// Path is the Go package's import path, and Name the function's.
	Path, Name string
	Sig        *types.Signature
	Shape      GoResultShape
	// Value is the bork type the Go result value converts to: the
	// result's leftmost member (an Option for GoValueOk), or nil.
	Value Type
	// Fallible is set when converting the result from Go can fail, so
	// the bork result has GoValueError.
	Fallible bool
	// GoError and GoValueError are the prelude's records, when the
	// result has them.
	GoError, GoValueError Type
}

// splitGoName splits "crypto/sha256.Sum256" into its import path and
// name. The path is everything before the last "." after the last "/".
func splitGoName(s string) (path, name string, ok bool) {
	slash := strings.LastIndex(s, "/")
	dot := strings.LastIndex(s, ".")
	if dot <= slash || dot == len(s)-1 {
		return "", "", false
	}
	return s[:dot], s[dot+1:], true
}

// checkBindings checks every binding of the program against Go.
func (c *checker) checkBindings(files []*syntax.File, goTypes GoTypes) {
	type pending struct {
		file *syntax.File
		fn   *Func
		path string
		name string
	}
	var all []pending
	paths := map[string]bool{}
	for _, f := range files {
		for _, fd := range f.Funcs {
			fn := c.info.FuncOf[fd]
			if fd.GoBind == nil || fn == nil {
				continue
			}
			c.inFile(f)
			if strings.HasPrefix(fd.GoBind.Name, "(") {
				c.bindErr(fd.GoBind.Pos, "binding Go methods is not supported yet; write an unsafe go body")
				continue
			}
			path, name, ok := splitGoName(fd.GoBind.Name)
			if !ok {
				c.bindErr(fd.GoBind.Pos, "%q is not a Go function name; write its import path and name, such as \"os.Getenv\"", fd.GoBind.Name)
				continue
			}
			if path == "internal" || strings.HasPrefix(path, "internal/") || strings.Contains(path, "/internal/") || strings.HasSuffix(path, "/internal") || strings.HasPrefix(path, "vendor/") {
				c.bindErr(fd.GoBind.Pos, "Go package %s is internal, so it cannot be bound", path)
				continue
			}
			all = append(all, pending{f, fn, path, name})
			paths[path] = true
		}
	}
	if len(all) == 0 {
		return
	}
	if goTypes == nil {
		c.diags.AddCode(all[0].fn.Decl.GoBind.Pos, "tool.error", "checking Go bindings needs Go's type information, which is not available here")
		return
	}
	var list []string
	for p := range paths {
		list = append(list, p)
	}
	sort.Strings(list)
	pkgs, errs := goTypes.Load(list)
	for _, b := range all {
		c.inFile(b.file)
		if err := errs[b.path]; err != nil {
			if strings.Contains(b.path[strings.LastIndex(b.path, "/")+1:], ".") {
				// "os.FileMode.String": a method.
				c.bindErr(b.fn.Decl.GoBind.Pos, "Go package %q cannot be loaded; to bind a method, write it as (T).M, which is not supported yet", b.path)
				continue
			}
			c.bindErr(b.fn.Decl.GoBind.Pos, "Go package %q cannot be loaded: %v", b.path, err)
			continue
		}
		c.checkBinding(b.fn, pkgs[b.path], b.path, b.name)
	}
}

func (c *checker) bindErr(pos diag.Pos, format string, args ...any) {
	for i, a := range args {
		if t, ok := a.(Type); ok && t != nil {
			args[i] = TypeText(t, c.pkg)
		}
	}
	c.diags.AddCode(pos, "bind.error", format, args...)
}

// checkBinding checks one binding, and records it in Info.GoBindings.
func (c *checker) checkBinding(fn *Func, pkg *types.Package, path, name string) {
	fd := fn.Decl
	pos := fd.GoBind.Pos
	full := path + "." + name
	if pkg == nil {
		c.bindErr(pos, "Go package %q not found", path)
		return
	}
	if !ast.IsExported(name) {
		c.bindErr(pos, "%s is not exported, so it cannot be bound", full)
		return
	}
	obj, _ := pkg.Scope().Lookup(name).(*types.Func)
	if obj == nil {
		if pkg.Scope().Lookup(name) == nil {
			c.bindErr(pos, "Go package %s has no function %s", path, name)
		} else {
			c.bindErr(pos, "%s is not a function, so it cannot be bound", full)
		}
		return
	}
	sig := obj.Type().(*types.Signature)
	goText := full + strings.TrimPrefix(types.TypeString(sig, goQualifier), "func")
	if sig.TypeParams().Len() > 0 {
		c.bindErr(pos, "%s is generic, and generic Go functions cannot be bound yet", full)
		return
	}
	if len(fn.TypeParams) > 0 {
		c.bindErr(fd.Pos, "a binding cannot have type parameters: %s has none", full)
		return
	}
	if len(fn.ResultConstraints) > 0 {
		c.bindErr(fd.Result.Pos, "facts on a binding's result are not supported yet; check them in bork after the call")
		return
	}
	b := &GoBinding{Path: path, Name: name, Sig: sig}
	ok := true
	params := sig.Params()
	if params.Len() != len(fn.Params) {
		c.bindErr(fd.Pos, "%s is bound to %s, which takes %d parameters, but it has %d", fd.Name, goText, params.Len(), len(fn.Params))
		ok = false
	} else {
		for i, pt := range fn.Params {
			gt := params.At(i).Type()
			if sig.Variadic() && i == params.Len()-1 {
				gt = gt.(*types.Slice) // ...T is []T
			}
			if !c.toGo(pt, gt) {
				c.bindErr(fd.Params[i].Pos, "%s is bound to %s, but parameter %s is %s, which does not convert to Go %s", fd.Name, goText, fd.Params[i].Name, pt, goTypeText(gt))
				ok = false
			}
		}
	}
	// The results: the Go function's, and the bork result's members.
	results := sig.Results()
	var value types.Type
	switch {
	case results.Len() == 0:
		b.Shape = GoNoResult
	case results.Len() == 1 && isGoError(results.At(0).Type()):
		b.Shape = GoErrorOnly
	case results.Len() == 1:
		b.Shape, value = GoValue, results.At(0).Type()
	case results.Len() == 2 && isGoError(results.At(1).Type()):
		b.Shape, value = GoValueError, results.At(0).Type()
	case results.Len() == 2 && types.Identical(results.At(1).Type(), types.Typ[types.Bool]):
		b.Shape, value = GoValueOk, results.At(0).Type()
	default:
		c.bindErr(pos, "%s returns %s, which cannot be bound: only one result, a result and an error, or a result and a bool can be; write an unsafe go body", full, goTypeText(results))
		return
	}
	if !ok {
		return
	}
	members := []Type{fn.Result}
	if u, isUnion := fn.Result.(*Union); isUnion {
		members = u.Members
	}
	b.Value = members[0]
	goErr, goValErr := c.info.Named["GoError"], c.info.Named["GoValueError"]
	hasErr, hasValErr := false, false
	for _, m := range members[1:] {
		switch {
		case identical(m, goErr):
			hasErr = true
		case identical(m, goValErr):
			hasValErr = true
		default:
			c.bindErr(fd.Result.Pos, "%s is bound to %s, which cannot return %s: a binding's result can only add GoError and GoValueError", fd.Name, goText, m)
			return
		}
	}
	if b.Shape == GoNoResult || b.Shape == GoErrorOnly {
		if b.Value != Unit {
			c.bindErr(resultPos(fd), "%s is bound to %s, which returns no value, but its result is %s", fd.Name, goText, b.Value)
			return
		}
		b.Value = nil
	} else {
		var conv convResult
		if b.Shape == GoValueOk {
			if !IsOption(b.Value) {
				c.bindErr(resultPos(fd), "%s is bound to %s, which returns a value and a bool, so its result is an Option, not %s", fd.Name, goText, b.Value)
				return
			}
			conv = c.fromGo(value, TypeArgs(b.Value)[0])
		} else {
			conv = c.fromGo(value, b.Value)
			if b.Shape == GoValueError && conv.ok && isGoNillable(value) && !IsOption(b.Value) {
				// A nil result without an error is a GoError (the Go
				// function is broken), not bad data.
				conv = c.fromGo(goElemOfNil(value), b.Value)
			}
		}
		if !conv.ok {
			c.bindErr(resultPos(fd), "%s is bound to %s, whose result %s does not convert to %s", fd.Name, goText, goTypeText(value), b.Value)
			return
		}
		b.Fallible = conv.fallible
	}
	needErr := b.Shape == GoErrorOnly || b.Shape == GoValueError
	want := func() string {
		parts := []string{"Unit"}
		if b.Value != nil {
			parts[0] = TypeText(b.Value, c.pkg)
		}
		if needErr {
			parts = append(parts, "GoError")
		}
		if b.Fallible {
			parts = append(parts, "GoValueError")
		}
		return strings.Join(parts, " | ")
	}
	switch {
	case needErr && !hasErr:
		c.bindErr(resultPos(fd), "%s is bound to %s, which can fail with an error, but its result has no GoError\n  hint: declare the result as %s", fd.Name, goText, want())
	case !needErr && hasErr:
		c.bindErr(resultPos(fd), "%s is bound to %s, which returns no error, so its result cannot have GoError\n  hint: declare the result as %s", fd.Name, goText, want())
	case b.Fallible && !hasValErr:
		c.bindErr(resultPos(fd), "%s is bound to %s, whose result %s may not fit %s, but its result has no GoValueError\n  hint: declare the result as %s", fd.Name, goText, goTypeText(value), b.Value, want())
	case !b.Fallible && hasValErr:
		c.bindErr(resultPos(fd), "%s is bound to %s, whose result always converts, so its result cannot have GoValueError\n  hint: declare the result as %s", fd.Name, goText, want())
	default:
		if hasErr {
			b.GoError = goErr
		}
		if hasValErr {
			b.GoValueError = goValErr
		}
		c.info.GoBindings[fn] = b
	}
}

func resultPos(fd *syntax.FuncDecl) diag.Pos {
	if fd.Result != nil {
		return fd.Result.Pos
	}
	return fd.Pos
}

func isGoError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

// isGoNillable reports whether a Go result can be nil where bork expects
// a value.
func isGoNillable(t types.Type) bool {
	_, ok := t.Underlying().(*types.Pointer)
	return ok
}

// goElemOfNil is what a nil-able result converts as, once a nil without
// an error is reported as a GoError: the value it points to.
func goElemOfNil(t types.Type) types.Type { return t.Underlying().(*types.Pointer).Elem() }

// goQualifier names other packages by their names, as Go code does.
func goQualifier(p *types.Package) string { return p.Name() }

func goTypeText(t types.Type) string { return types.TypeString(t, goQualifier) }

// convResult says whether a conversion is allowed, and whether it can
// fail at run time.
type convResult struct{ ok, fallible bool }

// goNumber is the bork number type of a Go basic number type, or nil.
func goNumber(b *types.Basic) Type {
	switch b.Kind() {
	case types.Int8:
		return Int8
	case types.Int16:
		return Int16
	case types.Int32:
		return Int32
	case types.Int64, types.Int:
		return Int
	case types.Uint8:
		return Uint8
	case types.Uint16:
		return Uint16
	case types.Uint32:
		return Uint32
	case types.Uint64, types.Uint:
		return Uint64
	case types.Float32:
		return Float32
	case types.Float64:
		return Float
	}
	return nil
}

// fromGo says how a Go value of type g converts to bork type t.
func (c *checker) fromGo(g types.Type, t Type) convResult {
	no := convResult{}
	if !goTypeVisible(g) {
		return no
	}
	switch u := g.Underlying().(type) {
	case *types.Basic:
		if n := goNumber(u); n != nil {
			switch {
			case IsFloat(n) || IsFloat(t):
				return convResult{ok: n == t}
			case IsInteger(t):
				return convResult{ok: true, fallible: !alwaysFits(n, t)}
			}
			return no
		}
		switch u.Kind() {
		case types.Bool:
			return convResult{ok: t == Bool}
		case types.String:
			return convResult{ok: t == String}
		}
		return no
	case *types.Slice:
		if t == Bytes {
			return convResult{ok: isGoByte(u.Elem())}
		}
		if l, ok := t.(*List); ok {
			return c.fromGo(u.Elem(), l.Elem)
		}
	case *types.Array:
		if t == Bytes {
			return convResult{ok: isGoByte(u.Elem())}
		}
		if l, ok := t.(*List); ok {
			return c.fromGo(u.Elem(), l.Elem)
		}
	case *types.Map:
		if m, ok := t.(*Map); ok && isKeyType(m.Key) && isGoKeyType(u.Key()) {
			k, v := c.fromGo(u.Key(), m.Key), c.fromGo(u.Elem(), m.Value)
			return convResult{ok: k.ok && v.ok, fallible: k.fallible || v.fallible}
		}
	case *types.Pointer:
		if IsOption(t) {
			return c.fromGo(u.Elem(), TypeArgs(t)[0])
		}
		r := c.fromGo(u.Elem(), t)
		return convResult{ok: r.ok, fallible: true} // nil
	}
	return no
}

// toGo reports whether a bork value of type t converts to Go type g.
// Converting to Go never fails.
func (c *checker) toGo(t Type, g types.Type) bool {
	if !goTypeVisible(g) {
		return false
	}
	switch u := g.Underlying().(type) {
	case *types.Basic:
		if n := goNumber(u); n != nil {
			if IsFloat(n) || IsFloat(t) {
				return IsFloat(n) && IsFloat(t) && bitsOf(t) <= bitsOf(n)
			}
			return IsInteger(t) && alwaysFits(t, n)
		}
		switch u.Kind() {
		case types.Bool:
			return t == Bool
		case types.String:
			return t == String
		}
		return false
	case *types.Slice:
		if t == Bytes {
			return isGoByte(u.Elem())
		}
		if l, ok := t.(*List); ok {
			return c.toGo(l.Elem, u.Elem())
		}
	case *types.Map:
		if m, ok := t.(*Map); ok && isKeyType(m.Key) && isGoKeyType(u.Key()) {
			return c.toGo(m.Key, u.Key()) && c.toGo(m.Value, u.Elem())
		}
	case *types.Pointer:
		if IsOption(t) {
			return c.toGo(TypeArgs(t)[0], u.Elem())
		}
		return c.toGo(t, u.Elem())
	}
	return false
}

// isGoByte reports whether t is byte itself (not a named byte type,
// whose slices do not convert to []byte).
func isGoByte(t types.Type) bool { return types.Identical(t, types.Typ[types.Byte]) }

// isKeyType reports whether a map key converts one to one at the
// boundary: integers, String, and Bool. (Not floats: NaN keys are
// never equal, so a Go map can hold several.)
func isKeyType(t Type) bool { return IsInteger(t) || t == String || t == Bool }

// isGoKeyType is isKeyType for the Go side: an integer, string, or bool
// type, so that two different Go keys never become one bork key.
func isGoKeyType(t types.Type) bool {
	b, ok := t.Underlying().(*types.Basic)
	return ok && b.Info()&(types.IsInteger|types.IsString|types.IsBoolean) != 0 && b.Kind() != types.Uintptr
}

// goTypeVisible reports whether generated code can name a Go type: it
// is not a named type that its package does not export.
func goTypeVisible(t types.Type) bool {
	n, ok := t.(*types.Named)
	return !ok || n.Obj().Pkg() == nil || n.Obj().Exported()
}
