package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// CheckEffects checks every function body against the effects its
// signature declares (see "Effects in signatures" in
// docs/requirements.md): a function that declares nothing does nothing
// outside its arguments and result. main and tests may use every
// effect, unless main declares some. It reads the typed tree, so it
// runs once the program type-checks.
func CheckEffects(files []*syntax.File, info *Info, diags *diag.List) {
	// Tests may use every effect, but not call main.
	for _, fn := range info.Tests {
		if fn.Body != nil {
			checkEffects(fn, diags)
		}
	}
	for _, f := range files {
		for _, fd := range f.Funcs {
			if fn := info.FuncOf[fd]; fn != nil && fn.Body != nil {
				checkEffects(fn, diags)
			}
		}
	}
}

// effectReason says why code uses an effect: the call that does, as in
// "it calls println".
type effectReason struct {
	pos  diag.Pos
	text string
}

// reasonNames are the effects reasons are kept for: every effect, and
// EffOpen (a call of an open parameter).
var reasonNames = append(effectNames[:len(effectNames):len(effectNames)], struct {
	eff  Effects
	name string
}{EffOpen, "open"})

// effectUses collects the effects code uses, with the first reason for
// each.
type effectUses struct {
	from    *Package // the package of the code, for qualified names
	used    Effects
	reasons map[Effects]effectReason
	// mainRefs are calls of main and uses of it as a value, which are
	// not allowed: main may use every effect.
	mainRefs []diag.Pos
}

func (u *effectUses) add(effs Effects, pos diag.Pos, text string) {
	for _, n := range reasonNames {
		if effs&n.eff != 0 && u.used&n.eff == 0 {
			if u.reasons == nil {
				u.reasons = map[Effects]effectReason{}
			}
			u.reasons[n.eff] = effectReason{pos, text}
		}
	}
	u.used |= effs
}

// why lists the reasons for effs, as "it calls println and fetch".
func (u *effectUses) why(effs Effects) string {
	var texts []string
	seen := map[string]bool{}
	for _, n := range effectNames {
		if r, ok := u.reasons[n.eff]; ok && effs&n.eff != 0 && !seen[r.text] {
			seen[r.text] = true
			texts = append(texts, r.text)
		}
	}
	if len(texts) == 0 {
		return ""
	}
	return "it calls " + strings.Join(texts, " and ")
}

// first gives the position of the first reason for effs.
func (u *effectUses) first(effs Effects, fallback diag.Pos) diag.Pos {
	pos := fallback
	found := false
	for _, n := range effectNames {
		if r, ok := u.reasons[n.eff]; ok && effs&n.eff != 0 && (!found || r.pos.Line < pos.Line || r.pos.Line == pos.Line && r.pos.Col < pos.Col) {
			pos, found = r.pos, true
		}
	}
	return pos
}

func checkEffects(fn *Func, diags *diag.List) {
	u := &effectUses{from: fn.Pkg}
	u.block(fn.Body)
	for _, pos := range u.mainRefs {
		diags.AddCode(pos, "effect.main", "main cannot be called or used as a value: it may use every effect")
	}
	fd := fn.Decl
	if fn.Test != nil || fd.Name == "main" && fd.Uses == nil {
		return // may use every effect
	}
	used := u.used &^ EffOpen
	if fd.IsPred {
		if r, ok := u.reasons[EffIO]; ok && r.text == "assertSnapshot" {
			diags.AddCode(r.pos, "effect.pred", "assertSnapshot can only be used in tests (and the functions they call), not in a predicate")
		} else if used != 0 {
			diags.AddCode(u.first(used, fd.Pos), "effect.pred", "%s uses %s (%s), but predicates must be pure, or their facts could go stale", fd.Name, used, u.why(used))
		}
		if r, ok := u.reasons[EffOpen]; ok {
			diags.AddCode(r.pos, "effect.pred", "%s calls %s, which may use any effect its caller passes, but predicates must be pure; declare the parameter's type with uses nothing", fd.Name, r.text)
		}
		return
	}
	declared := fn.Effects &^ EffOpen
	// An unexported function declares exactly what it uses (calls of
	// itself, or of functions that call it, count as uses: a limit of
	// the check). Others may declare more.
	exact := !Exported(fd.Name) && fn.Of == nil && fd.Name != "main"
	target := declared | used
	if exact {
		target = used
	}
	missing := used &^ declared
	if missing != 0 {
		pos := u.first(missing, fd.Pos)
		if allowed, ok := classAllows(fn); ok && missing&^allowed != 0 {
			diags.AddCode(pos, "effect.missing", "%s uses %s (%s), but class %s allows %s", fd.Name, missing, u.why(missing), fn.Of.Class.Name, allowedText(allowed))
		} else {
			diags.AddCode(pos, "effect.missing", "%s uses %s (%s), but its signature allows %s; declare it: uses %s", fd.Name, missing, u.why(missing), allowedText(declared), target)
			diags.Suggest(pos, "effect.missing", pos, usesFix(fd, target))
		}
	}
	if unused := declared &^ used; unused != 0 && exact {
		them := "it"
		if unused&(unused-1) != 0 {
			them = "them"
		}
		diags.AddCode(fd.Uses.Pos, "effect.unused", "%s declares %s, but never uses %s", fd.Name, unused, them)
		if missing == 0 { // else the missing effect's fix covers it
			diags.Suggest(fd.Uses.Pos, "effect.unused", fd.Uses.Pos, usesFix(fd, target))
		}
	}
}

