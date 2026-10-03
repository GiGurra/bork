# Transparent async bindings (design: bork-mais5u)

`async(s) name = expr` starts one initializer immediately as a task of scope
`s`. Reading `name` waits for that task and returns its result. The binding has
its ordinary type T, including failure alternatives and facts; it exposes no
Task wrapper. This is the scheduling counterpart of [lazy bindings](lazy.md),
sharing their initializer boundary, capture checks and transparent memo reads.
Local bindings are the first implementation; async fields and package bindings
are outside this design.

```bork
fn page(id: Int) uses net: String = scope request {
  async(request) user = fetchUser(id)
  async(request) orders = fetchOrders(id)
  render(user, orders)
}
```

## Declaration and reads

Both `async(s) name = expr` and `async(s) name: T = expr` are supported.
`async` is contextual only at a local binding head with the complete
`async(scope-expression) name` shape. Existing functions and values named
`async` remain usable. The scope expression is an ordinary expression of type
Scope, evaluated once at declaration, before scheduling the initializer. Its
`return` and `?` retain the enclosing function boundary; only the initializer
creates a new result boundary.
Destructuring, `_`, parameters, modifier combinations, fields and package
bindings are rejected. The binding's name is unavailable in its initializer.

Each execution of the declaration starts a fresh task, including each loop
iteration. The task evaluates the entire initializer, including call arguments,
in normal source order. Scheduling does not guarantee that it runs before the
next statement, only that it can run concurrently with that statement and other
tasks. Two declarations can therefore overlap without either being read.

The initializer is a function boundary returning T, exactly as for lazy.
Explicit `return` and `?` return from that boundary, not the enclosing function;
Option `?` needs a result annotation. `break`, `continue` and `yield` cannot
cross it. Ordinary inference, constrained annotations and result proof checks
apply before scheduling.

Any value read (an argument, condition, interpolation, match subject, `dbg`,
or eager alias `copy = name`) waits for completion. Capturing the binding in a
closure retains the shared cell without awaiting; reading it inside that closure
awaits. Concurrent and repeated reads share one result or cached panic.
No read restarts work. An eager alias contains the resolved T and follows its
ordinary lifetime rules. There is no public completion-state API or implicit
fan-in combinator: sequential reads can wait on tasks that already run in parallel.

## Effects and context

Initializer effects are charged at the declaration, including for unread
bindings. The scope expression's effects are charged there too. Scheduling
adds no new `state` effect, matching the existing `spawn` and `launch` API.
Reads are memo observations of work already charged at creation, rather than
repeatable pure evaluations of the initializer. Compile-time predicate and constant
evaluation must never schedule an async task or force a runtime async cell.
Ordinary runtime predicates and validators may read the resolved T.
A known-input pure candidate containing async is not evaluated at compile time;
existing proof or runtime validation rules apply instead.

Immutable lexical values and ambient `needs` are captured at declaration as
for lazy initializers and spawn callbacks. Dynamic mocks and logged/propagated
labels follow the existing spawn task-context rules: the scheduling context,
not the reader's context, determines the initializer's observations. Captured
capabilities are ordinary values, not snapshots of outside state.

## Scope ownership, cancellation and failure

The task belongs to the supplied scope. Captures must outlive that scope under
the same checks as a callback passed to `spawn`; use `attach` to extend resource
ownership when appropriate. The binding's cell depends on that scope and its
captures even when T is scalar. A closure retaining the cell cannot escape
those lifetimes; neither a later read nor a captured cell can be used after an
owned scope has been closed. Once read eagerly, a scope-independent scalar can
escape as ordinary data. Scope-dependent results keep their ordinary lifetime
requirements. OwnedScope capture and result restrictions match lazy bindings;
an owner may be created and consumed entirely inside the initializer.

An unread binding is still a task. Scope exit cancels the scope, joins its tasks,
then runs cleanup, with the existing timeout and logging policies. Cancellation
is cooperative; an initializer decides when to observe its captured scopes'
checkpoints. Reading does not invent a Cancelled alternative or independently
cancel its wait. Work needed before cancellation should be read before the
scope block ends. Unused-variable handling must retain the cell without reading
it, so it cannot turn an unread failure into an observed one.

Failure alternatives are values: `User | HttpError` remains that binding's type.
A panic cancels the task's scope and is re-raised on every read. If no read
observes it, scope exit reports it under the existing task-failure policy.
Reading marks the task failure observed exactly as `await` does. Retry requires
a new declaration. Task waits retain spawn's ordinary blocking/deadlock limits;
there is no whole-program dependency-cycle detector.

## Lowering and tooling

Keep source type T distinct from storage strategy. Extend the existing typed
binding/initializer metadata with an async scope expression and scheduling kind.
Reuse lazy's checked initializer boundary, facts, capture accounting, lowering of
all reads, closure cell capture and generic memo storage. Evaluate the scope
expression once and start its native scope task with the initializer callback.
The memo's initializer waits on that task; do not run work through `memo.get`
in the task, because doing so would incorrectly observe unread panics.
Successful task completion and cached panics are published through the existing
memo accessor. No separate transparent-read subsystem is needed.

`bork describe` reports T, async classification, owning scope, initializer effects
and lexical captures without starting or awaiting anything. `dbg` explains that
it awaits. Formatting and editor syntax preserve the modifier. Grammar and user
docs distinguish proposed syntax from implemented syntax until it ships.
Immediate-read lazy warnings do not apply to async bindings: even a first read
can synchronize with other work already started.

Acceptance covers scope-expression evaluation exactly once before scheduling,
its effects and enclosing-boundary return/?, runtime predicates/validators
reading async values without compile-time scheduling, concurrent start before reads, read-once/repeated/concurrent
reads, closure and alias behavior, loop cells, annotation/inference/generics,
initializer-local return/?, constrained results, effects and ambient captures,
mock task context, unread work and unread panics at scope exit, repeated panic
reads, cancellation and scope policies. Negative cases cover a non-Scope owner,
missing effects, short captures, scalar cell/closure escape, reading after owner
closure, OwnedScope capture/results, illegal modifiers and control transfer,
and compile-time scheduling/forcing. Runtime concurrency tests run with the race
detector, and tooling fixtures prove describe never starts work. Panic fixtures prove
that unread failure cancels siblings and is raised/logged at close, a read marks
the native task reported, logFailures never suppresses a panic on read, and
task timeouts retain existing orphan behavior.
