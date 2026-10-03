package gen

import (
	"fmt"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/syntax"
)

// Bindings (`fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`)
// become wrappers written by the compiler, as Go: they convert the
// arguments to Go, call the bound function, and convert its results
// back, returning GoError for a Go error and GoValueError for a value
// that does not fit its bork type. See the Go interop proposal in
// docs/requirements.md for the mapping.
//
//	func Atoi(s string) any {
//		_r, _err := _go_strconv.Atoi(s)
//		if _err != nil { return _bindGoError(_err) }
//		return int64(_r)
//	}

// bindFunc generates the wrapper of a binding.
func (g *gen) bindFunc(fd *syntax.FuncDecl, goName string) (string, error) {
	fn := g.info.FuncOf[fd]
	b := g.info.GoBindings[fn]
	g.usesBind = true
	// The wrapper names its parameters itself, so any bork name works.
	sig := g.signature(fd)
	sig.Name.Name = goName
	w := &bindWriter{g: g, b: b}
	var args []string
	params := b.Sig.Params()
	for i := range fd.Params {
		sig.Type.Params.List[i].Names[0].Name = "_a" + strconv.Itoa(i)
	}
	for i, pi := range b.ParamIndices {
		pname := "_a" + strconv.Itoa(pi)
		arg := w.toGo(pname, fn.Params[pi], params.At(i).Type())
		if b.Sig.Variadic() && i == params.Len()-1 {
			arg += "..."
		}
		args = append(args, arg)
	}
	var target string
	if b.Receiver != nil {
		target = "(" + w.goType(b.Receiver) + ")." + b.Name[strings.Index(b.Name, ").")+2:]
	} else {
		target = g.goImport(b.Path) + "." + b.Name
	}
	call := target + "(" + strings.Join(args, ", ") + ")"
	switch b.Shape {
	case check.GoNoResult:
		w.line(call)
	case check.GoErrorOnly:
		w.line("if _err := " + call + "; _err != nil { return _bindGoError(_err) }")
		g.usesUnit = true
		w.line("return _Unit{}")
	case check.GoValue:
		w.line("_r := " + call)
		w.line("return " + w.fromGo("_r", b.Sig.Results().At(0).Type(), b.Value, `"result"`))
	case check.GoValueError:
		w.line("_r, _err := " + call)
		w.line("if _err != nil { return _bindGoError(_err) }")
		rt := b.Sig.Results().At(0).Type()
		if check.GoTypeOf(b.Value) != nil && !check.IsOption(b.Value) {
			if goNillable(rt) {
				w.line(`if _r == nil { return _bindGoError(nil) }`)
			}
			w.line("return " + w.fromGo("_r", rt, b.Value, `"result"`))
		} else if p, ok := rt.Underlying().(*types.Pointer); ok && !check.IsOption(b.Value) {
			// A nil result without an error is the Go function's bug.
			w.line(`if _r == nil { return _bindGoError(nil) }`)
			w.line("return " + w.fromGo("*_r", p.Elem(), b.Value, `"result"`))
		} else {
			w.line("return " + w.fromGo("_r", rt, b.Value, `"result"`))
		}
	case check.GoValueOk:
		elem := check.TypeArgs(b.Value)[0]
		g.usesOptionHelpers = true
		w.line("_r, _ok := " + call)
		w.line("if !_ok { return _borkNone[" + g.typeText(elem) + "]() }")
		w.line("return _borkSome(" + w.fromGo("_r", b.Sig.Results().At(0).Type(), elem, `"result"`) + ")")
	}
	// The Go compiler reports errors in the wrapper (which would be
	// compiler bugs) at the binding.
	pos := fd.GoBind.Pos
	file := pos.File
	if abs, err := filepath.Abs(file); err == nil {
		file = abs
	}
	return fmt.Sprintf("%s {/*line %s:%d:%d*/\n%s}\n", g.text(sig), file, pos.Line, pos.Col, w.body.String()), nil
}

