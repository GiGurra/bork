# Moving resources between scopes

Design for bork-u8ndmy. Nothing here is implemented yet. The plan is three
PRs: the checker and runtime, then task, owned-scope and channel integration
with the editor support, then examples and reader docs.

## The problem

A resource belongs to the scopes it is registered with, and closes when the last
of them closes. `attach(conn, app)` adds a registration. Nothing removes one, so
a resource cannot leave the scope it was opened in:

```
scope s {
  conn = connect(s)?
  w = attach(conn, worker)       // conn now closes when s and worker have both closed
  ...                            // s still holds conn, and could keep using it
}
```

That is fine when `s` ends first, as a request handler's scope does. It fails in
three cases:

- **Prompt release.** The source is the longer-lived scope. A connection
  manager opens connections in `app` and hands each to a session scope. With
  `attach`, `app` keeps every connection it ever handed out until `app` ends.
- **Single owner, checked.** Once `conn` is handed over, nothing stops the
  source from using it concurrently with the new owner. The compiler should
  reject that.
- **Cancellation.** After `attach`, the resource follows the latest attachment
  for cancellation, but the source still holds it, so the source's cleanup
  order still applies to it.

`docs/requirements.md` (Resources and scopes, owned child scopes) already asks
for this: "distinguish keeping a resource usable from releasing its previous
owner promptly". This design adds the release.

## Other languages

