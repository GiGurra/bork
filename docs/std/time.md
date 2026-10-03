# bork/time

## Time and clocks

- **Time and configuration (implemented):** `bork/time` has immutable Unix-nanosecond instants, durations, formatting/parsing, checked arithmetic, scope-cancellable sleep, and a `Clock` whose `now` function tests can replace.

## API

`bork/time` exposes `Instant`, `Duration`, and `Clock` records, plus checked arithmetic, parsing/formatting, and scope-aware `Sleep`.

## Examples

`bork/time` provides instants, durations, formatting/parsing, cancellable sleep, and injectable clocks.

See [examples/time_env](../../examples/time_env/main.bork).

`cancelAfter(s, ms)` records the earliest scope deadline, visible to HTTP and
Go-context consumers. Repeating it cannot extend the deadline, and existing
children observe later deadlines added to an ancestor. External context values
and deadline limits are retained. Zero or negative milliseconds cancel now;
large positive values saturate safely. Deadline cancellation uses the standard
`context.DeadlineExceeded` cause, with text "context deadline exceeded".