// goImport imports a Go package for bindings, under a name that cannot
// clash with generated names or other imports: _go_strconv.
func (g *gen) goImport(path string) string {
	alias := "_go_" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, path)
	g.bindImports[path] = alias
	return alias
}

// bindWriter writes the body of a binding's wrapper.
type bindWriter struct {
	g    *gen
	b    *check.GoBinding
	body strings.Builder
	tmp  int
}

func (w *bindWriter) line(s string) { w.body.WriteString("\t" + s + "\n") }

func (w *bindWriter) newTmp() string {
	w.tmp++
	return "_v" + strconv.Itoa(w.tmp)
}

// goType is Go code for a Go type, with packages named by their imports.
func (w *bindWriter) goType(t types.Type) string {
	return types.TypeString(t, func(p *types.Package) string { return w.g.goImport(p.Path()) })
}

// toGo converts the bork value x, of type t, to Go type gt. Converting
// to Go never fails (the checker allows only lossless conversions), and
// always copies, so Go code cannot change a bork value.
func (w *bindWriter) toGo(x string, t check.Type, gt types.Type) string {
	if check.GoTypeOf(t) != nil {
		w.g.usesOpaque = true
		return "_borkGo(" + x + ")"
	}
	if t == check.Scope {
		w.g.usesScopes = true
		return "_borkScopeContext(" + x + ")"
	}
	if check.IsOption(t) && check.GoTypeOf(check.TypeArgs(t)[0]) != nil && goNillable(check.GoTypeOf(check.TypeArgs(t)[0])) {
		v, e, ok := w.newTmp(), w.newTmp(), w.newTmp()
		w.g.usesOptionHelpers = true
		w.g.usesOpaque = true
		w.line(fmt.Sprintf("var %s %s", v, w.goType(gt)))
		w.line(fmt.Sprintf("if %s, %s := _borkOptionGet(%s); %s { %s = _borkGo(%s) }", e, ok, x, ok, v, e))
		return v
	}

	switch u := gt.Underlying().(type) {
	case *types.Basic:
		return w.goType(gt) + "(" + x + ")"
	case *types.Slice:
		if t == check.Bytes {
			w.g.usesBytes = true
			return w.goType(gt) + "(_borkBytesData(" + x + "))"
		}
		elem := t.(*check.List).Elem
		v, i, e := w.newTmp(), w.newTmp(), w.newTmp()
		w.line(fmt.Sprintf("%s := make(%s, len(%s))", v, w.goType(gt), x))
		w.line(fmt.Sprintf("for %s, %s := range %s {", i, e, x))
		w.line(fmt.Sprintf("%s[%s] = %s", v, i, w.toGo(e, elem, u.Elem())))
		w.line("}")
		return v
	case *types.Map:
		m := t.(*check.Map)
		w.g.usesMap = true
		v, k, e := w.newTmp(), w.newTmp(), w.newTmp()
		w.line(fmt.Sprintf("%s := make(%s, _borkMapLen(%s))", v, w.goType(gt), x))
		w.line(fmt.Sprintf("_borkMapEach(%s, func(%s %s, %s %s) bool {", x, k, w.g.typeText(m.Key), e, w.g.typeText(m.Value)))
		key := w.toGo(k, m.Key, u.Key())
		w.line(fmt.Sprintf("%s[%s] = %s", v, key, w.toGo(e, m.Value, u.Elem())))
		w.line("return true")
		w.line("})")
		return v
	case *types.Pointer:
		v := w.newTmp()
		if check.IsOption(t) {
			w.g.usesOptionHelpers = true
			e, ok := w.newTmp(), w.newTmp()
			w.line(fmt.Sprintf("var %s %s", v, w.goType(gt)))
			w.line(fmt.Sprintf("if %s, %s := _borkOptionGet(%s); %s {", e, ok, x, ok))
			p := w.newTmp()
			w.line(fmt.Sprintf("%s := %s", p, w.toGo(e, check.TypeArgs(t)[0], u.Elem())))
			w.line(fmt.Sprintf("%s = &%s", v, p))
			w.line("}")
			return v
		}
		w.line(fmt.Sprintf("%s := %s", v, w.toGo(x, t, u.Elem())))
		return "&" + v
	}
	panic(fmt.Sprintf("binding: no conversion from %s to Go %s", t, gt))
}

