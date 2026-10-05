package gen

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// DebugPackage preserves runtime statement locations through Go's DWARF line
// directives. Runtime helpers point back to the retained generated source.
func DebugPackage(files []*syntax.File, info *check.Info, generatedPath string) ([]byte, error) {
	source, _, err := DebugPackageMap(files, info, generatedPath)
	return source, err
}

// DebugPackageMap emits source and the compiler's debugger presentation map.
func DebugPackageMap(files []*syntax.File, info *check.Info, generatedPath string) ([]byte, *DebugMap, error) {
	g := newGen(info)
	g.debugSource = generatedPath
	g.debugFiles = map[string]bool{}
	var roots []*check.Func
	for _, file := range files {
		if !file.Prelude && !strings.HasPrefix(file.Package, "bork/") {
			g.debugFiles[file.Path] = true
		}
		for _, fd := range file.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && !fn.Prelude {
				roots = append(roots, fn)
			}
		}
	}
	source, err := generate(g, files, roots, nil)
	if err != nil {
		return nil, nil, err
	}
	return source, g.debugMap(source), nil
}

func (g *gen) debugLine(pos diag.Pos) []ast.Stmt {
	if g.debugSource == "" || !g.debugFiles[pos.File] || pos.Line < 1 {
		return nil
	}
	path, err := filepath.Abs(pos.File)
	if err != nil {
		return nil
	}
	return []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("_borkDebugLineMarker"), Args: []ast.Expr{strLit(path), &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(pos.Line)}}}}}
}

func (g *gen) mapDebugSource(source []byte) ([]byte, error) {
	if strings.ContainsAny(g.debugSource, "\r\n") {
		return nil, fmt.Errorf("debug source path cannot contain a newline")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, g.debugSource, source, 0)
	if err != nil {
		return nil, err
	}
	type edit struct {
		lo, hi int
		text   string
	}
	var edits []edit
	for _, decl := range file.Decls {
		offset := fset.PositionFor(decl.Pos(), false).Offset
		directive := "//bork-debug-generated\n"
		// Map the function prologue too, so stopOnEntry and function frames
		// open the bork declaration rather than the generated Go signature.
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && len(fn.Body.List) > 0 {
			if statement, ok := fn.Body.List[0].(*ast.ExprStmt); ok {
				if call, ok := statement.X.(*ast.CallExpr); ok && len(call.Args) == 2 {
					if name, ok := call.Fun.(*ast.Ident); ok && name.Name == "_borkDebugLineMarker" {
						path := call.Args[0].(*ast.BasicLit)
						line := call.Args[1].(*ast.BasicLit)
						decoded, err := strconv.Unquote(path.Value)
						if err != nil {
							return nil, err
						}
						directive += "//line " + decoded + ":" + line.Value + "\n"
					}
				}
			}
		}
		edits = append(edits, edit{offset, offset, directive})
	}
	var mappingErr error
	ast.Inspect(file, func(node ast.Node) bool {
		stmt, ok := node.(*ast.ExprStmt)
		if !ok {
			return true
		}
		call, ok := stmt.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok || name.Name != "_borkDebugLineMarker" || len(call.Args) != 2 {
			return true
		}
		pathLiteral, pok := call.Args[0].(*ast.BasicLit)
		lineLiteral, lok := call.Args[1].(*ast.BasicLit)
		if !pok || !lok {
			return true
		}
		path, err := strconv.Unquote(pathLiteral.Value)
		if err != nil {
			return true
		}
		if strings.ContainsAny(path, "\r\n") {
			mappingErr = fmt.Errorf("bork debug cannot map a path containing a newline")
			return false
		}
		lo, hi := fset.PositionFor(stmt.Pos(), false).Offset, fset.PositionFor(stmt.End(), false).Offset
		start := strings.LastIndexByte(string(source[:lo]), '\n') + 1
		if strings.TrimSpace(string(source[start:lo])) != "" {
			mappingErr = fmt.Errorf("debug marker must be printed on its own line")
			return false
		}
		edits = append(edits, edit{start, hi, "//line " + path + ":" + lineLiteral.Value})
		return false
	})
	if mappingErr != nil {
		return nil, mappingErr
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].lo > edits[j].lo })
	text := string(source)
	for _, edit := range edits {
		text = text[:edit.lo] + edit.text + text[edit.hi:]
	}
	// Never interpret marker-like text inside raw strings or block comments.
	protected := map[int]bool{}
	scanSet := token.NewFileSet()
	scanFile := scanSet.AddFile("generated", -1, len(text))
	var lexer scanner.Scanner
	lexer.Init(scanFile, []byte(text), nil, scanner.ScanComments)
	for {
		pos, kind, literal := lexer.Scan()
		if kind == token.EOF {
			break
		}
		if kind != token.STRING && kind != token.COMMENT {
			continue
		}
		first := scanFile.PositionFor(pos, false).Line - 1
		for line := first + 1; line <= first+strings.Count(literal, "\n"); line++ {
			protected[line] = true
		}
	}
	var lines []string
	active := ""
	for index, line := range strings.Split(text, "\n") {
		if protected[index] {
			lines = append(lines, line)
			continue
		}
		if line == "//bork-debug-generated" {
			active = ""
			lines = append(lines, fmt.Sprintf("//line %s:%d", g.debugSource, len(lines)+2))
		} else if strings.HasPrefix(line, "//line ") {
			active = line
			lines = append(lines, line)
		} else {
			// A lowered bork statement may occupy many Go lines. Each must
			// retain the same location until the next compiler-owned marker.
			if active != "" && strings.TrimSpace(line) != "" {
				lines = append(lines, active)
			}
			lines = append(lines, line)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}
