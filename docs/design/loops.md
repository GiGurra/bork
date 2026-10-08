# Loops and tail calls (design: bork-8jiark)

> **Status:** Implemented: all loop forms, loop-carried rebinding and direct self-tail-call lowering. Current docs: [basics](../language/basics.md) and [collections](../language/collections.md); precise tail-call limits: [grammar](../grammar.md).
> Bork blocks below are design sketches; the linked current docs contain checked examples.

Before this design, bork had one loop, `for x in xs { ... }` over a `List` or `Seq`, with
`break` and `continue`. Other loops used recursion, and the generated Go did
not eliminate tail calls. A recursive event loop, such as a server's `select`
loop, a retry loop or a state machine, grows the goroutine stack on every
round until Go's 1 GB limit kills the process. Immutable bindings mean the
usual fix (a `while` loop that updates variables) cannot be written either.

The implemented design adds two things:

1. **Go-style loops** with the existing `for` keyword: `for { ... }`,
   `for cond { ... }` and `for init; cond; post { ... }`. Loop-carried
   state uses the same-block rebinding of bork-4exlxc (PR #349).
2. **Tail call optimization in bork's code generator**: self tail calls are
   compiled to jumps. `uses tailrec` is an opt-in guarantee that fails
   compilation when a recursive call is not compiled as a jump. Mutual
   recursion is not optimized. With loops and carried state, a state machine is
   a loop over a state value (see "Mutual recursion").

```bork fragment
fn sum(xs: List[Int]): Int {
  total = 0
  for x in xs {
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
  if b == 0 { a } else { gcd(b, a % b) }
}
```

## Prior art

| Language | Loops | Loop-carried state | Tail calls |
| --- | --- | --- | --- |
| Go | `for {}`, `for cond {}`, `for init; cond; post {}`, `for range`; labels | Mutable variables. Since Go 1.22 each iteration of a three-clause or range loop has fresh copies of its header variables, so closures capture that iteration's value. Variables declared outside the loop are shared by every iteration. | None. Stacks grow (segmented/copying) up to 1 GB, then the process dies. |
| Scala | `while`, `for` comprehensions | `var`s, or recursion | Self tail calls compiled to jumps; `@tailrec` makes it a compile error if a call isn't one. No mutual TCO on the JVM; `scala.util.control.TailCalls` is a library trampoline. |
| Kotlin | `while`, `for (x in xs)` | `var`s | `tailrec fun` turns self tail calls into a loop; a warning (not error) if it can't. Calls inside `try`/`finally` are not tail calls. |
| Clojure | `loop`/`recur`, `doseq` | `loop` bindings, rebound by `recur` with new values | `recur` is an explicit self jump, checked to be in tail position. `trampoline` for mutual recursion: functions return thunks. |
| Zig | `while (cond) : (post) {}`, `for` | Mutable `var`s; `while` has a continue expression run on `continue` | `@call(.always_tail, f, args)` asks LLVM for a guaranteed tail call, and is a compile error where it can't be done. |
| Rust | `loop`, `while`, `for`; labels; `break value` from `loop` | `let mut` | Not guaranteed. Explicit `become f(x)` (RFC 3407) is unstable. It requires matching signatures and drops locals before the jump. |

What bork takes:

- **From Go:** the three loop forms, the one `for` keyword, and per-iteration
  values for closures. Unlike Go, closures capture *every* loop-carried
  value per iteration, including state declared before the loop, not only
  the loop's header variables.
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
- **Not taken:** mutual tail calls, whether by trampolines (Clojure, Scala
  TailCalls) or by merging functions. See "Mutual recursion".

## 1. Loops

### Forms

```ebnf
For        = "for" [ LoopHeader | "(" LoopHeader ")" ] Block .
LoopHeader = ( Ident | "_" ) "in" Expr                        (* existing: for x in xs *)
           | Expr                                              (* while: for i < n *)
           | [ LoopInit { "," LoopInit } ] ";" [ Expr ] ";" [ Rebind { "," Rebind } ] .
LoopInit   = Ident [ ":" Type ] "=" Expr .
Rebind     = Ident "=" Expr .
LoopControl = "break" | "continue" .
```

- `for { ... }` loops until `break`, `return`, `?` or a panic.
- `for cond { ... }` checks the `Bool` condition before each iteration.
- `for i = 0; i < n; i = i + 1 { ... }` binds the header names (the
  *init*), checks the condition before each iteration, and runs the *post*
  rebindings after each iteration and on `continue`. Each of the three parts
  may be empty, as in Go: `for ; ; i = i + 1` is legal, though `for` and
  `for cond` are the idiomatic forms when init and post are empty. The
  formatter keeps the clauses as written; `--simplify` removes redundant
  head parentheses.
- Several header names are separated by commas, which Go's syntax can't do
  (`for lo = 0, hi = n; lo < hi; lo = lo + 1, hi = hi - 1`). The init
  bindings run in order, as successive bindings do, so `lo = 0, hi = lo + n`
  works. The post rebindings compute the next iteration's values. They all
  see the values from before the post clause, as a simultaneous assignment
  does: `lo = hi, hi = lo` swaps them.
- Header names belong to the loop. They are not visible after it, as in Go.
  A header name cannot already name a binding of an enclosing block: that
  would be nested shadowing, which PR #349 forbids. To carry an existing
  name, use it in a while loop. (`i = 0` before
  `for i < n { ... i = i + 1 }` carries `i`, and `i` stays visible after.)
- Post rebindings may rebind header names and the names the loop carries
  from outside (next section).
- `for x in xs` is unchanged.

Parsing: after `for` and an optional `(`, an identifier or `_` followed by
the contextual `in`
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

```bork fragment
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
an enclosing block. Loops need exactly one exception. **A loop body may
rebind a name that the block containing the loop may rebind, at its top level
or inside the branches of `if` and `match` statements in it** (human
decision, option B). Such a name is *carried*. Each rebinding gives the name a
new immutable value for the rest of its block. Where branches meet, the name
has the value of the branch that ran; a branch that does not rebind it keeps
the previous value. The latest value flows into the next iteration, and after
the loop the name has the value it had when the loop ended.

```bork fragment
fn stats(xs: List[Int]): String {
  count = 0
  total = 0
  negatives = 0
  for x in xs {
    if x < 0 {
      negatives = negatives + 1
      continue
    }
    count = count + 1
    total = total + x
  }
  s"$count values, total $total, $negatives skipped"
}
```

The names that can be carried:

- the header names of a three-clause loop (`i` above);
- names bound earlier in the block that contains the `for` statement, and
  the function's parameters if that block is the function body's top level.
  These are the names that block may rebind under PR #349;
- recursively, the names carried by an enclosing loop, when the inner `for`
  is where the outer loop's body may rebind them (its top level, or a branch
  as below).

Nested loops therefore carry through each level:

```bork fragment
total = 0
for row in rows {
  for x in row {
    total = total + x
  }
}
println(total)
```

**Branches.** A carried name may be rebound inside the then and else blocks of
an `if`, inside `match` arms, and inside plain nested blocks, at any depth,
when each of these is a *statement*. That means its value is not used: it is
an expression statement of the loop body, or of a branch that qualifies in
turn. Go programmers write `if x > best { best = x }`, and that works:

```bork fragment
best = 0
for x in xs {
  if x > best {
    best = x
  }
}
```

At the end of each `if`/`match` statement the name is *joined*. Its value is
that of the branch that ran, or the value from before the statement for a
branch that did not rebind it (including an `if` without `else`). Branches
that end in `return`, `break`, `continue`, `?` or a panic do not reach the
join. They carry their own current values to where they go.

Still errors, as under PR #349 outside loops: rebinding in an `if` or `match`
whose value is used (`y = if c { total = 1; 2 } else { 3 }`), because the
rebinding would happen in the middle of an expression; in a scope or `with`
block; in a lambda, `lazy`/`async` initializer or generator; and anywhere
outside a loop body. The error for a branch whose value is used says to move
the rebinding out, or to write the `if` as a statement.

A for-in's element name (`x`) is per-iteration, not carried. It belongs to
the loop, not to the body's block, so the body cannot rebind it (bind another
name), and the next iteration gets the next element.

**Why not only header names?** The lead suggested making header names the
only carried state. That leaves no way to get a result out of a loop without
`break value`, and a loop that carries two results would need a record or
tuple. The carried-rebinding rule covers for-in and while loops too
(`total = total + x` is the most common loop there is), and reads like Go
while staying immutable. The cost is one exception to "no rebinding in nested
blocks", limited to the one place where it means "next iteration's value".
The lead approved this. The human then chose option B, which allows
rebinding in branches too (below).

**What `continue` and `break` carry.** `continue` carries the values current at
the `continue`: the latest rebinding on the path to it, including rebindings
in the branches it is in. In a three-clause loop
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
loop invariant. The value that leaves an iteration must prove them on every
path: the value current at each `continue`, at the end of the body, after the
post clause, and at each `break`. Intermediate rebindings within an iteration
need not. The facts hold at the start of every iteration and after the loop.
At a join, the name knows the facts that hold for the value of every incoming
branch. Flow facts the first value had
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

```bork fragment
fs: List[() => Int] = []
for i = 0; i < 3; i = i + 1 {
  fs = fs.append(() => i)
}
println(fs.map(f => f())) // [0, 1, 2]
```

### Unused bindings

PR #349 makes every unused local binding an error. Carried names use the same
rule, with the loop's back edge counted as a use:

- A carried rebinding is used if, on some path from it, the value is read
  before the name is rebound. Paths run through the rest of its block, joins,
  the next iteration (end of body, `continue`, post clause, then the head), and
  the loop's end (condition false, `break`) to the code after the loop. A
  rebinding that every path overwrites before reading is unused, in a branch
  as anywhere.
- The binding before the loop is used if the loop reads it at the head, or if
  the loop may run zero times or `break` before rebinding it and the name is
  read after the loop.
- A three-clause header name follows the same rule, with the post clause as
  part of the iteration.

"Read" means *observed*. A read whose only purpose is to compute the next
value of a carried name that is itself never observed does not count. This is
computed as a fixpoint over the loop's carried names, so `count = count + 1`
alone does not keep `count` alive, and neither do two names that only feed
each other (`a = b + 1; b = a`). Conditions, `break`/`continue` guards,
arguments of calls, and reads after the loop are observations. As a result,
`for i = 0; ; i = i + 1 { if done() { break } }` is an error (`i` is never
observed), where Go accepts it. The fix offers `for { ... }`. This is the
same strictness PR #349 applies to every other binding.

A carried function parameter keeps #349's exemption for its value on entry.
Its rebindings inside the loop follow the rules above.

```bork fails
count = 0
for x in xs {
  count = count + 1 // error: count is never observed: it is only read to
} // compute its own next value, and not after the loop
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
  compile to Go like everything there). A `for {}` that never ends is stopped by
  the evaluator's existing ten-second timeout, with the usual error. `pred` bodies stay loop-free, as
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
    _ = total_1              // legal in Go when the body rebinds before reading
    if x < 0 { _c_total = total_1; continue }
    total_2 := total_1 + x
    _c_total = total_2       // end of body
}
total_3 := _c_total          // after the loop
```

Edges assign the binding current at that point to the state variable:
`continue`, end of body, post clause, and `break`. The post clause and the
three-clause header use the same state variables. The condition reads the
iteration's copies, so it is tested at the top of the body. The post clause
is the Go loop's post statement, which Go runs after the end of the body and
after `continue` alike: it assigns the state variables the result of a
function literal that binds copies of them first, so a closure made in the
post clause captures a copy, never a state variable
(`_c_i = func() int64 { _c_i := _c_i; return _c_i + 1 }()`). Header names
are bound before the loop too, for the header names after them. The existing
loop-exit machinery (scopes opened in the body closed on `break`/`continue`,
owners, mocks, `return` through range-over-func `Seq` loops) is reused for
the new forms.

Joins use the same rule. Before an `if` or `match` statement whose branches
rebind carried names, a join variable is declared for each. Every branch that
reaches the end assigns its current binding to it, and after the statement a
fresh binding copies it (`best_3 := _j_best`). Closures capture branch
bindings or the fresh copy, never the join variable. (As built, the join
variable is itself that fresh binding: it is assigned only inside the
statement and read only after it, so no closure can see it change.)

Every new statement carries its bork position for the debugger and `//line`
mapping. A breakpoint on the `for` line hits once per condition check.