// fromGo converts the Go value x, of type gt, to bork type t. path is Go
// code for where the value is (`"result"`), for GoValueError. A value
// that does not fit returns a GoValueError from the wrapper.
func (w *bindWriter) fromGo(x string, gt types.Type, t check.Type, path string) string {
	fail := func(cond, message string) {
		w.g.goType(w.b.GoValueError)
		w.line(fmt.Sprintf("if %s { return _bindValueError(%s, %s) }", cond, path, message))
	}
	if check.GoTypeOf(t) != nil {
		w.g.usesOpaque = true
		if goNillable(gt) {
			// A top-level nil paired with a nil error was checked by bindFunc.
			if !(x == "_r" && w.b.Shape == check.GoValueError) {
				fail(x+" == nil", `"nil"`)
			}
		}
		if _, resource := t.(*check.Resource); resource {
			v := w.newTmp()
			w.line(v + " := " + x)
			return fmt.Sprintf("%s{handle: %s, owner: _a%d.Own(func() { %s.Close() })}", w.g.typeText(t), v, w.b.ScopeIndex, v)
		}
		return w.g.typeText(t) + "{value: " + x + "}"
	}
	if check.IsOption(t) && check.GoTypeOf(check.TypeArgs(t)[0]) != nil && goNillable(check.GoTypeOf(check.TypeArgs(t)[0])) {
		elem := check.TypeArgs(t)[0]
		v := w.newTmp()
		w.g.usesOptionHelpers = true
		w.line(fmt.Sprintf("%s := _borkNone[%s]()", v, w.g.typeText(elem)))
		w.line("if " + x + " != nil {")
		// Conversion is non-fallible after the nil check.
		w.line(fmt.Sprintf("%s = _borkSome(%s)", v, w.opaqueValue(x, elem)))
		w.line("}")
		return v
	}
	switch u := gt.Underlying().(type) {
	case *types.Basic:
		bt := w.g.typeText(t)
		if check.IsInteger(t) && !fits(u, t) {
			v, ok := w.newTmp(), w.newTmp()
			w.line(fmt.Sprintf("%s, %s := _bindFits[%s](%s)", v, ok, bt, x))
			fail("!"+ok, fmt.Sprintf(`_bindRange(%s, %q)`, x, t.String()))
			return v
		}
		return bt + "(" + x + ")"
	case *types.Slice, *types.Array:
		if t == check.Bytes {
			w.g.usesBytes = true
			if _, ok := u.(*types.Array); ok {
				return "_borkBytesFrom((" + x + ")[:])"
			}
			return "_borkBytesFrom([]byte(" + x + "))"
		}
		elem := t.(*check.List).Elem
		var gelem types.Type
		if s, ok := u.(*types.Slice); ok {
			gelem = s.Elem()
		} else {
			gelem = u.(*types.Array).Elem()
		}
		v, i, e := w.newTmp(), w.newTmp(), w.newTmp()
		w.line(fmt.Sprintf("%s := make(%s, len(%s))", v, w.g.typeText(t), x))
		w.line(fmt.Sprintf("for %s, %s := range %s {", i, e, x))
		w.line(fmt.Sprintf("%s[%s] = %s", v, i, w.fromGo(e, gelem, elem, fmt.Sprintf("_bindIndex(%s, %s)", path, i))))
		w.line("}")
		return v
	case *types.Map:
		m := t.(*check.Map)
		w.g.usesMap = true
		ks, vs, k, e := w.newTmp(), w.newTmp(), w.newTmp(), w.newTmp()
		w.line(fmt.Sprintf("%s := make([]%s, 0, len(%s))", ks, w.g.typeText(m.Key), x))
		w.line(fmt.Sprintf("%s := make([]%s, 0, len(%s))", vs, w.g.typeText(m.Value), x))
		w.line(fmt.Sprintf("for %s, %s := range %s {", k, e, x))
		key := w.fromGo(k, u.Key(), m.Key, fmt.Sprintf("_bindKey(%s, %s)", path, k))
		val := w.fromGo(e, u.Elem(), m.Value, fmt.Sprintf("_bindKey(%s, %s)", path, k))
		w.line(fmt.Sprintf("%s = append(%s, %s)", ks, ks, key))
		w.line(fmt.Sprintf("%s = append(%s, %s)", vs, vs, val))
		w.line("}")
		// A Go map has no order, so it becomes an unordered map.
		return fmt.Sprintf("_mapUnordered(_borkMapOf(%s, %s))", ks, vs)
	case *types.Pointer:
		if check.IsOption(t) {
			w.g.usesOptionHelpers = true
			elem := check.TypeArgs(t)[0]
			v := w.newTmp()
			w.line(fmt.Sprintf("%s := _borkNone[%s]()", v, w.g.typeText(elem)))
			w.line(fmt.Sprintf("if %s != nil {", x))
			w.line(fmt.Sprintf("%s = _borkSome(%s)", v, w.fromGo("*"+x, u.Elem(), elem, path)))
			w.line("}")
			return v
		}
		fail(x+" == nil", `"nil"`)
		return w.fromGo("*"+x, u.Elem(), t, path)
	}
	panic(fmt.Sprintf("binding: no conversion from Go %s to %s", gt, t))
}

