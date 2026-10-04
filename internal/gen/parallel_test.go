package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParallelRuntime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and runs a generated Go test")
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"go.mod":           "module parallelrt\n\ngo 1.26\n",
		"scope.go":         scopeRuntime,
		"parallel.go":      parallelRuntime,
		"parallel_test.go": parallelRuntimeTest,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", runtimeTestArgs()...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("parallel runtime: %v\n%s", err, out)
	}
}

const parallelRuntimeTest = `package main

import (
 "context"
 "sync/atomic"
 "testing"
 "time"
)

func TestBoundAndJoin(t *testing.T) {
 for _, scoped := range []bool{false, true} {
  var s *_Scope
  if scoped { s = _scopeWith(context.Background()) }
  entered := make(chan struct{}, 2)
  release := make(chan struct{})
  var active, peak, calls atomic.Int64
  done := make(chan bool)
  go func() {
   done <- _borkParallel(s, 100, 2, func(i int) bool {
    n := active.Add(1)
    for old := peak.Load(); n > old; old = peak.Load() { if peak.CompareAndSwap(old, n) { break } }
    if i < 2 { entered <- struct{}{}; <-release }
    active.Add(-1)
    calls.Add(1)
    return true
   })
  }()
  for i := 0; i < 2; i++ {
   select { case <-entered: case <-time.After(5*time.Second): t.Fatal("workers did not run concurrently") }
  }
  close(release)
  if !<-done || calls.Load() != 100 || active.Load() != 0 || peak.Load() != 2 { t.Fatal("worker bound or join violated") }
  if s != nil { s.close() }
 }
}

func TestStop(t *testing.T) {
 var calls int
 if _borkParallel(nil, 100, 1, func(i int) bool { calls++; return false }) || calls != 1 { t.Fatal("failed to stop") }
 s := _scopeWith(context.Background())
 s.cancel(_errCancelled)
 if _borkParallel(s, 100, 2, func(i int) bool { t.Error("called after cancellation"); return true }) { t.Fatal("missed cancellation") }
 s.close()
}

func TestPanicJoinsAndCancels(t *testing.T) {
 s := _scopeWith(context.Background())
 entered := make(chan struct{})
 var finished atomic.Bool
 func() {
  defer func() {
   if recover() != "worker panic" { t.Error("panic was lost") }
   if !finished.Load() { t.Error("panic escaped before sibling finished") }
  }()
  _borkParallel(s, 2, 2, func(i int) bool {
   if i == 0 { <-entered; panic("worker panic") }
   close(entered)
   <-s.ctx.Done()
   finished.Store(true)
   return true
  })
 }()
 if s.ctx.Err() == nil { t.Fatal("panic did not cancel scope") }
 s.close()
}

func TestPurePanicJoins(t *testing.T) {
 entered := make(chan struct{})
 release := make(chan struct{})
 var finished atomic.Bool
 caught := make(chan any)
 go func() {
  defer func() { caught <- recover() }()
  _borkParallel(nil, 2, 2, func(i int) bool {
   if i == 0 { <-entered; panic("pure panic") }
   close(entered)
   <-release
   finished.Store(true)
   return true
  })
 }()
 <-entered
 close(release)
 if r := <-caught; r != "pure panic" || !finished.Load() { t.Fatal("pure panic did not join sibling") }
}
`