// classAllows is what the class method an instance method implements
// allows, if fn is one.
func classAllows(fn *Func) (Effects, bool) {
	if fn.Of == nil {
		return 0, false
	}
	for _, m := range fn.Of.Class.Methods {
		if m.Decl.Name == fn.Decl.Name {
			return m.Effects, true
		}
	}
	return 0, false
}

// usesFix is the edit that makes fd declare effs.
func usesFix(fd *syntax.FuncDecl, effs Effects) diag.Fix {
	text := "uses " + effs.String()
	if effs == 0 {
		text = ""
	}
	if fd.Uses == nil {
		at := fd.ParamsEnd
		at.Col++
		return diag.Fix{Message: "declare " + text, Edits: []diag.TextEdit{{Start: at, End: at, Replacement: " " + text}}}
	}
	start, end := fd.Uses.Pos, fd.Uses.End
	if effs == 0 {
		start = fd.ParamsEnd
		start.Col++ // just after the ')'
		return diag.Fix{Message: "remove the effects", Edits: []diag.TextEdit{{Start: start, End: end}}}
	}
	return diag.Fix{Message: "declare " + text, Edits: []diag.TextEdit{{Start: start, End: end, Replacement: text}}}
}

func (u *effectUses) block(b *Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *Let:
			u.expr(s.Value)
		case *ExprStmt:
			u.expr(s.X)
		case *Trust:
			u.expr(s.Call)
		}
	}
	u.expr(b.Tail)
}

// expr collects what evaluating x does. A lambda does nothing until it
// is called: its effects are in its type, and a call charges them.
func (u *effectUses) expr(x Expr) {
	switch x := x.(type) {
	case nil:
	case *Call:
		for _, a := range x.Args {
			u.expr(a)
		}
		if u.noMain(x.Func, x.Pos()) {
			return
		}
		name := x.Func.QualifiedName(u.from)
		u.add(x.Func.Effects&^EffOpen, x.Pos(), name)
		if isOpen(x.Func.Result) {
			return // the function it gives carries its arguments' effects
		}
		for i, a := range x.Args {
			if i < len(x.Func.Params) && isOpen(x.Func.Params[i]) {
				if at, ok := a.Type().(*FuncType); ok && at.Effects != 0 {
					u.add(at.Effects, a.Pos(), name+", with "+u.describeFunc(a))
				}
			}
		}
	case *CallBuiltin:
		for _, a := range x.Args {
			u.expr(a)
		}
		if x.Builtin == BuiltinPrintln || x.Builtin == BuiltinAssertSnapshot {
			u.add(EffIO, x.Pos(), x.Name)
		}
	case *CallValue:
		u.expr(x.Fun)
		for _, a := range x.Args {
			u.expr(a)
		}
		if ft, ok := x.Fun.Type().(*FuncType); ok {
			u.add(ft.Effects, x.Pos(), u.describeFunc(x.Fun))
		}
	case *FuncRef:
		_ = u.noMain(x.Inst.Func, x.Pos())
	case *Lambda:
		// Not run here, but main may not be referred to in it either.
		inner := &effectUses{from: u.from}
		inner.expr(x.Body)
		u.mainRefs = append(u.mainRefs, inner.mainRefs...)
	case *Const, *VarRef, *VariantValue:
	case *Interp:
		for _, e := range x.Exprs {
			u.expr(e)
		}
	case *Unary:
		u.expr(x.X)
	case *Binary:
		u.expr(x.X)
		u.expr(x.Y)
	case *ListLit:
		for _, e := range x.Elems {
			u.expr(e)
		}
	case *MapLit:
		for i := range x.Keys {
			u.expr(x.Keys[i])
			u.expr(x.Values[i])
		}
	case *If:
		u.expr(x.Cond)
		u.block(x.Then)
		u.expr(x.Else)
	case *Block:
		u.block(x)
	case *ScopeBlock:
		for _, p := range x.Policies {
			u.expr(p)
		}
		u.block(x.Body)
	case *Return:
		u.expr(x.Value)
	case *Select:
		u.expr(x.X)
	case *RecordLit:
		for _, f := range x.Fields {
			u.expr(f.Value)
		}
	case *Copy:
		u.expr(x.X)
		for _, f := range x.Updates {
			u.expr(f.Value)
		}
	case *Match:
		u.expr(x.X)
		for _, a := range x.Arms {
			u.expr(a.Body)
		}
	case *Try:
		u.expr(x.X)
	default:
		panic(fmt.Sprintf("effects: unexpected %T", x))
	}
}

// noMain records a reference to main (reported by checkEffects), and
// reports whether fn is main.
func (u *effectUses) noMain(fn *Func, pos diag.Pos) bool {
	if fn.Decl.Name != "main" || fn.Decl.IsMethod || fn.Of != nil || fn.Class != nil {
		return false
	}
	u.mainRefs = append(u.mainRefs, pos)
	return true
}

// describeFunc names a function value in a reason: "f", "run", "a
// lambda that calls println".
func (u *effectUses) describeFunc(x Expr) string {
	switch x := x.(type) {
	case *VarRef:
		return x.Var.Name
	case *FuncRef:
		return x.Name
	case *Select:
		return x.Name
	case *Lambda:
		inner := &effectUses{from: u.from}
		inner.expr(x.Body)
		if why := inner.why(inner.used); why != "" {
			return "a lambda that calls " + strings.TrimPrefix(why, "it calls ")
		}
		return "a lambda"
	}
	return "a function value"
}
