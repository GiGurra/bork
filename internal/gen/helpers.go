package gen

// The _bork helpers are the stable Go API for unsafe go bodies in standard
// packages. Their names, type parameters, arguments, results, and semantics
// are stable; the runtime representations they wrap are compiler internals.
// Maps are persistent; iteration follows the map's order and stops when the
// callback returns false. Scope contexts carry cancellation and effective deadlines; closing a scope
// waits for tasks and runs finalizers, while deferred abort also handles panic.
// Resource handles keep a stable cancellation context, which follows every
// owning scope (attachment adds one), and close on final release. Cancellation remains terminal.
// Option constructors return the prelude's Option, regardless of package names.
const okHelpers = `package main

func _borkOk() _Ok { return _Ok{} }
`

const scopeHelpers = `package main
import "context"
import "sync"
import "time"

// Stable scope helpers for unsafe go bodies.
func _borkScopeCleanupTimeout(s *_Scope) time.Duration { return s.finalizerTimeout }
func _borkScopeContext(s *_Scope) context.Context { return s.ctx }
func _borkScopeWith(ctx context.Context) *_Scope { return _scopeWith(ctx) }
func _borkScopeClose(s *_Scope) { s.close() }
func _borkScopeAbort(s *_Scope) { s.abort() }
// TryTask admits work atomically with scope closing; its waiter preserves task
// panic reporting. Rejected work is never called or registered.
func _borkTryScopeTask(s *_Scope, work func() any) (func() any, bool) {
 task:=s.tryGo(work)
 if task==nil { return nil,false }
 return task.Await,true
}

// A stable cancellation context that follows every scope owning the resource:
// it is cancelled once all of them are cancelled, with the cause of the last
// one (of the earliest owner, when several are found cancelled at once).
// Attachment adds a source; cancelled sources are dropped, so a handle
// attached to many short scopes keeps only the live ones.
// Value is immutable after construction. Close must run with final release.
type _borkResourceHandle struct {
 Value any
 mu sync.Mutex
 live []_borkHandleSource
 cause error
 ctx context.Context
 cancel context.CancelCauseFunc
}
type _borkHandleSource struct { ctx context.Context; stop func() bool }
func _borkNewResourceHandle(value any, s *_Scope) *_borkResourceHandle {
 ctx, cancel := context.WithCancelCause(context.Background())
 h := &_borkResourceHandle{Value: value, ctx: ctx, cancel: cancel}
 h._borkRebind(s)
 return h
}
// _borkRebind adds s as a source. A handle whose sources are all cancelled
// is cancelled for good: attaching it does not revive it.
func (h *_borkResourceHandle) _borkRebind(s *_Scope) {
 h.mu.Lock()
 defer h.mu.Unlock()
 h.pruneLocked()
 if h.ctx.Err() != nil { return }
 for _, source := range h.live {
  if source.ctx == s.ctx { return }
 }
 if s.ctx.Err() != nil {
  h.cause = context.Cause(s.ctx)
  if len(h.live) == 0 { h.cancel(h.cause) }
  return
 }
 source := s.ctx
 stop := context.AfterFunc(source, func() {
  h.mu.Lock()
  defer h.mu.Unlock()
  h.pruneLocked()
 })
 h.live = append(h.live, _borkHandleSource{source, stop})
}
// pruneLocked drops the sources that are cancelled (their AfterFunc may not
// have run yet), and cancels the handle once none is left.
func (h *_borkResourceHandle) pruneLocked() {
 kept := h.live[:0]
 var cause error
 for _, source := range h.live {
  if source.ctx.Err() == nil {
   kept = append(kept, source)
   continue
  }
  source.stop()
  if cause == nil { cause = context.Cause(source.ctx) }
 }
 clear(h.live[len(kept):])
 h.live = kept
 if cause != nil { h.cause = cause }
 if len(h.live) == 0 && h.cause != nil && h.ctx.Err() == nil { h.cancel(h.cause) }
}
// _borkUnbind removes s as a source (the resource was moved away from it).
// A handle left with no source is cancelled.
func (h *_borkResourceHandle) _borkUnbind(s *_Scope) {
 h.mu.Lock()
 defer h.mu.Unlock()
 for i, source := range h.live {
  if source.ctx == s.ctx {
   source.stop()
   h.live = append(h.live[:i], h.live[i+1:]...)
   break
  }
 }
 if len(h.live) == 0 && h.ctx.Err() == nil {
  if h.cause == nil { h.cause = context.Canceled }
  h.cancel(h.cause)
 }
}
func (h *_borkResourceHandle) Context() context.Context {
 h.mu.Lock()
 h.pruneLocked()
 h.mu.Unlock()
 return h.ctx
}
func (h *_borkResourceHandle) Close() {
 h.mu.Lock()
 defer h.mu.Unlock()
 for _, source := range h.live { source.stop() }
 h.live = nil
 h.cancel(context.Canceled)
}


`

const mapHelpers = `package main

// Stable helpers for unsafe go bodies. Keep generated representation details here.
func _borkMapOf[K, V any](keys []K, values []V) _Map[K, V] {
	if len(keys) != len(values) { panic("_borkMapOf: different key and value counts") }
	return _mapOf(keys, values)
}
func _borkMapGet[K, V any](m _Map[K, V], key K) (V, bool) { return m.get(key) }
func _borkMapPut[K, V any](m _Map[K, V], key K, value V) _Map[K, V] { return m.put(key, value) }
func _borkMapLen[K, V any](m _Map[K, V]) int { return m.len() }
func _borkMapEach[K, V any](m _Map[K, V], visit func(K, V) bool) { m.each(visit) }

`

const optionHelpers = `func _borkSome[T any](value T) Option[T] { return Option_Some[T]{value: value} }
func _borkNone[T any]() Option[T] { return Option_None[T]{} }
func _borkOptionGet[T any](option Option[T]) (T, bool) {
  if some, ok := option.(Option_Some[T]); ok { return some.value, true }
  var zero T
  return zero, false
}
`

// Derived record schemas expose declared field types and their proven decoders.
const decodeSchemaHelpers = `package main

type _borkDecodeField struct {
 Name string
 Type string
 Constraints []string
 Kind string
 Doc string
 HasDefault bool
 Default func() any
 Optional bool
 Decode func(Json) any
}
func _borkDecodeFields[T any](dict @Decode@[T]) ([]_borkDecodeField, bool) {
 if dict.fields == nil { return nil, false }
 return dict.fields(), true
}
`

// bytesRuntime keeps binary values distinct from lists in unions and printing.
const bytesRuntime = `package main

import "encoding/hex"

type _Bytes []byte

func (b _Bytes) String() string { return "Bytes(" + hex.EncodeToString(b) + ")" }

// Stable boundaries copy so Go callers cannot mutate a bork value.
func _borkBytesFrom(data []byte) _Bytes { return append(_Bytes{}, data...) }
func _borkBytesData(data _Bytes) []byte { return append([]byte{}, data...) }
`

// ioFailureHelpers classifies Go I/O errors without leaking Go error values.
const ioFailureHelpers = `package main
import "errors"
import "os"

func _borkIoFailure(err error) (kind, message string) {
 if err == nil { return "", "" }
 message = err.Error()
 var pathError *os.PathError
 if errors.As(err, &pathError) { message = pathError.Err.Error() }
 switch {
 case errors.Is(err, os.ErrNotExist): return "notFound", message
 case errors.Is(err, os.ErrPermission): return "permissionDenied", message
 case errors.Is(err, os.ErrExist): return "exists", message
 default: return "io", message
 }
}
`
