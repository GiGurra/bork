package check

import (
	"reflect"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Signature parsing owns diagnostics for unknown and repeated effects. This
// reads only known declared effects without repeating those errors at calls.
func deriveWrittenEffects(written *syntax.Uses) Effects {
	var effects Effects
	if written != nil {
		for _, effect := range written.Effects {
			effects |= effectNamed(effect.Name)
		}
	}
	return effects
}

// Only declared signature positions are open; a local function annotation
// without uses remains pure. This mirrors openAt/openParamAt for symbolic heads.
func (c *checker) deriveOpenSignatureTerm(term *deriveTypeTerm, written *syntax.TypeExpr, parameter bool) *deriveTypeTerm {
	if term == nil || written == nil {
		return term
	}
	copy := *term
	if parameter && term.head == "List" && written.Name == "List" && len(written.Args) == 1 && len(term.args) == 1 {
		copy.args = []*deriveTypeTerm{c.deriveOpenSignatureTerm(term.args[0], written.Args[0], false)}
	} else if term.head == "function" && c.unannotatedFunc(written, 0) {
		copy.effects |= EffOpen
		if copy.native != nil {
			copy.native = c.openAt(copy.native, written)
		}
	}
	return &copy
}

func deriveOpenTerm(term *deriveTypeTerm) bool {
	return term != nil && term.head == "function" && term.effects&EffOpen != 0
}

// deriveEffects mirrors CheckEffects for the independent parts of a derive
// definition. Each charge is one that every expansion also makes: a declared
// effect of a known callee, a callback passed to a known open parameter, or a
// call of a function value whose effects are known. Unresolved callees and
// dependent method calls charge nothing; their expansion checks them.
type deriveEffects struct {
	c    *checker
	uses *effectUses
	// lambdas holds each checked lambda's inferred body effects, which its
	// function value carries; creating it charges nothing.
	lambdas map[*syntax.Lambda]*effectUses
	// values holds the known effects of locals bound to function values
	// without an annotation, as an ordinary local's inferred type does.
	values map[*local]Effects
	// results holds the effects of calls whose declared result is open:
	// the returned function carries its open arguments' effects.
	results map[*syntax.Call]Effects
	// open marks values passed directly to open parameters, which accept
	// any effects; the caller is charged for them instead.
	open map[syntax.Expr]bool
	// quiet reports nothing, for metadata values, which expansion evaluates.
	quiet bool
}

func (c *checker) newDeriveEffects() *deriveEffects {
	return &deriveEffects{c: c, uses: &effectUses{from: c.pkg}, lambdas: map[*syntax.Lambda]*effectUses{}, values: map[*local]Effects{}, results: map[*syntax.Call]Effects{}, open: map[syntax.Expr]bool{}}
}

// nested collects the effects of a body that does not run where it is
// written: a lambda, or a native comptime block with its own allowance.
func (e *deriveEffects) nested(body func()) *effectUses {
	outer := e.uses
	e.uses = &effectUses{from: outer.from}
	body()
	inner := e.uses
	e.uses = outer
	return inner
}

// concrete charges what the ordinary checker charged an expression island.
func (e *deriveEffects) concrete(expr syntax.Expr, used Effects) {
	if used != 0 {
		e.uses.add(used, expr.Position(), deriveCalleeName(expr))
	}
}

// markOpen records lambdas in a call's known open parameter positions,
// including the elements of a list passed to a list of open callbacks.
func (e *deriveEffects) markOpen(call *syntax.Call, params []*deriveTypeTerm, names []string) {
	for i, argument := range call.Args {
		parameter := deriveTermArgument(call, i, params, names)
		if deriveOpenTerm(parameter) {
			e.open[argument] = true
		} else if parameter != nil && parameter.head == "List" && len(parameter.args) == 1 && deriveOpenTerm(parameter.args[0]) {
			if list, ok := argument.(*syntax.ListLit); ok {
				for _, element := range list.Elems {
					e.open[element] = true
				}
			}
		}
	}
}

// call charges a non-concrete call after its arguments were checked, as
// chargeCall and effectUses.expr do for an ordinary call.
func (e *deriveEffects) call(call *syntax.Call, signature *deriveCallSignature, params []*deriveTypeTerm, result *deriveTypeTerm, names []string, symbolic *deriveSymbolicTypes) {
	id, named := call.Fun.(*syntax.Ident)
	if !named || call.Pipe.File != "" {
		return
	}
	if local := e.c.lookup(id.Name); local != nil {
		// A call of a function value charges that value's effects.
		e.uses.add(e.value(id, symbolic), call.Pos, id.Name)
		return
	}
	if signature == nil {
		if kind := builtins[id.Name]; kind == BuiltinPrintln || kind == BuiltinAssertSnapshot {
			e.uses.add(EffIO, call.Pos, id.Name)
		}
		return
	}
	e.uses.add(signature.effects&^EffOpen, call.Pos, id.Name)
	var open Effects
	for i, argument := range call.Args {
		parameter := deriveTermArgument(call, i, params, names)
		var effects Effects
		if deriveOpenTerm(parameter) {
			effects = e.value(argument, symbolic)
		} else if parameter != nil && parameter.head == "List" && len(parameter.args) == 1 && deriveOpenTerm(parameter.args[0]) {
			if list, ok := argument.(*syntax.ListLit); ok {
				for _, element := range list.Elems {
					effects |= e.value(element, symbolic)
				}
			}
		}
		if effects != 0 && !deriveOpenTerm(result) {
			e.uses.add(effects, argument.Position(), id.Name+", with "+e.describe(argument))
		}
		open |= effects
	}
	if deriveOpenTerm(result) {
		e.results[call] = open
	}
}

// value gives the known effects a function value carries when called. An
// unknown value charges nothing; its expansion checks the call.
func (e *deriveEffects) value(expr syntax.Expr, symbolic *deriveSymbolicTypes) Effects {
	switch expr := expr.(type) {
	case *syntax.Lambda:
		if inner := e.lambdas[expr]; inner != nil {
			return inner.used
		}
	case *syntax.Call:
		return e.results[expr]
	case *syntax.Ident:
		local := e.c.lookup(expr.Name)
		if local == nil {
			if fn, found := e.c.funcNamed(expr.Name); found {
				return fn.Effects &^ EffOpen
			}
			if helper, _ := e.c.deriveHelperNamed(e.c.pkg, expr.Name); helper != nil {
				return deriveWrittenEffects(helper.Uses)
			}
			return 0
		}
		if effects := e.values[local]; effects != 0 {
			return effects
		}
		if term := symbolic.expr(expr); term != nil && term.head == "function" {
			return term.effects
		}
		if function, ok := local.typ.(*FuncType); ok {
			return function.Effects
		}
	}
	return 0
}

func (e *deriveEffects) describe(expr syntax.Expr) string {
	switch expr := expr.(type) {
	case *syntax.ListLit:
		return "a list of callbacks"
	case *syntax.Ident:
		return expr.Name
	case *syntax.Lambda:
		if inner := e.lambdas[expr]; inner != nil {
			if why := inner.why(inner.used); why != "" {
				return "a lambda that calls " + strings.TrimPrefix(why, "it calls ")
			}
		}
		return "a lambda"
	}
	return "a function value"
}

// lambda checks a lambda's body effects against its known function context.
func (e *deriveEffects) lambda(node *syntax.Lambda, inner *effectUses, allowed Effects, known bool) {
	e.lambdas[node] = inner
	if known {
		e.fits(node, inner.used, allowed, func(missing Effects) (diag.Pos, string) {
			return inner.first(missing, node.Pos), "this lambda uses " + missing.String() + " (" + inner.why(missing) + ")"
		})
	}
}

// value checks a function value with known effects against its context.
// Values with unknown effects remain checks of the expansion.
func (e *deriveEffects) valueFits(expr syntax.Expr, want Type, symbolicWant *deriveTypeTerm, symbolic *deriveSymbolicTypes) {
	var allowed Effects
	if context, ok := want.(*FuncType); ok {
		allowed = context.Effects
	} else if symbolicWant != nil && symbolicWant.head == "function" {
		allowed = symbolicWant.effects
	} else {
		return
	}
	e.fits(expr, e.value(expr, symbolic), allowed, func(missing Effects) (diag.Pos, string) {
		return expr.Position(), e.describe(expr) + " uses " + missing.String()
	})
}

// fits mirrors the effect part of assigning a function value: a value passed
// to an open parameter may use anything, since its caller is charged. An open
// result context allows only what open parameters use, and a closed context
// allows what it declares.
func (e *deriveEffects) fits(expr syntax.Expr, used, allowed Effects, describe func(Effects) (diag.Pos, string)) {
	if e.quiet || e.open[expr] {
		return
	}
	if missing := used &^ (allowed | EffOpen); missing != 0 {
		pos, what := describe(missing)
		e.c.diags.AddCode(pos, "effect.missing", "%s, but the function type expected here allows %s", what, allowedText(allowed))
	} else if used&EffOpen != 0 && allowed&EffOpen == 0 {
		e.c.diags.AddCode(expr.Position(), "effect.missing", "this function value uses what an open parameter uses, which its caller chooses: it can only be passed to an open parameter, or returned as an open result")
	}
}

// comptime checks a native comptime block's own build-only allowance.
func (e *deriveEffects) comptime(node *syntax.Comptime, inner *effectUses) {
	if used := inner.used &^ (EffBuild | EffOpen); used != 0 && !e.quiet {
		e.c.diags.AddCode(inner.first(used, node.Pos), "comptime.effects", "comptime requires pure code, found uses %s (%s)", used, inner.why(used))
	}
}

// finish checks the definition's body against its declared effects, and the
// open-result rule, as checkEffects and checkFunc do for a declared function.
func (e *deriveEffects) finish(method *syntax.FuncDecl, declared Effects, openResult bool) {
	if openResult && e.uses.used&EffOpen != 0 {
		e.c.diags.AddCode(method.Pos, "effect.open-result", "%s returns an open function, so it cannot call its open parameters itself (its callers are not charged for them); give them effects, or only return them", method.Name)
	}
	missing := e.uses.used &^ (declared | EffOpen)
	if missing == 0 {
		return
	}
	pos := e.uses.first(missing, method.Pos)
	if class := e.c.deriveTemplateClass(method); class != nil {
		if m := class.Method(method.Name); m != nil && missing&^m.Effects != 0 {
			e.c.diags.AddCode(pos, "effect.missing", "%s uses %s (%s), but class %s allows %s", method.Name, missing, e.uses.why(missing), class.Name, allowedText(m.Effects))
			return
		}
	}
	target := declared | missing
	e.c.diags.AddCode(pos, "effect.missing", "%s uses %s (%s), but its signature allows %s; declare it: uses %s", method.Name, missing, e.uses.why(missing), allowedText(declared), target)
	e.c.diags.Suggest(pos, "effect.missing", pos, usesFix(method, target))
}

func (c *checker) deriveTemplateClass(method *syntax.FuncDecl) *Class {
	if method.Instance == nil || !method.Instance.Derivation {
		return nil
	}
	return c.lookupClass(method.Instance.Class)
}

// deriveCalleeName names the first direct call in an expression island, for
// the reason an effect is used.
func deriveCalleeName(expr syntax.Expr) string {
	name := ""
	var find func(reflect.Value)
	find = func(v reflect.Value) {
		if name != "" || !v.IsValid() {
			return
		}
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if v.IsNil() {
				return
			}
			if call, ok := v.Interface().(*syntax.Call); ok {
				if id, named := call.Fun.(*syntax.Ident); named {
					name = id.Name
					return
				}
			}
			find(v.Elem())
		case reflect.Struct:
			for _, index := range walkableSyntaxFields(v.Type()) {
				find(v.Field(index))
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				find(v.Index(i))
			}
		}
	}
	find(reflect.ValueOf(expr))
	if name == "" {
		return "an expression"
	}
	return name
}
