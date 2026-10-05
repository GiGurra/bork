// Package diag holds source positions and compiler diagnostics.
package diag

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Pos is a position in a source file. Line and Col are 1-based.
type Pos struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"column"`
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Diagnostic is a single compiler error, reported in the Go-style
// file:line:col format so editors can click through.
type Diagnostic struct {
	Pos      Pos    `json:"-"`
	End      Pos    `json:"-"`
	Msg      string `json:"message"`
	Code     string `json:"code"`
	Severity string `json:"severity,omitempty"`
	Fixes    []Fix  `json:"fixes,omitempty"`
}

func (d Diagnostic) String() string {
	if d.Severity == "warning" {
		return fmt.Sprintf("%s: warning: %s", d.Pos, d.Msg)
	}
	return fmt.Sprintf("%s: %s", d.Pos, d.Msg)
}

// TextEdit replaces [Start, End) in a file; equal positions insert text.
type TextEdit struct {
	Start       Pos    `json:"start"`
	End         Pos    `json:"end"`
	Replacement string `json:"replacement"`
}

// Fix groups edits to apply together. RequiresInput marks illustrative type
// annotations whose placeholders must be filled in before applying the edits.
type Fix struct {
	Message       string     `json:"message"`
	RequiresInput bool       `json:"requires_input,omitempty"`
	Edits         []TextEdit `json:"edits"`
}

// MarshalJSON flattens the start position for consumers of diagnostic streams.
func (d Diagnostic) MarshalJSON() ([]byte, error) {
	end := d.End
	if end.File == "" {
		end = d.Pos
	}
	code := d.Code
	if code == "" {
		code = "compiler.error"
	}
	return json.Marshal(struct {
		SchemaVersion int    `json:"schema_version"`
		File          string `json:"file"`
		Line          int    `json:"line"`
		Column        int    `json:"column"`
		EndLine       int    `json:"end_line"`
		EndColumn     int    `json:"end_column"`
		Message       string `json:"message"`
		Code          string `json:"code"`
		Severity      string `json:"severity,omitempty"`
		Fixes         []Fix  `json:"fixes,omitempty"`
	}{1, d.Pos.File, d.Pos.Line, d.Pos.Col, end.Line, end.Col, d.Msg, code, d.Severity, d.Fixes})
}

// List collects diagnostics.
type List struct {
	items []Diagnostic
}

func (l *List) Add(pos Pos, format string, args ...any) {
	l.AddCode(pos, "compiler.error", format, args...)
}

// AddCode reports a diagnostic with a stable code independent of its message.
func (l *List) AddCode(pos Pos, code, format string, args ...any) {
	l.items = append(l.items, Diagnostic{Pos: pos, End: pos, Msg: fmt.Sprintf(format, args...), Code: code})
}

// Warn reports a nonfatal diagnostic.
func (l *List) Warn(pos Pos, code, message string) {
	l.items = append(l.items, Diagnostic{Pos: pos, End: pos, Msg: message, Code: code, Severity: "warning"})
}

// Suggest adds a range and optional fixes to a previously reported diagnostic.
func (l *List) Suggest(pos Pos, code string, end Pos, fixes ...Fix) {
	for i := len(l.items) - 1; i >= 0; i-- {
		d := &l.items[i]
		if d.Pos == pos && d.Code == code {
			d.End = end
			d.Fixes = append(d.Fixes, fixes...)
			return
		}
	}
}

// WriteJSON writes one JSON object per diagnostic in source order.
func (l *List) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	for _, d := range l.Sorted() {
		if err := enc.Encode(d); err != nil {
			return err
		}
	}
	return nil
}

func (l *List) Len() int { return len(l.items) }

// Append joins diagnostics collected by an independent compiler task.
func (l *List) Append(other *List) { l.items = append(l.items, other.items...) }

// Truncate drops the diagnostics added after the first n.
func (l *List) Truncate(n int) { l.items = l.items[:n] }

// Rewrite passes the diagnostics added after the first n to f, which may
// change them, and keeps those it reports true for.
func (l *List) Rewrite(n int, f func(*Diagnostic) bool) {
	kept := l.items[:n]
	for _, d := range l.items[n:] {
		if f(&d) {
			kept = append(kept, d)
		}
	}
	l.items = kept
}

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
