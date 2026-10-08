# bork/tasks

`bork/tasks` bounds active tasks across scopes with explicit, immediate admission.

```bork
import "bork/tasks"

fn main() {
  scope app {
    pool = tasks.Open(app, maxTasks: 1)
    gate = channel[Bool](app, 0)
    match tasks.TryFork(pool, app, () => { _ = gate.receive(app); "done" }) {
      task: Task[String] => {
        match tasks.TryFork(pool, app, () => "second") {
          error: tasks.TaskLimitReached => println(s"full: ${error.limit}")
          other => println(other)
        }
        _ = gate.send(app, true)
        println(await(task))
      }
      error: tasks.TaskLimitReached | Cancelled => println(error)
    }
  }
}
```

```text
full: 1
done
```

The first task holds its slot until the gate opens, so the second submission
fails immediately and never calls its callback.

## API

| Signature | Meaning |
| --- | --- |
| `Open(s: Scope, maxTasks: Limit) uses state: Pool` | Create a scope-owned pool with a positive capacity. |
| `TryFork[T](pool: Pool, s: Scope, work: () => T) uses state: Task[T] \| TaskLimitReached \| Cancelled` | Start a task if a slot is available; never wait for capacity. |

| Type or predicate | Meaning |
| --- | --- |
| `Pool` | A scope-owned shared capacity resource. |
| `Limit = Int where ValidLimit` | A strictly positive task limit. |
| `ValidLimit(limit: Int): Bool` | Prove a dynamic limit is positive. |
| `TaskLimitReached { limit: Int }` | Immediate saturation failure. |

`TryFork` charges `state` plus its callback's effects. Its explicit scope owns
and joins the task; the pool and captured resources must live at least as long
as that scope, which the compiler checks.

## Validate a configured limit

```bork
import "bork/tasks"

fn openPool(s: Scope, limit: Int) uses state: tasks.Pool | OutOfRange {
  if tasks.ValidLimit(limit) {
    tasks.Open(s, limit)
  } else {
    OutOfRange { value: toString(limit), target: "positive task limit" }
  }
}

fn main() {
  println(scope app {
    match openPool(app, 0) {
      _: OutOfRange => "invalid limit"
      _: tasks.Pool => "ready"
    }
  })
}
```

Share one pool across request scopes to bound their combined activity. A slot
remains occupied until its callback finishes or panics, even when that callback
ignores cancellation. Pool shutdown disables new submissions; existing callbacks
retain their slots until they finish. Attaching the pool extends its ownership
without changing the owners of tasks already started.

## Handle cancellation and work without a result

```bork
import "bork/tasks"

fn main() {
  scope app {
    pool = tasks.Open(app, 1)
    cancel(app)
    match tasks.TryFork(pool, app, () => println("working")) {
      _: Task[Ok] => println("admitted")
      error: tasks.TaskLimitReached => println(s"full: ${error.limit}")
      _: Cancelled => println("cancelled before admission")
    }
  }
}
```

```text
cancelled before admission
```

A cancelled or closing task scope, or a closed pool, gives `Cancelled`. Admission
racing cancellation can succeed; the admitted callback then observes cancellation
at its cancellation points. Rejected callbacks never run.

Work without a value gives `Task[Ok]`, which may be dropped after handling the
failure variants. Scope exit cancels then joins admitted tasks and reports
unawaited panics; await tasks inside the scope when completion is required.
A callback panic releases its slot and cancels its task scope.
See [scopes and tasks](../language/scopes.md).

## Choose backpressure deliberately

Submission has no hidden queue. A callback holding the last slot can submit a
child and get `TaskLimitReached` without deadlocking on admission; handle that
failure or compute the child directly. Use a bounded channel and explicit
workers when producers should wait for capacity. Ordinary `fork` and parallel
collection methods do not consume pool slots.

See the [task pool example](../../examples/task_pool/main.bork) and the
[backpressure design](../design/backpressure.md).

Run `bork doc bork/tasks` for the generated reference.

[All standard packages](README.md)
