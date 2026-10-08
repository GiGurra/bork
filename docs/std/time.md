# bork/time

`bork/time` provides instants, durations, injectable clocks, and cancellable timers.

```bork
import "bork/time"

fn main() {
  clock = time.FixedClock(time.Instant { unixNanos: 0 })
  println(time.Format(time.Read(clock), time.RFC3339()))
  println(time.FormatDuration(1_500_000_000.nanos()))
  match (time.ParseDuration("oops")) {
    _: ParseError => println("invalid duration")
    value: Duration => println(value)
  }
}
```

```text
1970-01-01T00:00:00Z
1.5s
invalid duration
```

## Duration

`Duration` is in the prelude; no import is needed. `time.Duration` names the
same type. Build spans with `250.millis()`, `5.seconds()`, or `2.minutes()`.
The methods `nanos()`, `micros()`, `millis()`, `seconds()`, `minutes()`, and
`hours()` work on every `Int`. Use `0.seconds()` for zero. Negative values
represent negative spans. Constructors saturate at the signed 64-bit
nanosecond limits, about 292 years in either direction, and never return an
error union.

`duration.add(other)`, `subtract(other)`, and `multiply(factor: Int)` also
saturate. Equality compares nanoseconds; `Ord` supports sorting and `Show`
renders readable text such as `1.5s` in nested and generic values too.
Use `.nanos` when you need the underlying integer. HTTP and network timeouts
require nonnegative durations, and retry refill intervals must be positive;
guard a dynamic span with `http.ValidTimeout(duration)`, `net.ValidTimeout`,
or `http.ValidRefill` as appropriate. Literal unit constructors are folded by
the compiler, so `timeout: 5.seconds()` satisfies these facts directly.

All prelude and standard timeout, delay, and grace parameters take Duration.
Defaults in declarations use closed record values, such as
`Duration { nanos: 0 }`; a method call is not a closed declaration default.

Old millisecond calls such as `delay(s, 5000)` and `timeoutMs: 5000` become
`delay(s, 5.seconds())` and `timeout: 5.seconds()`. `bork check --json` offers
edits for these arguments and the removed `time.Nanoseconds(n)` and
`time.Milliseconds(n)` constructors. The latter now saturates via `n.millis()`.

## API

| Signature | Meaning |
| --- | --- |
| `Now() uses clock: Instant` | Read the system clock. |
| `SystemClock(): Clock` | Build a clock backed by Now. |
| `FixedClock(instant: Instant): Clock` | Build a clock that always returns one instant. |
| `Read(clock: Clock) uses clock: Instant` | Read the supplied clock. |
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
2262-04-11. Durations are signed nanoseconds. `Add` and
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
    println(time.Sleep(app, 1_000_000.nanos()))
  }
}
```

`Sleep` returns `Ok` when elapsed and `Cancelled` when its scope is cancelled.
Nonpositive durations finish immediately. For a deadline across several
operations, use `withTimeout` or `cancelAfter` on their scope; every cancellable
operation then observes the same cancellation.

`cancelAfter(s, duration)` records the earliest scope deadline, also visible to HTTP
and Go-context consumers. Repeating it cannot extend it; existing children
observe deadlines later added to ancestors. External context values and
limits are retained. Zero or negative durations cancel immediately. Unit constructors saturate
at the signed nanosecond limits. The deadline cause is
`context.DeadlineExceeded`, with reason `"context deadline exceeded"`.
See [cancellation and timeouts](../language/scopes.md#cancellation-and-timeouts).

## Timer channels

```bork
import "bork/time"

fn main() {
  scope app {
    ticks = time.Tick(app, 1_000_000.nanos())
    deadline = time.After(app, 5_000_000.nanos())
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
