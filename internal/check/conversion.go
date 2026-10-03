package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

type conversionOverride struct {
	path    []string
	value   syntax.Expr
	success Type
	errors  []Type
	name    string
}
type conversionPair struct{ source, target Type }
type conversionBuilder struct {
	c      *checker
	call   *syntax.Call
	prefix string
	serial int
	active map[conversionPair]bool
	failed bool
}

func (g *conversionBuilder) problem(code string, pos diag.Pos, format string, args ...any) {
	g.c.diags.AddCode(pos, "conversion."+code, format, args...)
	g.failed = true
}
func (g *conversionBuilder) fresh(label string) string {
	g.serial++
	return fmt.Sprintf("%s%s%d", g.prefix, label, g.serial)
}
func (g *conversionBuilder) typeExpr(t Type) *syntax.TypeExpr {
	e := &syntax.TypeExpr{Pos: g.call.Pos}
	g.c.info.assemblyTypes[e] = t
	return e
}
func (g *conversionBuilder) id(name string) syntax.Expr {
	return &syntax.Ident{Pos: g.call.Pos, Name: name}
}
func (g *conversionBuilder) bind(name string, value syntax.Expr) *syntax.Binding {
	b := &syntax.Binding{Pos: g.call.Pos, Name: name, Value: value}
	g.c.info.assemblyNames[b] = "conversion value"
	return b
}
func (c *checker) into(call *syntax.Call, sel *syntax.Selector) Type {
	c.conversionSerial++
	g := &conversionBuilder{c: c, call: call, prefix: fmt.Sprintf("_into%d_", c.conversionSerial), active: map[conversionPair]bool{}}
	if len(call.TypeArgs) != 1 {
		g.problem("target", call.Pos, "into requires one target record type")
		return Invalid
	}
	target := c.resolveType(call.TypeArgs[0])
	record, ok := target.(*Record)
	if !ok || c.open(target) {
		if target != Invalid {
			g.problem("target", call.TypeArgs[0].Pos, "into requires a record target, found %s", target)
		}
		return Invalid
	}
	source := c.expr(sel.X)
	if source == Invalid {
		return Invalid
	}
	if source == Never {
		c.info.conversionInputs[sel.X] = true
		block := &syntax.Block{Pos: call.Pos, End: call.End, Tail: sel.X}
		c.info.conversionCalls[call] = block
		return c.block(block, target)
	}
	if _, ok := source.(*Record); !ok {
		g.problem("source", sel.Pos, "into requires a record source, found %s", source)
		return Invalid
	}
	c.info.conversionInputs[sel.X] = true
	sourceName := g.fresh("source")
	var overrides []*conversionOverride
	for i, value := range call.Args {
		if i >= len(call.Arguments) || call.Arguments[i].Name == "" {
			g.problem("override", value.Position(), "into overrides must name a target field")
			continue
		}
		path := strings.Split(call.Arguments[i].Name, ".")
		current := record
		var field *Field
		valid := true
		for j, name := range path {
			field = current.Field(name)
			if field == nil {
				g.problem("override", call.Arguments[i].Pos, "target has no field %s", strings.Join(path[:j+1], "."))
				var candidates []*syntax.Param
				for _, field := range current.Fields {
					candidates = append(candidates, &syntax.Param{Name: field.Name})
				}
				if name := closestParam(name, candidates); name != "" {
					corrected := append([]string(nil), path...)
					corrected[j] = name
					c.diags.Suggest(call.Arguments[i].Pos, "conversion.override", call.Arguments[i].NameEnd, diag.Fix{Message: "use field " + strings.Join(corrected, "."), Edits: []diag.TextEdit{{Start: call.Arguments[i].Pos, End: call.Arguments[i].NameEnd, Replacement: strings.Join(corrected, ".")}}})
				}
				valid = false
				break
			}
			if j < len(path)-1 {
				current, ok = field.Type.(*Record)
				if !ok {
					g.problem("override", call.Arguments[i].Pos, "%s is not a record field", strings.Join(path[:j+1], "."))
					valid = false
					break
				}
			}
		}
		if !valid {
			continue
		}
		for _, previous := range overrides {
			a, b := strings.Join(previous.path, "."), strings.Join(path, ".")
			if a == b || strings.HasPrefix(a, b+".") || strings.HasPrefix(b, a+".") {
				g.problem("override", call.Arguments[i].Pos, "overrides of %s and %s overlap", a, b)
			}
		}
		typ := c.exprWant(value, field.Type)
		c.info.conversionInputs[value] = true
		override := &conversionOverride{path: path, value: value, success: typ, name: g.fresh("override")}
		if typ == Invalid {
			g.failed = true
			continue
		}
		if !assignable(typ, field.Type) && typ != Never {
			union, ok := typ.(*Union)
			if !ok {
				g.problem("incompatible_field", value.Position(), "override %s requires %s, found %s", strings.Join(path, "."), field.Type, typ)
				g.valueSuggestion(i)
				continue
			}
			var successes []Type
			for _, member := range union.Members {
				if assignable(member, field.Type) {
					successes = append(successes, member)
				} else {
					if identical(member, target) {
						g.problem("failure_member", value.Position(), "failure member %s is also the conversion target; use ? or a wrapper error", member)
					}
					override.errors = append(override.errors, member)
				}
			}
			if len(successes) == 0 {
				g.problem("incompatible_field", value.Position(), "override %s has no member assignable to %s", strings.Join(path, "."), field.Type)
				g.valueSuggestion(i)
				continue
			}
			override.success = newUnion(successes)
		}
		overrides = append(overrides, override)
	}
	if g.failed {
		return Invalid
	}
	last := len(overrides)
	for i, override := range overrides {
		if override.success == Never {
			last = i
			break
		}
	}
	var tail syntax.Expr
	if last < len(overrides) {
		// A bottom input ends the conversion itself; it cannot be bound to
		// a temporary, and no candidate or later override is evaluated.
		tail = overrides[last].value
	} else {
		candidate := g.build(g.id(sourceName), source, target, overrides, "")
		if g.failed {
			return Invalid
		}
		// The successful annotation proves additional target-alias facts,
		// including identity conversions that construct no new record.
		resultName := g.fresh("result")
		success := g.bind(resultName, candidate)
		success.Type = call.TypeArgs[0]
		c.info.assemblyNames[success] = "conversion target " + TypeText(target, c.pkg)
		tail = &syntax.Block{Pos: call.Pos, End: call.End, Stmts: []syntax.Stmt{success}, Tail: g.id(resultName)}
	}
	for i := last - 1; i >= 0; i-- {
		override := overrides[i]
		if len(override.errors) == 0 {
			tail = &syntax.Block{Pos: call.Pos, End: call.End, Stmts: []syntax.Stmt{g.bind(override.name, override.value)}, Tail: tail}
			continue
		}
		match := &syntax.Match{Pos: override.value.Position(), X: override.value}
		pat := &syntax.TypePat{Pos: override.value.Position(), Name: override.name, Type: g.typeExpr(override.success)}
		c.info.assemblyNames[pat] = "successful conversion override " + strings.Join(override.path, ".")
		match.Arms = append(match.Arms, &syntax.Arm{Pattern: pat, Body: tail})
		for _, failure := range override.errors {
			name := g.fresh("failure")
			match.Arms = append(match.Arms, &syntax.Arm{Pattern: &syntax.TypePat{Pos: override.value.Position(), Name: name, Type: g.typeExpr(failure)}, Body: g.id(name)})
		}
		tail = match
	}
	block := &syntax.Block{Pos: call.Pos, End: call.End, Stmts: []syntax.Stmt{g.bind(sourceName, sel.X)}, Tail: tail}
	c.info.conversionCalls[call] = block
	var members []Type
	if last == len(overrides) {
		members = append(members, target)
	}
	for _, override := range overrides[:last] {
		members = append(members, override.errors...)
	}
	result := Type(Never)
	if len(members) > 0 {
		result = newUnion(members)
	}
	return c.block(block, result)
}

