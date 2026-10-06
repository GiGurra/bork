// Package describe adapts source positions to typed compiler queries. The
// lookup is isolated from the CLI and its output format.
package describe

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type Result struct {
	Rebinds        *diag.Pos                        `json:"rebinds,omitempty"`
	Async          *check.AsyncDescription          `json:"async,omitempty"`
	Lazy           *check.LazyDescription           `json:"lazy,omitempty"`
	ProviderBundle *check.ProviderBundleDescription `json:"provider_bundle,omitempty"`
	Assembly       *check.Assembly                  `json:"assembly,omitempty"`
	Documentation  string                           `json:"documentation,omitempty"`
	SchemaVersion  int                              `json:"schema_version"`
	Position       diag.Pos                         `json:"position"`
	Type           string                           `json:"type"`
	Expression     string                           `json:"expression,omitempty"`
	Definition     *diag.Pos                        `json:"definition,omitempty"`
	Methods        []check.MethodDescription        `json:"methods"`
	Facts          []check.KnownFact                `json:"facts"`
	// BelongsTo lists the scopes the value belongs to: it is usable
	// while all of them are open.
	BelongsTo []string `json:"belongs_to,omitempty"`
	// Ownership says whether a resource variable was acquired here and
	// can be moved, is borrowed, is kept by a task or a channel, or was
	// moved.
	Ownership string                     `json:"ownership,omitempty"`
	Proof     *check.Proof               `json:"proof,omitempty"`
	Callable  *check.CallableDescription `json:"callable,omitempty"`
	// TailCall says, at a call of a function by itself, whether it is
	// compiled as a jump.
	TailCall *check.TailCall `json:"tail_call,omitempty"`
}

// Selection is a source value and the position at which to query its facts.
type Selection struct {
	ProviderBundle *check.ProviderBundleDescription
	Expr           check.Expr
	Func           *check.Func
	Package        *check.Package
	Type           check.Type
	Expression     string
	Definition     *diag.Pos
	Site           diag.Pos
	Value          bool
	Callable       *check.CallableDescription
	Assembly       *check.Assembly
	TailCall       *check.TailCall
}

func ParsePosition(text string) (diag.Pos, error) {
	last := strings.LastIndexByte(text, ':')
	if last < 0 {
		return diag.Pos{}, fmt.Errorf("expected file:line:column, found %q", text)
	}
	before := strings.LastIndexByte(text[:last], ':')
	if before < 1 {
		return diag.Pos{}, fmt.Errorf("expected file:line:column, found %q", text)
	}
	line, errLine := strconv.Atoi(text[before+1 : last])
	column, errColumn := strconv.Atoi(text[last+1:])
	if errLine != nil || errColumn != nil || line < 1 || column < 1 {
		return diag.Pos{}, fmt.Errorf("line and column must be positive integers")
	}
	return diag.Pos{File: text[:before], Line: line, Col: column}, nil
}

