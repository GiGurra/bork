package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/std"
	"github.com/GiGurra/bork/internal/syntax"
)

// A definition witness is an ordinary function standing for every expansion
// of a derive template method or helper. Target-dependent parts become holes:
// Invalid-typed calls that still evaluate their arguments but carry no facts.
// Target and helper type parameters are Invalid as well. Staged control
// becomes a branch on an opaque condition, so each staged copy is checked
// once. Witnesses are checked only after the program checks, with their own
// diagnostics discarded. Code that fails to check becomes a hole on the next
// attempt; a witness that still fails, or cannot be lowered, is dropped. The
// fact checker then reports only failures that no target can avoid (see
// derive_witness_taint.go).
type deriveWitnessBuilder struct {
	c       *checker
	pkg     *Package
	shape   map[string]bool // the source package's aliases of bork/shape
	names   map[string]bool // target and helper type parameters
	helpers map[*syntax.FuncDecl]*Func
	// repairs are source positions whose expression becomes a hole, or
	// whose unannotated lambda parameter becomes dependent, after an earlier
	// attempt to check the witness failed there.
	repairs map[diag.Pos]bool
}

type deriveWitnessSource struct {
	decl    *syntax.FuncDecl
	targets []*syntax.TypeParam
	pkg     *Package
	fn      *Func
}

// Attempts per witness: each one turns the positions that failed to check
// into holes. Most definitions need none.
const deriveWitnessAttempts = 4

// DefinitionWitnesses enables definition witnesses. Tests turn it off to
// show that witnesses never change the emitted program.
var DefinitionWitnesses = true

func (c *checker) checkDeriveWitnesses(files []*syntax.File) {
	if !DefinitionWitnesses {
		return
	}
	b := &deriveWitnessBuilder{c: c, helpers: map[*syntax.FuncDecl]*Func{}}
	var sources []*deriveWitnessSource
	for _, file := range files {
		if file.Prelude {
			continue
		}
		c.inFile(file)
		pkg := c.pkg
		// Shipped standard templates are validated by their own package tests.
		if strings.HasPrefix(pkg.Path, std.Prefix) && !pkg.Root {
			continue
		}
		for _, helper := range file.DeriveHelpers {
			if pkg.deriveHelpers[helper.Name] == helper {
				sources = append(sources, &deriveWitnessSource{decl: helper, pkg: pkg})
			}
		}
		for _, template := range file.Templates {
			for _, method := range template.Methods {
				sources = append(sources, &deriveWitnessSource{decl: method, targets: template.TypeParams, pkg: pkg})
			}
		}
	}
	if len(sources) == 0 {
		return
	}
	if c.deriveCalls == nil {
		c.deriveCalls = map[*syntax.Call]*Func{}
	}
	savedDiags, savedPkg := c.diags, c.pkg
	defer func() { c.diags, c.pkg = savedDiags, savedPkg }()
	// Declare every signature first: witness bodies call helper witnesses.
	for _, source := range sources {
		c.diags = &diag.List{}
		b.enter(source)
		source.fn = b.declare(source)
		c.witnessNodes(reflect.ValueOf(source.fn))
		if source.fn != nil && source.targets == nil {
			b.helpers[source.decl] = source.fn
		}
	}
	for _, source := range sources {
		if source.fn == nil {
			continue
		}
		b.enter(source)
		b.repairs = map[diag.Pos]bool{}
		for attempt := 0; attempt < deriveWitnessAttempts; attempt++ {
			body := cloneSyntax(source.decl.Body)
			b.pin(reflect.ValueOf(body))
			source.fn.Decl.Body = b.block(body)
			c.witnessNodes(reflect.ValueOf(source.fn.Decl.Body))
			c.diags = &diag.List{}
			snapshot := c.witnessSnapshot()
			failed := !c.checkWitness(source.fn)
			for _, d := range c.diags.Sorted() {
				// Bindings whose only reads became holes are not failures.
				if d.Code != "binding.unused" && d.Severity != "warning" {
					failed = true
					if !b.repairs[d.Pos] {
						b.repairs[d.Pos] = true
					}
				}
			}
			if !failed && snapshot.unchanged(c) {
				c.info.DefinitionWitnesses = append(c.info.DefinitionWitnesses, source.fn)
				break
			}
			snapshot.restore(c)
		}
	}
}

