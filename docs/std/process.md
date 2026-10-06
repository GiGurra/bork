# bork/process

`bork/process` runs argv commands without a shell. `Run` waits for the child
and gives its Result; `Start` gives a scope-owned `Process` to stream from,
write to, wait on, signal or stop later. Each of stdin, stdout and stderr is configured on its own. A
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
| `stdin: Input` | `Empty` (/dev/null), `Inherit`, `Data { data }`, `Text { text }`, `File { path }`, `Pipe`, `From { reader }` | `.Empty` |
| `stdout: Output` | `Capture`, `Inherit`, `Discard`, `File { path }` (truncates), `Append { path }`, `Pipe` | `.Capture` |
| `stderr: ErrorOutput` | the Output variants, plus `Stdout` (2>&1) | `.Capture` |

`Capture` keeps the stream in memory in `Result.stdout` or `Result.stderr`;
`Inherit` forwards it to the parent's own stream; `Discard` drops it. `Pipe`
streams it through the Process while it runs (see [Streaming](#streaming));
Run has no Process, so there a piped stdin is empty and piped output is
captured.
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
naming that path, and nothing runs. To send both streams to one file, use
`stderr: .Stdout` rather than the same path twice, which opens two handles
that overwrite each other. Captured output drains in the background, so a
chatty child never blocks on a full pipe, even if nobody waits for it. It is
kept in memory without a limit, so give long-running children `.Discard`,
`.File` or `.Inherit`.

## Streaming

A `.Pipe` stream is read or written while the child runs. `child.Stdout()` and
`child.Stderr()` give a `Reader`; `child.Stdin()` gives a `Writer`:

```bork
import "bork/process"

fn follow(s: Scope) uses io + state: Ok | IoError | Cancelled {
  child = process.Start(s, "ping", ["-c", "3", "localhost"], stdout: .Pipe)?
  for (item in child.Stdout().Lines()) {
    match (item) {
      line: String => println(line)
      error: IoError => eprintln(error.message)
      stopped: Cancelled => eprintln(stopped.reason)
    }
  }
  println(child.Wait()?.code)
}

fn ask(s: Scope) uses io + state: Ok | IoError | Cancelled | Closed {
  echo = process.Start(s, "cat", stdin: .Pipe, stdout: .Pipe)?
  echo.Stdin().WriteText("ping\n")?
  println(echo.Stdout().NextLine())
  echo.Stdin().Close()
  _ = echo.Wait()?
}
```

Reader methods:

- `NextLine()` gives the next line without `\n` or `\r\n`, a final
  unterminated line, then `Closed` at the end of the stream.
- `NextChunk(limit = 32768)` gives the bytes available now, up to `limit`.
- `ReadAll()` reads to the end.
- `Lines()` is a `Seq` of lines for `for` loops; `Cancelled` or `IoError` end it
  as a final element.
- `LinesChannel(s, capacity = 64)` pumps lines from a task of `s` into a
  bounded channel and closes it at the end, so process output can be received
  together with other channels. A read failure is the last element. The
  Reader is attached to `s`, so when `s` ends or is cancelled the Reader is
  closed too; use the scope that should own it.

Writer methods: `Write(bytes)` and `WriteText(text)` give `Ok`, or `Closed`
once stdin is closed or the child stopped reading; `Close()` sends end of
input and can be repeated. Close stdin when you are done: a child that reads
to the end waits for it, and `Wait` does not close it for you, so another task
can still be writing. Reads and writes wait for the child and give
`Cancelled` when the scope is cancelled.

`Stdout()` on a stream that is not `.Pipe` gives a Reader that is already at
its end (`Closed`), and `Stdin()` on a stdin that is not `.Pipe` gives a Writer
whose writes give `Closed`. Every call gives the same Reader or Writer.

A Reader releases its pipe at the end of the stream, and piped stdin is
closed once the child exits. A pipe that is never read to the end stays open
until the scope ends. One task reads a Reader at a time: a second read, or
handing it to `From`, waits for the first to return.

A pipe holds only a small OS buffer, so a child writing to a pipe nobody reads
waits. Read each piped stream (in a task when there are two), use
`stderr: .Stdout` for one combined stream, or use `.Capture`, which always
drains. Cancelling the scope still stops the child and closes its pipes.

```bork
import "bork/process"

fn both(s: Scope) uses io + state: Ok | IoError | Cancelled {
  child = process.Start(s, "make", stdout: .Pipe, stderr: .Pipe)?
  errors = fork(s, () => child.Stderr().ReadAll())
  output = child.Stdout().ReadAll()?
  println(output.length())
  println(await(errors)?.length())
  _ = child.Wait()?
}
```

## Pipelines

`stdin: .From { reader }` connects one child's piped output to another's
stdin through the OS, without copying through bork. The Reader moves to the
new child when that child starts: reading it afterwards gives `Closed`. Lines
it had already buffered are passed on first. A Start that fails to open one
of its files leaves the Reader with its owner; one whose program cannot be
started consumes it.

```bork
import "bork/process"

fn count(s: Scope) uses io + state: String | IoError | Cancelled {
  producer = process.Start(s, "git", ["log", "--oneline"], stdout: .Pipe)?
  counter = process.Start(s, "wc", ["-l"], stdin: .From { reader: producer.Stdout() })?
  lines = counter.Wait()?.StdoutText().trim()
  _ = producer.Wait()?
  lines
}
```

`stdin` is declared `in s`: a Reader passed with `From` belongs to the scope
that starts the new child, as one from a sibling Start in the same scope does.
A function that passes its own `Input` parameter on to Run or Start declares
it the same way: `stdin: process.Input in s`.

## Exit status

`Result` is `{ code, status, stdout, stderr }`. `status` is an `ExitStatus`:
`Exited { code }`, or `Signaled { number, name }` when a signal killed the
child (`name` is the conventional name, such as `"SIGKILL"`; Unix only).
`code` is the exit code, or -1 after a signal. `Success()` is `code == 0`.
`StdoutText()` and `StderrText()` give the bytes as a String; use
`encoding.ParseUtf8` when they must be validated.

`Check()` turns a failed Result into an `ExitError { code, status, stdout,
stderr, message }`, like Python's `check=True`. The message names the exit and ends
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
  repeatedly and always gives the same result. It leaves a piped stdin open:
  waiting on a child that reads stdin to the end hangs until you call
  `Stdin().Close()` (or the scope is cancelled).
- `TryWait()` does not block: `None` while the child runs.
- `Pid()` is the process ID.
- `Signal(signal)` sends a `bork/signal` Signal (`.Interrupt`, `.Terminate`,
  `.Hangup`, `.User1`, `.User2`) to the child's process group. It does nothing
  after the child has finished; unsupported signals give IoError.
- `Stop(grace = 5s)` ends the child gracefully: SIGTERM to its group, then
  SIGKILL after `grace`. `Kill()` sends SIGKILL at once. Neither cancels the
  scope; `Wait` gives the child's Result, such as
  `Signaled { number: 15, name: "SIGTERM" }`, or the code the child chose when
  it handled SIGTERM. On Windows both terminate the child at once and it
  reports `Exited { code: 1 }`.

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
was never called. `attach` adds an owner: the child is cancelled once every owner is.
Process operations declare `io + state`; Pid reads a stored value without
effects. `Args` and `Exit` declare `io`.

## Process groups

Each child starts a new Unix process group. Cancellation, Stop, Kill and Signal
reach the whole group, including shell-wrapper descendants. Descendants that
create another group or session are outside its ownership. Group signals apply
while the direct child is running: descendants that outlive its normal exit are
not killed by later cancellation or scope cleanup. Other targets act only on
the direct child, and Stop kills at once there.

Wait (and so Run) waits at most one second (plus `cancelGrace`) for
inherited output pipes after the child exits, is stopped or is cancelled. If a descendant keeps them open after a
successful exit, the result is IoError rather than partial output. Nonzero
exits keep their Result and output may be truncated at this bound. This bound
keeps capture from blocking cleanup forever.

## Shutdown signals

SIGINT and SIGTERM cancel root scopes; nested scopes inherit cancellation.
Scope-aware waits and checkpoints observe it, and cleanup runs as scopes end,
which also stops their children. Code that does not reach a cancellation point
continues running. See [bork/signal](signal.md) to change this policy.

## Examples

Each runs as is and is tested:

- [process](../../examples/process/main.bork): running a subprocess
- [process_capture](../../examples/process_capture/main.bork): capturing stdout and stderr separately
- [process_combined](../../examples/process_combined/main.bork): capturing stdout and stderr together, in order (2>&1)
- [process_forward](../../examples/process_forward/main.bork): forwarding a child's stdin, stdout and stderr
- [process_mixed](../../examples/process_mixed/main.bork): forwarding, capturing, discarding or writing each stream to a file
- [process_exit_codes](../../examples/process_exit_codes/main.bork): exit codes, signal deaths and checked runs
- [process_stdin](../../examples/process_stdin/main.bork): feeding stdin from text, bytes, a file or a pipe
- [process_streaming](../../examples/process_streaming/main.bork): reading output line by line while it runs, and two pipes at once
- [process_concurrent](../../examples/process_concurrent/main.bork): several processes at once, monitored and collected
- [process_timeout](../../examples/process_timeout/main.bork): timeouts, graceful cancellation and Stop
- [process_pipeline](../../examples/process_pipeline/main.bork): a pipeline of processes connected by OS pipes

[All standard packages](README.md)
