package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// DebugWarnings reports development markers without making checking fail.
func DebugWarnings(info *Info) *diag.List {
	warnings := &diag.List{}
	for call, builtin := range info.callBuiltins {
		if builtin != BuiltinDbg && builtin != BuiltinTodo {
			continue
		}
		start := call.Start
		if start.File == "" {
			start = call.Fun.Position()
		}
		code, message := "debug.dbg", "leftover dbg probe"
		if builtin == BuiltinTodo {
			code, message = "debug.todo", "unfinished todo marker"
		}
		warnings.Warn(start, code, message)
		end := call.End
		if call.Pipe.File != "" {
			end = call.PipeTargetEnd
		}
		warnings.Suggest(start, code, end)
		if builtin == BuiltinDbg && len(call.Args) == 1 && call.Pipe.File != "" {
			warnings.Suggest(start, code, end, diag.Fix{
				Message: "remove the dbg wrapper",
				Edits: []diag.TextEdit{
					{Start: call.PipeStart, End: call.PipeStart, Replacement: "("},
					{Start: call.PipeEnd, End: end, Replacement: ")"},
				},
			})
		} else if builtin == BuiltinDbg && len(call.Arguments) == 1 {
			arg := call.Arguments[0]
			// Two edits preserve the original expression, including comments
			// inside it, and keep precedence and nested probe fixes intact.
			warnings.Suggest(start, code, call.End, diag.Fix{
				Message: "remove the dbg wrapper",
				Edits: []diag.TextEdit{
					{Start: start, End: arg.Pos, Replacement: "("},
					{Start: arg.End, End: call.End, Replacement: ")"},
				},
			})
		}
	}
	return warnings
}

func sourceText(file *syntax.File, start, end diag.Pos) string {
	if file == nil {
		return ""
	}
	offset := func(pos diag.Pos) int {
		off := 0
		for line := 1; line < pos.Line; line++ {
			n := strings.IndexByte(file.Source[off:], '\n')
			if n < 0 {
				return len(file.Source)
			}
			off += n + 1
		}
		return min(off+pos.Col-1, len(file.Source))
	}
	return file.Source[offset(start):offset(end)]
}

// debugValue identifies the value a probe returns, for proofs about that value.
func debugValue(x Expr) Expr {
	for {
		call, ok := x.(*CallBuiltin)
		if !ok || call.Builtin != BuiltinDbg {
			return x
		}
		x = call.Args[0]
	}
}
