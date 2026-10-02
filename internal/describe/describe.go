// Package describe adapts source positions to compiler queries. Syntax and Info
// accesses live here so the lookup can move to the typed tree independently of
// the CLI and its output format.
package describe

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/check"
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Result is a compiler description of the selected source value.
type Result struct {
	SchemaVersion int                       `json:"schema_version"`
	Position      diag.Pos                  `json:"position"`
	Type          string                    `json:"type"`
	Definition    *diag.Pos                 `json:"definition,omitempty"`
	Methods       []check.MethodDescription `json:"methods"`
	Facts         []check.KnownFact         `json:"facts"`
	Proof         *check.Proof              `json:"proof,omitempty"`
}

// Selection is the current adapter's handle on a source value.
type Selection struct {
	Expr       syntax.Expr
	Func       *check.Func
	Package    *check.Package
	Type       check.Type
	Definition *diag.Pos
	Site       diag.Pos
}

// ParsePosition accepts file:line:column, including colons in the file path.
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

// Lookup selects expression tokens, binding names, and parameter names.
// Operators select their expression; a call's opening '(' selects its result.
// Whitespace and comments do not silently select a neighboring expression.
func Lookup(files []*syntax.File, info *check.Info, pos diag.Pos, src []byte) (*Selection, error) {
	lines := strings.Split(string(src), "\n")
	if pos.Line > len(lines) || pos.Col > len(lines[pos.Line-1]) {
		return nil, fmt.Errorf("position %s is outside the source", pos)
	}
	index := &sourceIndex{info: info, pos: pos, src: src}
	for _, file := range files {
		if file.Path != pos.File {
			continue
		}
		for _, fd := range file.Funcs {
			fn := info.FuncOf[fd]
			if fn == nil {
				continue
			}
			index.fn = fn
			for i, param := range fd.Params {
				if index.contains(param.Pos, len(param.Name)) {
					index.selectLocal(param, param.Name, param.Pos, fn.Params[i])
					if fd.Body != nil {
						index.selected.Site = fd.Body.Pos
					}
				}
				index.walk(param.Default)
			}
			if fd.Body != nil {
				index.walk(fd.Body)
			}
		}
		for _, fn := range info.Tests {
			if fn.Test.Pos.File == pos.File {
				index.fn = fn
				index.walk(fn.Decl.Body)
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
	src      []byte
	fn       *check.Func
	selected *Selection
}

func (s *sourceIndex) contains(start diag.Pos, width int) bool {
	return start.File == s.pos.File && start.Line == s.pos.Line && start.Col <= s.pos.Col && s.pos.Col < start.Col+width
}

func (s *sourceIndex) choose(x syntax.Expr, t check.Type, def *diag.Pos) {
	if t != nil {
		s.selected = &Selection{Expr: x, Func: s.fn, Package: s.fn.Pkg, Type: t, Definition: def, Site: x.Position()}
	}
}

func (s *sourceIndex) selectLocal(node any, name string, pos diag.Pos, t check.Type) {
	id := &syntax.Ident{Pos: pos, Name: name}
	// Synthetic identifiers give the existing prover the same declaration
	// identity as references to the local. The typed-tree adapter will use Var.
	s.info.Defs[id] = node
	s.info.Types[id] = t
	s.choose(id, t, &pos)
}

func (s *sourceIndex) walk(x syntax.Expr) {
	if x == nil {
		return
	}
	if s.contains(x.Position(), s.width(x)) {
		s.choose(x, s.info.Types[x], definition(s.info, x))
	}
	switch x := x.(type) {
	case *syntax.Call:
		if inst := s.info.Instances[x]; inst != nil && s.contains(x.Fun.Position(), s.width(x.Fun)) {
			params := inst.Params
			if inst.Func.Decl.IsMethod {
				params = params[1:]
			}
			def := inst.Func.Decl.Pos
			s.choose(x.Fun, &check.FuncType{Params: params, Result: inst.Result}, &def)
		}
		s.walk(x.Fun)
		for _, a := range x.Args {
			s.walk(a)
		}
	case *syntax.Unary:
		s.walk(x.X)
	case *syntax.Binary:
		s.walk(x.X)
		s.walk(x.Y)
	case *syntax.If:
		s.walk(x.Cond)
		s.walk(x.Then)
		s.walk(x.Else)
	case *syntax.Block:
		for _, stmt := range x.Stmts {
			switch stmt := stmt.(type) {
			case *syntax.Binding:
				if s.contains(stmt.Pos, len(stmt.Name)) {
					s.selectLocal(stmt, stmt.Name, stmt.Pos, s.info.Bindings[stmt])
					s.selected.Site = stmt.Value.Position()
				}
				s.walk(stmt.Value)
			case *syntax.ExprStmt:
				s.walk(stmt.X)
			case *syntax.TrustStmt:
				s.walk(stmt.Call)
			}
		}
		s.walk(x.Tail)
	case *syntax.Return:
		s.walk(x.Value)
	case *syntax.Selector:
		s.walk(x.X)
	case *syntax.RecordLit:
		for _, field := range x.Fields {
			s.walk(field.Value)
		}
	case *syntax.Copy:
		s.walk(x.X)
		for _, update := range x.Updates {
			s.walk(update.Value)
		}
	case *syntax.Match:
		s.walk(x.X)
		for _, arm := range x.Arms {
			s.walk(arm.Body)
		}
	case *syntax.Try:
		s.walk(x.X)
	case *syntax.Interp:
		for _, part := range x.Exprs {
			s.walk(part)
		}
	case *syntax.Lambda:
		if ft, ok := s.info.Types[x].(*check.FuncType); ok {
			for i, param := range x.Params {
				if s.contains(param.Pos, len(param.Name)) {
					s.selectLocal(param, param.Name, param.Pos, ft.Params[i])
					s.selected.Site = x.Position()
				}
			}
		}
		s.walk(x.Body)
	case *syntax.ScopeExpr:
		for _, policy := range x.Policies {
			s.walk(policy)
		}
		s.walk(x.Body)
	case *syntax.ListLit:
		for _, elem := range x.Elems {
			s.walk(elem)
		}
	case *syntax.MapLit:
		for i, key := range x.Keys {
			s.walk(key)
			s.walk(x.Values[i])
		}
	}
}

func (s *sourceIndex) width(x syntax.Expr) int {
	switch x := x.(type) {
	case *syntax.Ident:
		return len(x.Name)
	case *syntax.Selector:
		return len(x.Name)
	case *syntax.IntLit:
		return len(x.Text)
	case *syntax.FloatLit:
		return len(x.Text)
	case *syntax.RuneLit:
		return len(x.Text)
	case *syntax.BoolLit:
		if x.Value {
			return 4
		}
		return 5
	case *syntax.Unary:
		return len(strings.Trim(x.Op.String(), "'"))
	case *syntax.Binary:
		return len(strings.Trim(x.Op.String(), "'"))
	case *syntax.If:
		return 2
	case *syntax.Match:
		return 5
	case *syntax.Return:
		return 6
	case *syntax.ScopeExpr:
		return 5
	case *syntax.Copy:
		return 4
	case *syntax.RecordLit:
		return s.width(x.Type)
	}
	// Strings and interpolation need their raw spelling, since their AST
	// values have already been unquoted. Stop at the first unescaped quote.
	if _, ok := x.(*syntax.StringLit); ok {
		start := s.offset(x.Position())
		for i := start + 1; i < len(s.src); i++ {
			if s.src[i] == '\\' {
				i++
			} else if s.src[i] == '"' {
				return i - start + 1
			}
		}
	}
	return 1
}

func (s *sourceIndex) offset(pos diag.Pos) int {
	offset := 0
	for line := 1; line < pos.Line; line++ {
		offset += strings.IndexByte(string(s.src[offset:]), '\n') + 1
	}
	return offset + pos.Col - 1
}

func definition(info *check.Info, x syntax.Expr) *diag.Pos {
	var pos diag.Pos
	switch x := x.(type) {
	case *syntax.Ident:
		switch d := info.Defs[x].(type) {
		case *syntax.Param:
			pos = d.Pos
		case *syntax.Binding:
			pos = d.Pos
		case syntax.Pattern:
			pos = d.Position()
		case *syntax.FieldPat:
			pos = d.Pos
		case *syntax.ScopeExpr:
			pos = d.Pos
		}
		if inst := info.FuncRefs[x]; inst != nil {
			pos = inst.Func.Decl.Pos
		}
	case *syntax.Call:
		if fn := info.CallFuncs[x]; fn != nil {
			pos = fn.Decl.Pos
		}
	case *syntax.Selector:
		if r, ok := info.Types[x.X].(*check.Record); ok {
			for _, field := range r.Decl.Fields {
				if field.Name == x.Name {
					pos = field.Pos
				}
			}
		}
		if v := info.SelectorVariants[x]; v != nil {
			for _, variant := range v.Parent.Decl.Variants {
				if variant.Name == v.Name {
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
