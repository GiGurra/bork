# bork/time

`bork/time` provides instants, durations, injectable clocks, and cancellable timers.

```bork
import "bork/time"

fn main() {
  clock = time.FixedClock(time.Instant { unixNanos: 0 })
  println(time.Format(time.Read(clock), time.RFC3339()))
  println(time.FormatDuration(time.Nanoseconds(1_500_000_000)))
  match (time.ParseDuration("oops")) {
    _: ParseError => println("invalid duration")
    value: time.Duration => println(value)
  }
}
```

```text
1970-01-01T00:00:00Z
1.5s
invalid duration
```

## API

| Signature | Meaning |
| --- | --- |
| `Now() uses clock: Instant` | Read the system clock. |
| `SystemClock(): Clock` | Build a clock backed by Now. |
| `FixedClock(instant: Instant): Clock` | Build a clock that always returns one instant. |
| `Read(clock: Clock) uses clock: Instant` | Read the supplied clock. |
| `Nanoseconds(n: Int): Duration` | Construct a duration directly. |
| `Milliseconds(n: Int): Duration \| OutOfRange` | Convert milliseconds with overflow checking. |
| `ParseDuration(text: String): Duration \| ParseError` | Parse a duration such as "1.5s". |
| `FormatDuration(duration: Duration): String` | Format a duration. |
| `RFC3339(): String` | Return the RFC3339 layout with optional nanoseconds. |
| `Format(instant: Instant, layout: String): String` | Format an instant using a Go time layout. |
| `Parse(text: String, layout: String): Instant \| ParseError` | Parse an instant, rejecting values outside its range. |
| `Add(instant: Instant, duration: Duration): Instant \| OutOfRange` | Add a duration with overflow checking. |
| `Between(start: Instant, end: Instant): Duration \| OutOfRange` | Compute end minus start with overflow checking. |
| `Sleep(s: Scope, duration: Duration) uses clock + state: Ok \| Cancelled` | Wait until elapsed or cancelled. |
| `After(s: Scope, d: Duration) uses clock + state: Channel[Instant]` | Create a one-shot timer channel. |
| `Tick(s: Scope, every: Duration) uses clock + state: Channel[Instant]` | Create a periodic timer channel. |

| Record | Fields |
| --- | --- |
| `Instant` | `unixNanos: Int` |
| `Duration` | `nanos: Int` |
| `Clock` | `now: () uses clock => Instant` |

Instants are signed Unix nanoseconds, covering roughly 1677-09-21 through
2262-04-11. Durations are signed nanoseconds. `Milliseconds`, `Add`, and
`Between` give `OutOfRange` instead of wrapping. Formatting layouts use Go's
reference instant, such as `"2006-01-02"`; `RFC3339()` supplies a common layout.

## Inject a clock

```bork
import "bork/time"

fn stamp(clock: time.Clock) uses clock: String {
  time.Format(time.Read(clock), time.RFC3339())
}

test "fixed clock" {
  clock = time.FixedClock(time.Instant { unixNanos: 0 })
  assertEqual(stamp(clock), "1970-01-01T00:00:00Z")
}

fn main() {
  println(stamp(time.FixedClock(time.Instant { unixNanos: 0 })))
}
```

Pass `SystemClock()` in production and `FixedClock(...)` in tests. A fixed clock
controls reads through `Read`; it does not change `Sleep`, `After`, or `Tick`.

## Sleep and scope deadlines

```bork
import "bork/time"

fn main() {
  scope app {
    cancel(app)
    println(time.Sleep(app, time.Nanoseconds(1_000_000)))
  }
}
```

`Sleep` returns `Ok` when elapsed and `Cancelled` when its scope is cancelled.
Nonpositive durations finish immediately. For a deadline across several
operations, use `withTimeout` or `cancelAfter` on their scope; every cancellable
operation then observes the same cancellation.

`cancelAfter(s, ms)` records the earliest scope deadline, also visible to HTTP
and Go-context consumers. Repeating it cannot extend it; existing children
observe deadlines later added to ancestors. External context values and
limits are retained. Zero or negative milliseconds cancel immediately; large
positive values saturate safely. The deadline cause is
`context.DeadlineExceeded`, with reason `"context deadline exceeded"`.
See [cancellation and timeouts](../language/scopes.md#cancellation-and-timeouts).

## Timer channels

```bork
import "bork/time"

fn main() {
  scope app {
    ticks = time.Tick(app, time.Nanoseconds(1_000_000))
    deadline = time.After(app, time.Nanoseconds(5_000_000))
    match (ticks.receive(app)) {
      _: time.Instant => println("tick")
      other => println(other)
    }
    _ = deadline.receive(app)
    println(deadline.receive(app))
    ticks.close()
  }
}
```

```text
tick
Closed {}
```

`After` sends one instant and closes its channel; later receives return `Closed`.
A nonpositive duration fires immediately. `Tick` follows its period with a
one-element buffer: it keeps the **earliest pending tick** and drops subsequent
ticks while that buffer is full. A slow receiver gets that pending timestamp,
with no backlog. Its period must be positive; otherwise `Tick` panics.

Closing a timer channel or ending its scope stops its timer. Use these ordinary
[channels](../language/channels.md) with
[`select`](../language/channels.md#select) when timing is one event among others.
See the [time and environment example](../../examples/time_env/main.bork).

Run `bork doc bork/time` for the generated reference.

[All standard packages](README.md)
