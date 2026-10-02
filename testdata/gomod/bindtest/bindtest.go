// Package bindtest is a Go package that bork test cases bind, for the
// shapes Go's standard library does not have. The driver's tests add it
// to generated programs' go.mod (see goModuleHook).
package bindtest

import "errors"

func ptr[T any](v T) *T { return &v }

// Results with pointers, nested slices and maps, and arrays.
func Ptrs() []*int                   { return []*int{ptr(1), nil, ptr(3)} }
func PtrPtr() **int                  { return ptr(ptr(7)) }
func NilPtrPtr() **int               { return ptr((*int)(nil)) }
func Nested() map[string][]int       { return map[string][]int{"a": {1, 2}, "b": nil} }
func Big() map[string]int64          { return map[string]int64{"a": 1, "b": 1000} }
func Pair() [2]string                { return [2]string{"x", "y"} }
func Digest() [4]byte                { return [4]byte{0xca, 0xfe, 0xba, 0xbe} }
func NilSlice() []string             { return nil }
func NilMap() map[string]int         { return nil }
func NilResult() (*int, error)       { return nil, nil }
func Failing() (*int, error)         { return nil, errors.New("it failed") }
func Found(n int64) (int64, bool)    { return n, n >= 0 }
func Name(p *string) string          { return deref(p, "nobody") }
func Count(m map[string][]*int8) int { return countSet(m) }
func Deref(p *[]string) []string     { return *p }
func Prefixed(prefix string, xs ...*int) []string {
	out := []string{}
	for _, x := range xs {
		if x == nil {
			out = append(out, prefix+"nil")
		} else {
			out = append(out, prefix+string(rune('0'+*x)))
		}
	}
	return out
}

// IntPtr is a named pointer: a nil one without an error is still a
// broken result.
type IntPtr *int

func NamedNil() (IntPtr, error) { return nil, nil }

// Go code that keeps a slice it was given, or one it returned, and
// changes it later must not change bork's lists.
var kept, held []int

func Keep(xs []int) { kept = xs }
func ChangeKept()   { kept[0] = 99 }
func Held() []int   { held = []int{1, 2}; return held }
func ChangeHeld()   { held[0] = 99 }

// For the checker's errors.
type lower int

func TakeLower(x lower) int         { return int(x) }
func PtrKeys() map[*int]string      { return nil }
func FloatKeys() map[float64]string { return nil }

func deref(p *string, or string) string {
	if p == nil {
		return or
	}
	return *p
}

func countSet(m map[string][]*int8) int {
	n := 0
	for _, xs := range m {
		for _, x := range xs {
			if x != nil {
				n++
			}
		}
	}
	return n
}