func (b *deriveWitnessBuilder) enter(source *deriveWitnessSource) {
	b.pkg = source.pkg
	b.c.pkg = source.pkg
	b.shape = map[string]bool{}
	for alias, imported := range source.pkg.imports {
		if imported.Path == "bork/shape" {
			b.shape[alias] = true
		}
	}
	b.names = map[string]bool{}
	for _, parameter := range source.targets {
		b.names[parameter.Name] = true
	}
	for _, parameter := range source.decl.TypeParams {
		b.names[parameter.Name] = true
	}
}

// checkWitness reports whether fn checked without panicking. The checker
// assumes checked programs; Invalid types can reach code that does not
// expect them, and such a witness is dropped like one that fails to check.
func (c *checker) checkWitness(fn *Func) (ok bool) {
	scopes, function, params := c.scopes, c.fn, c.typeParams
	defer func() {
		if recover() != nil {
			c.scopes, c.fn, c.typeParams, c.inPrelude = scopes, function, params, false
			ok = false
		}
	}()
	c.checkFunc(fn)
	return true
}

// witnessNodes records the syntax a witness was made of. Checking it
// records types, bindings and calls for that syntax at the template's own
// source positions; purgeWitnessNodes removes them once witnesses are lowered,
// so editor and lint queries by position see only the program.
func (c *checker) witnessNodes(v reflect.Value) {
	if c.witnessSyntax == nil {
		c.witnessSyntax = map[any]bool{}
	}
	var walk func(reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Pointer:
			if v.IsNil() || !v.CanInterface() {
				return
			}
			if _, isFunc := v.Interface().(*Func); isFunc {
				walk(reflect.ValueOf(v.Interface().(*Func).Decl))
				return
			}
			if c.witnessSyntax[v.Interface()] {
				return
			}
			c.witnessSyntax[v.Interface()] = true
			walk(v.Elem())
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(v.Type()) {
				walk(v.Field(index))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	walk(v)
}

func (c *checker) purgeWitnessNodes() {
	if len(c.witnessSyntax) == 0 {
		return
	}
	info := reflect.ValueOf(c.info).Elem()
	for i := 0; i < info.NumField(); i++ {
		field := info.Field(i)
		if field.Kind() != reflect.Map || field.IsNil() {
			continue
		}
		switch field.Type().Key().Kind() {
		case reflect.Pointer, reflect.Interface:
		default:
			continue
		}
		field = reflect.NewAt(field.Type(), field.Addr().UnsafePointer()).Elem()
		for _, key := range field.MapKeys() {
			if key.Kind() == reflect.Interface && key.IsNil() {
				continue
			}
			if c.witnessSyntax[key.Interface()] {
				field.SetMapIndex(key, reflect.Value{})
			}
		}
	}
	for call := range c.deriveCalls {
		if c.witnessSyntax[call] {
			delete(c.deriveCalls, call)
		}
	}
	c.witnessSyntax = nil
}

// Witness checking must leave no trace in the emitted program. A witness that
// would add an instance, expansion, comptime or captured file is dropped.
type deriveWitnessSnapshot struct {
	expanded, instances, comptimes, comptimeSyntax, embeds, reads, batches, mocks, types int
}

func (c *checker) witnessSnapshot() deriveWitnessSnapshot {
	i := c.info
	return deriveWitnessSnapshot{len(i.ExpandedFunctions), len(i.ClassInstances), len(i.Comptimes), len(i.comptimeSyntax), len(i.Embeds), len(i.BuildReads), len(i.InterpolationBatches), len(i.Mocks), len(i.TypeOrder)}
}

func (s deriveWitnessSnapshot) unchanged(c *checker) bool { return s == c.witnessSnapshot() }

func (s deriveWitnessSnapshot) restore(c *checker) {
	i := c.info
	i.ExpandedFunctions = i.ExpandedFunctions[:s.expanded]
	i.ClassInstances = i.ClassInstances[:s.instances]
	i.Comptimes = i.Comptimes[:s.comptimes]
	i.comptimeSyntax = i.comptimeSyntax[:s.comptimeSyntax]
	i.Embeds = i.Embeds[:s.embeds]
	i.BuildReads = i.BuildReads[:s.reads]
	i.InterpolationBatches = i.InterpolationBatches[:s.batches]
	i.Mocks = i.Mocks[:s.mocks]
	i.TypeOrder = i.TypeOrder[:s.types]
}

// declare clones the source signature as an ordinary function. Its type
// parameters stand for target-dependent types, so they are Invalid too.
func (b *deriveWitnessBuilder) declare(source *deriveWitnessSource) *Func {
	c := b.c
	shallow := *source.decl
	shallow.Instance, shallow.Body, shallow.TypeParams = nil, nil, nil
	fd := cloneSyntax(&shallow)
	fd.Derivation = false
	b.pin(reflect.ValueOf(fd))
	fn := &Func{Decl: fd, Pkg: source.pkg, Witness: true}
	fn.Effects = c.declEffects(fn)
	c.needsOf(fn)
	fn.Result = c.resolveType(fd.Result)
	for _, p := range fd.Params {
		fn.Params = append(fn.Params, c.resolveType(p.Type))
	}
	c.ownerSignature(fn)
	c.openSignature(fn)
	if c.diags.Len() != 0 {
		return nil
	}
	// Witnesses stay out of FuncOf: code generation and editor queries
	// walk it, and no witness is part of the program.
	c.resolveFunctionConstraints(fn)
	c.checkFunctionRequirement(fn)
	if c.diags.Len() != 0 {
		return nil
	}
	return fn
}

// pin gives target-dependent annotations the type Invalid, which the checker
// accepts in any position and the reporting passes treat as dependent: type
// parameters, descriptor projections (field.Type, variant.Type, tag.Type)
// and bork/shape's descriptor and builder types, whose operations exist only
// during expansion. Their facts are dependent, so their where clauses go.
func (b *deriveWitnessBuilder) pin(v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			b.pin(v.Elem())
		}
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		if written, ok := v.Interface().(*syntax.TypeExpr); ok && b.dependentType(written) {
			b.c.info.assemblyTypes[written] = Invalid
			written.Where = nil
			return
		}
		b.pin(v.Elem())
	case reflect.Struct:
		for _, index := range walkableSyntaxFields(v.Type()) {
			b.pin(v.Field(index))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			b.pin(v.Index(i))
		}
	}
}

