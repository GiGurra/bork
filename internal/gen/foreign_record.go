package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

const foreignSchemaHelpers = `package main

type _borkGoTag struct { Name, Value string }
type _borkGoStructField struct {
 Name string
 Type string
 Constraints []string
 Doc string
 HasDefault bool
 Default func() any
 Optional bool
 GoName string
 Tags []_borkGoTag
}
func _borkGoTags(tag string) []_borkGoTag {
 var out []_borkGoTag
 for tag!="" {
  for len(tag)>0 && tag[0]==' ' { tag=tag[1:] }
  i:=0
  for i<len(tag) && tag[i]>' ' && tag[i]!=':' && tag[i]!='"' && tag[i]!=0 { i++ }
  if i==0 || i+1>=len(tag) || tag[i]!=':' || tag[i+1]!='"' { break }
  key:=tag[:i]; tag=tag[i+1:]
  j:=1
  for j<len(tag) { if tag[j]=='\\' { j++ } else if tag[j]=='"' { break }; j++ }
  if j>=len(tag) { break }
  value,err:=@strconv@.Unquote(tag[:j+1]); if err!=nil { break }
  out=append(out,_borkGoTag{Name:key,Value:value}); tag=tag[j+1:]
 }
 return out
}
`

// foreignDictionary emits the conversion bridge of any class whose derive
// template declares a shape.ForeignRecord layout.
func (g *gen) foreignDictionary(ci *check.ClassInstance) *ast.CompositeLit {
	r := ci.Type.(*check.Record)
	g.usesForeign = true
	bt := g.typeText(r)
	errType := g.typeText(g.info.Named["GoValueError"])
	w := &bindWriter{g: g, b: &check.GoBinding{GoValueError: g.info.Named["GoValueError"]}}
	gt := w.goType(r.GoMirror)
	lit := &ast.CompositeLit{Type: g.classType(ci.Class, r)}
	add := func(key, src string) {
		x, err := parser.ParseExpr(src)
		if err != nil {
			panic(fmt.Sprintf("%s bridge %s %s: %v\n%s", ci.Class.Name, r.Name, key, err, src))
		}
		lit.Elts = append(lit.Elts, &ast.KeyValueExpr{Key: ast.NewIdent(key), Value: x})
	}
	w.line("out := new(" + gt + ")")
	for i, f := range r.Fields {
		if f.Computed {
			continue
		}
		if f.Default != nil {
			w.line("out." + strings.Join(r.GoFields[i].Path, ".") + " = " + w.toGo(g.fieldDefault(f), f.Type, r.GoFields[i].Type))
		}
	}
	w.line("return out")
	add("New", "func() any {\n"+w.body.String()+"}")
	from := g.mirrorHelperName("_fromGo_", r)
	var facts strings.Builder
	for _, con := range ci.Constraints {
		if cond := g.constraintCond(con, ast.NewIdent("out"), r); cond != nil {
			setup, path, message := g.constraintFailure(con, ast.NewIdent("out"), r, stringLit("result"))
			fmt.Fprintf(&facts, "if len(errs)==0 && !(%s) {\n", g.text(cond))
			for _, stmt := range setup {
				facts.WriteString(g.text(stmt) + "\n")
			}
			fmt.Fprintf(&facts, "errs=append(errs,%s{path:%s,message:%s}) }\n", errType, g.text(path), g.text(message))
		}
	}
	add("FromGo", fmt.Sprintf(`func(value any) (%s, []%s) {
 convert := func(value any) (%s, []%s) {
 switch value:=value.(type) {
 case %s: return %s(value,"result",map[any]bool{})
 case *%s:
  if value != nil { return %s(*value,"result",map[any]bool{}) }
  var zero %s; return zero, []%s{{path:"result",message:"is nil"}}
 default:
  var zero %s; return zero, []%s{{path:"result",message:"has the wrong Go type"}}
 }
 }
 out, errs := convert(value)
 %s
 if len(errs)>0 { var zero %s; return zero,errs }
 return out,errs
}`, bt, errType, bt, errType, gt, from, gt, from, bt, errType, bt, errType, facts.String(), bt))
	add("ToGo", "func(value "+bt+") any { return "+g.mirrorHelperName("_toGo_", r)+"(value) }")
	var fields strings.Builder
	fields.WriteString("func() []_borkGoStructField { return []_borkGoStructField{\n")
	for _, i := range r.Foreign.Slots {
		f := r.Fields[i]
		var cons []string
		for _, con := range f.Constraints {
			cons = append(cons, strconv.Quote(con.String()))
		}
		def := "nil"
		if f.Default != nil {
			def = "func() any { return " + g.fieldDefault(f) + " }"
		}
		tagExpr := "_borkGoTags(" + strconv.Quote(r.GoFields[i].Tag) + ")"
		fmt.Fprintf(&fields, "{Name:%q,Type:%q,Doc:%q,Constraints:[]string{%s},Optional:%t,HasDefault:%t,Default:%s,GoName:%q,Tags:%s},\n", f.Name, f.Type.String(), f.Doc, strings.Join(cons, ","), check.IsOption(f.Type), f.Default != nil, def, strings.Join(r.GoFields[i].Path, "."), tagExpr)
	}
	fields.WriteString("} }")
	add("Fields", fields.String())
	return lit
}
