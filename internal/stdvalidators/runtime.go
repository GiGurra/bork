// Package stdvalidators executes generated, pinned standard-library intrinsics.
//
//go:generate go run ../gen/cmd/genstdvalidators runtime.go generated.go
package stdvalidators

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

const ABI = 1
const InputLimit = 1 << 20

type Kind struct{ Tag, PackagePath, Name string }
type Request struct {
	Parts []string
	Holes [][]Kind
}
type Source struct{ Path, Digest string }
type Descriptor struct {
	Package, Instance, Builder, Signature, Identity string
	Sources                                         []Source
	Definitions                                     []Source
}
type Binding struct {
	index      int
	descriptor Descriptor
}
type Call struct {
	Binding Binding
	Request Request
}

// Generated code supplies the registry and native evaluator. An absent artifact
// is an ordinary fallback, never permission to evaluate an unknown implementation.
var descriptors []Descriptor
var nativeEvaluation func([]Call) []byte

func Lookup(pkg, instance, builder, signature string) (Binding, bool) {
	for i, d := range descriptors {
		if d.Package == pkg && d.Instance == instance && d.Builder == builder && d.Signature == signature {
			d.Sources = slices.Clone(d.Sources)
			d.Definitions = slices.Clone(d.Definitions)
			return Binding{index: i, descriptor: d}, true
		}
	}
	return Binding{}, false
}
func (b Binding) Descriptor() Descriptor {
	d := b.descriptor
	d.Sources = slices.Clone(d.Sources)
	d.Definitions = slices.Clone(d.Definitions)
	return d
}

var nativeMutex sync.Mutex
var nativeDeadline time.Time
var nativeSteps, nativeDepth, nativeBytes int

// BudgetError identifies limits introduced by the intrinsic execution mode.
// The driver may retry these in the ordinary evaluator with remaining time.
type BudgetError struct{ Kind string }

func (e *BudgetError) Error() string { return "standard validator " + e.Kind + " limit exceeded" }

type budgetPanic string

// Every generated call and function entry/loop reaches these guards. Their
// implementation is compiler-owned support, outside the instrumented closure.
func nativeStep() {
	nativeSteps++
	if nativeSteps > 4_000_000 {
		panic(budgetPanic("execution step"))
	}
	if nativeSteps&63 == 0 && !time.Now().Before(nativeDeadline) {
		panic("standard validator evaluation deadline exceeded")
	}
}

// Conservatively charge growing buffers before allocation or writes.
func nativeAllocate(size int) {
	if size < 0 || size > (16<<20)-nativeBytes {
		panic(budgetPanic("allocation"))
	}
	nativeBytes += size
}
func nativeExpansion(source, replacement int) int {
	if source < 0 || source >= 16<<20 || replacement < 0 || replacement > ((16<<20)/(source+1)) {
		panic(budgetPanic("allocation"))
	}
	return source + (source+1)*replacement
}
func nativeEnter() {
	nativeStep()
	nativeDepth++
	if nativeDepth > 256 {
		panic(budgetPanic("recursion"))
	}
}
func nativeLeave() { nativeDepth-- }

// Run executes fresh metadata-only calls. The lock isolates generated encoder
// state; acquisition respects the same deadline as evaluation.
func Run(calls []Call, limit time.Duration) (out []byte, err error) {
	if nativeEvaluation == nil {
		return nil, fmt.Errorf("standard validator artifact unavailable")
	}
	deadline := time.Now().Add(limit)
	for !nativeMutex.TryLock() {
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("standard validator evaluation deadline exceeded")
		}
		time.Sleep(time.Millisecond)
	}
	defer nativeMutex.Unlock()
	nativeDeadline = deadline
	nativeSteps = 0
	nativeDepth = 0
	nativeBytes = 0
	defer func() {
		if value := recover(); value != nil {
			out = nil
			if budget, ok := value.(budgetPanic); ok {
				err = &BudgetError{Kind: string(budget)}
			} else {
				if message, ok := value.(string); ok {
					err = fmt.Errorf("standard validator failed: %s", message)
				} else {
					err = fmt.Errorf("standard validator failed with panic type %T", value)
				}
			}
		}
	}()
	total := 0
	for _, call := range calls {
		nativeStep()
		total += 32
		b := call.Binding
		if b.index < 0 || b.index >= len(descriptors) || b.descriptor.Identity != descriptors[b.index].Identity {
			return nil, fmt.Errorf("invalid standard validator binding")
		}
		r := call.Request
		if len(r.Parts) != len(r.Holes)+1 {
			return nil, fmt.Errorf("invalid standard validator parts")
		}
		for _, part := range r.Parts {
			total += 16 + len(part)
		}
		for _, hole := range r.Holes {
			total += 32
			if len(hole) == 0 {
				return nil, fmt.Errorf("invalid standard validator hole kinds")
			}
			for _, kind := range hole {
				total += 32 + len(kind.Tag) + len(kind.PackagePath) + len(kind.Name)
			}
		}
		if total > InputLimit {
			return nil, fmt.Errorf("standard validator input limit exceeded")
		}
	}
	out = nativeEvaluation(calls)
	if !time.Now().Before(deadline) {
		return nil, fmt.Errorf("standard validator evaluation deadline exceeded")
	}
	return out, nil
}
