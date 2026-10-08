# Process API: stream control, sync and async

> **Status:** Implemented: stream configuration, process handles and typed exit status. Current docs: [process API](../std/process.md). The comparisons below preserve the original API design.
> Bork blocks below are design sketches; the linked current docs contain checked examples.

Design for bork-y4tu16. The original `bork/process` API had `Run`, `Start`, `Await`,
`Stop` and `Pid`, captures both output streams in memory, takes stdin as
`Option[Bytes]` and reports `Result { code, stdout, stderr }`. That covered
"run a command and look at its output". The implemented design adds
independent control of stdin, stdout and stderr, an exit status that separates
exit codes from signals, a checked-run convenience, and an async handle that
streams output, feeds input incrementally and stops gracefully. It adds no
keyword or syntax: everything is plain std types, sealed variants and named
arguments.

## What other languages do

| Concern | Go os/exec | Rust std::process | Python subprocess | bork (this design) |
| --- | --- | --- | --- | --- |
| Per-stream config | `Cmd.Stdin/Stdout/Stderr` fields (nil = /dev/null, `*os.File`, any `io.Writer`, or `StdoutPipe()`) | `Stdio::inherit/piped/null`, `From<File>`, `From<ChildStdout>` | `stdin/stdout/stderr=None/PIPE/DEVNULL/fd/file` | `stdin: Input`, `stdout: Output`, `stderr: ErrorOutput` sealed values |
| Default | all /dev/null | `output()` captures, `spawn()`/`status()` inherit | inherit | Run and Start capture stdout/stderr; stdin is empty |
| Combined output | `CombinedOutput()`, or same writer for both | not built in (needs `os_pipe`) | `stderr=STDOUT` | `stderr: .Stdout`, ordered by the OS |
| Non-zero exit | `*ExitError` from `Run/Output` | data: `ExitStatus` | data: `returncode`; `check=True` raises | data: `Result.status`; `result.Check()` turns it into `ExitError` |
| Killed by signal | `ExitCode() == -1`, `WaitStatus.Signaled()` | `ExitStatusExt::signal()` | negative `returncode` | `ExitStatus.Signaled { number, name }` |
| Timeout | `CommandContext` | none (manual `try_wait` loop) | `timeout=` raises `TimeoutExpired` | a scope with `cancelAfter` gives `Cancelled` |
| Async handle | `Start` + `Wait`; pipes are `io.ReadCloser` | `Child` with `stdin/stdout/stderr: Option<…>`, `wait`, `try_wait`, `kill` | `Popen` with `poll`, `wait`, `communicate`, `send_signal`, `terminate`, `kill` | `Process` with `Stdout()/Stderr()` readers, `Stdin()` writer, `Wait`, `TryWait`, `Signal`, `Stop`, `Kill`, `Pid` |
| Pipeline | `b.Stdin, _ = a.StdoutPipe()` | `Stdio::from(child.stdout.take())` | `stdin=p1.stdout` | `stdin: .From { reader: a.Stdout() }` |
| Cleanup | caller must `Wait`; orphans possible | `Child` drop does not kill | `with Popen(...)` waits, does not kill | the owning scope kills the process group and reaps it |

The lessons we take: Python's per-stream vocabulary (inherit, pipe, discard,
file, merge into stdout) is the clearest, and `check=True` is the convenience
people reach for. Rust's "exit status is data" is right for a language of
guarantees. Go's context cancellation and process-group kill already underpin
the current implementation, and its "same writer for both streams" trick is
how combined output keeps its order. Every one of them leaks children on some
path; scope ownership is the part bork already does better and keeps.

## Stream configuration

Each stream is configured on its own with a sealed value. Leading-dot variant
syntax keeps call sites short:

```bork fragment
type Input = sealed {
  Empty,                       // /dev/null (the default)
  Inherit,                     // the parent's stdin
  Data { data: Bytes },        // written by bork, then closed
  Text { text: String },       // UTF-8 text, then closed
  File { path: String },       // opened for reading
  Pipe,                        // written incrementally with child.Stdin()
  From { reader: Reader },     // another process's piped output (pipelines)
}

type Output = sealed {
  Capture,                     // retained in memory, in Result (the default)
  Inherit,                     // forwarded to the parent's same stream
  Discard,                     // /dev/null
  File { path: String },       // created or truncated
  Append { path: String },     // created or appended
  Pipe,                        // streamed with child.Stdout()/Stderr()
}

type ErrorOutput = sealed {
  Capture, Inherit, Discard, File { path: String }, Append { path: String }, Pipe,
  Stdout,                      // merged into wherever stdout goes (2>&1)
}
```

