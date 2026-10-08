package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"github.com/GiGurra/bork/internal/check"
)

// Handle the result outside the returning function so its deferred scope
// cleanup completes before reporting a failure or terminating the process.
func (g *gen) resultMain(result check.Type) *ast.FuncDecl {
	g.imports["fmt"], g.imports["os"] = true, true
	g.usesShow = true
	cases := "case " + g.text(g.goType(check.Ok)) + ":\n"
	if g.usesScopes {
		cases += "_borkSignalExit()\n"
	}
	for _, member := range result.(*check.Union).Members[1:] {
		record, ok := member.(*check.Record)
		if !ok || record.Pkg == nil || record.Pkg.Path != "bork/process" || record.Name != "ExitCode" {
			continue
		}
		cases += fmt.Sprintf("case %s: if failure.message != \"\" { fmt.Fprintln(os.Stderr, \"error:\", failure.message) }; os.Exit(int(failure.code))\n", g.text(g.goType(member)))
	}
	cases += "default:\n"
	if g.usesScopes {
		cases += "_borkSignalExit()\n"
	}
	cases += "fmt.Fprintln(os.Stderr, \"error:\", _str(failure)); os.Exit(1)\n"
	source := "package main\nfunc main() { switch failure := _borkMain().(type) {" + cases + "} }"
	file, err := parser.ParseFile(token.NewFileSet(), "", source, 0)
	if err != nil {
		panic(err)
	}
	return file.Decls[0].(*ast.FuncDecl)
}