| Language | Mechanism | Use after move | Aliases at the move |
|----------|-----------|----------------|---------------------|
| Rust | Every non-`Copy` value is affine: assignment, passing and returning move it. `Drop` runs at the last owner's end. | Compile error, per place (partial moves of struct fields too), flow-sensitive, with "value moved here, in previous iteration of loop" and "possibly moved" for branches. | Borrows must end before the move (NLL). `thread::spawn` needs `'static` captures, so a value lent to a thread cannot be moved. |
| C++ | `std::move` is a cast to an rvalue; `unique_ptr`'s move constructor nulls the source. | Allowed. The source is "valid but unspecified" (`unique_ptr`: null). Only linters (clang-tidy `bugprone-use-after-move`) catch it. | Raw pointers and references into the moved object dangle silently. |
| Swift | `~Copyable` types are affine. `consume x` ends a binding early, for copyable values too. `consuming`/`borrowing` parameters. | Compile error for local bindings ("used after consume"), flow-sensitive. `consume` does not apply to globals or captured escaping vars. | Copyable values: other copies stay valid (consume ends one binding, not the value). |
| Go | None. Ownership is a convention: `defer c.Close()` in the owner, and handing off means not deferring, or setting a flag the deferred close checks. | Nothing. | Nothing. |

What bork takes:

- **From Rust,** compile-time, flow-sensitive "moved" and "possibly moved"
  states, with the same diagnostics shape: where it moved, which branch, which
  loop iteration. Also Rust's rule that something lent to another routine
  cannot be moved while that routine may still use it.
- **From Swift,** the move is an explicit operation (`consume`), not something
  every assignment does. bork values are immutable and freely copied, so making
  every binding of a resource affine would change the whole language for one
  feature. Unlike Swift, a bork move ends every copy, not just one binding,
  because what moves is the scope's registration, which all copies rely on.
- **From C++,** the warning: a runtime-only "moved-from" state, with no checker
  behind it, gives you use-after-move as a runtime bug.
- **From Go,** the runtime side: a move is "this scope no longer closes it".
  The checker makes that safe.

## Surface

No new keyword. A prelude function, which the checker treats specially, as it
does `attach`:

```
// move hands r from the scope it belongs to here over to scope s: the
// source no longer keeps it open, and it closes when s closes (or later,
// if other scopes it is attached to are still open). It gives r as a value
// of s. r and every value holding it are unusable after the move.
fn move[R](r: R, s: Scope): R
```

A connection manager dials upstream connections in its long-lived scope, warms
each one up, and hands it to a session. With `move`, the session alone keeps it,
and `app` lets go of it at once instead of when `app` ends:

```
fn startSession(address: String, app: Scope, session: Scope) uses net + state + clock: Ok | IoError | Cancelled {
  conn = net.Dial(address, app)?
  _ = net.WriteLine(conn, "HELLO")?       // set up while app owns it
  owned = move(conn, session)             // app no longer keeps it
  launch(session, () => serve(owned))
  _ = net.WriteLine(conn, "BYE")?         // error: conn was moved to scope session at line 4
}
```

Once same-block rebinding lands (bork-4exlxc), the usual form is
`conn = move(conn, session)`. Until then, bind the moved value to a new name.

## Static semantics

### Registrations, handles and origins

At runtime, a resource has registrations: one per scope that keeps it open
(`s.Own`, and one more per `attach`). A move transfers one registration from
scope A to scope B. The checker has to know which registration that is, which
values rely on it, and that nothing it cannot see still relies on it.

It tracks two things per value:

- **Lifetime (existing), plus handles.** The lifetime is the set of scopes a
  value depends on. It already flows through bindings, records, lists, unions,
  lambda captures, call results, patterns and block values. This design adds a
  new kind of element, the **handle**: "the registration of one resource in one
  scope, acquired in this frame". For `storeShorter`, `scopeOutlives`, `result`
  and the `in` checks, a handle behaves exactly like its scope, so every
  existing check keeps its answer. A handle adds a state: open, moved, possibly
  moved, or pinned. Every value built from a resource carries its handle: copies,
  records, lists, lambdas, `lazy` bindings, a transaction made from it. When the
  move ends the handle, all of them become unusable at once.
- **Origin (new).** The lifetime over-approximates what a value was *computed
  from*, which is not what it *is*. `other(decoy, p)` has `decoy`'s handle in its
  lifetime but may return `p`. So the checker tracks a separate origin set per
  resource-typed expression: the handles the value may be. Only these
  expressions have known origins:
  - an acquisition (below), `attach` and `move`, which create a fresh handle;
  - a variable, whose origin is its initializer's;
  - `?`, a block's tail, and a pattern that binds the whole matched value;
  - `if` and `match`, whose origin is the union of their branches' origins;
  - a record field select, whose origin is the field's origin as built
    (`{c: conn}.c`), tracked through record literals and copies of records
    bound in this frame.

  Every other expression has an unknown origin: other calls, `receive`,
  `current`, `await`, lambda and function parameters, list elements. A value of
  unknown origin is borrowed.

### Acquisitions

A call creates a fresh handle only when the checker knows that the callee
returns a new registration in a known scope:

- **`unsafe go` functions** whose declared result type mentions a concrete
  resource type `R` (`File | Error`, not a type parameter instantiated to `R`;
  `withTimeout[T]` with `T = Conn` returns whatever its lambda returned) and that
  take exactly one `Scope` argument. Go code is bound by the convention in
  `docs/std-go.md`. This design adds a line to it: a function given a scope that
  returns a resource returns a fresh registration in that scope (`s.Own`, or an
  attach), never one someone else holds, and keeps no task using it.
- **bork functions verified to acquire.** When the checker checks a function
  with exactly one `Scope` parameter `s` and a resource result, it records on
  the `Func` whether every returned value has a known origin whose handles are
  all in `s`, fresh in this call (made by an acquisition, `attach` or `move` in
  the body), and neither moved nor pinned at the return. `fn open(s: Scope) =
  connect(s)` and `scope inner { c = connect(inner)?; move(c, s) }` qualify.
  `fn rewrap(c: Conn, s: Scope): Conn = c` does not (unknown origin). Nor does
  a function that launches a task using the connection before returning it (it
  is pinned). The summary is computed in dependency order. Recursive functions
  without a summary yet are treated as not acquiring.
- **The scope argument is simple:** a scope block's variable, a `Scope`
  parameter or lambda parameter, or `b.scope`. `connect(if (x) { s } else { t
  })` or `connect(cfg.scope)` gives a borrowed value. That keeps the source of
  every handle a single runtime value that code generation can name.

Function values never acquire: their result has an unknown origin.

### What can be moved

`move(x, target)` needs:

1. **`x` is of a resource type,** as for `attach` (`R` must be a `*Resource`).
   Moving tasks, channels, atoms and scopes is not offered (see below).
2. **`x` is owned here:** its origin is known and not empty. Otherwise the error
   says that `x` is borrowed. It may be a parameter (the caller still has it), a
   lambda parameter, or have come out of a channel, an atom, a task or a call
   the checker cannot see into. The error suggests `attach(x, target)`, which
   works on borrowed values.
3. **One source.** All of `x`'s origin handles name the same scope. `c = if (a)
   { c1 } else { c2 }` with `c1` from `s` and `c2` from `t` cannot be moved:
   "c may belong to scope s or scope t". With both from `s`, the move ends both
   handles. Only the chosen one changes owner at runtime, so the other is
   conservatively unusable but still closed by `s`.
4. **Same frame.** Every origin handle was created in the current function or
   lambda, and outside any loop whose body contains the move. A lambda may run
   later (after the source ended) or more than once, and a loop would move the
   same registration twice. Owned scopes already use this rule ("a lambda
   cannot close or pass on owned scope b", "a loop cannot consume owned scope b
   from outside its body"). `lazy` and `async` initializers, generators and
   `comptime` blocks are separate frames too, so they cannot move what they
   capture.
5. **Not pinned** (next section).
6. **The target is a different scope.** `move(conn, s)` where `conn` belongs to
   `s` is an error ("conn already belongs to scope s"). The target is evaluated
   once. Any `Scope` expression works; the result belongs to its lifetime.
7. **A direct call.** `move` cannot be used as a function value, as `send`
   already cannot when its element type can belong to a scope.

The move ends the origin handles. The result has the lifetime
`{target..., new handle}` and that new handle as its origin. `move` is an
`unsafe go` prelude function with a `Scope` argument, so its special case
returns before the Go keep check, as `attach`'s does. Otherwise it would pin its
own argument.

**Moves inside a call's arguments.** Arguments are evaluated in `ArgOrder`
(named arguments can reorder them), and keeps are recorded after all of them.
So a call is re-checked after its arguments, as `ownerArgs` already does for
owners consumed during argument evaluation. No other argument of the call may
carry a handle that one of its arguments moved. `serve(conn, s, move(conn, w))`
is rejected.

### Pins: what another routine may still use

Some uses hand a value to code that keeps it after the current expression: a
task, a channel, an atom, Go code given a scope, a parameter declared `in`
another. What they keep is out of the checker's sight. What comes out of a
channel has the channel's lifetime and an unknown origin, and a running task is
not a value the checker sees again. Ending the handle cannot reach these
copies. So these uses **pin** the handles in what they keep (its lifetime, not
just its origin), and a pinned handle cannot be moved:

```
conn = connect(s)?
launch(s, () => pump(conn))               // pins conn's handle until s ends
w = move(conn, worker)                    // error: conn cannot be moved: a task of scope s
                                          // started at line 2 may still use it; attach it instead
