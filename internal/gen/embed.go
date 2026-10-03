package gen

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
)

func embedVar(file check.EmbeddedFile) string {
	return "_borkEmbedded_" + strings.TrimSuffix(filepath.Base(file.StagePath), ".bin")
}

func (g *gen) embedDecls() string {
	var out strings.Builder
	for _, request := range g.info.Embeds {
		for _, file := range request.Files {
			if out.Len() == 0 {
				out.WriteString("\n// Embedded assets: stage the following files beside this Go source.\n")
				if !g.imports["embed"] && g.bindImports["embed"] == "" {
					g.bindImports["embed"] = "_"
				}
			}
			source := request.Path
			if file.Name != "" {
				source += "/" + file.Name
			}
			fmt.Fprintf(&out, "// %s <- %s (source package %s)\n//go:embed %s\nvar %s string\n\n", file.StagePath, strconv.Quote(source), strconv.Quote(filepath.Dir(request.Pos.File)), file.StagePath, embedVar(file))
		}
	}
	return out.String()
}

func (g *gen) embedCall(call *check.Call) ast.Expr {
	request := call.Embedded
	data := func(file check.EmbeddedFile) ast.Expr {
		g.usesBytes = true
		return &ast.CallExpr{Fun: ast.NewIdent("_borkBytesFrom"), Args: []ast.Expr{
			&ast.CallExpr{Fun: &ast.ArrayType{Elt: ast.NewIdent("byte")}, Args: []ast.Expr{ast.NewIdent(embedVar(file))}},
		}}
	}
	if request.Kind == "ReadString" {
		return ast.NewIdent(embedVar(request.Files[0]))
	}
	if request.Kind == "ReadBytes" {
		return data(request.Files[0])
	}
	g.usesMap = true
	keys := &ast.CompositeLit{Type: &ast.ArrayType{Elt: ast.NewIdent("string")}}
	values := &ast.CompositeLit{Type: &ast.ArrayType{Elt: g.goType(check.Bytes)}}
	for _, file := range request.Files {
		keys.Elts = append(keys.Elts, &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(file.Name)})
		values.Elts = append(values.Elts, data(file))
	}
	files := &ast.CallExpr{Fun: &ast.IndexListExpr{X: ast.NewIdent("_borkMapOf"), Indices: []ast.Expr{ast.NewIdent("string"), g.goType(check.Bytes)}}, Args: []ast.Expr{keys, values}}
	return &ast.CompositeLit{Type: g.goType(call.Type()), Elts: []ast.Expr{
		&ast.KeyValueExpr{Key: ast.NewIdent("files"), Value: files},
	}}
}
