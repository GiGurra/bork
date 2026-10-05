package check

import (
	"go/constant"
	"math/big"
	"strconv"
	"strings"

	"github.com/GiGurra/bork/internal/diag"
)

// Fact descriptors retain resolved obligations, including their predicate,
// dictionary scope and typed arguments. Display strings never replace them.
type shapeFact struct {
	constraint *Constraint
	owner      Type
	field      *Field
}

func (p *deriveExpansion) factSequence(pos diag.Pos, owner Type, field *Field, groups ...[]*Constraint) (any, bool) {
	count := 0
	for _, facts := range groups {
		if !p.charge(pos, len(facts)) {
			return nil, false
		}
		count += len(facts)
	}
	items := make([]any, 0, count)
	for _, facts := range groups {
		for _, fact := range facts {
			items = append(items, shapeFact{constraint: fact, owner: owner, field: field})
		}
	}
	return shapeSequence{items: items, element: p.descriptorType("Fact", owner)}, true
}

func targetShapeFacts(target Type) []*Constraint {
	switch target := target.(type) {
	case *Record:
		return target.Constraints
	case *Sealed:
		return target.Constraints
	}
	return nil
}

// Format into one bounded buffer. Charging each component first also bounds
// quoting and numeric rendering; alternatives never allocate intermediate text.
func (p *deriveExpansion) factText(pos diag.Pos, fact *Constraint) (string, bool) {
	var out strings.Builder
	write := func(text string) bool {
		if !p.charge(pos, len(text)) {
			return false
		}
		out.WriteString(text)
		return true
	}
	var format func(*Constraint) bool
	format = func(fact *Constraint) bool {
		if !p.enter(pos) {
			return false
		}
		defer func() { p.budget.depth-- }()
		if fact.Or != nil {
			for i, alternative := range fact.Or {
				if i > 0 && !write(" or ") || !format(alternative) {
					return false
				}
			}
			return true
		}
		if fact.PredParam != "" {
			return write(fact.PredParam)
		}
		fn := fact.Pred
		if !fn.Prelude && fn.Pkg != nil && fn.Pkg != p.template.Pkg && fn.Pkg.Path != "" {
			qualifier := fn.Pkg.Name
			for alias, pkg := range p.template.Pkg.imports {
				if pkg == fn.Pkg {
					qualifier = alias
					break
				}
			}
			if !write(qualifier) || !write(".") {
				return false
			}
		}
		if !write(fn.Decl.Name) {
			return false
		}
		if len(fact.Args) == 0 {
			return true
		}
		if !write("(") {
			return false
		}
		for i, arg := range fact.Args {
			if i > 0 && !write(", ") {
				return false
			}
			if arg.Const == nil {
				if !write(arg.Param) {
					return false
				}
				continue
			}
			if arg.Const.Kind() == constant.String {
				text := constant.StringVal(arg.Const)
				// Quote emits at most six characters per input byte plus quotes.
				for range 6 {
					if !p.charge(pos, len(text)) {
						return false
					}
				}
				if !p.charge(pos, 2) {
					return false
				}
				out.WriteString(strconv.Quote(text))
				continue
			}
			// Reserve an upper bound for arbitrary precision numeric constants
			// before their formatter builds decimal numerator/denominator text.
			reserve := 64
			switch number := constant.Val(arg.Const).(type) {
			case *big.Int:
				reserve += number.BitLen()
			case *big.Rat:
				reserve += number.Num().BitLen() + number.Denom().BitLen()
			case *big.Float:
				reserve += int(number.Prec())
			}
			if !p.charge(pos, reserve) || !write(arg.Const.String()) {
				return false
			}
		}
		return write(")")
	}
	if !format(fact) {
		return "", false
	}
	return out.String(), true
}
