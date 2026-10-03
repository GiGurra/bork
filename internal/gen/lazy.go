package gen

// A cell publishes a value/panic once, independently of reader synchronization.
// Recursive reads fail loudly; concurrent readers wait for the same completion.
const lazyRuntime = `package main
import (
 "runtime"
 "strconv"
 "sync"
)
var _lazyCompileTime bool
type _lazyCell[T any] struct {
 mu sync.Mutex
 state uint8 // empty, evaluating, value, panic
 owner uint64
 done chan struct{}
 init func() T
 value T
 failure any
 constant bool // fresh pure recipe in the compiler's known-input candidate
}
func _lazyNew[T any](init func() T) *_lazyCell[T] {
 return &_lazyCell[T]{init:init, done:make(chan struct{})}
}
func _lazyResolved[T any](value T) *_lazyCell[T] {
 return &_lazyCell[T]{state:2, value:value, constant:_lazyCompileTime}
}
func _lazyConstNew[T any](init func() T) *_lazyCell[T] {
 cell := _lazyNew(init)
 cell.constant = true
 return cell
}
// Go has no public goroutine identity API. Check the diagnostic header only
// while starting/waiting on work; cached reads do not inspect goroutine state.
func _lazyGoroutine() uint64 {
 var buf [64]byte
 n := runtime.Stack(buf[:], false)
 const prefix = "goroutine "
 if n < len(prefix) || string(buf[:len(prefix)]) != prefix {
  panic("bork: cannot identify a lazy initializer's goroutine")
 }
 end := len(prefix)
 for end < n && buf[end] >= '0' && buf[end] <= '9' { end++ }
 id, err := strconv.ParseUint(string(buf[len(prefix):end]), 10, 64)
 if err != nil || id == 0 || end >= n || buf[end] != ' ' {
  panic("bork: cannot identify a lazy initializer's goroutine")
 }
 return id
}
func (c *_lazyCell[T]) get() T {
 if _lazyCompileTime && !c.constant { panic("bork: compile-time evaluation cannot force a runtime lazy cell") }
 for {
  c.mu.Lock()
  switch c.state {
  case 0:
   c.mu.Unlock()
   id := _lazyGoroutine()
   c.mu.Lock()
   if c.state != 0 { c.mu.Unlock(); continue }
   c.state, c.owner = 1, id
   c.mu.Unlock()
   c.run()
  case 1:
   owner, done := c.owner, c.done
   c.mu.Unlock()
   if owner == _lazyGoroutine() { panic("bork: recursive lazy initializer") }
   <-done
  case 2:
   value := c.value
   c.mu.Unlock()
   return value
  case 3:
   failure := c.failure
   c.mu.Unlock()
   panic(failure)
  default:
   c.mu.Unlock()
   panic("bork: invalid lazy cell state")
  }
 }
}
func (c *_lazyCell[T]) run() {
 completed := false
 var value T
 defer func() {
  failure := recover()
  c.mu.Lock()
  if completed {
   c.value, c.state = value, 2
  } else {
   if failure == nil { failure = "bork: lazy initializer exited without a value" }
   c.failure, c.state = failure, 3
  }
  c.init, c.owner = nil, 0
  close(c.done)
  c.mu.Unlock()
 }()
 value = c.init()
 completed = true
}
`

const asyncRuntime = `package main
func _asyncNew[T any](s *_Scope, work func() T) *_lazyCell[T] {
 if _lazyCompileTime { panic("bork: compile-time evaluation cannot schedule an async initializer") }
 task := s.Go(func() any { return work() })
 return _lazyNew(func() T { return task.Await().(T) })
}
`
