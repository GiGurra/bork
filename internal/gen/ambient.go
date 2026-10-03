package gen

import (
	"go/ast"
	"go/token"
	"path"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

// Logged and propagated ambient values (see "Ambient values" in
// docs/requirements.md). A with that binds a marked value also publishes
// it in the goroutine's profiler labels, under the label key of its
// declaration, until the with ends; goroutines started meanwhile
// inherit it. Only the _bork helpers below read the labels, from
// standard-library boundary code: bork/log adds logged values to every
// record, and net clients and servers send and receive propagated ones.
// Bork code reads ambient values only through needs.

// ambientMarked lists the program's logged and propagated ambient
// declarations: _ambientDecls, in this order.
func (g *gen) ambientMarked() []*check.Ambient {
	var out []*check.Ambient
	for _, a := range g.info.Ambients {
		if a.Marked() && a.Type != check.Invalid {
			out = append(out, a)
		}
	}
	return out
}

// ambientPreds are the predicates incoming propagated values are
// checked with.
func (g *gen) ambientPreds() []*check.Func {
	var out []*check.Func
	for _, a := range g.ambientMarked() {
		if a.Header == "" {
			continue
		}
		for _, con := range a.Constraints {
			out = append(out, constraintPreds(con)...)
		}
	}
	return out
}

// ambientPush publishes a with's marked bindings, once they are bound:
// in the goroutine's labels until the with's block ends (on every way
// out of it, as a mock's block ends).
func (g *gen) ambientPush(labels []*check.Var) []ast.Stmt {
	g.usesAmbients = true
	index := map[*check.Ambient]int{}
	for i, a := range g.ambientMarked() {
		index[a] = i
	}
	var args []ast.Expr
	for _, v := range labels {
		args = append(args, &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(index[v.Ambient])}, varIdent(v))
	}
	g.mockN++
	frame := ast.NewIdent("_af" + strconv.Itoa(g.mockN))
	g.openMocks = append(g.openMocks, openMock{frame: frame, depth: len(g.openScopes), ambient: true})
	push := define(frame, &ast.CallExpr{Fun: ast.NewIdent("_ambientPush"), Args: args})
	if g.labelGuard != nil {
		// A body that never ends normally has no restore to use it.
		*g.labelGuard = true
		return []ast.Stmt{push, assign(ast.NewIdent("_"), frame)}
	}
	return []ast.Stmt{push, &ast.DeferStmt{Call: &ast.CallExpr{Fun: &ast.SelectorExpr{X: frame, Sel: ast.NewIdent("restore")}}}}
}

// guardLabels generates a Go function's body, and, if a with in it
// publishes values, first defers putting back the labels the function
// started with. A with's block restores them on every normal way out
// (see openMock); the guard covers panics. One guard per function, not
// a deferred restore per with: a with in a loop would keep a deferred
// call for every iteration until the function returns.
func (g *gen) guardLabels(body func() []ast.Stmt) []ast.Stmt {
	saved := g.labelGuard
	used := false
	g.labelGuard = &used
	out := body()
	g.labelGuard = saved
	if !used {
		return out
	}
	guard := &ast.DeferStmt{Call: &ast.CallExpr{Fun: ast.NewIdent("_setLabels"), Args: []ast.Expr{&ast.CallExpr{Fun: ast.NewIdent("_labels")}}}}
	return append([]ast.Stmt{guard}, out...)
}