`stderr` takes `ErrorOutput`, which is `Output` plus `Stdout`. A separate type
makes "merge stdout into itself" unrepresentable instead of a runtime error.
`.Stdout` follows stdout's configuration: with `.Capture` both streams land in
`Result.stdout` in the order the child wrote them (one OS pipe, so ordering is
exact, not interleaved by bork), `Result.stderr` is empty; with `.Inherit`
stderr goes to the parent's stdout; with `.Pipe` one reader yields both; with
`.File` both share the file.

Examples of each combination people ask for:

```bork fragment
process.Run(s, "make")                                          // capture both separately
process.Run(s, "make", stderr: .Stdout)                         // capture combined, in order
process.Run(s, "make", stdin: .Inherit, stdout: .Inherit, stderr: .Inherit)  // forward all
process.Run(s, "make", stdout: .Discard, stderr: .Inherit)      // show only errors
process.Run(s, "sort", stdin: .Text { text: "b\na\n" })          // feed input
process.Run(s, "gzip", stdin: .File { path: "a" }, stdout: .File { path: "a.gz" })
```

**Defaults stay the current behaviour**: stdin `.Empty`, stdout and stderr
`.Capture`, for both Run and Start. Scripts that want a shell-like "just run
it" pass `.Inherit`; a later convenience can be added if examples show it is
common. Files are opened by bork before the child starts; a failure is an
`IoError` naming the path, and nothing is started.

**Compatibility.** `stdin: Option[Bytes]` becomes `stdin: Input`. Existing call
sites that passed `Option.Some(data)` change to
`.Data { data: data }`; the repo's callers migrate in the same PR. The old
`Option.None` default is `.Empty`, unchanged. Positional arguments keep their
order (`s, name, arguments, environment, directory, stdin`); the new
`stdout`/`stderr`/`cancelGrace` parameters are appended, so only call sites
passing stdin change.

## Exit status

```bork fragment
type ExitStatus = sealed {
  Exited { code: Int },                    // normal exit with a status code
  Signaled { number: Int, name: String },  // killed by a signal (Unix)
}

type Result = { code: Int, status: ExitStatus, stdout: Bytes, stderr: Bytes }
```

