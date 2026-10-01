// Package diag holds source positions and compiler diagnostics.
package diag

import (
	"fmt"
	"sort"
	"strings"
)

// Pos is a position in a source file. Line and Col are 1-based.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Diagnostic is a single compiler error, reported in the Go-style
// file:line:col format so editors can click through.
type Diagnostic struct {
	Pos Pos
	Msg string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s", d.Pos, d.Msg)
}

// List collects diagnostics.
type List struct {
	items []Diagnostic
}

func (l *List) Add(pos Pos, format string, args ...any) {
	l.items = append(l.items, Diagnostic{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

func (l *List) Len() int { return len(l.items) }

// Sorted returns the diagnostics ordered by file, line, and column.
func (l *List) Sorted() []Diagnostic {
	out := append([]Diagnostic(nil), l.items...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Pos, out[j].Pos
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
	return out
}

// Error renders all diagnostics, one per line.
func (l *List) Error() string {
	var sb strings.Builder
	for i, d := range l.Sorted() {
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(d.String())
	}
	return sb.String()
}
