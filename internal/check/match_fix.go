package check

import (
	"strconv"
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
		arms = append(arms, pattern+" => todo()")
		if pattern == "_" {
			break
		}
	}
	prefix := " "
	if len(m.Arms) > 0 && !m.TrailingSeparator {
		prefix = ", "
	}
	return diag.Fix{Message: "Add missing match arms", Edits: []diag.TextEdit{{
		Start: m.Close, End: m.Close, Replacement: prefix + strings.Join(arms, ", ") + ", ",
	}}}
}

func (c *checker) missingMatchPattern(w *witness) string {
	h := w.ctor
	if h == nil {
		return "_"
	}
	if h.member != nil {
		if !c.matchTypeVisible(h.member) {
			return "_"
		}
		name := "missingValue"
		probe := *c
		probe.diags = &diag.List{}
		for i := 2; probe.nameTaken(name, diag.Pos{}); i++ {
			name = "missingValue" + strconv.Itoa(i)
		}
		return name + ": " + TypeText(h.member, c.pkg)
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
		if !c.matchTypeVisible(v.Parent) {
			return "_"
		}
		owner = v.Parent.Pkg
		if owner != nil && owner != c.pkg && !Exported(v.Name) {
			return "_"
		}
		name = qualify(v.Parent.Name, owner, c.pkg) + "." + v.Name
		fields = v.Fields
	} else {
		if !c.matchTypeVisible(h.record) {
			return "_"
		}
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

// Re-exported aliases may expose values whose nominal owner is not imported.
// Such names cannot be written by this package; a wildcard still covers the
// missing values without adding imports or guessing how an alias is spelled.
func (c *checker) matchTypeVisible(t Type) bool {
	var owner *Package
	var name string
	var args []Type
	switch t := t.(type) {
	case *Record:
		owner, name, args = t.Pkg, t.Name, t.Args
	case *Sealed:
		owner, name, args = t.Pkg, t.Name, t.Args
	case *Resource:
		owner, name = t.Pkg, t.Name
	case *Opaque:
		owner, name = t.Pkg, t.Name
	case *List:
		args = []Type{t.Elem}
	case *Seq:
		args = []Type{t.Elem}
	case *Map:
		args = []Type{t.Key, t.Value}
	case *Union:
		args = t.Members
	case *FuncType:
		args = append(append([]Type{}, t.Params...), t.Result)
	}
	for _, arg := range args {
		if !c.matchTypeVisible(arg) {
			return false
		}
	}
	if owner == nil || owner == c.pkg || owner.Path == "" {
		return true
	}
	if !Exported(name) {
		return false
	}
	for _, imported := range c.pkg.imports {
		if imported == owner {
			return true
		}
	}
	return false
}