`code` stays for compatibility and simple checks: the exit code, or -1 when
the process was killed by a signal (Go's `ExitCode()` convention). `status` says
which happened (the type is `ExitStatus` because `process.Exit` is already
the function that ends the program). `name` is the conventional name (`"SIGKILL"`, `"SIGTERM"`); a
number is kept because `bork/signal.Signal` deliberately covers only catchable
shutdown signals, and SIGKILL or SIGSEGV must still be reportable. Windows
never produces `Signaled`.

A non-zero exit and a signal death are both data: Run returns `Result`, never
`IoError`, for them. `IoError` stays for "could not start" and wait failures;
`Cancelled` for cancellation by the owning scope.

Methods on Result:

```bork fragment
fn (r: Result) Success(): Bool                       // exit is Exited { code: 0 }
fn (r: Result) Check(): Result | ExitError           // like Python's check=True
fn (r: Result) StdoutText(): String                  // the bytes as a String
fn (r: Result) StderrText(): String
type ExitError = { status: ExitStatus, stdout: Bytes, stderr: Bytes, message: String }
```

`Check` is a method, not a flag on Run, so Run's result type never depends on
an argument. Usage: `out = process.Run(s, "git", ["status"])?.Check()?`.
`ExitError.message` is a one-line description ending with the last line of
captured stderr (`exit status 2: fatal: not a git repository`), so
`eprintln(error.message)` is already useful. Text accessors follow the existing
`fs.Lines` convention: bork Strings hold the bytes as-is; use
`encoding.ParseUtf8` when validation matters.

## Timeouts and cancellation

There is no `timeout` argument. A deadline is a scope, as everywhere else in
bork:

```bork fragment
result = scope limited {
  cancelAfter(limited, 2000.millis())
  process.Run(limited, "slow-tool")
}
```

When the deadline passes, the child's process group is killed and Run returns
`Cancelled { reason: "context deadline exceeded" }`. One mechanism covers
explicit `cancel`, SIGINT/SIGTERM shutdown of root scopes (bork/signal) and
deadlines; there is no separate TimedOut type to forget in a union.

`cancelGrace: Duration = 0.nanos()` on Run and Start makes cancellation graceful:
the group first receives SIGTERM, and SIGKILL follows only if the child is
still running after the grace period. Zero keeps today's immediate kill. Scope
cleanup waits at most the grace period plus the existing one-second pipe bound,
so a child can never hold its scope open indefinitely.

## Async: Start and the Process handle

`Start` takes the same arguments as Run and returns a `Process` owned by the
scope. Everything else is a method (the free functions `Await`, `Stop` and
`Pid` are replaced; callers are migrated in the same PR):

```bork fragment
fn (p: Process) Wait() uses io + state: Result | IoError | Cancelled
fn (p: Process) TryWait() uses io + state: Option[Result | IoError | Cancelled]
fn (p: Process) Pid(): Int
fn (p: Process) Signal(signal: signal.Signal) uses io + state: Ok | IoError
fn (p: Process) Stop(grace: Duration = 5_000_000_000.nanos()) uses io + state
fn (p: Process) Kill() uses io + state
fn (p: Process) Stdin(): Writer
fn (p: Process) Stdout(): Reader
fn (p: Process) Stderr(): Reader
```

- `Wait` is repeatable and returns the same immutable result each time
  (today's `Await`). Unlike Rust it does not close a piped stdin: a monitor
  task waiting while another task writes input is the normal async shape, and
  closing stdin under that writer would cut it off. Callers close stdin when
  done; scope cleanup closes it otherwise.
- `TryWait` never blocks: `None` while running. It is the polling primitive for
  monitoring loops; a task calling `Wait` is the blocking one.
- `Signal` sends one of bork/signal's signals to the process group, reusing the
  `Signal` sealed type from #331–#334 rather than inventing a second list.
- `Stop` is graceful: SIGTERM to the group, then SIGKILL after `grace`. The
  child exiting because of it is ordinary data: `Wait` returns `Result` with
  `Signaled { name: "SIGTERM" }` or whatever exit code the child chose on
  SIGTERM. **This changes today's Stop**, which kills immediately and makes
  Await return `Cancelled`; `Cancelled` now means only that the owning scope
  was cancelled. `Kill` is the immediate SIGKILL. On Windows both terminate the
  direct child.
- All of these act on the Unix process group Start already creates, with the
  existing ownership caveats (descendants that leave the group are outside it).

Scope ownership is unchanged: closing or cancelling the owning scope kills and
reaps the child, closes its pipes and finishes pump goroutines, whether or not
anyone called Wait. No path leaves an orphan the scope could have reached.

### Streaming output: Reader

A stream configured `.Pipe` is read through a `Reader` resource owned by the
process's scope:

```bork fragment
fn (r: Reader) NextLine() uses io + state: String | Closed | Cancelled | IoError
fn (r: Reader) NextChunk(max: Int = 32768) uses io + state: Bytes | Closed | Cancelled | IoError
fn (r: Reader) ReadAll() uses io + state: Bytes | Cancelled | IoError
fn (r: Reader) Lines() uses io + state: Seq[String | Cancelled | IoError]
fn (r: Reader) LinesChannel(s: Scope, capacity: Int = 64) uses io + state: Channel[String | IoError]
```

`NextLine` strips `\n` and `\r\n`, returns a final unterminated line, then
`Closed` at end of stream (the same `Closed` the channel API uses). `NextChunk`
returns whatever is available, up to `max`. Reads observe the owning scope:
cancellation returns `Cancelled` and unblocks a pending read.

The pull Reader is the primitive because it maps to all three references
(`bufio.Scanner`, `BufRead::lines`, `readline`), gives natural backpressure (a
slow reader stalls the child at the OS pipe buffer instead of growing memory)
and needs no callback effects. `Lines` is a sequence for `for` loops, like `fs.Lines`. `LinesChannel` adapts
the Reader to the channels world: it
starts a task in `s` that pumps lines into a bounded `Channel` and closes the
channel at end of stream, so process output can take part in select alongside
timers and other channels (bork-73dn9h). The element type carries `IoError` so
a read failure is delivered in order rather than lost. The adapter's exact
shape follows the channels redesign; if that work adds a selectable source
interface, Reader implements it directly and the pump task goes away.

Calling `Stdout()` on a stream that is not `.Pipe` gives a Reader that is
already at end of stream (`Closed`), and `Stdin()` on a non-piped stdin gives a
Writer whose writes return `Closed`. A failure union on every accessor would
make every streaming call site carry a `NotPiped` case for a programming
mistake that the first test run reveals; Rust's `Option` take has the same
cost. This is the decision most worth revisiting.

Wait does not drain pipes. As with Go, a child blocks when its pipe buffer is
full, so read pipes (in a task, or before Wait) or use `.Capture`. With
`stderr: .Pipe` and `stdout: .Pipe`, read them in two tasks or use
`stderr: .Stdout` and one reader. The docs and the streaming example show this.

### Writing input: Writer

```bork fragment
fn (w: Writer) Write(data: Bytes) uses io + state: Ok | Closed | Cancelled | IoError
fn (w: Writer) WriteText(text: String) uses io + state: Ok | Closed | Cancelled | IoError
fn (w: Writer) Close() uses io + state
```

Writes block while the child is not reading (pipe backpressure) and observe
scope cancellation. A write after the child exited is `Closed` (EPIPE is not an
`IoError` and never raises SIGPIPE in the parent; Go already ignores it).
`Close` is idempotent and sends EOF.

### Pipelines

`.From { reader: reader }` connects a piped output directly to another
process's stdin at the OS level, without copying through bork:

```bork fragment
producer = process.Start(s, "git", ["log", "--oneline"], stdout: .Pipe)?
counter = process.Start(s, "wc", ["-l"], stdin: .From { reader: producer.Stdout() })?
lines = counter.Wait()?.StdoutText()
_ = producer.Wait()?
```

Handing a Reader to another process transfers it: further reads on it return
`Closed`, and bytes it had buffered are passed on first. Both processes stay
owned by their scopes. Because `Input` can now hold a Reader, `stdin` is
declared `stdin: Input in s` on Run and Start: the Reader belongs to the
starting scope. Run, which has no handle, treats a piped stdin as empty and
piped output as captured.

## Effects and types

All operations that start, signal, wait or move bytes declare `io + state`, as
today. `Pid` reads a stored value and stays effect-free. `Process`, `Reader`
and `Writer` are resources owned by the scope passed to Start and follow the
usual attach rules. `Input`, `Output`, `ErrorOutput`, `ExitStatus`, `Result` and
`ExitError` are plain immutable values.

## Implementation notes

- Capture with stdout and stderr both `.Capture` keeps two buffers. With
  `stderr: .Stdout` the same Go writer is assigned to both, so os/exec gives the
  child one pipe and the OS orders the writes.
- `.Pipe` uses `os.Pipe` directly rather than `Cmd.StdoutPipe`, so `stderr:
  .Stdout` can share the write end and a Reader can be handed to another
  process as an `*os.File`. The parent closes its copy of write ends after
  Start. Reader wraps the read end with a `bufio.Reader`; scope cancellation
  closes the read end, which unblocks a pending read on Go's poller.
- `ExitStatus` comes from `ProcessState.Sys().(syscall.WaitStatus)`; Windows
  defines the same type with `Signaled()` always false, so it reports
  `Exited`.
- `Stop`/`cancelGrace` send SIGTERM to `-pid` and arm a timer for SIGKILL;
  cancellation uses `Cmd.Cancel` plus `Cmd.WaitDelay` (grace + 1s).
- The current one-second bound for inherited output pipes after exit stays,
  with its documented IoError-on-success behaviour.

## Delivery

1. **Stream config and sync** — `Input`/`Output`/`ErrorOutput` (without
   `Pipe`/`From`), `ExitStatus`, `Result.status`, `Success`/`Check`/text
   methods, `cancelGrace`, migration of `stdin` callers, and every Process
   method except the stream accessors (`Wait`, `TryWait`, `Pid`, `Signal`,
   graceful `Stop`, `Kill`), so callers migrate once. Golden tests in
   `testdata/cases/process_streams` for each stream mode, merge ordering, exit codes,
   signal deaths, Check and deadline cancellation.
2. **Async and streaming** — `Reader`, `Writer`, `.Pipe`, `.From` pipelines,
   `Stdin`/`Stdout`/`Stderr` accessors, `Lines` channel adapter
   (aligned with bork-73dn9h). Tests for interleaved streaming, incremental
   stdin, cleanup on cancel with unread pipes, and pipelines.
3. **Examples and docs** — small runnable, tested examples under
   `examples/process_*`: capture separate, capture combined, forward all,
   mixed forwarding, exit codes and checked run, stdin feeding, streaming line
   by line, concurrent processes, timeout and cancellation, pipeline. Rewrite
   `docs/std/process.md` as the reader page, and update the tour, examples
   index and scripts page.

## Open questions

- Variant names avoid clashing with type names (`Data`, `From` rather than `Bytes`, `Reader`).
- Accessors on non-piped streams return a closed Reader/Writer instead of a
  failure (see above).
- `Stop` changes meaning from "kill, Await gives Cancelled" to "graceful
  terminate, Wait gives the Result". The only repo caller is the process golden
  test.
- Defaults capture rather than inherit. Matching Rust's `output()` and today's
  behaviour, but a script running `make` must ask for `.Inherit` to see output.
