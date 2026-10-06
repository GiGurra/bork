# Handing resources over through a channel

> **Status:** Implemented: scoped resource handoffs. Current docs: [channels](../language/channels.md) and [scopes](../language/scopes.md).
> Bork blocks below are design sketches; the linked current docs contain checked examples.

Design for bork-u9vlg7, a follow-up of [move](move.md) and the
[channels redesign](channels.md). Implemented as described here (the
lead approved the API on 2026-10-06).

## The problem

`move(conn, w)` hands a resource to a scope the sender can name. A worker
pool cannot be written that way: the acceptor does not know which worker
takes the next connection. Today it sends the connection on a channel:

```
conn = net.Dial(addr, app)?
_ = jobs.send(app, conn)?        // pins conn's handle: app keeps it until app ends
```

`send` is a keep. The sender may go on using `conn`, so `app` keeps it
open until `app` ends, and the worker gets a borrowed value it cannot move
on. The supported workaround, `jobs.send(s, move(conn, jobsScope))`, gives
the source prompt release, but the worker still borrows the connection:
it cannot move it into its session scope, so the connection lives until
the channel's scope ends.

## Proposal

A channel kind for resources, whose send consumes:

```bork fragment
// Handoff passes resources between tasks, one owner at a time.
type Handoff[R] = private { native: ChannelHandle }

fn handoff[R](s: Scope, capacity: Int where validCapacity = 0): Handoff[R]

fn (h: Handoff[R]) handOver[R](s: Scope, r: R) uses state: Ok | Closed | Cancelled
fn (h: Handoff[R]) receive[R](s: Scope) uses state: R | Closed | Cancelled
fn (h: Handoff[R]) tryReceive[R]() uses state: Option[R] | Closed
fn (h: Handoff[R]) values[R](s: Scope): Seq[R] uses state
fn (h: Handoff[R]) close[R]() uses state
fn (h: Handoff[R]) length[R]() uses state: Int
fn (h: Handoff[R]) capacity[R](): Int
```

`R` must be a resource type, as for `move`: `handoff[Int](s)` is a
compile error.

```bork fragment
scope app {
  conns = handoff[Conn](app, 16)
  for (i in Seq.range(0, workers)) {
    fork(app, () => {
      for (c in conns.values(app)) {
        scope session {
          owned = move(c, session)        // the worker owns it now
          serve(owned)
        }                                 // closed here, not when app ends
      }
    })
  }
  for (n in Seq.range(0, 100)) {
    conn = net.Dial(addr, app)?
    conns.handOver(app, conn)?            // app lets go of it
    // using conn here is an error: it was handed over to channel conns at line 14
  }
  conns.close()
}
```

### Sender: `handOver` is a move into the channel's scope

