package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
	"strconv"
	"strings"
)

// Positional constructors share field inference and validation with named
// literals; the source call remains available for editor queries.
func (c *checker) positionalVariantCall(call *syntax.Call, want Type) (Type, bool) {
	var variant *Variant
	switch head := call.Fun.(type) {
	case *syntax.ContextName:
		owner, ok := c.contextTarget(head, want, false).(*Sealed)
		if !ok {
			for _, arg := range call.Args {
				c.expr(arg)
			}
			return Invalid, true
		}
		variant = c.contextVariantOf(head, owner)
	case *syntax.Selector:
		var owner Type
		switch x := head.X.(type) {
		case *syntax.Ident:
			if c.lookup(x.Name) != nil {
				return nil, false
			}
			owner = c.typeNamed(x.Name)
		case *syntax.TypeHead:
			owner = c.resolveType(x.Type)
		default:
			return nil, false
		}
		sealed, ok := owner.(*Sealed)
		if !ok {
			return nil, false
		}
		variant = c.specializedVariant(head.Pos, sealed, head.Name)
	default:
		return nil, false
	}
	if variant == nil {
		for _, arg := range call.Args {
			c.expr(arg)
		}
		return Invalid, true
	}
	if !variant.Positional {
		c.errorf(call.Pos, "%s.%s has no positional payload; use braces for named fields or a bare name for a fieldless variant", variant.Parent.Name, variant.Name)
		for _, arg := range call.Args {
			c.expr(arg)
		}
		return Invalid, true
	}
	if len(call.TypeArgs) != 0 {
		c.errorf(call.Pos, "type arguments belong on the variant owner, such as %s[Int].%s(...)", variant.Parent.Name, variant.Name)
	}
	if hasNamedArgs(call) {
		c.errorf(call.Pos, "positional variant payloads do not accept named arguments")
	}
	if len(call.Args) != len(variant.Fields) {
		c.errorf(call.Pos, "%s.%s needs %d payload values, got %d", variant.Parent.Name, variant.Name, len(variant.Fields), len(call.Args))
		for _, arg := range call.Args {
			c.expr(arg)
		}
		return Invalid, true
	}
	literal := &syntax.RecordLit{End: call.End, Type: call.Fun, Positional: true}
	for i, arg := range call.Args {
		literal.Fields = append(literal.Fields, &syntax.FieldInit{Pos: arg.Position(), Name: strconv.Itoa(i), Value: arg})
	}
	c.info.variantCalls[call] = literal
	typ := c.recordLit(literal, want)
	c.record(literal, typ)
	return typ, true
}

func (c *checker) variantPatterns(pat *Pat, raw *syntax.VariantPat, variant *Variant, owner string) bool {
	if raw.Positional {
		if !variant.Positional {
			c.errorf(raw.Pos, "%s has no positional payload; use braces for named fields or a bare name for a fieldless variant", owner)
			return false
		}
		if len(raw.Elems) != len(variant.Fields) {
			c.errorf(raw.Pos, "%s needs %d payload patterns, got %d", owner, len(variant.Fields), len(raw.Elems))
			return false
		}
		ok := true
		for i, elem := range raw.Elems {
			sub := c.pattern(elem, variant.Fields[i].Type)
			if sub == nil {
				ok = false
				continue
			}
			pat.Fields = append(pat.Fields, &PatField{Name: variant.Fields[i].Name, Pat: sub})
		}
		return ok
	}
	if raw.Braces && variant.Positional {
		c.diags.AddCode(raw.Pos, "type.variant_payload_form", "%s uses positional payloads; write %s(...) instead of braces", owner, owner)
		if len(raw.Fields) == 1 && raw.Fields[0].Field == "value" && len(variant.Fields) == 1 {
			file := c.bindingFiles[raw.Pos.File]
			if file != nil {
				source := newLintSource(file)
				lo := source.positions[raw.PayloadPos]
				hi := source.pairs[lo]
				valueStart := lo + 1
				if raw.Fields[0].Pattern != nil {
					valueStart = source.positions[raw.Fields[0].Pattern.Position()]
				}
				c.payloadBraceFix(raw.Pos, raw.PayloadPos, source.tokens[valueStart].Pos, source.tokens[hi-1].End, raw.PayloadEnd)
			}
		}
		return false
	}
	return c.fieldPatterns(pat, raw.Fields, variant.Fields, owner)
}

