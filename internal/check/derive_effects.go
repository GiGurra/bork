package check

import (
	"github.com/GiGurra/bork/internal/diag"
	"github.com/GiGurra/bork/internal/syntax"
)

// Signature parsing owns diagnostics for unknown and repeated effects. This
// reads only known declared effects without repeating those errors at calls.
func deriveWrittenEffects(written *syntax.Uses) Effects {
	var effects Effects
	if written != nil {
		for _, effect := range written.Effects {
			effects |= effectNamed(effect.Name)
		}
	}
	return effects
}

// Only declared signature positions are open; a local function annotation
// without uses remains pure. This mirrors openAt/openParamAt for symbolic heads.
func (c *checker) deriveOpenSignatureTerm(term *deriveTypeTerm, written *syntax.TypeExpr, parameter bool) *deriveTypeTerm {
	if term == nil || written == nil {
		return term
	}
	copy := *term
	if parameter && term.head == "List" && written.Name == "List" && len(written.Args) == 1 && len(term.args) == 1 {
		copy.args = []*deriveTypeTerm{c.deriveOpenSignatureTerm(term.args[0], written.Args[0], false)}
	} else if term.head == "function" && c.unannotatedFunc(written, 0) {
		copy.effects |= EffOpen
		if copy.native != nil {
			copy.native = c.openAt(copy.native, written)
		}
	}
	return &copy
}

// Independent declared effects do not need a concrete target or execution.
// Open callback positions and unresolved calls remain expansion obligations.
func (c *checker) checkDeriveKnownEffects(pos diag.Pos, used, allowed Effects, name string) {
	if allowed&EffOpen != 0 {
		return
	}
	if missing := used &^ (allowed | EffOpen); missing != 0 {
		c.diags.AddCode(pos, "effect.missing", "derive %s uses %s, but its signature allows %s", name, missing, allowedText(allowed))
	}
}
