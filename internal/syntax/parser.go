package syntax

import (
	"errors"
	"fmt"
	goparser "go/parser"
	goscanner "go/scanner"
	gotoken "go/token"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
)

// Parse parses one source file. Syntax errors are added to diags; the
// returned file holds whatever could be parsed.
func Parse(path string, src []byte, diags *diag.List) *File {
	toks, comments := Lex(path, src, diags)
	return parse(path, string(src), toks, comments, diags, false)
}

// ParseScript accepts an explicit main or top-level statements in an implicit main.
func ParseScript(path string, src []byte, diags *diag.List) *File {
	toks, comments := Lex(path, src, diags)
	return parseMode(path, string(src), toks, comments, diags, false, true)
}

// ParseExpression parses exactly one expression, for compiler-backed queries.
func ParseExpression(path string, src []byte, diags *diag.List) (out Expr) {
	toks, comments := Lex(path, src, diags)
	var spans []ExpressionSpan
	var operators []diag.Pos
	p := &parser{toks: toks, comments: comments, diags: diags, imports: map[string]bool{}, spans: &spans, spanSeen: map[Expr][]SourceSpan{}, patternTestOperators: &operators}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			out = nil
		}
	}()
	out = p.expr()
	p.skipSemis()
	p.expect(EOF, "after the expression")
	return out
}

func parse(path, src string, toks []Token, comments []Comment, diags *diag.List, compiler bool) *File {
	return parseMode(path, src, toks, comments, diags, compiler, strings.HasPrefix(src, "#!"))
}

func parseMode(path, src string, toks []Token, comments []Comment, diags *diag.List, compiler, script bool) *File {
	f := &File{Path: path, Source: src, Comments: comments, Script: script}
	p := &parser{iterationOperators: &f.IterationOperators, headParentheses: &f.HeadParentheses, patternTestOperators: &f.PatternTestOperators, toks: toks, comments: comments, diags: diags, imports: map[string]bool{}, compiler: compiler, spans: &f.ExpressionSpans, spanSeen: map[Expr][]SourceSpan{}}
	var statements []Stmt
	var firstStatement diag.Pos
	// Imports come first.
	for {
		p.skipSemis()
		if !p.at(TIdent) || p.tok().Text != "import" || (p.peekKind() != TString && p.peekKind() != TIdent) {
			break
		}
		if imp := p.importDecl(); imp != nil {
			f.Imports = append(f.Imports, imp)
			p.imports[imp.Name] = true
		}
	}
	// Then the instances used.
	for {
		p.skipSemis()
		if !p.at(TIdent) || p.tok().Text != "use" || p.peekKind() != TIdent {
			break
		}
		if u := p.useDecl(); u != nil {
			f.Uses = append(f.Uses, u)
		}
	}
	for {
		p.skipSemis()
		if p.at(EOF) {
			break
		}
		switch {
		case p.at(TIdent) && p.tok().Text == "derive" && (p.peekKind() == KwFn || p.peekKind() == TIdent && p.toks[p.i+1].Text == "instance"):
			p.next()
			p.inDerivation = true
			if p.at(KwFn) {
				if fn := p.funcDecl(); fn != nil {
					fn.Derivation = true
					f.DeriveHelpers = append(f.DeriveHelpers, fn)
				}
			} else if id := p.instanceDecl(); id != nil {
				id.Derivation = true
				f.Templates = append(f.Templates, id)
			}
			p.inDerivation = false
		case p.at(TIdent) && p.tok().Text == "derive" && p.peekKind() == TIdent:
			if d := p.deriveDecl(); d != nil {
				f.Derives = append(f.Derives, d)
			}
		case p.at(TIdent) && p.tok().Text == "class" && p.peekKind() == TIdent:
			if cd := p.classDecl(); cd != nil {
				f.Classes = append(f.Classes, cd)
			}
		case p.at(TIdent) && p.tok().Text == "instance" && p.peekKind() == TIdent:
			if id := p.instanceDecl(); id != nil {
				f.Instances = append(f.Instances, id)
				f.Funcs = append(f.Funcs, id.Methods...)
			}
		case p.at(TIdent) && p.tok().Text == "providers" && p.peekKind() == TIdent:
			p.legacyProviders()
		case p.at(TIdent) && p.tok().Text == "instances" && p.peekKind() == TIdent:
			if b := p.bundleDecl(); b != nil {
				f.Bundles = append(f.Bundles, b)
			}
		case p.at(TIdent) && p.tok().Text == "use" && p.peekKind() == TIdent:
			p.errorf(p.tok().Pos, "use must come after the imports, before the declarations")
			p.syncTopLevel()
		case p.at(TIdent) && p.tok().Text == "import" && (p.peekKind() == TString || p.peekKind() == TIdent):
			p.errorf(p.tok().Pos, "imports must come before the declarations")
			p.syncTopLevel()
		case p.at(KwFn) || p.at(KwPred):
			if fn := p.funcDecl(); fn != nil {
				f.Funcs = append(f.Funcs, fn)
			}
		case p.at(KwRule):
			if r := p.ruleDecl(); r != nil {
				f.Rules = append(f.Rules, r)
			}
		case p.at(KwType):
			if td := p.typeDecl(); td != nil {
				f.Types = append(f.Types, td)
			}
		case p.at(TIdent) && p.tok().Text == MetadataFamilyKeyword && p.peekKind() == KwType:
			keyword := p.next().Pos
			if td := p.typeDecl(); td != nil {
				td.MetadataFamily, td.MetadataPos = true, keyword
				f.Types = append(f.Types, td)
			}
		case p.at(TIdent) && (p.tok().Text == "ambient" && p.peekKind() == TIdent || p.atAmbientMarker()):
			if ad := p.ambientDecl(); ad != nil {
				f.Ambients = append(f.Ambients, ad)
			}
		case p.at(TIdent) && p.tok().Text == "test" && p.peekKind() == TString:
			if td := p.testDecl(); td != nil {
				f.Tests = append(f.Tests, td)
			}
		case !script && p.atAsyncBinding():
			p.errorf(p.tok().Pos, "package async bindings are not supported; use an async local binding inside a function")
			p.syncTopLevel()
		case p.at(TIdent) && p.tok().Text == "lazy" && (p.peekKind() == TIdent || p.peekKind() == Underscore):
			if binding := p.packageLazyBinding(); binding != nil {
				f.Bindings = append(f.Bindings, binding)
			}
		case !script && p.at(TIdent) && (p.peekKind() == Assign || p.peekKind() == Colon):
			if binding := p.packageBinding(); binding != nil {
				f.Bindings = append(f.Bindings, binding)
			}
		case script:
			pos := p.tok().Pos
			if stmt := p.scriptStatement(); stmt != nil {
				if len(statements) == 0 {
					firstStatement = pos
				}
				statements = append(statements, stmt)
			}
		default:
			p.errorf(p.tok().Pos, "expected a declaration ('fn', 'pred', 'rule', 'type', 'ambient', 'class', 'instance', or 'test'), found %s", p.tok().Kind)
			p.syncTopLevel()
		}
	}
	if script {
		explicitMain := false
		for _, fn := range f.Funcs {
			if fn.Name == "main" && !fn.IsMethod {
				explicitMain = true
				if len(statements) > 0 {
					p.diags.AddCode(fn.Pos, "script.mixed-entrypoints", "a script cannot combine fn main() with top-level statements (first statement at %s); move the statements into main or remove main", firstStatement)
				}
			}
		}
		if !explicitMain {
			pos := diag.Pos{File: path, Line: 1, Col: 1}
			body := &Block{Pos: pos, End: p.tok().Pos, Stmts: statements}
			f.Funcs = append(f.Funcs, &FuncDecl{Pos: pos, Name: "main", Body: body, ScriptMain: true})
		}
	}
	return f
}

func (p *parser) deriveDecl() (decl *DeriveDecl) {
	defer p.recoverDecl(func() { decl = nil })
	pos := p.next().Pos
	class := p.expect(TIdent, "(class name)")
	decl = &DeriveDecl{Pos: pos, ClassPos: class.Pos, Class: p.qualify(class)}
	p.expect(KwFor, "after the class to derive")
	decl.Type = p.typeExpr()
	decl.End = p.tok().Pos
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after derive declaration, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return decl
}

func (p *parser) packageLazyBinding() (binding *Binding) {
	defer p.recoverDecl(func() { binding = nil })
	binding = p.lazyBinding()
	binding.Package = true
	return binding
}

func (p *parser) packageBinding() (binding *Binding) {
	defer p.recoverDecl(func() { binding = nil })
	name := p.next()
	binding = &Binding{Pos: name.Pos, Name: name.Text, Package: true}
	if p.at(Colon) {
		p.next()
		binding.Type = p.typeExpr()
	}
	p.expect(Assign, "after the package binding's name or type")
	binding.Value = p.expr()
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after the package binding, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return binding
}

func (p *parser) lazyBinding() *Binding {
	pos := p.next().Pos
	name := p.expect(TIdent, "after lazy (a single binding name)")
	binding := &Binding{Pos: name.Pos, Name: name.Text, Lazy: true, LazyPos: pos}
	if p.at(Colon) {
		p.next()
		binding.Type = p.typeExpr()
	}
	p.expect(Assign, "after the lazy binding's name or type")
	binding.Value = p.expr()
	return binding
}

// bailout aborts parsing of the current declaration after an error.
type bailout struct{}

type parser struct {
	inDerivation                bool
	typeDepth, patternTypeDepth int
	spans                       *[]ExpressionSpan
	spanSeen                    map[Expr][]SourceSpan
	compiler                    bool
	comments                    []Comment
	toks                        []Token
	i                           int
	diags                       *diag.List
	testingPattern              bool
	patternTestOperators        *[]diag.Pos
	headParentheses             *[]SourceSpan
	iterationOperators          *[]diag.Pos
	// noLambda is set while parsing rule premises, where `x =>` ends
	// the premises instead of starting a lambda.
	noLambda bool
	// imports holds the names of the file's imported packages.
	imports map[string]bool
	// noRecordLit is set in control heads and scope policies, where a
	// brace after a name starts the body rather than a record literal.
	noRecordLit bool
}

// importDecl parses `import "path"` or `import name "path"`.
func (p *parser) importDecl() (imp *Import) {
	defer p.recoverDecl(func() { imp = nil })
	pos := p.next().Pos
	imp = &Import{Pos: pos}
	if p.at(TIdent) {
		imp.Name = p.next().Text
	}
	t := p.expect(TString, "(the package's import path)")
	imp.End = t.End
	path, err := strconv.Unquote(t.Text)
	if err != nil || path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.Contains(path, "\\") {
		p.errorf(t.Pos, "invalid import path %s", t.Text)
		panic(bailout{})
	}
	imp.Path = path
	if imp.Name == "" {
		imp.Name = path[strings.LastIndex(path, "/")+1:]
	}
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after the import, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return imp
}

// qualify reads `pkg.name`, where pkg (the token just read) names an
// imported package, as the one name "pkg.name".
func (p *parser) qualify(t Token) string {
	if p.imports[t.Text] && p.at(Dot) && p.peekKind() == TIdent {
		p.next()
		return t.Text + "." + p.next().Text
	}
	return t.Text
}

func (p *parser) tok() Token     { return p.toks[p.i] }
func (p *parser) at(k Kind) bool { return p.toks[p.i].Kind == k }
func (p *parser) peekKind() Kind { return p.toks[min(p.i+1, len(p.toks)-1)].Kind }
func (p *parser) next() Token    { t := p.toks[p.i]; p.i = min(p.i+1, len(p.toks)-1); return t }
func (p *parser) skipSemis() {
	for p.at(Semi) {
		p.next()
	}
}
func (p *parser) errorf(pos diag.Pos, format string, args ...any) {
	p.diags.AddCode(pos, "syntax.error", format, args...)
}

// skipNewlines skips newline-terminators, which are meaningless inside
// parentheses (e.g. arguments split over several lines).
func (p *parser) skipNewlines() {
	for p.at(Semi) && p.tok().Text == "\n" {
		p.next()
	}
}

func (p *parser) expect(k Kind, what string) Token {
	if !p.at(k) {
		p.errorf(p.tok().Pos, "expected %s %s, found %s", k, what, p.tok().Kind)
		panic(bailout{})
	}
	return p.next()
}

// syncTopLevel skips ahead to the next declaration at the start of a line.
func (p *parser) syncTopLevel() {
	for !p.at(EOF) {
		if (p.at(KwFn) || p.at(KwType) || p.at(TIdent) && (p.tok().Text == "derive" && p.peekKind() == TIdent || p.tok().Text == MetadataFamilyKeyword && p.peekKind() == KwType || p.tok().Text == "providers" && p.peekKind() == TIdent || p.peekKind() == Assign || p.peekKind() == Colon || p.tok().Text == "lazy" && p.peekKind() == TIdent)) && (p.i == 0 || p.toks[p.i-1].Kind == Semi) {
			return
		}
		p.next()
	}
}

