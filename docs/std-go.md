# Go helpers for standard packages

Standard packages use these helpers in `unsafe go` bodies. Their names,
type parameters, signatures, and behavior are stable. The compiler provides
helpers when a body uses them; no Go import is needed except for Go library
types such as `context.Context`. Implementations live in
`internal/gen/helpers.go`. Runtime fields, methods, and variant names are
compiler internals; keep dependencies on them inside the helper implementations.

| Helper | Behavior |
| --- | --- |
| `_borkSome[T](value T) Option[T]` | Constructs the prelude's present option. Go infers `T` from the argument. |
| `_borkNone[T]() Option[T]` | Constructs the prelude's absent option. Supply `T` explicitly. |
| `_borkMapOf[K,V](keys []K, values []V)` | Builds a persistent map in insertion order. Duplicate keys keep the last value and first position. Panics when slice lengths differ. |
| `_borkMapGet[K,V](m, key K) (V, bool)` | Looks up a key; returns the zero value and false when absent. |
| `_borkMapPut[K,V](m, key K, value V)` | Returns a new map, preserving the original and its ordering policy. |
| `_borkMapLen[K,V](m) int` | Returns the number of entries, including zero for an empty map. |
| `_borkMapEach[K,V](m, visit func(K,V) bool)` | Visits entries in the map's order; stops when visit returns false. |
| `_borkScopeContext(s) context.Context` | Returns the scope's cancellation context. |
| `_borkScopeWith(ctx context.Context)` | Opens a scope cancelled with ctx; the caller must close it. |
| `_borkScopeClose(s)` | Cancels the scope, waits for tasks, runs finalizers; may panic for a task or finalizer failure. Repeated closure does nothing. |
| `_borkScopeAbort(s)` | Deferred cleanup: closes an unclosed scope even when its body panics; closure failures may themselves panic. |

Map arguments/results use the compiler's map representation, inferred from
bork signatures or other helpers. Map keys must obey bork's `Eq` requirement;
helpers do not validate facts or the immutability of Go values. Slices and
values passed into these helpers must never subsequently be mutated.

For example, a package can return an option without spelling a generated
variant name:

```bork
fn Lookup(name: String): Option[String] unsafe go {
  import "os"
  value, found := os.LookupEnv(name)
  if !found { return _borkNone[string]() }
  return _borkSome(value)
}
```

To bridge a Go operation's context into a scope:

```go
child := _borkScopeWith(ctx)
defer _borkScopeAbort(child)
// Run the handler with child.
_borkScopeClose(child)
```

Resources still use their generated record's `handle` and `owner` fields and
`s.Own(closeFn)`; record fields and primitive/list representations follow the
prelude's documented Go representation. The helper API isolates maps, options,
and scope context/lifecycle operations. `testdata/cases/go_helpers` compiles an
imported package using every helper and checks persistence and scope cleanup.

Derived `Decode` dictionaries also expose a general record schema. In an
`unsafe go` function with `T: Decode`, `_d_T_Decode` is the dictionary parameter;
`_borkDecodeFields(_d_T_Decode)` returns `(fields, supported)`. `supported` is
false for custom, primitive, and sealed-type instances. Fields follow declaration
order and have these stable members:

- `Name`: the bork field name.
- `Type`: the underlying bork type's display name (aliases are resolved).
- `Constraints`: the field's declared fact requirements, as text.
- `Kind`: `string`, `number`, `bool`, or `json`; Option uses its element's kind.
- `Optional`: whether the field is an Option and may be absent.
- `Decode`: a `func(Json) any` that returns the field's decoded value or a
  `DecodeError`, checking its facts just as the derived record decoder does.

Schemas are fresh immutable snapshots; callers must not change their slices.
They describe fields independently, and do not construct partial records in
bork. Call the regular `Decode` method to construct a complete proven record.
`bork/env` consumes this schema; it is also available to other std integrations.
`_borkOptionGet(option)` returns `(value, present)` without variant-name coupling.
