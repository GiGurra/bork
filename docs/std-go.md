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
| `_borkScopeCleanupTimeout(s) time.Duration` | Returns the scope cleanup timeout; zero means unbounded. |
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
- `Doc`: consecutive `//` comment lines directly above the field, without markers.
- `HasDefault`: whether the field declares a closed default value.
- `Default`: a `func() any` producing that value, or nil when absent.
- `Kind`: `string`, `number`, `bool`, or `json`; Option uses its element's kind.
- `Optional`: whether the field is an Option. `HasDefault` independently permits an absent field.
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

If a Go binding receives a context and returns a resource type, that resource
type cannot use `attach` anywhere in the program: Go may retain the supplied
context, which can cancel the resource. This includes converted scopes, contexts
in collections, and opaque context arguments. The compiler names the responsible binding. An
ownership-only scope that is not passed to Go has no such restriction. An
`unsafe go` wrapper using `_borkNewResourceHandle` remains the way to make a
resource whose cancellation follows its latest attachment.

`bork/rand` uses Go's `math/rand/v2` default source for functions declaring
`uses random`. `Seed(first, second = 0)` creates an opaque immutable PCG
generator. `IntFrom`, `FloatFrom`, `ShuffleFrom`, and `PickFrom` return a
`Draw[T]` with `value` and `next`; the corresponding generator methods do the
same. Reusing the input replays a draw; using `next` advances it. Seeded draws
are pure and require no effects. Do not rely on identical bounded draws across
32-bit and 64-bit platforms or future runtime versions.

Integer ranges include `lo` and exclude `hi`, require `hi > lo`, and support
ranges spanning the signed Int boundary. Floats lie in `[0, 1)`. Shuffle copies
its input. Pick returns None for an empty list, preserving the generator state.
This package is for simulations and sampling; use `bork/crypto` for secrets.

`bork/crypto` provides SHA256/SHA512 (Bytes and hex forms), HMAC256/HMAC512,
constant-time `Equal` for equal-length byte strings, and HMAC verification.
`RandomBytes` accepts 0 through 16 MiB; `Token(size = 32)` accepts 16 through
4096 bytes of entropy and returns unpadded URL-safe base64. Both use the system
cryptographic random source and return Error on entropy failure.

`HashPassword` uses Argon2id with a fresh 16-byte salt and 32-byte key, encoded
as an Argon2id v19 PHC string. Defaults are 19456 KiB (19 MiB), two iterations
and one lane, following the [OWASP password storage recommendation](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html).
`PasswordParams` is a checked record: memoryKiB 1024–262144, iterations 1–16,
parallelism 1–16. These bounds also limit work when verifying untrusted hashes.
Overrides below the defaults are supported for testing or an explicit app
policy; defaults are recommended for password storage. Argon2id has no bcrypt
72-byte password limit. The package pins golang.org/x/crypto v0.37.0.

`VerifyPassword` returns false for a wrong password and Error for a malformed,
unsupported or out-of-bounds hash. It also accepts legacy bcrypt 2/2a/2b/2y
hashes with cost at most 16; passwords longer than 72 bytes return false for
these legacy hashes. Argon2id salt/key lengths of 8–64 and 16–64 bytes are
accepted for imports. Padded base64, whitespace, unknown/duplicate parameters
and Argon2 versions other than v19 are rejected.

Call `NeedsRehash(hash, parameters = default)` after successful verification.
It returns true for bcrypt, invalid hashes, parameter differences, or salt/key
lengths other than 16/32. It compares against the exact application target,
including when a stored hash uses higher work factors; pass the application's
chosen parameters consistently to hashing and rehash checks.

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