// recoverDecl handles a bailout inside a declaration: skip to the next
// declaration and report that nothing was parsed.
func (p *parser) recoverDecl(failed func()) {
	if r := recover(); r != nil {
		if _, ok := r.(bailout); !ok {
			panic(r)
		}
		failed()
		p.next()
		p.syncTopLevel()
	}
}

// list parses items separated by commas or newlines, up to the closing
// token, which it consumes. A trailing comma is allowed.
func (p *parser) list(closing Kind, what string, item func()) {
	p.skipSemis()
	for !p.at(closing) {
		item()
		sep := false
		if p.at(Comma) {
			p.next()
			sep = true
		}
		if p.at(Semi) {
			p.skipSemis()
			sep = true
		}
		if !sep && !p.at(closing) {
			p.errorf(p.tok().Pos, "expected ',', a newline, or %s after %s, found %s", closing, what, p.tok().Kind)
			panic(bailout{})
		}
	}
	p.next()
}

// legacyProviders recognizes only the removed declaration for migration. It
// produces no AST declaration, exported symbol or assembly contract.
func (p *parser) legacyProviders() {
	defer p.recoverDecl(func() {})
	keyword := p.next()
	name := p.expect(TIdent, "for legacy provider bundle name")
	p.expect(Assign, "after legacy provider bundle name")
	open := p.expect(LBrace, "before legacy provider entries")
	edits := []diag.TextEdit{{Start: keyword.Pos, End: keyword.End, Replacement: ""}, {Start: open.Pos, End: open.End, Replacement: "("}}
	safe, count := true, 0
	names := map[string]bool{}
	for i := p.i; i < len(p.toks) && p.toks[i].Kind == Semi; i++ {
		if p.toks[i].Text == ";" {
			edits = append(edits, diag.TextEdit{Start: p.toks[i].Pos, End: p.toks[i].End, Replacement: ""})
		}
	}
	p.list(RBrace, "legacy provider entry", func() {
		label := p.expect(TIdent, "for legacy provider entry name")
		colon := p.expect(Colon, "after legacy provider entry name")
		provider := p.expr()
		if p.at(Semi) && p.tok().Text == ";" {
			semi := p.tok()
			edits = append(edits, diag.TextEdit{Start: semi.Pos, End: semi.End, Replacement: ","})
		} else if !p.at(Comma) {
			end := p.toks[p.i-1].End
			edits = append(edits, diag.TextEdit{Start: end, End: end, Replacement: ","})
		}
		for i := p.i; i < len(p.toks) && (p.toks[i].Kind == Comma || p.toks[i].Kind == Semi); i++ {
			if p.toks[i].Kind == Semi && p.toks[i].Text == ";" && (i != p.i || !p.at(Semi)) {
				edits = append(edits, diag.TextEdit{Start: p.toks[i].Pos, End: p.toks[i].End, Replacement: ""})
			}
		}
		_, plain := provider.(*Ident)
		safe = safe && plain && !names[label.Text]
		names[label.Text] = true
		count++
		edits = append(edits, diag.TextEdit{Start: label.Pos, End: label.End, Replacement: ""}, diag.TextEdit{Start: colon.Pos, End: colon.End, Replacement: ""})
	})
	close := p.toks[p.i-1]
	edits = append(edits, diag.TextEdit{Start: close.Pos, End: close.End, Replacement: ")"})
	code := "migration.providers"
	p.diags.AddCode(keyword.Pos, code, "providers declarations were removed; use an ordinary tuple package value, for example %s = (newService,). Functions with uncaptured ambient needs must stay inline assembly providers or take explicit parameters: assemble[Service](s, newService). Use test.Swap/SwapAt or a tuple literal for replacements; Swap requires the exact element type, unlike changed-signature specialization. See docs/language/packages.md", name.Text)
	if safe && count > 0 {
		p.diags.Suggest(keyword.Pos, code, close.End, diag.Fix{Message: "replace provider declaration with tuple package value", Edits: edits})
	}
}

func (p *parser) typeDecl() (td *TypeDecl) {
	defer p.recoverDecl(func() { td = nil })
	pos := p.expect(KwType, "").Pos
	name := p.expect(TIdent, "(type name)")
	td = &TypeDecl{Pos: pos, Name: name.Text}
	if p.at(LBrack) {
		p.next()
		p.list(RBrack, "a type parameter", func() {
			t := p.expect(TIdent, "(type parameter name)")
			td.TypeParams = append(td.TypeParams, &TypeParam{Pos: t.Pos, Name: t.Text})
		})
	}
	p.expect(Assign, "after the type name")
	if p.at(TIdent) && p.tok().Text == "private" && p.peekKind() == LBrace {
		td.Private = true
		p.next()
	}
	switch {
	case p.at(KwSealed):
		td.Kind = SealedType
		p.next()
		p.expect(LBrace, "to start the list of variants")
		p.list(RBrace, "a variant", func() {
			vname := p.expect(TIdent, "(variant name)")
			v := &VariantDecl{Pos: vname.Pos, Name: vname.Text, Doc: p.fieldDoc(vname.Pos)}
			if p.at(LBrace) {
				v.Fields = p.fieldDecls()
			} else if p.at(LParen) {
				v.Positional = true
				p.next()
				if p.at(RParen) {
					p.errorf(p.tok().Pos, "a positional variant needs at least one payload type; write %s without parentheses for a fieldless variant", v.Name)
					panic(bailout{})
				}
				p.list(RParen, "a payload type", func() { v.Slots = append(v.Slots, p.typeExpr()) })
			}
			v.TagGroups = p.tagGroups()
			v.Where = p.whereClause()
			td.Variants = append(td.Variants, v)
		})
	case p.at(LBrace):
		td.Kind = RecordType
		td.Fields = p.fieldDecls()
	case p.at(TIdent) && p.tok().Text == "go" && p.peekKind() == TString:
		td.Kind = GoType
		p.next()
		n := p.expect(TString, "(Go type name)")
		name, err := strconv.Unquote(n.Text)
		if err != nil {
			p.errorf(n.Pos, "invalid Go type name: %v", err)
			panic(bailout{})
		}
		td.GoName = &GoBind{Pos: n.Pos, Name: name}
		if p.at(LBrace) {
			td.Kind = RecordType
			td.Fields = p.fieldDecls()
		}
	case p.at(TIdent) && p.tok().Text == "resource" && (p.peekKind() == Semi || p.peekKind() == EOF || p.peekKind() == TIdent):
		// `resource` is a keyword only here.
		td.Kind = ResourceType
		p.next()
		if p.at(TIdent) && p.tok().Text == "go" {
			p.next()
			n := p.expect(TString, "(Go resource type name)")
			name, err := strconv.Unquote(n.Text)
			if err != nil {
				p.errorf(n.Pos, "invalid Go type name: %v", err)
				panic(bailout{})
			}
			td.GoName = &GoBind{Pos: n.Pos, Name: name}
		}
	default:
		td.Kind = AliasType
		td.Alias = p.typeExpr()
	}
	td.TagGroups = p.tagGroups()
	if td.Kind == RecordType || td.Kind == SealedType {
		td.Where = p.whereClause()
	}
	if p.at(TIdent) && p.tok().Text == "derive" && p.peekKind() == LParen {
		td.DerivePos = p.next().Pos
		p.next()
		p.list(RParen, "a class to derive", func() {
			td.Derive = append(td.Derive, p.qualify(p.expect(TIdent, "(class name)")))
		})
	}
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after type declaration, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return td
}

// fieldDecls parses `{ name: Type, ... }`.
func (p *parser) fieldDecls() []*FieldDecl {
	var fields []*FieldDecl
	p.expect(LBrace, "to start the fields")
	p.list(RBrace, "a field", func() {
		var lazy bool
		var lazyPos diag.Pos
		if p.at(TIdent) && p.tok().Text == "lazy" && p.peekKind() == TIdent {
			lazy, lazyPos = true, p.tok().Pos
			p.next()
		}
		fname := p.expect(TIdent, "(field name)")
		p.expect(Colon, "after field name")
		field := &FieldDecl{Lazy: lazy, LazyPos: lazyPos, Pos: fname.Pos, Name: fname.Text, Type: p.typeExpr(), Doc: p.fieldDoc(fname.Pos)}
		if p.at(Assign) {
			p.next()
			field.Default = p.expr()
		}
		field.TagGroups = p.tagGroups()
		fields = append(fields, field)
	})
	return fields
}

// A group starts on the same line as the type/default/payload it follows.
// Newline-separated identifiers in a sealed body remain variant names.
func (p *parser) tagGroups() []*TagGroup {
	var groups []*TagGroup
	for p.at(TIdent) && p.peekKind() == LBrace && p.tok().Pos.Line == p.toks[p.i-1].End.Line {
		name := p.next()
		group := &TagGroup{Pos: name.Pos, Name: name.Text}
		p.next()
		p.list(RBrace, "a tag entry", func() {
			key := p.expect(TIdent, "(tag name)")
			p.expect(Colon, "after the tag name")
			var value Expr
			if group.Name == "go" {
				token := p.expect(TString, "(Go struct tag value)")
				decoded, err := strconv.Unquote(token.Text)
				if err != nil {
					p.errorf(token.Pos, "invalid Go struct tag value: %v", err)
					panic(bailout{})
				}
				value = &StringLit{Pos: token.Pos, Value: decoded}
			} else {
				p.skipNewlines()
				value = p.expr()
			}
			group.Entries = append(group.Entries, &FieldInit{Pos: key.Pos, Name: key.Text, Value: value})
		})
		group.End = p.toks[p.i-1].End
		groups = append(groups, group)
	}
	return groups
}

func (p *parser) funcDecl() (fn *FuncDecl) {
	defer p.recoverDecl(func() { fn = nil })
	return p.funcDeclIn(true, false)
}

