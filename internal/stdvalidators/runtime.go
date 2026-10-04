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

// Every generated call and function entry/loop reaches these guards. Their
// implementation is compiler-owned support, outside the instrumented closure.
func nativeStep() {
	nativeSteps++
	if nativeSteps > 4_000_000 {
		panic("standard validator execution step limit exceeded")
	}
	if nativeSteps&63 == 0 && !time.Now().Before(nativeDeadline) {
		panic("standard validator evaluation deadline exceeded")
	}
}

// Conservatively charge growing buffers before allocation or writes.
func nativeAllocate(size int) {
	if size < 0 || size > (16<<20)-nativeBytes {
		panic("standard validator allocation limit exceeded")
	}
	nativeBytes += size
}
func nativeEnter() {
	nativeStep()
	nativeDepth++
	if nativeDepth > 256 {
		panic("standard validator recursion limit exceeded")
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
			err = fmt.Errorf("standard validator failed: %v", value)
		}
	}()
	total := 0
	for _, call := range calls {
		b := call.Binding
		if b.index < 0 || b.index >= len(descriptors) || b.descriptor.Identity != descriptors[b.index].Identity {
			return nil, fmt.Errorf("invalid standard validator binding")
		}
		r := call.Request
		if len(r.Parts) != len(r.Holes)+1 {
			return nil, fmt.Errorf("invalid standard validator parts")
		}
		for _, part := range r.Parts {
			total += len(part)
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
