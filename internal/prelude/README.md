# Embedded prelude

Every Bork package sees these files as one prelude package. The compiler
embeds all `*.bork` files here and parses them in filename order. Positions
use `prelude/<filename>` in diagnostics and `bork describe`.

| File | Definitions |
| --- | --- |
| [prelude.bork](prelude.bork) | Common errors and the Go representation of Bork values |
| [options.bork](options.bork) | `Option` and its methods |
| [parallel.bork](parallel.bork) | Ordered, bounded pure and scoped parallel list operations |
| [lists.bork](lists.bork) | List constructors, `notEmpty`, and list methods |
| [strings.bork](strings.bork) | Number and Boolean parsing, and string methods |
| [runes.bork](runes.bork) | Unicode rune helpers |
| [maps.bork](maps.bork) | `Entry` and persistent map methods |
| [bytes.bork](bytes.bork) | Immutable bytes and UTF-8 conversions |
| [classes.bork](classes.bork) | `Eq`, `Show`, `Ord`, `GoStruct`, and primitive ordering instances |
| [json.bork](json.bork) | JSON values, parsing, rendering, `Decode`, `Encode`, and their instances |
| [fanin.bork](fanin.bork) | Ordered task fan-in, races, typed channel selection, and cooperative timeouts |
| [concurrency.bork](concurrency.bork) | Tasks, cancellation, atoms, channels, and sleep |
| [scopes.bork](scopes.bork) | Resource attachment, scope policies, and finalizers |
| [environment.bork](environment.bork) | `IoError`, arguments, exit, and standard error output |
| [testing.bork](testing.bork) | `Mock`, the handle of a mock in a test, and its `count` and `calls` |

Parallel list callbacks are pure by default: `xs.parMap(f, workers: 4)`.
For effects, use `xs.parMapIn(s, (child, x) => work(child, x))`; the supplied
scope enables cooperative cancellation, and the call returns `List[U] | Cancelled`.
The same pure/`In` pairs exist for `parFilter`, `parFlatMap` and `parForEach`.
Workers default to available CPUs, stop scheduling on cancellation, and all
finish before the call returns. Output order follows input; effect order does not.

`xs.parMapUntil[Int, MyError](f)` returns `List[Int] | MyError`, stopping on
the first observed failure. `parMapUntilIn[Int, MyError](s, f)` cancels its
internal callback scope on failure, joins workers, and also returns `Cancelled`.
That internal scope closes with `s`, preserving returned resource lifetimes.
Its cancellation reaches child-owned operations; parent-owned channels and
I/O handles continue following their owner's cancellation.
Success must be concrete and non-union, distinguishable from concrete failure members in Go;
use explicit success/failure type arguments where inference is ambiguous.
For tiny inputs or cheap callbacks, sequential methods usually cost less.

Task lists support `awaitAll()` (ordered), `awaitFirst(s)` (first completion),
and `awaitAllUntil[Success, Failure](s)` (first observed failure). Existing
tasks remain in their original scopes; `race(s, [child => work(child)])`
starts its own child tasks, cancels losers, and joins all before returning.
`withTimeout(s, milliseconds, child => work(child))` includes spawned tasks
in its cooperative deadline; use `withTimeoutDo` for an Ok callback.
A successful timeout stops its timer, keeping returned resources usable until
the parent closes. Race resources follow the cancelled race scope.

For heterogeneous channels, map each arm to one result type with
`ch.receiveCase(value => event(value))`, then call `arms.select(s)`. Only
one channel is consumed; channel cancellation/closure reaches its arm mapper,
while cancellation of the selection scope returns `Cancelled` directly.
Empty task/race/select lists return `Option.None`. Ready ties are unspecified.
See [the runnable example](../../examples/task_fanin/main.bork).

Internal helpers can opt into caller locations by calling
`compilerCallerLocation()`. This yields the hidden caller's `file:line:column`
String and forwards it to tracked helper calls, assertions, snapshots and dbg.
It is accepted only in prelude/standard-library function bodies. Ordinary
function types stay unchanged; a function value captures its reference site.
