# Go helpers for standard packages

Standard packages use these helpers in `unsafe go` bodies. Their names,
type parameters, signatures, and behavior are stable. The compiler provides
helpers when a body uses them; no Go import is needed except for Go library
types such as `context.Context`. Implementations live in
`internal/gen/helpers.go`. Runtime fields, methods, and variant names are
compiler internals; keep dependencies on them inside the helper implementations.

## I/O errors

| Helper | Behavior |
| --- | --- |
| `_borkIoFailure(err error) (kind, message string)` | Classifies not-found, permission, exists, or other I/O errors; unwraps os.PathError messages. Nil returns empty strings. |

`_borkIoFailure` returns kind `notFound`, `permissionDenied`, `exists`, or `io`
for a non-nil Go error. It preserves `errors.Is` classification through wrapped
errors. Standard packages translate these strings into their public union
errors, supplying the path appropriate to the operation.

## Bytes

| Helper | Behavior |
| --- | --- |
| `_borkBytesFrom(data []byte) Bytes` | Copies Go bytes into immutable bork Bytes; nil becomes empty. |
| `_borkBytesData(data Bytes) []byte` | Copies Bytes into a Go slice the caller may mutate. |

`Bytes` is represented by a distinct named Go slice type, not `[]byte` or
`List[Byte]`. Use `_borkBytesFrom` for returned data and `_borkBytesData` for
Go APIs; both copy so later Go mutation cannot change an existing bork value.
Functions whose signatures or Go bodies use Bytes helpers include the runtime
automatically. Bytes has no writable fields or elements in bork.

## Options

| Helper | Behavior |
| --- | --- |
| `_borkSome[T](value T) Option[T]` | Constructs the prelude's present option. Go infers `T` from the argument. |
| `_borkNone[T]() Option[T]` | Constructs the prelude's absent option. Supply `T` explicitly. |

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

## Maps

| Helper | Behavior |
| --- | --- |
| `_borkMapOf[K,V](keys []K, values []V)` | Builds a persistent map in insertion order. Duplicate keys keep the last value and first position. Panics when slice lengths differ. |
| `_borkMapGet[K,V](m, key K) (V, bool)` | Looks up a key; returns the zero value and false when absent. |
| `_borkMapPut[K,V](m, key K, value V)` | Returns a new map, preserving the original and its ordering policy. |
| `_borkMapLen[K,V](m) int` | Returns the number of entries, including zero for an empty map. |
| `_borkMapEach[K,V](m, visit func(K,V) bool)` | Visits entries in the map's order; stops when visit returns false. |

Map arguments/results use the compiler's map representation, inferred from
bork signatures or other helpers. Map keys must obey bork's `Eq` requirement;
helpers do not validate facts or the immutability of Go values. Slices and
values passed into these helpers must never subsequently be mutated.

## Scope lifecycle

| Helper | Behavior |
| --- | --- |
| `_borkScopeCleanupTimeout(s) time.Duration` | Returns the scope cleanup timeout; zero means unbounded. |
| `_borkScopeContext(s) context.Context` | Returns the scope's context, preserving values and exposing its effective deadline and typed cancellation cause. |
| `_borkScopeWith(ctx context.Context)` | Opens a scope cancelled with ctx; the caller must close it. |
| `_borkScopeClose(s)` | Cancels the scope, waits for tasks, runs finalizers; may panic for a task or finalizer failure. Repeated closure does nothing. |
| `_borkTryScopeTask(s, work func() any) (func() any, bool)` | Atomically admits work while s is open and uncancelled. Returns a waiter with normal task panic reporting; rejection returns nil/false and never invokes work. |
| `_borkScopeAbort(s)` | Deferred cleanup: closes an unclosed scope even when its body panics; closure failures may themselves panic. |

`cancelAfter` deadlines are visible through `_borkScopeContext(s).Deadline()`,
including deadlines added later to ancestors. They can only shorten; Go code
that caches a deadline still receives subsequent cancellation through Done.
Deadline expiry reports `context.DeadlineExceeded` through Err and Cause.

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

