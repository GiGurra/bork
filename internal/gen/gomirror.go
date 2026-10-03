package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) mirrorDecl(r *check.Record) []ast.Decl {
	g.usesBind = true
	g.usesMirror = true
	g.goType(g.info.Named["GoError"])
	errType := g.typeText(g.info.Named["GoValueError"])
	w := &bindWriter{g: g, b: &check.GoBinding{GoValueError: g.info.Named["GoValueError"]}}
	n := typeName(r.Name, r.Pkg).Name
	gt := w.goType(r.GoMirror)
	var src strings.Builder
	src.WriteString("package main\n")
	if r.GoTo {
		w.line("var out " + gt)
		for i, f := range r.Fields {
			w.line("out." + strings.Join(r.GoFields[i].Path, ".") + " = " + w.toGo("v."+f.Name, f.Type, r.GoFields[i].Type))
		}
		w.line("return out")
		fmt.Fprintf(&src, "func _toGo_%s(v %s) %s {\n%s}\nfunc (v %s) _borkGoMirror() %s { return _toGo_%s(v) }\n", n, n, gt, w.body.String(), n, gt, n)
	}
	if r.GoFrom {
		w.body.Reset()
		w.tmp = 0
		w.collect = "_errs"
		w.seen = "_seen"
		w.line("var out " + n)
		w.line("var _errs []" + errType)
		for i, f := range r.Fields {
			path := "_path + " + strconv.Quote("."+f.Name)
			converted := w.fromGo("v."+strings.Join(r.GoFields[i].Path, "."), r.GoFields[i].Type, f.Type, path)
			w.line("out." + f.Name + " = " + converted)
			if len(f.Constraints) > 0 {
				saved := w.newTmp()
				w.line(fmt.Sprintf("%s := append([]%s(nil), _errs...)", saved, errType))
				for _, con := range f.Constraints {
					w.factAtPath("out."+f.Name, f.Type, splitPath(con.Path), path, con, saved)
				}
			}

		}
		w.line("return out, _errs")
		fmt.Fprintf(&src, "func _fromGo_%s(v %s, _path string, _seen map[any]bool) (%s, []%s) {\n%s}\n", n, gt, n, errType, w.body.String())
		fmt.Fprintf(&src, "func (out *%s) _borkSetMirror(v any) []%s { var errs []%s; *out, errs = _fromGo_%s(v.(%s), \"result\", map[any]bool{}); return errs }\n", n, errType, errType, n, gt)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src.String(), 0)
	if err != nil {
		panic(fmt.Sprintf("mirror %s: %v\n%s", n, err, src.String()))
	}
	return f.Decls
}

func (g *gen) mirrorRuntime() string {
	r := g.goImport("reflect")
	s := g.goImport("strings")
	return fmt.Sprintf(`
func _borkToGo[G any](v interface { _borkGoMirror() G }) G { return v._borkGoMirror() }
func _borkFromGo[T any](v any) (T, []%s) {
 var out T
 errs := any(&out).(interface { _borkSetMirror(any) []%s })._borkSetMirror(v)
 return out, errs
}
type _bindCycleKey struct { typ %[3]s.Type; ptr uintptr; length int }
func _bindCollectionKey(v any) _bindCycleKey {
 r := %[3]s.ValueOf(v)
 length := 0
 if r.Kind()==%[3]s.Slice { length=r.Len() }
 return _bindCycleKey{r.Type(),uintptr(r.UnsafePointer()),length}
}
func _bindPathValid(errors []%[1]s, path string) bool {
 for _,e:=range errors {
  if e.path==path || %[4]s.HasPrefix(e.path,path+".") || %[4]s.HasPrefix(e.path,path+"[") || %[4]s.HasPrefix(path,e.path+".") || %[4]s.HasPrefix(path,e.path+"[") { return false }
 }
 return true
}
`, g.typeText(g.info.Named["GoValueError"]), g.typeText(g.info.Named["GoValueError"]), r, s)
}

