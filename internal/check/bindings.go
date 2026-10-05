package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// bindRebinding installs a sequential local binding after its initializer has
// been checked against the preceding environment. Simultaneous declarations
// (parameters and patterns) use bind, which also rejects duplicate names.
func (c *checker) bindRebinding(name string, pos diag.Pos, t Type, node any) {
	if b, ok := node.(*syntax.Binding); ok && !c.noCarry {
		if l, i := c.lookupAt(name); l != nil && l.carry != nil {
			c.rebindCarried(b, l, i, t)
			return
		} else if l != nil && i < len(c.scopes)-1 && c.misplacedRebinding(name, pos, i) {
			c.scopes[len(c.scopes)-1][name] = &local{typ: Invalid, decl: node, used: true}
			return
		}
	}
	scope := c.scopes[len(c.scopes)-1]
	if previous := scope[name]; previous != nil {
		c.unusedLocal(previous)
		c.info.rebindings[node] = previous.decl
		scope[name] = &local{typ: t, node: node, decl: node}
		return
	}
	c.bind(name, pos, t, node)
}

func (c *checker) unusedLocal(l *local) {
	// Whether a carried value is read is decided over the whole loop
	// (see CheckCarried).
	if l.node == nil || l.used || l.carry != nil || c.info.unused[l.node] {
		return
	}
	c.info.unused[l.node] = true
	if l.typ == Invalid {
		return
	}
	var name string
	var pos diag.Pos
	var replacement string
	var extraFixes []diag.Fix
	switch node := l.node.(type) {
	case *syntax.Binding:
		name, pos, replacement = node.Name, node.Pos, "_"
		// Deferred initializers cannot be discarded without changing when they run.
		if node.Lazy || node.AsyncScope != nil {
			replacement = ""
		}
		if c.info.consts[node.Value] != nil && node.AsyncScope == nil {
			if file := c.bindingFiles[node.Pos.File]; file != nil {
				_, end := newLintSource(file).exprRange(node.Value)
				for _, span := range file.ExpressionSpans {
					if span.Expr == node.Value && compareEditorPosition(span.End, end) > 0 {
						end = span.End
					}
				}
				start := node.Pos
				if node.Lazy {
					start = node.LazyPos
				}
				extraFixes = append(extraFixes, diag.Fix{Message: "Remove the binding", Edits: []diag.TextEdit{{Start: start, End: end}}})
			}
		}
	case *syntax.VariantPat:
		if len(node.Path) != 1 {
			return
		}
		name, pos, replacement = node.Path[0], node.Pos, "_"
	case *syntax.TypePat:
		name, pos, replacement = node.Name, node.Pos, "_"
	case *syntax.FieldPat:
		name, pos, replacement = node.Field, node.Pos, node.Field+": _"
	case *syntax.ListPat:
		name, pos = node.Rest, node.RestPos
		pos.Col += len("...")
		if file := c.bindingFiles[node.RestPos.File]; file != nil {
			source := newLintSource(file)
			if i, ok := source.positions[node.RestPos]; ok && i+1 < len(source.tokens) {
				pos = source.tokens[i+1].Pos
			}
		}
		end := pos
		end.Col += len(name)
		extraFixes = append(extraFixes, diag.Fix{Message: "Discard the rest", Edits: []diag.TextEdit{{Start: pos, End: end}}})
	case *syntax.MockStmt:
		name, pos = node.Name, node.Pos
		extraFixes = append(extraFixes, diag.Fix{Message: "Remove the binding", Edits: []diag.TextEdit{{Start: pos, End: node.MockPos}}})
	case *syntax.For:
		name, pos, replacement = node.Name, node.NamePos, "_"
	default:
		return
	}
	if name == "" || strings.HasPrefix(name, "_") {
		return
	}
	end := pos
	end.Col += len(name)
	c.diags.AddCode(pos, "binding.unused", "binding %s is never read; discard explicitly with _", name)
	var fixes []diag.Fix
	if replacement != "" {
		fixes = append(fixes, diag.Fix{Message: "Replace the binding with _", Edits: []diag.TextEdit{{Start: pos, End: end, Replacement: replacement}}})
	}
	fixes = append(fixes, extraFixes...)
	c.diags.Suggest(pos, "binding.unused", end, fixes...)
}

// Rebinding reports the declaration replaced by the binding at definition.
func Rebinding(info *Info, definition diag.Pos) *diag.Pos {
	for node, previous := range info.rebindings {
		if sourceNodePosition(node) != definition {
			continue
		}
		pos := sourceNodePosition(previous)
		if pos.Line != 0 {
			return &pos
		}
	}
	return nil
}
