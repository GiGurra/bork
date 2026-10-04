package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMapRuntime runs random operations on the persistent map runtime
// and compares it with a plain Go map and slice, with hash functions
// that force collisions at every level of the trie.
func TestMapRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a Go test")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module maprt\n\ngo 1.26\n",
		"map.go":      mapRuntime,
		"show.go":     showRuntime,
		"equal.go":    equalRuntime,
		"hash.go":     hashRuntime,
		"map_test.go": mapRuntimeTest,
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("map runtime test failed: %v\n%s", err, out)
	}
}

const mapRuntimeTest = `package main

import (
	"math"
	"math/rand"
	"slices"
	"testing"
)

type model struct {
	keys []int64
	vals map[int64]string
}

func (m model) put(k int64, v string) model {
	out := model{keys: slices.Clone(m.keys), vals: map[int64]string{}}
	for k, v := range m.vals {
		out.vals[k] = v
	}
	if _, ok := m.vals[k]; !ok {
		out.keys = append(out.keys, k)
	}
	out.vals[k] = v
	return out
}

func (m model) remove(k int64) model {
	out := model{vals: map[int64]string{}}
	for _, x := range m.keys {
		if x != k {
			out.keys = append(out.keys, x)
			out.vals[x] = m.vals[x]
		}
	}
	return out
}

func check(t *testing.T, step int, m _Map[int64, string], want model) {
	t.Helper()
	if m.len() != len(want.keys) {
		t.Fatalf("step %d: len %d, want %d", step, m.len(), len(want.keys))
	}
	if got := m.keys(); !slices.Equal(got, want.keys) {
		t.Fatalf("step %d: keys %v, want %v", step, got, want.keys)
	}
	for k := int64(0); k < 300; k++ {
		v, ok := m.get(k)
		wv, wok := want.vals[k]
		if ok != wok || v != wv {
			t.Fatalf("step %d: get(%d) = %q %v, want %q %v", step, k, v, ok, wv, wok)
		}
	}
}

func TestSorted(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	var empty _Map[int64, string]
	m := _mapSortedBy(empty, func(a, b int64) bool { return a < b })
	want := model{vals: map[int64]string{}}
	var old []_Map[int64, string]
	var olds []model
	for step := 0; step < 5000; step++ {
		k := int64(r.Intn(300))
		if r.Intn(3) == 0 {
			m, want = m.remove(k), want.remove(k)
		} else {
			v := string(rune('a' + r.Intn(26)))
			m, want = m.put(k, v), want.put(k, v)
		}
		sorted := model{keys: slices.Sorted(slices.Values(want.keys)), vals: want.vals}
		check(t, step, m, sorted)
		if step%250 == 0 {
			old, olds = append(old, m), append(olds, sorted)
		}
	}
	for i := range old {
		check(t, -i, old[i], olds[i])
	}
	// The same entries as an insertion-ordered map are equal.
	if !_equal(m, _mapInOrder(m)) || !_equal(_mapInOrder(m), m) {
		t.Fatal("sorted and insertion-ordered maps with the same entries differ")
	}
	doubled := _mapValues(m, func(s string) string { return s + s })
	if doubled.len() != m.len() || !slices.Equal(doubled.keys(), m.keys()) {
		t.Fatal("mapValues changed the keys")
	}
}

func TestUnordered(t *testing.T) {
	hashes := map[string]func(any) uint64{
		"real":      _mapHash,
		"constant":  func(any) uint64 { return 42 },
		"few bits":  func(k any) uint64 { return uint64(k.(int64) % 7) },
		"high bits": func(k any) uint64 { return uint64(k.(int64)%5) << 61 },
	}
	real := _mapHash
	for name, h := range hashes {
		t.Run(name, func(t *testing.T) {
			_mapHash = h
			defer func() { _mapHash = real }()
			r := rand.New(rand.NewSource(3))
			var empty _Map[int64, string]
			m := _mapUnordered(empty)
			want := model{vals: map[int64]string{}}
			var old []_Map[int64, string]
			var olds []model
			for step := 0; step < 3000; step++ {
				k := int64(r.Intn(300))
				if r.Intn(3) == 0 {
					m, want = m.remove(k), want.remove(k)
				} else {
					v := string(rune('a' + r.Intn(26)))
					m, want = m.put(k, v), want.put(k, v)
				}
				// Compare in sorted order: the map's own order is the hashes'.
				got := _mapSortedBy(m, func(a, b int64) bool { return a < b })
				check(t, step, got, model{keys: slices.Sorted(slices.Values(want.keys)), vals: want.vals})
				if step%250 == 0 {
					old, olds = append(old, m), append(olds, want)
				}
			}
			for i := range old {
				got := _mapSortedBy(old[i], func(a, b int64) bool { return a < b })
				check(t, -i, got, model{keys: slices.Sorted(slices.Values(olds[i].keys)), vals: olds[i].vals})
			}
			if !_equal(m, _mapInOrder(m)) {
				t.Fatal("unordered and insertion-ordered maps with the same entries differ")
			}
			doubled := _mapValues(m, func(s string) string { return s + s })
			if v, ok := doubled.get(want.keys[0]); !ok || v != want.vals[want.keys[0]]+want.vals[want.keys[0]] {
				t.Fatal("mapValues is wrong")
			}
		})
	}
}

func TestMapPrinting(t *testing.T) {
	real := _mapHash
	defer func() { _mapHash = real }()
	for _, hash := range []func(any) uint64{real, func(any) uint64 { return 0 }, func(k any) uint64 { return ^real(k) }} {
		_mapHash = hash
		ints := _mapOf([]int64{10, 2, -1}, []string{"ten", "two", "minus"})
		unordered := _mapUnordered(ints)
		before := unordered.keys()
		if got := unordered.String(); got != "{-1: \"minus\", 2: \"two\", 10: \"ten\"}" {
			t.Fatalf("numeric order: %s", got)
		}
		if !slices.Equal(before, unordered.keys()) {
			t.Fatal("printing changed traversal order")
		}
		if got := ints.String(); got != "{10: \"ten\", 2: \"two\", -1: \"minus\"}" {
			t.Fatalf("insertion order: %s", got)
		}
		desc := _mapSortedBy(ints, func(a, b int64) bool { return a > b })
		if got := desc.String(); got != "{10: \"ten\", 2: \"two\", -1: \"minus\"}" {
			t.Fatalf("custom sort order: %s", got)
		}
		unsigned := _mapUnordered(_mapOf([]uint64{10, 2, 0}, []int64{10, 2, 0}))
		if got := unsigned.String(); got != "{0: 0, 2: 2, 10: 10}" {
			t.Fatalf("unsigned order: %s", got)
		}
		floats := _mapUnordered(_mapOf([]float64{10, -2.5, 0.25}, []int64{10, -2, 0}))
		if got := floats.String(); got != "{-2.5: -2, 0.25: 0, 10.0: 10}" {
			t.Fatalf("float order: %s", got)
		}
		lists := _mapUnordered(_mapOf([][]int64{{2}, {10}}, []int64{2, 10}))
		if got := lists.String(); got != "{[10]: 10, [2]: 2}" {
			t.Fatalf("text fallback: %s", got)
		}
		mixed := _mapUnordered(_mapOf([]any{int64(10), "a", int64(2)}, []int64{10, 1, 2}))
		if got := mixed.String(); got != "{2: 2, 10: 10, \"a\": 1}" {
			t.Fatalf("mixed keys: %s", got)
		}
		ties := _mapUnordered(_mapOf([]printKey{{2}, {1}}, []int64{20, 10}))
		if got := ties.String(); got != "{same: 10, same: 20}" {
			t.Fatalf("text ties: %s", got)
		}
		nested := _mapUnordered(_mapOf([]string{"z", "a"}, []_Map[int64, string]{unordered, unordered}))
		if got := nested.String(); got != "{\"a\": {-1: \"minus\", 2: \"two\", 10: \"ten\"}, \"z\": {-1: \"minus\", 2: \"two\", 10: \"ten\"}}" {
			t.Fatalf("nested maps: %s", got)
		}
		if got := _mapUnordered(_Map[int64, string]{}).String(); got != "{:}" {
			t.Fatalf("empty map: %s", got)
		}
	}
}

type printKey struct { id int64 }

func (printKey) String() string { return "same" }

func TestStructuralKeys(t *testing.T) {
	real := _mapHash
	defer func() { _mapHash = real }()
	for _, hash := range []func(any) uint64{real, func(any) uint64 { return 42 }} {
		_mapHash = hash
		m := _mapOf([][]int64{nil, {1, 2}, {2, 1}}, []int64{0, 12, 21})
		if v, ok := m.get([]int64{}); !ok || v != 0 { t.Fatal("nil and empty list keys differ") }
		changed := m.put([]int64{}, 3).remove([]int64{1, 2})
		if changed.len() != 2 || m.len() != 3 { t.Fatal("structural key update/remove changed size or old map") }
		if v, ok := changed.get([]int64{2, 1}); !ok || v != 21 { t.Fatal("collision lost another list key") }
		// These unequal keys have identical text; text is not identity.
		ties := _mapOf([][]printKey{{{1}}, {{2}}}, []int64{10, 20})
		if ties.len() != 2 { t.Fatal("identical text conflated unequal list keys") }
		if v, ok := ties.get([]printKey{{2}}); !ok || v != 20 { t.Fatal("list key lookup is wrong") }
		a := _mapOf([]int64{1, 2}, []string{"one", "two"})
		b := _mapUnordered(_mapOf([]int64{2, 1}, []string{"two", "one"}))
		nested := _mapOf([]_Map[int64, string]{a}, []int64{12})
		if v, ok := nested.get(b); !ok || v != 12 { t.Fatal("map keys depend on entry order or map kind") }
	}
	zero := _mapOf([]float64{0}, []int64{1})
	if v, ok := zero.get(math.Copysign(0, -1)); !ok || v != 1 { t.Fatal("signed zero keys differ") }
	for _, xs := range [][]int8{nil, {}, {-1, 2, 3}} {
		if _hash(xs) != _hashList(xs, _hashOf[int8]) { t.Fatal("typed and fallback int8 hashes differ") }
	}
	for _, xs := range [][]float64{nil, {}, {0, -2.5, 3}} {
		if _hash(xs) != _hashList(xs, _hashOf[float64]) { t.Fatal("typed and fallback float hashes differ") }
	}
	for _, xs := range [][]string{nil, {}, {"a", "b"}} {
		if _hash(xs) != _hashList(xs, _hashOf[string]) { t.Fatal("typed and fallback string hashes differ") }
	}
	for _, xs := range [][]int64{nil, {}, {1, 2, 3}} {
		if _hash(xs) != _hashList(xs, _hashOf[int64]) { t.Fatal("typed and fallback list hashes differ") }
	}
}

func TestRandom(t *testing.T) {
	hashes := map[string]func(any) uint64{
		"real":      _mapHash,
		"constant":  func(any) uint64 { return 42 },
		"few bits":  func(k any) uint64 { return uint64(k.(int64) % 7) },
		"high bits": func(k any) uint64 { return uint64(k.(int64)%5) << 61 },
	}
	for name, h := range hashes {
		t.Run(name, func(t *testing.T) {
			_mapHash = h
			r := rand.New(rand.NewSource(1))
			var m _Map[int64, string]
			want := model{vals: map[int64]string{}}
			// Old versions, which must not change.
			type version struct {
				m    _Map[int64, string]
				want model
			}
			var old []version
			for step := 0; step < 3000; step++ {
				k := int64(r.Intn(300))
				if r.Intn(3) == 0 {
					m, want = m.remove(k), want.remove(k)
				} else {
					v := string(rune('a' + r.Intn(26)))
					m, want = m.put(k, v), want.put(k, v)
				}
				check(t, step, m, want)
				if step%100 == 0 {
					old = append(old, version{m, want})
				}
			}
			for i, v := range old {
				check(t, -i, v.m, v.want)
			}
			// Equality ignores order.
			a := _mapOf([]int64{1, 2, 3}, []string{"x", "y", "z"})
			b := _mapOf([]int64{3, 1, 2}, []string{"z", "x", "y"})
			if !_equal(a, b) || _equal(a, b.put(1, "q")) || _equal(a, a.remove(2)) {
				t.Fatal("map equality is wrong")
			}
		})
	}
}
`