// ambientDecls is the Go source of _ambientDecls.
func (g *gen) ambientDecls() string {
	src := "package main\n\nvar _ambientDecls = []*_ambientDecl{\n"
	// A logged value is logged under its name, or, where two
	// packages log the same name, under its package's and its name.
	// (or its package's path and its name, where the last elements of
	// two packages' paths are the same).
	logNames := map[*check.Ambient]string{}
	count := func(name func(*check.Ambient) string) map[string]int {
		n := map[string]int{}
		for _, a := range g.ambientMarked() {
			if a.Logged {
				n[name(a)]++
			}
		}
		return n
	}
	plain := count(func(a *check.Ambient) string { return a.Name })
	short := count(func(a *check.Ambient) string { return path.Base(a.Pkg.Path) + "." + a.Name })
	for _, a := range g.ambientMarked() {
		switch {
		case plain[a.Name] < 2 || a.Pkg.Root:
			logNames[a] = a.Name
		case short[path.Base(a.Pkg.Path)+"."+a.Name] < 2:
			logNames[a] = path.Base(a.Pkg.Path) + "." + a.Name
		default:
			logNames[a] = a.Pkg.Path + "." + a.Name
		}
	}
	for _, a := range g.ambientMarked() {
		logName := logNames[a]
		kind := map[check.Type]string{check.String: "s", check.Int: "i", check.Float: "f", check.Bool: "b"}[a.Type]
		valid := "nil"
		if a.Header != "" && len(a.Constraints) > 0 {
			x := ast.NewIdent("x")
			var cond ast.Expr
			for _, con := range a.Constraints {
				c := g.constraintCond(con, x, a.Type)
				if c == nil {
					continue
				}
				if cond == nil {
					cond = c
				} else {
					cond = &ast.BinaryExpr{X: paren(cond), Op: token.LAND, Y: paren(c)}
				}
			}
			if cond != nil {
				valid = "func(v any) bool { x := v.(" + g.typeText(a.Type) + "); return " + g.text(cond) + " }"
			}
		}
		src += "\t{key: " + strconv.Quote("bork.ambient."+a.Pkg.Path+"."+a.Name) +
			", name: " + strconv.Quote(logName) +
			", logged: " + strconv.FormatBool(a.Logged) +
			", header: " + strconv.Quote(a.Header) +
			", kind: '" + kind + "'" +
			", valid: " + valid + "},\n"
	}
	return src + "}\n"
}

