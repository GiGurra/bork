package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) opaqueDecl(n string, pkg *check.Package, gt types.Type, resource bool) []ast.Decl {
	g.usesOpaque = true
	name := typeName(n, pkg).Name
	w := &bindWriter{g: g}
	goName := w.goType(gt)
	field := "value"
	extra := ""
	if resource {
		g.usesScopes = true
		field = "handle"
		extra = "; owner *_Owner; rebind func(*_Scope)"
	}
	label := "<go " + types.TypeString(gt, func(p *types.Package) string { return p.Path() }) + ">"
	src := fmt.Sprintf(`package main
 type %[1]s struct { %[2]s %[3]s %[4]s }
 func (v %[1]s) String() string { return %[5]s }
 func (v %[1]s) _borkGoValue() %[3]s { return v.%[2]s }
 func (v *%[1]s) _borkSetGo(x any) { v.%[2]s = x.(%[3]s) }
 `, name, field, goName, extra, strconv.Quote(label))
	if resource {
		src += fmt.Sprintf("func (v %s) _ownerOf() *_Owner { return v.owner }\nfunc (v %s) _borkRebind(s *_Scope) { if v.rebind != nil { v.rebind(s) } }\n", name, name)
	}
	f, err := parser.ParseFile(token.NewFileSet(), "", src, 0)
	if err != nil {
		panic(err)
	}
	return f.Decls
}

const opaqueRuntime = `
func _borkGo[G any](v interface { _borkGoValue() G }) G { return v._borkGoValue() }
func _borkOpaque[T any](v any) T {
 var out T
 any(&out).(interface { _borkSetGo(any) })._borkSetGo(v)
 return out
}
`
