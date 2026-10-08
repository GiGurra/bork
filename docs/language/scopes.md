# Scopes and tasks

A scope is a block that owns things with a lifetime: open files, connections, and running tasks. When the block ends, it cancels its tasks, waits for them to stop, and closes its resources.

## Scopes own resources

```bork
import "bork/fs"

fn firstLine(path: String) uses io: String | fs.Error {
  scope s {
    file = fs.Open(path, s)?
    text = fs.ReadAllText(file)?
    text.lines().head().getOr("")
  }
}

fn main() {
  println(firstLine("notes.txt"))
}
```

`scope s { ... }` opens a scope named `s`. Functions that create a resource take the scope as an argument, and the resource then belongs to it. When the block ends, the file is closed. That happens on every way out: reaching the end, returning early with `?`, or a panic.

The value of the scope block is the value of its last expression, so results flow out normally. A scope body is a nested block, so it cannot rebind a name from outside the scope; see [names and values](basics.md#names-and-values).

## A resource cannot outlive its scope

The compiler tracks which scope each resource belongs to. Using one after its scope has ended does not compile:

```bork fails
import "bork/fs"

fn broken(path: String) uses io: String | fs.Error {
  file = scope s { fs.Open(path, s)? }
  fs.ReadAllText(file)
}
```

```text
file may be released: it belongs to scope s, which ended on line 4
```

Files are not closed by hand. A function that needs an open resource takes it as a parameter, and its caller's scope keeps it open for the duration of the call.

A scope is itself a value, of type `Scope`, so a function can take one as a parameter and open resources or start tasks in it. A function taking a `Scope` may run work in it until the scope ends, even after the function returns. Several examples below do this with `s: Scope`.

Tuples preserve these lifetimes too: wrapping a resource in `(file, label)` or
destructuring that tuple cannot let the resource outlive its scope. A tuple cannot
hold an OwnedScope, because owning scopes cannot be copied into containers.

## Process signals

SIGINT (Ctrl+C) and SIGTERM cancel root scopes by default. Nested scopes and
tasks inherit that cancellation, and scope cleanup still runs. Tasks observe
cancellation at a checkpoint or a cancellable wait; a signal does not interrupt
arbitrary computation.

[bork/signal](../std/signal.md) explains exit codes, repeated signals, Windows
behavior, and how to choose cancellation signals, set a grace duration or
subscribe to reload events. Registrations affect the whole process, and their
scopes own how long they remain active.

## Tasks

`fork` starts a task that runs at the same time as the code that started it. A task also belongs to a scope. Await the work you need before leaving its scope: exit requests cancellation before waiting for the tasks.

```bork
fn fib(n: Int): Int {
  if n < 2 { n } else { fib(n - 1) + fib(n - 2) }
}

fn main() {
  results = scope s {
    a = fork(s, () => fib(27))
    b = fork(s, () => fib(28))
    [await(a), await(b)]
  }
  println(results)
}
```

- `fork(s, () => ...)` gives a `Task[T]`, and `await(task)` waits for its result.
- Work that gives no value gives a `Task[Ok]`. It can be dropped like an `Ok`, so `fork(s, () => { ... })` on its own line starts fire-and-forget work. When the scope exits, it cancels and joins the task, and reports an unawaited panic. Work that observes cancellation may stop before completing. Other tasks must be used: dropping a `Task[Int]` is an error, as dropping an `Int` is.

Use a `fork` prefix only when a caller-supplied closure runs as a task that may outlive the call. Taking a `Scope` alone does not require a `fork` prefix; internal background tasks are bounded by that scope.

## What happens at scope exit

The order is **cancel → join → cleanup**. Leaving the block cancels the scope,
then waits for its tasks, then runs resource cleanup in reverse acquisition
order. This also happens on early return, `?`, or panic. Every cleanup runs even
if another fails; unawaited task panics and cleanup failures are reported after
cleanup finishes.

Awaiting inside the block lets a delayed task finish before cancellation:

```bork
fn delayed(s: Scope) uses clock + state: String | Cancelled {
  delay(s, 10.millis())?
  "finished"
}

fn main() {
  scope s {
    onClose(s, () => println("cleanup"))
    task = fork(s, () => delayed(s))
    println(await(task))
  }
}
```

```text
finished
cleanup
```

Leaving without awaiting cancels that same wait. This example uses a long delay
so that normal scope exit happens first:

```bork
fn main() {
  scope s {
    onClose(s, () => println("cleanup"))
    fork(s, () => {
      println(delay(s, 1.minutes()))
    })
  }
}
```

```text
Cancelled { reason: "the scope ended" }
cleanup
```

Cancellation is cooperative. Code that never checks it can continue running,
and the scope normally waits for that code too. The exception is
`taskTimeout(duration)`: when its join deadline expires, the scope leaves remaining
tasks running and proceeds with cleanup. Those tasks may encounter closed
resources; see [cancellation and timeouts](#cancellation-and-timeouts).

Lists of tasks have helpers: `tasks.awaitAll()` gives every result in order, and `tasks.awaitFirst(s)` the first to finish. `race(s, [...])` runs several functions and keeps the first result, cancelling the others. See the [task_fanin example](../../examples/task_fanin/main.bork).

## Cancellation and timeouts

A scope can be cancelled. Its tasks see this at their next cancellation point, such as `delay` or `checkpoint`, which then returns `Cancelled`.

```bork
fn slowAnswer(s: Scope) uses clock + state: Int | Cancelled {
  delay(s, 2.seconds())?
  42
}

fn main() {
  scope s {
    println(withTimeout(s, 50.millis(), child => slowAnswer(child)))
  }
}
```

```text
Cancelled { reason: "deadline exceeded" }
```

`withTimeout(s, duration, work)` runs `work` in a child scope and cancels it after the time limit. `cancel(s)` cancels a scope directly, and `cancelAfter(s, duration)` after a delay. Cancellation is a value like any other failure: it shows up in result types as `Cancelled`, and `?` passes it along.

A scope can be given limits when it is opened:

```bork
fn main() {
  scope s with taskTimeout(100.millis()), cleanupTimeout(500.millis()) {
    println(checkpoint(s))
  }
}
```

- `taskTimeout(duration)` limits how long the scope waits for its tasks to stop once it has been cancelled. After that it stops waiting, and a task that is still running is left behind. This is the one case where a task outlives its scope.
- `cleanupTimeout(duration)` limits how long the scope waits for each resource to close.

## Channels

A channel carries values between tasks.

```bork
fn main() {
  scope s {
    numbers = channel[Int](s, 2)
    fork(s, () => {
      _ = numbers.send(s, 1)
      _ = numbers.send(s, 2)
      numbers.close()
    })
    for n in numbers.values(s) {
      println(n)
    }
    println(numbers.receive(s))
  }
}
```

```text
1
2
Closed {}
```

`channel[T](s, capacity)` makes a channel owned by the scope. `send` and `receive` wait in the scope they are given, and report `Closed` or `Cancelled` as values. `for x in ch.values(s)` receives until the channel is closed, and `select` waits for whichever of several channel operations can happen first. [Channels](channels.md) covers them in full: capacities, closing, producers, `select`, timeouts, and patterns such as pipelines and fan-out.

## Shared state

All values are immutable, so tasks cannot interfere with each other's data. When tasks do need to share something that changes, they use an atom: a cell that holds a value and replaces it atomically.

```bork
fn main() {
  counter = atom(0)
  scope s {
    tasks = range(0, 100).map(i => fork(s, () => {
      update(counter, n => n + 1)
    }))
    _ = tasks.awaitAll()
  }
  println(current(counter))
}
```

This always prints `100`. `update(a, f)` applies a pure function to the current value and stores the result. If another task got in between, it tries again, so no update is lost. `current(a)` reads the value.

Reading or updating an atom is the `state` [effect](effects.md), so functions that touch shared state say so in their signature.

## Lazy and async bindings

Two kinds of binding delay or move the work of computing a value, without changing its type.

```bork
fn expensive(n: Int): Int {
  range(0, n).fold(0, (sum, i) => sum + i)
}

fn main() {
  lazy total = expensive(1_000_000)
  scope s {
    async(s) other = expensive(2_000_000)
    println("started")
    println(total + other)
  }
}
```

- `lazy name = ...` computes the value the first time it is read, and remembers it. If it is never read, the work is never done.
- `async(s) name = ...` starts computing right away as a task of scope `s`. Reading the name waits for the result.

In both cases `total` and `other` are plain `Int` values to the rest of the code.

## Longer-lived resources

Several tools cover lifetimes that do not fit a single block:

- `attach(resource, s)` keeps a resource open until another scope `s` ends as well. A function can open something in its own scope and hand it to its caller's.
- `move(resource, s)` hands a resource over to scope `s`: the scope it was opened in lets go of it, and `s` closes it. The compiler then rejects any further use of the original, or of anything holding it. Only a resource acquired right there can be moved, and not while a task or a channel may still use it.
- A `Handoff` passes a resource to whichever task receives it, which may then move it on. See [handing resources over](channels.md#handing-resources-over).
- `openScope(parent)` opens a child scope with an explicit end, `closeScope(owner)`. The compiler checks that it is closed exactly once on every path. This is for lifetimes that overlap without nesting, such as replacing a connection with a new one before releasing the old one.

```bork
import "bork/fs"

// Opens the file in a short scope, checks it, and hands it to the caller's.
fn openChecked(path: String, app: Scope) uses io: fs.File | fs.Error {
  scope check {
    file = fs.Open(path, check)?
    _ = fs.ReadAllText(file)?
    move(file, app)
  }
}

fn main() {
  scope app {
    match openChecked("notes.txt", app) {
      file: fs.File => println(fs.Path(file))
      failure: fs.Error => println(failure)
    }
  }
}
```

### Sharing with `attach`

`attach` keeps both owners. Here a helper opens a temporary file in a short
scope and attaches it to the application's scope before returning it:

```bork
import "bork/fs"

fn temporary(app: Scope) uses io: fs.File | fs.Error {
  scope check {
    file = fs.TempFile(check)?
    attach(file, app)
  }
}

fn main() {
  scope app {
    match temporary(app) {
      file: fs.File => println(fs.Path(file) != "")
      failure: fs.Error => println(failure)
    }
  }
}
```

The short scope no longer keeps the file open after it ends, but `app` does.
`attach` must run while the resource is still usable. It cannot revive a
resource after its last known scope has ended.

### Explicit child scopes

`OwnedScope` is the right to end a child scope opened with `openScope`.
`owner.scope` borrows that child's ordinary `Scope`, for opening resources or
starting tasks. The owner is passed on or closed exactly once on every path;
it cannot be copied into records, tuples or lists. Returning it transfers that
responsibility to the caller. Early exits close owners that remain local.

```bork
fn finish(owner: OwnedScope, work: () => Ok in owner) uses state {
  work()
  closeScope(owner)
}

fn main() {
  scope app {
    old = openScope(app)
    next = openScope(app)
    onClose(old.scope, () => println("old closed"))
    onClose(next.scope, () => println("next closed"))
    finish(old, () => println("using old"))
    println("next is still open")
    closeScope(next)
  }
}
```

Both children are open during the handover; closing `old` leaves `next` open.
The parameter `work: () => Ok in owner` tells the compiler that the callback
stays valid while the child is open. The helper runs it before closing that
child.

## Functions that keep resources

An ordinary synchronous helper can take `file: fs.File` without a lifetime
annotation. It runs while its caller keeps the file alive. When the helper
stores a value, starts longer-lived work, or takes an owned scope it may close,
its signature must describe the needed lifetime.

### A parameter that must outlive another

`value: T in other` says that `value` stays usable at least as long as `other`.
For a task that captures an atom parameter, declare `a: Atom[Int] in s` when
starting work with `fork(s, ...)`. A callback parameter similarly uses
`work: () => Int in s`. `attach` is for resource values; these parameters need
a lifetime promise in the signature.

For a channel, this lets a helper put a resource into it:

```bork
import "bork/fs"

fn put(files: Channel[fs.File], file: fs.File in files, wait: Scope) uses state: Ok | Closed | Cancelled {
  files.send(wait, file)
}

fn main() {
  scope app {
    files = channel[fs.File](app, 1)
    match fs.TempFile(app) {
      file: fs.File => println(put(files, file, app))
      failure: fs.Error => println(failure)
    }
  }
}
```

The direction matters: `file in files` lets the channel keep the file. A
shorter-lived file must be attached to the channel's scope before sending it.
This is a checked lifetime relationship, not a request to attach automatically.

### Returning a value that retains a resource

A record, list, tuple or closure holding a resource carries its lifetime too.
There is no extra keyword for retaining it. Give the helper its caller's scope,
and return the resource or a value that contains it:

```bork
import "bork/fs"

type LabeledFile = { file: fs.File, label: String }

fn labeled(app: Scope) uses io: LabeledFile | fs.Error {
  file = fs.TempFile(app)?
  LabeledFile { file: file, label: "scratch" }
}

fn main() {
  scope app {
    match labeled(app) {
      result: LabeledFile => println(result.label, fs.Path(result.file) != "")
      failure: fs.Error => println(failure)
    }
  }
}
```

The whole `LabeledFile` is usable only while `app` stays open. Returning a
wrapper does not make a file opened in the helper's own scope safe to escape;
use the caller's scope, `attach` or `move` instead.

### Declaring a resource type

Library authors declare opaque resource handles with `resource`:

```bork
type Connection = resource

fn keep(connection: Connection): Connection {
  connection
}
```

The declaration marks values as scoped resources. It does not open anything
or provide a bork constructor. An [`unsafe go` implementation](go-interop.md)
creates the handle and registers cleanup with its ownership scope; a
`resource go` declaration can wrap a Go type with a `Close` method. Most
programs use the resource types already supplied by standard packages.

Most programs need only `scope` blocks.

## More

- [wc](../../examples/wc/main.bork) reads files concurrently, one task and one scope per file.
- [handoff](../../examples/handoff/main.bork) dials connections in the application's scope and moves each into a short session scope, which closes it.
- [bork/tasks](../std/tasks.md) has bounded task pools for limiting how much work runs at once.
- [bork/http](../std/http.md), [bork/sql](../std/sql.md), [bork/fs](../std/fs.md), and [bork/net](../std/net.md) all hand out resources that belong to scopes.

---

Previous: [Effects](effects.md) · Next: [Channels](channels.md) · [All pages](../README.md#the-language)
