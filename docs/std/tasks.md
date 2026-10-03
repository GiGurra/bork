# bork/tasks

`tasks.Open(s, maxTasks: n)` creates a scope-owned `Pool` with a positive
capacity. `tasks.ValidLimit(n)` proves a dynamic limit. Share one pool across
request scopes to bound their combined task activity. A slot remains occupied
until its callback finishes or panics, even if that callback ignores cancellation.

`tasks.TrySpawn(pool, s, work)` gives `Task[T] | tasks.TaskLimitReached | Cancelled`.
`tasks.TryLaunch(pool, s, work)` gives `Ok | tasks.TaskLimitReached | Cancelled`.
Both charge `state` plus the callback's effects. `TrySpawn` needs a value result;
use `TryLaunch` for an Ok callback. Saturation returns `TaskLimitReached { limit }`
at once. A cancelled or closing task scope, or a closed pool, gives `Cancelled`.
Rejected callbacks never run. Admission racing cancellation may succeed, in
which case the callback can observe that cancellation.

The explicit scope owns and joins the task. The pool and captured resources
must live as long as that scope; the compiler checks this. Pool attachment
extends the pool's ownership, and does not move tasks already started. Awaiting
a task preserves normal panic reporting; an unawaited task panic is reported at
its scope's end. A callback panic releases its slot and cancels its task scope.
Pool shutdown disables further submissions; existing callbacks retain their
slots until they finish.

Submission has no hidden queue and never waits for capacity. A callback using
the last slot can submit a child and receive TaskLimitReached without deadlocking
on admission. It must handle that failure or compute the child directly. Use a
bounded channel and explicit workers when producers should wait. Ordinary
`spawn`/`launch` and parallel collection methods keep their existing APIs and do
not implicitly consume a pool; limits apply only to chosen pool submissions.

See [the runnable example](../../examples/task_pool/main.bork), and the
[backpressure design](../design/backpressure.md) for the HTTP admission and retry
budget follow-ups.