// funcDeclIn parses a function declaration; without withBody (a class
// method), just its signature. inBraces (a class's or an instance's
// method) lets a '}' end it, as in `instance i: C[T] { fn f() { ... } }`.
func (p *parser) funcDeclIn(withBody, inBraces bool) *FuncDecl {
	isPred := p.at(KwPred)
	pos := p.next().Pos
	// A method: `fn (xs: List[T]) first[T](): T`.
	var receiver *Param
	if p.at(LParen) && !isPred && withBody && !inBraces {
		p.next()
		rname := p.expect(TIdent, "(the receiver's name)")
		p.expect(Colon, "after the receiver's name")
		receiver = &Param{Pos: rname.Pos, Name: rname.Text, Type: p.typeExpr()}
		p.expect(RParen, "to end the receiver")
	}
	name := p.expect(TIdent, "(function name)")
	fn := &FuncDecl{Pos: pos, Name: name.Text, IsPred: isPred}
	defer func() { fn.End = p.toks[p.i-1].End }()
	if p.at(Assign) {
		p.next()
		target := p.expect(TIdent, "(record name)")
		owner := target.Text
		p.expect(Dot, "before new")
		member := p.expect(TIdent, "(new)")
		if p.at(Dot) {
			owner += "." + member.Text
			p.next()
			member = p.expect(TIdent, "(new)")
		}
		if member.Text != "new" || isPred || receiver != nil || !withBody || inBraces {
			p.errorf(pos, "generated constructors use a top-level fn declaration: fn New = Config.new")
			panic(bailout{})
		}
		fn.Constructor = &TypeExpr{Pos: target.Pos, Name: owner}
		if !p.at(Semi) && !p.at(EOF) {
			p.errorf(p.tok().Pos, "expected end of line after generated constructor declaration")
			panic(bailout{})
		}
		return fn
	}
	fn.TypeParams = p.typeParams()
	if receiver != nil {
		fn.IsMethod = true
		fn.Params = append(fn.Params, receiver)
	}
	p.expect(LParen, "to start the parameter list")
	p.skipNewlines()
	for !p.at(RParen) {
		pname := p.expect(TIdent, "(parameter name)")
		p.expect(Colon, "after parameter name")
		param := &Param{Pos: pname.Pos, Name: pname.Text, Type: p.typeExpr()}
		if p.at(TIdent) && p.tok().Text == "in" {
			param.InPos = p.next().Pos
			param.In = p.expect(TIdent, "(the parameter whose scope it belongs to)").Text
		}
		if p.at(Assign) {
			p.next()
			param.Default = p.expr()
		}
		fn.Params = append(fn.Params, param)
		p.skipNewlines()
		if !p.at(Comma) {
			break
		}
		p.next()
		p.skipNewlines()
	}
	fn.ParamsEnd = p.expect(RParen, "to end the parameter list").Pos
	if p.at(KwWhere) {
		p.next()
		if isPred {
			p.errorf(p.tok().Pos, "predicates do not support function-level where clauses")
		}
		func() {
			saved := p.noRecordLit
			defer func() { p.noRecordLit = saved }()
			p.noRecordLit = true
			fn.Requires = p.requirementGroup()
		}()
	}
	fn.Uses = p.uses()
	fn.Needs = p.needs()
	if isPred {
		// A predicate always returns Bool.
		fn.Result = &TypeExpr{Pos: name.Pos, Name: "Bool"}
		if p.at(Colon) {
			p.errorf(p.tok().Pos, "a pred always returns Bool; leave out the result type")
			panic(bailout{})
		}
	} else if p.at(Colon) {
		p.next()
		fn.Result = p.typeExpr()
	}
	switch {
	case !withBody:
	case p.at(KwUnsafe):
		p.next()
		if p.at(TIdent) && p.tok().Text == "go" {
			// A binding: unsafe go "os.Getenv".
			p.next()
			t := p.expect(TString, "after 'unsafe go' (write `unsafe go { ... }`, or `unsafe go \"pkg.Func\"` to bind a Go function)")
			name, err := strconv.Unquote(t.Text)
			if err != nil || name == "" {
				p.errorf(t.Pos, "invalid Go function name %s", t.Text)
				panic(bailout{})
			}
			fn.GoBind = &GoBind{Pos: t.Pos, Name: name}
			break
		}
		t := p.expect(TGoCode, "after 'unsafe' (write `unsafe go { ... }`)")
		fn.GoBody = p.goCode(t)
	default:
		fn.Body = p.block()
	}
	if !p.at(Semi) && !p.at(EOF) && (!inBraces || !p.at(RBrace)) {
		p.errorf(p.tok().Pos, "expected end of line after function body, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return fn
}

// typeParams parses `[A, B: Show + Eq]`, if present.
func (p *parser) typeParams() []*TypeParam {
	if !p.at(LBrack) {
		return nil
	}
	var out []*TypeParam
	p.next()
	p.list(RBrack, "a type parameter", func() {
		t := p.expect(TIdent, "(type parameter name)")
		tp := &TypeParam{Pos: t.Pos, Name: t.Text}
		if p.at(Colon) {
			p.next()
			for {
				tp.Bounds = append(tp.Bounds, p.qualify(p.expect(TIdent, "(class name)")))
				if !p.at(Plus) {
					break
				}
				p.next()
			}
		}
		out = append(out, tp)
	})
	return out
}

// classDecl parses `class Show[T] { fn show(x: T): String ... }`.
func (p *parser) classDecl() (cd *ClassDecl) {
	defer p.recoverDecl(func() { cd = nil })
	pos := p.next().Pos
	name := p.expect(TIdent, "(class name)")
	cd = &ClassDecl{Pos: pos, Name: name.Text, TypeParams: p.typeParams()}
	p.expect(LBrace, "to start the class's methods")
	for {
		p.skipSemis()
		if p.at(RBrace) {
			p.next()
			break
		}
		if !p.at(KwFn) {
			p.errorf(p.tok().Pos, "expected a method (fn name(...): Type) or '}', found %s", p.tok().Kind)
			panic(bailout{})
		}
		cd.Methods = append(cd.Methods, p.funcDeclIn(false, true))
	}
	return cd
}

// instanceDecl parses `instance name[T: Bound]: Class[Type] { fn ... }`.
func (p *parser) instanceDecl() (id *InstanceDecl) {
	defer p.recoverDecl(func() { id = nil })
	pos := p.next().Pos
	name := p.expect(TIdent, "(instance name)")
	id = &InstanceDecl{Pos: pos, Name: name.Text, TypeParams: p.typeParams()}
	p.expect(Colon, "after the instance's name (write `instance name: Class[Type] { ... }`)")
	head := p.typeAtom()
	if head.Func != nil || len(head.Args) != 1 || len(head.Where) > 0 {
		p.errorf(head.Pos, "expected the class and the type it is an instance for, as in Show[Int]")
		panic(bailout{})
	}
	id.Class, id.ClassPos, id.Type = head.Name, head.Pos, head.Args[0]
	p.expect(LBrace, "to start the instance's methods")
	for {
		p.skipSemis()
		if p.at(RBrace) {
			p.next()
			break
		}
		if !p.at(KwFn) {
			if p.at(TIdent) && p.tok().Text == "metadata" {
				position := p.next().Pos
				key := p.typeExpr()
				p.expect(Assign, "after the metadata type")
				id.Metadata = append(id.Metadata, &InstanceMetadata{Pos: position, Type: key, Value: p.expr()})
				continue
			}
			p.errorf(p.tok().Pos, "expected a method (fn name(...) { ... }) or '}', found %s", p.tok().Kind)
			panic(bailout{})
		}
		m := p.funcDeclIn(true, true)
		m.Instance = id
		id.Methods = append(id.Methods, m)
	}
	return id
}

// bundleDecl parses `instances Name { item, ... }`, where an item is
// what `use` takes.
func (p *parser) bundleDecl() (b *Bundle) {
	defer p.recoverDecl(func() { b = nil })
	pos := p.next().Pos
	b = &Bundle{Pos: pos, Name: p.expect(TIdent, "(name of the set of instances)").Text}
	p.expect(LBrace, "to start the instances")
	for {
		p.skipSemis()
		if p.at(RBrace) {
			p.next()
			return b
		}
		u, ended := p.useItem(p.tok().Pos)
		b.Items = append(b.Items, u)
		if p.at(Comma) {
			p.next()
		} else if !ended && !p.at(Semi) && !p.at(RBrace) {
			p.errorf(p.tok().Pos, "expected ',' or '}' after an instance, found %s", p.tok().Kind)
			panic(bailout{})
		}
	}
}

// useDecl parses `use name`, `use pkg.name`, or `use pkg.*`.
func (p *parser) useDecl() (u *Use) {
	defer p.recoverDecl(func() { u = nil })
	pos := p.next().Pos
	u, ended := p.useItem(pos)
	if ended {
		return u
	}
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after use, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return u
}

// useItem parses `name`, `pkg.name`, or `pkg.*`. ended reports a line
// ending after '*', which does not end a statement in general (a * b
// can span lines) but ends this one.
func (p *parser) useItem(pos diag.Pos) (u *Use, ended bool) {
	t := p.expect(TIdent, "(instance name)")
	u = &Use{Pos: pos, Name: t.Text}
	if p.at(Dot) {
		p.next()
		if p.at(Star) {
			star := p.next()
			u.Name += ".*"
			return u, p.tok().Pos.Line > star.Pos.Line
		}
		u.Name += "." + p.expect(TIdent, "(instance name)").Text
	}
	return u, false
}

// typeExpr parses a type: `Name`, `Name[Args]`, or a union `A | B`.
func (p *parser) typeExpr() *TypeExpr {
	p.typeDepth++
	defer func() { p.typeDepth-- }()
	first := p.constrainedType()
	if !p.at(Pipe) {
		return first
	}
	u := &TypeExpr{Pos: first.Pos, Union: []*TypeExpr{first}}
	for p.at(Pipe) {
		p.next()
		p.skipNewlines()
		u.Union = append(u.Union, p.constrainedType())
	}
	return u
}

// constrainedType parses `T`, or `T where p and q(args)`.
func (p *parser) constrainedType() *TypeExpr {
	t := p.typeAtom()
	t.Where = p.whereClause()
	return t
}

func (p *parser) whereClause() []*PredRef {
	if !p.at(KwWhere) {
		return nil
	}
	var clauses []*PredRef
	p.next()
	for {
		var clause *PredRef
		if p.at(LParen) {
			p.next()
			clause = p.predOr()
			p.expect(RParen, "to end the alternatives")
		} else {
			clause = p.predOr()
			// `p or q and r` could mean two things; ask for parentheses.
			if len(clause.Or) > 0 && (len(clauses) > 0 || p.at(KwAnd)) {
				p.errorf(clause.Pos, "mixing and with or needs parentheses: write (p or q) and r")
			}
		}
		clauses = append(clauses, clause)
		if !p.at(KwAnd) {
			return clauses
		}
		p.next()
	}
}

// predOr parses predicates joined by `or`.
func (p *parser) predOr() *PredRef {
	ref := p.pred()
	for p.at(KwOr) {
		p.next()
		ref.Or = append(ref.Or, p.pred())
	}
	return ref
}

// pred parses one predicate of a where clause: `positive` or
// `between(1, 65535)`.
func (p *parser) pred() *PredRef {
	name := p.expect(TIdent, "(predicate name)")
	ref := &PredRef{Pos: name.Pos, Name: p.qualify(name)}
	if p.at(LParen) {
		p.next()
		p.skipNewlines()
		for !p.at(RParen) {
			ref.Args = append(ref.Args, p.expr())
			p.skipNewlines()
			if !p.at(Comma) {
				break
			}
			p.next()
			p.skipNewlines()
		}
		p.expect(RParen, "to end the predicate's arguments")
	}
	return ref
}

// ambientDecl parses `ambient name: Type`.
func (p *parser) ambientDecl() (ad *AmbientDecl) {
	defer p.recoverDecl(func() { ad = nil })
	ad = &AmbientDecl{Pos: p.tok().Pos}
	for p.at(TIdent) && (p.tok().Text == "logged" || p.tok().Text == "propagated") {
		t := p.next()
		switch {
		case t.Text == "logged" && ad.Logged == nil:
			ad.Logged = &t.Pos
		case t.Text == "propagated" && ad.Propagated == nil:
			p.expect(LParen, "after propagated (the header that carries the value: propagated(\"traceparent\"))")
			h := p.expect(TString, "(the header that carries the value)")
			header, err := strconv.Unquote(h.Text)
			if err != nil {
				p.errorf(h.Pos, "the header name must be a plain string, such as \"traceparent\"")
				panic(bailout{})
			}
			p.expect(RParen, "after the header name")
			ad.Propagated = &Propagated{Pos: t.Pos, Header: header, HeaderPos: h.Pos}
		default:
			p.errorf(t.Pos, "%s is given twice", t.Text)
			panic(bailout{})
		}
	}
	if !p.at(TIdent) || p.tok().Text != "ambient" {
		p.errorf(p.tok().Pos, "expected ambient after the markers logged and propagated, found %s", p.tok().Kind)
		panic(bailout{})
	}
	p.next()
	name := p.expect(TIdent, "(the ambient value's name)")
	p.expect(Colon, "after the ambient value's name")
	ad.Name, ad.Type = name.Text, p.typeExpr()
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after the ambient declaration, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return ad
}

// atAmbientMarker reports whether a marked ambient declaration starts
// here: with `logged` or `propagated` (at the top level, nothing else
// does).
func (p *parser) atAmbientMarker() bool {
	return p.tok().Text == "logged" || p.tok().Text == "propagated"
}

// needs parses `needs traceId + locale?`, if present: the ambient
// values a function reads.
func (p *parser) needs() *Needs {
	if !p.at(TIdent) || p.tok().Text != "needs" || p.peekKind() != TIdent {
		return nil
	}
	n := &Needs{Pos: p.next().Pos}
	for {
		t := p.expect(TIdent, "(ambient value name)")
		need := &Need{Pos: t.Pos, Name: p.qualify(t)}
		if p.at(Dot) {
			p.errorf(t.Pos, "%s is not an imported package", t.Text)
			panic(bailout{})
		}
		n.End = p.toks[p.i-1].Pos
		n.End.Col += len(p.toks[p.i-1].Text)
		if p.at(Quest) {
			need.Optional = true
			n.End = p.next().Pos
			n.End.Col++
		}
		n.Items = append(n.Items, need)
		if p.at(TIdent) && p.tok().Text == "uses" {
			p.errorf(p.tok().Pos, "uses comes before needs: write uses ... needs ...")
			panic(bailout{})
		}
		if !p.at(Plus) {
			return n
		}
		p.next()
	}
}

// withExpr parses `with (name: value, ...) { ... }`.
func (p *parser) withExpr() Expr {
	w := &WithExpr{Pos: p.next().Pos}
	p.expect(LParen, "after with")
	p.list(RParen, "an ambient value to bind", func() {
		t := p.expect(TIdent, "(ambient value name)")
		b := &WithBinding{Pos: t.Pos, Name: p.qualify(t)}
		p.expect(Colon, "after the ambient value's name")
		b.Value = p.expr()
		w.Bindings = append(w.Bindings, b)
	})
	w.Body = p.block()
	return w
}

// requirementGroup keeps and/or homogeneous at each parenthesis level.
func (p *parser) requirementGroup() Expr {
	atom := func() Expr {
		if p.at(LParen) {
			p.next()
			x := p.requirementGroup()
			p.expect(RParen, "to end the requirement group")
			return x
		}
		return p.binary(precedence[Eq])
	}
	x := atom()
	var join Kind
	for p.at(KwAnd) || p.at(KwOr) {
		op := p.next()
		if join != 0 && join != op.Kind {
			p.errorf(op.Pos, "mixing and with or needs parentheses")
		}
		join = op.Kind
		p.skipNewlines()
		kind := AndAnd
		if op.Kind == KwOr {
			kind = OrOr
		}
		x = &Binary{Pos: op.Pos, Op: kind, X: x, Y: atom()}
	}
	return x
}

// uses parses `uses io + net` or `uses nothing`, if present: the
// effects of a function or a function type.
func (p *parser) uses() *Uses {
	if !p.at(TIdent) || p.tok().Text != "uses" {
		return nil
	}
	u := &Uses{Pos: p.next().Pos}
	if !p.at(TIdent) {
		p.errorf(p.tok().Pos, "expected an effect name or nothing after uses, found %s", p.tok().Kind)
		panic(bailout{})
	}
	if p.tok().Text == "nothing" {
		u.End = p.next().Pos
		u.End.Col += len("nothing")
		if p.at(Plus) {
			p.errorf(p.tok().Pos, "uses nothing cannot be combined with effects")
			panic(bailout{})
		}
		return u
	}
	for {
		name := p.expect(TIdent, "(effect name)")
		if name.Text == "nothing" {
			p.errorf(name.Pos, "uses nothing cannot be combined with effects")
			panic(bailout{})
		}
		u.Effects = append(u.Effects, Effect{Pos: name.Pos, Name: name.Text})
		u.End = name.Pos
		u.End.Col += len(name.Text)
		if !p.at(Plus) {
			return u
		}
		p.next()
	}
}

func (p *parser) typeAtom() *TypeExpr {
	if p.at(LParen) {
		// A function type `(A, B) => C`, or a parenthesized type.
		pos := p.next().Pos
		var params []*TypeExpr
		comma := false
		p.skipNewlines()
		for !p.at(RParen) {
			params = append(params, p.typeExpr())
			p.skipNewlines()
			if !p.at(Comma) {
				break
			}
			comma = true
			p.next()
			p.skipNewlines()
		}
		p.expect(RParen, "to close the type")
		uses := p.uses()
		if uses != nil && !p.at(Arrow) {
			p.errorf(p.tok().Pos, "expected => after a function type's effects")
			panic(bailout{})
		}
		if p.at(Arrow) && (p.typeDepth != p.patternTypeDepth || len(params) == 0 || uses != nil || p.patternFunctionArrow()) {
			p.next()
			return &TypeExpr{Pos: pos, Func: &FuncTypeExpr{Params: params, Uses: uses, Result: p.typeExpr()}}
		}
		if comma {
			return &TypeExpr{Pos: pos, Tuple: params}
		}
		if len(params) != 1 {
			p.errorf(pos, "empty tuples are not supported; expected => after function parameters")
			panic(bailout{})
		}
		return params[0]
	}
	t := p.expect(TIdent, "(type name)")
	te := &TypeExpr{Pos: t.Pos, Name: p.qualify(t)}
	if p.inDerivation && p.at(Dot) && p.peekKind() == TIdent {
		p.next()
		te.Name += "." + p.next().Text
	}
	if p.at(LBrack) {
		p.next()
		for {
			te.Args = append(te.Args, p.typeExpr())
			if !p.at(Comma) {
				break
			}
			p.next()
		}
		p.expect(RBrack, "to close the type arguments")
	}
	if te.Name == "Seq" {
		te.Uses = p.uses()
	}
	return te
}

// headExpr parses an expression terminated by the control's opening brace.
func (p *parser) headExpr() Expr {
	saved := p.noRecordLit
	p.noRecordLit = true
	defer func() { p.noRecordLit = saved }()
	start := p.i
	x := p.expr()
	p.rememberHeadParentheses(start, p.i, false)
	return x
}

// wrappedHead reports whether an opening parenthesis encloses the entire head.
// A grouped first operand, as in `for (i) < n {}`, is not a header wrapper.
func (p *parser) wrappedHead() bool {
	if !p.at(LParen) {
		return false
	}
	depth := 0
	for i := p.i; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case LParen:
			depth++
		case RParen:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Kind == LBrace
			}
		}
	}
	return false
}