// fits reports whether every value of the Go integer type g fits the
// bork integer type t.
func fits(g *types.Basic, t check.Type) bool {
	var from check.Type
	switch g.Kind() {
	case types.Int8:
		from = check.Int8
	case types.Int16:
		from = check.Int16
	case types.Int32:
		from = check.Int32
	case types.Int64, types.Int:
		from = check.Int
	case types.Uint8:
		from = check.Uint8
	case types.Uint16:
		from = check.Uint16
	case types.Uint32:
		from = check.Uint32
	default:
		from = check.Uint64
	}
	return check.AlwaysFits(from, t)
}

// bindRuntime supports the wrappers of bindings.
const bindRuntime = `package main

import (
	"fmt"
	"strconv"
)

type _bindInt interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 | ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

// _bindFits converts a Go integer to a bork one, and reports whether
// it fits.
func _bindFits[T, F _bindInt](v F) (T, bool) {
	t := T(v)
	return t, F(t) == v && (t < 0) == (v < 0)
}

func _bindRange(v any, target string) string { return fmt.Sprint(v) + " does not fit " + target }
func _bindIndex(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }
func _bindKey(path string, k any) string { return path + "[" + fmt.Sprint(k) + "]" }

func _bindValueError(path, message string) GoValueError {
	return GoValueError{path: path, message: message}
}

// _bindGoError is a Go error as a bork GoError. A nil error stands for
// a nil result without an error.
func _bindGoError(err error) (e GoError) {
	if err == nil {
		return GoError{message: "nil result without an error", goType: ""}
	}
	e.goType = fmt.Sprintf("%T", err)
	defer func() {
		if recover() != nil {
			e.message = "(its Error method panicked)"
		}
	}()
	e.message = err.Error()
	return e
}
`

func goNillable(t types.Type) bool {
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Interface:
		return true
	}
	return false
}

func (w *bindWriter) opaqueValue(x string, t check.Type) string {
	w.g.usesOpaque = true
	if _, resource := t.(*check.Resource); resource {
		v := w.newTmp()
		w.line(v + " := " + x)
		return fmt.Sprintf("%s{handle: %s, owner: _a%d.Own(func() { %s.Close() })}", w.g.typeText(t), v, w.b.ScopeIndex, v)
	}
	return w.g.typeText(t) + "{value: " + x + "}"
}
