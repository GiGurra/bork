# Loops and tail calls (design: bork-8jiark)

bork has one loop today, `for (x in xs) { ... }` over a `List` or `Seq`, with
`break` and `continue`. Everything else is written as recursion, and Go does
not eliminate tail calls. A recursive event loop, such as a server's `select`
loop, a retry loop or a state machine, grows the goroutine stack on every
round until Go's 1 GB limit kills the process. Immutable bindings mean the
usual fix (a `while` loop that updates variables) cannot be written either.

This design adds two things, as decided by the human:

1. **Go-style loops** with the existing `for` keyword: `for { ... }`,
   `for (cond) { ... }` and `for (init; cond; post) { ... }`. Loop-carried
   state uses the same-block rebinding of bork-4exlxc (PR #349).
2. **Tail call optimization in bork's code generator**: self tail calls and
   mutual tail calls within a package are compiled to jumps. `uses tailrec`
   is an opt-in guarantee that fails compilation when a recursive call is not
   compiled as a jump.

```bork
fn sum(xs: List[Int]): Int {
  total = 0
  for (x in xs) {
    total = total + x
  }
  total
}

fn serve(s: Scope, jobs: Channel[Job], quit: Channel[Ok]) uses io + state: Ok | Cancelled {
  for {
    match (select {
      job = jobs.receive(s) => job
      _ = quit.receive(s) => Stop {}
    }?) {
      job: Job => handle(job)
      _: Closed => break
      _: Stop => break
    }
  }
}

fn gcd(a: Int, b: Int) uses tailrec: Int {
  if (b == 0) { a } else { gcd(b, a % b) }
}
```

## Prior art

| Language | Loops | Loop-carried state | Tail calls |
| --- | --- | --- | --- |
| Go | `for {}`, `for cond {}`, `for init; cond; post {}`, `for range`; labels | Mutable variables. Since Go 1.22 each iteration of a three-clause loop has fresh copies of its header variables, so closures capture that iteration's value. | None. Stacks grow (segmented/copying) up to 1 GB, then the process dies. |
| Scala | `while`, `for` comprehensions | `var`s, or recursion | Self tail calls compiled to jumps; `@tailrec` makes it a compile error if a call isn't one. No mutual TCO on the JVM; `scala.util.control.TailCalls` is a library trampoline. |
| Kotlin | `while`, `for (x in xs)` | `var`s | `tailrec fun` turns self tail calls into a loop; a warning (not error) if it can't. Calls inside `try`/`finally` are not tail calls. |
| Clojure | `loop`/`recur`, `doseq` | `loop` bindings, rebound by `recur` with new values | `recur` is an explicit self jump, checked to be in tail position. `trampoline` for mutual recursion: functions return thunks. |
| Zig | `while (cond) : (post) {}`, `for` | Mutable `var`s; `while` has a continue expression run on `continue` | `@call(.always_tail, f, args)` asks LLVM for a guaranteed tail call, and is a compile error where it can't be done. |
| Rust | `loop`, `while`, `for`; labels; `break value` from `loop` | `let mut` | Not guaranteed. Explicit `become f(x)` (RFC 3407) is unstable. It requires matching signatures and drops locals before the jump. |

What bork takes:

- **From Go:** the three loop forms, the one `for` keyword, and per-iteration
  values for closures. Unlike Go, closures capture *every* loop-carried
  value per iteration, not only the header variables of a three-clause loop.
- **From Clojure:** the loop is a tail-recursive local function in disguise.
  Its carried names are the function's parameters, and each iteration rebinds
  them. bork writes `recur`'s arguments as ordinary rebindings in the body
  rather than as one call at the end, because `continue` and `break` can leave
  from the middle.
- **From Scala and Kotlin:** automatic self TCO, and an opt-in marker that
  turns "this isn't a tail call" into a compile error. bork optimizes without
  the marker; the marker only guarantees.
- **From Zig and Rust `become`:** the guarantee is checked by the compiler,
  not by hoping the backend does it. The rule matches Rust's: cleanup that must
  run after the call (bork's scope blocks, Rust's drops, Kotlin's `finally`)
  means it is not a tail call.
- **Not taken:** trampolines (Clojure, Scala TailCalls) as the general
  mechanism. See "Mutual recursion".

## 1. Loops

### Forms

```ebnf
For        = "for" [ "(" LoopHeader ")" ] Block .
LoopHeader = ( Ident | "_" ) "in" Expr                        (* existing: for (x in xs) *)
           | Expr                                              (* while: for (i < n) *)
           | [ LoopInit { "," LoopInit } ] ";" [ Expr ] ";" [ Rebind { "," Rebind } ] .
LoopInit   = Ident [ ":" Type ] "=" Expr .
Rebind     = Ident "=" Expr .
LoopControl = "break" | "continue" .
```

- `for { ... }` loops until `break`, `return`, `?` or a panic.
- `for (cond) { ... }` checks the `Bool` condition before each iteration.
- `for (i = 0; i < n; i = i + 1) { ... }` binds the header names (the
  *init*), checks the condition before each iteration, and runs the *post*
  rebindings after each iteration and on `continue`. Each of the three parts
  may be empty, as in Go: `for (; ; i = i + 1)` is legal, though `for` and
  `for (cond)` are the formatted forms when init and post are empty. The
  formatter rewrites `for (; cond;)` to `for (cond)` and `for (;;)` to `for`.
- Several header names are separated by commas, which Go's syntax can't do
  (`for (lo = 0, hi = n; lo < hi; lo = lo + 1, hi = hi - 1)`). The post
  rebindings see the values from before the post clause, as a simultaneous
  assignment does: `lo = hi, hi = lo` swaps them.
- `for (x in xs)` is unchanged.

Parsing: after `for (`, an identifier or `_` followed by the contextual `in`
is a for-in. Otherwise the parser reads a binding list or an expression and
looks at the next token: `;` means three-clause, `)` means while. `x = ...`
cannot be an expression in bork, so a binding head is unambiguous. `for {` is
currently a syntax error, so no program changes meaning.

**Labels** (`break outer`) are left out. Go has them, and nested loops that
leave both loops need a carried flag without them. They are additive later
(`outer: for ...`); see open questions.

**Types.** A loop is an expression of type `Ok`, and its body must have type
`Ok` (as today). An infinite `for { ... }` with no `break` that targets it has
type `Never`. Code after it is unreachable, which is a compile error, as after
`return`. A function whose body ends in such a loop needs no final value:

```bork
fn main() uses io + state {
  for {
    println(next())
  }
}
```

**No `break value`.** Carried names already carry results out of a loop (next
section), so a loop never needs to be an expression with a value. `break
value` can be added later without changing anything here; Rust's
`let x = loop { break 5 }` would become `x = for { break 5 }`.

**`prelude forever()`:** not added. It was option B before loops existed.
`for { ... }` does the same without a sequence object, and `break` works.

### Loop-carried state: carried rebinding

Bindings are immutable, and after PR #349 a name may be rebound in the same
block (`x = f(x)` makes a new binding). Nested blocks cannot rebind names from
an enclosing block. Loops need exactly one exception. **The top-level block
of a loop body may rebind a name that the block containing the loop may
rebind.** Such a name is *carried*. Each rebinding gives the name a new
immutable value for the rest of the iteration. Its latest value flows into the
next iteration, and after the loop the name has the value it had when the
loop ended.

```bork
fn stats(xs: List[Int]): String {
  count = 0
  total = 0
  for (x in xs) {
    if (x < 0) { continue }
    count = count + 1
    total = total + x
  }
  s"$count values, total $total"
}
```

The names that can be carried:

- the header names of a three-clause loop (`i` above);
- names bound earlier in the block that contains the `for` statement, and
  the function's parameters if that block is the function body's top level.
  These are the names that block may rebind under PR #349;
- recursively, the names carried by an enclosing loop, when the inner `for`
  is at the top level of the outer loop's body.

Nested loops therefore carry through each level:

```bork
total = 0
for (row in rows) {
  for (x in row) {
    total = total + x
  }
}
println(total)
```

Not carried, and still an error under PR #349: rebinding from a nested block
inside the body (an `if`'s block, a match arm, a scope or `with` block, a
lambda). A conditional update is a rebinding with an `if` value:
`best = if (x > best) { x } else { best }`. Keeping updates at the body's top
level keeps every carried value visible in one column, and keeps the
data flow to a simple chain per iteration that the checker can follow without
general flow analysis.

A for-in's element name (`x`) is per-iteration, not carried: rebinding it in
the body is an ordinary same-block rebinding, and the next iteration gets the
next element.

**Why not only header names?** The lead suggested making header names the
only carried state. That leaves no way to get a result out of a loop without
`break value`, and a loop that carries two results would need a record or
tuple. The carried-rebinding rule covers for-in and while loops too
(`total = total + x` is the most common loop there is), and reads like Go
while staying immutable. The cost is one exception to "no rebinding in nested
blocks", limited to the one place where it means "next iteration's value".
This is the decision I most want the lead to check.

**What `continue` and `break` carry.** `continue` carries the values current at
the `continue`: the latest top-level rebinding before it (bindings inside the
nested block holding the `continue` are not carried). In a three-clause loop
the post clause then runs. `break` ends the loop with the values current at the
`break`. Reaching the end of the body is an implicit `continue`.

**Types.** A carried name keeps one type across the loop: the type of the
binding the loop starts with (its annotation if it has one). Every carried
rebinding in the body or post clause must be assignable to it. A rebinding to a
different type is an error that explains the rule and suggests annotating the
first binding with the wider type:

```text
total is carried to the next iteration of the loop at line 4, so its new
value must be Int (its type before the loop), found Float; declare it
before the loop as total: Float = ...
```

Declared facts on the first binding (`i: Int where nonNegative = 0`) are the
loop invariant. Every carried rebinding must prove them, and they hold at the
start of every iteration and after the loop. Flow facts the first value had
(its constant identity, branch facts) are dropped at the loop head, because
later iterations may not keep them. Inside the body, a while or three-clause
condition is known (`i < n`), as an `if` guard is. After a loop with no
`break`, the negated condition is known for the carried values, since that is
the only way out besides `return`.

`lazy` and `async` bindings cannot be carried, in either direction. Carrying
would force a lazy value at the loop head, or await an async one. The error
says to bind the forced value under another name first.

**Closures.** Every value is a fresh immutable binding, so a closure captures
the value the name had where the closure was made. That covers each
iteration's values and each rebinding within an iteration. This is stronger
than Go 1.22, where only three-clause header variables are per iteration.

```bork
fs: List[() => Int] = []
for (i = 0; i < 3; i = i + 1) {
  fs = fs.append(() => i)
}
println(fs.map(f => f()))   // [0, 1, 2]
```

### Unused bindings

PR #349 makes every unused local binding an error. Carried names use the same
rule, with the loop's back edge counted as a use:

- A carried rebinding is used if it is read later in the same iteration, or if
  it can reach the next iteration (end of body, `continue`, post clause) and
  the name is read at the head (condition, post clause, or the body before its
  first rebinding), or if it can reach the loop's end (condition false,
  `break`) and the name is read after the loop.
- The binding before the loop is used if the loop reads it at the head, or if
  the loop may run zero times or `break` before rebinding it and the name is
  read after the loop.
- A three-clause header name must be read in the condition, the body or the
  post clause. `for (i = 0; ...)` with `i` never read is the usual unused
  error. The fix replaces the header with a while form or `_`.

```bork fails
count = 0
for (x in xs) {
  count = count + 1   // error: count is never read: not after the loop, and
}                     // not before it is rebound in the next iteration
```

The fix suggestions are those of PR #349. "Remove the binding" applies only
when the removed value has no effects.

### Interactions

- **Lifetimes.** A carried value's lifetime at the loop head is the lifetime
  of the binding before the loop. Every carried rebinding must have a lifetime
  within it, which already holds for scopes opened inside the body, since
  their blocks cannot rebind outer names. The check exists for values bound to
  a scope parameter of a lambda or a scope from `openScope` in the body. The
  error names the scope and the loop.
- **Owned scopes and moves.** An `OwnedScope` cannot be carried. The existing
  rule ("a loop cannot consume owned scope b from outside its body") already
  forbids consuming the value from before the loop, and carrying one through
  iterations would need ownership flow facts this design does not add. The
  move design (bork-u8ndmy) forbids moving, inside a body, a handle created
  outside it. A carried resource counts as created outside the body, so the
  loop moves only what it acquires in the same iteration.
- **Effects.** None of their own. A while or three-clause condition
  contributes its effects to the function as any expression does.
- **Generators.** `yield` works inside any loop form, and carried values
  survive across yields (they are ordinary Go variables of the producer).
- **Comptime, predicates.** The new loops are allowed in comptime blocks (they
  compile to Go like everything there). `pred` bodies stay loop-free, as
  today.
- **Select.** Arm bodies of PR #370's `select` are ordinary expressions of
  the enclosing function, so `break` and `continue` in an arm refer to the
  enclosing loop. That gives the event-loop example at the top.

### Code generation

Each carried name gets a Go state variable that no closure ever captures, and
every bork binding stays a Go variable that is assigned exactly once:

```go
_c_total := total            // before the loop
for _, x := range xs {
    total_1 := _c_total      // this iteration's value; closures capture this
    if x < 0 { _c_total = total_1; continue }
    total_2 := total_1 + x
    _c_total = total_2       // end of body
}
total_3 := _c_total          // after the loop
```

Edges assign the binding current at that point to the state variable:
`continue`, end of body, post clause, and `break`. The post clause and the
three-clause header use the same state variables. The condition reads the
iteration's copies. A Go three-clause `for` is not used: Go copies header
variables at the start of the next iteration, *after* the body could have
mutated the copy a closure holds. Plain variables plus `for { if !cond {
break } ... }` keep bork's guarantee independent of Go's rules. The existing
loop-exit machinery (scopes opened in the body closed on `break`/`continue`,
owners, mocks, `return` through range-over-func `Seq` loops) is reused for
the new forms.

Every new statement carries its bork position for the debugger and `//line`
mapping. A breakpoint on the `for` line hits once per condition check.

### Tooling

Per docs/syntax-changes.md: lexer (no new tokens), parser and AST
(`For` gains `Init`, `Cond`, `Post` and a form tag), formatter (header spacing
`for (i = 0; i < n; i = i + 1)`, normalizing empty clauses), checker,
lowering, facts, lifetimes, unused analysis, effects, execution audit, code
generation, describe (carried names and their head/after bindings), LSP
(hover on a carried rebinding: "carried to the next iteration"; semantic
token modifier as for rebinding; folding), tree-sitter, TextMate, Vim,
Emacs, Helix and Zed grammars, playground, docs (grammar.md, requirements.md,
docs/language/basics.md, tour.md) and examples.

## 2. Tail calls

### What is a tail call

A call is a **tail call** when its result becomes the function's result with
nothing left to do after it. Tail positions are:

- the final expression of the function body block;
- the operand of `return`, also inside loops;
- in a tail position: the final expression of a block, both branches of
  `if`/`else`, every arm of `match`, and every arm body of `select`.

They are *not* tail positions, with the reason the compiler gives:

| Where | Why |
| --- | --- |
| inside a `scope` block | the scope closes (waits for tasks, runs finalizers) after the call returns |
| inside a `with` block | the ambient values, and published log labels, are restored after the call |
| a block in which a `mock` is in force | the mock ends after the call (tests only) |
| the operand of `?`, or inside any larger expression | the result is still inspected or used |
| a call whose value is converted to the result type | the value changes after the call: a `T` widened into a different Go representation of the result (a self call returns exactly the result type, so this concerns calls to other functions in mutual recursion) |
| inside a lambda, `lazy`/`async` initializer, generator, `comptime` | a different function |
| a self call with other type arguments (polymorphic recursion) | Go needs a different instantiation |
| an `unsafe go` body | not bork code |

`return f(x)` inside a loop is a tail call: a Go `continue` to the function's
loop label leaves any range-over-func iteration correctly, and bork's
existing loop exit runs first (the loop cannot be inside a scope block, or it
would not be a tail position).

### Self tail calls

A function with at least one self tail call is compiled with its body inside a
loop. Each tail call evaluates its arguments in order, assigns them to the
parameters together, and jumps to the top:

```go
func gcd(a_in int64, b_in int64) int64 {
_tail:
    for {
        a, b := a_in, b_in        // fresh per call: closures capture these
        if b == 0 {
            return a
        }
        a_in, b_in = b, a%b       // gcd(b, a % b)
        continue _tail
    }
}
```

The per-call copies matter for the same reason as in loops: a closure made in
one call must keep that call's parameters. Named and default arguments,
receivers (methods are functions with the receiver first), hidden ambient
(`needs`) parameters and generic dictionaries are parameters like any
other. They are passed unchanged, since a self call has the same type
arguments and ambient values. Test mode runtime checks of parameter
requirements stay inside the loop, so they run on every call as before. A
result promise is checked once at the real return. That return gives the last
call's result, which carries the same promise.

TCO is **automatic**. It is applied to every self tail call, with or without
`tailrec`, as in Scala and Kotlin. It changes no result, effect or order of
evaluation; it only stops the stack from growing.

**Mocks.** A test that mocks `f` sees `f`'s recursive calls today, because
calls go through the dispatcher. In a test build, a jump in a mocked function
first asks the dispatcher. If a mock is in force for the current test, the call
is made through it as an ordinary call; otherwise the function jumps. Programs
have no dispatchers, so nothing changes there.

**Debugging and stack traces.** A jump reuses the frame, so a panic in a deep
recursion shows one frame of `f` rather than a million. That is the point, and
it is how every TCO language behaves. Stepping over a tail call in the
debugger lands on the function's first line, as stepping into a new call
does. The jump statement carries the call's position, so "step" stops on the
call line first. The DAP relay's bork stack view
(PR #340) marks a frame that has jumped. This is cheap: a hidden
per-frame counter of jumps, shown as `gcd (tail call ×12)`. If it is not
cheap enough, it is dropped.

### Mutual recursion

**Decision: same-package loop merging, not trampolines.**

A set of functions that tail-call each other (a strongly connected component
of the call graph restricted to tail calls) is compiled into one private Go
function with a `switch` on which function is running. Each function keeps
its own Go entry point, a one-line wrapper that enters the merged function at
its case:

```go
func isEven(n int64) bool { return _tc_isEven_isOdd(0, n, 0) }
func isOdd(n int64) bool  { return _tc_isEven_isOdd(1, 0, n) }

func _tc_isEven_isOdd(which int, isEven_n_in int64, isOdd_n_in int64) bool {
    for {
        switch which {
        case 0: // isEven
            n := isEven_n_in
            if n == 0 { return true }
            which, isOdd_n_in = 1, n-1
        case 1: // isOdd
            n := isOdd_n_in
            if n == 0 { return false }
            which, isEven_n_in = 0, n-1
        }
    }
}
```

Why merging:

- **Zero cost.** No allocation per bounce, no interface call, no change to
  any function's signature or to function values. Calls from outside the
  component, function values and Go interop see the ordinary function. A
  trampoline makes every member return a thunk, or a sum of "result or next
  call", allocates on every bounce, and needs a second calling convention
  for function values.
- **Packages can't be cyclic,** so every component of mutually recursive bork
  functions is already inside one package and one generated Go file set.
  Merging never needs to see across packages.
- **State machines are the use case.** `fn idle(...)` / `fn running(...)`
  that call each other in tail position is the natural way to write a
  protocol or a server's states, and the case the human's event-loop report
  was about.

A component is merged when all its members:

- have the same result type;
- are either all non-generic, or have identical type parameter lists, which
  Go's generic function then has once;
- have the same `needs` (hidden ambient parameters are shared slots);
- are bork-bodied (not `unsafe go` or Go bindings).

Members that call each other but don't meet these conditions are compiled
separately, and their mutual calls stay ordinary calls. A self tail call is
still a jump either way. Mock dispatch works as for self calls, per target.
Stack traces show the member function's name through the wrapper and the
`which` label, kept as the case's position mapping. The DAP relay shows the
merged frame under the running member's name.

Not supported, by design: tail calls to function values or class methods
(the target is unknown), or across packages.

### `uses tailrec`: the guarantee

```bork
fn serve(s: Scope, state: State) uses io + state + tailrec: Ok | Cancelled { ... }
```

`tailrec` in a function's `uses` list is a **marker, not an effect**. It
promises that the function's recursion cannot grow the stack. Compilation fails
unless every recursive call is compiled as a jump. A recursive call is a call
from the function to itself, or, with mutual recursion, a call between two
members of its component:

- Every self call in the body must be a tail call. Each one that is not gets
  an error naming the call and the reason from the table above:
  `serve(s, next) is not a tail call: it is inside the scope block at line 14,
  which closes after the call returns`.
- If the function is in a mutually recursive component, every call between
  members of that component, in any member's body, must be a tail call. The
  component must also be mergeable. Otherwise the error names the member and
  the failed condition: `serve and drain call each other but have different
  result types (Ok | Cancelled, Ok)`. Other members need not declare
  `tailrec`; the promise belongs to the function that declares it, and it
  covers every path that recursion through it can take.
- A function declaring `tailrec` that is not recursive at all is an error
  (the marker would promise nothing), with a fix to remove it.

Marker rules:

- It does not propagate. Callers don't need it in their `uses`, and it is
  not part of the function's type: a function value of `serve` has type
  `(Scope, State) uses io + state => Ok | Cancelled`.
- It is allowed only on a declared function or method with a bork body. It is
  an error in a function type (`(Int) uses tailrec => Int`), on a lambda, a
  class method signature, an `unsafe go` body or binding, a `pred` (which
  cannot declare `uses`), and on `main` or tests (which can't be called).
- `uses tailrec` alone means the function is pure (`uses nothing` plus the
  marker), not "may use every effect".
- Mocks of a `tailrec` function are ordinary mocks. The marker is not part of
  what is mocked, and a mock in force turns the recursive call into an
  ordinary call through it (above). The guarantee is about the program, not
  about a test that replaces the function.
- It is not a word that can be used as an effect anywhere else: `tailrec` is
  contextual inside `uses` lists only and stays usable as a name.

Without the marker, nothing reports a non-tail recursive call. Plenty of
recursion is shallow and fine, such as recursion over a tree's depth.

### Visibility

- **LSP hover** on a call shows `tail call: compiled as a jump` (or
  `tail call: jump to isOdd, merged with isEven`). On a recursive call that is
  not one, it shows `recursive call, not a tail call: inside the scope block
  at line 14`. Inlay hints are left to the editor worker.
- **`bork describe`** at a call gives the same `tailCall` fact in JSON
  (`{"kind": "self" | "mutual" | "none", "reason": ...}`), and on a function
  its component.
- Semantic tokens: none new.

## 3. Examples and docs

- Rewrite recursive loops in examples and docs as loops where a loop is what
  they mean. Candidates: `testdata/cases/ambient` `countdown`,
  `testdata/cases/scopes` `countdown`, the state machine in
  `docs/language/matching.md` (which becomes a `tailrec` example), and the
  channel examples the channels worker is holding for this
  (`examples/channel_select`, `unbounded_queue`, `pipeline`). I'll coordinate
  with `channels` on who rewrites them once loops land.
- New example `examples/loops`: the three forms, carried state, nested loops,
  break/continue with carried values, a `tailrec` state machine.
- `docs/language/basics.md` gets a Loops section (with carried rebinding) and
  a short Recursion and tail calls section. tour.md and grammar.md get the
  forms, and requirements.md gets the full rules. diagnostics.md gets the new
  codes (`loop.carried-type`, `loop.carried-lazy`, `tailrec.not-tail`,
  `tailrec.not-recursive`, `tailrec.unmergeable`, `tailrec.position`).

## Implementation plan

Loops depend on PR #349 (rebinding and unused locals) being merged. Tail calls
do not.

1. **This design** (PR).
2. **Self TCO + `uses tailrec`** (checker: tail positions and the marker;
   gen: loop wrapping, mock-aware jumps; describe and hover). Independent of
   #349, so it can go first.
3. **Loop forms + carried rebinding**: parser, AST, formatter, checker,
   lowering, facts, lifetimes, unused, gen, describe, LSP, playground, docs,
   `examples/loops`. Possibly split into (a) `for {}` / while / three-clause
   with header names and (b) carried rebinding of outer names, if (3) gets
   too big.
4. **Editor grammars** (tree-sitter, TextMate, Vim, Emacs, Helix, Zed) for
   the new headers and `tailrec`. Coordinated with `editors`.
5. **Mutual tail calls**: component merging, `tailrec` over components, DAP
   frame naming.
6. **Examples migration** with `channels`.

## Open questions

- **Labels** for `break`/`continue` across nested loops (`outer: for ...`).
  Not needed for the motivating cases. Add when an example wants them.
- **`break value`**, if carried names turn out to be clumsy for search loops
  (`found = Option.None` plus `break`).
- **A lint for unbounded non-tail recursion** in long-running functions
  (`uses state` + recursion without `tailrec`). Not planned.