func (g *conversionBuilder) build(source syntax.Expr, sourceType, target Type, overrides []*conversionOverride, path string) syntax.Expr {
	pos := g.call.Pos
	if len(overrides) == 0 && source != nil && assignable(sourceType, target) {
		return source
	}
	pair := conversionPair{sourceType, target}
	if g.active[pair] {
		g.problem("cycle", pos, "recursive conversion cycle at %s from %s to %s", conversionPath(path), sourceType, target)
		return &syntax.IntLit{Pos: pos, Text: "0"}
	}
	g.active[pair] = true
	defer delete(g.active, pair)
	if record, ok := target.(*Record); ok {
		if source != nil {
			if _, ok := sourceType.(*Record); !ok {
				g.problem("incompatible_field", pos, "cannot convert field %s from %s to %s", conversionPath(path), sourceType, target)
				g.overrideSuggestion("incompatible_field", path)
				return &syntax.IntLit{Pos: pos, Text: "0"}
			}
		}
		if !g.c.recordConstruction(pos, record, "convert into") {
			g.failed = true
			return &syntax.IntLit{Pos: pos, Text: "0"}
		}
		var bindings []syntax.Stmt
		if source != nil {
			name := g.fresh("nested")
			bindings = append(bindings, g.bind(name, source))
			source = g.id(name)
		}
		sourceRecord, _ := sourceType.(*Record)
		lit := &syntax.RecordLit{Type: &syntax.Ident{Pos: pos, Name: record.Name}}
		g.c.info.conversionRecords[lit] = record
		for _, field := range record.Fields {
			fieldPath := field.Name
			if path != "" {
				fieldPath = path + "." + fieldPath
			}
			var nested []*conversionOverride
			var explicit *conversionOverride
			for _, override := range overrides {
				if override.path[0] != field.Name {
					continue
				}
				if len(override.path) == 1 {
					explicit = override
				} else {
					copy := *override
					copy.path = override.path[1:]
					nested = append(nested, &copy)
				}
			}
			var value syntax.Expr
			if explicit != nil {
				value = g.id(explicit.name)
			} else {
				var original syntax.Expr
				var originalType Type
				if sourceRecord != nil {
					if found := sourceRecord.Field(field.Name); found != nil {
						original = &syntax.Selector{Pos: pos, X: source, Name: field.Name}
						originalType = found.Type
					}
				}
				if original == nil {
					g.c.ensureFieldDefault(field)
					if def := g.c.info.fieldDefaults[field]; def != nil {
						original = def
						originalType = field.Type
						g.c.info.conversionInputs[def] = true
					}
				}
				if original == nil && len(nested) == 0 {
					g.problem("missing_field", pos, "conversion target is missing required field %s", fieldPath)
					g.overrideSuggestion("missing_field", fieldPath)
					continue
				}
				value = g.build(original, originalType, field.Type, nested, fieldPath)
			}
			lit.Fields = append(lit.Fields, &syntax.FieldInit{Pos: pos, Name: field.Name, Value: value})
		}
		if len(bindings) > 0 {
			return &syntax.Block{Pos: pos, End: g.call.End, Stmts: bindings, Tail: lit}
		}
		return lit
	}
	if len(overrides) == 0 && source != nil {
		if from, ok := sourceType.(*List); ok {
			if to, ok := target.(*List); ok {
				name := g.fresh("element")
				body := g.build(g.id(name), from.Elem, to.Elem, nil, path+".[]")
				return &syntax.Call{Pos: pos, Fun: &syntax.Selector{Pos: pos, X: source, Name: "map"}, Args: []syntax.Expr{&syntax.Lambda{Pos: pos, Params: []*syntax.Param{{Pos: pos, Name: name}}, Body: body}}}
			}
		}
		if from, ok := sourceType.(*Sealed); ok && from.Name == "Option" && from.Pkg == g.c.preludePkg {
			if to, ok := target.(*Sealed); ok && to.Name == "Option" && to.Pkg == g.c.preludePkg {
				name := g.fresh("element")
				body := g.build(g.id(name), from.Args[0], to.Args[0], nil, path+".value")
				return &syntax.Call{Pos: pos, Fun: &syntax.Selector{Pos: pos, X: source, Name: "map"}, Args: []syntax.Expr{&syntax.Lambda{Pos: pos, Params: []*syntax.Param{{Pos: pos, Name: name}}, Body: body}}}
			}
		}
	}
	g.problem("incompatible_field", pos, "cannot convert field %s from %s to %s", conversionPath(path), sourceType, target)
	g.overrideSuggestion("incompatible_field", path)
	return &syntax.IntLit{Pos: pos, Text: "0"}
}
func conversionPath(path string) string {
	if path == "" {
		return "<target>"
	}
	return path
}

// Suggestions request an explicit replacement policy, without reevaluating the source.
func (g *conversionBuilder) overrideSuggestion(code, path string) {
	if path == "" {
		return
	}
	field := strings.Split(path, ".")[0]
	insertion := g.call.Pos
	insertion.Col++
	text := field + ": Value"
	if len(g.call.Args) > 0 {
		text += ", "
	}
	g.c.diags.Suggest(g.call.Pos, "conversion."+code, g.call.End, diag.Fix{Message: "supply an override for " + field + " (replace Value with its value)", RequiresInput: true, Edits: []diag.TextEdit{{Start: insertion, End: insertion, Replacement: text}}})
}

func (g *conversionBuilder) valueSuggestion(index int) {
	arg := g.call.Arguments[index]
	if arg.ValueStart.File == "" || arg.End.File == "" {
		return
	}
	g.c.diags.Suggest(g.call.Args[index].Position(), "conversion.incompatible_field", arg.End, diag.Fix{Message: "replace the override for " + arg.Name + " with a compatible value", RequiresInput: true, Edits: []diag.TextEdit{{Start: arg.ValueStart, End: arg.End, Replacement: "Value"}}})
}