// Lookup maps a source token to the typed tree. Only the function containing
// the position is visited, so inserted defaults in other bodies cannot select
// a declaration in the current file.
func Lookup(files []*syntax.File, info *check.Info, pos diag.Pos, src []byte) (*Selection, error) {
	lines := strings.Split(string(src), "\n")
	if pos.Line < 1 || pos.Col < 1 || pos.Line > len(lines) || pos.Col > len(lines[pos.Line-1]) {
		return nil, fmt.Errorf("position %s is outside the source", pos)
	}
	if bundle, pkg := info.ProviderBundleAt(pos); bundle != nil {
		return &Selection{ProviderBundle: bundle, Package: pkg, Type: check.Invalid, Definition: &bundle.Definition, Expression: bundle.Name}, nil
	}
	tokens, _ := syntax.Lex(pos.File, src, &diag.List{})
	index := &sourceIndex{pos: pos, tokens: tokens, lines: lines, info: info}
	for _, file := range files {
		if file.Path != pos.File {
			continue
		}
		for _, derive := range file.Derives {
			if typ := check.EditorWrittenType(info, pos); typ != nil && derive.Pos.Line <= pos.Line && derive.End.Line >= pos.Line {
				definitions := check.EditorTypeDefinitions(typ)
				var definition *diag.Pos
				if len(definitions) > 0 {
					definition = &definitions[0].Pos
				}
				return &Selection{Type: typ, Expression: derive.Type.Name, Definition: definition, Site: pos}, nil
			}
		}
		for _, binding := range info.PackageBindings {
			if binding.Decl.Pos.File != pos.File {
				continue
			}
			index.fn = binding.Boundary
			if index.contains(binding.Var.Pos, len(binding.Var.Name)) || index.contains(binding.Decl.LazyPos, len("lazy")) {
				index.selectVar(binding.Var, binding.Value.Value.Pos())
			} else {
				index.walk(binding.Value.Value)
			}
		}
		for _, fd := range file.Funcs {
			fn := info.FuncOf[fd]
			if fn == nil {
				continue
			}
			index.fn = fn
			for _, v := range fn.ParamVars {
				if index.contains(v.Pos, len(v.Name)) {
					site := v.Pos
					if fn.Body != nil {
						site = fn.Body.Pos()
					}
					index.selectVar(v, site)
				}
			}
			if fn.Requires != nil {
				index.walk(fn.Requires)
			}
			if fn.Body != nil && index.inside(fn.Body) {
				index.walk(fn.Body)
			}
		}
		for _, fn := range info.Tests {
			if fn.Test.Pos.File == pos.File && index.inside(fn.Body) {
				index.fn = fn
				index.walk(fn.Body)
			}
		}
	}
	if index.selected == nil {
		return nil, fmt.Errorf("no expression or local name at %s", pos)
	}
	return index.selected, nil
}

type sourceIndex struct {
	info     *check.Info
	pos      diag.Pos
	tokens   []syntax.Token
	lines    []string
	fn       *check.Func
	selected *Selection
}

func (s *sourceIndex) inside(b *check.Block) bool {
	start, end := b.Pos(), b.End
	return start.File == s.pos.File && (s.pos.Line > start.Line || s.pos.Line == start.Line && s.pos.Col >= start.Col) && (s.pos.Line < end.Line || s.pos.Line == end.Line && s.pos.Col <= end.Col)
}

func (s *sourceIndex) contains(start diag.Pos, width int) bool {
	return start.File == s.pos.File && start.Line == s.pos.Line && start.Col <= s.pos.Col && s.pos.Col < start.Col+width
}

func tokenWidth(t syntax.Token) int {
	if t.Text != "" {
		width := len(t.Text)
		if t.Kind == syntax.TInterp {
			width++
		}
		return width
	}
	return len(strings.Trim(t.Kind.String(), "'"))
}

func (s *sourceIndex) token(pos diag.Pos) (syntax.Token, int) {
	for i, token := range s.tokens {
		if token.Pos == pos {
			return token, i
		}
	}
	return syntax.Token{}, -1
}

func (s *sourceIndex) choose(x check.Expr, t check.Type, def *diag.Pos) {
	s.selected = &Selection{Expr: x, Func: s.fn, Package: s.fn.Pkg, Type: t, Definition: def, Site: x.Pos(), Value: true}
	if c, ok := x.(*check.Const); ok && c.SourceSpan != nil {
		span := s.balancedSpan(*c.SourceSpan)
		parts := append([]string(nil), s.lines[span.Start.Line-1:span.End.Line]...)
		parts[len(parts)-1] = parts[len(parts)-1][:span.End.Col-1]
		parts[0] = parts[0][span.Start.Col-1:]
		s.selected.Expression = strings.Join(parts, "\n")
	}
}

func (s *sourceIndex) selectVar(v *check.Var, site diag.Pos) {
	s.choose(check.Reference(v), v.Type, &v.Pos)
	s.selected.Site = site
}

