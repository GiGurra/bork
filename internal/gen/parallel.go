package gen

// parallelRuntime joins every worker before returning or propagating a panic.
const parallelRuntime = `package main

import (
 "errors"
 "runtime"
 "sync"
)

func _borkParallel(s *_Scope, n int, workers int64, work func(int) bool) bool {
 if workers <= 0 { workers = int64(runtime.GOMAXPROCS(0)) }
 workers = min(workers, int64(n))
 var mu sync.Mutex
 next := 0
 stopped := false
 var failure any
 run := func() {
  defer func() {
   if r := recover(); r != nil {
    mu.Lock()
    if failure == nil { failure = r }
    stopped = true
    mu.Unlock()
    if s != nil { s.cancel(errors.New("a parallel worker failed")) }
   }
  }()
  for {
   mu.Lock()
   if stopped || next == n || (s != nil && s.ctx.Err() != nil) {
    mu.Unlock()
    return
   }
   i := next
   next++
   mu.Unlock()
   if !work(i) {
    mu.Lock()
    stopped = true
    mu.Unlock()
    return
   }
  }
 }
 if s == nil && workers <= 1 {
  run()
 } else if s == nil {
  var wg sync.WaitGroup
  for i := int64(0); i < workers; i++ {
   wg.Add(1)
   go func() { defer wg.Done(); run() }()
  }
  wg.Wait()
 } else {
  tasks := make([]*_task, 0, workers)
  for i := int64(0); i < workers; i++ {
   tasks = append(tasks, s.Go(func() any { run(); return nil }))
  }
  for _, t := range tasks { t.Await() }
 }
 if failure != nil { panic(failure) }
 return !stopped && (s == nil || s.ctx.Err() == nil)
}
`
