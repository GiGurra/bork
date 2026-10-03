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
// alive. Joining here still propagates failures in callback-spawned tasks.
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

func _borkFanInTimeout(s *_Scope, ms int64, work func(*_Scope) any) any {
 child := _scopeWith(s.ctx)
 child.name = "withTimeout"
 s.Defer(child.close)
 defer _borkFanInAbort(child)
 expired := make(chan struct{})
 duration := time.Duration(max(int64(0), min(ms, int64(9223372036854)))) * time.Millisecond
 timer := time.AfterFunc(duration, func() { child.cancel(errors.New("deadline exceeded")); close(expired) })
 defer timer.Stop()
 if ms <= 0 { child.cancel(errors.New("deadline exceeded")) }
 if child.ctx.Err() != nil { return Cancelled{reason: child._cancelReason()} }
 value := work(child)
 _borkFanInJoin(child)
 if !timer.Stop() { <-expired }
 if child.ctx.Err() != nil { return Cancelled{reason: child._cancelReason()} }
 return value
}

type _borkReceiveChoice struct {
 data reflect.Value
 closed <-chan struct{}
 ctx context.Context
}

// Selection receives from exactly one channel. Closing a channel leaves its
// buffer available before Closed is returned, matching receive.
func _borkFanInReceive(ctx context.Context, arms []*_borkReceiveChoice) (int, any) {
 if len(arms) > 21845 { panic("bork: select: at most 21845 receive arms") }
 cases := make([]reflect.SelectCase, len(arms)*3+1)
 for i, arm := range arms {
  cases[i*3] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: arm.data}
  cases[i*3+1] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(arm.closed)}
  cases[i*3+2] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(arm.ctx.Done())}
 }
 cases[len(cases)-1] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ctx.Done())}
 if ctx.Err() != nil { return -1, Cancelled{reason: context.Cause(ctx).Error()} }
 index, value, _ := reflect.Select(cases)
 if index == len(cases)-1 { return -1, Cancelled{reason: context.Cause(ctx).Error()} }
 arm := arms[index/3]
 switch index%3 {
 case 0: return index/3, value.Interface()
 case 1:
  if value, ok := arm.data.TryRecv(); ok { return index/3, value.Interface() }
  return index/3, Closed{}
 default: return index/3, Cancelled{reason: context.Cause(arm.ctx).Error()}
 }
}
`