func (s *sourceIndex) walk(x check.Expr) {
	if source := s.info.Interpolations[x]; source != nil {
		start, end := source.Prefix.Start, source.Prefix.End
		if start.File == s.pos.File && (s.pos.Line > start.Line || s.pos.Line == start.Line && s.pos.Col >= start.Col) && (s.pos.Line < end.Line || s.pos.Line == end.Line && s.pos.Col < end.Col) {
			s.choose(x, x.Type(), definition(source.Factory))
		}
		s.walkInterpolationHoles(source.Holes)
		return
	}
	if lit, ok := x.(*check.RecordLit); ok && lit.Promoted {
		payload := lit.Fields[0].Value
		s.walk(payload)
		if s.selected != nil && s.selected.Expr == payload && s.selected.Value && s.selected.Type == payload.Type() {
			s.choose(lit, lit.Type(), definition(payload))
		}
		return
	}
	if block, ok := x.(*check.Block); ok && block.Conversion != nil {
		start, end := block.Conversion.Start, block.Conversion.End
		if start.File == s.pos.File && (s.pos.Line > start.Line || s.pos.Line == start.Line && s.pos.Col >= start.Col) && (s.pos.Line < end.Line || s.pos.Line == end.Line && s.pos.Col < end.Col) {
			s.choose(block, block.Type(), nil)
			return
		}
	}
	if ref, ok := x.(*check.VarRef); ok && strings.HasPrefix(ref.Var.Name, "_") {
		return
	}
	if block, ok := x.(*check.Block); ok && block.Assembly != nil {
		if s.contains(block.TokenPos(), len(block.Assembly.Mode)) || s.pos == block.Pos() {
			s.choose(block, block.Type(), nil)
			s.selected.Assembly = block.Assembly
			return
		}
	}
	if x == nil {
		return
	}
	// A select expression's lowered code calls prelude helpers at the
	// positions of what was written; describe what was written instead.
	if call, ok := x.(*check.Call); ok && call.Func.Prelude && strings.HasPrefix(call.Func.Decl.Name, "compilerSelect") {
		for _, a := range call.Args {
			s.walk(a)
		}
		return
	}
	token, i := s.token(x.TokenPos())
	if i < 0 {
		at := x.TokenPos()
		if at.File == s.pos.File && at.Line > 0 && at.Line <= len(s.lines) && at.Col <= len(s.lines[at.Line-1]) {
			nested, _ := syntax.Lex(at.File, []byte(s.lines[at.Line-1][at.Col-1:]), &diag.List{})
			if len(nested) > 0 {
				token = nested[0]
			}
		}
	}
	width := tokenWidth(token)
	switch x := x.(type) {
	case *check.VarRef:
		width = len(x.Var.Name)
	case *check.FuncRef:
		width = len(x.Name)
	case *check.Select:
		width = len(x.Name)
	case *check.RecordLit:
		_, i := s.token(x.TokenPos())
		if i >= 0 {
			if s.tokens[i].Kind == syntax.Dot && i+1 < len(s.tokens) && s.tokens[i+1].Kind == syntax.TIdent {
				i++
				width = s.tokens[i].Pos.Col + len(s.tokens[i].Text) - x.TokenPos().Col
			}
			for i+2 < len(s.tokens) && s.tokens[i+1].Kind == syntax.Dot && s.tokens[i+2].Kind == syntax.TIdent && s.tokens[i+2].Pos.Line == x.TokenPos().Line {
				i += 2
				width = s.tokens[i].Pos.Col + len(s.tokens[i].Text) - x.TokenPos().Col
			}
		}
	case *check.VariantValue:
		_, i := s.token(x.TokenPos())
		if i >= 0 && s.tokens[i].Kind == syntax.Dot && i+1 < len(s.tokens) && s.tokens[i+1].Kind == syntax.TIdent {
			width = s.tokens[i+1].Pos.Col + len(s.tokens[i+1].Text) - x.TokenPos().Col
		}
	}
	if s.contains(x.TokenPos(), width) || s.foldedContains(x) {
		s.choose(x, x.Type(), definition(x))
	}
	var head *check.ConstructorHead
	switch v := x.(type) {
	case *check.RecordLit:
		head = v.Head
	case *check.VariantValue:
		head = v.Head
	}
	if head != nil {
		for _, use := range head.Uses {
			if s.contains(use.Pos, len(use.Name)) {
				s.choose(x, use.Type, use.Definition)
				s.selected.Value = false
			}
		}
	}
	switch x := x.(type) {
	case *check.Call:
		s.callee(x)
		for _, label := range x.Labels {
			if s.contains(label.Pos, len(label.Name)) {
				pos := x.Func.Decl.Params[label.Param].Pos
				s.choose(x.Args[label.Param], x.Inst.Params[label.Param], &pos)
			}
		}
		for _, a := range x.Args {
			s.walk(a)
		}
	case *check.CallBuiltin:
		if s.contains(x.Pos(), len(x.Name)) {
			s.choose(x, x.Type(), nil)
		}
		for _, a := range x.Args {
			s.walk(a)
		}
	case *check.CallValue:
		s.walk(x.Fun)
		for _, a := range x.Args {
			s.walk(a)
		}
	case *check.Unary:
		s.walk(x.X)
	case *check.Binary:
		s.walk(x.X)
		s.walk(x.Y)
	case *check.If:
		s.walk(x.Cond)
		s.walk(x.Then)
		s.walk(x.Else)
	case *check.Block:
		for _, stmt := range x.Stmts {
			switch stmt := stmt.(type) {
			case *check.Let:
				width := len("lazy")
				if stmt.Deferred == check.AsyncBinding {
					width = len("async")
				}
				if !strings.HasPrefix(stmt.Var.Name, "_") && (s.contains(stmt.Var.Pos, len(stmt.Var.Name)) || stmt.Initializer != nil && s.contains(stmt.Initializer.Pos(), width)) {
					s.selectVar(stmt.Var, stmt.Value.Pos())
				}
				s.walk(stmt.AsyncScope)
				s.walk(stmt.Value)
			case *check.ExprStmt:
				s.walk(stmt.X)
			case *check.Trust:
				s.walk(stmt.Call)
			case *check.Mock:
				s.mock(stmt)
			}
		}
		s.walk(x.Tail)
	case *check.Return:
		s.walk(x.Value)
	case *check.Select:
		s.walk(x.X)
	case *check.RecordLit:
		for _, field := range x.Fields {
			s.walk(field.Value)
		}
	case *check.Copy:
		s.walk(x.X)
		for _, update := range x.Updates {
			s.walk(update.Value)
		}
	case *check.Match:
		if x.Assertion != nil && x.SourceCall != nil {
			name := x.SourceCall.Fun
			width := 0
			if id, ok := name.(*syntax.Ident); ok {
				width = len(id.Name)
			}
			if s.contains(name.Position(), width) {
				inst := x.Assertion
				pos := inst.Func.Decl.Pos
				s.choose(x, &check.FuncType{Params: inst.Params, Result: inst.Result, Effects: inst.Func.Effects}, &pos)
				s.selected.Value = false
				s.selected.Callable = check.DescribeCallable(inst.Func, inst.Params, s.fn.Pkg, false)
			}
		}
		s.walk(x.X)
		for _, arm := range x.Arms {
			s.pattern(arm.Pat, arm.Body.Pos())
			s.walk(arm.Body)
		}
	case *check.Try:
		s.walk(x.X)
	case *check.Interp:
		s.walkInterpolationHoles(x.Exprs)
	case *check.Lambda:
		for _, param := range x.Params {
			if s.contains(param.Pos, len(param.Name)) {
				s.selectVar(param, x.Pos())
			}
		}
		s.walk(x.Body)
	case *check.ScopeBlock:
		_, i := s.token(x.TokenPos())
		if i >= 0 && i+1 < len(s.tokens) && s.tokens[i+1].Text == x.Var.Name && s.contains(s.tokens[i+1].Pos, len(x.Var.Name)) {
			s.selectVar(x.Var, x.Body.Pos())
		}
		for _, policy := range x.Policies {
			s.walk(policy)
		}
		s.walk(x.Body)
	case *check.For:
		if x.Var != nil && s.contains(x.Var.Pos, len(x.Var.Name)) {
			s.selectVar(x.Var, x.Body.Pos())
		}
		s.walk(x.Items)
		for _, c := range x.Carries {
			if c.Header() && s.contains(c.Head.Pos, len(c.Head.Name)) {
				s.selectVar(c.Head, x.Body.Pos())
			}
			s.walk(c.Init)
		}
		s.walk(x.Cond)
		s.walk(x.Body)
		for _, c := range x.Carries {
			s.walk(c.Post)
		}
	case *check.ListLit:
		for _, elem := range x.Elems {
			s.walk(elem)
		}
	case *check.MapLit:
		for i, key := range x.Keys {
			s.walk(key)
			s.walk(x.Values[i])
		}
	}
}

