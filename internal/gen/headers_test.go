package gen

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"testing"
)

// Literals in statement headers print as Go that parses, also when the
// whole header is parenthesized and the literal's type is generic
// (go/printer drops such parentheses), and literals already in brackets
// are left alone.
func TestParenHeaders(t *testing.T) {
	lit := func(typ ast.Expr) *ast.CompositeLit { return &ast.CompositeLit{Type: typ} }
	generic := &ast.IndexExpr{X: ast.NewIdent("G"), Index: ast.NewIdent("int")}
	body := &ast.BlockStmt{}
	fn := &ast.FuncDecl{Name: ast.NewIdent("f"), Type: &ast.FuncType{Params: &ast.FieldList{}}, Body: &ast.BlockStmt{List: []ast.Stmt{
		&ast.IfStmt{Cond: &ast.ParenExpr{X: &ast.BinaryExpr{X: ast.NewIdent("x"), Op: token.EQL, Y: lit(generic)}}, Body: body},
		&ast.IfStmt{Cond: &ast.CallExpr{Fun: &ast.SelectorExpr{X: lit(ast.NewIdent("P")), Sel: ast.NewIdent("ok")}, Args: []ast.Expr{lit(ast.NewIdent("Q"))}}, Body: body},
		&ast.RangeStmt{Key: ast.NewIdent("_"), Tok: token.ASSIGN, X: &ast.SelectorExpr{X: lit(generic), Sel: ast.NewIdent("run")}, Body: body},
	}}}
	parenHeaders([]ast.Decl{fn})
	var buf bytes.Buffer
	buf.WriteString("package p\n")
	if err := printer.Fprint(&buf, token.NewFileSet(), fn); err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "x.go", buf.Bytes(), 0); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("ok((Q{}))")) {
		t.Errorf("a literal in call arguments was wrapped:\n%s", buf.String())
	}
}
