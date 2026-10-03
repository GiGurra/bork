# Go helpers for standard packages

Standard packages use these helpers in `unsafe go` bodies. Their names,
type parameters, signatures, and behavior are stable. The compiler provides
helpers when a body uses them; no Go import is needed except for Go library
types such as `context.Context`. Implementations live in
`internal/gen/helpers.go`. Runtime fields, methods, and variant names are
compiler internals; keep dependencies on them inside the helper implementations.

| Helper | Behavior |
| --- | --- |
| `_borkIoFailure(err error) (kind, message string)` | Classifies not-found, permission, exists, or other I/O errors; unwraps os.PathError messages. Nil returns empty strings. |
| `_borkBytesFrom(data []byte) Bytes` | Copies Go bytes into immutable bork Bytes; nil becomes empty. |
| `_borkBytesData(data Bytes) []byte` | Copies Bytes into a Go slice the caller may mutate. |
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

`Bytes` is represented by a distinct named Go slice type, not `[]byte` or
`List[Byte]`. Use `_borkBytesFrom` for returned data and `_borkBytesData` for
Go APIs; both copy so later Go mutation cannot change an existing bork value.
Functions whose signatures or Go bodies use Bytes helpers include the runtime
automatically. Bytes has no writable fields or elements in bork.

A standard package that imports third-party Go libraries declares their pinned
module requirements in `internal/std/<package>/go-deps.mod` and corresponding
checksums in `go-deps.sum`. These use Go's native `go.mod`/`go.sum` syntax; the
names avoid creating a nested Go module inside the compiler's embedded sources.
Declare the complete `go mod tidy` requirement graph, including indirect
requirements. Generate both files from a temporary Go module importing exactly
the libraries the package uses, then copy its go.mod/go.sum under these names.
Only `module`, `go`, and canonical pinned `require` declarations are supported;
Other directives are rejected.

The driver combines declarations for loaded standard packages, including
transitive bork imports, and ships only their checksums into the generated
module. Imports without declarations add no modules. Shared module requirements
select the highest declared version, following Go's minimal version selection;
conflicting hashes are errors. Maintainers must refresh and test declarations
together when a shared dependency changes. The Go directive uses the highest
required version, starting at 1.22. Builds use `-mod=readonly`, so missing or
incomplete declarations fail instead of resolving new dependency versions.

Dependencies use Go's module cache; source is not vendored in the compiler.
A first build can download the pinned modules using the user's `GOPROXY` and
verify them against shipped checksums. For offline use, build a program importing
the required packages while online, or prepopulate/copy the Go module cache.
Then `GOPROXY=off bork build <program>` works from that cache. A cold cache fails
with Go's missing-module error and an explanation that offline builds require
cached modules. No checksum database access is needed when shipped hashes cover
the build. The pinned SQLite and Postgres manifests are tested both online and
from an offline warm cache. See the [Go module reference](https://go.dev/ref/mod).

Resource handles may implement `_borkRebind(*_Scope)`. The generated resource
forwards this optional method to its handle, and `attach(resource, s)` calls it
after retaining ownership in `s`. Cancellation should then follow the destination
scope, even after the opening scope ends. The latest attachment selects the
cancellation source; final cleanup still waits for every retained owner.

`_borkNewResourceHandle(value any, s)` returns a `*_borkResourceHandle` with
`Value`, `Context() context.Context`, and `Close()`. `Value` must be initialized
before publishing the resource and never mutated afterwards. Its stable context
carries cancellation from the current owner, supports rebinding, and carries no
scope context values or deadline metadata. Call `Close` in the resource's final
cleanup. Cancellation is terminal: attaching an already cancelled resource does
not revive it. This allows Go APIs such as `database/sql.BeginTx` to retain the
same context while attachment changes its cancellation source.

`_borkIoFailure` returns kind `notFound`, `permissionDenied`, `exists`, or `io`
for a non-nil Go error. It preserves `errors.Is` classification through wrapped
errors. Standard packages translate these strings into their public union
errors, supplying the path appropriate to the operation.

Opaque Go declarations (`type Request = go "*net/http.Request"`) use a generated
box. `_borkGo(value)` returns its statically typed Go value; `_borkOpaque[T](value)`
boxes a Go value as the declared bork type `T`. These helpers share the Go value.
An `unsafe go` body using them is responsible for keeping its promise of a non-nil
opaque value. Generated bindings check nils themselves. Go resources use the same
unboxing helper and keep their scope owner; generated bindings register `Close`
with the scope and ignore its error.