// ambientRuntime publishes marked ambient values in goroutine labels
// (see labelRuntime), and gives standard-library boundary code the
// stable helpers that read them (docs/std-go.md).
const ambientRuntime = `package main

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"unsafe"
)

// _ambientDecl is a logged or propagated ambient declaration.
type _ambientDecl struct {
	key    string // its label
	name   string // what logs call it
	logged bool
	header string // the header a propagated value is sent under, or ""
	kind   byte   // its type: 's' String, 'i' Int, 'f' Float, 'b' Bool
	valid  func(any) bool // whether a value has its type's facts (nil: no facts)
}

// _ambientFrame is a with's publication of its marked values: the
// labels to put back when the with ends.
type _ambientFrame struct {
	prev     unsafe.Pointer
	restored bool
}

// _ambientPush publishes values on this goroutine, and on the
// goroutines it starts, until restore: kv is the index of a declaration
// in _ambientDecls and a value, for each value.
func _ambientPush(kv ...any) *_ambientFrame {
	f := &_ambientFrame{prev: _labels()}
	labels := make([]string, 0, len(kv))
	for i := 0; i < len(kv); i += 2 {
		labels = append(labels, _ambientDecls[kv[i].(int)].key, "="+_ambientText(kv[i+1]))
	}
	_labelsSet(labels...)
	return f
}

// restore ends the publication (once): the goroutine gets its labels
// from before it.
func (f *_ambientFrame) restore() {
	if !f.restored {
		f.restored = true
		_setLabels(f.prev)
	}
}

// _ambientText is the text a value is published as, as bork shows it.
// (A label's value is "=" and the text, as a label cannot be empty.)
func _ambientText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		if a := math.Abs(v); math.IsInf(v, 0) || math.IsNaN(v) || (a != 0 && (a < 1e-6 || a >= 1e21)) {
			return strconv.FormatFloat(v, 'g', -1, 64)
		}
		s := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case bool:
		return strconv.FormatBool(v)
	}
	panic(fmt.Sprintf("bork: ambient value of Go type %T", v))
}

// read is the value text stands for, or an error if it stands for
// none: a value of the declaration's type, with its facts.
func (d *_ambientDecl) read(text string) (v any, err error) {
	// Only the text bork shows is read: plain decimal numbers (no "+",
	// "_" or hex; a Float's NaN, +Inf and -Inf as shown), and Bool only
	// true or false. The errors never quote the text,
	// which came from outside.
	switch d.kind {
	case 's':
		v = text
	case 'i':
		if !_ambientNumber(text, false) {
			return nil, fmt.Errorf("it is not an Int")
		}
		if v, err = strconv.ParseInt(text, 10, 64); err != nil {
			return nil, fmt.Errorf("it is not an Int")
		}
	case 'f':
		// NaN and the infinities still go through the facts below.
		switch {
		case text == "NaN":
			v = math.NaN()
		case text == "+Inf":
			v = math.Inf(1)
		case text == "-Inf":
			v = math.Inf(-1)
		case !_ambientNumber(text, true):
			return nil, fmt.Errorf("it is not a Float")
		default:
			if v, err = strconv.ParseFloat(text, 64); err != nil {
				return nil, fmt.Errorf("it is not a Float")
			}
		}
	case 'b':
		if text != "true" && text != "false" {
			return nil, fmt.Errorf("it is not a Bool")
		}
		v = text == "true"
	}
	if d.valid != nil {
		ok := false
		func() {
			defer func() {
				if recover() != nil {
					// The panic's text may hold the value.
					err = fmt.Errorf("checking its facts panicked")
				}
			}()
			ok = d.valid(v)
		}()
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("it does not have the facts of its type")
		}
	}
	return v, nil
}

// _ambientNumber reports whether text is a plain decimal number: an
// optional "-", digits, and for a Float an optional fraction and
// exponent.
func _ambientNumber(text string, float bool) bool {
	i := 0
	digits := func() bool {
		start := i
		for i < len(text) && '0' <= text[i] && text[i] <= '9' {
			i++
		}
		return i > start
	}
	if i < len(text) && text[i] == '-' {
		i++
	}
	if !digits() {
		return false
	}
	if float && i < len(text) && text[i] == '.' {
		i++
		if !digits() {
			return false
		}
	}
	if float && i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		if !digits() {
			return false
		}
	}
	return i == len(text)
}

// _ambientLabel is the text published for d on this goroutine.
func _ambientLabel(d *_ambientDecl) (string, bool) {
	v := _label(d.key)
	if v == "" {
		return "", false
	}
	return v[1:], true
}

// _borkAmbient is a logged value: its declaration's name, and its value
// (a string, int64, float64 or bool).
type _borkAmbient struct {
	Name  string
	Value any
}

// _borkLogged lists the logged values bound on this goroutine, in
// declaration order.
func _borkLogged() []_borkAmbient {
	if _labels() == nil {
		return nil
	}
	var out []_borkAmbient
	for _, d := range _ambientDecls {
		if !d.logged {
			continue
		}
		if text, ok := _ambientLabel(d); ok {
			v, err := d.read(text)
			if err != nil {
				v = text
			}
			out = append(out, _borkAmbient{Name: d.name, Value: v})
		}
	}
	return out
}

// _borkHeader is a propagated value as it is sent: the header (or
// message metadata) name, and the value's text.
type _borkHeader struct {
	Name, Value string
}

// _borkPropagated lists the propagated values bound on this goroutine,
// in declaration order.
func _borkPropagated() []_borkHeader {
	if _labels() == nil {
		return nil
	}
	var out []_borkHeader
	for _, d := range _ambientDecls {
		if d.header == "" {
			continue
		}
		if text, ok := _ambientLabel(d); ok {
			out = append(out, _borkHeader{Name: d.header, Value: text})
		}
	}
	return out
}

// _borkBindPropagated binds the propagated values of an incoming
// request or message on this goroutine (and the goroutines it starts),
// until the returned restore: a boundary, so values bound before it are
// not inherited. It first clears them, so what get and the values'
// checks log does not carry them either, then reads the values. get
// gives the value under a header name, if present. A value that is not
// of its declaration's type, or lacks its facts, is not bound, and is
// logged at warn level (without its text).
func _borkBindPropagated(get func(name string) (string, bool)) (restore func()) {
	prev := _labels()
	var clear []string
	for _, d := range _ambientDecls {
		if d.header != "" {
			clear = append(clear, d.key, "")
		}
	}
	if len(clear) == 0 {
		return func() {}
	}
	_labelsSet(clear...)
	ok := false
	defer func() {
		// get panicked: the caller has no restore yet.
		if !ok {
			_setLabels(prev)
		}
	}()
	var bind []string
	for _, d := range _ambientDecls {
		if d.header == "" {
			continue
		}
		text, found := get(d.header)
		if !found {
			continue
		}
		v, err := d.read(text)
		if err != nil {
			slog.Warn("ignored an invalid propagated value", "header", d.header, "ambient", d.name, "error", err.Error())
			continue
		}
		bind = append(bind, d.key, "="+_ambientText(v))
	}
	if len(bind) > 0 {
		_labelsSet(bind...)
	}
	ok = true
	return func() { _setLabels(prev) }
}
`

// ambientStubs are the helpers of a program without logged or
// propagated declarations: there is nothing to read or bind.
const ambientStubs = `package main

type _borkAmbient struct {
	Name  string
	Value any
}

func _borkLogged() []_borkAmbient { return nil }

type _borkHeader struct {
	Name, Value string
}

func _borkPropagated() []_borkHeader { return nil }

func _borkBindPropagated(get func(name string) (string, bool)) (restore func()) { return func() {} }
`