// rememberHeadParentheses records safely removable head grouping for fmt's
// opt-in cleanup. Top-level braces need grouping to avoid record ambiguity;
// top-level commas need grouping to remain tuples. Nested grouping is retained
// when necessary, so `((User {}))` can become `(User {})`.
func (p *parser) rememberHeadParentheses(start, end int, loop bool) {
	if p.headParentheses == nil {
		return
	}
	for start < end && p.toks[start].Kind == LParen && p.toks[end-1].Kind == RParen {
		depth, safe, whole := 0, true, true
		for i := start + 1; i < end-1; i++ {
			switch p.toks[i].Kind {
			case LBrace:
				if depth == 0 {
					safe = false
				}
				depth++
			case LParen, LBrack:
				depth++
			case RParen, RBrack, RBrace:
				depth--
				if depth < 0 {
					whole = false
				}
			case Comma:
				if depth == 0 && !loop {
					safe = false
				}
			case Semi:
				if depth == 0 && p.toks[i].Text == "\n" {
					safe = false
				}
			}
		}
		if !whole {
			return
		}
		if safe {
			*p.headParentheses = append(*p.headParentheses, SourceSpan{p.toks[start].Pos, p.toks[end-1].Pos})
		}
		start++
		end--
		loop = false
	}
}

// forLoop parses infinite, iteration, condition and three-clause loops.
func (p *parser) forLoop() *For {
	f := &For{Pos: p.next().Pos}
	if p.at(LBrace) {
		f.Body = p.block()
		if p.at(KwYield) {
			p.errorf(p.tok().Pos, "only a comprehension ends with yield, and its first line is a generator such as x in xs")
			panic(bailout{})
		}
		return f
	}
	start := p.i
	wrapped := p.wrappedHead()
	saved := p.noRecordLit
	p.noRecordLit = !wrapped
	defer func() { p.noRecordLit = saved }()
	end := LBrace
	if wrapped {
		p.next()
		end = RParen
		p.skipNewlines()
	}
	if p.iterationPatternAhead() {
		f.Pattern = p.iterationPattern()
		f.NamePos = f.Pattern.Position()
		switch pattern := f.Pattern.(type) {
		case *VariantPat:
			f.Name = pattern.Path[0]
		case *WildcardPat:
			f.Name = "_"
		}
		f.Items = p.expr()
	} else if p.at(Semi) || p.at(TIdent) && (p.peekKind() == Assign || p.peekKind() == Colon) {
		f.Clauses = true
		f.Init = p.loopBindings(true)
		p.expect(Semi, "after the loop's header bindings")
		if !p.at(Semi) {
			f.Cond = p.expr()
		}
		p.expect(Semi, "after the loop's condition")
		if !p.at(end) {
			f.Post = p.loopBindings(false)
		}
	} else {
		f.Cond = p.expr()
		if p.at(Semi) {
			p.errorf(p.tok().Pos, "a loop's header bindings are name = value, as in for i = 0; i < n; i = i + 1")
			panic(bailout{})
		}
	}
	if wrapped {
		p.skipNewlines()
		p.expect(RParen, "after the loop head")
	}
	p.rememberHeadParentheses(start, p.i, f.Clauses || f.Items != nil)
	p.noRecordLit = saved
	f.Body = p.block()
	return f
}