## Ambient values

| Helper | Behavior |
| --- | --- |
| `_borkLogged() []_borkAmbient` | The `logged` ambient values bound on this goroutine, in declaration order: `_borkAmbient{Name string; Value any}`, Value a `string`, `int64`, `float64` or `bool`. |
| `_borkPropagated() []_borkHeader` | The `propagated` ambient values bound on this goroutine, in declaration order, as sent: `_borkHeader{Name, Value string}`, the header name and the value's text. |
| `_borkBindPropagated(get func(name string) (string, bool)) (restore func())` | Binds an incoming request's propagated values on this goroutine, and the goroutines it starts, until `restore` puts back its labels. A boundary: every propagated value bound before is cleared first. A value `get` does not give stays unbound; one that is not of its declaration's type, or lacks its facts, stays unbound and is logged at warn level through `log/slog`, without its text. Never panics on input. |

A `with` publishes the values of `logged` and `propagated("header")`
declarations in the goroutine's profiler labels until its block ends; these
helpers read them (see "Logged and propagated values" in requirements.md). They
are for effectful boundary code (logging, and network clients and servers):
nothing a function computes may depend on them, since they follow the goroutine
rather than the source. `bork/log` adds `_borkLogged()` to every record. A
client sends each `_borkPropagated()` pair as a header:

```go
for _, h := range _borkPropagated() {
  req.Header.Set(h.Name, h.Value)
}
```

and a server binds a request's values around its handler:

```go
restore := _borkBindPropagated(func(name string) (string, bool) {
  v := req.Header.Values(name)
  if len(v) == 0 { return "", false }
  return v[0], true
})
defer restore()
```

In a program without marked declarations the helpers read and bind nothing.

## Decode schema

Derived `Decode` dictionaries also expose a general record schema. In an
`unsafe go` function with `T: Decode`, `_d_T_Decode` is the dictionary parameter;
`_borkDecodeFields(_d_T_Decode)` returns `(fields, supported)`. `supported` is
false for custom, primitive, and sealed-type instances. Fields follow declaration
order and have these stable members:

- `Name`: the bork field name.
- `Type`: the underlying bork type's display name (aliases are resolved).
- `Constraints`: the field's declared fact requirements, as text.
- `Doc`: consecutive `//` comment lines directly above the field, without markers.
- `HasDefault`: whether the field declares a closed default value.
- `Default`: a `func() any` producing that value, or nil when absent.
- `Kind`: `string`, `number`, `bool`, `json`, or `list:<element kind>` recursively; Option uses its element's kind. Generic element kinds come from their decoder dictionary.
- `Optional`: whether the field is an Option. `HasDefault` independently permits an absent field.
- `Decode`: a `func(Json) any` that returns the field's decoded value or a
  `DecodeError`, checking its facts just as the derived record decoder does.

Schemas are fresh immutable snapshots; callers must not change their slices.
They describe fields independently, and do not construct partial records in
bork. Call the regular `Decode` method to construct a complete proven record.
`bork/env` consumes this schema; it is also available to other std integrations.
`_borkOptionGet(option)` returns `(value, present)` without variant-name coupling.

## Standard Go dependencies

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
required version, starting at 1.23. Builds use `-mod=readonly`, so missing or
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

## Opaque Go values

Opaque Go declarations (`type Request = go "*net/http.Request"`) use a generated
box. `_borkGo(value)` returns its statically typed Go value; `_borkOpaque[T](value)`
boxes a Go value as the declared bork type `T`. These helpers share the Go value.
An `unsafe go` body using them is responsible for keeping its promise of a non-nil
opaque value. Generated bindings check nils themselves. Go resources use the same
unboxing helper and keep their scope owner; generated bindings register `Close`
with the scope and ignore its error.

