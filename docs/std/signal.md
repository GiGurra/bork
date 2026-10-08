# bork/signal

`bork/signal` configures process shutdown and receives scope-owned signal notifications.

```bork
import "bork/signal"

fn main() {
  scope app {
    events = signal.MockSubscription(app)
    _ = events.Emit(.Hangup)
    println(events.Next())
    match (signal.Subscribe(app, [])) {
      error: signal.Error => println(error.message)
      _: signal.Subscription => println("subscribed")
    }
  }
}
```

```text
Signal.Hangup
Subscribe requires at least one signal
```

The mock injects an event without sending an OS signal. The empty subscription
shows a handled registration error.

## API

| Signature | Meaning |
| --- | --- |
| `Configure(s: Scope, cancel: List[Signal] = [.Interrupt, .Terminate], grace: Option[time.Duration] = .None) uses io + state: Policy \| Error` | Replace process cancellation policy while owned. |
| `Ignore(s: Scope, signals: List[Signal]) uses io + state: Policy \| Error` | Suppress signals unless a subscription handles them. |
| `Subscribe(s: Scope, signals: List[Signal]) uses io + state: Subscription \| Error` | Receive signals and suppress their cancellation and OS defaults. |
| `(events: Subscription) Next() uses io + state: Signal \| Cancelled \| Closed` | Wait for the next notification. |
| `MockSubscription(s: Scope) uses state: Subscription` | Create an isolated subscription without OS registration. |
| `(events: Subscription) Emit(value: Signal) uses state: Ok \| Closed \| Error` | Inject a mock notification; never send an OS signal. |

| Type | Fields or variants |
| --- | --- |
| `Signal` | `Interrupt`, `Terminate`, `Hangup`, `User1`, `User2` |
| `Error` | `message: String` |
| `Policy`, `Subscription` | Scope-owned resources with process-wide disposition. |

## Default shutdown

SIGINT (Ctrl+C) and SIGTERM cancel every root scope by default, including nested
tasks through their parents. Cleanup finishes before a normal return exits with
130 or 143. An explicit `process.ExitNow(code)` keeps its chosen code. Copies within
500 ms count as one cancellation; another cancelling signal after that window
terminates immediately. Other signals keep their Go/OS defaults, and there is no
default grace deadline. Three Ctrl+C presses within 5 seconds, each counted press at least 500 ms
after the previous one, always exit with
130, even if the program subscribes to or ignores `Interrupt`, so the keyboard
can stop a program even when Interrupt is subscribed to or ignored.
Programs that never open a scope install no bork signal handler.

Scope-aware waits and checkpoints observe cancellation. Pure work must reach an
explicit checkpoint to observe it. Scope exit cancels tasks, joins them and runs
resource cleanup; see [scopes and tasks](../language/scopes.md).

**A registration's scope owns its lifetime, not an isolated signal disposition.**
Signal disposition affects the whole process. Register policies in your
application scope, and let libraries receive subscriptions as appropriate.
Attaching a registration to another scope extends its lifetime until the last
owner closes.

## Cancellation and grace

```bork
import "bork/signal"
import "bork/time"

fn configure(app: Scope) uses io + state: signal.Policy | signal.Error {
  signal.Configure(app,
    cancel: [.Interrupt, .Terminate, .Hangup],
    grace: .Some(time.Nanoseconds(5_000_000_000)))
}

fn main() {
  scope app {
    match (configure(app)) {
      _: signal.Policy => println("shutdown configured")
      error: signal.Error => eprintln(error.message)
    }
  }
}
```

`Configure` replaces the cancellation set while its registration is owned.
The newest live registration wins; closing it restores the previous live
registration or the default. An empty list removes all cancellation signals;
signals without other registrations regain their OS behavior. Duplicate names
are accepted once.

`grace: .None` waits indefinitely for cleanup. A nonnegative `time.Duration`
starts at the first cancelling signal and forces exit with that signal's status
when it expires, even if tasks or finalizers are stuck. Zero expires immediately;
negative durations return `signal.Error`. Later configuration cannot undo
cancellation or extend a started grace deadline. Cancellation is terminal: root
scopes opened afterward inherit the same cause.

Configure, Ignore, Subscribe and Next use `io + state`: `io` already includes
process operations, and `state` covers shared registrations and notifications.
The grace policy does not require callers to read a clock, like scope cleanup
timeouts. Signal reasons name the signal, such as `Interrupt (interrupt)`.

## Receiving and ignoring

```bork
import "bork/signal"

fn reload(app: Scope) uses io + state: signal.Signal | signal.Error | Cancelled | Closed {
  events = signal.Subscribe(app, [.Hangup])?
  events.Next()
}

fn main() {
  println(scope app { reload(app) })
}
```

`Subscribe` prevents its signals from cancelling root scopes or invoking their
OS defaults. `Next()` waits for an event, or returns `Cancelled` when its scope
is cancelled and `Closed` when the subscription closes. All subscribers receive
each event independently. Each has a 16-event buffer; a full buffer drops
further notifications. OS signals can also coalesce, so do not use them as a
counted message queue.

`Ignore(app, [.Hangup])` discards signals while its registration is owned.
Subscriptions take precedence over ignores, which take precedence over
cancellation. Closing the last matching registration restores the remaining
policy or previous OS behavior. Cancellation keeps registrations active during
cleanup, so a subscription can still receive a signal while other scopes shut
down. Empty Subscribe and Ignore lists return `signal.Error`.

The available `Signal` variants are `Interrupt` (SIGINT), `Terminate` (SIGTERM),
`Hangup` (SIGHUP), `User1` (SIGUSR1) and `User2` (SIGUSR2). Unsupported platform
signals return `signal.Error`. Uncatchable and runtime fault signals are absent.

## Tests without OS signals

Mock the application's call to Subscribe using the existing function-mocking
facility. `MockSubscription(s)` creates an isolated subscription with no OS
registration; `Emit` puts an event into its normal receiving path. It never
cancels real root scopes or sends an OS signal. It supports every Signal variant
on every platform. Calling Emit on a real subscription returns `signal.Error`.

```bork
import "bork/signal"

fn reload(app: Scope) uses io + state: signal.Signal | signal.Error | Cancelled | Closed {
  events = signal.Subscribe(app, [.Hangup])?
  events.Next()
}

test "reload receives a notification" {
  scope app {
    calls = mock signal.Subscribe(s, signals) {
      events = signal.MockSubscription(s)
      fork(s, () => { _ = events.Emit(.Hangup) })
      events
    }
    assertEqual(reload(app), signal.Signal.Hangup)
    calls.expect(times: 1)
  }
}
```

The mock must create the subscription in its caller's scope; returning one
captured from the test scope fails lifetime checking. For later injection, a
task of the caller's scope can wait on a captured atom before calling Emit.

## More

See the [signal design](../design/signals.md) for policy rationale.

## Windows

Ctrl+C and Ctrl+Break both become Interrupt and normally exit with 130 after
cleanup. Console close, logoff and shutdown can produce Terminate notifications
and a normal-return status of 143, but Windows retains its own deadline for
ending the process. A grace duration does not extend that deadline. Hangup,
User1 and User2 are unavailable for real registrations on Windows. These
mappings follow [Go's os/signal behavior](https://pkg.go.dev/os/signal#hdr-Windows).

Run `bork doc bork/signal` for the generated reference.

[All standard packages](README.md)