func (b *deriveWitnessBuilder) dependentType(written *syntax.TypeExpr) bool {
	if written.Name == "" {
		return false
	}
	if b.names[written.Name] || b.shapeName(written.Name) {
		return true
	}
	owner, member, projected := strings.Cut(written.Name, ".")
	return projected && (member == "Type" || member == "RawType") && b.pkg.imports[owner] == nil
}

func (b *deriveWitnessBuilder) hole(pos diag.Pos, result Type, args ...syntax.Expr) syntax.Expr {
	decl := &syntax.FuncDecl{Pos: pos, Name: "_derive_hole"}
	fn := &Func{Decl: decl, Pkg: b.pkg, Result: result, Witness: true, defaultsChecked: true}
	call := &syntax.Call{Start: pos, Pos: pos, End: pos, Fun: &syntax.Ident{Pos: pos, Name: decl.Name}}
	for _, arg := range args {
		param := Type(Invalid)
		if lambda, ok := arg.(*syntax.Lambda); ok {
			// A hole gives its lambdas dependent inputs and accepts any result
			// and effects, as an open callback parameter does.
			context := &FuncType{Result: Invalid, Effects: EffOpen}
			for range lambda.Params {
				context.Params = append(context.Params, Invalid)
			}
			param = context
		}
		decl.Params = append(decl.Params, &syntax.Param{Pos: arg.Position(), Name: "_"})
		fn.Params = append(fn.Params, param)
		fn.ParamConstraints = append(fn.ParamConstraints, nil)
		call.Args = append(call.Args, arg)
	}
	b.c.deriveCalls[call] = fn
	return call
}