### Tooling

Per docs/syntax-changes.md: lexer (no new tokens), parser and AST
(`For` gains `Init`, `Cond`, `Post` and a form tag), formatter (header spacing
`for i = 0; i < n; i = i + 1`, and `for ; ;`), checker,
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
| inside a lambda, `lazy`/`async` initializer, generator, `comptime` | a different function |
| a self call of a function with an `OwnedScope` parameter | the call's owner must be dropped when the call returns, if it was not passed on (a per-call Go `defer` today) |
| a self call with other type arguments (polymorphic recursion) | Go needs a different instantiation |
| an `unsafe go` body | not bork code |

`return f(x)` inside a loop is a tail call. It leaves the loops the way
`return` does today: it computes the arguments into the jump's parameter
slots, sets the exit flag with a "jump" mark instead of a result, and breaks.
At each loop level the existing post-loop code (`forget` of owners,
re-checking the flag) runs. After the outermost loop, the function jumps
instead of returning. No cleanup is skipped. The jump simply replaces the
final `return`.

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
one call must keep that call's parameters. Each copy is followed by `_ = x`,
since bork parameters may be unused. Named and default arguments,
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

**Go defers.** A jump must not leave a per-call Go `defer` behind. That would
make the deferred list grow with every jump, which is the growth TCO is
meant to remove. The generator emits three function-level defers today:

