package gen

const fanInRuntime = `package main

import (
 "context"
 "errors"
 "reflect"
 "time"
)

// Completion selection creates no waiter goroutines and leaves loser tasks
// with their original owners. Ready tasks are unordered when several are ready.
func _borkFanInFirst(ctx context.Context, tasks []*_task) int {
 if len(tasks) > 65535 { panic("bork: awaitFirst: at most 65535 tasks") }
 cases := make([]reflect.SelectCase, len(tasks)+1)
 for i, t := range tasks { cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(t.done)} }
 cases[len(tasks)] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
 index, _, _ := reflect.Select(cases)
 if index == len(tasks) {
  // A task panic cancels its scope. Report a completed panic before turning
  // that cancellation into a value, as await would.
  for i, t := range tasks {
   select { case <-t.done: if t.failure != nil { return i }; default: }
  }
  return -1
 }
 return index
}

// A panic in synchronous child work still cancels and joins its tasks.
func _borkFanInAbort(s *_Scope) {
 if r := recover(); r != nil {
  s.cancel(errors.New("a child operation failed"))
  func() { defer func() { recover() }(); _borkFanInJoin(s) }()
  panic(r)
 }
}

// A private child remains owned by its caller, so returned resources stay
// alive. Joining here still propagates failures in callback-forked tasks.
func _borkFanInJoin(s *_Scope) {
 // taskTimeout limits scope closure, not the join promised by this call.
 s.running.Wait()
 s.mu.Lock()
 tasks := append([]*_task(nil), s.tasks...)
 s.mu.Unlock()
 var failure any
 for _, t := range tasks {
  if t.failure != nil && !t.reported.Swap(true) && failure == nil { failure = t.failure }
 }
 if failure != nil { panic(failure) }
}

func _borkFanInTimeout(s *_Scope, nanos int64, work func(*_Scope) any) any {
 child := _scopeWith(s.ctx)
 child.name = "withTimeout"
 s.Defer(child.close)
 defer _borkFanInAbort(child)
 expired := make(chan struct{})
 duration := time.Duration(max(int64(0), nanos))
 timer := time.AfterFunc(duration, func() { child.cancel(errors.New("deadline exceeded")); close(expired) })
 defer timer.Stop()
 if nanos <= 0 { child.cancel(errors.New("deadline exceeded")) }
 if child.ctx.Err() != nil { return Cancelled{reason: child._cancelReason()} }
 value := work(child)
 _borkFanInJoin(child)
 if !timer.Stop() { <-expired }
 if child.ctx.Err() != nil { return Cancelled{reason: child._cancelReason()} }
 return value
}
`
