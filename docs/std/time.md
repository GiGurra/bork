# bork/time

## Time and clocks

- **Time and configuration (implemented):** `bork/time` has immutable Unix-nanosecond instants, durations, formatting/parsing, checked arithmetic, scope-cancellable sleep, and a `Clock` whose `now` function tests can replace.

## API

`bork/time` exposes `Instant`, `Duration`, and `Clock` records, plus checked arithmetic, parsing/formatting, scope-aware `Sleep`, and the timer channels `After` and `Tick`.

## Timer channels

Timeouts and periodic events are ordinary [channels](../language/scopes.md#channels), as in Go:

- `time.After(s, d)` gives a `Channel[Instant]` of scope `s` that receives the instant `d` has passed, once, and is then closed. A deadline made once therefore stays expired: every later receive gives `Closed` at once. A nonpositive `d` fires at once.
- `time.Tick(s, every)` gives a `Channel[Instant]` that receives an instant every period, keeping to the cadence. It buffers one tick: a tick that finds the last one not yet received is dropped, so a slow receiver gets the latest rather than a backlog. The period must be positive; `Tick` panics otherwise, as Go's `NewTicker` does.

Neither leaks a timer: closing the channel, or the end of its scope, stops it. They are ordinary channels, so a [`select`](../language/channels.md#select) can wait for one alongside others.

```bork
import "bork/time"

fn main() {
  scope s {
    ticks = time.Tick(s, time.Nanoseconds(1_000_000))
    deadline = time.After(s, time.Nanoseconds(5_000_000))
    match (ticks.receive(s)) {
      _: time.Instant => println("tick")
      other => println(other)
    }
    _ = deadline.receive(s)
    println(deadline.receive(s))
  }
}
```

A deadline for a whole piece of work belongs on its scope instead (`withTimeout(s, ms, c => ...)` or `cancelAfter(s, ms)`): every operation waiting in that scope then gives `Cancelled` on its own. `After` and `Tick` are for timing that is part of the conversation, such as waiting a while for a reply, or doing something every second.

## Examples

`bork/time` provides instants, durations, formatting/parsing, cancellable sleep, and injectable clocks.

See [examples/time_env](../../examples/time_env/main.bork).

`cancelAfter(s, ms)` records the earliest scope deadline, visible to HTTP and
Go-context consumers. Repeating it cannot extend the deadline, and existing
children observe later deadlines added to an ancestor. External context values
and deadline limits are retained. Zero or negative milliseconds cancel now;
large positive values saturate safely. Deadline cancellation uses the standard
`context.DeadlineExceeded` cause, with text "context deadline exceeded".