- the loop cleanup root of a top-level `for` (`defer root.close()`). It is
  created once per Go function, outside the `_tail` loop, and shared by
  every call, since its job (closing scopes and owners still open in an
  abandoned iteration) is the same for every call;
- the ambient label guard (`guardLabels`), also hoisted. A tail call cannot
  be inside a `with` block, so no labels are pushed at the jump;
- `dropOwner` for `OwnedScope` parameters. It is per call, so a self call of
  such a function is not a tail position (table above).

The internal `compilerCallerLocation` helpers (prelude and std only) take a
hidden caller location that a self call would change. They are not
optimized, and they cannot declare `tailrec`.

**Mocks.** A test that mocks `f` sees `f`'s recursive calls today, because
calls go through the dispatcher. In a test build, a jump in a mocked function
first asks the dispatcher. If a mock is in force for the current test, the call
is made through it as an ordinary call; otherwise the function jumps. Programs
have no dispatchers, so nothing changes there.

**Debugging and stack traces.** A jump reuses the frame, so a panic in a deep
recursion shows one frame of `f` rather than a million. That is the point, and
it is how every TCO language behaves. For the debugger, the jump
carries the call's position, so stepping stops on the call line first. In
plain Delve, "step over" a tail call then lands on the function's first line:
step-over turns into step-into, because no new frame exists to step over. The
DAP relay (PR #340) can make step-over at a jump run to the function's real
return instead, using the jump positions the generator records in its debug
map. That is a follow-up there; until then the behavior is documented in
docs/debugging.md.

### Mutual recursion

**Decision: not optimized. `tailrec` rejects it and points at the loop
rewrite.** Two designs were considered:

- **Trampolines** (Clojure's `trampoline`, Scala's `TailCalls`). Every member
  returns "a result or the next call", the caller bounces, and every bounce
  allocates. It needs a second calling convention for function values and Go
  interop, and it hides the real function in every stack trace.
- **Same-package merging.** Packages can't be cyclic, so mutually recursive
  functions are always in one package. Their component can be compiled into
  one private Go function with a `switch` on which member is running, plus a
  one-line wrapper per member (`func isOdd(n int64) bool { return
  _tc_isEven_isOdd(1, 0, n) }`). It costs nothing at run time. But the members
  need the same result type, the same type arguments at every call between
  them (not just identical type-parameter lists), and the same `needs`. Mock
  dispatch has to work per member, and Go stack traces show
  `_tc_isEven_isOdd` and the entry wrapper rather than the member that is
  running, since `//line` cannot rename functions.

Both are real work for a case that loops now cover. The motivating case,
a server or protocol state machine, is a loop over a state value with carried
rebinding:

```bork fragment
state: State = State.Idle
for {
  state = match state {
    State.Idle => awaitJob(s)?
    State.Running { job } => run(s, job)?
    State.Done => break
  }
}
```

That reads as well as `idle()`/`running()` functions calling each other, and
its stack use is obvious. So bork optimizes self tail calls only.
Same-package merging stays the design to use if an example ever needs
mutual recursion. It is additive, and `tailrec`'s promise (below) would then
widen from "self" to "the component".

### `uses tailrec`: the guarantee

```bork fragment
fn serve(s: Scope, state: State) uses io + state + tailrec: Ok | Cancelled { ... }
```

`tailrec` in a function's `uses` list is a **marker, not an effect**. It
promises that the function's *direct* recursion cannot grow the stack.
Compilation fails unless:

- Every direct self call in the body is a tail call, so it is compiled as a
  jump. Each one that is not gets an error naming the call and the reason
  from the table above: `serve(s, next) is not a tail call: it is inside the
  scope block at line 14, which closes after the call returns`.
- The function is not part of mutual recursion through direct calls. That is
  a cycle in the full direct call graph that passes through another function,
  whether or not those calls are in tail position. The error names the cycle
  (`serve calls drain (line 9), which calls serve (line 31)`) and suggests a
  loop over a state value.
- The function calls itself at all. Declaring `tailrec` on a function that
  is not recursive is an error with a fix to remove the marker, since it would
  promise nothing.

The promise covers direct calls only. Recursion through function values,
class methods or higher-order calls (`retry(serve)`, `xs.map(f)`) is not
visible to the compiler and not covered.

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
  ordinary call through it (above). A spy mock that delegates to the real
  function therefore grows the stack in that test. The guarantee is about the
  program, not about a test that replaces the function.
- It is not a word that can be used as an effect anywhere else: `tailrec` is
  contextual inside `uses` lists only and stays usable as a name.

Without the marker, nothing reports a non-tail recursive call. Plenty of
recursion is shallow and fine, such as recursion over a tree's depth.

### Visibility

- **LSP hover** on a call shows `tail call: compiled as a jump`. On a recursive call that is
  not one, it shows `recursive call, not a tail call: inside the scope block
  at line 14`. Inlay hints are left to the editor worker.
- **`bork describe`** at a call gives the same `tailCall` fact in JSON
  (`{"jump": true}` or `{"jump": false, "reason": ...}`).
- Semantic tokens: none new.

## 3. Examples and docs

- Rewrite recursive loops in examples and docs as loops where a loop is what
  they mean. Candidates: `testdata/cases/ambient` `countdown`,
  `testdata/cases/scopes` `countdown`, the state machine in
  `docs/language/matching.md`, and the
  channel examples the channels worker is holding for this
  (`examples/channel_select`, `unbounded_queue`, `pipeline`). I'll coordinate
  with `channels` on who rewrites them once loops land.
- New example `examples/loops`: the three forms, carried state, nested loops,
  break/continue with carried values, a `tailrec` state machine.
- `docs/language/basics.md` gets a Loops section (with carried rebinding) and
  a short Recursion and tail calls section. tour.md and grammar.md get the
  forms, and requirements.md gets the full rules. diagnostics.md gets the new
  codes (`loop.carried-type`, `loop.carried-lazy`, `tailrec.not-tail`,
  `tailrec.not-recursive`, `tailrec.mutual`, `tailrec.position`).

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
5. **Examples migration** with `channels`.

## Open questions

- **Labels** for `break`/`continue` across nested loops (`outer: for ...`).
  Not needed for the motivating cases. Add when an example wants them.
- **`break value`**, if carried names turn out to be clumsy for search loops
  (`found = Option.None` plus `break`).
- **Mutual tail calls** by same-package merging (above), if an example needs
  them.
- **A lint for unbounded non-tail recursion** in long-running functions
  (`uses state` + recursion without `tailrec`). Not planned.
