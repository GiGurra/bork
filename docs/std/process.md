# bork/process

`bork/process` runs argv commands without a shell. `Run` waits for the child
and gives its Result; `Start` gives a scope-owned `Process` to wait on, signal
or stop later. Each of stdin, stdout and stderr is configured on its own. A
nonzero exit or a death by signal is data in the Result, not an error.
`Args()` returns the command-line arguments without the program name, and
`Exit(code)` terminates with that status without closing scopes.

## Running a command

```bork
import "bork/process"

fn status(s: Scope) uses io + state: Ok | IoError | Cancelled {
  result = process.Run(s, "git", ["status", "--short"])?
  println(result.code)
  println(result.StdoutText())
}
```

`Run(scope, name, arguments = [], environment = Option.None, directory = "",
stdin = .Empty, stdout = .Capture, stderr = .Capture, cancelGrace = 0)` gives
`Result | IoError | Cancelled`. None environment inherits the parent
environment, `Some([])` clears it, and `Some(values)` supplies `KEY=value`
entries. An empty directory inherits the current directory. `IoError` means
the child could not be started (its path names the executable or the file that
could not be opened) or could not be waited for. `Cancelled` means the owning
scope was cancelled.

## Streams

Each stream takes its own value, so any mix works:

| Argument | Variants | Default |
| --- | --- | --- |
| `stdin: Input` | `Empty` (/dev/null), `Inherit`, `Data { data }`, `Text { text }`, `File { path }` | `.Empty` |
| `stdout: Output` | `Capture`, `Inherit`, `Discard`, `File { path }` (truncates), `Append { path }` | `.Capture` |
| `stderr: ErrorOutput` | the Output variants, plus `Stdout` (2>&1) | `.Capture` |

`Capture` keeps the stream in memory in `Result.stdout` or `Result.stderr`;
`Inherit` forwards it to the parent's own stream; `Discard` drops it.
`stderr: .Stdout` sends stderr wherever stdout goes. With capture, both land in
`Result.stdout` in the order the child wrote them, because the child gets a
single pipe, and `Result.stderr` is empty.

```bork
import "bork/process"

fn build(s: Scope) uses io + state: Ok | IoError | Cancelled {
  // Show errors live, keep normal output quiet.
  _ = process.Run(s, "make", stdout: .Discard, stderr: .Inherit)?
  // One transcript of both streams, in order.
  log = process.Run(s, "make", ["test"], stderr: .Stdout)?
  println(log.StdoutText())
  // Feed input and forward everything.
  _ = process.Run(s, "sort", stdin: .Text { text: "b\na\n" }, stdout: .Inherit, stderr: .Inherit)?
}
```

Files are opened before the child starts; failing to open one is an IoError
naming that path, and nothing runs. Captured output drains in the background,
so a chatty child never blocks on a full pipe, even if nobody waits for it.

## Exit status

`Result` is `{ code, status, stdout, stderr }`. `status` is an `ExitStatus`:
`Exited { code }`, or `Signaled { number, name }` when a signal killed the
child (`name` is the conventional name, such as `"SIGKILL"`; Unix only).
`code` is the exit code, or -1 after a signal. `Success()` is `code == 0`.
`StdoutText()` and `StderrText()` give the bytes as a String; use
`encoding.ParseUtf8` when they must be validated.

`Check()` turns a failed Result into an `ExitError { status, stdout, stderr,
message }`, like Python's `check=True`. The message names the exit and ends
with the last line of captured stderr, such as `exit status 128: fatal: not a
git repository`.

```bork
import "bork/process"

fn head(s: Scope) uses io + state: String | IoError | Cancelled | process.ExitError {
  process.Run(s, "git", ["rev-parse", "HEAD"])?.Check()?.StdoutText().trim()
}
```

## Timeouts and cancellation

A timeout is a scope with a deadline. When it passes, the child's process group
is killed and Run gives `Cancelled` with the scope's reason:

```bork
import "bork/process"

fn bounded(s: Scope) uses io + state + clock: process.Result | IoError | Cancelled {
  scope limited {
    cancelAfter(limited, 2000)
    process.Run(limited, "slow-tool")
  }
}
```

By default cancellation kills at once. A positive `cancelGrace` (a
`time.Duration`) sends SIGTERM first and SIGKILL only if the child is still
running after that long, so it can clean up. Scope cleanup then waits at most
the grace period plus one second.

## Processes

`Start` takes the same arguments and gives a `Process` owned by the scope:

- `Wait()` waits and gives `Result | IoError | Cancelled`. It can be called
  repeatedly and always gives the same result.
- `TryWait()` does not block: `None` while the child runs.
- `Pid()` is the process ID.
- `Signal(signal)` sends a `bork/signal` Signal (`.Interrupt`, `.Terminate`,
  `.Hangup`, `.User1`, `.User2`) to the child's process group. It does nothing
  after the child has finished; unsupported signals give IoError.
- `Stop(grace = 5s)` ends the child gracefully: SIGTERM to its group, then
  SIGKILL after `grace`. `Kill()` sends SIGKILL at once. Neither cancels the
  scope; `Wait` gives the child's Result, such as
  `Signaled { number: 15, name: "SIGTERM" }`, or the code the child chose when
  it handled SIGTERM.

```bork
import "bork/process"

fn server(s: Scope) uses io + state: Ok | IoError | Cancelled {
  child = process.Start(s, "my-server", ["--port", "8080"], stdout: .Inherit, stderr: .Inherit)?
  println(child.Pid())
  child.Stop()
  println(child.Wait()?.status)
}
```

Closing or cancelling the owning scope kills and reaps the child even if Wait
was never called. `attach` moves cancellation to the destination scope.
Process operations declare `io + state`; Pid reads a stored value without
effects. `Args` and `Exit` declare `io`.

## Process groups

Each child starts a new Unix process group. Cancellation, Stop, Kill and Signal
reach the whole group, including shell-wrapper descendants. Descendants that
create another group or session are outside its ownership. Group signals apply
while the direct child is running: descendants that outlive its normal exit are
not killed by later cancellation or scope cleanup. Other targets act only on
the direct child, and Stop kills at once there.

Run waits at most one second (plus `cancelGrace`) for inherited output pipes
after the child exits or is cancelled. If a descendant keeps them open after a
successful exit, the result is IoError rather than partial output. Nonzero
exits keep their Result and output may be truncated at this bound. This bound
keeps capture from blocking cleanup forever.

## Shutdown signals

SIGINT and SIGTERM cancel root scopes; nested scopes inherit cancellation.
Scope-aware waits and checkpoints observe it, and cleanup runs as scopes end,
which also stops their children. Code that does not reach a cancellation point
continues running. See [bork/signal](signal.md) to change this policy.

See [the process example](../../examples/process/main.bork).

[All standard packages](README.md)