// staged is the opaque condition of a staged branch. Unlike a value hole, it
// adds no runtime fact: an expansion removes the branch instead of testing it.
func (b *deriveWitnessBuilder) staged(pos diag.Pos) syntax.Expr {
	call := b.hole(pos, Bool).(*syntax.Call)
	b.c.deriveCalls[call].WitnessStage = true
	return call
}

func (b *deriveWitnessBuilder) shapeName(name string) bool {
	alias, _, qualified := strings.Cut(name, ".")
	return qualified && b.shape[alias]
}

func (b *deriveWitnessBuilder) block(x *syntax.Block) *syntax.Block {
	if x == nil {
		return nil
	}
	for i, stmt := range x.Stmts {
		x.Stmts[i] = b.stmt(stmt)
	}
	x.Tail = b.expr(x.Tail)
	return x
}

func (b *deriveWitnessBuilder) stmt(s syntax.Stmt) syntax.Stmt {
	switch s := s.(type) {
	case *syntax.ExprStmt:
		s.X = b.expr(s.X)
		return s
	case *syntax.Binding:
		s.Value = b.expr(s.Value)
		s.AsyncScope = b.expr(s.AsyncScope)
		return s
	}
	b.children(reflect.ValueOf(s))
	return s
}

// copy is one staged copy of a comptime for body, if any is selected.
func (b *deriveWitnessBuilder) copy(loop *syntax.For) syntax.Expr {
	then := b.loop(loop)
	b.absorb(then)
	return &syntax.If{Pos: loop.Pos, Cond: b.staged(loop.Pos), Then: then, Else: &syntax.Block{Pos: loop.Pos}}
}

// loop lowers one staged copy of a comptime for body. Its descriptor binding
// is a hole, so every use of it is target-dependent.
func (b *deriveWitnessBuilder) loop(loop *syntax.For) *syntax.Block {
	body := b.block(loop.Body)
	binding := &syntax.Binding{Pos: loop.NamePos, Name: loop.Name, Value: b.hole(loop.Pos, Invalid, b.expr(loop.Items))}
	body.Stmts = append([]syntax.Stmt{binding}, body.Stmts...)
	return body
}