// A binding verifies the facts its signature promises before publishing a
// converted result. GoValueError carries any failed promise back to bork.
func (w *bindWriter) result(x string, t check.Type) string {
	fn := w.fn
	if fn == nil || len(fn.ResultConstraints) == 0 {
		return x
	}
	v := w.newTmp()
	w.line(v + " := " + x)
	for _, mc := range fn.ResultConstraints {
		if mc.Type != t {
			continue
		}
		for _, con := range mc.Constraints {
			stmts := w.g.atPath(ast.NewIdent(v), t, splitPath(con.Path), func(x ast.Expr, t check.Type) []ast.Stmt {
				cond := w.g.constraintCond(w.bindingConstraint(con), x, t)
				if cond == nil {
					return nil
				}
				src := fmt.Sprintf("package main\nfunc _(){ if !(%s) { return _bindValueError(%q,%q) } }", w.g.text(cond), "result", "must be "+con.String())
				f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
				if err != nil {
					panic(err)
				}
				return f.Decls[0].(*ast.FuncDecl).Body.List
			})
			for _, stmt := range stmts {
				w.line(w.g.text(stmt))
			}
		}
	}
	return v
}

func hasMirror(t check.Type, seen map[check.Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch t := t.(type) {
	case *check.Record:
		return t.GoMirror != nil
	case *check.List:
		return hasMirror(t.Elem, seen)
	case *check.Map:
		return hasMirror(t.Value, seen)
	case *check.Sealed:
		for _, v := range t.Variants {
			for _, f := range v.Fields {
				if hasMirror(f.Type, seen) {
					return true
				}
			}
		}
	case *check.Union:
		for _, m := range t.Members {
			if hasMirror(m, seen) {
				return true
			}
		}
	}
	return false
}

func (w *bindWriter) bindingConstraint(con *check.Constraint) *check.Constraint {
	out := *con
	out.Args = append([]check.CArg(nil), con.Args...)
	for i, a := range out.Args {
		for j, p := range w.fn.Decl.Params {
			if a.Param == p.Name {
				out.Args[i].Param = fmt.Sprintf("_a%d", j)
			}
		}
	}
	out.Or = nil
	for _, alt := range con.Or {
		out.Or = append(out.Or, w.bindingConstraint(alt))
	}
	return &out
}

func (w *bindWriter) beginCollection(x, path string) string {
	if w.seen == "" || w.collect == "" && !w.b.Fallible {
		return ""
	}
	key := w.newTmp()
	w.line(key + " := _bindCollectionKey(" + x + ")")
	w.line("if len(" + x + ")>0 && " + w.seen + "[" + key + "] {")
	if w.collect != "" {
		w.line(fmt.Sprintf("%s=append(%s,_bindValueError(%s,%q))", w.collect, w.collect, path, "cyclic Go value"))
	} else {
		w.line(fmt.Sprintf("return _bindValueError(%s,%q)", path, "cyclic Go value"))
	}
	w.line("} else {")
	w.line(w.seen + "[" + key + "]=true")
	return key
}
func (w *bindWriter) endCollection(key string) {
	if key == "" {
		return
	}
	w.line("delete(" + w.seen + ", " + key + ")")
	w.line("}")
}

func (w *bindWriter) factAtPath(x string, t check.Type, steps []string, path string, con *check.Constraint, conversionErrors string) {
	if len(steps) == 0 {
		cond := w.g.constraintCond(con, ast.NewIdent(x), t)
		if cond == nil {
			return
		}
		w.line(fmt.Sprintf("if _bindPathValid(%s,%s) && !(%s) { _errs=append(_errs,_bindValueError(%s,%q)) }", conversionErrors, path, w.g.text(cond), path, "must be "+con.String()))
		return
	}
	step, rest := steps[0], steps[1:]
	switch t := t.(type) {
	case *check.List:
		i, e := w.newTmp(), w.newTmp()
		w.line(fmt.Sprintf("for %s,%s:=range %s {", i, e, x))
		w.factAtPath(e, t.Elem, rest, fmt.Sprintf("_bindIndex(%s,%s)", path, i), con, conversionErrors)
		w.line("}")
	case *check.Record:
		if f := t.Field(step); f != nil {
			w.factAtPath(x+"."+step, f.Type, rest, path+" + "+strconv.Quote("."+step), con, conversionErrors)
		}
	case *check.Sealed:
		for _, v := range t.Variants {
			if f := v.Field(step); f != nil {
				e, ok := w.newTmp(), w.newTmp()
				w.line(fmt.Sprintf("if %s,%s:= %s.(%s);%s {", e, ok, x, w.g.text(w.g.variantType(v)), ok))
				w.factAtPath(e+"."+step, f.Type, rest, path+" + "+strconv.Quote("."+step), con, conversionErrors)
				w.line("}")
			}
		}
	}
}
