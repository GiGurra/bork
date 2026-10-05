package check

import (
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// SemanticToken describes a compiler-owned source classification. Positions
// use byte columns; protocol adapters choose their own encoding and legend.
type SemanticToken struct {
	Start, End                             diag.Pos
	Kind                                   string
	Declaration, Readonly, Static, Builtin bool
	GoBinding, Rebinding                   bool
}

// LexicalSemanticTokens is the current-source fallback when checking fails.
// Interpolated strings and raw Go are left to the editor's embedded grammar.
func LexicalSemanticTokens(path, source string) []SemanticToken {
	tokens, comments := syntax.Lex(path, []byte(source), &diag.List{})
	return lexicalSemanticTokens(tokens, comments)
}

func lexicalSemanticTokens(tokens []syntax.Token, comments []syntax.Comment) []SemanticToken {
	reserved := map[string]bool{}
	for _, word := range syntax.Keywords() {
		reserved[word] = true
	}
	var out []SemanticToken
	for _, token := range tokens {
		kind := ""
		switch token.Kind {
		case syntax.TString, syntax.TRune:
			kind = "string"
		case syntax.TInt, syntax.TFloat:
			kind = "number"
		case syntax.KwTrue, syntax.KwFalse:
			// Leave boolean literals to the local highlighting grammar.
		case syntax.Assign, syntax.Plus, syntax.Minus, syntax.Star, syntax.Slash, syntax.Pct,
			syntax.Amp, syntax.Caret, syntax.Shl, syntax.Shr, syntax.Not, syntax.AndAnd, syntax.OrOr, syntax.Eq, syntax.NotEq, syntax.Lt,
			syntax.LtEq, syntax.Gt, syntax.GtEq, syntax.Pipe, syntax.PipeGt, syntax.Arrow, syntax.Quest:
			kind = "operator"
		default:
			if reserved[token.Text] && token.Kind != syntax.TIdent {
				kind = "keyword"
			}
		}
		if kind != "" {
			out = append(out, SemanticToken{Start: token.Pos, End: token.End, Kind: kind})
		}
	}
	for _, comment := range comments {
		out = append(out, SemanticToken{Start: comment.Pos, End: comment.End, Kind: "comment"})
	}
	sortSemanticTokens(out)
	return out
}

type semanticIndex struct {
	file   *syntax.File
	info   *Info
	tokens []syntax.Token
	at     map[diag.Pos]int
	marks  map[diag.Pos]SemanticToken
	lines  []string
}

// SemanticTokens uses resolved checker identities, including names that share
// spelling but denote different declarations. It never resolves names again.
func SemanticTokens(file *syntax.File, info *Info) []SemanticToken {
	tokens, comments := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
	s := &semanticIndex{file: file, info: info, tokens: tokens, at: map[diag.Pos]int{}, marks: map[diag.Pos]SemanticToken{}, lines: strings.Split(file.Source, "\n")}
	for i, token := range tokens {
		s.at[token.Pos] = i
	}
	for _, declaration := range file.Types {
		s.declaration(declaration.Pos, declaration.Name, "type", declaration.GoName != nil)
		s.typeParams(declaration.TypeParams)
		for _, predicate := range declaration.Where {
			s.predicate(predicate)
		}
		for _, field := range declaration.Fields {
			s.name(field.Pos, field.Name, SemanticToken{Kind: "property", Declaration: true, Readonly: true})
		}
		for _, variant := range declaration.Variants {
			for _, predicate := range variant.Where {
				s.predicate(predicate)
			}
			s.name(variant.Pos, variant.Name, SemanticToken{Kind: "enumMember", Declaration: true, Readonly: true})
			for _, field := range variant.Fields {
				s.name(field.Pos, field.Name, SemanticToken{Kind: "property", Declaration: true, Readonly: true})
			}
		}
		if declaration.GoName != nil {
			s.goName(declaration.GoName, "type")
		}
	}
	for _, class := range file.Classes {
		s.declaration(class.Pos, class.Name, "class", false)
		s.typeParams(class.TypeParams)
	}
	for _, derive := range file.Derives {
		s.name(derive.Pos, "derive", SemanticToken{Kind: "keyword"})
		s.name(derive.ClassPos, derive.Class, SemanticToken{Kind: "class", Readonly: true})
	}
	for declaration, fn := range info.FuncOf {
		if declaration.Pos.File != file.Path || declaration.ScriptMain {
			continue
		}
		kind := semanticFunctionKind(fn)
		s.declaration(declaration.Pos, declaration.Name, kind, declaration.IsGo())
		s.typeParams(declaration.TypeParams)
		for _, param := range declaration.Params {
			s.name(param.Pos, param.Name, SemanticToken{Kind: "parameter", Declaration: true, Readonly: true})
		}
		s.effects(declaration.Uses)
		if declaration.GoBind != nil {
			s.goName(declaration.GoBind, "function")
		}
	}
	for _, test := range file.Tests {
		for _, param := range test.Params {
			s.name(param.Pos, param.Name, SemanticToken{Kind: "parameter", Declaration: true, Readonly: true})
		}
	}
	for _, ambient := range file.Ambients {
		s.declaration(ambient.Pos, ambient.Name, "variable", false)
	}
	for binding := range info.bindings {
		s.name(binding.Pos, binding.Name, SemanticToken{Kind: "variable", Declaration: true, Readonly: true, Static: binding.Package, Rebinding: info.rebindings[binding] != nil})
	}
	for ident, definition := range info.defs {
		token := SemanticToken{Kind: "variable", Readonly: true}
		switch definition := definition.(type) {
		case *syntax.Param:
			token.Kind = "parameter"
		case *syntax.Binding:
			token.Static = definition.Package
		}
		s.name(ident.Pos, ident.Name, token)
	}
	for expr, instance := range info.funcRefs {
		s.functionName(expr, instance.Func)
	}
	for call, fn := range info.callFuncs {
		s.functionName(call.Fun, fn)
		for _, argument := range call.Arguments {
			if argument.Name != "" {
				s.name(argument.Pos, argument.Name, SemanticToken{Kind: "parameter", Readonly: true})
			}
		}
	}
	for call := range info.callBuiltins {
		if ident, ok := call.Fun.(*syntax.Ident); ok {
			s.name(ident.Pos, ident.Name, SemanticToken{Kind: "function", Builtin: true})
		}
	}
	for written, typ := range info.writtenTypes {
		kind := "type"
		if _, ok := typ.(*TypeParam); ok {
			kind = "typeParameter"
		}
		_, builtin := basicTypes[written.Name]
		s.name(written.Pos, written.Name, SemanticToken{Kind: kind, Builtin: builtin && kind != "typeParameter", GoBinding: semanticGoType(typ)})
		s.effects(written.Uses)
		if written.Func != nil {
			s.effects(written.Func.Uses)
		}
		for _, predicate := range written.Where {
			s.predicate(predicate)
		}
	}
	for expr := range info.types {
		switch expr := expr.(type) {
		case *syntax.Lambda:
			for _, param := range expr.Params {
				s.name(param.Pos, param.Name, SemanticToken{Kind: "parameter", Declaration: true, Readonly: true})
			}
		case *syntax.For:
			s.name(expr.NamePos, expr.Name, SemanticToken{Kind: "variable", Declaration: true, Readonly: true})
		case *syntax.ScopeExpr:
			s.declaration(expr.Pos, expr.Name, "variable", false)
		case *syntax.Selector:
			if variant := info.selectorVariants[expr]; variant != nil {
				s.name(expr.Pos, expr.Name, SemanticToken{Kind: "enumMember", Readonly: true})
				s.constructor(expr.X, "type", false)
			} else if _, method := info.funcRefs[expr]; !method {
				s.name(expr.Pos, expr.Name, SemanticToken{Kind: "property", Readonly: true})
			}
		case *syntax.Copy:
			for _, update := range expr.Updates {
				s.qualifiedName(update.Pos, strings.Join(update.Path, "."), SemanticToken{Kind: "property", Readonly: true}, "property")
			}
		}
	}
	for expr, variant := range info.contextVariants {
		if variant != nil {
			s.contextName(expr, "enumMember")
		}
	}
	for literal, target := range info.recordTargets {
		kind := "type"
		if _, ok := target.(*Variant); ok {
			kind = "enumMember"
		}
		goBinding := false
		if typ, ok := target.(Type); ok {
			goBinding = semanticGoType(typ)
		}
		s.constructor(literal.Type, kind, goBinding)
		for _, field := range literal.Fields {
			s.name(field.Pos, field.Name, SemanticToken{Kind: "property", Readonly: true})
		}
	}
	for binding, pattern := range info.tuplePats {
		s.pattern(pattern)
		s.patternNames(binding.Pattern, pattern)
	}
	for arm, pattern := range info.armPats {
		s.pattern(pattern)
		s.patternNames(arm.Pattern, pattern)
	}
	for mock := range info.mockHandles {
		s.name(mock.Pos, mock.Name, SemanticToken{Kind: "variable", Declaration: true, Readonly: true, Rebinding: info.rebindings[mock] != nil})
	}
	for source, pattern := range info.patternTests {
		s.name(source.Pos, "is", SemanticToken{Kind: "keyword"})
		s.patternNames(source.Pattern, pattern)
	}
	for _, mock := range info.Mocks {
		for _, param := range mock.Decl.Params {
			s.name(param.Pos, param.Name, SemanticToken{Kind: "parameter", Declaration: true, Readonly: true})
		}
	}
	marks := make([]SemanticToken, 0, len(s.marks))
	for _, token := range s.marks {
		marks = append(marks, token)
	}
	sortSemanticTokens(marks)
	out := make([]SemanticToken, 0, len(tokens)+len(marks))
	index := 0
	for _, token := range lexicalSemanticTokens(tokens, comments) {
		for index < len(marks) && !semanticBefore(token.Start, marks[index].End) {
			index++
		}
		if index == len(marks) || !semanticBefore(marks[index].Start, token.End) {
			out = append(out, token)
		}
	}
	out = append(out, marks...)
	sortSemanticTokens(out)
	return out
}

func sortSemanticTokens(tokens []SemanticToken) {
	slices.SortFunc(tokens, func(a, b SemanticToken) int {
		if a.Start.Line != b.Start.Line {
			return a.Start.Line - b.Start.Line
		}
		return a.Start.Col - b.Start.Col
	})
}

func semanticBefore(a, b diag.Pos) bool {
	return a.File == b.File && (a.Line < b.Line || a.Line == b.Line && a.Col < b.Col)
}

func semanticFunctionKind(fn *Func) string {
	if fn.Decl.IsPred {
		return "predicate"
	}
	if fn.Decl.IsMethod {
		return "method"
	}
	return "function"
}

func (s *semanticIndex) add(token SemanticToken) {
	if token.Start.File != s.file.Path || !semanticBefore(token.Start, token.End) {
		return
	}
	if previous, ok := s.marks[token.Start]; ok {
		if previous.Kind == token.Kind {
			token.Readonly = token.Readonly || previous.Readonly
			token.Static = token.Static || previous.Static
			token.Builtin = token.Builtin || previous.Builtin
			token.GoBinding = token.GoBinding || previous.GoBinding
		}
		if previous.Declaration || previous.Kind == "parameter" || previous.Kind == "variable" && token.Kind == "function" || (previous.Kind == "method" || previous.Kind == "function" || previous.Kind == "predicate") && token.Kind == "property" {
			return
		}
	}
	s.marks[token.Start] = token
}

func (s *semanticIndex) name(pos diag.Pos, name string, token SemanticToken) {
	s.qualifiedName(pos, name, token, "namespace")
}

func (s *semanticIndex) qualifiedName(pos diag.Pos, name string, token SemanticToken, prefixKind string) {
	if pos.File != s.file.Path || name == "" || name == "_" || pos.Line < 1 || pos.Line > len(s.lines) {
		return
	}
	parts := strings.Split(name, ".")
	line := s.lines[pos.Line-1]
	if pos.Col > 0 && pos.Col-1+len(name) <= len(line) && line[pos.Col-1:pos.Col-1+len(name)] == name {
		for i, part := range parts {
			current := token
			current.Start, current.End = pos, pos
			current.End.Col += len(part)
			if i < len(parts)-1 {
				current = SemanticToken{Start: current.Start, End: current.End, Kind: prefixKind, Readonly: prefixKind == "property"}
			}
			s.add(current)
			pos.Col += len(part) + 1
		}
		return
	}
	index, ok := s.at[pos]
	if !ok {
		return
	}
	for i, part := range parts {
		if index >= len(s.tokens) || s.tokens[index].Kind != syntax.TIdent || s.tokens[index].Text != part {
			return
		}
		current := token
		current.Start, current.End = s.tokens[index].Pos, s.tokens[index].End
		if i < len(parts)-1 {
			current = SemanticToken{Start: current.Start, End: current.End, Kind: prefixKind, Readonly: prefixKind == "property"}
		}
		s.add(current)
		index++
		if i < len(parts)-1 {
			if index >= len(s.tokens) || s.tokens[index].Kind != syntax.Dot {
				return
			}
			index++
		}
	}
}

func (s *semanticIndex) declaration(pos diag.Pos, name, kind string, goBinding bool) {
	depth := 0
	index, ok := s.at[pos]
	if !ok {
		return
	}
	for _, token := range s.tokens[index:] {
		switch token.Kind {
		case syntax.LParen, syntax.LBrack:
			depth++
		case syntax.RParen, syntax.RBrack:
			depth--
		}
		if token.Kind == syntax.TIdent && token.Text == name && depth == 0 {
			s.name(token.Pos, name, SemanticToken{Kind: kind, Declaration: true, Readonly: kind == "variable", GoBinding: goBinding})
			return
		}
	}
}

func (s *semanticIndex) typeParams(params []*syntax.TypeParam) {
	for _, param := range params {
		s.name(param.Pos, param.Name, SemanticToken{Kind: "typeParameter", Declaration: true})
	}
}

func (s *semanticIndex) effects(uses *syntax.Uses) {
	if uses == nil {
		return
	}
	for _, effect := range uses.Effects {
		s.name(effect.Pos, effect.Name, SemanticToken{Kind: "effect", Builtin: true})
	}
}

func (s *semanticIndex) predicate(predicate *syntax.PredRef) {
	if resolved := s.info.predicateRefs[predicate.Pos]; resolved != nil {
		if resolved.PredParam != "" {
			s.name(predicate.Pos, predicate.Name, SemanticToken{Kind: "parameter", Readonly: true})
		} else if resolved.Pred != nil {
			s.name(predicate.Pos, predicate.Name, SemanticToken{Kind: "predicate", Builtin: resolved.Pred.Prelude, GoBinding: resolved.Pred.Decl.IsGo()})
		}
	}
	for _, alternative := range predicate.Or {
		s.predicate(alternative)
	}
}

func (s *semanticIndex) functionName(expr syntax.Expr, fn *Func) {
	token := SemanticToken{Kind: semanticFunctionKind(fn), Builtin: fn.Prelude, GoBinding: fn.Decl.IsGo()}
	switch expr := expr.(type) {
	case *syntax.Ident:
		s.name(expr.Pos, expr.Name, token)
	case *syntax.Selector:
		s.name(expr.Pos, expr.Name, token)
	}
}

func (s *semanticIndex) constructor(expr syntax.Expr, kind string, goBinding bool) {
	switch expr := expr.(type) {
	case *syntax.Ident:
		s.name(expr.Pos, expr.Name, SemanticToken{Kind: kind, GoBinding: goBinding})
	case *syntax.TypeHead:
		// The written type already carries its resolved kind and modifiers.
	case *syntax.Selector:
		s.name(expr.Pos, expr.Name, SemanticToken{Kind: kind, Readonly: kind == "enumMember", GoBinding: goBinding})
		s.constructor(expr.X, "type", false)
	case *syntax.ContextName:
		s.contextName(expr, kind)
	}
}

func (s *semanticIndex) contextName(expr *syntax.ContextName, kind string) {
	if expr.Name != "" {
		pos := expr.NamePos
		if pos.File == "" {
			pos = expr.Pos
			pos.Col++
		}
		s.name(pos, expr.Name, SemanticToken{Kind: kind, Readonly: kind == "enumMember"})
	}
}

func (s *semanticIndex) goName(binding *syntax.GoBind, kind string) {
	if index, ok := s.at[binding.Pos]; ok && s.tokens[index].Kind == syntax.TString {
		token := s.tokens[index]
		start, end := token.Pos, token.End
		start.Col++
		end.Col--
		s.add(SemanticToken{Start: start, End: end, Kind: kind, GoBinding: true})
	}
}

func (s *semanticIndex) pattern(pattern *Pat) {
	if pattern == nil {
		return
	}
	if pattern.Var != nil {
		v := pattern.Var
		s.name(v.Pos, v.Name, SemanticToken{Kind: "variable", Declaration: true, Readonly: true})
	}
	s.pattern(pattern.Sub)
	s.pattern(pattern.Rest)
	for _, field := range pattern.Fields {
		s.pattern(field.Pat)
	}
	for _, element := range pattern.Elems {
		s.pattern(element)
	}
}

// Pattern classifications use the checker result, so a name bound in a match
// is never confused with a variant or a record type with the same spelling.
func (s *semanticIndex) patternNames(source syntax.Pattern, checked *Pat) {
	if checked == nil {
		return
	}
	switch source := source.(type) {
	case *syntax.VariantPat:
		if checked.Sub != nil {
			checked = checked.Sub
		}
		kind := ""
		switch checked.Kind {
		case PatVariant:
			kind = "enumMember"
		case PatRecord, PatType:
			kind = "type"
		}
		if kind != "" {
			pos := source.Pos
			if source.Context {
				pos = source.NamePos
			}
			s.name(pos, strings.Join(source.Path, "."), SemanticToken{Kind: kind, Readonly: kind == "enumMember"})
			if checked.Kind == PatVariant && len(source.Path) > 1 {
				s.name(source.Pos, strings.Join(source.Path[:len(source.Path)-1], "."), SemanticToken{Kind: "type"})
			}
		}
		for _, field := range source.Fields {
			s.name(field.Pos, field.Field, SemanticToken{Kind: "property", Readonly: true})
			for _, resolved := range checked.Fields {
				if resolved.Name == field.Field {
					s.patternNames(field.Pattern, resolved.Pat)
				}
			}
		}
	case *syntax.TypePat:
		// Preserve the resolved written-type classification.
	case *syntax.TuplePat:
		for i, element := range source.Elems {
			if i < len(checked.Fields) {
				s.patternNames(element, checked.Fields[i].Pat)
			}
		}
	case *syntax.ListPat:
		for i, element := range source.Elems {
			if i < len(checked.Elems) {
				s.patternNames(element, checked.Elems[i])
			}
		}
	}
}

func semanticGoType(typ Type) bool {
	switch typ := typ.(type) {
	case *Opaque:
		return typ.Decl != nil && typ.Decl.GoName != nil
	case *Resource:
		return typ.Decl != nil && typ.Decl.GoName != nil
	case *Record:
		return typ.Decl != nil && typ.Decl.GoName != nil
	}
	return false
}
