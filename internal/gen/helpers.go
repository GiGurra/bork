package gen

// The _bork helpers are the stable Go API for unsafe go bodies in standard
// packages. Their names, type parameters, arguments, results, and semantics
// are stable; the runtime representations they wrap are compiler internals.
// Maps are persistent; iteration follows the map's order and stops when the
// callback returns false. Scope contexts carry cancellation; closing a scope
// waits for tasks and runs finalizers, while deferred abort also handles panic.
// Resource handles keep a stable cancellation context, rebind its source on
// attachment, and close on final release. Cancellation remains terminal.
// Option constructors return the prelude's Option, regardless of package names.
const scopeHelpers = `package main
import "context"
import "sync"

// Stable scope helpers for unsafe go bodies.
func _borkScopeContext(s *_Scope) context.Context { return s.ctx }
func _borkScopeWith(ctx context.Context) *_Scope { return _scopeWith(ctx) }
func _borkScopeClose(s *_Scope) { s.close() }
func _borkScopeAbort(s *_Scope) { s.abort() }

// A stable cancellation context whose source follows resource attachment.
// Value is immutable after construction. Close must run with final release.
type _borkResourceHandle struct {
 Value any
 mu sync.Mutex
 source context.Context
 ctx context.Context
 cancel context.CancelCauseFunc
 generation uint64
}
func _borkNewResourceHandle(value any, s *_Scope) *_borkResourceHandle {
 ctx, cancel := context.WithCancelCause(context.Background())
 h := &_borkResourceHandle{Value: value, ctx: ctx, cancel: cancel}
 h._borkRebind(s)
 return h
}
func (h *_borkResourceHandle) _borkRebind(s *_Scope) {
 h.mu.Lock()
 if h.source != nil && h.source.Err() != nil { h.cancel(context.Cause(h.source)) }
 h.source = s.ctx
 h.generation++
 generation := h.generation
 if s.ctx.Err() != nil { h.cancel(context.Cause(s.ctx)) }
 h.mu.Unlock()
 go func() {
  select {
  case <-s.ctx.Done():
   h.mu.Lock()
   if h.generation == generation { h.cancel(context.Cause(s.ctx)) }
   h.mu.Unlock()
  case <-h.ctx.Done():
  }
 }()
}
func (h *_borkResourceHandle) Context() context.Context {
 h.mu.Lock()
 if h.source.Err() != nil { h.cancel(context.Cause(h.source)) }
 h.mu.Unlock()
 return h.ctx
}
func (h *_borkResourceHandle) Close() { h.cancel(context.Canceled) }

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
 Optional bool
 Decode func(Json) any
}
func _borkDecodeFields[T any](dict Decode[T]) ([]_borkDecodeField, bool) {
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
