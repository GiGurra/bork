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
	sources := map[string]*lintSource{}
	proven, patternProven := lintProvenChecks(info)
	roots := map[string]*syntax.File{}
	for _, file := range files {
		for _, pkg := range info.Packages {
			if pkg.Root && pkg.Path == file.Package && !file.Prelude {
				roots[file.Path] = file
				sources[file.Path] = newLintSource(file)
			}
		}
	}
	warn := func(pos diag.Pos, code, message string, end diag.Pos, fixes ...diag.Fix) {
		file := roots[pos.File]
		if file == nil || sources[file.Path].ignored[pos.Line][code] || sources[file.Path].ignored[pos.Line]["all"] {
			return
		}
		warnings.Warn(pos, code, message)
		warnings.Suggest(pos, code, end, fixes...)
	}
	for source, pat := range info.patternTests {
		certain, decided := info.patternCertainties[source]
		if pat.Kind == PatNever || (decided && !certain) {
			warn(source.Pos, "lint.pattern-always-false", "pattern test always fails", source.End)
		} else if (decided && certain) || patternProven[source.Pos] || (!pat.HasGuard() && len(missingCases([]*Pat{pat}, pat.Type)) == 0) {
			warn(source.Pos, "lint.pattern-always-true", "pattern test always succeeds", source.End)
		}
	}
	for source, assertion := range info.patternAssertions {
		if assertion.Pattern.Kind == PatNever {
			warn(source.Pos, "lint.pattern-always-false", "test.AssertIs always fails", source.End)
		} else if patternProven[source.Pos] {
			warn(source.Pos, "lint.pattern-always-true", "test.AssertIs always succeeds", source.End)
		}
	}
	used := map[any]bool{}
	for _, declaration := range info.defs {
		used[declaration] = true
	}
	for node := range info.unused {
		switch node := node.(type) {
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
	selfReferences := map[*Func]int{}
	for call, fn := range info.callFuncs {
		if info.exprOwners[call] != fn {
			referenced[fn] = true
		} else {
			selfReferences[fn]++
		}
	}
	for expr, inst := range info.funcRefs {
		if info.exprOwners[expr] != inst.Func {
			referenced[inst.Func] = true
		} else {
			selfReferences[inst.Func]++
		}
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
		for _, token := range sources[file.Path].tokens {
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
			if !opaque[file.Package] && !Exported(fd.Name) && fd.Name != "main" && !referenced[fn] && nameUses[file.Package][fd.Name] <= 1+selfReferences[fn] {
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
				start, end := sources[file.Path].exprRange(binary)
				lo, hi := sources[file.Path].exprRange(keep)
				if start.File == "" || end.File == "" || lo.File == "" || hi.File == "" {
					continue
				}
				replacement := "(" + sources[file.Path].text(lo, hi) + ")"
				warn(start, "lint.simplify", "boolean expression can be simplified", end,
					diag.Fix{Message: "remove the redundant boolean literal", Edits: []diag.TextEdit{{Start: start, End: end, Replacement: replacement}}})
			}
		}
	}
	return warnings
}

type lintSource struct {
	file      *syntax.File
	tokens    []syntax.Token
	positions map[diag.Pos]int
	ends      map[diag.Pos]int
	pairs     map[int]int
	offsets   []int
	ignored   map[int]map[string]bool
}

func newLintSource(file *syntax.File) *lintSource {
	tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	source := &lintSource{file: file, tokens: tokens, positions: map[diag.Pos]int{}, ends: map[diag.Pos]int{}, pairs: map[int]int{}, offsets: []int{0}, ignored: map[int]map[string]bool{}}
	var stack []int
	for i, token := range tokens {
		source.positions[token.Pos] = i
		source.ends[token.End] = i
		switch token.Kind {
		case syntax.LParen, syntax.LBrace, syntax.LBrack:
			stack = append(stack, i)
		case syntax.RParen, syntax.RBrace, syntax.RBrack:
			if len(stack) > 0 {
				j := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				source.pairs[i] = j
				source.pairs[j] = i
			}
		}
	}
	for i, c := range file.Source {
		if c == '\n' {
			source.offsets = append(source.offsets, i+1)
		}
	}
	for _, comment := range file.Comments {
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")))
		if len(fields) < 2 || fields[0] != "lint:ignore" {
			continue
		}
		for _, line := range []int{comment.Pos.Line, comment.Pos.Line + 1} {
			if source.ignored[line] == nil {
				source.ignored[line] = map[string]bool{}
			}
			for _, code := range strings.Split(fields[1], ",") {
				source.ignored[line][code] = true
			}
		}
	}
	return source
}

func (s *lintSource) text(start, end diag.Pos) string {
	lo := s.offsets[start.Line-1] + start.Col - 1
	hi := s.offsets[end.Line-1] + end.Col - 1
	return s.file.Source[lo:hi]
}

// Token pairs restore grouping parentheses omitted from the AST. The index is
// built once per source, and each edit examines only its expression's tokens.
func (s *lintSource) exprRange(expression syntax.Expr) (diag.Pos, diag.Pos) {
	var bounds func(syntax.Expr) (diag.Pos, diag.Pos)
	leaf := func(pos diag.Pos) diag.Pos {
		if i, ok := s.positions[pos]; ok {
			return s.tokens[i].End
		}
		return pos
	}
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
		case *syntax.Selector:
			start, _ := bounds(expr.X)
			return start, leaf(expr.Pos)
		case *syntax.RecordLit:
			start, _ := bounds(expr.Type)
			return start, leaf(expr.End)
		case *syntax.Block:
			return expr.Pos, leaf(expr.End)
		case *syntax.If:
			_, end := bounds(expr.Then)
			if expr.Else != nil {
				_, end = bounds(expr.Else)
			}
			return expr.Pos, end
		case *syntax.Interp:
			if expr.Prefix != nil {
				start, _ := bounds(expr.Prefix)
				return start, leaf(expr.PrefixEnd)
			}
			return expr.Pos, leaf(expr.Pos)
		case *syntax.Ident, *syntax.BoolLit, *syntax.IntLit, *syntax.FloatLit, *syntax.StringLit, *syntax.RuneLit, *syntax.ContextName:
			return expr.Position(), leaf(expr.Position())
		}
		return diag.Pos{}, diag.Pos{}
	}
	start, end := bounds(expression)
	if start.File == "" || end.File == "" {
		return diag.Pos{}, diag.Pos{}
	}
	lo, ok := s.positions[start]
	if !ok {
		return diag.Pos{}, diag.Pos{}
	}
	hi, ok := s.ends[end]
	if !ok {
		return diag.Pos{}, diag.Pos{}
	}
	for i := lo; i <= hi; i++ {
		if j, ok := s.pairs[i]; ok {
			if j < lo {
				lo = j
				i = lo - 1
			}
			if j > hi {
				hi = j
			}
		}
	}
	return s.tokens[lo].Pos, s.tokens[hi].End
}

// Reuse the facts walk without evaluating its pending constant queries. Lint
// never executes additional compile-time code and ordinary checks pay no cost.
func lintProvenChecks(info *Info) (map[diag.Pos]bool, map[diag.Pos]bool) {
	f := &factChecker{info: info, diags: &diag.List{}, paths: map[*Func][]branch{}, active: map[string]bool{}, params: map[*Var]*VarRef{}, predParams: map[*Var]*Func{}, lambdaArgs: map[*Var]lambdaArg{}, lintProven: map[diag.Pos]bool{}, lintPatternProven: map[diag.Pos]bool{}}
	f.validators = validationContexts(info)
	for _, fn := range info.FuncOf {
		if fn.Pkg != nil && fn.Pkg.Root && fn.Body != nil && !fn.Decl.IsPred {
			f.function(fn)
		}
	}
	for _, fn := range info.Tests {
		f.function(fn)
	}
	return f.lintProven, f.lintPatternProven
}