func (s *sourceIndex) walkInterpolationHoles(holes []check.Expr) {
	for _, part := range holes {
		// Interpolation expressions are nested inside one lexer token.
		// Re-index their source so call names and parentheses work too.
		outer := s.tokens
		at := part.Pos()
		if at.File == s.pos.File && at.Line > 0 && at.Line <= len(s.lines) {
			line := s.lines[at.Line-1]
			start := at.Col - 1
			// Grouping parentheses have no typed nodes of their own.
			for start > 0 && strings.ContainsRune("( \t", rune(line[start-1])) {
				start--
			}
			s.tokens, _ = syntax.Lex(at.File, []byte(line[start:]), &diag.List{})
			for i := range s.tokens {
				s.tokens[i].Pos.Line += at.Line - 1
				s.tokens[i].Pos.Col += start
			}
		}
		s.walk(part)
		s.tokens = outer
	}
}

// The parser drops grouping parentheses. Recover those needed to keep the
// folded source spelling balanced, without swallowing a call's parentheses.
func (s *sourceIndex) balancedSpan(span check.SourceSpan) check.SourceSpan {
	_, first := s.token(span.Start)
	if first < 0 {
		return span
	}
	last, depth, missing := first, 0, 0
	for i := first; i < len(s.tokens); i++ {
		t := s.tokens[i]
		if t.Pos.Line > span.End.Line || t.Pos.Line == span.End.Line && t.Pos.Col >= span.End.Col {
			break
		}
		last = i
		switch t.Kind {
		case syntax.LParen:
			depth++
		case syntax.RParen:
			depth--
			if -depth > missing {
				missing = -depth
			}
		}
	}
	for missing > 0 && first > 0 && s.tokens[first-1].Kind == syntax.LParen {
		first--
		span.Start = s.tokens[first].Pos
		depth++
		missing--
	}
	for depth > 0 && last+1 < len(s.tokens) && s.tokens[last+1].Kind == syntax.RParen {
		last++
		span.End = s.tokens[last].Pos
		span.End.Col++
		depth--
	}
	return span
}