// loopBindings parses comma-separated `name = value` bindings of a
// loop's header (init may also annotate a type), up to a ';' or ')'.
func (p *parser) loopBindings(init bool) []*Binding {
	var out []*Binding
	for p.at(TIdent) {
		name := p.next()
		b := &Binding{Pos: name.Pos, Name: name.Text}
		if init && p.at(Colon) {
			p.next()
			b.Type = p.typeExpr()
		}
		p.expect(Assign, "after the loop variable's name")
		b.Value = p.expr()
		out = append(out, b)
		if !p.at(Comma) {
			break
		}
		p.next()
		if !p.at(TIdent) {
			p.errorf(p.tok().Pos, "expected another binding after ',', found %s", p.tok().Kind)
			panic(bailout{})
		}
	}
	if init && len(out) == 0 && !p.at(Semi) || !init && len(out) == 0 {
		p.errorf(p.tok().Pos, "expected a binding, name = value, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return out
}

func (p *parser) block() *Block {
	saved := p.noRecordLit
	p.noRecordLit = false
	defer func() { p.noRecordLit = saved }()
	b := &Block{Pos: p.expect(LBrace, "to start a block").Pos}
	for {
		p.skipSemis()
		if p.at(RBrace) {
			b.End = p.next().Pos
			return b
		}
		if p.at(EOF) {
			p.errorf(b.Pos, "block is not closed (missing '}')")
			panic(bailout{})
		}
		stmt := p.statement()
		if p.at(RBrace) {
			b.End = p.next().Pos
			if es, ok := stmt.(*ExprStmt); ok {
				b.Tail = es.X
			} else {
				b.Stmts = append(b.Stmts, stmt)
			}
			return b
		}
		if !p.at(Semi) {
			p.errorf(p.tok().Pos, "expected end of line or '}', found %s", p.tok().Kind)
			panic(bailout{})
		}
		b.Stmts = append(b.Stmts, stmt)
		// A statement followed only by newlines and '}' is the tail.
		save := p.i
		p.skipSemis()
		if p.at(RBrace) {
			if es, ok := stmt.(*ExprStmt); ok {
				b.Stmts = b.Stmts[:len(b.Stmts)-1]
				b.Tail = es.X
			}
			b.End = p.next().Pos
			return b
		}
		p.i = save
	}
}

func (p *parser) scriptStatement() (stmt Stmt) {
	defer p.recoverDecl(func() { stmt = nil })
	stmt = p.statement()
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after the script statement, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return stmt
}

func (p *parser) statement() Stmt {
	var stmt Stmt
	switch {
	case p.at(LParen) && p.tupleBindingAhead():
		pattern, ok := p.pattern().(*TuplePat)
		if !ok {
			p.errorf(p.tok().Pos, "tuple binding needs a comma in its pattern")
			panic(bailout{})
		}
		p.expect(Assign, "after the tuple binding pattern")
		stmt = &TupleBinding{Pos: pattern.Pos, Pattern: pattern, Value: p.expr()}
	case p.atAsyncBinding():
		pos := p.next().Pos
		p.next() // '('
		scope := p.expr()
		p.expect(RParen, "after the async scope")
		name := p.expect(TIdent, "after async(scope) (a single binding name)")
		binding := &Binding{Pos: name.Pos, Name: name.Text, AsyncScope: scope, AsyncPos: pos}
		if p.at(Colon) {
			p.next()
			binding.Type = p.typeExpr()
		}
		p.expect(Assign, "after the async binding's name or type")
		binding.Value = p.expr()
		stmt = binding
	case p.at(TIdent) && p.tok().Text == "lazy" && (p.peekKind() == TIdent || p.peekKind() == Underscore):
		stmt = p.lazyBinding()
	case p.atMock():
		stmt = p.mockStmt(p.tok().Pos, "")
	case p.at(TIdent) && p.peekKind() == Assign && p.toks[min(p.i+2, len(p.toks)-1)].Text == "mock" && p.toks[min(p.i+3, len(p.toks)-1)].Kind == TIdent:
		name := p.next()
		p.next() // '='
		stmt = p.mockStmt(name.Pos, name.Text)
	case (p.at(TIdent) || p.at(Underscore)) && p.peekKind() == Assign:
		// `_ = f()` evaluates f() and drops its value.
		name := p.next()
		if name.Kind == Underscore {
			name.Text = "_"
		}
		p.next() // '='
		stmt = &Binding{Pos: name.Pos, Name: name.Text, Value: p.expr()}
	case p.at(KwTrust):
		pos := p.next().Pos
		x := p.expr()
		call, ok := x.(*Call)
		if !ok {
			p.errorf(x.Position(), "trust needs a predicate call, as in trust positive(x)")
			panic(bailout{})
		}
		stmt = &TrustStmt{Pos: pos, Call: call}
	case (p.at(TIdent) || p.at(Underscore)) && p.peekKind() == Colon:
		name := p.next()
		p.next() // ':'
		typ := p.typeExpr()
		p.expect(Assign, "after the binding's type")
		stmt = &Binding{Pos: name.Pos, Name: name.Text, Type: typ, Value: p.expr()}
	default:
		stmt = &ExprStmt{X: p.expr()}
	}
	return stmt
}

// atMock reports whether a mock statement starts here: `mock` followed
// by a name. (`mock` is a keyword only there.)
func (p *parser) atMock() bool {
	return p.at(TIdent) && p.tok().Text == "mock" && p.peekKind() == TIdent
}

// mockStmt parses `mock target(a, b) { ... }`, at 'mock'; pos and name
// are those of the handle it is bound to, if any.
func (p *parser) mockStmt(pos diag.Pos, name string) *MockStmt {
	m := &MockStmt{Pos: pos, Name: name, MockPos: p.next().Pos}
	t := p.next()
	if t.Kind != TIdent {
		p.errorf(t.Pos, "expected the function to mock after 'mock', found %s", t.Kind)
		panic(bailout{})
	}
	m.Target = &Ident{Pos: t.Pos, Name: p.qualify(t)}
	if p.at(Dot) && p.peekKind() == TIdent {
		p.next()
		method := p.next()
		m.Target = &Selector{Pos: method.Pos, X: m.Target, Name: method.Text}
	}
	if p.at(LBrack) {
		p.errorf(p.tok().Pos, "generic functions cannot be mocked yet")
		panic(bailout{})
	}
	m.ParamsStart = p.expect(LParen, "to start the mock's parameters").Pos
	for !p.at(RParen) {
		par := p.next()
		switch par.Kind {
		case TIdent:
		case Underscore:
			par.Text = "_"
		default:
			p.errorf(par.Pos, "a mock's parameters are names only (their types come from the function it mocks), found %s", par.Kind)
			panic(bailout{})
		}
		m.Params = append(m.Params, &Param{Pos: par.Pos, Name: par.Text})
		if !p.at(Comma) {
			break
		}
		p.next()
	}
	m.ParamsEnd = p.expect(RParen, "to close the mock's parameters").Pos
	m.Body = p.block()
	return m
}

// Binary operator precedence, lowest first.
var precedence = map[Kind]int{
	PipeGt: 1,
	OrOr:   2,
	AndAnd: 3,
	Eq:     4, NotEq: 4, Lt: 4, LtEq: 4, Gt: 4, GtEq: 4,
	Plus: 5, Minus: 5, Pipe: 5, Caret: 5,
	Star: 6, Slash: 6, Pct: 6, Amp: 6, Shl: 6, Shr: 6,
}

func (p *parser) expr() Expr { return p.binary(1) }

func (p *parser) binary(minPrec int) (out Expr) {
	start := p.tok().Pos
	defer func() { p.rememberSpan(out, start) }()
	x := p.unary()
	for {
		p.rememberSpan(x, start)
		op := p.tok()
		prec, ok := precedence[op.Kind]
		is := op.Kind == TIdent && op.Text == "is"
		if is {
			prec, ok = 4, true
		}
		if !ok || prec < minPrec {
			return x
		}
		end := p.toks[p.i-1].End
		p.next()
		p.skipNewlines() // an operator at the end of a line continues the expression
		if is {
			if p.patternTestOperators != nil {
				*p.patternTestOperators = append(*p.patternTestOperators, op.Pos)
			}
			saved := p.testingPattern
			p.testingPattern = true
			pat := p.testPattern()
			p.testingPattern = saved
			x = &Is{Pos: op.Pos, X: x, Pattern: pat, End: p.toks[p.i-1].End}
			continue
		}
		y := p.binary(prec + 1)
		if op.Kind == PipeGt {
			x = pipe(op, x, y, start, end, p.toks[p.i-1].End)
			continue
		}
		x = &Binary{Pos: op.Pos, Op: op.Kind, X: x, Y: y}
	}
}

// pipe desugars `x |> f(a, b)` to `f(x, a, b)`, and `x |> f` to `f(x)`.
func pipe(op Token, x, y Expr, start, end, targetEnd diag.Pos) Expr {
	call, ok := y.(*Call)
	if ok {
		call.Args = append([]Expr{x}, call.Args...)
		call.Arguments = append([]Argument{{Pos: start, End: end}}, call.Arguments...)
	} else {
		call = &Call{Pos: op.Pos, Fun: y, Args: []Expr{x}, PipeBare: true}
	}
	call.Pipe, call.PipeStart, call.PipeEnd = op.Pos, start, end
	call.PipeTargetEnd = targetEnd
	switch recv := x.(type) {
	case *Binary, *Unary, *Lambda:
		call.PipeWrap = true
	case *Call:
		call.PipeWrap = recv.Pipe.File != ""
	}
	return call
}

func (p *parser) unary() (out Expr) {
	start := p.tok().Pos
	defer func() { p.rememberSpan(out, start) }()
	if p.at(Minus) || p.at(Not) || p.at(Caret) {
		op := p.next()
		return &Unary{Pos: op.Pos, Op: op.Kind, X: p.unary()}
	}
	return p.postfix(p.primary(), start)
}

func (p *parser) rememberSpan(x Expr, start diag.Pos) {
	if x == nil || p.spans == nil || p.i == 0 {
		return
	}
	span := SourceSpan{start, p.toks[p.i-1].End}
	spans := p.spanSeen[x]
	for _, old := range spans {
		if old == span {
			return
		}
	}
	p.spanSeen[x] = append(spans, span)
	*p.spans = append(*p.spans, ExpressionSpan{x, span})
}

func (p *parser) postfix(x Expr, start diag.Pos) Expr {
	for {
		p.rememberSpan(x, start)
		funEnd := p.toks[p.i-1].End
		var typeArgs []*TypeExpr
		_, isID := x.(*Ident)
		_, isSel := x.(*Selector)
		if (isID || isSel) && p.at(LBrack) {
			// Explicit arguments of a call, method call, or constructor
			// owner: empty[Int](), xs.map[String](f), Box[Int] { ... }.
			p.next()
			for {
				typeArgs = append(typeArgs, p.typeExpr())
				if !p.at(Comma) {
					break
				}
				p.next()
			}
			end := p.expect(RBrack, "to end the type arguments").End
			if !p.at(LParen) {
				id, ok := x.(*Ident)
				if !ok || !p.at(LBrace) && !p.at(Dot) {
					p.errorf(p.tok().Pos, "expected a call, record literal, or variant after the type arguments")
					panic(bailout{})
				}
				x = &TypeHead{Type: &TypeExpr{Pos: id.Pos, Name: id.Name, Args: typeArgs}, End: end}
				typeArgs = nil
			}
		}
		switch {
		case p.at(LParen):
			if sel, ok := x.(*Selector); ok && sel.Name == "into" {
				callPos := p.tok().Pos
				updates := p.copyExpr(sel.X, sel.Pos).(*Copy)
				call := &Call{Start: start, Pos: callPos, End: p.toks[p.i-1].End, Fun: sel, FunEnd: funEnd, TypeArgs: typeArgs}
				for _, update := range updates.Updates {
					call.Args = append(call.Args, update.Value)
					call.Arguments = append(call.Arguments, Argument{Pos: update.Pos, Name: strings.Join(update.Path, "."), NameEnd: update.PathEnd, ValueStart: update.ValueStart, End: update.ValueEnd})
				}
				x = call
				continue
			}
			call := &Call{Start: start, Pos: p.next().Pos, Fun: x, FunEnd: funEnd, TypeArgs: typeArgs}
			saved := p.noRecordLit
			p.noRecordLit = false // within the parentheses, '{' is a literal again
			p.skipNewlines()
			for !p.at(RParen) {
				arg := Argument{Pos: p.tok().Pos}
				if p.at(TIdent) && (p.peekKind() == Colon || p.peekKind() == Assign) {
					label := p.next()
					arg.Name, arg.NameEnd = label.Text, label.End
					if t := p.next(); t.Kind == Assign {
						// Assignment is a statement, so `name = value` here can only be
						// a named argument written the way Python and Kotlin spell it.
						p.diags.AddCode(t.Pos, "syntax.named_argument_separator", "named arguments use ':'; write `%s: value`", label.Text)
						p.diags.Suggest(t.Pos, "syntax.named_argument_separator", t.End, diag.Fix{
							Message: "replace '=' with ':'", Edits: []diag.TextEdit{{Start: t.Pos, End: t.End, Replacement: ":"}},
						})
					}
					p.skipNewlines()
				}
				arg.ValueStart = p.tok().Pos
				call.Args = append(call.Args, p.expr())
				arg.End = p.toks[p.i-1].End
				call.Arguments = append(call.Arguments, arg)
				p.skipNewlines()
				if !p.at(Comma) {
					break
				}
				p.next()
				p.skipNewlines()
			}
			call.End = p.expect(RParen, "to end the argument list").End
			for i := 1; i < len(call.Arguments); i++ {
				arg := &call.Arguments[i]
				if arg.Name == "" {
					continue
				}
				start := call.Arguments[i-1].End
				commented := false
				for _, comment := range p.comments {
					if (comment.Pos.Line > start.Line || comment.Pos.Line == start.Line && comment.Pos.Col >= start.Col) &&
						(comment.Pos.Line < arg.End.Line || comment.Pos.Line == arg.End.Line && comment.Pos.Col < arg.End.Col) {
						commented = true
						break
					}
				}
				if !commented {
					arg.RemovalStart = start
				}
			}
			p.noRecordLit = saved
			x = call
		case p.at(Dot):
			p.next()
			var name Token
			if p.at(TInt) {
				name = p.next()
				if strings.Trim(name.Text, "0123456789") != "" {
					p.errorf(name.Pos, "tuple selectors require a decimal position")
				}
			} else {
				name = p.expect(TIdent, "after '.'")
			}
			if name.Text == "copy" && p.at(LParen) {
				x = p.copyExpr(x, name.Pos)
			} else {
				x = &Selector{Pos: name.Pos, X: x, Name: name.Text}
			}
		case p.at(Quest):
			x = &Try{Pos: p.next().Pos, X: x}
		case p.at(LBrace) && isTypePath(x) && !p.noRecordLit:
			x = p.recordLit(x)
		default:
			return x
		}
	}
}

// isTypePath reports whether x could name a record type or variant
// (`User`, `Shape.Circle`), so a following '{' starts a record literal.
func isTypePath(x Expr) bool {
	switch x := x.(type) {
	case *ContextName, *TypeHead:
		return true
	case *Ident:
		return true
	case *Selector:
		switch x.X.(type) {
		case *Ident, *TypeHead:
			return true
		}
		return false
	}
	return false
}

func (p *parser) recordLit(typ Expr) Expr {
	saved := p.noRecordLit
	p.noRecordLit = false
	defer func() { p.noRecordLit = saved }()
	lit := &RecordLit{Type: typ}
	p.expect(LBrace, "")
	p.list(RBrace, "a field", func() {
		fname := p.expect(TIdent, "(field name)")
		p.expect(Colon, "after field name")
		p.skipNewlines()
		lit.Fields = append(lit.Fields, &FieldInit{Pos: fname.Pos, Name: fname.Text, Value: p.expr()})
	})
	return lit
}

func (p *parser) copyExpr(x Expr, pos diag.Pos) Expr {
	saved := p.noRecordLit
	p.noRecordLit = false
	defer func() { p.noRecordLit = saved }()
	c := &Copy{Pos: pos, X: x}
	p.expect(LParen, "")
	p.list(RParen, "a field update", func() {
		first := p.expect(TIdent, "(field name)")
		u := &CopyUpdate{Pos: first.Pos, PathEnd: first.End, Path: []string{first.Text}}
		for p.at(Dot) {
			p.next()
			part := p.expect(TIdent, "(field name)")
			u.Path = append(u.Path, part.Text)
			u.PathEnd = part.End
		}
		if p.at(Assign) {
			t := p.next()
			p.diags.AddCode(t.Pos, "syntax.copy_separator", "copy updates use ':'; write `field: value`")
			p.diags.Suggest(t.Pos, "syntax.copy_separator", t.End, diag.Fix{
				Message: "replace '=' with ':'", Edits: []diag.TextEdit{{Start: t.Pos, End: t.End, Replacement: ":"}},
			})
		} else {
			p.expect(Colon, "after the field path (write `field: value`)")
		}
		p.skipNewlines()
		u.ValueStart = p.tok().Pos
		u.Value = p.expr()
		u.ValueEnd = p.toks[p.i-1].End
		c.Updates = append(c.Updates, u)
	})
	return c
}

func (p *parser) matchExpr() Expr {
	m := &Match{Pos: p.next().Pos}
	m.X = p.headExpr()
	saved := p.noRecordLit
	p.noRecordLit = false
	defer func() { p.noRecordLit = saved }()
	p.expect(LBrace, "to start the match arms")
	p.list(RBrace, "a match arm", func() {
		pat := p.pattern()
		p.expect(Arrow, "after the pattern")
		p.skipNewlines()
		m.Arms = append(m.Arms, &Arm{Pattern: pat, Body: p.expr()})
	})
	m.Close = p.toks[p.i-1].Pos
	if p.i >= 2 {
		m.TrailingSeparator = p.toks[p.i-2].Kind == Comma || p.toks[p.i-2].Kind == Semi
	}
	return m
}

// A function annotation has a result type followed by a second arm arrow.
// Speculate without publishing diagnostics so a tuple arm's first arrow remains
// its separator when what follows is an expression rather than a result type.
func (p *parser) patternFunctionArrow() (ok bool) {
	trial := *p
	trial.diags = &diag.List{}
	var spans []ExpressionSpan
	trial.spans = &spans
	trial.spanSeen = map[Expr][]SourceSpan{}
	defer func() {
		if r := recover(); r != nil {
			if _, bailout := r.(bailout); !bailout {
				panic(r)
			}
			ok = false
		}
	}()
	trial.next()
	trial.patternTypeDepth = trial.typeDepth + 1
	trial.typeExpr()
	return trial.at(Arrow) && trial.diags.Len() == 0
}

func (p *parser) patternType() *TypeExpr {
	saved := p.patternTypeDepth
	p.patternTypeDepth = p.typeDepth + 1
	defer func() { p.patternTypeDepth = saved }()
	return p.typeExpr()
}

func (p *parser) selectExpr() Expr {
	sel := &Select{Pos: p.next().Pos}
	p.expect(LBrace, "to start the select arms")
	p.list(RBrace, "a select arm", func() {
		arm := &SelectArm{Pos: p.tok().Pos}
		switch {
		case p.at(Underscore) && p.peekKind() == Arrow:
			p.next()
		case (p.at(TIdent) || p.at(Underscore)) && p.peekKind() == Assign:
			name := p.next()
			if name.Kind == TIdent {
				arm.Name, arm.NamePos = name.Text, name.Pos
			}
			p.next()
			arm.Op = p.expr()
		default:
			arm.Op = p.expr()
		}
		p.expect(Arrow, "after the select arm's operation")
		p.skipNewlines()
		arm.Body = p.expr()
		sel.Arms = append(sel.Arms, arm)
	})
	sel.Close = p.toks[p.i-1].Pos
	return sel
}

func (p *parser) pattern() Pattern {
	t := p.tok()
	switch t.Kind {
	case LParen:
		saved := p.noRecordLit
		p.noRecordLit = false
		defer func() { p.noRecordLit = saved }()
		p.next()
		p.skipNewlines()
		var first Pattern
		if p.testingPattern {
			first = p.testPattern()
		} else {
			first = p.pattern()
		}
		p.skipNewlines()
		if !p.at(Comma) {
			p.expect(RParen, "to close the pattern")
			return first
		}
		tuple := &TuplePat{Pos: t.Pos, Elems: []Pattern{first}}
		for p.at(Comma) {
			p.next()
			p.skipNewlines()
			if p.at(RParen) {
				break
			}
			if p.testingPattern {
				tuple.Elems = append(tuple.Elems, p.testPattern())
			} else {
				tuple.Elems = append(tuple.Elems, p.pattern())
			}
			p.skipNewlines()
		}
		tuple.End = p.expect(RParen, "to close the tuple pattern").End
		return tuple
	case Underscore:
		p.next()
		if p.at(Colon) {
			p.next()
			return &TypePat{Pos: t.Pos, Name: "_", Type: p.patternType()}
		}
		return &WildcardPat{Pos: t.Pos}
	case TInt, TFloat, TRune, TString, KwTrue, KwFalse, Minus:
		return &LitPat{Pos: t.Pos, Value: p.unary()}
	case LBrack:
		saved := p.noRecordLit
		p.noRecordLit = false
		defer func() { p.noRecordLit = saved }()
		p.next()
		lp := &ListPat{Pos: t.Pos}
		p.list(RBrack, "a pattern", func() {
			if lp.HasRest {
				p.errorf(p.tok().Pos, "the rest of the list (...) must come last")
				panic(bailout{})
			}
			if p.at(Ellipsis) {
				lp.RestPos = p.next().Pos
				lp.HasRest = true
				if p.at(TIdent) {
					lp.Rest = p.next().Text
				}
				return
			}
			if p.testingPattern {
				lp.Elems = append(lp.Elems, p.testPattern())
			} else {
				lp.Elems = append(lp.Elems, p.pattern())
			}
		})
		return lp
	case TIdent, Dot:
		if t.Kind == TIdent && p.peekKind() == Colon {
			p.next()
			p.next()
			return &TypePat{Pos: t.Pos, Name: t.Text, Type: p.patternType()}
		}
		vp := &VariantPat{Pos: t.Pos}
		if t.Kind == Dot {
			p.next()
			name := p.expect(TIdent, "after '.' in a variant pattern")
			vp.Context, vp.End, vp.Path = true, name.End, []string{name.Text}
			vp.NamePos = name.Pos
		} else {
			vp.Path = []string{p.qualify(p.next())}
			if p.at(LBrack) {
				vp.Owner = &TypeExpr{Pos: t.Pos, Name: vp.Path[0]}
				p.next()
				p.list(RBrack, "a type argument", func() { vp.Owner.Args = append(vp.Owner.Args, p.typeExpr()) })
				if !p.at(Dot) {
					p.errorf(p.tok().Pos, "expected a variant after the specialized owner")
					panic(bailout{})
				}
			}
		}
		for !vp.Context && p.at(Dot) {
			p.next()
			name := p.expect(TIdent, "(variant name)")
			vp.NamePos = name.Pos
			vp.Path = append(vp.Path, name.Text)
		}
		if p.at(LBrace) && !p.noRecordLit {
			vp.PayloadPos = p.next().Pos
			vp.Braces = true
			p.list(RBrace, "a field pattern", func() {
				f := p.expect(TIdent, "(field name)")
				fp := &FieldPat{Pos: f.Pos, Field: f.Text}
				if p.at(Colon) {
					p.next()
					if p.testingPattern {
						fp.Pattern = p.testPattern()
					} else {
						fp.Pattern = p.pattern()
					}
				}
				vp.Fields = append(vp.Fields, fp)
			})
			vp.PayloadEnd = p.toks[p.i-1].End
		}
		if p.at(LParen) {
			if vp.Braces {
				p.errorf(p.tok().Pos, "a variant pattern cannot mix named fields and positional payloads")
				panic(bailout{})
			}
			saved := p.noRecordLit
			p.noRecordLit = false
			defer func() { p.noRecordLit = saved }()
			vp.Positional = true
			vp.PayloadPos = p.next().Pos
			p.list(RParen, "a payload pattern", func() {
				if p.testingPattern {
					vp.Elems = append(vp.Elems, p.testPattern())
				} else {
					vp.Elems = append(vp.Elems, p.pattern())
				}
			})
			vp.PayloadEnd = p.toks[p.i-1].End
		}
		if p.testingPattern && !vp.Context && !vp.Braces && !vp.Positional && len(vp.Path) == 1 {
			return &TypePat{Pos: vp.Pos, Type: &TypeExpr{Pos: vp.Pos, Name: vp.Path[0]}}
		}
		return vp
	}
	p.errorf(t.Pos, "expected a pattern, found %s", t.Kind)
	panic(bailout{})
}

func (p *parser) primary() Expr {
	t := p.tok()
	switch t.Kind {
	case Dot:
		p.next()
		x := &ContextName{Pos: t.Pos, End: t.End}
		if p.at(LBrace) {
			return p.recordLit(x)
		}
		name := p.expect(TIdent, "after '.' (write .Variant or .{ field: value })")
		x.Name, x.End = name.Text, name.End
		x.NamePos = name.Pos
		return x
	case KwGenerate:
		p.next()
		p.expect(LBrack, "after generate")
		elem := p.typeExpr()
		p.expect(RBrack, "after the generated element type")
		return &Generate{Pos: t.Pos, Elem: elem, Body: p.block()}
	case KwYield:
		p.next()
		return &Yield{Pos: t.Pos, Value: p.expr()}
	case KwFor:
		if p.comprehensionAhead() {
			return p.comprehension()
		}
		return p.forLoop()
	case KwBreak, KwContinue:
		p.next()
		return &LoopControl{Pos: t.Pos, Continue: t.Kind == KwContinue}
	case TInt:
		p.next()
		return &IntLit{Pos: t.Pos, Text: t.Text}
	case TFloat:
		p.next()
		return &FloatLit{Pos: t.Pos, Text: t.Text}
	case TRune:
		p.next()
		return &RuneLit{Pos: t.Pos, Text: t.Text}
	case TInterp:
		p.next()
		return p.interp(t, nil)
	case TString:
		p.next()
		v, err := strconv.Unquote(t.Text)
		if err != nil {
			p.errorf(t.Pos, "invalid string literal %s", t.Text)
		}
		return &StringLit{Pos: t.Pos, Value: v}
	case KwTrue, KwFalse:
		p.next()
		return &BoolLit{Pos: t.Pos, Value: t.Kind == KwTrue}
	case TIdent:
		if p.peekKind() == Arrow && !p.noLambda {
			return p.lambda()
		}
		// Staged controls belong to a derive template; ordinary comptime
		// blocks retain their existing evaluation semantics.
		if t.Text == "comptime" && (p.peekKind() == KwFor || p.peekKind() == KwIf || p.peekKind() == KwMatch) {
			p.next()
			if !p.inDerivation {
				p.errorf(t.Pos, "comptime controls are available only inside derive templates and derive helpers")
			}
			x := p.primary()
			switch x := x.(type) {
			case *For:
				x.Comptime = true
			case *If:
				x.Comptime = true
			case *Match:
				x.Comptime = true
			case *Generate:
				p.errorf(t.Pos, "a comprehension cannot be comptime; write a comptime list comprehension, [comptime for (x in xs) value]")
			}
			return x
		}
		// `comptime` is contextual before a computation block.
		if t.Text == "comptime" && p.peekKind() == LBrace {
			p.next()
			return &Comptime{Pos: t.Pos, Body: p.block()}
		}
		// `with` is a keyword where an expression starts and '(' follows.
		if t.Text == "with" && p.peekKind() == LParen {
			return p.withExpr()
		}
		// `select` is a keyword where an expression starts and '{' follows,
		// except in a scope's policy, where the '{' starts the scope's body.
		if t.Text == "select" && p.peekKind() == LBrace && !p.noRecordLit {
			return p.selectExpr()
		}
		// `scope` is a keyword only where a scope block starts.
		if t.Text == "scope" && p.peekKind() == TIdent {
			p.next()
			name := p.next()
			se := &ScopeExpr{Pos: t.Pos, Name: name.Text}
			if p.at(TIdent) && p.tok().Text == "with" {
				// The policy is followed by the block, so a bare name
				// before '{' is not a record literal.
				p.next()
				saved := p.noRecordLit
				p.noRecordLit = true
				se.Policies = append(se.Policies, p.expr())
				for p.at(Comma) {
					p.next()
					se.Policies = append(se.Policies, p.expr())
				}
				p.noRecordLit = saved
			}
			se.Body = p.block()
			return se
		}
		p.next()
		name := &Ident{Pos: t.Pos, Name: p.qualify(t)}
		if p.at(TInterp) && p.toks[p.i-1].End == p.tok().Pos {
			return p.interp(p.next(), name)
		}
		return name
	case LBrack:
		saved := p.noRecordLit
		p.noRecordLit = false
		defer func() { p.noRecordLit = saved }()
		p.next()
		lit := &ListLit{Pos: t.Pos}
		p.skipNewlines()
		if p.at(TIdent) && p.tok().Text == "comptime" && p.peekKind() == KwFor {
			prefix := p.next()
			if !p.inDerivation {
				p.errorf(prefix.Pos, "comptime list comprehensions are available only inside derive templates and derive helpers")
			}
			loop := p.forHeader()
			loop.Comptime, loop.Comprehension = true, true
			p.skipNewlines()
			var guard Expr
			if p.at(TIdent) && p.tok().Text == "comptime" && p.peekKind() == KwIf {
				p.next()
				pos := p.next().Pos
				p.expect(LParen, "after comptime if")
				guard = &If{Pos: pos, Comptime: true, Cond: p.expr()}
				p.expect(RParen, "after the comprehension guard")
				p.skipNewlines()
			}
			value := p.expr()
			if guarded, ok := guard.(*If); ok {
				guarded.Then = &Block{Pos: value.Position(), Tail: value}
				value = guarded
			}
			loop.Body = &Block{Pos: value.Position(), Tail: value}
			lit.Elems = []Expr{loop}
			p.skipNewlines()
			p.expect(RBrack, "after the comptime list comprehension")
			return lit
		}
		p.list(RBrack, "a list element", func() {
			p.skipNewlines()
			lit.Elems = append(lit.Elems, p.expr())
		})
		return lit
	case LParen:
		saved := p.noRecordLit
		p.noRecordLit = false
		defer func() { p.noRecordLit = saved }()
		if !p.noLambda && p.lambdaAhead() {
			return p.lambda()
		}
		p.next()
		p.skipNewlines()
		x := p.expr()
		p.skipNewlines()
		if p.at(Comma) {
			lit := &TupleLit{Pos: t.Pos, Elems: []Expr{x}}
			for p.at(Comma) {
				p.next()
				p.skipNewlines()
				if p.at(RParen) {
					break
				}
				lit.Elems = append(lit.Elems, p.expr())
				p.skipNewlines()
			}
			lit.End = p.expect(RParen, "to close the tuple").End
			return lit
		}
		p.expect(RParen, "to close the parenthesis")
		return x
	case LBrace:
		if p.mapAhead() {
			return p.mapLit()
		}
		return p.block()
	case KwIf:
		return p.ifExpr()
	case KwMatch:
		return p.matchExpr()
	case KwReturn:
		p.next()
		r := &Return{Pos: t.Pos}
		if !p.at(Semi) && !p.at(RBrace) && !p.at(RParen) && !p.at(EOF) {
			r.Value = p.expr()
		}
		return r
	}
	p.errorf(t.Pos, "expected an expression, found %s", t.Kind)
	panic(bailout{})
}

// lambdaAhead reports whether the '(' at the current token starts a
// lambda's parameter list: `(a, b: Int) =>`.
func (p *parser) lambdaAhead() bool {
	depth := 0
	for i := p.i; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case LParen, LBrack:
			depth++
		case RParen, RBrack:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Kind == Arrow
			}
		case EOF, LBrace, RBrace, Assign:
			return false
		}
	}
	return false
}

