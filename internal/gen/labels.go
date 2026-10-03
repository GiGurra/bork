package gen

// labelRuntime reads and sets a goroutine's profiler labels, which
// carry state to every goroutine started from it: the Go runtime copies
// a goroutine's labels to the goroutines it starts. Mocks find their
// frames through them (mockRuntime), tests run with --parallel find
// which test is running (testRuntime), and with publishes logged and
// propagated ambient values in them (ambientRuntime).
const labelRuntime = `package main

import (
	"context"
	"runtime/pprof"
	"sync"
	"unsafe"
)

// _labels gives the current goroutine's profiler label set (nil if it
// has none). It is the hook profilers use.
//
//go:linkname _labels runtime/pprof.runtime_getProfLabel
func _labels() unsafe.Pointer

// _setLabels gives the current goroutine the label set p, as _labels
// gave it: so code that ends puts back the very labels it found, which
// _mockFrames knows.
//
//go:linkname _setLabels runtime/pprof.runtime_setProfLabel
func _setLabels(p unsafe.Pointer)

// _labelsMoved, if set, is told when _labelsSet replaces the label set
// from with the label set to.
var _labelsMoved func(from, to unsafe.Pointer)

// _labelsSet gives the current goroutine (and the goroutines it starts
// from now on) its labels with kv (key, value, ...) set: a key with
// the value "" is removed.
func _labelsSet(kv ...string) {
	_labelsCheck.Do(_labelsCheckLayout)
	from := _labels()
	var list []string
	if from != nil {
		list = _labelList(from)
	}
	for i := 0; i < len(kv); i += 2 {
		j := 0
		for j < len(list) && list[j] != kv[i] {
			j += 2
		}
		switch {
		case j < len(list) && kv[i+1] == "":
			list = append(list[:j], list[j+2:]...)
		case j < len(list):
			list[j+1] = kv[i+1]
		case kv[i+1] != "":
			list = append(list, kv[i], kv[i+1])
		}
	}
	pprof.SetGoroutineLabels(pprof.WithLabels(context.Background(), pprof.Labels(list...)))
	if _labelsMoved != nil {
		_labelsMoved(from, _labels())
	}
}

// _labelSet is how runtime/pprof stores a label set: what _labels
// points to. _labelsCheckLayout checks that it still is.
type _labelSet struct {
	list []struct{ key, value string }
}

// _labelList lists the labels of a label set as key, value, key,
// value, ...
func _labelList(p unsafe.Pointer) []string {
	var kv []string
	for _, l := range (*_labelSet)(p).list {
		kv = append(kv, l.key, l.value)
	}
	return kv
}

// _label is the current goroutine's label key, or "".
func _label(key string) string {
	p := _labels()
	if p == nil {
		return ""
	}
	for _, l := range (*_labelSet)(p).list {
		if l.key == key {
			return l.value
		}
	}
	return ""
}

var _labelsCheck sync.Once

// _labelsCheckLayout fails loudly if this Go version stores profiler
// labels differently than bork expects.
func _labelsCheckLayout() {
	ctx := pprof.WithLabels(context.Background(), pprof.Labels("bork.check", "1"))
	done := make(chan []string)
	go func() {
		pprof.SetGoroutineLabels(ctx)
		p := _labels()
		if p == nil {
			done <- nil
			return
		}
		done <- _labelList(p)
	}()
	if kv := <-done; len(kv) != 2 || kv[0] != "bork.check" || kv[1] != "1" {
		panic("bork: cannot read this Go version's profiler labels (runtime/pprof changed); please report it")
	}
}
`
