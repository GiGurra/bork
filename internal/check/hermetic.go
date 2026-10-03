package check

import "github.com/GiGurra/bork/internal/diag"

// Hermetic tests (bork-x0g9au; see "Hermetic tests" in
// docs/requirements.md): a test is hermetic for an effect if every
// function doing it in Go code (an unsafe go body or a Go binding that
// declares it) that the test can reach is mocked where the test reaches
// it. The checker records each call in a test (its body, lambdas and
// mock bodies) with the mocks in force there; Unmocked follows the
// calls from each, stopping at what a mock answers.

// mockInForce is a mock in force at a point of a test; whole if it is
// in force for all of the test (see mockStmt).
type mockInForce struct {
	target *Func
	depth  int
	whole  bool
}

// testSite is a call (or function value) in a test, and the targets of
// the mocks in force there.
type testSite struct {
	fn      *Func
	pos     diag.Pos
	inForce []*Func
	inMock  *Func // the target of the mock whose body it is in
}

// testCall records a call of fn at pos, if it is in a test. Mocks
// apply by goroutine, where code runs, not where it is written: a call
// in a lambda, or a function used as a value, may run after the mock's
// block, or on a task started before the mock, so only the mocks in
// force for all of the test cover it. A call made where it is written
// is covered by the mocks in force there.
func (c *checker) testCall(fn *Func, pos diag.Pos, value bool) {
	test := c.fn
	if test.MockIn != nil {
		test = test.MockIn
	}
	if test.Test == nil {
		return
	}
	site := testSite{fn: fn, pos: pos, inMock: c.fn.MockOf}
	later := value || c.lambdaDepth > 0
	for _, m := range c.inForce {
		if m.whole || !later {
			site.inForce = append(site.inForce, m.target)
		}
	}
	c.info.testSites[test] = append(c.info.testSites[test], site)
}

// Mockable reports whether a test of package from can mock fn (see
// mockTarget): a declared function with effects, of from or exported,
// that is not a predicate, a class method, or the prelude's.
func (fn *Func) Mockable(from *Package) bool {
	switch {
	case fn.Prelude || fn.Class != nil || fn.Of != nil || fn.Derived != nil || fn.Decl == nil || fn.Decl.IsPred:
		return false
	case fn.Effects&^EffOpen == 0:
		return false
	}
	return fn.Pkg == from || Exported(fn.Decl.Name)
}

// UnmockedPath is how a test reaches, unmocked, a function that does
// an effect in Go code: the call in the test, and the functions from
// there to it (the last). InMock is the target of the mock whose body
// makes the call, if one does.
type UnmockedPath struct {
	Pos    diag.Pos
	Funcs  []*Func
	InMock *Func
}

// Unmocked lists, for the test, a path to each function that does one
// of effects in Go code and that the test reaches with no mock of it,
// or of a function on the way, in force (one path per function, the
// first found).
func (info *Info) Unmocked(test *Func, effects Effects) []UnmockedPath {
	var out []UnmockedPath
	found := map[*Func]bool{}
	for _, site := range info.testSites[test] {
		mocked := map[*Func]bool{}
		for _, t := range site.inForce {
			mocked[t] = true
		}
		for _, p := range info.unmockedFrom(site.fn, mocked, effects, found) {
			out = append(out, UnmockedPath{Pos: site.pos, Funcs: p, InMock: site.inMock})
		}
	}
	return out
}

// Reaching lists a path from fn to each function doing one of effects
// in Go code that calling fn, with no mocks, can reach.
func (info *Info) Reaching(fn *Func, effects Effects) [][]*Func {
	return info.unmockedFrom(fn, nil, effects, map[*Func]bool{})
}

// unmockedFrom follows the calls from fn, stopping at mocked functions,
// to the functions doing effects in Go code not in found (adding them).
// A class method's calls are those of its instances' methods.
func (info *Info) unmockedFrom(fn *Func, mocked map[*Func]bool, effects Effects, found map[*Func]bool) [][]*Func {
	var out [][]*Func
	seen := map[*Func]bool{}
	var path []*Func
	var walk func(f *Func)
	walk = func(f *Func) {
		if f == nil || seen[f] || mocked[f] {
			return
		}
		seen[f] = true
		path = append(path, f)
		defer func() { path = path[:len(path)-1] }()
		if f.Effects&effects != 0 && f.Decl != nil && f.Decl.IsGo() && !found[f] {
			found[f] = true
			out = append(out, append([]*Func(nil), path...))
		}
		for _, callee := range f.Calls {
			walk(callee)
		}
		if f.Class != nil && f.Of == nil {
			for _, ci := range info.ClassInstances {
				if ci.Class != f.Class {
					continue
				}
				for _, m := range ci.Methods {
					if m.Decl != nil && m.Decl.Name == f.Decl.Name {
						walk(m)
					}
				}
			}
		}
	}
	walk(fn)
	return out
}