Generated context-bound resource bindings pass stable cancellation contexts to Go. The opening
scope and all Scope context arguments form a union; `attach` on any result adds
its destination scope. All resources from that call share cancellation, but
retain separate close owners. Cancellation waits until every contributing scope
has ended or cancelled, or until the last returned resource releases ownership.
The final member cancels the group before invoking Close, allowing that Close to
wait for cancellation. Binding authors must ensure a member's Close does not
wait for the group's cancellation while another sibling is still owned. Shared
cancellation cannot end early without cancelling the retained sibling; such a
Close can block scope cleanup. `cleanupTimeout(ms)` is the escape hatch for a
misbehaving binding: it bounds each finalizer and lets remaining finalizers run. An explicit
opaque context remains a fixed external limit: attach extends ownership but
cannot outlive that context's cancellation or deadline.

Each argument's context values are preserved. The context reports the earliest
deadline among still-live scopes and explicit contexts; a Go library that copies
a deadline may retain the earlier limit. In these bindings, Go errors and failed
conversions close
all raw results once, including resources conversion has not visited. Duplicate
comparable handles from the same call share their close owner. A discarded raw
value returned with `ok == false` also closes. Context fields inside a mirror
record are supported; contexts hidden inside an opaque struct require a mirror
or an unsafe wrapper. The `_borkNewResourceHandle` helper for handwritten unsafe
wrappers retains its own attachment behavior described above.

## Mirror conversion helpers

Mirror records (`type Url = go "net/url.URL" { host: String }`) keep their own
bork representation. `_borkToGo(v)` copies the selected fields into the named Go
struct and leaves omitted fields at zero. `_borkFromGo[T](g)` returns `(T,
[]GoValueError)`, copying from that Go struct and collecting conversion and fact
failures with paths beginning at `result`. Use the converted value only when the
error list is empty. Nested records and collections are copied; opaque fields
share their Go value. Cyclic pointer paths produce errors, while shared acyclic
values convert independently.

The helper methods exist only for supported directions: an array field prevents
conversion to Go, for example. A mirror cannot contain resource fields, because
this helper has no scope to own them. An `unsafe go` body supplies the mirror's
exact Go struct type to `_borkFromGo`; checked bindings also support pointers,
including `Option` for nil.

## Generated Go structs

`derive (GoStruct)` creates a separate Go struct with exported fields, leaving
bork's record representation intact. Fields map recursively using the binding
rules. Nested ordinary records need `GoStruct`; nested mirrors use their Go type.
Fields with unresolved type parameters or resources are rejected, while phantom
generic parameters are allowed. A mirror can derive `GoStruct` when both
conversion directions work; it keeps the mirrored struct and its tags.

A field on a generated struct can add ordered tags:

```bork
type Options = {
  // Port to listen on.
  port: Int = 8080 go { json: "port", short: "p" }
} derive (GoStruct)
```

A generic unsafe Go body with `[T: GoStruct]` receives `_d_T_GoStruct`:

- `New()` returns `any`, holding a pointer to a fresh Go struct with declared
  defaults filled in. Other fields keep Go's zero values; they must be checked
  through `FromGo` before publishing a bork value.
- `ToGo(value T)` returns `any`, holding the Go struct value. Collections and
  nested records are copied; opaque fields share the Go value.
- `FromGo(value any)` accepts that Go struct or a pointer to it and returns
  `(T, []GoValueError)`. It collects conversion and fact errors. A nil pointer or
  wrong Go type returns an error. Use the value only when the error list is empty.
- `Fields()` returns `[]_borkGoStructField`. Each entry exposes the Decode schema
  metadata (`Name`, `Type`, `Constraints`, `Kind`, `Optional`, `Doc`, `HasDefault`,
  `Default`, `Decode`), plus `GoName` and ordered `Tags` (`Name`, `Value`).
  `Decode` is nil when no decoder is available in the declaring package.

`GoStruct` does not require deriving `Decode`. Its instances are created by
`derive` and imported with `use`, like other derived instances. `_borkToGo` and
`_borkFromGo[T]` also work for these generated records as they do for mirrors.

## User Go dependencies

