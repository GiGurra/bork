package gen

import (
	"go/scanner"
	"go/token"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

func (g *gen) codecType(name string) check.Type {
	pkg := g.info.PackageNamed("bork/codec")
	if pkg == nil {
		panic("codec type requested without bork/codec")
	}
	typ := pkg.TypeNamed(name)
	if typ == nil {
		panic("missing codec type " + name)
	}
	return typ
}

// Resolve runtime template identifiers by declaration identity, preserving
// comments, strings and the import prefixes chosen for this build.
func (g *gen) codecRuntime(src string) string {
	pkg := g.info.PackageNamed("bork/codec")
	names := map[string]string{}
	if pkg != nil {
		for old, current := range map[string]string{"Json": "Value", "JsonField": "Field", "DecodeError": "DecodeError"} {
			typ := g.codecType(current)
			names[old] = g.typeText(typ)
			if sealed, ok := typ.(*check.Sealed); ok {
				for _, v := range sealed.Variants {
					names[old+"_"+v.Name] = g.text(g.variantType(v))
				}
			}
		}
		src = strings.ReplaceAll(src, "@Decode@", className(pkg.ClassNamed("Decode")).Name)
	} else {
		names["Json"] = "any"
		if index := strings.Index(src, "func _borkDecodeFields"); index >= 0 {
			src = src[:index]
		}
	}
	var scan scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("", -1, len(src))
	scan.Init(file, []byte(src), nil, 0)
	var out strings.Builder
	cursor := 0
	for {
		pos, tok, ident := scan.Scan()
		if tok == token.EOF {
			break
		}
		if replacement, ok := names[ident]; tok == token.IDENT && ok {
			offset := file.Offset(pos)
			out.WriteString(src[cursor:offset])
			out.WriteString(replacement)
			cursor = offset + len(ident)
		}
	}
	out.WriteString(src[cursor:])
	return out.String()
}