// rejectPositionalBraces diagnoses the source form before generic field inference.
func (c *checker) rejectPositionalBraces(literal *syntax.RecordLit, want Type) bool {
	var typ Type
	var name string
	switch head := literal.Type.(type) {
	case *syntax.ContextName:
		candidates, unknown := c.contextCandidates(head.Name, want)
		if !unknown && len(candidates) == 1 {
			typ = candidates[0]
			name = head.Name
		}
	case *syntax.Selector:
		name = head.Name
		switch owner := head.X.(type) {
		case *syntax.Ident:
			typ = c.typeNamed(owner.Name)
		case *syntax.TypeHead:
			typ = c.resolveType(owner.Type)
		}
	}
	owner, ok := typ.(*Sealed)
	if !ok {
		return false
	}
	variant := owner.Variant(name)
	if variant == nil || !variant.Positional {
		return false
	}
	pos := literal.Position()
	c.diags.AddCode(pos, "type.variant_payload_form", "%s.%s uses positional payloads; write %s.%s(...) instead of braces", owner.Name, name, owner.Name, name)
	if len(literal.Fields) == 1 && literal.Fields[0].Name == "value" && len(variant.Fields) == 1 {
		if file := c.bindingFiles[pos.File]; file != nil {
			source := newLintSource(file)
			start, end := source.exprRange(literal.Fields[0].Value)
			for _, span := range file.ExpressionSpans {
				if span.Expr == literal.Fields[0].Value {
					if start.Line == 0 || compareEditorPosition(span.Start, start) < 0 {
						start = span.Start
					}
					if end.Line == 0 || compareEditorPosition(span.End, end) > 0 {
						end = span.End
					}
				}
			}
			lo := source.positions[pos]
			for lo < len(source.tokens) && source.tokens[lo].Kind != syntax.LBrace {
				lo++
			}
			if hi, ok := source.pairs[lo]; ok {
				c.payloadBraceFix(pos, source.tokens[lo].Pos, start, end, source.tokens[hi].End)
			}
		}
	}
	if len(literal.Fields) == 1 && literal.Fields[0].Name == "value" && len(variant.Fields) == 1 {
		payload := variant.Fields[0].Type
		if expected, ok := instanceIn(want, genericBaseOrSelf(owner)).(*Sealed); ok {
			payload = expected.Variant(name).Fields[0].Type
		}
		c.exprWant(literal.Fields[0].Value, payload)
	} else {
		c.skipFieldInits(literal)
	}
	return true
}

func (c *checker) payloadBraceFix(pos, open, valueStart, valueEnd, end diag.Pos) {
	c.diags.Suggest(pos, "type.variant_payload_form", end, diag.Fix{Message: "use a positional payload", Edits: []diag.TextEdit{
		{Start: open, End: valueStart, Replacement: "("}, {Start: valueEnd, End: end, Replacement: ")"},
	}})
}

func (c *checker) variantExpectedFix(pos diag.Pos, owner string, sealed *Sealed, name string) {
	const code = "type.variant_expected"
	c.diags.AddCode(pos, code, "cannot tell which %s type %s.%s is here; give it an expected type or write explicit type arguments on %s", owner, owner, name, owner)
	file := c.bindingFiles[pos.File]
	if file == nil {
		return
	}
	source := newLintSource(file)
	index, ok := source.positions[pos]
	if !ok {
		return
	}
	// Expression diagnostics point at the variant name; patterns point at
	// the owner. Find the owner/variant separator in either source form.
	if index > 0 && source.tokens[index-1].Kind == syntax.Dot {
		index--
	} else {
		for index+1 < len(source.tokens) && !(source.tokens[index].Kind == syntax.Dot && source.tokens[index+1].Text == name) {
			index++
		}
	}
	if index < 1 || index >= len(source.tokens) {
		return
	}
	insert := source.tokens[index-1].End
	var args []string
	for range sealed.TypeParams {
		args = append(args, "_")
	}
	c.diags.Suggest(pos, code, source.tokens[index+1].End, diag.Fix{Message: "add explicit type arguments to " + owner, RequiresInput: true, Edits: []diag.TextEdit{{Start: insert, End: insert, Replacement: "[" + strings.Join(args, ", ") + "]"}}})
}

func (c *checker) constructorArgumentsFix(pos diag.Pos, head syntax.Expr, args []Type) {
	if selector, ok := head.(*syntax.Selector); ok {
		head = selector.X
	}
	owner, ok := head.(*syntax.Ident)
	if !ok {
		return
	}
	file := c.bindingFiles[pos.File]
	if file == nil {
		return
	}
	source := newLintSource(file)
	index, ok := source.positions[owner.Pos]
	if !ok {
		return
	}
	end := source.tokens[index].End
	// Imported owners are represented by a qualified Ident.
	for range strings.Count(owner.Name, ".") {
		index += 2
		if index >= len(source.tokens) {
			return
		}
		end = source.tokens[index].End
	}
	var text []string
	input := false
	for _, arg := range args {
		arg = c.zonk(arg)
		if c.open(arg) {
			text = append(text, "_")
			input = true
		} else {
			text = append(text, TypeText(arg, c.pkg))
		}
	}
	c.diags.Suggest(pos, "type.constructor_inference", end, diag.Fix{Message: "add explicit owner type arguments", RequiresInput: input, Edits: []diag.TextEdit{{Start: end, End: end, Replacement: "[" + strings.Join(text, ", ") + "]"}}})
}
