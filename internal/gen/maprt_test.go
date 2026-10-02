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
	if testing.Short() {
		t.Skip("builds and runs a Go test")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module maprt\n\ngo 1.26\n",
		"map.go":      mapRuntime,
		"show.go":     showRuntime,
		"equal.go":    equalRuntime,
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