// mapAhead reports whether the `{` at the current token starts a map
// literal rather than a block: `{:}`, or a first line of the form
// `key: value` followed by `,`, `}`, or a newline. (A block's only
// statement with a `:`, the binding `x: Int = 1`, has an `=`.)
func (p *parser) mapAhead() bool {
	i := p.i + 1
	for i < len(p.toks) && p.toks[i].Kind == Semi && p.toks[i].Text == "\n" {
		i++
	}
	if i+1 < len(p.toks) && p.toks[i].Kind == Colon && p.toks[i+1].Kind == RBrace {
		return true
	}
	depth, colon := 0, false
	for ; i < len(p.toks); i++ {
		switch t := p.toks[i]; t.Kind {
		case LParen, LBrack, LBrace:
			depth++
		case RParen, RBrack:
			depth--
		case RBrace:
			if depth == 0 {
				return colon
			}
			depth--
		case Colon:
			if depth == 0 {
				if colon {
					return false
				}
				colon = true
			}
		case Comma:
			if depth == 0 {
				return colon
			}
		case Semi:
			if depth == 0 {
				return colon
			}
		case Assign:
			if depth == 0 {
				return false
			}
		case EOF:
			return false
		}
	}
	return false
}

// mapLit parses `{key: value, ...}`, or `{:}` for the empty map.
func (p *parser) mapLit() Expr {
	lit := &MapLit{Pos: p.next().Pos}
	saved := p.noRecordLit
	p.noRecordLit = false
	defer func() { p.noRecordLit = saved }()
	p.skipNewlines()
	if p.at(Colon) {
		p.next()
		p.skipNewlines()
		p.expect(RBrace, "to close the empty map {:}")
		return lit
	}
	p.list(RBrace, "a map entry", func() {
		p.skipNewlines()
		lit.Keys = append(lit.Keys, p.expr())
		p.expect(Colon, "between a map entry's key and value")
		p.skipNewlines()
		lit.Values = append(lit.Values, p.expr())
	})
	return lit
}

