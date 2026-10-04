package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// LintWarnings reads compiler identities and effect/proof results. It never
// changes type checking, inferred contracts, or the generated program.
func LintWarnings(files []*syntax.File, info *Info) *diag.List {
	warnings := &diag.List{}
	proven := lintProvenChecks(info)
	roots := map[string]*syntax.File{}
	for _, file := range files {
		for _, pkg := range info.Packages {
			if pkg.Root && pkg.Path == file.Package && !file.Prelude {
				roots[file.Path] = file
			}
		}
	}
	warn := func(pos diag.Pos, code, message string, end diag.Pos, fixes ...diag.Fix) {
		file := roots[pos.File]
		if file == nil || lintIgnored(file, pos.Line, code) {
			return
		}
		warnings.Warn(pos, code, message)
		warnings.Suggest(pos, code, end, fixes...)
	}
	used := map[any]bool{}
	for _, declaration := range info.defs {
		used[declaration] = true
	}
	for node := range info.unused {
		switch node := node.(type) {
		case *syntax.Binding:
			if node.Name == "_" {
				continue
			}
			end := node.Pos
			end.Col += len(node.Name)
			var fixes []diag.Fix
			if !node.Lazy && node.AsyncScope == nil {
				fixes = append(fixes, diag.Fix{Message: "discard the unused value", Edits: []diag.TextEdit{{Start: node.Pos, End: end, Replacement: "_"}}})
			}
			warn(node.Pos, "lint.unused-binding", "binding "+node.Name+" is never read", end, fixes...)
		case *syntax.VariantPat:
			if len(node.Path) != 1 {
				continue
			}
			end := node.Pos
			end.Col += len(node.Path[0])
			warn(node.Pos, "lint.unused-binding", "pattern binding "+node.Path[0]+" is never read", end)
		case *syntax.FieldPat:
			end := node.Pos
			end.Col += len(node.Field)
			warn(node.Pos, "lint.unused-binding", "pattern binding "+node.Field+" is never read", end)
		case *syntax.ListPat:
			if node.Rest == "" {
				continue
			}
			end := node.RestPos
			end.Col += len(node.Rest)
			warn(node.RestPos, "lint.unused-binding", "pattern binding "+node.Rest+" is never read", end)

		case *syntax.Param:
			if node.Name == "_" {
				continue
			}
			end := node.Pos
			end.Col += len(node.Name)
			// Parameter names are call labels; renaming one could break callers.
			warn(node.Pos, "lint.unused-parameter", "parameter "+node.Name+" is never read", end)
		}
	}
	parameters := map[*syntax.Param]bool{}
	for fd, fn := range info.FuncOf {
		if fn.Body == nil || fd.Constructor != nil {
			continue
		}
		for _, parameter := range fd.Params {
			parameters[parameter] = true
		}
	}
	for _, fn := range info.Tests {
		for _, parameter := range fn.Decl.Params {
			parameters[parameter] = true
		}
	}
	for expr := range info.types {
		if lambda, ok := expr.(*syntax.Lambda); ok {
			for _, parameter := range lambda.Params {
				parameters[parameter] = true
			}
		}
	}
	for parameter := range parameters {
		if used[parameter] || info.unused[parameter] || parameter.Name == "_" {
			continue
		}
		end := parameter.Pos
		end.Col += len(parameter.Name)
		warn(parameter.Pos, "lint.unused-parameter", "parameter "+parameter.Name+" is never read", end)
	}
	referenced := map[*Func]bool{}
	for call, fn := range info.callFuncs {
		if info.exprOwners[call] != fn {
			referenced[fn] = true
		}
	}
	for _, inst := range info.funcRefs {
		referenced[inst.Func] = true
	}
	// Raw Go may refer to private Go declarations by name. Keep those packages
	// out of declaration linting rather than guessing about embedded Go code.
	opaque := map[string]bool{}
	for _, file := range roots {
		for _, fn := range file.Funcs {
			if fn.IsGo() {
				opaque[file.Package] = true
			}
		}
	}
	nameUses := map[string]map[string]int{}
	for _, file := range roots {
		if nameUses[file.Package] == nil {
			nameUses[file.Package] = map[string]int{}
		}
		tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
		for _, token := range tokens {
			if token.Kind == syntax.TIdent {
				nameUses[file.Package][token.Text]++
			}
		}
	}
	for _, file := range roots {
		var pkg *Package
		for _, p := range info.Packages {
			if p.Root && p.Path == file.Package {
				pkg = p
				break
			}
		}
		if pkg == nil {
			continue
		}
		for _, fd := range file.Funcs {
			fn := info.FuncOf[fd]
			if fn == nil || fn.Prelude || fd.ScriptMain || fd.IsMethod || fn.Of != nil || fn.Class != nil {
				continue
			}
			if !opaque[file.Package] && !Exported(fd.Name) && fd.Name != "main" && !referenced[fn] && nameUses[file.Package][fd.Name] <= 1 {
				warn(fd.Pos, "lint.unused-declaration", "private function "+fd.Name+" is never used", fd.ParamsEnd)
			}
			if fd.Uses != nil && fn.Body != nil {
				uses := &effectUses{from: fn.Pkg}
				uses.block(fn.Body)
				if needless := fn.Effects &^ uses.used; needless != 0 && uses.open == nil {
					// Replacing only the uses clause preserves defaults, needs and result
					// contracts. Declared effects of callees remain part of the contract.
					replacement := "uses " + (fn.Effects &^ needless).String()
					message := "function never performs " + needless.String()
					if Exported(fd.Name) {
						message += "; suppress this warning if the declaration reserves effects for API compatibility"
					}
					warn(fd.Uses.Pos, "lint.needless-effects", message, fd.Uses.End,
						diag.Fix{Message: "remove needless effects", Edits: []diag.TextEdit{{Start: fd.Uses.Pos, End: fd.Uses.End, Replacement: replacement}}})
				}
			}
		}
		if !opaque[file.Package] {
			for _, binding := range file.Bindings {
				if !Exported(binding.Name) && binding.Name != "_" && !used[binding] {
					end := binding.Pos
					end.Col += len(binding.Name)
					warn(binding.Pos, "lint.unused-declaration", "private package value "+binding.Name+" is never used", end)
				}
			}
			// Type names cannot be shadowed in bork. Count compiler tokens for names
			// in patterns and constructors too, rather than assuming every use has a
			// written TypeExpr (aliases can share their underlying type identity).
			for _, decl := range file.Types {
				if Exported(decl.Name) {
					continue
				}
				count := nameUses[file.Package][decl.Name]
				if count <= 1 {
					warn(decl.Pos, "lint.unused-declaration", "private type "+decl.Name+" is never used", decl.Pos)
				}
			}
		}
	}
	for expr := range info.types {
		file := roots[expr.Position().File]
		if file == nil {
			continue
		}
		if call, ok := expr.(*syntax.Call); ok && proven[call.Position()] {
			warn(call.Start, "lint.redundant-check", "predicate check is already proved by the compiler", call.End)
		}
		if binary, ok := expr.(*syntax.Binary); ok {
			var keep syntax.Expr
			left, lok := binary.X.(*syntax.BoolLit)
			right, rok := binary.Y.(*syntax.BoolLit)
			switch binary.Op {
			case syntax.AndAnd:
				if lok && left.Value {
					keep = binary.Y
				} else if rok && right.Value {
					keep = binary.X
				}
			case syntax.OrOr:
				if lok && !left.Value {
					keep = binary.Y
				} else if rok && !right.Value {
					keep = binary.X
				}
			}
			if keep != nil {
				start, end := lintExprRange(file, binary)
				lo, hi := lintExprRange(file, keep)
				if start.File == "" || end.File == "" || lo.File == "" || hi.File == "" {
					continue
				}
				replacement := "(" + sourceText(file, lo, hi) + ")"
				warn(start, "lint.simplify", "boolean expression can be simplified", end,
					diag.Fix{Message: "remove the redundant boolean literal", Edits: []diag.TextEdit{{Start: start, End: end, Replacement: replacement}}})
			}
		}
	}
	return warnings
}

