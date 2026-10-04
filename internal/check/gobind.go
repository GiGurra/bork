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
	Path, Name   string
	Receiver     types.Type
	ParamIndices []int
	ScopeIndex   int
	Sig          *types.Signature
	Shape        GoResultShape
	// Value is the bork type the Go result value converts to: the
	// result's leftmost member (an Option for GoValueOk), or nil.
	Value Type
	// Fallible is set when converting the result from Go can fail, so
	// the bork result has GoValueError.
	Fallible bool
	// Contextual resource results share generated, attachable cancellation.
	Contextual bool
	// GoError and GoValueError are the prelude's records, when the
	// result has them.
	GoError, GoValueError Type
}

// splitGoName splits "crypto/sha256.Sum256" into its import path and
// name. The path is everything before the last "." after the last "/".
func splitGoName(s string) (path, name string, ok bool) {
	if strings.HasPrefix(s, "(") {
		end := strings.Index(s, ").")
		if end < 0 {
			return "", "", false
		}
		path, recv, ok := splitGoName(strings.TrimPrefix(s[1:end], "*"))
		if !ok || strings.Contains(s[end+2:], ".") {
			return "", "", false
		}
		if strings.HasPrefix(s[1:end], "*") {
			recv = "*" + recv
		}
		return path, "(" + recv + ")." + s[end+2:], true
	}
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
			path, name, ok := splitGoName(fd.GoBind.Name)
			if !ok {
				c.bindErr(fd.GoBind.Pos, "%q is not a Go function name; write its import path and name, such as \"os.Getenv\"", fd.GoBind.Name)
				continue
			}
			if forbiddenGoPath(path) {
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
	full := fd.GoBind.Name
	if pkg == nil {
		c.bindErr(pos, "Go package %q not found", path)
		return
	}
	var obj *types.Func
	var recv types.Type
	if strings.HasPrefix(name, "(") {
		end := strings.Index(name, ").")
		method := name[end+2:]
		recvName := name[1:end]
		pointer := strings.HasPrefix(recvName, "*")
		recvName = strings.TrimPrefix(recvName, "*")
		if !ast.IsExported(method) {
			c.bindErr(pos, "%s is not exported, so it cannot be bound", full)
			return
		}
		recv = c.namedGoType(path+"."+recvName, fd.GoBind)
		if recv == nil {
			return
		}
		if pointer {
			recv = types.NewPointer(recv)
		}
		selection := types.NewMethodSet(recv).Lookup(nil, method)
		if selection == nil {
			c.bindErr(pos, "Go type %s has no method %s", goTypeText(recv), method)
			return
		}
		obj = selection.Obj().(*types.Func)
	} else {
		if !ast.IsExported(name) {
			c.bindErr(pos, "%s is not exported, so it cannot be bound", full)
			return
		}
		obj, _ = pkg.Scope().Lookup(name).(*types.Func)
		if obj == nil {
			if pkg.Scope().Lookup(name) == nil {
				c.bindErr(pos, "Go package %s has no function %s", path, name)
			} else {
				c.bindErr(pos, "%s is not a function, so it cannot be bound", full)
			}
			return
		}
	}
	var checkSeqEffects func(*syntax.TypeExpr)
	checkSeqEffects = func(t *syntax.TypeExpr) {
		if t == nil {
			return
		}
		if t.Name == "Seq" && t.Uses == nil {
			c.bindErr(t.Pos, "a checked Go iterator binding must declare the Seq latent effects explicitly, including uses nothing")
		}
		for _, child := range t.Args {
			checkSeqEffects(child)
		}
		for _, child := range t.Union {
			checkSeqEffects(child)
		}
		if t.Func != nil {
			for _, child := range t.Func.Params {
				checkSeqEffects(child)
			}
			checkSeqEffects(t.Func.Result)
		}
	}
	for _, p := range fd.Params {
		checkSeqEffects(p.Type)
	}
	checkSeqEffects(fd.Result)
	sig := obj.Type().(*types.Signature)
	if recv != nil {
		ps := []*types.Var{types.NewVar(0, nil, "receiver", recv)}
		for i := 0; i < sig.Params().Len(); i++ {
			ps = append(ps, sig.Params().At(i))
		}
		sig = types.NewSignatureType(nil, nil, nil, types.NewTuple(ps...), sig.Results(), sig.Variadic())
	}
	goText := full + strings.TrimPrefix(types.TypeString(sig, goQualifier), "func")
	if sig.TypeParams().Len() > 0 {
		c.bindErr(pos, "%s is generic, and generic Go functions cannot be bound yet", full)
		return
	}
	if len(fn.TypeParams) > 0 {
		c.bindErr(fd.Pos, "a binding cannot have type parameters: %s has none", full)
		return
	}
	b := &GoBinding{Path: path, Name: name, Sig: sig, Receiver: recv, ScopeIndex: -1}
	ok := true
	params := sig.Params()
	for i, pt := range fn.Params {
		if pt == Scope {
			if b.ScopeIndex >= 0 && containsGoResource(fn.Result, map[Type]bool{}) {
				c.bindErr(fd.Pos, "a binding returning a Go resource needs exactly one Scope parameter")
				ok = false
			}
			b.ScopeIndex = i
		}
	}
	if containsGoResource(fn.Result, map[Type]bool{}) && b.ScopeIndex < 0 {
		c.bindErr(fd.Pos, "a binding returning a Go resource needs a Scope parameter to own its Close method")
		ok = false
	}
	for i := range fn.Params {
		if containsGoResource(fn.Result, map[Type]bool{}) && len(fn.Params) == params.Len()+1 && i == b.ScopeIndex {
			continue
		}
		b.ParamIndices = append(b.ParamIndices, i)
	}
	if params.Len() != len(b.ParamIndices) {
		c.bindErr(fd.Pos, "%s is bound to %s, which takes %d parameters, but it has %d", fd.Name, goText, params.Len(), len(fn.Params))
		ok = false
	} else {
		for i, pi := range b.ParamIndices {
			pt := fn.Params[pi]
			gt := params.At(i).Type()
			if !c.toGo(pt, gt) {
				c.bindErr(fd.Params[pi].Pos, "%s is bound to %s, but parameter %s is %s, which does not convert to Go %s", fd.Name, goText, fd.Params[pi].Name, pt, goTypeText(gt))
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
		if b.Value != Ok {
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
				if GoTypeOf(b.Value) != nil {
					conv.fallible = false
				} else {
					conv = c.fromGo(goElemOfNil(value), b.Value)
				}
			}
		}
		if !conv.ok {
			rt := b.Value
			if IsOption(rt) {
				rt = TypeArgs(rt)[0]
			}
			if _, resource := rt.(*Resource); hasGoClose(value) && !resource {
				c.bindErr(resultPos(fd), "%s returns %s, which has a Close method, but %s is not a resource, so nothing would close it\n  hint: declare the Go type with resource go", fd.Name, goTypeText(value), rt)
				return
			}
			c.bindErr(resultPos(fd), "%s is bound to %s, whose result %s does not convert to %s", fd.Name, goText, goTypeText(value), b.Value)
			return
		}
		b.Fallible = conv.fallible
	}
	if len(fn.ResultConstraints) > 0 {
		b.Fallible = true
	}
	needErr := b.Shape == GoErrorOnly || b.Shape == GoValueError
	want := func() string {
		parts := []string{"Ok"}
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
		for i, pi := range b.ParamIndices {
			gt := b.Sig.Params().At(i).Type()
			if containsGoResource(b.Value, map[Type]bool{}) && containsGoContext(gt, map[types.Type]bool{}) {
				b.Contextual = true
				if !canWrapBindingContexts(fn.Params[pi], gt, map[goConvPair]bool{}) {
					c.bindErr(fd.Params[pi].Pos, "a context inside an opaque Go value cannot be rebound for a resource binding\n  hint: expose its context fields in a mirror record or use an unsafe go wrapper")
					return
				}
			}
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
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Interface:
		return true
	}
	return false
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
type goConvPair struct {
	b Type
	g types.Type
}

func (c *checker) fromGo(g types.Type, t Type) convResult {
	return c.fromGoSeen(g, t, map[goConvPair]bool{})
}
func (c *checker) fromGoSeen(g types.Type, t Type, seen map[goConvPair]bool) convResult {
	pair := goConvPair{t, g}
	if seen[pair] {
		return convResult{ok: true, fallible: true}
	}
	seen[pair] = true
	defer delete(seen, pair)
	no := convResult{}
	if seq, ok := t.(*Seq); ok {
		if elem := goSeqElem(g); elem != nil {
			inner := c.fromGoSeen(elem, seq.Elem, seen)
			return convResult{ok: inner.ok && !inner.fallible}
		}
		return no
	}
	rt := t
	if IsOption(rt) {
		rt = TypeArgs(rt)[0]
	}
	if _, resource := rt.(*Resource); hasGoClose(g) && !resource {
		return no
	}
	if gt := GoTypeOf(t); gt != nil {
		return convResult{ok: types.Identical(g, gt), fallible: isGoNillable(g)}
	}
	if IsOption(t) && GoTypeOf(TypeArgs(t)[0]) != nil && isGoNillable(GoTypeOf(TypeArgs(t)[0])) {
		r := c.fromGoSeen(g, TypeArgs(t)[0], seen)
		r.fallible = false
		return r
	}
	if !goTypeVisible(g) {
		return no
	}
	switch u := g.Underlying().(type) {
	case *types.Basic:
		if t == Rune {
			return convResult{ok: u.Kind() == types.Int32, fallible: true}
		}
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
			return c.fromGoSeen(u.Elem(), l.Elem, seen)
		}
	case *types.Array:
		if t == Bytes {
			return convResult{ok: isGoByte(u.Elem())}
		}
		if l, ok := t.(*List); ok {
			return c.fromGoSeen(u.Elem(), l.Elem, seen)
		}
	case *types.Map:
		if m, ok := t.(*Map); ok && isKeyType(m.Key) && isGoKeyType(u.Key()) {
			k, v := c.fromGoSeen(u.Key(), m.Key, seen), c.fromGoSeen(u.Elem(), m.Value, seen)
			return convResult{ok: k.ok && v.ok, fallible: k.fallible || v.fallible}
		}
	case *types.Struct:
		r, ok := t.(*Record)
		if !ok || r.GoMirror == nil || !types.Identical(r.GoMirror, g) || len(r.GoFields) != len(r.Fields) {
			return no
		}
		out := convResult{ok: true}
		for i, f := range r.Fields {
			if f.Computed {
				continue
			}
			gf := r.GoFields[i]
			if gf.Type == nil {
				return no
			}
			v := c.fromGoSeen(gf.Type, f.Type, seen)
			out.ok = out.ok && v.ok
			out.fallible = out.fallible || v.fallible || len(f.Constraints) > 0
		}
		return out
	case *types.Pointer:
		if IsOption(t) {
			return c.fromGoSeen(u.Elem(), TypeArgs(t)[0], seen)
		}
		r := c.fromGoSeen(u.Elem(), t, seen)
		return convResult{ok: r.ok, fallible: true} // nil
	}
	return no
}

// toGo reports whether a bork value of type t converts to Go type g.
// Converting to Go never fails.
func (c *checker) toGo(t Type, g types.Type) bool { return c.toGoSeen(t, g, map[goConvPair]bool{}) }
func (c *checker) toGoSeen(t Type, g types.Type, seen map[goConvPair]bool) bool {
	pair := goConvPair{t, g}
	if seen[pair] {
		return true
	}
	seen[pair] = true
	defer delete(seen, pair)
	if seq, ok := t.(*Seq); ok {
		elem := goSeqElem(g)
		return elem != nil && c.toGoSeen(seq.Elem, elem, seen)
	}
	if gt := GoTypeOf(t); gt != nil {
		return types.AssignableTo(gt, g)
	}
	if t == Scope {
		return isGoContext(g)
	}
	if IsOption(t) && GoTypeOf(TypeArgs(t)[0]) != nil && isGoNillable(GoTypeOf(TypeArgs(t)[0])) {
		return types.AssignableTo(GoTypeOf(TypeArgs(t)[0]), g)
	}
	if !goTypeVisible(g) {
		return false
	}
	switch u := g.Underlying().(type) {
	case *types.Basic:
		if t == Rune {
			return u.Kind() == types.Int32
		}
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
			return c.toGoSeen(l.Elem, u.Elem(), seen)
		}
	case *types.Map:
		if m, ok := t.(*Map); ok && isKeyType(m.Key) && isGoKeyType(u.Key()) {
			return c.toGoSeen(m.Key, u.Key(), seen) && c.toGoSeen(m.Value, u.Elem(), seen)
		}
	case *types.Struct:
		r, ok := t.(*Record)
		if !ok || r.GoMirror == nil || !types.Identical(r.GoMirror, g) || len(r.GoFields) != len(r.Fields) {
			return false
		}
		for i, f := range r.Fields {
			if f.Computed {
				continue
			}
			if r.GoFields[i].Type == nil || !c.toGoSeen(f.Type, r.GoFields[i].Type, seen) {
				return false
			}
		}
		return true
	case *types.Pointer:
		if IsOption(t) {
			return c.toGoSeen(TypeArgs(t)[0], u.Elem(), seen)
		}
		return c.toGoSeen(t, u.Elem(), seen)
	}
	return false
}

// isGoByte reports whether t is byte itself (not a named byte type,
// whose slices do not convert to []byte).
func isGoByte(t types.Type) bool { return types.Identical(t, types.Typ[types.Byte]) }

// isKeyType reports whether a map key converts one to one at the
// boundary: integers, String, and Bool. (Not floats: NaN keys are
// never equal, so a Go map can hold several.)
func isKeyType(t Type) bool { return IsInteger(t) || t == Rune || t == String || t == Bool }

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

// Opaque values pass through unchanged; only an explicit context or a mirrored
// field can be replaced with the binding's stable cancellation context.
func canWrapBindingContexts(t Type, gt types.Type, seen map[goConvPair]bool) bool {
	if !containsGoContext(gt, map[types.Type]bool{}) || isGoContext(gt) {
		return true
	}
	pair := goConvPair{t, gt}
	if seen[pair] {
		return true
	}
	seen[pair] = true
	if GoTypeOf(t) != nil {
		return false
	}
	if IsOption(t) {
		t = TypeArgs(t)[0]
	}
	switch u := gt.Underlying().(type) {
	case *types.Pointer:
		return canWrapBindingContexts(t, u.Elem(), seen)
	case *types.Slice:
		l, ok := t.(*List)
		return ok && canWrapBindingContexts(l.Elem, u.Elem(), seen)
	case *types.Array:
		l, ok := t.(*List)
		return ok && canWrapBindingContexts(l.Elem, u.Elem(), seen)
	case *types.Map:
		m, ok := t.(*Map)
		return ok && canWrapBindingContexts(m.Key, u.Key(), seen) && canWrapBindingContexts(m.Value, u.Elem(), seen)
	case *types.Struct:
		r, ok := t.(*Record)
		if !ok {
			return false
		}
		for i, field := range r.Fields {
			if field.Computed {
				continue
			}
			if !canWrapBindingContexts(field.Type, r.GoFields[i].Type, seen) {
				return false
			}
		}
		return true
	}
	return false
}

// goSeqElem recognizes the explicit one-value iterator bridge.
func goSeqElem(t types.Type) types.Type {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "iter" || named.Obj().Name() != "Seq" || named.TypeArgs().Len() != 1 {
		return nil
	}
	return named.TypeArgs().At(0)
}