// lambda parses `x => body` or `(params) => body`.
func (p *parser) lambda() Expr {
	l := &Lambda{Pos: p.tok().Pos}
	if p.at(TIdent) {
		t := p.next()
		l.Params = []*Param{{Pos: t.Pos, Name: t.Text}}
	} else {
		p.next()
		p.list(RParen, "a parameter", func() {
			t := p.expect(TIdent, "(parameter name)")
			param := &Param{Pos: t.Pos, Name: t.Text}
			if p.at(Colon) {
				p.next()
				param.Type = p.typeExpr()
			}
			l.Params = append(l.Params, param)
		})
	}
	p.expect(Arrow, "after the lambda's parameters")
	p.skipNewlines()
	l.Body = p.expr()
	return l
}

func (p *parser) ifExpr() Expr {
	e := &If{Pos: p.next().Pos}
	e.Cond = p.headExpr()
	e.Then = p.block()
	if p.at(Semi) && p.tok().Text == "\n" && p.peekKind() == KwElse {
		p.errorf(p.toks[p.i+1].Pos, "'else' must be on the same line as the closing '}' of the if-block")
		panic(bailout{})
	}
	if p.at(KwElse) {
		p.next()
		if p.at(KwIf) {
			e.Else = p.ifExpr()
		} else {
			e.Else = p.block()
		}
	}
	return e
}

// goCode splits an `unsafe go { ... }` body into its leading
// `import "path"` lines and the Go statements, and checks that the
// statements are valid Go syntax. Syntax errors are reported at their
// bork positions; Go type errors are reported later, by the Go
// compiler (the generated code maps positions back to bork files).
func (p *parser) goCode(t Token) *GoCode {
	gc := &GoCode{Pos: t.Pos}
	lines := strings.Split(t.Text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		rest, ok := strings.CutPrefix(trimmed, "import ")
		if !ok {
			break
		}
		rest = strings.TrimSpace(rest)
		alias := ""
		if !strings.HasPrefix(rest, "\"") {
			parts := strings.Fields(rest)
			if len(parts) >= 2 && gotoken.IsIdentifier(parts[0]) {
				alias = parts[0]
				rest = strings.TrimSpace(strings.TrimPrefix(rest, alias))
			}
		}
		path, err := strconv.Unquote(rest)
		if alias == "_" {
			p.errorf(p.goPos(t.Pos, i, 0), "blank Go import aliases are not supported")
		}
		duplicate := false
		for _, previous := range gc.Imports {
			if previous == path {
				duplicate = true
			}
		}
		if duplicate {
			p.errorf(p.goPos(t.Pos, i, 0), "Go package %q is imported more than once in this unsafe go body", path)
		}
		if err != nil {
			p.errorf(p.goPos(t.Pos, i, 0), "expected `import [alias] \"path\"` in unsafe go block")
		} else {
			gc.Imports = append(gc.Imports, path)
			if alias != "" {
				if gc.ImportAliases == nil {
					gc.ImportAliases = map[string]string{}
				}
				gc.ImportAliases[path] = alias
			}
		}
		lines[i] = ""
	}
	gc.Body = strings.Join(lines, "\n")

	const prefix = "package p\n\nfunc _() {"
	fset := gotoken.NewFileSet()
	_, err := goparser.ParseFile(fset, "", prefix+gc.Body+"}\n", 0)
	var list goscanner.ErrorList
	if errors.As(err, &list) && len(list) > 0 {
		// Only the first error: later ones tend to follow from it.
		e := list[0]
		line, col := e.Pos.Line-3, e.Pos.Column
		if line == 0 {
			col -= len("func _() {")
		}
		p.errorf(p.goPos(t.Pos, line, col-1), "in unsafe go block: %s", e.Msg)
	}
	return gc
}

// goPos is the bork position of a place in a Go code token: line lines
// after the '{' at start, and col columns into that line (or after the
// '{', on its line).
func (p *parser) goPos(start diag.Pos, line, col int) diag.Pos {
	if line == 0 {
		return diag.Pos{File: start.File, Line: start.Line, Col: start.Col + 1 + col}
	}
	return diag.Pos{File: start.File, Line: start.Line + line, Col: col + 1}
}

// interp splits an interpolated string into its text parts and
// expressions: `$name` is a name, `${...}` any expression, and `$$` a
// dollar sign.
func (p *parser) interp(t Token, prefix Expr) *Interp {
	e := &Interp{Pos: t.Pos, Prefix: prefix}
	offset := 2 // s followed by the opening quote
	if prefix != nil {
		e.Pos = prefix.Position()
		e.PrefixEnd = t.Pos
		offset = 1 // token starts at the opening quote
	}
	raw := t.Text[1 : len(t.Text)-1]
	// Columns in raw are relative to the opening quote.
	col := func(i int) int { return t.Pos.Col + offset + i }
	var seg strings.Builder
	endPart := func() {
		text, err := strconv.Unquote(`"` + seg.String() + `"`)
		if err != nil {
			p.errorf(t.Pos, "invalid string literal s\"%s\"", raw)
		}
		e.Parts = append(e.Parts, text)
		seg.Reset()
	}
	for i := 0; i < len(raw); {
		c := raw[i]
		next := byte(0)
		if i+1 < len(raw) {
			next = raw[i+1]
		}
		switch {
		case c == '\\' && next != 0:
			seg.WriteString(raw[i : i+2])
			i += 2
		case c == '$' && next == '$':
			seg.WriteByte('$')
			i += 2
		case c == '$' && next == '{':
			end := matchingBrace(raw, i+2)
			if end < 0 {
				p.errorf(diag.Pos{File: t.Pos.File, Line: t.Pos.Line, Col: col(i)}, "${ is not closed in interpolated string")
				return e
			}
			endPart()
			e.Exprs = append(e.Exprs, p.subExpr(raw[i+2:end], diag.Pos{File: t.Pos.File, Line: t.Pos.Line, Col: col(i + 2)}))
			i = end + 1
		case c == '$' && next != '_' && isLetter(next):
			j := i + 1
			for j < len(raw) && (isLetter(raw[j]) || isDigit(raw[j])) {
				j++
			}
			endPart()
			e.Exprs = append(e.Exprs, &Ident{Pos: diag.Pos{File: t.Pos.File, Line: t.Pos.Line, Col: col(i + 1)}, Name: raw[i+1 : j]})
			i = j
		case c == '$':
			p.errorf(diag.Pos{File: t.Pos.File, Line: t.Pos.Line, Col: col(i)}, "in an interpolated string, write $name, ${expression}, or $$ for a dollar sign")
			i++
		default:
			seg.WriteByte(c)
			i++
		}
	}
	endPart()
	return e
}

// matchingBrace finds the '}' closing a '${' whose contents start at
// from, skipping nested braces and string literals. It returns -1 if
// there is none.
func matchingBrace(s string, from int) int {
	end := braceEnd([]byte(s), from)
	if end < 0 {
		return -1
	}
	return end - 1
}

// subExpr parses the expression inside ${...}, starting at pos.
func (p *parser) subExpr(src string, pos diag.Pos) (x Expr) {
	toks, _ := lexAt(pos.File, []byte(src), pos.Line, pos.Col, p.diags, p.compiler)
	sub := &parser{iterationOperators: p.iterationOperators, headParentheses: p.headParentheses, toks: toks, diags: p.diags, imports: p.imports, compiler: p.compiler, spans: p.spans, spanSeen: p.spanSeen, inDerivation: p.inDerivation}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
			x = &StringLit{Pos: pos}
		}
	}()
	sub.skipSemis()
	if sub.at(EOF) {
		p.errorf(pos, "empty ${} in interpolated string")
		return &StringLit{Pos: pos}
	}
	x = sub.expr()
	sub.skipSemis()
	if !sub.at(EOF) {
		sub.errorf(sub.tok().Pos, "unexpected %s in interpolated expression", sub.tok().Kind)
	}
	return x
}

// testDecl parses `test "name" { ... }`. (`test` is not a keyword, so
// it remains a usable name.)
func (p *parser) testDecl() (td *TestDecl) {
	defer p.recoverDecl(func() { td = nil })
	pos := p.next().Pos
	name := p.next()
	v, err := strconv.Unquote(name.Text)
	if err != nil {
		p.errorf(name.Pos, "invalid string literal %s", name.Text)
	}
	td = &TestDecl{Pos: pos, Name: v}
	if p.at(LParen) {
		p.next()
		p.list(RParen, "a parameter", func() {
			pname := p.expect(TIdent, "(parameter name)")
			p.expect(Colon, "after parameter name")
			td.Params = append(td.Params, &Param{Pos: pname.Pos, Name: pname.Text, Type: p.typeExpr()})
		})
	}
	td.Body = p.block()
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after test, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return td
}

func (p *parser) ruleDecl() (r *RuleDecl) {
	defer p.recoverDecl(func() { r = nil; p.noLambda = false })
	pos := p.next().Pos
	name := p.expect(TIdent, "(rule name)")
	r = &RuleDecl{Pos: pos, Name: name.Text}
	p.expect(LParen, "to start the rule's variables")
	p.list(RParen, "a variable", func() {
		pname := p.expect(TIdent, "(variable name)")
		p.expect(Colon, "after variable name")
		r.Params = append(r.Params, &Param{Pos: pname.Pos, Name: pname.Text, Type: p.typeExpr()})
	})
	p.expect(LBrace, "to start the rule")
	p.skipNewlines()
	// `a >= b => ...` is not a lambda.
	p.noLambda = true
	r.Premises = p.andList()
	p.noLambda = false
	p.skipNewlines()
	p.expect(Arrow, "between the premises and the conclusions (write `premises => conclusions`)")
	p.skipNewlines()
	r.Conclusions = p.andList()
	p.skipSemis()
	p.expect(RBrace, "to end the rule")
	if !p.at(Semi) && !p.at(EOF) {
		p.errorf(p.tok().Pos, "expected end of line after rule, found %s", p.tok().Kind)
		panic(bailout{})
	}
	return r
}