func (s *sourceIndex) foldedContains(x check.Expr) bool {
	c, ok := x.(*check.Const)
	if !ok || c.SourceSpan == nil {
		return false
	}
	span := c.SourceSpan
	start, end := span.Start, span.End
	inside := start.File == s.pos.File && (s.pos.Line > start.Line || s.pos.Line == start.Line && s.pos.Col >= start.Col) && (s.pos.Line < end.Line || s.pos.Line == end.Line && s.pos.Col < end.Col)
	if !inside {
		return false
	}
	for _, token := range s.tokens {
		if s.contains(token.Pos, tokenWidth(token)) {
			return true
		}
	}
	return false
}

// A declared call contains no FuncRef node. Its written callee is the
// identifier before '(' (or before explicit type arguments). Its type and
// definition still come entirely from the resolved call.
func (s *sourceIndex) callee(call *check.Call) {
	_, i := s.token(call.TokenPos())
	if i < 1 {
		return
	}
	if !call.Func.Decl.IsMethod {
		i--
	}
	if call.Func.Decl.IsMethod {
		j := i + 1
		if j < len(s.tokens) && s.tokens[j].Kind == syntax.LBrack {
			depth := 1
			for j++; j < len(s.tokens); j++ {
				if s.tokens[j].Kind == syntax.LBrack {
					depth++
				}
				if s.tokens[j].Kind == syntax.RBrack {
					depth--
				}
				if depth == 0 {
					j++
					break
				}
			}
		}
		for j < len(s.tokens) && s.tokens[j].Kind == syntax.RParen {
			j++
		}
		if j < len(s.tokens) && s.tokens[j].Kind == syntax.LParen && s.contains(s.tokens[j].Pos, 1) {
			pos := call.Func.Decl.Pos
			s.choose(call, call.Type(), &pos)
			return
		}
	}
	for i > 0 && s.tokens[i].Kind == syntax.RParen {
		i--
	}
	if s.tokens[i].Kind == syntax.RBrack {
		depth := 1
		for i--; i >= 0; i-- {
			switch s.tokens[i].Kind {
			case syntax.RBrack:
				depth++
			case syntax.LBrack:
				depth--
			}
			if depth == 0 {
				i--
				break
			}
		}
	}
	if i < 0 || s.tokens[i].Kind != syntax.TIdent {
		return
	}
	name := s.tokens[i]
	width := len(name.Text)
	if !call.ReceiverCall {
		for i >= 2 && s.tokens[i-1].Kind == syntax.Dot && s.tokens[i-2].Kind == syntax.TIdent {
			first := s.tokens[i-2]
			if first.Pos.Line != name.Pos.Line {
				break
			}
			width = name.Pos.Col + width - first.Pos.Col
			name = first
			i -= 2
		}
	}
	if !s.contains(name.Pos, width) {
		return
	}
	params := call.Inst.Params
	if call.ReceiverCall {
		params = params[1:]
	}
	pos := call.Func.Decl.Pos
	s.choose(call, &check.FuncType{Params: params, Result: call.Inst.Result, Effects: call.Func.Effects}, &pos)
	s.selected.Value = false
	s.selected.Callable = check.DescribeCallable(call.Func, params, s.fn.Pkg, call.ReceiverCall)
	s.selected.TailCall = s.info.TailCalls[call]
}

