# Process signals

Status: approved for bork-pd656h.

## Default

A program that uses scopes installs a process signal broker. SIGINT and
SIGTERM cancel every root scope with a cause naming the signal, propagated to
nested scopes and tasks through ordinary cancellation. The first cancelling
signal fixes the normal-return exit status at `128 + signal number` (130 for
SIGINT, 143 for SIGTERM). Cleanup finishes before that exit. An explicit
`process.Exit(code)` still exits with exactly that code; a panic keeps its
ordinary failure behavior.

Copies received within 500 ms of the first cancelling signal count as the same
request. A subsequent cancelling signal terminates immediately without waiting
for cleanup. Default cancellation signals return to their previous OS behavior
after the duplicate window, preserving #331's second-interrupt escape hatch.
Signals with an explicit subscription or ignore registration remain handled.
Configured user signals also remain handled for escalation, because their
Go/OS defaults do not terminate the process.
No default grace deadline is imposed. Other signals retain their Go/OS defaults,
including SIGHUP. Programs without scopes install no bork signal handler.

## API and effects

Import `Duration` from `bork/time`. Add `bork/signal`, with no syntax or prelude additions. Proposed signatures:

```text
type Signal = sealed { Interrupt, Terminate, Hangup, User1, User2 }
type Error = { message: String }
type Policy = resource
type Subscription = resource

fn Configure(s: Scope, cancel: List[Signal] = [.Interrupt, .Terminate],
             grace: Option[Duration] = .None) uses io + state: Policy | Error
fn Ignore(s: Scope, signals: List[Signal]) uses io + state: Policy | Error
fn Subscribe(s: Scope, signals: List[Signal]) uses io + state: Subscription | Error
fn (events: Subscription) Next() uses io + state: Signal | Cancelled | Closed

fn MockSubscription(s: Scope) uses state: Subscription
fn (events: Subscription) Emit(value: Signal) uses state: Ok | Closed | Error
```

`io` already covers process operations (`bork/process`); `state` covers shared
registrations and receiving events. No new effect is needed. `grace` describes
a shutdown policy rather than a caller reading a clock, like scope cleanup
timeouts, so Configure does not require `clock`. MockSubscription subscriptions need no OS
access; their `Next` signature remains the same as real subscriptions so callers
can use the existing function-mocking facility when isolating `io`.

Names represent supported asynchronous signals, not arbitrary platform numbers.
Unsupported signals return Error; SIGKILL, SIGSTOP and runtime fault signals are
deliberately absent. Empty cancellation lists are valid and restore ordinary OS
behavior for signals without subscriptions or ignore registrations. Empty
Subscribe/Ignore lists are errors. Duplicate names are deduplicated. Invalid
negative durations return Error; zero means immediate forced termination,
and positive durations use their nanosecond value without conversion overflow.

## Ownership and process-wide rules

OS signal disposition is process-wide. The scope argument owns a registration's
lifetime, **not** an isolated OS disposition for that subtree. This distinction
will be prominent in the std and scopes documentation.

Configure temporarily replaces the process cancellation set and grace policy.
The newest live Configure registration wins; closing it restores the newest
remaining registration, or the default. Removing an older registration never
overwrites a newer one. Libraries should normally leave Configure to main.

For each signal, an active Subscribe wins over Ignore, which wins over the
cancellation policy. All active subscriptions to that signal receive the event;
it does not cancel any root scope or set a shutdown exit status. Ignore discards
the signal while its owner is live. Closing the last relevant registration
restores the remaining policy or the previous OS behavior. Registrations are
removed by scope finalizers; cancellation itself does not remove them before
cleanup. Subscription reads stop on owner cancellation or closure. Each
subscriber has a bounded buffer (16 events); full buffers coalesce/drop further
events, because OS signals are notifications rather than a counted queue.

Cancellation latches once for the process, as the current shared root context
does: later root scopes inherit its cause. Grace starts at that first cancelling
signal, using the effective policy at that moment. Later policy changes cannot
undo cancellation or extend the deadline. Its expiry exits with the first
signal's status even if work or finalizers remain; normal process exit makes
the timer moot. Explicit subscriptions still receive their signals while other
signals are shutting the program down.

MockSubscription creates an isolated subscription owned by its scope, without altering OS
registrations or root cancellation. `Emit` accepts only mock subscriptions,
returns Error for real subscriptions, and never sends a real OS signal. It
injects events into the same receiving path, so application reload loops can be
tested without affecting parallel tests. Mock the application's call to
`signal.Subscribe` to create a subscription in its caller scope, emit from a
task of that scope, then return it; document
and compile a worked example of that pattern. Broker tests separately inject fake
signal delivery, clock and termination hooks to test cancellation and escalation
without killing the test process.

## Windows

Interrupt means Ctrl+C **and** Ctrl+Break; both become SIGINT and normally
produce 130 after cleanup. Terminate means the SIGTERM notification Go produces
for console close, logoff and shutdown, with a normal-return status of 143.
Windows still imposes its own termination deadline for those notifications;
grace does not extend it. Hangup/User1/User2 return Error on Windows. Unix names
map to the target's signal numbers, never Linux numbers hardcoded for all OSes.
These mappings follow [Go's os/signal documentation](https://pkg.go.dev/os/signal#hdr-Windows).

## Implementation and tests

Keep the broker in generated scope runtime, alongside _newScope; std functions
use small compiler-owned helpers to register policies and receive events.
Default initialization remains conditional on generated scope use. Add the
signal exit-status check only after successful completion of generated main,
so explicit exits and panics keep their existing semantics. Test-program main
uses its test runner's status; fake broker tests do not latch real process exit.

Focused tests cover default causes and all roots, duplicate suppression and
second-signal escalation, grace expiry, policy precedence and restoration,
Subscribe/Ignore precedence and cleanup, buffer behavior, validation, mock
events, and unsupported platforms. CLI process tests from #331 expect 130/143
after graceful return and verify explicit exit overrides, a second interrupt,
and no-scope defaults. Coordinate their PID helper with runperf's Unix exec
handoff. Cross-compile generated examples for Windows and Darwin to catch
platform constants; do not send Unix signals in Windows tests.

Update docs/language/scopes.md, docs/language/effects.md, docs/std/signal.md,
docs/README.md, docs/requirements.md and README.md to describe the behavior and
link the new module. Regenerate std validators if required. Run focused tests,
golangci-lint and formatting locally; full suites run in GitHub CI.
