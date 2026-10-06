package check

import (
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/syntax"
)

func TestDerivePackageTagValueChargesCheckedSyntax(t *testing.T) {
	plan := factTestPlan(50)
	plan.c.info = &Info{}
	literal := &syntax.RecordLit{Fields: []*syntax.FieldInit{{Name: "text", Value: &syntax.StringLit{Value: strings.Repeat("x", 1000)}}}}
	plan.env = map[string]any{"tag": shapePackageTag{literal: literal}}
	call := &syntax.Call{Fun: &syntax.Selector{X: &syntax.Ident{Name: "tag"}, Name: "value"}}
	if _, ok := plan.eval(call); ok || !plan.failed {
		t.Fatal("oversized tag value escaped the derive expansion budget")
	}
}