func (b *deriveWitnessBuilder) expr(x syntax.Expr) syntax.Expr {
	if x != nil && b.repaired(x) {
		switch x.(type) {
		case *syntax.Block, *syntax.Lambda:
		default:
			// The replaced code may have left early; what follows it
			// cannot rely on facts it established (see stagedExit).
			call := b.hole(x.Position(), Invalid).(*syntax.Call)
			b.c.deriveCalls[call].WitnessRepair = true
			return call
		}
	}
	switch x := x.(type) {
	case nil:
		return nil
	case *syntax.Lambda:
		for _, parameter := range x.Params {
			if parameter.Type == nil && b.repairs[parameter.Pos] {
				parameter.Type = &syntax.TypeExpr{Pos: parameter.Pos, Name: "_"}
				b.c.info.assemblyTypes[parameter.Type] = Invalid
			}
		}
	case *syntax.Ident:
		if b.shapeName(x.Name) {
			return b.hole(x.Pos, Invalid)
		}
		return x
	case *syntax.RecordLit:
		// The written type names the record, even one from bork/shape.
		for _, field := range x.Fields {
			field.Value = b.expr(field.Value)
		}
		return x
	case *syntax.Comptime:
		// Native evaluation belongs to the expansion.
		return b.hole(x.Pos, Invalid)
	case *syntax.Block:
		return b.block(x)
	case *syntax.If:
		x.Then = b.block(x.Then)
		x.Else = b.expr(x.Else)
		if x.Comptime {
			x.Comptime = false
			x.Cond = b.staged(x.Pos)
			if x.Else == nil {
				// A guard may be a value; the unselected copy has none.
				x.Else = &syntax.Block{Pos: x.Pos}
			}
			// Each selected copy may have a type of its own.
			b.absorb(x.Then)
			if other, ok := x.Else.(*syntax.Block); ok {
				b.absorb(other)
			}
			return x
		}
		x.Cond = b.expr(x.Cond)
		return x
	case *syntax.Match:
		subject := x.X
		if !x.Comptime {
			subject = b.expr(x.X)
			if !b.dependentMatch(x, subject) {
				x.X = subject
				for _, arm := range x.Arms {
					arm.Body = b.expr(arm.Body)
				}
				return x
			}
		}
		// A staged match selects one arm per expansion. A runtime match of
		// a dependent value tests what only an expansion knows: its arms
		// become branches on opaque conditions, with dependent bindings.
		var result syntax.Expr = &syntax.Block{Pos: x.Pos}
		for i := len(x.Arms) - 1; i >= 0; i-- {
			arm := x.Arms[i]
			then := &syntax.Block{Pos: arm.Body.Position(), Tail: b.expr(arm.Body)}
			if !x.Comptime {
				var names []string
				patternBinders(arm.Pattern, &names)
				for _, name := range names {
					then.Stmts = append(then.Stmts, &syntax.Binding{Pos: arm.Body.Position(), Name: name, Value: b.hole(arm.Body.Position(), Invalid)})
				}
			}
			b.absorb(then)
			var cond syntax.Expr
			switch {
			case x.Comptime:
				cond = b.staged(arm.Body.Position())
			case i == 0:
				cond = b.hole(x.Pos, Bool, subject)
			default:
				cond = b.hole(arm.Body.Position(), Bool)
			}
			result = &syntax.If{Pos: arm.Body.Position(), Cond: cond, Then: then, Else: result}
		}
		return result
	case *syntax.For:
		if x.Comptime && !x.Comprehension {
			return b.copy(x)
		}
	case *syntax.ListLit:
		comprehension := false
		for _, elem := range x.Elems {
			if loop, ok := elem.(*syntax.For); ok && loop.Comptime && loop.Comprehension {
				comprehension = true
			}
		}
		if !comprehension {
			break
		}
		// The element count differs per target, so the list is a hole over
		// its staged elements and carries no length fact.
		var args []syntax.Expr
		for _, elem := range x.Elems {
			if loop, ok := elem.(*syntax.For); ok && loop.Comptime && loop.Comprehension {
				args = append(args, b.copy(loop))
				continue
			}
			args = append(args, b.expr(elem))
		}
		return b.hole(x.Pos, Invalid, args...)
	case *syntax.Call:
		if id, ok := x.Fun.(*syntax.Ident); ok && x.Pipe.File == "" {
			args := make([]syntax.Expr, len(x.Args))
			for i, arg := range x.Args {
				args[i] = b.expr(arg)
			}
			if b.shapeName(id.Name) {
				// An exhausted selection does not continue. (An expansion
				// replaces shape.fail with nothing, after reporting it.)
				if _, member, _ := strings.Cut(id.Name, "."); member == "exhausted" {
					return b.hole(x.Pos, Never, args...)
				}
				return b.hole(x.Pos, Invalid, args...)
			}
			if helper, _ := b.c.deriveHelperNamed(b.pkg, id.Name); helper != nil {
				fn := b.helpers[helper]
				if fn == nil {
					return b.hole(x.Pos, Invalid, args...)
				}
				// The helper's type parameters are dependent in its witness.
				x.Args, x.TypeArgs = args, nil
				b.c.deriveCalls[x] = fn
				return x
			}
			saved := b.c.pkg
			b.c.pkg = b.pkg
			fn, found := b.c.funcNamed(id.Name)
			b.c.pkg = saved
			if found && (buildIntrinsic(fn) || embedIntrinsic(fn)) {
				// Unused definitions capture no files.
				return b.hole(x.Pos, Invalid, args...)
			}
			x.Args = args
			return x
		}
	}
	b.children(reflect.ValueOf(x))
	return x
}

// absorb turns a selected copy's value into a hole over it, so copies of
// different types can share one witness branch.
func (b *deriveWitnessBuilder) absorb(block *syntax.Block) {
	if block.Tail != nil {
		block.Tail = b.hole(block.Tail.Position(), Invalid, block.Tail)
	}
}

