# bork/process

## Processes and shutdown signals

`bork/process` starts argv commands without a shell. Run captures stdout and
stderr as Bytes and returns the exit code, including nonzero exits. Start gives
a scoped Process with repeatable Await, explicit Stop, and Pid. Arguments,
environment inheritance/replacement, working directory and optional Bytes stdin
are supported. Missing stdin is empty. Launch/wait errors are IoError;
explicit or owner cancellation returns Cancelled. Output is retained in memory.
A final resource cleanup cancels, kills and reaps the child, including a child
that was never awaited. Attachment selects the destination cancellation source.
Each subprocess starts a new Unix process group. Cancellation kills that whole
group, including shell-wrapper descendants. Descendants that leave the group
or outlive the direct child's normal exit are outside its ownership. Group
cancellation applies while the direct child is running; other targets kill only
the direct child.
Args returns command-line arguments without the program name; Exit terminates
with the given status without closing scopes. These are the only process
argument/exit helpers. Process operations declare io + state;
Args/Exit declare io. Pid reads the stored ID without effects.

The scope runtime registers SIGINT/SIGTERM for the program lifetime, cancelling
root scopes and their nested scopes. Scope-aware waits and checkpoints observe
cancellation; cleanup runs when those scopes end. Pure work needs an explicit
checkpoint to observe shutdown. This introduces no new language syntax.

Process capture waits at most one second for inherited output pipes after the
child exits or is cancelled. If a descendant keeps them open after a successful
exit, the result is IoError rather than partial output. Nonzero exits retain
their exit code as Result and output may be truncated at this bound.
Cancellation still returns Cancelled. This bound keeps pipe capture from blocking cleanup forever.

## Process API

`bork/process` runs argv commands without a shell. `Run(scope, executable,
arguments = [], environment = Option.None, directory = "", stdin = Option.None)`
returns `Result | IoError | Cancelled`; Result contains `code`, `stdout: Bytes`,
and `stderr: Bytes`. Nonzero exits are results. None environment inherits the
parent environment, Some([]) clears it, and Some(values) supplies KEY=value
entries. An empty directory inherits the current directory. Optional stdin is
Bytes; None gives an empty input stream. Output is captured in memory.
`Start` takes the same arguments and returns a scoped Process; `Await(process)`
returns its result and can be repeated. `Stop(process)` cancels just that child,
and `Pid` gives its process ID. Processes are killed on owner cancellation and
reaped at final scope cleanup, even without Await. `attach` moves cancellation
to the destination scope. These operations declare io and state effects;
`process.Args` and `process.Exit` alias the prelude helpers with io effects.

SIGINT and SIGTERM cancel root scopes; nested scopes inherit cancellation.
Scope-aware waits and checkpoints observe it, and cleanup runs as scopes end.
Code that does not reach a cancellation point continues running. Signals are
registered for the program's lifetime when scope runtime is used. Stopping a
subprocess kills its Unix process group, including descendants that stay in the
group. Each subprocess starts a new group; descendants that create another
group or session are outside its ownership. Group cancellation applies while
the direct child is running: descendants that outlive its normal exit are not
killed by later cancellation or scope cleanup. Other targets kill only the
direct child.
See [the process example](../../examples/process/main.bork).

Process capture waits at most one second for inherited output pipes after the
child exits or is cancelled. If a descendant keeps them open after a successful
exit, the result is IoError rather than partial output. Nonzero exits retain
their exit code as Result and output may be truncated at this bound.
Cancellation still returns Cancelled. This bound keeps pipe capture from blocking cleanup forever.

## Examples

Import `bork/process` to run argv commands with captured Bytes output, an exit
code, optional environment/workdir/stdin, and scope-owned cancellation. SIGINT
and SIGTERM cancel root scopes so scope-aware work can finish and clean up.
See [examples/process](../../examples/process/main.bork).
