package gen

// labelRuntime reads a goroutine's profiler labels, which test builds
// use to carry per-test state to every goroutine a test starts: the Go
// runtime copies a goroutine's labels to the goroutines it starts. Mocks
// find their frames through them (mockRuntime), and tests run with
// --parallel find which test is running (testRuntime).
const labelRuntime = `package main

import (
	"context"
	"runtime/pprof"
	"sync"
	"unsafe"
)

// _labels gives the current goroutine's profiler label set (nil if it
// has none). It is the hook profilers use; test builds only.
//
//go:linkname _labels runtime/pprof.runtime_getProfLabel
func _labels() unsafe.Pointer

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
// labels differently than test builds expect.
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
		panic("bork: tests cannot read this Go version's profiler labels (runtime/pprof changed); please report it")
	}
}
`
