package check

import (
	"strings"

	"github.com/GiGurra/bork/internal/syntax"
)

// Effects is a set of effects: what a function may do outside its
// arguments and result (see "Effects in signatures" in
// docs/requirements.md). The empty set is pure.
type Effects uint8

const (
	EffIO     Effects = 1 << iota // standard streams, files, the process
	EffNet                        // the network
	EffClock                      // time and waiting
	EffRandom                     // random numbers
	EffState                      // state shared between tasks
)

// effectNames lists the effects in the order they are written.
var effectNames = []struct {
	eff  Effects
	name string
}{
	{EffIO, "io"},
	{EffNet, "net"},
	{EffClock, "clock"},
	{EffRandom, "random"},
	{EffState, "state"},
}

// String writes the effects as a uses declaration lists them:
// "io + net", or "nothing".
func (e Effects) String() string {
	var names []string
	for _, n := range effectNames {
		if e&n.eff != 0 {
			names = append(names, n.name)
		}
	}
	if len(names) == 0 {
		return "nothing"
	}
	return strings.Join(names, " + ")
}

// effectsOf gives the effects of a `uses` declaration (none for nil),
// reporting names that are not effects, and effects named twice.
func (c *checker) effectsOf(u *syntax.Uses) Effects {
	if u == nil {
		return 0
	}
	var effs Effects
	for _, e := range u.Effects {
		eff := effectNamed(e.Name)
		switch {
		case eff == 0:
			c.errorf(e.Pos, "unknown effect %s; the effects are io, net, clock, random, and state", e.Name)
		case effs&eff != 0:
			c.errorf(e.Pos, "effect %s is listed twice", e.Name)
		}
		effs |= eff
	}
	return effs
}

func effectNamed(name string) Effects {
	for _, n := range effectNames {
		if n.name == name {
			return n.eff
		}
	}
	return 0
}
