package check

import (
	"fmt"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func hasNamedArgs(e *syntax.Call) bool {
	for _, a := range e.Arguments {
		if a.Name != "" {
			return true
		}
	}
	return false
}

// namedArgs maps supplied values to parameters before defaults and inference.
// The syntax stays in source order; the typed call retains its permutation.
func (c *checker) namedArgs(e *syntax.Call, name string, fn *Func, args []syntax.Expr) ([]syntax.Expr, bool) {
	if !hasNamedArgs(e) {
		return c.withDefaults(args, fn), true
	}
	if fn.Decl == nil {
		c.rejectNamedArgs(e, "this callable has no declared parameter names")
		return args, false
	}
	c.ensureDefaults(fn)
	params := fn.Decl.Params
	var names []string
	for _, p := range params {
		names = append(names, p.Name)
	}
	out := make([]syntax.Expr, len(params))
	order := make([]int, 0, len(params))
	offset := len(args) - len(e.Args) // receiver inserted by methodCallOf
	if offset > 0 {
		out[0] = args[0]
		order = append(order, 0)
	}
	named, valid := false, true
	next := offset
	for i, value := range e.Args {
		arg := e.Arguments[i]
		index := next
		if arg.Name == "" {
			if named {
				c.diags.AddCode(arg.Pos, "call.positional_after_named", "move positional arguments before named arguments in %s", name)
				valid = false
				continue
			}
			next++
		} else {
			named = true
			index = argumentIndex(names, arg.Name, next)
			if index < 0 {
				c.diags.AddCode(arg.Pos, "call.unknown_argument", "%s has no parameter named %s", name, arg.Name)
				candidates := params
				if fn.Decl.IsMethod {
					candidates = candidates[1:]
				}
				if replacement := closestParam(arg.Name, candidates); !c.durationNamedFix(arg, value, fn) && replacement != "" {
					c.diags.Suggest(arg.Pos, "call.unknown_argument", arg.NameEnd, diag.Fix{
						Message: "use parameter " + replacement,
						Edits:   []diag.TextEdit{{Start: arg.Pos, End: arg.NameEnd, Replacement: replacement}},
					})
				}
				valid = false
				continue
			}
			if fn.Decl.IsMethod && index == 0 {
				c.diags.AddCode(arg.Pos, "call.named_receiver", "the receiver of %s cannot be named; supply it positionally", name)
				valid = false
				continue
			}
		}
		if index >= len(out) {
			c.diags.AddCode(arg.Pos, "call.too_many_arguments", "%s takes at most %d argument(s)", name, len(params)-offset)
			valid = false
			continue
		}
		if out[index] != nil {
			c.diags.AddCode(arg.Pos, "call.duplicate_argument", "parameter %s of %s is already supplied at %s", params[index].Name, name, out[index].Position())
			if arg.RemovalStart.File != "" {
				c.diags.Suggest(arg.Pos, "call.duplicate_argument", arg.End, diag.Fix{
					Message: "remove duplicate argument " + params[index].Name,
					Edits:   []diag.TextEdit{{Start: arg.RemovalStart, End: arg.End}},
				})
			}
			valid = false
			continue
		}
		out[index] = value
		order = append(order, index)
	}
	var missing []string
	for i, p := range params {
		if out[i] != nil {
			continue
		}
		if p.Default == nil || !isLiteral(p.Default) && !c.sharedDefaults[p.Default] {
			missing = append(missing, fmt.Sprintf("%s: %s", p.Name, fn.Params[i]))
			continue
		}
		out[i] = c.copyDefault(p.Default)
		order = append(order, i)
	}
	if len(missing) > 0 {
		c.diags.AddCode(e.Pos, "call.missing_argument", "%s is missing required argument(s): %s", name, strings.Join(missing, ", "))
		valid = false
	}
	if valid {
		c.info.callOrder[e] = order
	}
	return out, valid
}

func (c *checker) rejectNamedArgs(e *syntax.Call, reason string) {
	for _, arg := range e.Arguments {
		if arg.Name != "" {
			c.diags.AddCode(arg.Pos, "call.named_argument_unavailable", "named arguments require a direct call to a declared function or method: %s", reason)
		}
	}
}

// Offer only a unique nearby name; ties leave the diagnostic without an edit.
func closestParam(name string, params []*syntax.Param) string {
	best, distance := "", 3
	for _, p := range params {
		d := nameDistance(name, p.Name)
		if d < distance {
			best, distance = p.Name, d
		} else if d == distance {
			best = ""
		}
	}
	return best
}

func nameDistance(a, b string) int {
	left, right := []rune(a), []rune(b)
	row := make([]int, len(right)+1)
	for i := range row {
		row[i] = i
	}
	for i, x := range left {
		prev := row[0]
		row[0] = i + 1
		for j, y := range right {
			old, cost := row[j+1], 0
			if x != y {
				cost = 1
			}
			row[j+1] = min(row[j+1]+1, row[j]+1, prev+cost)
			prev = old
		}
	}
	return row[len(right)]
}

func argumentIndex(names []string, name string, next int) int {
	if name == "" {
		return next
	}
	for i, candidate := range names {
		if candidate == name {
			return i
		}
	}
	return -1
}
