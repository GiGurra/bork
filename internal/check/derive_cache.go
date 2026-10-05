package check

import (
	"fmt"
	"slices"
	"strings"
)

// Cache identities retain resolved nominal types, predicates, argument types,
// alternatives, paths and the package selecting predicate dictionaries. They
// do not depend on a display spelling or on where the obligation was written.
func deriveFactsKey(facts []*Constraint) string {
	keys := make([]string, len(facts))
	for i, fact := range facts {
		keys[i] = deriveFactKey(fact)
	}
	slices.Sort(keys)
	return strings.Join(keys, "\x01")
}

func deriveFactKey(fact *Constraint) string {
	if fact == nil {
		return "nil"
	}
	key := fact.Path + "\x00" + fact.PredParam
	if fact.Pred != nil {
		key += fmt.Sprintf("\x00%p", fact.Pred)
	}
	if fact.Pkg != nil {
		key += "\x00" + fact.Pkg.Path
	}
	for _, argument := range fact.Args {
		typ := "nil"
		if argument.Type != nil {
			typ = typeKey(argument.Type)
		}
		key += fmt.Sprintf("\x00%s:%s:%t", argument.String(), typ, argument.Sibling)
	}
	if len(fact.Or) != 0 {
		key += "\x00or(" + deriveFactsKey(fact.Or) + ")"
	}
	return key
}
