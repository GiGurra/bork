package check

import (
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func isDuration(t Type) bool {
	rec, ok := t.(*Record)
	return ok && rec.Prelude && rec.Name == "Duration"
}

func durationReplacement(text, unit string) string {
	if n, err := strconv.ParseInt(strings.ReplaceAll(text, "_", ""), 10, 64); err == nil && n >= 0 {
		if unit == "millis" && n != 0 && n%1000 == 0 {
			return strconv.FormatInt(n/1000, 10) + ".seconds()"
		}
		return text + "." + unit + "()"
	}
	return "(" + text + ")." + unit + "()"
}

func (c *checker) durationText(arg syntax.Argument, unit string) string {
	for _, file := range c.files {
		if file.Path == arg.ValueStart.File {
			return durationReplacement(sourceText(file, arg.ValueStart, arg.End), unit)
		}
	}
	return ""
}

// Only standard timing APIs have a known historical millisecond unit.
func standardDurationFunction(fn *Func) bool {
	if fn.Decl == nil {
		return false
	}
	if fn.Prelude {
		switch fn.Decl.Name {
		case "delay", "cancelAfter", "withTimeout", "withTimeoutDo", "taskTimeout", "cleanupTimeout", "waitFor", "waitForWhere":
			return true
		}
		return false
	}
	return fn.Pkg != nil && (fn.Pkg.Path == "bork/http" || fn.Pkg.Path == "bork/net") && Exported(fn.Decl.Name)
}

func (c *checker) durationArgumentFix(call *syntax.Call, fn *Func, arg syntax.Expr, want, actual Type) {
	if !standardDurationFunction(fn) || !isDuration(want) || actual != Int {
		return
	}
	for i, value := range call.Args {
		if value != arg || i >= len(call.Arguments) {
			continue
		}
		a := call.Arguments[i]
		if text := c.durationText(a, "millis"); text != "" {
			c.diags.Suggest(arg.Position(), "type.error", a.End, diag.Fix{Message: "convert milliseconds to Duration", Edits: []diag.TextEdit{{Start: a.ValueStart, End: a.End, Replacement: text}}})
		}
	}
}

func (c *checker) durationNamedFix(arg syntax.Argument, value syntax.Expr, fn *Func) bool {
	if !standardDurationFunction(fn) {
		return false
	}
	name := strings.TrimSuffix(arg.Name, "Ms")
	if arg.Name == "ms" {
		name = "duration"
		if fn.Decl.Name == "waitFor" || fn.Decl.Name == "waitForWhere" {
			name = "timeout"
		}
	}
	if name == arg.Name {
		return false
	}
	for i, p := range fn.Decl.Params {
		if p.Name != name || i >= len(fn.Params) || !isDuration(fn.Params[i]) {
			continue
		}
		edits := []diag.TextEdit{{Start: arg.Pos, End: arg.NameEnd, Replacement: name}}
		if text := c.durationText(arg, "millis"); c.exprWant(value, nil) == Int && text != "" {
			edits = append(edits, diag.TextEdit{Start: arg.ValueStart, End: arg.End, Replacement: text})
		}
		c.diags.Suggest(arg.Pos, "call.unknown_argument", arg.End, diag.Fix{Message: "use " + name + " with a Duration", Edits: edits})
		return true
	}
	return false
}

func (c *checker) removedDurationCall(call *syntax.Call, id *syntax.Ident) bool {
	alias, name, ok := strings.Cut(id.Name, ".")
	pkg := c.pkg.imports[alias]
	if !ok || pkg == nil || pkg.Path != "bork/time" || name != "Nanoseconds" && name != "Milliseconds" {
		return false
	}
	c.pkg.used[alias] = true
	const code = "migration.duration_constructor"
	c.diags.AddCode(id.Pos, code, "time.%s was removed; use an Int duration method", name)
	if len(call.Args) != 1 || len(call.TypeArgs) != 0 || call.Pipe.File != "" {
		return true
	}
	unit := "nanos"
	if name == "Milliseconds" {
		unit = "millis"
	}
	if text := c.durationText(call.Arguments[0], unit); text != "" {
		end := call.End
		if name == "Milliseconds" {
			next := end
			next.Col++
			for _, file := range c.files {
				if file.Path == end.File && sourceText(file, end, next) == "?" {
					end = next
					break
				}
			}
		}
		c.diags.Suggest(id.Pos, code, end, diag.Fix{Message: "use ." + unit + "()", Edits: []diag.TextEdit{{Start: call.Start, End: end, Replacement: text}}})
	}
	return true
}
