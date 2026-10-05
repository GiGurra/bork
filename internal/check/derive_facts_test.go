package check

import (
	"go/constant"
	"strings"
	"testing"

	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

func factTestPlan(remaining int) *deriveExpansion {
	return &deriveExpansion{
		c:        &checker{diags: &diag.List{}},
		template: &DeriveTemplate{Pkg: &Package{Name: "here", Path: "example/here"}},
		budget:   &deriveBudget{remaining: remaining},
	}
}

func TestDeriveFactTextMatchesResolvedDisplay(t *testing.T) {
	plan := factTestPlan(100000)
	foreign := &Package{Name: "foreign", Path: "example/foreign"}
	plan.template.Pkg.imports = map[string]*Package{"rules": foreign}
	local := &Func{Decl: &syntax.FuncDecl{Name: "local"}, Pkg: plan.template.Pkg}
	external := &Func{Decl: &syntax.FuncDecl{Name: "external"}, Pkg: foreign}
	for _, fact := range []*Constraint{
		{Pred: local},
		{Pred: external, Args: []CArg{{Param: "sibling", Sibling: true}, {Const: constant.MakeString("quoted\"\nå")}, {Const: constant.MakeInt64(-2)}}},
		{PredParam: "keep"},
		{Or: []*Constraint{{Pred: local}, {Pred: external, Args: []CArg{{Const: constant.MakeBool(true)}}}}},
	} {
		got, ok := plan.factText(diag.Pos{}, fact)
		if !ok || got != fact.Text(plan.template.Pkg) {
			t.Fatalf("fact text: got %q/%t, want %q", got, ok, fact.Text(plan.template.Pkg))
		}
	}
}

func TestDeriveFactTextLimits(t *testing.T) {
	t.Run("quoted argument", func(t *testing.T) {
		plan := factTestPlan(50)
		fact := &Constraint{Pred: &Func{Decl: &syntax.FuncDecl{Name: "rule"}, Prelude: true}, Args: []CArg{{Const: constant.MakeString(strings.Repeat("\n", 1000))}}}
		if text, ok := plan.factText(diag.Pos{}, fact); ok || text != "" || !plan.failed {
			t.Fatalf("oversized text accepted: %q/%t", text, ok)
		}
	})
	t.Run("alternative depth", func(t *testing.T) {
		plan := factTestPlan(100000)
		fact := &Constraint{PredParam: "keep"}
		for range 300 {
			fact = &Constraint{Or: []*Constraint{fact}}
		}
		if _, ok := plan.factText(diag.Pos{}, fact); ok || !plan.budget.depthFailed {
			t.Fatal("deep fact text accepted")
		}
	})
	t.Run("combined sequence", func(t *testing.T) {
		plan := factTestPlan(5)
		// A failed count stops before resolving a descriptor type or allocating items.
		if _, ok := plan.factSequence(diag.Pos{}, Int, nil, make([]*Constraint, 3), make([]*Constraint, 3)); ok || !plan.failed {
			t.Fatal("oversized fact sequence accepted")
		}
	})
}