// mock selects in a mock statement: its handle, its target (the
// function it mocks), its parameters (with the target's types), or code
// in its body, which is checked as a function of its own.
func (s *sourceIndex) mock(m *check.Mock) {
	if m.Var != nil && s.contains(m.Var.Pos, len(m.Var.Name)) {
		s.selectVar(m.Var, m.Pos)
	}
	if s.contains(m.TargetPos, len(m.Text)) {
		pos := m.Target.Decl.Pos
		s.choose(check.MockTargetRef(m), check.MockTargetRef(m).Type(), &pos)
		s.selected.Value = false
		s.selected.Callable = check.DescribeCallable(m.Target, m.Target.Params, s.fn.Pkg, false)
	}
	outer := s.fn
	s.fn = m.Func
	for _, p := range m.Func.ParamVars {
		if s.contains(p.Pos, len(p.Name)) {
			s.selectVar(p, m.Func.Body.Pos())
		}
	}
	s.walk(m.Func.Body)
	s.fn = outer
}

func (s *sourceIndex) pattern(p *check.Pat, site diag.Pos) {
	if p == nil {
		return
	}
	if p.Var != nil && !strings.HasPrefix(p.Var.Name, "_") && s.contains(p.Var.Pos, len(p.Var.Name)) {
		s.selectVar(p.Var, site)
	}
	s.pattern(p.Sub, site)
	for _, field := range p.Fields {
		s.pattern(field.Pat, site)
	}
	for _, elem := range p.Elems {
		s.pattern(elem, site)
	}
	s.pattern(p.Rest, site)
}

func definition(x check.Expr) *diag.Pos {
	var pos diag.Pos
	switch x := x.(type) {
	case *check.VarRef:
		pos = x.Var.Pos
		if x.Var.Origin.Line != 0 {
			pos = x.Var.Origin
		}
	case *check.FuncRef:
		pos = x.Inst.Func.Decl.Pos
	case *check.Call:
		pos = x.Func.Decl.Pos
	case *check.Select:
		if r, ok := x.X.Type().(*check.Record); ok && r.Decl != nil {
			for _, field := range r.Decl.Fields {
				if field.Name == x.Name {
					pos = field.Pos
				}
			}
		}
	case *check.VariantValue:
		for _, variant := range x.Variant.Parent.Decl.Variants {
			if variant.Name == x.Variant.Name {
				pos = variant.Pos
			}
		}
	case *check.RecordLit:
		if x.Record != nil && x.Record.Decl != nil {
			pos = x.Record.Decl.Pos
		} else if x.Variant != nil {
			for _, variant := range x.Variant.Parent.Decl.Variants {
				if variant.Name == x.Variant.Name {
					pos = variant.Pos
				}
			}
		}
	}
	if pos.File == "" {
		return nil
	}
	return &pos
}