`h.handOver(s, r)` checks `r` exactly as `move(r, <the channel's scope>)`:
owned here, one source, same frame and loop, not pinned, a direct call. It
ends `r`'s handles, so `r` and every value holding it are unusable after
it ("conn was handed over to handoff conns at line 14, which ends its use
here"). It does not pin anything, since the sender no longer has it.
There is no quick fix, unlike `move` → `attach`: a handoff has no `send`,
and switching to a `Channel` changes what the receiver may do.

Unlike `move`, a resource that already belongs to the channel's scope can
be handed over: the registration stays, and only the sender's handle ends.

**Failure releases it.** If the hand-over gives `Closed` or `Cancelled`,
nobody received the resource, and the sender's handle is gone either way
(the checker cannot tie handle states to which member of the result came
back). So the runtime releases the registration at once: the resource
closes, unless it is attached elsewhere. This is Go's
`if err := send(conn); err != nil { conn.Close() }`, done for you, and
Rust's `SendError` dropped. Nothing waits for the channel's scope to end.

### Receiver: a handle in the channel's scope

Every value in a `Handoff` was handed over, so each is registered in the
channel's scope and held by no one else. `receive(s)`
gives it with a **fresh handle** in the channel's scope, created in the
receiving frame, as an acquisition does. The receiver can use it (it
stays open until the channel's scope ends), `move` it, `attach` it, or
hand it over again. Received through a pattern (`c: Conn => ...`) or `?`,
the origin flows as for any acquisition. `tryReceive()` gives an
`Option[R]`, and a variant's payload has no tracked origin, so what it
gives is borrowed.

`for (c in h.values(s)) { ... }` gives `c` a fresh handle per iteration,
created inside the loop body, so the body may move it. `values` used any
other way (`toList`, passed on as a `Seq`) gives borrowed values.

The handle's source scope must be nameable at the later `move`, for code
generation. It is read from the channel itself: the runtime channel
records the scope it was made in. So the received value is owned only when
the channel is a simple name (a local or a parameter, also one declared
`in` another, whose handles then name the parameter); `make().receive(s)`
gives a borrowed value.

### Runtime

`handOver` compiles to `_borkHandOver(s, ch, r, from)`:

1. Move the registration from `from` (recorded by the checker, as for
   `move`) to the channel's scope, before the value is visible. A receiver
   may move it on as soon as it is queued, so it must already belong there.
2. Send, as `send` does.
3. On `Closed` or `Cancelled`, release that registration in the channel's
   scope. A channel whose scope has finished closing gives `Closed`
   instead of the move's panic.

`receive` and `values` need nothing new. A buffered value nobody receives
closes with the channel's scope, as any resource registered there.
`_borkChan` gains its `*_Scope` (it has only the context today).

## Why a separate type

`send` stays a keep (lead decision on bork-u8ndmy). If `Channel[Conn]`
also took `handOver`, a receiver could not tell whether the value it got
was handed over (unique, movable) or sent (the sender may still use it).
Moving a sent one would take a registration someone else still relies on.
So the receiver's ownership has to come from the channel's type. Plain
`Channel[Conn]` keeps today's meaning and its existing users.

Alternatives considered:

- **`handOver` on `Channel[T]`, receiver borrows.** Gives the sender
  prompt release, but the receiver can never move: the main use (worker
  pool, then a session scope) stays impossible. It is what
  `send(move(conn, chScope))` already gives.
- **Make `send` consuming on `Channel[R]` for resource `R`.** Breaks the
  keep pattern (`stores` cases, `put(ch, c: Conn in ch)`) and the lead's
  decision, and hides the move at the call site.
- **Give the resource back on failure** (`Ok | Refused[R] | ...`, as
  Rust's `SendError<T>`). Needs result-dependent handle states, or a field
  origin, which the checker does not track. Possible later with
  `tryHandOver` for load shedding that answers before closing.
- **`Channel[Owned[R]]`.** Same type-level split, but every operation
  would wrap and unwrap.

## Select arms

A `select` takes a handoff's `h.receive(s)` and `h.handOver(s, r)` arms
(bork-hu1nam). A receive arm's value is the receiver's own, as for
`receive`. A hand-over arm checks `r` as `handOver` does where the arm is
written, but moves it only if the arm is the one completed: the checker
marks `r` handed over where that arm's body starts, so the other arms'
bodies (the `_` arm and a `Cancelled` outcome included) still own it, and
after the `select` it is possibly handed over. At run time the move
happens with the channel locked, before the value can be received, and
only when the send completes. A sender that turns out to have no live
receiver after all moves it back. A hand-over to a closed handoff
releases `r`, as `handOver` does. A move that fails (an orphaned
task's source scope has finished) fails the sender, not the receiver.
The locks nest channel → owner → scope → resource handle, and nothing
takes a channel's lock while it holds one of the others: a scope's
finalizers and an owner's `closeFn` run with neither lock held, and
cancellation callbacks run on their own goroutines.

## Not in this design
- `tryHandOver`, an unbounded `Handoff`, `forkProducer`/`merge` for handoffs.
- Values holding a resource (a record with a `Conn` field): `move` does
  not move them either.

## Plan

One PR, after the lead's OK on this note: the type and methods in the
prelude (`internal/prelude/concurrency.bork`), the checker (handOver as a
move, fresh handles at receive and in `values` loops), the
runtime, golden cases `handoff` and `handoff_fail`, runtime tests for the
failure release and a receiver moving while the sender waits, and docs:
`docs/language/channels.md`, `scopes.md`, `requirements.md`, and the
Interactions section of `move.md`. LSP hover gets "handed over to handoff
conns at line N" from the existing ownership text.