// andList parses expressions joined by `and`.
func (p *parser) andList() []Expr {
	list := []Expr{p.expr()}
	for p.at(KwAnd) {
		p.next()
		p.skipNewlines()
		list = append(list, p.expr())
	}
	return list
}

func (p *parser) fieldDoc(pos diag.Pos) string {
	line := pos.Line - 1
	var lines []string
	for i := len(p.comments) - 1; i >= 0; i-- {
		comment := p.comments[i]
		if comment.Pos.Line > line {
			continue
		}
		if comment.Pos.Line != line || !strings.HasPrefix(comment.Text, "//") {
			break
		}
		// A trailing comment belongs to the preceding field or declaration.
		trailing := false
		for _, token := range p.toks {
			if token.Pos.Line == line && token.Pos.Col < comment.Pos.Col && token.Kind != Semi {
				trailing = true
				break
			}
		}
		if trailing {
			break
		}
		lines = append(lines, strings.TrimSpace(strings.TrimPrefix(comment.Text, "//")))
		line--
	}
	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return strings.Join(lines, "\n")
}

// Distinguish the binding modifier from ordinary calls of a function named async.
func (p *parser) atAsyncBinding() bool {
	if !p.at(TIdent) || p.tok().Text != "async" || p.peekKind() != LParen {
		return false
	}
	depth := 0
	for i := p.i + 1; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case LParen:
			depth++
		case RParen:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && (p.toks[i+1].Kind == TIdent || p.toks[i+1].Kind == Underscore)
			}
		case EOF:
			return false
		}
	}
	return false
}

func (p *parser) tupleBindingAhead() bool {
	depth := 0
	for i := p.i; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case LParen:
			depth++
		case RParen:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Kind == Assign
			}
		case EOF:
			return false
		}
	}
	return false
}

// testPattern extends match patterns with unbound fact-qualified type tests.
func (p *parser) testPattern() Pattern {
	if p.at(TIdent) {
		start := p.i
		p.next()
		for p.at(Dot) {
			p.next()
			p.expect(TIdent, "after '.'")
		}
		typed := p.at(LBrack) || p.at(KwWhere) || p.at(Pipe)
		if p.at(LBrack) {
			depth := 0
			for i := p.i; i < len(p.toks); i++ {
				if p.toks[i].Kind == LBrack {
					depth++
				}
				if p.toks[i].Kind == RBrack {
					depth--
					if depth == 0 {
						typed = i+1 >= len(p.toks) || p.toks[i+1].Kind != Dot
						break
					}
				}
			}
		}
		p.i = start
		if typed {
			t := p.typeExpr()
			return &TypePat{Pos: t.Pos, Type: t}
		}
	}
	if p.at(LParen) && p.patternTestTypeAhead() {
		t := p.typeExpr()
		return &TypePat{Pos: t.Pos, Type: t}
	}
	return p.pattern()
}

// Parentheses introduce tuple/parenthesized patterns except when followed by
// type annotation syntax. Nested arrows remain inside their own parentheses.
func (p *parser) patternTestTypeAhead() bool {
	depth := 0
	for i := p.i; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case LParen:
			depth++
		case RParen:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && ((p.toks[i+1].Kind == TIdent && p.toks[i+1].Text == "uses") || p.toks[i+1].Kind == Arrow || p.toks[i+1].Kind == KwWhere || p.toks[i+1].Kind == Pipe)
			}
		case EOF:
			return false
		}
	}
	return false
}

// iterationPatternAhead recognizes an iteration binding followed by in.
// A parenthesized tuple is a binding only when in follows its closing paren.
func (p *parser) iterationPatternAhead() bool {
	if p.at(TIdent) || p.at(Underscore) {
		return p.peekKind() == TIdent && p.toks[p.i+1].Text == "in"
	}
	if !p.at(LParen) {
		return false
	}
	depth := 0
	for i := p.i; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case LParen:
			depth++
		case RParen:
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Kind == TIdent && p.toks[i+1].Text == "in"
			}
		case EOF:
			return false
		}
	}
	return false
}

// iterationPattern parses a binding pattern and its following in token.
func (p *parser) iterationPattern() Pattern {
	var pattern Pattern
	if p.at(TIdent) {
		token := p.next()
		pattern = &VariantPat{Pos: token.Pos, End: token.End, Path: []string{token.Text}}
	} else {
		pattern = p.pattern()
	}
	p.iterationIn()
	return pattern
}

// iterationIn consumes the in after an iteration pattern.
func (p *parser) iterationIn() {
	in := p.expect(TIdent, "in after the iteration pattern")
	if in.Text != "in" {
		p.errorf(in.Pos, "expected in after the iteration pattern")
	} else if p.iterationOperators != nil {
		*p.iterationOperators = append(*p.iterationOperators, in.Pos)
	}
}

// irrefutable reports whether a pattern matches every value it can be
// given: a name, _, or a tuple of those.
func irrefutable(pattern Pattern) bool {
	switch pattern := pattern.(type) {
	case *WildcardPat:
		return true
	case *VariantPat:
		return len(pattern.Path) == 1 && !pattern.Context && !pattern.Braces && !pattern.Positional && pattern.Owner == nil
	case *TuplePat:
		for _, elem := range pattern.Elems {
			if !irrefutable(elem) {
				return false
			}
		}
		return true
	}
	return false
}

// forHeader parses the parenthesized header of a derive list comprehension.
func (p *parser) forHeader() *For {
	pos := p.expect(KwFor, "").Pos
	p.expect(LParen, "after for")
	p.skipNewlines()
	pattern := p.iterationPattern()
	p.skipNewlines()
	items := p.expr()
	p.skipNewlines()
	p.expect(RParen, "after the iteration source")
	loop := &For{Pos: pos, Pattern: pattern, NamePos: pattern.Position(), Items: items}
	switch pattern := pattern.(type) {
	case *VariantPat:
		loop.Name = pattern.Path[0]
	case *WildcardPat:
		loop.Name = "_"
	}
	return loop
}

// comprehensionAhead reports whether `for {` starts a comprehension:
// its first line is a generator, a pattern followed by in. No statement
// of a `for { }` loop body starts that way.
func (p *parser) comprehensionAhead() bool {
	if p.peekKind() != LBrace {
		return false
	}
	save := p.i
	defer func() { p.i = save }()
	p.next() // for
	p.next() // {
	p.skipSemis()
	return p.generatorAhead()
}

// generatorAhead reports whether a comprehension's generator line
// starts here: a pattern followed by in. Besides the loop-header
// patterns, a generator may have any match pattern (.Some(v) in xs),
// so it looks for in at the line's top level, before anything a pattern
// cannot contain.
func (p *parser) generatorAhead() bool {
	if p.iterationPatternAhead() {
		return true
	}
	switch p.tok().Kind {
	case TIdent, Underscore, Dot, LParen, LBrack, TInt, TFloat, TRune, TString, KwTrue, KwFalse, Minus:
	default:
		return false
	}
	depth := 0
	for i := p.i; i < len(p.toks); i++ {
		switch t := p.toks[i]; t.Kind {
		case LParen, LBrack, LBrace:
			depth++
		case RParen, RBrack, RBrace:
			if depth == 0 {
				return false
			}
			depth--
		case TIdent:
			if depth == 0 && t.Text == "in" {
				return i > p.i
			}
		case Semi:
			if depth == 0 {
				return false
			}
		case Assign, Arrow, KwFor, KwIf, KwElse, KwMatch, KwReturn, KwGenerate, KwYield, KwBreak, KwContinue, EOF:
			return false
		}
	}
	return false
}

// comprehension parses `for { clauses } yield value`, at for. It
// becomes the generator it means: each generator clause a loop around
// the clauses after it, each filter an if around them, each binding a
// statement before them, and the yield innermost.
func (p *parser) comprehension() *Generate {
	pos := p.next().Pos
	saved := p.noRecordLit
	defer func() { p.noRecordLit = saved }()
	p.noRecordLit = false // within the braces, '{' is a literal again
	p.expect(LBrace, "after for")
	var clauses []any // *For, *If, *Match (a refutable generator's), or a binding Stmt
	for {
		p.skipSemis()
		if p.at(RBrace) {
			break
		}
		if p.at(EOF) {
			p.errorf(pos, "comprehension is not closed (missing '}')")
			panic(bailout{})
		}
		clauses = append(clauses, p.comprehensionClause())
		if !p.at(Semi) && !p.at(RBrace) {
			p.errorf(p.tok().Pos, "expected end of line or '}' after the comprehension line, found %s", p.tok().Kind)
			panic(bailout{})
		}
	}
	p.next()              // }
	p.noRecordLit = saved // the yielded value is in the enclosing context
	if !p.at(KwYield) {
		if p.at(Semi) && p.peekKind() == KwYield {
			p.errorf(p.toks[p.i+1].Pos, "a comprehension's yield goes on the line of its closing '}', as in } yield value")
		} else {
			p.errorf(p.tok().Pos, "a comprehension ends with } yield value; a for { } loop cannot start with a generator such as x in xs")
		}
		panic(bailout{})
	}
	y := p.next()
	value := p.expr()
	// The blocks end with the yielded value, which their names reach.
	end := p.toks[p.i-1].End
	body := &Block{Pos: y.Pos, End: end, Stmts: []Stmt{&ExprStmt{X: &Yield{Pos: y.Pos, Value: value}}}}
	for i := len(clauses) - 1; i >= 0; i-- {
		switch clause := clauses[i].(type) {
		case *For:
			clause.Body = body
			body = &Block{Pos: clause.Pos, End: end, Stmts: []Stmt{&ExprStmt{X: clause}}}
		case *Match:
			// for _elem in source { match _elem { pattern => { rest }, _ => {} } }
			clause.Arms[0].Body = body
			loop := clause.X.(*For)
			clause.X = &Ident{Pos: loop.NamePos, Name: loop.Name}
			loop.Body = &Block{Pos: clause.Pos, End: end, Stmts: []Stmt{&ExprStmt{X: clause}}}
			body = &Block{Pos: loop.Pos, End: end, Stmts: []Stmt{&ExprStmt{X: loop}}}
		case *If:
			clause.Then = body
			body = &Block{Pos: clause.Pos, End: end, Stmts: []Stmt{&ExprStmt{X: clause}}}
		case Stmt:
			body.Stmts = append([]Stmt{clause}, body.Stmts...)
			body.Pos = stmtPos(clause)
		}
	}
	return &Generate{Pos: pos, Body: body, Comprehension: true}
}

// comprehensionClause parses one line of a comprehension: a generator
// `pattern in source`, a filter `if cond`, or a binding.
func (p *parser) comprehensionClause() any {
	switch {
	case p.generatorAhead():
		var pattern Pattern
		var in diag.Pos
		if p.iterationPatternAhead() {
			pattern = p.iterationPattern()
			in = p.toks[p.i-1].Pos
		} else {
			pattern = p.pattern()
			in = p.tok().Pos
			p.iterationIn()
		}
		pos := pattern.Position()
		f := &For{Pos: pos, Pattern: pattern, NamePos: pos}
		if !irrefutable(pattern) {
			// The values that do not match are skipped: the loop binds
			// each one to a name no source can write, and a match tests
			// it. The name is placed at in, where no symbol of the
			// pattern is, so references to it never resolve to one.
			f.Pattern = nil
			f.NamePos = in
			f.Name = fmt.Sprintf("_elem_%d_%d", in.Line, in.Col)
			f.Items = p.expr()
			return &Match{Pos: pos, Filter: true, X: f, Arms: []*Arm{{Pattern: pattern}, {Pattern: &WildcardPat{Pos: pos}, Body: &Block{Pos: pos, End: pos}}}}
		}
		switch pattern := pattern.(type) {
		case *VariantPat:
			f.Name = pattern.Path[0]
		case *WildcardPat:
			f.Name = "_"
		}
		f.Items = p.expr()
		return f
	case p.at(KwIf):
		// The condition is an if head: a bare `Name {` is not a record
		// literal, so a block after it gets the message below.
		x := &If{Pos: p.next().Pos, Cond: p.headExpr()}
		if p.at(LBrace) || p.at(KwElse) {
			p.errorf(p.tok().Pos, "a comprehension's filter is if cond alone, without a block or else")
			panic(bailout{})
		}
		return x
	}
	pos := p.tok().Pos
	stmt := p.statement()
	switch s := stmt.(type) {
	case *Binding:
		if !s.Lazy && s.AsyncScope == nil {
			return s
		}
	case *TupleBinding:
		return s
	}
	p.errorf(pos, "a comprehension line is a generator x in xs, a filter if cond, or a binding name = value")
	panic(bailout{})
}

func stmtPos(s Stmt) diag.Pos {
	switch s := s.(type) {
	case *Binding:
		return s.Pos
	case *TupleBinding:
		return s.Pos
	}
	return diag.Pos{}
}
