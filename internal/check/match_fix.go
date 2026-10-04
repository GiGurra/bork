package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// A fix covers each missing top-level constructor completely. A witness can
// describe just one uncovered payload; copying that payload would leave other
// values uncovered, so generated field patterns deliberately match any value.
func (c *checker) missingMatchFix(m *syntax.Match, pats []*Pat, t Type) diag.Fix {
	var arms []string
	for _, w := range missingWitnesses(pats, t) {
		pattern := c.missingMatchPattern(w)
		arms = append(arms, pattern+" => todo(),")
		if pattern == "_" {
			break
		}
	}
	return diag.Fix{Message: "Add missing match arms", Edits: []diag.TextEdit{{
		Start: m.Close, End: m.Close, Replacement: "\n" + strings.Join(arms, "\n") + "\n",
	}}}
}

func (c *checker) missingMatchPattern(w *witness) string {
	h := w.ctor
	if h == nil {
		return "_"
	}
	if h.member != nil {
		return "value: " + TypeText(h.member, c.pkg)
	}
	if h.lit != nil {
		return h.label
	}
	if h.list != nil {
		if len(h.args) == 0 {
			return "[]"
		}
		return "[_, ...]"
	}
	var name string
	var fields []*Field
	var owner *Package
	if h.variant != nil {
		v := h.variant
		owner = v.Parent.Pkg
		if owner != nil && owner != c.pkg && !Exported(v.Name) {
			return "_"
		}
		name = qualify(v.Parent.Name, owner, c.pkg) + "." + v.Name
		fields = v.Fields
	} else {
		owner = h.record.Pkg
		name = qualify(h.record.Name, owner, c.pkg)
		fields = h.record.Fields
	}
	var labels []string
	for _, f := range fields {
		if owner == nil || owner == c.pkg || Exported(f.Name) {
			labels = append(labels, f.Name+": _")
		}
	}
	if len(labels) > 0 {
		name += " { " + strings.Join(labels, ", ") + " }"
	}
	return name
}
