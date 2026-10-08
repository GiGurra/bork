# Channels and select

> **Status:** Implemented: channel methods, buffering, producers and select expressions. Current docs: [channels and select](../language/channels.md). The original implementation review and delivery plan below record the design rationale.
> Bork blocks below are design sketches; the linked current docs contain checked examples.

Design rationale for bork-73dn9h, including [the case for a select
expression](#the-case-for-a-select-expression).

Before the redesign, channels used `channel[T](s, capacity)` with free functions
`send`, `receive`, `closeChannel` and `received`, and a receive-only
`[a.receiveCase(f), b.receiveCase(g)].select(s)` whose mapping functions
must turn every arm into one common event type. That API was documented in one
section of the scopes page and shown in `examples/task_fanin`. This
note records the review that led to the implemented redesign: Go-familiar
methods, explicit capacity choices including an opt-in unbounded buffer,
a `select` expression with receive, send, timer and non-blocking arms,
time-based sources (`time.After`, `time.Tick`), iteration, producers, and
dedicated examples and reader pages.

## Review of the original implementation

`internal/prelude/concurrency.bork` (`channel`, `send`, `receive`,
`closeChannel`, `received`), `internal/prelude/fanin.bork` (`receiveCase`,
`select`) and `internal/gen/fanin.go` (`_borkFanInReceive`). A channel is a
Go `chan T` that is never closed, plus a separate `closed` signal channel,
so that a late send cannot panic.

| # | Finding | Severity |
|---|---|---|
| 1 | **Cancellation only reaches operations through the channel's owner scope.** An operation running in a narrower scope never sees that scope's cancellation. `scope s { ch = channel[Int](s, 0); withTimeout(s, 50.millis(), c => receive(ch)) }` hangs forever: the deadline cancels `c`, but `receive` waits on `s`, and `withTimeout` joins its work. Reproduced with a built binary. The same applies to `race` losers and to tasks of an owned child scope. | bug |
| 2 | **`Closed` is not final.** Close and a blocked send race: a sender blocked on a full buffer when the channel closes picks at random between "space freed" and "closed", so it can still deliver after a receiver has already been given `Closed`. The value is then either lost (no one receives again) or received after `Closed`. Its `send` returned `Ok` either way. | bug (narrow race) |
| 3 | **Cancellation is not decisive.** `receive` selects between data and `ctx.Done()` at random, so after cancellation it may keep returning values for a while; `send` likewise may still succeed. Callers cannot rely on "cancelled means stopped". | semantics |
| 4 | **`received` hides cancellation.** It returns a partial list on `Cancelled`, which looks like a complete one. | semantics |
| 5 | **Select only receives**, cannot send, time out or poll, and its arms must be pure mapping functions to a common result type (callbacks stored in a record cannot have open effects, which is why `mapResult` is `uses nothing`). Arms cannot `return`, `?` or `break` out of the enclosing function. | design |
| 6 | **Capacity** is an unchecked `Int`: a negative capacity panics inside Go's `make`. There is no unbounded option, and a Go channel cannot grow. | design |
| 7 | **Naming and shape** are free functions (`send(ch, x)`, `closeChannel`), against the "methods are the API" convention, and unlike Go users' expectations (`ch <- x`, `close(ch)`). | design |
| 8 | Fairness is fine (Go's `reflect.Select` picks uniformly among ready arms), and there are no goroutine leaks: no helper goroutines are started, and every channel registers one finalizer with its scope. | ok |

Findings 1 to 3 are correctness problems in the runtime, and the fix for
them (one synchronisation point per channel, owned by bork's runtime rather
than by Go's `chan`) is also what unbounded buffers, send arms and fair
timer arms need. So the redesign replaces the runtime rather than patching it.

## Design overview

```bork fragment
scope s {
  jobs = channel[Job](s, 16)          // fixed buffer: send waits when 16 are queued
  results = channel[Result](s)        // unbuffered: send waits for a receiver
  log = unboundedChannel[String](s)   // grows; send never waits

  fork(s, () => {
    for (job in jobs.values(s)) {      // until jobs is closed
      _ = results.send(s, run(job))
    }
  })

  _ = jobs.send(s, job)?               // Ok | Closed | Cancelled
  deadline = time.After(s, seconds(2))

  select {
    r = results.receive(s) => handle(r)            // r: Result | Closed
    jobs.send(s, next) => println("queued")
    _ = deadline.receive(s) => println("timed out")
  }?                                   // the select gives R | Cancelled
}
```

### Channels and capacity

```bork fragment
fn channel[T](s: Scope, capacity: Int where nonNegative = 0): Channel[T]
fn unboundedChannel[T](s: Scope): Channel[T]
```

| Kind | Made with | `send` waits | Memory | Use for |
|---|---|---|---|---|
| Unbuffered | `channel[T](s)` | until a receiver takes the value | none | handing off work; synchronising two tasks |
| Fixed | `channel[T](s, n)` | while `n` values are queued | `n` values | pipelines with backpressure: a slow consumer slows the producer |
| Unbounded | `unboundedChannel[T](s)` | never (only `Closed` or `Cancelled`) | grows with the backlog | mailboxes and event queues whose producers must never block, and cycles that would otherwise deadlock |

Decision: **growing is opt-in, under its own name.** A fixed buffer is the
default because backpressure is what keeps a pipeline's memory bounded when a
consumer falls behind; an unbounded channel trades that for producers that
never wait, and its memory is bounded only by how much the producers send.
A separate constructor (rather than a sentinel capacity such as `-1` or a
`Capacity` variant) makes that choice visible in review and greppable. The
unbounded buffer is a ring that doubles when full and halves when a quarter
full, so a burst does not pin memory forever. There is no soft limit or
drop policy in this version (Go has neither); `trySend` on a fixed channel
is the way to shed load.

`capacity` gains a `nonNegative` fact, so a negative capacity is a compile
error, or a checked failure at an untrusted call site, instead of a Go panic.

### Operations

Methods, named after Go where Go has a name:

```bork fragment
fn (ch: Channel[T]) send[T](s: Scope, x: T) uses state: Ok | Closed | Cancelled
fn (ch: Channel[T]) receive[T](s: Scope) uses state: T | Closed | Cancelled
fn (ch: Channel[T]) trySend[T](x: T) uses state: Ok | Full | Closed
fn (ch: Channel[T]) tryReceive[T]() uses state: Option[T] | Closed
fn (ch: Channel[T]) close[T]() uses state
fn (ch: Channel[T]) length[T]() uses state: Int      // values queued now
fn (ch: Channel[T]) capacity[T](): Option[Int]       // None when unbounded
fn (ch: Channel[T]) values[T](s: Scope): Seq[T] uses state
fn (ch: Channel[T]) toList[T](s: Scope) uses state: List[T] | Cancelled
```

**Every blocking operation names the scope it may be cancelled by**
(`send(s, x)`, `receive(s)`, `values(s)`), exactly like `delay(s, duration)` and
`time.Sleep(s, d)`. This is the fix for finding 1: the operation waits on
the caller's scope, not only on the channel's owner. It also gives up with
`Cancelled` when the channel's owner scope is cancelled (which a caller's
scope nested in the owner sees anyway). There is no implicit "current scope"
in bork's runtime, and inventing one (goroutine-local state) would be a
larger change than the problem warrants. The cost is one argument per call;
the payoff is that `withTimeout(s, 50.millis(), c => ch.receive(c))` works, and the
mistake `withTimeout(s, 50.millis(), c => ch.receive(s))` is visible in the code
(a later lint can flag a lambda that ignores its scope parameter while
waiting on an outer one).

Semantics, all decided under the channel's one lock:

- **Close** is idempotent and final. After `close()`, every `send` and
  `trySend` gives `Closed`, including senders blocked at the moment of the
  close (their values are not delivered). Receivers get the values already
  queued, in order, and then `Closed`, forever. `send` giving `Ok` means
  the value was queued or handed to a receiver, and will be received unless
  the owner scope ends first. (Go panics on a send to a closed channel; bork
  makes it a value, as now.)
- **Cancellation wins.** An operation checks its scopes before and after
  waiting: once the operation's scope or the owner is cancelled, it gives
  `Cancelled`, even if data is available. `Cancelled` reports the reason of
  whichever scope was cancelled (the caller's first).
- **Scope end.** When the owner scope ends it is cancelled first (every
  waiting operation gives `Cancelled`), its tasks are joined, and then the
  channel is closed and its buffer dropped. A channel never outlives its
  scope; the existing lifetime checks already reject that.
- **Order and fairness.** Values are received in the order they were sent
  (FIFO per channel). Blocked senders and receivers are served first come,
  first served. A `select` with several ready arms picks one uniformly at
  random, as Go does, so no arm starves.
- **Signals without a payload.** `Ok` cannot be a type argument, so a
  channel that only signals is `Channel[Bool]` or similar; closing a
  channel is itself the idiomatic broadcast ("no more work"). Tasks that
  need a stop signal use scope cancellation, not a done channel.

`trySend` and `tryReceive` never wait; `Full` is a new prelude record
(`{}` like `Closed`). They cover load shedding and polling without a
`select`.

`values(s)` is a `Seq[T] uses state` that receives until the channel is
closed, so `for (job in jobs.values(s)) { ... }` is Go's
`for job := range jobs`. It also ends when `s` is cancelled; code that must
tell the two apart calls `checkpoint(s)?` after the loop. (A sequence cannot
end with a value, and making every element `T | Cancelled` would make the
common loop noisy.) `toList(s)` replaces `received`, and gives `Cancelled`
rather than a partial list (finding 4). Traversing `values` again continues
receiving where the last traversal stopped, as the Seq design allows for
effectful sequences.

### Producers

```bork fragment
fn forkProducer[T](s: Scope, capacity: Int where nonNegative,
              work: (Channel[T]) => Ok | Closed | Cancelled): Channel[T]
```

`forkProducer` makes a channel, launches `work` as a task of `s` with it, and
closes the channel when `work` returns, so consumers' loops end. If `work`
panics, the failed task cancels `s` as any task does: consumers waiting in
`s` get `Cancelled`, and the channel closes with `s`. It is Go's "generator goroutine that `defer close(out)`s", without the
chance of forgetting the close:

```bork fragment
squares = forkProducer[Int](s, 4, out => {
  for (n in Seq.range(0, 10)) {
    out.send(s, n * n)?
  }
  Ok
})
for (x in squares.values(s)) { println(x) }
```

`work`'s callback effects are charged to the caller, as for `fork`. `?`
works inside because `Closed` (the consumer closed it) and `Cancelled` are
the natural ways for a producer to stop.

`merge[T](s, sources: List[Channel[T]]): Channel[T]` is the fan-in helper:
an unbuffered channel fed by one task per source, closed when every source
is closed. It covers "select over a dynamic number of channels", which a
fixed-arm `select` cannot express.

## Select

### The proposal: a `select` expression

```bork fragment
outcome = select {
  n = numbers.receive(s) => s"number $n"     // n: Int | Closed
  t = texts.receive(s) => s"text $t"         // t: String | Closed
  results.send(s, next) => "sent"            // the binding is optional
  _ = deadline.receive(s) => "timed out"
  _ => "nothing ready"                       // optional: makes select non-blocking
}
```

- A `select` has one or more **operation arms** and at most one `_` arm.
  An operation arm is `[Name =] channel.receive(scope)` or
  `[Name =] channel.send(scope, value)`: the very calls used outside a
  select, so there is nothing new to learn about what they mean. The
  channel, scope and value expressions are evaluated once, in source order,
  before waiting.
- It waits until one operation can complete, completes **exactly that
  one**, binds its result (`T | Closed` for a receive, `Ok | Closed` for a
  send, as the standalone calls give minus `Cancelled`) and evaluates that
  arm's body. With several ready, one is chosen uniformly at random. A
  closed channel's receive is always ready, as in Go (a receive arm that
  should stop firing belongs in a loop that drops it, or in `merge`).
- With a `_` arm it does not wait: if no operation is ready, the `_` arm
  runs (Go's `default`).
- **Cancellation is implicit.** If any arm's scope (or a channel's owner)
  is cancelled before an operation completes, the select completes no
  operation and gives `Cancelled`. Its type is the join of the arm bodies'
  types (the same widening as `if` and `match`) plus `Cancelled`, so the
  usual forms work: `select { ... }?` to pass cancellation on, or a
  `match` over it. A select whose arms use several scopes is cancelled by
  any of them.
- Arm bodies are ordinary expressions of the enclosing function: `return`,
  `?`, `break` and `continue` (inside a `for`) mean what they mean
  around the select. This is what Go code does in `case` bodies all the
  time, and it is the main reason for an expression rather than a library
  call.
- Effects: the select itself is `state`; arm bodies and operand expressions
  contribute their own effects to the enclosing function, as any code does.
- Errors: an arm head that is not a send or receive of a `Channel` is an
  error with the expected forms; a select with only a `_` arm or with two
  `_` arms is an error; the compiler offers a fix from the old
  `[...receiveCase(f)].select(s)` form where it is mechanical.

`select` becomes a keyword only where an expression starts and is followed
by `{`, as `scope` and `with (` are contextual. The list method `select` and
`receiveCase` are removed (pre-1.0, with a fix).

### Timeouts and time sources

Timeouts are channels, as in Go (`time.After`), and select treats them like
any other receive:

```bork fragment
fn After(s: Scope, d: Duration) uses clock + state: Channel[Instant]
fn Tick(s: Scope, every: Duration where positive) uses clock + state: Channel[Instant]
```

- `time.After(s, d)` gives a channel owned by `s` that delivers one
  `Instant` (when it fired) after `d`, and is then closed. Because it closes
  after firing, a deadline made once before a loop stays expired: every
  later select sees its arm ready (with `Closed`). That is the behavior
  loops want, and it avoids Go's "the timer fired once and the next select
  waits forever" trap. A nonpositive `d` fires immediately.
- `time.Tick(s, every)` delivers an `Instant` every period into a buffer of
  one, dropping ticks a slow receiver misses (Go's Ticker), until the
  channel is closed or `s` ends.
- **No leaked timers**: both are backed by `time.AfterFunc`, stopped when the
  channel is closed (by `close()`, by firing, or when `s` ends). Unlike
  Go's `time.After` before Go 1.23, nothing outlives the scope. A timer that
  fired and was closed releases its runtime timer at once; its scope keeps
  only a small owner record until the scope ends. Creating one `time.After`
  per iteration of a long loop in a long-lived scope therefore grows that
  scope by a few words per iteration: make the deadline once outside the
  loop, or put the loop body in its own `scope`. (The runtime may later
  drop fired owners from the scope's finalizer list; see open questions.)
- Tests can replace them with ordinary channels, since they are ordinary
  channels.

**Two styles, one rule.** A deadline that bounds a whole piece of work
(including tasks it starts, HTTP calls and `delay`s) belongs on the
*scope*: `withTimeout(s, duration, c => ...)` or `cancelAfter(s, duration)`, after
which every select and channel operation with that scope gives `Cancelled`
on its own, with no arm needed. A timeout that is *part of the
conversation* (wait for a reply for 200 ms, then resend; tick every second;
give up waiting on one source but keep the others) is a `time.After` or
`time.Tick` arm. Example:

```bork fragment
scope s {
  cancelAfter(s, 5.seconds())                  // the whole exchange: implicit Cancelled
  replyBy = time.After(s, millis(200))  // this request: an arm
  match (select {
    r = replies.receive(s) => r          // Reply | Closed
    _ = replyBy.receive(s) => TimedOut {}
  }) {
    reply: Reply => handle(reply)
    _: Closed => println("server went away")
    _: TimedOut => retry()
    c: Cancelled => println(s"gave up: ${c.reason}")
  }
}
```

### The case for a select expression

The lead asked for a keyword-free form if possible. Three were considered;
each is clearly worse than the expression, for reasons that do not go away
with tuples or positional variants.

1. **Handler callbacks** (Kotlin's `select { onReceive(ch) { ... } }` as
   a library: `select(s, [numbers.onReceive(n => ...), ...])`). Arms
   cannot `return`, `?`, `break` or `continue` out of the enclosing
   function, which is what most select arms do (stop a loop, propagate a
   failure). Worse, a record or list cannot hold callbacks with open
   effects (`field run of Arm must be (Int) => R, ... it uses what an open
   parameter uses`), so the handlers would have to be `uses nothing`: the
   current design's restriction, and the reason it needs an Event type.
2. **Match over a positional result**, once tuples and positional variant
   payloads land: `match (select(s, (numbers.recv(), texts.recv(),
   after))) { .First(n) => ...; .Second(t) => ...; .Third(_) => ... }`.
   This needs a family of prelude types (`Selected2` ... `SelectedN`), and
   the arms name sources by position, far from where the sources are
   written; reordering the tuple silently swaps meanings when the payload
   types agree. Send arms and the `_` arm have no natural place.
3. **Match over a union of distinct types** (`Int | String | Instant`):
   breaks as soon as two sources carry the same type, and cannot say which
   channel closed.

The expression costs one contextual keyword and one grammar rule. Per
docs/syntax-changes.md it touches the parser, formatter, checker, generator,
LSP (completion, semantic tokens, folding), the tree-sitter grammar and the
editor packages. The generated Go is a call into the runtime's select with
an arm table, followed by a Go `switch` on the chosen index, so arm bodies
compile like `match` arms.

If the human prefers no keyword anyway, the fallback is option 2, built on
bork-ys21yg and bork-3ic73j, with the runtime and every other part of this
design unchanged.

## Runtime

A new runtime file in `internal/gen` replaces `_borkReceiveChoice` and
`_borkFanInReceive`:

- `_borkChan` holds a mutex, a ring buffer (fixed, or growing for
  unbounded), `closed`, and FIFO queues of waiting receivers and senders.
- A waiter belongs to one select (a single `send`/`receive` is a select of
  one arm, with a fast path that does not allocate a waiter when it can
  complete at once). A select shuffles its arms, locks the distinct
  channels in a fixed (address) order, and completes the first ready arm;
  otherwise it enqueues a waiter on every channel, unlocks, and parks on its
  wake-up channel and its scopes' `Done()` channels. A waker claims the
  select with a compare-and-swap, so exactly one operation completes;
  stale waiters are removed on wake-up. This is Go's own `selectgo`
  design, in Go.
- Close takes the lock, marks the channel closed, and wakes every waiter:
  receivers drain the buffer in order and then see `Closed`; senders see
  `Closed`. Nothing completes after `closed` is set except draining, which
  gives finding 2's guarantee.
- Cancellation is checked under the lock before an arm completes, which
  gives finding 3's.
- Timers are `time.AfterFunc` callbacks that do a non-blocking send into a
  capacity-1 channel (and close it, for After); `close()` and the owner
  scope's finalizer stop the timer.

`awaitFirst`, `race` and the other task fan-in helpers keep their own
runtime (they select over task completion, not channels). A later change
could let a `Task` be a select source (`r = task.awaited(s) => ...`); not in
scope here.

## Comparison with Go

| | Go | bork |
|---|---|---|
| Make | `make(chan T)`, `make(chan T, n)` | `channel[T](s)`, `channel[T](s, n)`, `unboundedChannel[T](s)` |
| Unbounded buffer | no (write a goroutine + slice) | opt-in constructor |
| Lifetime | garbage collected; goroutines can leak blocked on it | owned by a scope; closed and every waiter released when the scope ends |
| Send / receive | `ch <- x`, `x, ok := <-ch` | `ch.send(s, x)`, `ch.receive(s)`, results as values |
| Send on closed | panic | `Closed` |
| Close twice | panic | no-op |
| Cancellation | by convention: a `ctx.Done()` case in every select | every blocking operation takes a scope and gives `Cancelled` |
| Nil channel | blocks forever (used to disable a case) | no nil; use `merge`, or a loop that drops the arm |
| Range | `for x := range ch` | `for (x in ch.values(s))` |
| Select | statement; `case`s; `default` | expression; arms are the ordinary calls; `_` arm; value is `R \| Cancelled` |
| Timeout | `time.After` (timer lives until it fires), `context.WithTimeout` | `time.After(s, d)` (closed after firing, stopped with its scope), `withTimeout` / `cancelAfter` |
| Ticker | `time.NewTicker`, must `Stop()` | `time.Tick(s, d)`, stopped with its scope |
| Direction types | `chan<- T`, `<-chan T` | not yet (open question) |

## Examples and docs

New runnable, tested examples (each with a golden output case under
`testdata/cases`, and listed in docs/examples.md):

- `examples/channels`: making channels of each kind, send/receive/close,
  results as values, `for` over `values`, `forkProducer`.
- `examples/channel_select`: select with a reply timeout, a ticker, a send
  arm, a non-blocking poll, and scope-deadline cancellation.
- `examples/pipeline`: a worker pool fed by a bounded jobs channel, results
  collected, showing backpressure.
- `examples/fan_in_out`: fan-out to N workers, fan-in with `merge`.
- `examples/unbounded_queue`: an event mailbox with `unboundedChannel`, and
  where its memory goes.

`examples/task_fanin` loses its channel half and stays about tasks.

A new reader page, `docs/language/channels.md` (between scopes and
comptime), replaces the channels section of scopes.md with a link, and
covers: kinds and capacity tradeoffs, results as values, close semantics,
select, timeouts (both styles), iteration, producers, fan-in/out, and a
"coming from Go" table. docs/grammar.md, docs/requirements.md, README.md,
docs/tour.md and the time std page are updated with each PR.

## Delivery

1. **Runtime and methods.** New channel runtime, method API with explicit
   scopes, capacity fact, `unboundedChannel`, `trySend`/`tryReceive`,
   `values`, `toList`, `forkProducer`, `merge`; migrate prelude users, std,
   examples and tests; the free functions are removed with compiler fixes
   (as for other pre-1.0 renames). The list `select` keeps working on the
   new runtime until PR 3.
2. **Time sources.** `time.After`, `time.Tick` in bork/time, with tests for
   timer release on close and scope end.
3. **Select expression** (after the human's decision), end to end per
   docs/syntax-changes.md; removes `receiveCase` / list `select`.
4. **Examples and docs**: the examples above and docs/language/channels.md.
   Pieces of this land with 1 to 3 where they document new behavior.

Tests in each PR: race-detector unit tests of the runtime (close racing
send, cancellation racing data, select exactly-once under contention,
fairness over many trials, unbounded growth and shrink, no goroutines left
after scope end), plus golden cases for the bork surface and for the
compile errors.

## Decisions to check

- Blocking operations take an explicit scope (`ch.receive(s)`): breaking,
  and one more argument, but it fixes finding 1 for good.
- Unbounded is opt-in under its own constructor.
- `time.After` closes after firing, so a reused deadline stays expired.
- `values(s)` ends quietly on cancellation (follow with `checkpoint(s)?`).
- A select's value includes `Cancelled` always, even with a `_` arm (a
  select in a cancelled scope gives `Cancelled` rather than running `_`).
- The select expression itself (human decision).

## Open questions (not in this epic unless asked)

- Direction types (`Sender[T]` / `Receiver[T]`) so `time.After`'s channel
  cannot be sent to or closed by a consumer.
- Tasks as select sources.
- Dropping a fired timer's owner record from its scope early, so per-loop
  `time.After` in a long-lived scope costs nothing.
- A lint for a lambda that waits on an outer scope while ignoring its own
  scope parameter.
