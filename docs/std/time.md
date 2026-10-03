# bork/time

## Time and clocks

- **Time and configuration (implemented):** `bork/time` has immutable Unix-nanosecond instants, durations, formatting/parsing, checked arithmetic, scope-cancellable sleep, and a `Clock` whose `now` function tests can replace.

## API

`bork/time` exposes `Instant`, `Duration`, and `Clock` records, plus checked arithmetic, parsing/formatting, and scope-aware `Sleep`.

## Examples

`bork/time` provides instants, durations, formatting/parsing, cancellable sleep, and injectable clocks.

See [examples/time_env](../../examples/time_env/main.bork).