// dependentMatch reports whether a runtime match tests a dependent value:
// a hole subject, or an arm with a dependent type pattern.
func (b *deriveWitnessBuilder) dependentMatch(x *syntax.Match, subject syntax.Expr) bool {
	if call, ok := subject.(*syntax.Call); ok {
		if fn := b.c.deriveCalls[call]; fn != nil && fn.Witness && fn.Decl.Body == nil {
			return true
		}
	}
	for _, arm := range x.Arms {
		if pattern, ok := arm.Pattern.(*syntax.TypePat); ok && pattern.Type != nil && b.c.info.assemblyTypes[pattern.Type] == Invalid {
			return true
		}
	}
	return false
}

func patternBinders(pattern syntax.Pattern, names *[]string) {
	add := func(name string) {
		if name != "" && name != "_" {
			*names = append(*names, name)
		}
	}
	switch pattern := pattern.(type) {
	case *syntax.TypePat:
		add(pattern.Name)
	case *syntax.TuplePat:
		for _, element := range pattern.Elems {
			patternBinders(element, names)
		}
	case *syntax.ListPat:
		add(pattern.Rest)
		for _, element := range pattern.Elems {
			patternBinders(element, names)
		}
	case *syntax.VariantPat:
		if len(pattern.Path) == 1 && !pattern.Context && !pattern.Braces && !pattern.Positional && len(pattern.Fields) == 0 && len(pattern.Elems) == 0 && pattern.Owner == nil && !isUpper(pattern.Path[0]) {
			add(pattern.Path[0])
		}
		for _, element := range pattern.Elems {
			patternBinders(element, names)
		}
		for _, field := range pattern.Fields {
			if field.Pattern == nil {
				add(field.Field)
			} else {
				patternBinders(field.Pattern, names)
			}
		}
	}
}

func isUpper(name string) bool { return name != "" && name[0] >= 'A' && name[0] <= 'Z' }

// repaired reports whether a diagnostic of an earlier attempt is at x.
func (b *deriveWitnessBuilder) repaired(x syntax.Expr) bool {
	switch x := x.(type) {
	case *syntax.Selector:
		return b.repairs[x.Pos] || b.repairs[x.Position()]
	case *syntax.Call:
		return b.repairs[x.Pos] || b.repairs[x.Start]
	}
	return b.repairs[x.Position()]
}

// children rewrites the expressions directly below a node.
func (b *deriveWitnessBuilder) children(v reflect.Value) {
	if v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	v = v.Elem()
	if v.Kind() != reflect.Struct {
		return
	}
	exprType := reflect.TypeOf((*syntax.Expr)(nil)).Elem()
	stmtType := reflect.TypeOf((*syntax.Stmt)(nil)).Elem()
	blockType := reflect.TypeOf((*syntax.Block)(nil))
	var visit func(reflect.Value)
	visit = func(field reflect.Value) {
		switch {
		case field.Type() == blockType:
			if !field.IsNil() {
				field.Set(reflect.ValueOf(b.block(field.Interface().(*syntax.Block))))
			}
		case field.Type() == exprType:
			if !field.IsNil() {
				field.Set(reflect.ValueOf(b.expr(field.Interface().(syntax.Expr))))
			}
		case field.Type() == stmtType:
			if !field.IsNil() {
				field.Set(reflect.ValueOf(b.stmt(field.Interface().(syntax.Stmt))))
			}
		case field.Kind() == reflect.Slice:
			for i := 0; i < field.Len(); i++ {
				visit(field.Index(i))
			}
		case field.Kind() == reflect.Pointer && !field.IsNil() && field.Elem().Kind() == reflect.Struct:
			if _, isType := field.Interface().(*syntax.TypeExpr); isType {
				return
			}
			if _, isExpr := field.Interface().(syntax.Expr); isExpr {
				if replaced := b.expr(field.Interface().(syntax.Expr)); reflect.TypeOf(replaced) == field.Type() {
					field.Set(reflect.ValueOf(replaced))
				}
				return
			}
			b.children(field)
		}
	}
	for _, index := range walkableSyntaxFields(v.Type()) {
		visit(v.Field(index))
	}
}
