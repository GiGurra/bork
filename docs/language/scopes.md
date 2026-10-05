# Scopes and tasks

A scope is a block that owns things with a lifetime: open files, connections, and running tasks. When the block ends, everything it owns is finished or closed.

Local names may be rebound only within the same block. A scope body is a nested
block, so it cannot rebind a name from outside that scope. Each local value must
be used; explicitly discard unwanted values with `_`.

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

The value of the scope block is the value of its last expression, so results flow out normally.

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

A scope is itself a value, of type `Scope`, so a function can take one as a parameter and open resources or start tasks in it. Several examples below do this with `s: Scope`.

Tuples preserve these lifetimes too: wrapping a resource in `(file, label)` or
destructuring that tuple cannot let the resource outlive its scope. A tuple cannot
hold an OwnedScope, because owning scopes cannot be copied into containers.

## Process signals

SIGINT (Ctrl+C) and SIGTERM cancel every root scope by default. Nested scopes
and tasks inherit the cancellation, whose reason names the signal. After cleanup,
a normal return exits with `128 + signal number`: 130 for SIGINT, 143 for SIGTERM.
An explicit `process.Exit(code)` keeps that code. Copies within 500 ms count as
one request; a subsequent cancelling signal terminates immediately. Three
Ctrl+C presses within 5 seconds always exit with 130, even when the program
subscribes to or ignores `Interrupt`. There is no
default grace deadline. Other signals, including SIGHUP, keep their Go/OS
behavior. Programs that never open a scope install no bork signal handler.

[bork/signal](../std/signal.md) chooses cancellation signals, sets a grace
duration, subscribes to events such as SIGHUP reloads, or ignores signals.
**A registration's scope owns its lifetime, not an isolated disposition.**
Its signal behavior affects the whole process until its last owner closes.
Subscriptions prevent their signals from cancelling any root scope.

On Windows, Ctrl+C and Ctrl+Break both act as SIGINT. Console close, logoff
and shutdown give a SIGTERM cleanup opportunity, subject to Windows' own
termination deadline.

## Tasks

`spawn` starts a task that runs at the same time as the code that started it. A task also belongs to a scope, and the scope does not end until its tasks are done.

```bork
fn fib(n: Int): Int {
  if (n < 2) { n } else { fib(n - 1) + fib(n - 2) }
}

fn main() {
  results = scope s {
    a = spawn(s, () => fib(27))
    b = spawn(s, () => fib(28))
    [await(a), await(b)]
  }
  println(results)
}
```

- `spawn(s, () => ...)` gives a `Task[T]`, and `await(task)` waits for its result.
- `launch(s, () => ...)` starts a task that has no result to wait for.

Every task belongs to a scope. When the scope block is finished, all of its tasks are finished.

Lists of tasks have helpers: `tasks.awaitAll()` gives every result in order, and `tasks.awaitFirst(s)` the first to finish. `race(s, [...])` runs several functions and keeps the first result, cancelling the others. See the [task_fanin example](../../examples/task_fanin/main.bork).

## Cancellation and timeouts

A scope can be cancelled. Its tasks see this at their next cancellation point, such as `delay` or `checkpoint`, which then returns `Cancelled`.

```bork
fn slowAnswer(s: Scope) uses clock + state: Int | Cancelled {
  delay(s, 2000)?
  42
}

fn main() {
  scope s {
    println(withTimeout(s, 50, child => slowAnswer(child)))
  }
}
```

```text
Cancelled { reason: "deadline exceeded" }
```

`withTimeout(s, ms, work)` runs `work` in a child scope and cancels it after the time limit. `cancel(s)` cancels a scope directly, and `cancelAfter(s, ms)` after a delay. Cancellation is a value like any other failure: it shows up in result types as `Cancelled`, and `?` passes it along.

A scope can be given limits when it is opened:

```bork fragment
scope s with taskTimeout(100), cleanupTimeout(500) {
  ...
}
```

- `taskTimeout(ms)` limits how long the scope waits for its tasks to stop once it has been cancelled. After that it stops waiting, and a task that is still running is left behind. This is the one case where a task outlives its scope.
- `cleanupTimeout(ms)` limits how long the scope waits for each resource to close.

## Channels

A channel carries values between tasks.

```bork
fn main() {
  scope s {
    numbers = channel[Int](s, 2)
    launch(s, () => {
      _ = send(numbers, 1)
      _ = send(numbers, 2)
      closeChannel(numbers)
    })
    println(receive(numbers))
    println(receive(numbers))
    println(receive(numbers))
  }
}
```

```text
1
2
Closed {}
```

`channel[T](s, capacity)` makes a channel owned by the scope. `send` and `receive` wait when the channel is full or empty, and report `Closed` or `Cancelled` as values.

## Shared state

All values are immutable, so tasks cannot interfere with each other's data. When tasks do need to share something that changes, they use an atom: a cell that holds a value and replaces it atomically.

```bork
fn main() {
  counter = atom(0)
  scope s {
    range(0, 100).forEach(i => launch(s, () => {
      _ = update(counter, n => n + 1)
    }))
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

Two tools cover the cases where a block does not fit:

- `attach(resource, s)` keeps a resource open until another scope `s` ends as well. A function can open something in its own scope and hand it to its caller's.
- `openScope(parent)` opens a child scope with an explicit end, `closeScope(owner)`. The compiler checks that it is closed exactly once on every path. This is for lifetimes that overlap without nesting, such as replacing a connection with a new one before releasing the old one.

Most programs need only `scope` blocks.

## More

- [wc](../../examples/wc/main.bork) reads files concurrently, one task and one scope per file.
- [bork/tasks](../std/tasks.md) has bounded task pools for limiting how much work runs at once.
- [bork/http](../std/http.md), [bork/sql](../std/sql.md), [bork/fs](../std/fs.md), and [bork/net](../std/net.md) all hand out resources that belong to scopes.

---

Previous: [Effects](effects.md) · Next: [Compile-time evaluation](comptime.md) · [All pages](../README.md#the-language)