A user module can declare third-party Go dependencies in `go-deps.mod` and
`go-deps.sum` beside its `bork.mod`. Every package in that bork module uses them
for binding checks, compile-time predicate evaluation, and generated builds.
The package still needs its `unsafe "<package path>"` entry for unsafe Go bodies
and bindings.

`go-deps.mod` uses native Go syntax, supporting `module`, `go`, and canonical
pinned `require` declarations. Its module name is descriptive; generated
programs use `borkprogram`. Include the indirect requirements needed by the Go
module graph, as in a complete Go module manifest. `go-deps.sum` must contain
module and `go.mod` hashes for every selected requirement. For example:

```go
module example.com/app

go 1.26

require github.com/google/uuid v1.6.0
```

The compiler merges these with imported standard packages' manifests and
checksums. Go's minimum version selection chooses the higher version of each
module path. User requirements can therefore raise a standard package's pinned
version; compatibility with that newer version is the program's responsibility.
Different hashes for the same module/version are rejected. `replace`, `exclude`,
`retract`, `toolchain`, and other directives are not supported in these manifests.

Go resolves transitive requirements using its normal module graph. Builds and
binding checks use `-mod=readonly`; the compiler does not edit the source
manifests. Populate the manifests and checksums with `bork deps` and keep them under
version control. Warm Go module caches support `GOPROXY=off`; an empty
offline cache fails with the missing module reported. Dependencies are not
vendored.

The helper operates on the nearest `bork.mod` from the current directory (or
`--path <directory>`), without needing valid bork source:

```sh
bork deps init                         # create empty manifests; refuse overwrite
bork deps get github.com/google/uuid@v1.6.0  # add or pin a dependency
bork deps get github.com/google/uuid@latest  # resolve and pin an update
bork deps get github.com/google/uuid@none    # remove a dependency
bork deps download                     # warm the cache and fill missing checksums
```

`get` accepts one or more Go package or module queries and creates the manifests
if absent. It uses `go get`, retaining canonical resolved versions and indirect
requirements, then `go mod download` to pin every requirement's checksums.
`download` requires an existing manifest and can recreate a missing checksum
file. These operations run in a temporary Go module; resolution or validation
errors leave the project manifests untouched. An automatically added Go
`toolchain` suggestion is omitted; the `go` directive records the minimum Go
version. Other unsupported directives remain errors. The helper uses Go's normal
proxy, checksum database, credentials, and module cache, while ignoring `GOWORK`
and `GOFLAGS` so unrelated Go projects cannot redirect its work. It manages user
requirements; imported standard packages continue to merge into the shared
check/build graph when the program is compiled. `tidy` is intentionally absent:
bork sources are not Go imports, so Go's tidy would remove needed dependencies.

## Codec privacy

Structurally deriving through foreign private variants is rejected, including
variants reachable through records, containers, and concrete generic
specializations. At a field boundary, derivation may delegate to an existing
codec provided by the field type's owning package, as with `math.Decimal`.
Re-deriving an alias of that private sealed type still cannot inspect its variants.

Checked iterator bindings map `iter.Seq[G]` to `Seq[T]` when element conversion is infallible, and convert elements lazily in both directions. Direct Seq annotations must declare latent effects explicitly (`uses nothing` for a pure iterator). A nil returned iterator is empty. Iterators must yield synchronously and stop when the callback returns false; retaining the callback or yielding again violates the unsafe contract. Compiler-owned `_Seq[T]` has a `run iter.Seq[T]` field for standard-library implementations.

## Go import names

Unsafe Go import lines accept explicit names, such as
`import mathrand "math/rand/v2"`. Aliases belong to the Bork file, and other
unsafe bodies in that file may use them. Different files may use the same alias
for different packages. Go locals and Bork parameters can shadow an alias.
Unaliased imports retain the shared namespace of generated Go; conflicting
package names are diagnosed before generation. Declared Go package names are
resolved from package metadata, including names that differ from path basenames.
Give colliding packages distinct aliases, for example `mathrand` and `cryptorand`.
Dot and blank imports are not supported, and a body cannot import the same path
twice. Imports precede other Go statements. Effect checks follow the underlying
package through its alias.
