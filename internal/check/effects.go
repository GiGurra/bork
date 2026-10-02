package check

import (
	"fmt"
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
// "io + net", or "nothing". EffOpen is written "open".
func (e Effects) String() string {
	var names []string
	for _, n := range effectNames {
		if e&n.eff != 0 {
			names = append(names, n.name)
		}
	}
	if e&EffOpen != 0 {
		names = append(names, "open")
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

// chargeCall adds what a call of fn does to the effects of the code
// being checked, and gives the call's result. The call does what fn
// declares, and what the arguments to its open parameters do, unless
// its result is open: then those are what the function it gives does.
func (c *checker) chargeCall(fn *Func, result Type, args []Type) Type {
	c.used |= fn.Effects
	var open Effects
	for i, t := range args {
		if i < len(fn.Params) && isOpen(fn.Params[i]) {
			if at, ok := t.(*FuncType); ok {
				open |= at.Effects
			}
		}
	}
	if rf, ok := result.(*FuncType); ok && isOpen(rf) {
		return &FuncType{Params: rf.Params, Result: rf.Result, Effects: rf.Effects&^EffOpen | open}
	}
	c.used |= open
	return result
}

// isOpen reports whether t is the type of an open parameter or result.
func isOpen(t Type) bool {
	ft, ok := t.(*FuncType)
	return ok && ft.Effects&EffOpen != 0
}

// fitsParam reports whether an argument of type t can be passed for a
// parameter of type p. An open parameter takes a function with any
// effects.
func fitsParam(t, p Type) bool {
	if isOpen(p) {
		if tf, ok := t.(*FuncType); ok {
			return sameSignature(tf, p.(*FuncType))
		}
	}
	return assignable(t, p)
}

// closeOpen gives the type of a function with open positions used as a
// value: they become pure.
func closeOpen(ft *FuncType) *FuncType {
	out := &FuncType{Params: make([]Type, len(ft.Params)), Result: ft.Result, Effects: ft.Effects}
	for i, p := range ft.Params {
		out.Params[i] = closeOne(p)
	}
	out.Result = closeOne(ft.Result)
	return out
}

func closeOne(t Type) Type {
	if !isOpen(t) {
		return t
	}
	ft := t.(*FuncType)
	return &FuncType{Params: ft.Params, Result: ft.Result, Effects: ft.Effects &^ EffOpen}
}

// effectsNote explains a message about an expected and a found
// function type (the first two function types among args, in that
// order) when they differ only in their effects, which the types alone
// do not show well.
func effectsNote(args []any) string {
	var fts []*FuncType
	for _, a := range args {
		if ft, ok := a.(*FuncType); ok {
			fts = append(fts, ft)
		}
	}
	if len(fts) < 2 || !sameSignature(fts[0], fts[1]) {
		return ""
	}
	want, found := fts[0].Effects, fts[1].Effects
	switch extra := found &^ want; {
	case extra&EffOpen != 0:
		return " (it uses what an open parameter uses, which its caller chooses: it can only be passed to an open parameter, or returned as an open result)"
	case extra != 0 && want&EffOpen != 0:
		return fmt.Sprintf(" (it uses %s, but an open result can only use what the open parameters use; to allow more, write the parameters' and the result's effects)", extra)
	case extra != 0 && want == 0:
		return fmt.Sprintf(" (it uses %s, where a function that uses nothing is expected)", extra)
	case extra != 0:
		return fmt.Sprintf(" (it uses %s, which is not allowed there)", extra)
	}
	return ""
}

func effectNamed(name string) Effects {
	for _, n := range effectNames {
		if n.name == name {
			return n.eff
		}
	}
	return 0
}