```

The points that pin are the existing "keeps its argument" points of the
lifetime checker:

- `send`, `update` and `swap` pin what is stored, kept by the container.
- An `unsafe go` function or a function value given a `Scope` pins its other
  arguments, kept by that scope. This covers `spawn`, `launch`, `onClose`,
  `tasks.TrySpawn`, `sql.Begin(conn, s)` and the like.
- A parameter declared `in` another (`c: Conn in ch`) pins its argument, kept
  by the target argument.
- `async(s) x = ...` pins what the initializer captures, kept by `s`. A mock
  pins its captures for the rest of the test.

A pin records its keeper's lifetime and is released once every scope of that
lifetime has ended:

- **Inner scope blocks.** A task of an inner scope block stops pinning when
  that block ends. Not if the block has a `taskTimeout` policy, though: an
  orphaned task may still run, so its pins stay for the rest of the frame.
- **Owned children.** An owned child releases its pins only when it is closed
  with `closeScope`, not when its owner is passed on or returned: the child's
  tasks keep running under the new owner.
- **Branches.** Pins join across branches like moves do: pinned on any path is
  pinned.

This is deliberately conservative. A synchronous prelude function that takes a
scope only for cancellation (`withTimeout(s, ms, f)`) pins what `f` captures.
That costs nothing until someone wants to move such a value, and the error
names the pinning call. A `keeps nothing` marker for such functions can come
later if it turns out to matter.

### Using a moved value

Using a value with a moved handle is a lifetime error, the same kind as
"possibly released":

```
conn was moved to scope session at line 4; to keep using it here, attach it instead of moving it
holder may be released: it holds conn, which was moved to scope session at line 4
```

The fix (in `bork check --json`, and as an LSP code action) replaces that
`move(` with `attach(`. It is offered when the source still uses the value after
the move, the common mistake.

### Branches, conditions, loops, early exits

- **Branches.** An `if` or `match` that moves `x` in some branches leaves it
  possibly moved after the join: "conn may have been moved to scope w at
  line 7 (in the then branch of the if on line 6)". Unlike owners, this is not
  an error by itself: the branch that did not move it leaves the registration in
  the source, which then closes it. It is only an error to use `x` after the
  join. A branch that ends (`return`, `?`, panic) does not count, as for
  owners.
- **Conditions.** A move in a match guard or on the right of `&&`/`||` makes
  the value possibly moved afterwards (it may not have run).
- **Loops.** A loop body cannot move a value whose handle was created outside
  the body. A value acquired and moved in the same iteration is fine.
- **Early exits.** Nothing special. After the move, the target owns the
  registration, so a `?` or panic later in the function leaves it to the
  target. Before the move, the source still owns it.

### The trust model is attach's

`move(r, s)`, like `attach(r, s)`, gives `r` a lifetime of `s` alone, and drops
whatever else `r` was computed from. That is sound only if a resource stays
usable while it has a live registration, the rule `attach` already relies on
and the one `s.Own` documents for Go code. A resource whose Go value uses
another resource keeps that resource itself (`sql.Begin`'s transaction keeps
the connection's Go handle). The acquisition convention above is the one new
obligation on Go code.

## Runtime

### Registrations

`_Owner` keeps a count of registrations today, and each registration is an
anonymous `s.Defer(o.release)`. A move has to name the registration it
transfers:

```go
type _ownerReg struct { owner *_Owner; scope *_Scope; active bool }

// Own and attach create a _ownerReg; its deferred release does nothing once
// the reg is inactive.
func (o *_Owner) move(from, to *_Scope) error
```

`move(from, to)`, under `o.mu`:

1. Fails if the resource is closed (`count == 0`), or has no active
   registration in `from`.
2. Registers in `to` with a new `tryDefer`. It fails only once `to` has
   *finalized*: a new flag, set under `s.mu` in the same critical section in
   which the drain loop finds no finalizers left. It is not `closed`, which is
   set before the scope waits for its tasks. A task of `t` may still attach or
   move into `t` while `t` waits for it, as it can attach today, and the drain
   loop runs what it adds. Plain `Defer` after finalizing would add a finalizer
   that never runs, and the resource would never close. `attach` switches to
   `tryDefer` too.
3. Deactivates the `from` registration and drops its `owner` pointer, so the
   tombstone left in `from`'s finalizer list holds no Go handle or buffers.
   Scopes compact deactivated entries once they are more than half the list.
   A long-lived source that moves connections out one by one would otherwise
   grow without bound. The count does not change (+1 −1), so the resource
   never reaches zero during a move.

Any failure panics (`bork: move: the resource is already closed`, `...is not
owned by the source scope`, `...the target scope has finished closing`) before
anything changes, so a failed move leaves ownership as it was. If the source
closes concurrently (only an orphaned task can race it), `active`, read under
`o.mu`, decides: either the move wins and the source's release does nothing, or
the source releases first and the move fails.

The checker records the source scope of each move, which is always a simple
scope value (see Acquisitions), and code generation passes it:
`_move(r, from, to)`. The user-visible signature stays `move(r, s)`.

Cleanup order: the target runs the moved resource's release in its LIFO order
at the position of the move, as though it had been acquired there. It closes
before what the target had acquired earlier.

### Cancellation follows the active registrations

Resource handles with the rebinding hook (`_borkResourceHandle`) take
cancellation from one source today, the latest `attach`. With moves, that is
wrong:

```
conn = net.Dial(addr, s)?
a = attach(conn, app)                  // app also keeps it; it now follows app
launch(app, () => serveForever(a))
_ = move(conn, w)                      // follows w; w ends, raw.Close()
                                       // app still owns it, but the socket is gone
```

Instead, a handle is cancelled once the scopes of *all* its active
registrations are cancelled (or released), as the contextual Go bindings
already do (`requirements.md`, bind contexts). Attach adds a source, a move
replaces one, and a release removes one. The hook becomes a set; the API in
`std-go.md` stays the same. This also fixes the latent version of the bug with
`attach` alone (the original scope cancelled while it still owns the
resource, but the handle follows the attached one). It changes `attach`'s
documented behavior ("the latest attachment selects the cancellation
source"), so it is the one change here outside `move` (question 4).
Cancellation is still terminal. Moving a resource whose sources are all
cancelled gives a cancelled resource.

### Resources without an owner

Resources registered by Go code with plain `s.Defer` and no `_Owner` pass the
checker but panic at runtime when moved, as with `attach` today. The standard
library registers every resource with `Own`.

## Interactions

- **attach then move.** `a = attach(conn, app)` adds an `app` registration, and
  `a` gets its own handle in `app`. `move(conn, w)` moves only the `s`
  registration: `a` stays usable, and the resource closes when `app` and `w`
  have both closed. `move(a, w2)` would move the `app` one. A move never makes
  the target the only owner if the resource is attached elsewhere. It transfers
  one registration, exactly as `attach` adds one.
- **Tasks.** Move before handing over: `c = move(conn, w)`, then
  `launch(w, () => serve(c))`. No `spawn ... with handover` sugar for now: the
  two-line form says which registration moves, and the sugar would only save
  the binding. A move inside the task's lambda is already rejected by the
  same-frame rule. The task may start after the source has ended. Moving tasks
  themselves stays out of scope, as `requirements.md` decided for owned scopes:
  a task's captures, cancellation and failures stay with the scope it started
  in.
- **Owned child scopes.** `move(conn, b.scope)` gives `b` the resource.
  `closeScope(b)` closes it, and passing `b` on passes it on with
  `c: Conn in b`. Moving out of a child (`conn = connect(b.scope)`,
  `move(conn, app)`) lets the connection survive `closeScope(b)`. This is the
  rolling hand-over-hand case without nesting.
- **Channels.** `send` stays a keep, not a move: it pins the sender's handle.
  Handing ownership to whoever receives needs the receiver to get a handle,
  which `receive` cannot give for a value the sender may still hold. Until the
  channels redesign (bork-73dn9h) settles its API, the supported pattern is to
  move into the channel's scope (`send(ch, move(conn, chScope))`). The
  receiver borrows it and can `attach` it. A consuming send
  (`handOver(ch, conn)`: move into the channel's scope, end the sender's
  handle, give the receiver a handle in the channel's scope, which it may move
  again) is proposed for PR 2, in coordination with the channels worker. Each
  value is received once, so the receiver's handle is unique.
- **Server handlers.** `net.Listen` gives its handler a borrowed connection (a
  lambda parameter), which cannot be moved. The handler's scope closes when it
  returns, so `attach(conn, worker)` already has the effect of a move there. An
  accept-style API that acquires into a caller's scope is not part of this
  epic.
- **Parameters.** A parameter is never movable, even `c: Conn in b` with
  `b: OwnedScope`. The caller's tasks of `b` may still use `c`, and the callee
  cannot see those pins. Lifting this needs pin information in signatures:
  future work.
- **Orphaned tasks** (`taskTimeout`). They run after their scope started
  closing. Their moves are safe, but their outcome depends on timing:
  - A move from that scope succeeds while the scope has not yet released the
    registration, and panics with ownership unchanged once it has.
  - A move into a scope that has finished closing fails `tryDefer` and panics,
    instead of leaking.
  - A resource an orphaned task acquired on a closed scope (`Own`'s plain
    `Defer`) leaks today. Moving it into a live scope rescues it. Making `Own`
    use `tryDefer` is a separate fix, out of scope here.
- **Rebinding (bork-4exlxc).** `conn = move(conn, w)` binds a new `conn`. The
  old binding's handle is moved, and nothing can name it any more. Unused-local
  rules apply as usual. `_ = move(conn, w)` is the way to release from the
  source and let `w` close it without using it.

## Tooling

- **Hover** on a resource variable shows its lifetime (as today), plus "moved to
  scope w at line N", "possibly moved (line N, then branch of the if on line M)"
  or "pinned by the task started at line N". `Info` records per-variable move
  states for the LSP and `bork describe`. Hover also says when a resource is
  borrowed (unknown origin), so it is clear why it cannot be moved.
- **Diagnostics** keep the `lifetime.error` code. The "use after move" error
  carries the `move` → `attach` fix. The borrowed-value error suggests `attach`.
  The pinned error names the pinning call and suggests `attach`.
- **Editor grammars** need nothing. `move` is an ordinary identifier, so the
  syntax-change checklist does not apply. The `go_opaque_attach_allowed` case
  binds a local named `move`, which may need renaming once bork-4exlxc forbids
  shadowing prelude functions.

## Alternatives considered

- **A `move` keyword or `consume x`.** A function mirrors `attach`, needs no
  grammar, formatter or editor changes, and reads the same.
- **Affine resource types** (Rust, Swift `~Copyable`). Every binding, record
  field, list element and lambda capture of a resource would need move or
  borrow rules. bork's immutable values are freely copied, and the lifetime sets
  already track every copy. A handle in those sets gets the same safety without
  changing what a binding means.
- **Sole ownership** (a move removes every registration and leaves only the
  target). The checker would have to kill attached aliases in other scopes too,
  which it cannot see from the attaching scope, for example after an `attach`
  in a callee. Moving exactly one registration composes with `attach` and keeps
  the refcount rule ("closes when the last owner closes") unchanged.
- **An explicit source, `move(conn, from: s, to: w)`.** The checker always knows
  the source (rule 3), so writing it would add noise and one more error.
- **Ownership from the lifetime set alone** (an earlier draft of this
  document). Lifetimes over-approximate what a value was computed from:
  `other(decoy, p)` carries `decoy`'s handle and may be `p`. Moving it would
  move the caller's registration of `p`. Origins are needed.
- **Kill aliases instead of rejecting pinned moves.** Tasks and channel buffers
  are outside the checker's view. Rejecting is the only sound choice that does
  not involve the runtime.

## Plan

1. **Checker and runtime** (PR 1): handles in lifetimes, origins, acquisition
   summaries, the move rules, pins, branch and loop states, the call re-check,
   `_Owner` registrations, `tryDefer` (also used by `attach`), tombstones,
   cancellation from the set of active registrations, codegen of the source
   scope, `move` in the prelude. Tests:
   `testdata/cases/move` (hand-off, prompt release, attach mixes, owned child
   in and out, branches with a non-moving path, rebinding once available) and
   `move_fail` (use after move, holders, possibly moved, loops, lambdas,
   borrowed parameters, received values, pinned by task/channel/Go/`in`, two
   sources, same scope, function value, unknown origins such as `other(decoy,
   p)` and a generic `withTimeout` result, non-acquiring bork functions, moves
   inside a call's own arguments). Runtime tests for failed moves leaving
   ownership unchanged, and for the race with an orphaned task. Update
   `requirements.md`, `docs/language/scopes.md` and `std-go.md`.
2. **Integration** (PR 2): hover and describe states, the `attach` fix, the
   consuming channel hand-over if the channels design agrees, and owned-scope
   rolling with move.
3. **Examples and docs** (PR 3): an `examples/` program, a connection manager
   handing sessions to workers with prompt release, plus the tour and scopes
   page.

## Questions for the lead

1. The consuming channel send (`handOver`) in PR 2, or defer it until the
   channels redesign lands?
2. Is rejecting moves of all parameters (even `in` an owned scope) acceptable
   for v1?
3. Are conservative pins from synchronous scope-taking prelude functions
   (`withTimeout`) acceptable for v1, without a `keeps nothing` marker?
4. Cancellation following all active registrations changes `attach`'s
   documented "latest attachment selects the cancellation source". It fixes a
   latent bug, but it is a behavior change. OK to make it in PR 1?
