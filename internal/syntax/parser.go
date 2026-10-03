package syntax

import (
	"errors"
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
	p := &parser{toks: toks, comments: comments, diags: diags, imports: map[string]bool{}}
	f := &File{Path: path, Source: string(src), Comments: comments}
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
		case p.at(TIdent) && p.tok().Text == "class" && p.peekKind() == TIdent:
			if cd := p.classDecl(); cd != nil {
				f.Classes = append(f.Classes, cd)
			}
		case p.at(TIdent) && p.tok().Text == "instance" && p.peekKind() == TIdent:
			if id := p.instanceDecl(); id != nil {
				f.Instances = append(f.Instances, id)
				f.Funcs = append(f.Funcs, id.Methods...)
			}
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
		case p.at(TIdent) && p.tok().Text == "test" && p.peekKind() == TString:
			if td := p.testDecl(); td != nil {
				f.Tests = append(f.Tests, td)
			}
		default:
			p.errorf(p.tok().Pos, "expected a declaration ('fn', 'pred', 'rule', 'type', 'class', 'instance', or 'test'), found %s", p.tok().Kind)
			p.syncTopLevel()
		}
	}
	return f
}

// bailout aborts parsing of the current declaration after an error.
type bailout struct{}

type parser struct {
	comments []Comment
	toks     []Token
	i        int
	diags    *diag.List
	// noLambda is set while parsing rule premises, where `x =>` ends
	// the premises instead of starting a lambda.
	noLambda bool
	// imports holds the names of the file's imported packages.
	imports map[string]bool
	// noRecordLit is set while parsing a scope's policy, where a '{'
	// after a name starts the scope's block.
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
		if (p.at(KwFn) || p.at(KwType)) && (p.i == 0 || p.toks[p.i-1].Kind == Semi) {
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
	switch {
	case p.at(KwSealed):
		td.Kind = SealedType
		p.next()
		p.expect(LBrace, "to start the list of variants")
		p.list(RBrace, "a variant", func() {
			vname := p.expect(TIdent, "(variant name)")
			v := &VariantDecl{Pos: vname.Pos, Name: vname.Text}
			if p.at(LBrace) {
				v.Fields = p.fieldDecls()
			}
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
		fname := p.expect(TIdent, "(field name)")
		p.expect(Colon, "after field name")
		field := &FieldDecl{Pos: fname.Pos, Name: fname.Text, Type: p.typeExpr(), Doc: p.fieldDoc(fname.Pos)}
		if p.at(Assign) {
			p.next()
			field.Default = p.expr()
		}
		if p.at(TIdent) && p.tok().Text == "go" && p.peekKind() == LBrace {
			p.next()
			p.next()
			p.list(RBrace, "a Go struct tag", func() {
				key := p.expect(TIdent, "(Go struct tag name)")
				p.expect(Colon, "after the Go struct tag name")
				value := p.expect(TString, "(Go struct tag value)")
				decoded, err := strconv.Unquote(value.Text)
				if err != nil {
					p.errorf(value.Pos, "invalid Go struct tag value: %v", err)
					panic(bailout{})
				}
				field.GoTags = append(field.GoTags, GoTag{Pos: key.Pos, Name: key.Text, Value: decoded})
			})
		}
		fields = append(fields, field)
	})
	return fields
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
	fn.Uses = p.uses()
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
	if !p.at(KwWhere) {
		return t
	}
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
			if len(clause.Or) > 0 && (len(t.Where) > 0 || p.at(KwAnd)) {
				p.errorf(clause.Pos, "mixing and with or needs parentheses: write (p or q) and r")
			}
		}
		t.Where = append(t.Where, clause)
		if !p.at(KwAnd) {
			return t
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
		p.skipNewlines()
		for !p.at(RParen) {
			params = append(params, p.typeExpr())
			p.skipNewlines()
			if !p.at(Comma) {
				break
			}
			p.next()
			p.skipNewlines()
		}
		p.expect(RParen, "to close the type")
		uses := p.uses()
		if uses != nil && !p.at(Arrow) {
			p.errorf(p.tok().Pos, "expected => after a function type's effects")
			panic(bailout{})
		}
		if p.at(Arrow) {
			p.next()
			return &TypeExpr{Pos: pos, Func: &FuncTypeExpr{Params: params, Uses: uses, Result: p.typeExpr()}}
		}
		if len(params) != 1 {
			p.errorf(pos, "expected => after a function type's parameters")
			panic(bailout{})
		}
		return params[0]
	}
	t := p.expect(TIdent, "(type name)")
	te := &TypeExpr{Pos: t.Pos, Name: p.qualify(t)}
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
	return te
}

func (p *parser) block() *Block {
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
		var stmt Stmt
		switch {
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
		case p.at(TIdent) && p.peekKind() == Colon:
			name := p.next()
			p.next() // ':'
			typ := p.typeExpr()
			p.expect(Assign, "after the binding's type")
			stmt = &Binding{Pos: name.Pos, Name: name.Text, Type: typ, Value: p.expr()}
		default:
			stmt = &ExprStmt{X: p.expr()}
		}
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

// Binary operator precedence, lowest first.
var precedence = map[Kind]int{
	PipeGt: 1,
	OrOr:   2,
	AndAnd: 3,
	Eq:     4, NotEq: 4, Lt: 4, LtEq: 4, Gt: 4, GtEq: 4,
	Plus: 5, Minus: 5,
	Star: 6, Slash: 6, Pct: 6,
}

func (p *parser) expr() Expr { return p.binary(1) }

func (p *parser) binary(minPrec int) Expr {
	start := p.tok().Pos
	x := p.unary()
	for {
		op := p.tok()
		prec, ok := precedence[op.Kind]
		if !ok || prec < minPrec {
			return x
		}
		end := p.toks[p.i-1].End
		p.next()
		p.skipNewlines() // an operator at the end of a line continues the expression
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

func (p *parser) unary() Expr {
	if p.at(Minus) || p.at(Not) {
		op := p.next()
		return &Unary{Pos: op.Pos, Op: op.Kind, X: p.unary()}
	}
	start := p.tok().Pos
	return p.postfix(p.primary(), start)
}

func (p *parser) postfix(x Expr, start diag.Pos) Expr {
	for {
		funEnd := p.toks[p.i-1].End
		var typeArgs []*TypeExpr
		_, isID := x.(*Ident)
		_, isSel := x.(*Selector)
		if (isID || isSel) && p.at(LBrack) {
			// Explicit type arguments of a call: empty[Int](), or of a
			// method call: xs.map[String](f).
			p.next()
			for {
				typeArgs = append(typeArgs, p.typeExpr())
				if !p.at(Comma) {
					break
				}
				p.next()
			}
			p.expect(RBrack, "to end the type arguments")
			if !p.at(LParen) {
				p.errorf(p.tok().Pos, "expected '(' to call the function after its type arguments")
				panic(bailout{})
			}
		}
		switch {
		case p.at(LParen):
			call := &Call{Start: start, Pos: p.next().Pos, Fun: x, FunEnd: funEnd, TypeArgs: typeArgs}
			saved := p.noRecordLit
			p.noRecordLit = false // within the parentheses, '{' is a literal again
			p.skipNewlines()
			for !p.at(RParen) {
				arg := Argument{Pos: p.tok().Pos}
				if p.at(TIdent) && p.peekKind() == Colon {
					label := p.next()
					arg.Name, arg.NameEnd = label.Text, label.End
					p.next()
					p.skipNewlines()
				}
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
			name := p.expect(TIdent, "after '.'")
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
	case *Ident:
		return true
	case *Selector:
		_, ok := x.X.(*Ident)
		return ok
	}
	return false
}

func (p *parser) recordLit(typ Expr) Expr {
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
	c := &Copy{Pos: pos, X: x}
	p.expect(LParen, "")
	p.list(RParen, "a field update", func() {
		first := p.expect(TIdent, "(field name)")
		u := &CopyUpdate{Pos: first.Pos, Path: []string{first.Text}}
		for p.at(Dot) {
			p.next()
			u.Path = append(u.Path, p.expect(TIdent, "(field name)").Text)
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
		u.Value = p.expr()
		c.Updates = append(c.Updates, u)
	})
	return c
}

func (p *parser) matchExpr() Expr {
	m := &Match{Pos: p.next().Pos}
	p.expect(LParen, "after 'match' (write 'match (value) { ... }')")
	p.skipNewlines()
	m.X = p.expr()
	p.skipNewlines()
	p.expect(RParen, "to close the matched value")
	p.expect(LBrace, "to start the match arms")
	p.list(RBrace, "a match arm", func() {
		pat := p.pattern()
		p.expect(Arrow, "after the pattern")
		p.skipNewlines()
		m.Arms = append(m.Arms, &Arm{Pattern: pat, Body: p.expr()})
	})
	return m
}

func (p *parser) pattern() Pattern {
	t := p.tok()
	switch t.Kind {
	case Underscore:
		p.next()
		return &WildcardPat{Pos: t.Pos}
	case TInt, TFloat, TRune, TString, KwTrue, KwFalse, Minus:
		return &LitPat{Pos: t.Pos, Value: p.unary()}
	case LBrack:
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
			lp.Elems = append(lp.Elems, p.pattern())
		})
		return lp
	case TIdent:
		if p.peekKind() == Colon {
			p.next()
			p.next()
			return &TypePat{Pos: t.Pos, Name: t.Text, Type: p.typeExpr()}
		}
		vp := &VariantPat{Pos: t.Pos, Path: []string{p.qualify(p.next())}}
		for p.at(Dot) {
			p.next()
			vp.Path = append(vp.Path, p.expect(TIdent, "(variant name)").Text)
		}
		if p.at(LBrace) {
			p.next()
			vp.Braces = true
			p.list(RBrace, "a field pattern", func() {
				f := p.expect(TIdent, "(field name)")
				fp := &FieldPat{Pos: f.Pos, Field: f.Text}
				if p.at(Colon) {
					p.next()
					fp.Pattern = p.pattern()
				}
				vp.Fields = append(vp.Fields, fp)
			})
		}
		return vp
	}
	p.errorf(t.Pos, "expected a pattern, found %s", t.Kind)
	panic(bailout{})
}

func (p *parser) primary() Expr {
	t := p.tok()
	switch t.Kind {
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
		return p.interp(t)
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
		return &Ident{Pos: t.Pos, Name: p.qualify(t)}
	case LBrack:
		p.next()
		lit := &ListLit{Pos: t.Pos}
		p.list(RBrack, "a list element", func() {
			p.skipNewlines()
			lit.Elems = append(lit.Elems, p.expr())
		})
		return lit
	case LParen:
		if !p.noLambda && p.lambdaAhead() {
			return p.lambda()
		}
		p.next()
		p.skipNewlines()
		x := p.expr()
		p.skipNewlines()
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
	p.expect(LParen, "after 'if' (conditions are written as 'if (cond)')")
	p.skipNewlines()
	e.Cond = p.expr()
	p.skipNewlines()
	p.expect(RParen, "to close the condition")
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
		path, err := strconv.Unquote(strings.TrimSpace(rest))
		if err != nil {
			p.errorf(p.goPos(t.Pos, i, 0), "expected `import \"path\"` in unsafe go block")
		} else {
			gc.Imports = append(gc.Imports, path)
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
func (p *parser) interp(t Token) *Interp {
	e := &Interp{Pos: t.Pos}
	raw := t.Text[1 : len(t.Text)-1]
	// Columns in raw are relative to the opening quote, after the s.
	col := func(i int) int { return t.Pos.Col + 2 + i }
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
	depth := 1
	for i := from; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		case '"':
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' {
					i++
				}
			}
		}
	}
	return -1
}

// subExpr parses the expression inside ${...}, starting at pos.
func (p *parser) subExpr(src string, pos diag.Pos) (x Expr) {
	toks, _ := lexAt(pos.File, []byte(src), pos.Line, pos.Col, p.diags)
	sub := &parser{toks: toks, diags: p.diags, imports: p.imports}
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