func lintIgnored(file *syntax.File, line int, code string) bool {
	for _, comment := range file.Comments {
		if comment.Pos.Line != line && comment.Pos.Line != line-1 {
			continue
		}
		text := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
		fields := strings.Fields(text)
		if len(fields) >= 2 && fields[0] == "lint:ignore" {
			for _, rule := range strings.Split(fields[1], ",") {
				if rule == code || rule == "all" {
					return true
				}
			}
		}
	}
	return false
}

// Lexer token pairs restore grouping parentheses omitted from the AST. This
// keeps edits balanced without reimplementing expression parsing.
func lintExprRange(file *syntax.File, expression syntax.Expr) (diag.Pos, diag.Pos) {
	tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	var bounds func(syntax.Expr) (diag.Pos, diag.Pos)
	bounds = func(expr syntax.Expr) (diag.Pos, diag.Pos) {
		switch expr := expr.(type) {
		case *syntax.Binary:
			start, _ := bounds(expr.X)
			_, end := bounds(expr.Y)
			return start, end
		case *syntax.Unary:
			_, end := bounds(expr.X)
			return expr.Pos, end
		case *syntax.Call:
			return expr.Start, expr.End
		case *syntax.Ident, *syntax.BoolLit:
			for _, tok := range tokens {
				if tok.Pos == expr.Position() {
					return tok.Pos, tok.End
				}
			}
		}
		return diag.Pos{}, diag.Pos{}
	}
	start, end := bounds(expression)
	if start.File == "" || end.File == "" {
		return diag.Pos{}, diag.Pos{}
	}
	pairs := map[int]int{}
	var stack []int
	for i, tok := range tokens {
		if tok.Kind == syntax.LParen {
			stack = append(stack, i)
		}
		if tok.Kind == syntax.RParen && len(stack) > 0 {
			j := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			pairs[i] = j
			pairs[j] = i
		}
	}
	before := func(a, b diag.Pos) bool { return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col }
	for changed := true; changed; {
		changed = false
		for i, j := range pairs {
			tok, other := tokens[i], tokens[j]
			if !before(tok.Pos, start) && before(tok.Pos, end) {
				if before(other.Pos, start) {
					start = other.Pos
					changed = true
				}
				if before(end, other.End) {
					end = other.End
					changed = true
				}
			}
		}
	}
	return start, end
}

// Reuse the facts walk without evaluating its pending constant queries. Lint
// never executes additional compile-time code and ordinary checks pay no cost.
func lintProvenChecks(info *Info) map[diag.Pos]bool {
	f := &factChecker{info: info, diags: &diag.List{}, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}, lintProven: map[diag.Pos]bool{}}
	f.validators = validationContexts(info)
	for _, fn := range info.FuncOf {
		if fn.Pkg != nil && fn.Pkg.Root && fn.Body != nil && !fn.Decl.IsPred {
			f.function(fn)
		}
	}
	for _, fn := range info.Tests {
		f.function(fn)
	}
	return f.lintProven
}
