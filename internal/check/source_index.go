package check

import (
	"reflect"
	"slices"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Symbol identifies a source declaration. Definition points at its identifier,
// so independently checked import graphs agree on the same source identity.
type Symbol struct {
	Name, Kind, Package, Container string
	Declaration, Definition, End   diag.Pos
	Exported                       bool
}

// SourceReference is an owned source range and its checked declaration identity.
// Prefix/Suffix expand shorthand patterns without renaming their bound variable.
type SourceReference struct {
	Start, End, Definition diag.Pos
	Name                   string
	Declaration            bool
	Prefix, Suffix         string
}

type SymbolIndex struct {
	symbols    []Symbol
	references []SourceReference
}

func (index *SymbolIndex) Symbols() []Symbol                { return slices.Clone(index.symbols) }
func (index *SymbolIndex) AllReferences() []SourceReference { return slices.Clone(index.references) }
func (index *SymbolIndex) References(def diag.Pos) []SourceReference {
	var out []SourceReference
	for _, ref := range index.references {
		if ref.Definition == def {
			out = append(out, ref)
		}
	}
	return out
}
func (index *SymbolIndex) At(pos diag.Pos) *SourceReference {
	for _, ref := range index.references {
		if ref.Start.File == pos.File && ref.Start.Line == pos.Line && ref.Start.Col <= pos.Col && pos.Col < ref.End.Col {
			result := ref
			return &result
		}
	}
	return nil
}

// BuildSourceIndex adapts existing checker metadata. No name lookup occurs in
// the protocol adapter, and the resulting index retains no compiler graph.
func BuildSourceIndex(files []*syntax.File, info *Info) *SymbolIndex {
	b := &sourceIndexBuilder{files: map[string]*sourceIndexFile{}, definitions: map[diag.Pos]Symbol{}, refs: map[SourceReference]bool{}}
	for _, file := range files {
		tokens, _ := syntax.Lex(file.Path, []byte(file.Source), &diag.List{})
		b.files[file.Path] = &sourceIndexFile{file: file, tokens: tokens, lines: strings.Split(file.Source, "\n")}
	}
	for _, file := range files {
		b.declarations(file, info)
	}
	var bindings func(*Pat)
	bindings = func(pat *Pat) {
		if pat == nil {
			return
		}
		if pat.Bind != "" {
			if file := b.files[sourceNodePosition(pat.bindNode).File]; file != nil {
				b.declaration(file.file, sourceNodePosition(pat.bindNode), pat.Bind, "variable", "")
			}
		}
		bindings(pat.Sub)
		bindings(pat.Rest)
		for _, field := range pat.Fields {
			bindings(field.Pat)
		}
		for _, element := range pat.Elems {
			bindings(element)
		}
	}
	for _, pat := range info.armPats {
		bindings(pat)
	}
	for _, pat := range info.tuplePats {
		bindings(pat)
	}
	for _, pat := range info.loopPats {
		bindings(pat)
	}
	for pos, def := range info.sourceDefinitions {
		b.referenceNamed(pos, info.sourceNames[pos], def)
	}
	ambientDefs := map[any]diag.Pos{}
	for _, fn := range info.FuncOf {
		for _, need := range fn.Needs {
			if need.Decl == nil || need.Ambient == nil {
				continue
			}
			ambientDefs[need.Decl] = need.Ambient.Decl.Pos
			b.referenceNamed(need.Decl.Pos, need.Decl.Name, need.Ambient.Decl.Pos)
		}
	}
	for binding, ambient := range info.withAmbients {
		ambientDefs[binding] = ambient.Decl.Pos
		b.referenceNamed(binding.Pos, binding.Name, ambient.Decl.Pos)
	}
	for ident, def := range info.defs {
		pos := sourceNodePosition(def)
		if ambient, ok := ambientDefs[def]; ok {
			pos = ambient
		}
		b.referenceNamed(ident.Pos, ident.Name, pos)
	}
	// Unused mocks still contain source references without lowered calls.

	for statement, fn := range info.mocks {
		b.function(statement.Target, fn.MockOf)
	}
	for expr, instance := range info.funcRefs {
		b.function(expr, instance.Func)
	}
	for call, fn := range info.callFuncs {
		b.function(call.Fun, fn)
		for _, argument := range call.Arguments {
			if argument.Name == "" {
				continue
			}
			for i, param := range fn.Decl.Params {
				if param.Name != argument.Name {
					continue
				}
				def := param.Pos
				if fn.Decl.Constructor != nil {
					if record, ok := fn.Result.(*Record); ok && i < len(record.Fields) {
						def = record.Fields[i].Decl.Pos
					}
				}
				b.reference(argument.Pos, def, "", "")
			}
		}
	}
	for expr := range info.types {
		switch expr := expr.(type) {
		case *syntax.Selector:
			if variant := info.selectorVariants[expr]; variant != nil {
				b.reference(expr.Pos, variant.Parent.Decl.Variants[variant.Index].Pos, "", "")
			} else if record, ok := info.types[expr.X].(*Record); ok {
				if field := record.Field(expr.Name); field != nil && field.Decl != nil {
					b.reference(expr.Pos, field.Decl.Pos, "", "")
				}
			}
		case *syntax.Copy:
			current, _ := info.types[expr.X].(*Record)
			for _, update := range expr.Updates {
				owner := current
				positions := b.pathPositions(update.Pos, update.Path)
				for i, name := range update.Path {
					if owner == nil {
						break
					}
					field := owner.Field(name)
					if field == nil || field.Decl == nil {
						break
					}
					if i < len(positions) {
						b.reference(positions[i], field.Decl.Pos, "", "")
					}
					owner, _ = field.Type.(*Record)
				}
			}
		}
	}
	for expr, variant := range info.contextVariants {
		if variant != nil && expr.Name != "" {
			b.contextVariant(expr, variant)
		}
	}
	for literal, target := range info.recordTargets {
		var fields []*Field
		switch target := target.(type) {
		case *Record:
			fields = target.Fields
		case *Variant:
			fields = target.Fields
			switch head := literal.Type.(type) {
			case *syntax.Selector:
				b.reference(head.Pos, target.Parent.Decl.Variants[target.Index].Pos, "", "")
			case *syntax.ContextName:
				b.contextVariant(head, target)
			}
		}
		if literal.Positional {
			continue
		}
		for _, initializer := range literal.Fields {
			if field := findField(fields, initializer.Name); field != nil && field.Decl != nil {
				b.reference(initializer.Pos, field.Decl.Pos, "", "")
			}
		}
	}
	for arm, pat := range info.armPats {
		b.pattern(arm.Pattern, pat)
	}
	for binding, pat := range info.tuplePats {
		b.pattern(binding.Pattern, pat)
	}
	for loop, pat := range info.loopPats {
		b.pattern(loop.Pattern, pat)
	}
	for source, pattern := range info.patternTests {
		b.pattern(source.Pattern, pattern)
	}
	var out SymbolIndex
	for _, symbol := range b.definitions {
		out.symbols = append(out.symbols, symbol)
	}
	for ref := range b.refs {
		out.references = append(out.references, ref)
	}
	slices.SortFunc(out.symbols, func(a, b Symbol) int { return sourcePositionCompare(a.Definition, b.Definition) })
	slices.SortFunc(out.references, func(a, b SourceReference) int {
		if c := sourcePositionCompare(a.Start, b.Start); c != 0 {
			return c
		}
		if a.Declaration != b.Declaration {
			if a.Declaration {
				return -1
			}
			return 1
		}
		return sourcePositionCompare(a.Definition, b.Definition)
	})
	return &out
}

type sourceIndexFile struct {
	file   *syntax.File
	tokens []syntax.Token
	lines  []string
}
type sourceIndexBuilder struct {
	files       map[string]*sourceIndexFile
	definitions map[diag.Pos]Symbol
	refs        map[SourceReference]bool
}

func sourcePositionCompare(a, b diag.Pos) int {
	if c := strings.Compare(a.File, b.File); c != 0 {
		return c
	}
	if a.Line != b.Line {
		return a.Line - b.Line
	}
	return a.Col - b.Col
}
func sourceNodePosition(node any) diag.Pos {
	switch n := node.(type) {
	case *syntax.Param:
		return n.Pos
	case *syntax.Binding:
		return n.Pos
	case *syntax.ScopeExpr:
		return n.Pos
	case *syntax.VariantPat:
		return n.Pos
	case *syntax.TypePat:
		return n.Pos
	case *syntax.FieldPat:
		return n.Pos
	case *syntax.ListPat:
		return n.RestPos
	case *syntax.MockStmt:
		return n.Pos
	case *syntax.For:
		return n.NamePos
	case *carryNode:
		return sourceNodePosition(n.origin)
	}
	return diag.Pos{}
}
func (b *sourceIndexBuilder) declaration(file *syntax.File, pos diag.Pos, name, kind, container string) {
	if name == "" || strings.HasPrefix(name, "_") || pos.File == "" {
		return
	}
	source := b.files[pos.File]
	if source == nil {
		return
	}
	start := pos
	depth := 0
	found := false
	for _, token := range source.tokensAt(pos) {
		switch token.Kind {
		case syntax.LParen, syntax.LBrack:
			depth++
		case syntax.RParen, syntax.RBrack:
			depth--
		}
		if token.Kind == syntax.TIdent && token.Text == name && depth == 0 {
			start = token.Pos
			found = true
			break
		}
		if token.Kind == syntax.EOF || depth == 0 && (token.Kind == syntax.Semi || token.Kind == syntax.LBrace || token.Kind == syntax.Assign) {
			break
		}
	}
	if !found {
		return
	}
	end := start
	end.Col += len(name)
	symbol := Symbol{Name: name, Kind: kind, Package: file.Package, Container: container, Declaration: pos, Definition: start, End: end, Exported: Exported(name)}
	if previous, ok := b.definitions[pos]; !ok || previous.Kind == "parameter" {
		b.definitions[pos] = symbol
	}
	b.refs[SourceReference{Start: start, End: end, Definition: start, Name: name, Declaration: true}] = true
}
func (b *sourceIndexBuilder) reference(pos, rawDef diag.Pos, prefix, suffix string) {
	symbol, ok := b.definitions[rawDef]
	if !ok {
		return
	}
	file := b.files[pos.File]
	if file == nil || pos.Line < 1 || pos.Line > len(file.lines) || pos.Col < 1 {
		return
	}
	// Source nodes identify exact tokens; qualified names arrive through
	// referenceNamed. The fallback also exposes interpolation-hole tokens.
	start := pos
	found := false
	tokens := file.tokensAt(pos)
	for i := 0; i < len(tokens); i += 2 {
		token := tokens[i]
		if token.Kind != syntax.TIdent {
			break
		}
		if token.Text == symbol.Name {
			start = token.Pos
			found = true
			break
		}
		if i+1 >= len(tokens) || tokens[i+1].Kind != syntax.Dot {
			break
		}
	}
	if !found {
		return
	}
	end := start
	end.Col += len(symbol.Name)
	b.refs[SourceReference{Start: start, End: end, Definition: symbol.Definition, Name: symbol.Name, Prefix: prefix, Suffix: suffix}] = true
}
func (b *sourceIndexBuilder) function(expr syntax.Expr, fn *Func) {
	if fn == nil || fn.Decl == nil {
		return
	}
	switch expr := expr.(type) {
	case *syntax.Ident:
		b.referenceNamed(expr.Pos, expr.Name, fn.Decl.Pos)
	case *syntax.Selector:
		b.reference(expr.Pos, fn.Decl.Pos, "", "")
	}
}

func (b *sourceIndexBuilder) declarations(file *syntax.File, info *Info) {
	add := func(pos diag.Pos, name, kind, container string) { b.declaration(file, pos, name, kind, container) }
	for _, typ := range file.Types {
		add(typ.Pos, typ.Name, "type", "")
		for _, field := range typ.Fields {
			add(field.Pos, field.Name, "field", typ.Name)
		}
		for _, variant := range typ.Variants {
			add(variant.Pos, variant.Name, "variant", typ.Name)
			for _, field := range variant.Fields {
				add(field.Pos, field.Name, "field", typ.Name+"."+variant.Name)
			}
		}
	}
	for _, fn := range file.Funcs {
		if fn.ScriptMain {
			continue
		}
		kind := "function"
		if fn.IsPred {
			kind = "predicate"
		}
		if fn.IsMethod {
			kind = "method"
		}
		add(fn.Pos, fn.Name, kind, "")
	}
	for _, helper := range file.DeriveHelpers {
		add(helper.Pos, helper.Name, "function", "")
	}
	for _, template := range file.Templates {
		add(template.Pos, template.Name, "instance", "")
		for _, method := range template.Methods {
			add(method.Pos, method.Name, "method", template.Name)
		}
	}
	for _, class := range file.Classes {
		add(class.Pos, class.Name, "class", "")
		for _, fn := range class.Methods {
			add(fn.Pos, fn.Name, "method", class.Name)
		}
	}
	for _, ambient := range file.Ambients {
		add(ambient.Pos, ambient.Name, "ambient", "")
	}
	for binding := range info.bindings {
		if binding.Pos.File == file.Path {
			kind := "variable"
			if binding.Package {
				kind = "value"
			}
			add(binding.Pos, binding.Name, kind, "")
		}
	}
	for _, instance := range file.Instances {
		add(instance.Pos, instance.Name, "instance", "")
		for _, fn := range instance.Methods {
			add(fn.Pos, fn.Name, "method", instance.Name)
		}
	}
	for _, bundle := range file.Bundles {
		add(bundle.Pos, bundle.Name, "instances", "")
	}

	sourceWalk(reflect.ValueOf(file), func(node any) {
		switch n := node.(type) {
		case *syntax.Binding:
			kind := "variable"
			if n.Package {
				kind = "value"
			}
			add(n.Pos, n.Name, kind, "")
		case *syntax.Param:
			add(n.Pos, n.Name, "parameter", "")
		case *syntax.TypeParam:
			add(n.Pos, n.Name, "typeParameter", "")
		case *syntax.ScopeExpr:
			add(n.Pos, n.Name, "variable", "")
		case *syntax.MockStmt:
			add(n.Pos, n.Name, "variable", "")
		case *syntax.For:
			if n.Items != nil {
				add(n.NamePos, n.Name, "variable", "")
			}
			for _, b := range n.Init {
				add(b.Pos, b.Name, "variable", "")
			}
		}
	})
	for ident, node := range info.defs {
		if ident.Pos.File == file.Path {
			pos := sourceNodePosition(node)
			name := ident.Name
			if n, ok := node.(*syntax.FieldPat); ok {
				name = n.Field
			}
			add(pos, name, "variable", "")
		}
	}
}

func sourceWalk(value reflect.Value, visit func(any)) {
	seen := make(map[any]bool)
	var walk func(reflect.Value)
	walk = func(value reflect.Value) {
		switch value.Kind() {
		case reflect.Pointer:
			if value.IsNil() {
				return
			}
			node := value.Interface()
			if seen[node] {
				return
			}
			seen[node] = true
			visit(node)
			walk(value.Elem())
		case reflect.Interface:
			if !value.IsNil() {
				walk(value.Elem())
			}
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(value.Type()) {
				walk(value.Field(index))
			}
		case reflect.Slice:
			for i := 0; i < value.Len(); i++ {
				walk(value.Index(i))
			}
		}
	}
	walk(value)
}

func (b *sourceIndexBuilder) pattern(raw syntax.Pattern, pat *Pat) {
	if pat == nil {
		return
	}
	switch raw := raw.(type) {
	case *syntax.VariantPat:
		var fields []*Field
		target := pat
		if target.Sub != nil {
			target = target.Sub
		}
		if target.Variant != nil {
			v := target.Variant
			fields = v.Fields
			pos := raw.Pos
			if raw.Context {
				pos = raw.NamePos
			}
			written := strings.Join(raw.Path, ".")
			if raw.Owner != nil {
				pos, written = raw.NamePos, v.Name
			}
			b.referenceNamed(pos, written, v.Parent.Decl.Variants[v.Index].Pos)
		} else if record, ok := target.Type.(*Record); ok {
			fields = record.Fields
		}
		for i, element := range raw.Elems {
			if i < len(target.Fields) {
				b.pattern(element, target.Fields[i].Pat)
			}
		}
		for i, field := range raw.Fields {
			definition := findField(fields, field.Field)
			if definition != nil && definition.Decl != nil {
				suffix := ""
				if field.Pattern == nil {
					suffix = ": " + field.Field
				}
				b.reference(field.Pos, definition.Decl.Pos, "", suffix)
			}
			if field.Pattern == nil {
				if local, ok := b.definitions[field.Pos]; ok {
					ref := SourceReference{Start: local.Definition, End: local.End, Definition: local.Definition, Name: local.Name, Declaration: true}
					delete(b.refs, ref)
					ref.Prefix = field.Field + ": "
					b.refs[ref] = true
				}
			}
			if i < len(target.Fields) && field.Pattern != nil {
				b.pattern(field.Pattern, target.Fields[i].Pat)
			}
		}
	case *syntax.TuplePat:
		for i, element := range raw.Elems {
			if i < len(pat.Fields) {
				b.pattern(element, pat.Fields[i].Pat)
			}
		}
	case *syntax.ListPat:
		for i, element := range raw.Elems {
			if i < len(pat.Elems) {
				b.pattern(element, pat.Elems[i])
			}
		}
	}
}

func (c *checker) noteSourceType(pos diag.Pos, name string) {
	if pos.File == "" {
		return
	}
	c.info.sourceNames[pos] = name
	if param := c.typeParams[name]; param != nil && param.Decl != nil {
		c.info.sourceDefinitions[pos] = param.Decl.Pos
		return
	}
	if entry := c.lookupType(name); entry != nil {
		c.info.sourceDefinitions[pos] = entry.decl.Pos
	}
}

func (b *sourceIndexBuilder) pathPositions(start diag.Pos, names []string) []diag.Pos {
	file := b.files[start.File]
	if file == nil || start.Line < 1 || start.Line > len(file.lines) || start.Col < 1 {
		return nil
	}
	tokens := file.tokensAt(start)
	var positions []diag.Pos
	for _, token := range tokens {
		if token.Kind == syntax.Dot {
			continue
		}
		if token.Kind == syntax.Semi && token.Text == "" {
			continue
		}
		if len(positions) == len(names) || token.Kind != syntax.TIdent || token.Text != names[len(positions)] {
			break
		}
		positions = append(positions, token.Pos)
	}
	return positions
}

func (b *sourceIndexBuilder) referenceNamed(pos diag.Pos, name string, def diag.Pos) {
	if parts := strings.Split(name, "."); len(parts) > 1 {
		positions := b.pathPositions(pos, parts)
		if len(positions) != len(parts) {
			return
		}
		pos = positions[len(positions)-1]
	}
	b.reference(pos, def, "", "")
}

func (b *sourceIndexBuilder) contextVariant(expr *syntax.ContextName, variant *Variant) {
	positions := b.pathPositions(expr.Pos, []string{expr.Name})
	if len(positions) == 1 {
		b.reference(positions[0], variant.Parent.Decl.Variants[variant.Index].Pos, "", "")
	}
}

// Most nodes share the outer token stream. Only interpolation holes need
// separate lexing, bounded by their enclosing token rather than the file tail.
func (file *sourceIndexFile) tokensAt(pos diag.Pos) []syntax.Token {
	index, exact := slices.BinarySearchFunc(file.tokens, pos, func(token syntax.Token, pos diag.Pos) int { return sourcePositionCompare(token.Pos, pos) })
	if exact {
		return file.tokens[index:]
	}
	offset := file.offset(pos)
	if offset < 0 {
		return nil
	}
	end := len(file.file.Source)
	if index > 0 {
		token := file.tokens[index-1]
		if token.Kind == syntax.TInterp && sourcePositionCompare(pos, token.End) < 0 {
			end = file.offset(token.End)
		}
	}
	if end < offset {
		return nil
	}
	tokens, _ := syntax.Lex(pos.File, []byte(file.file.Source[offset:end]), &diag.List{})
	for i := range tokens {
		if tokens[i].Pos.Line == 1 {
			tokens[i].Pos.Col += pos.Col - 1
		}
		if tokens[i].End.Line == 1 {
			tokens[i].End.Col += pos.Col - 1
		}
		tokens[i].Pos.Line += pos.Line - 1
		tokens[i].End.Line += pos.Line - 1
	}
	return tokens
}
func (file *sourceIndexFile) offset(pos diag.Pos) int {
	if pos.Line < 1 || pos.Line > len(file.lines) || pos.Col < 1 || pos.Col > len(file.lines[pos.Line-1])+1 {
		return -1
	}
	offset := pos.Col - 1
	for i := 0; i < pos.Line-1; i++ {
		offset += len(file.lines[i]) + 1
	}
	return offset
}
