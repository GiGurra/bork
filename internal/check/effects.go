package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Effects is a set of effects: what a function may do outside its
// arguments and result (see "Effects in signatures" in
// docs/requirements.md). The empty set is pure.
type Effects uint8

const (
	EffIO     Effects = 1 << iota // standard streams, files, the process
	EffNet                        // the network
	EffClock                      // time and waiting
	EffRandom                     // random numbers
	EffState                      // state shared between tasks

	// EffOpen stands for the effects of an open function parameter:
	// one written without `uses`, which takes a function with any
	// effects, chosen by each call.
	EffOpen
)

// effectNames lists the effects in the order they are written.
var effectNames = []struct {
	eff  Effects
	name string
}{
	{EffIO, "io"},
	{EffNet, "net"},
	{EffClock, "clock"},
	{EffRandom, "random"},
	{EffState, "state"},
}

// String writes the effects as a uses declaration lists them:
// "io + net", or "nothing". It leaves out EffOpen.
func (e Effects) String() string {
	var names []string
	for _, n := range effectNames {
		if e&n.eff != 0 {
			names = append(names, n.name)
		}
	}
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, " + ")
}

// allowedText says what a declaration allows: "no effects", or
// "only io + net".
func allowedText(e Effects) string {
	if e&^EffOpen == 0 {
		return "no effects"
	}
	return "only " + e.String()
}

// effectsOf gives the effects of a `uses` declaration (none for nil),
// reporting names that are not effects, and effects named twice.
func (c *checker) effectsOf(u *syntax.Uses) Effects {
	if u == nil {
		return 0
	}
	var effs Effects
	for _, e := range u.Effects {
		eff := effectNamed(e.Name)
		switch {
		case eff == 0:
			c.diags.AddCode(e.Pos, "effect.unknown", "unknown effect %s; the effects are io, net, clock, random, and state", e.Name)
		case effs&eff != 0:
			c.diags.AddCode(e.Pos, "effect.duplicate", "effect %s is listed twice", e.Name)
		}
		effs |= eff
	}
	return effs
}

// openSignature marks fn's open positions: parameters and the result
// whose type is a function type written without `uses` (directly, or
// through an alias). Their effects are EffOpen.
func (c *checker) openSignature(fn *Func) {
	for i, p := range fn.Decl.Params {
		if i < len(fn.Params) && p.Type != nil {
			fn.Params[i] = c.openAt(fn.Params[i], p.Type)
		}
	}
	if fn.Decl.Result != nil && !fn.Decl.IsPred {
		fn.Result = c.openAt(fn.Result, fn.Decl.Result)
	}
}

// openAt gives t, written as te, with EffOpen as its effects if it is a
// function type written without `uses`.
func (c *checker) openAt(t Type, te *syntax.TypeExpr) Type {
	ft, ok := t.(*FuncType)
	if !ok || ft.Effects != 0 || !c.unannotatedFunc(te, 0) {
		return t
	}
	return &FuncType{Params: ft.Params, Result: ft.Result, Effects: EffOpen}
}

// unannotatedFunc reports whether te is a function type written
// without `uses`, or an alias of one.
func (c *checker) unannotatedFunc(te *syntax.TypeExpr, depth int) bool {
	switch {
	case te.Func != nil:
		return te.Func.Uses == nil
	case len(te.Union) > 0 || len(te.Args) > 0 || len(te.Where) > 0 || depth > 20:
		return false
	}
	e := c.lookupType(te.Name)
	return e != nil && e.decl.Kind == syntax.AliasType && c.unannotatedFunc(e.decl.Alias, depth+1)
}

func effectNamed(name string) Effects {
	for _, n := range effectNames {
		if n.name == name {
			return n.eff
		}
	}
	return 0
}
