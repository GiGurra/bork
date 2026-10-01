# bork requirements

> Living document. Each section is worked through in discussion and recorded here once agreed.
> **Decided** items are commitments for v0.1. Open questions are listed per section.

## Scope of v0.1

v0.1 is deliberately feature-sparse. It exists to prove the core idea (below): **when everything is immutable, every proven fact stays true**, so correctness checks are cheap, local, and permanent. Anything that doesn't serve that idea waits.

The main advantages over Go are **immutability, facts, and scopes**. Everything else is there to support those three, or to stay out of their way.

## The core idea: immutable values, growing knowledge

**Values never change, but what we know about them only grows, and that knowledge is part of their type.**

When code discovers a property of a value (by a guard, a `match`, a boundary check, or a function's promised result), the property becomes part of that value's type from that point on, as the value is passed forward into other functions, records, and collections. (The compiler derives these facts lazily, only where something needs them; see below.)

```
fn handle(raw: Int, name: Option[String]): Receipt | HandleError = {
  // raw: Int
  if !positive(raw) { return Err(NotPositive) }
  // raw: Int where positive

  match name {
    Some(n) => greet(n)     // n: String
    None    => greetAnon()
  }

  transfer(raw)             // transfer needs Int where positive, and raw has it
}
```

What follows from this:

- **One mechanism, many sources.** Checking an `Option`, an `if` guard, a `match`, a boundary `prove`, an inference rule, and a function's postcondition all do the same thing: add a fact to a type. The statically checked `Option` is simply the most common case.
- **Immutability is what makes it sound.** A fact about an immutable value can never become false, so facts are only ever added, never invalidated. There is no need to track writes or aliasing.
- **More facts means a more specific type.** `Int where positive` can be used anywhere an `Int` is expected. Because nothing is mutable, `List[Int where positive]` is also safely usable as a `List[Int]`.
- **Facts flow through generics.** Passing `x: Int where positive` through `identity[T]` keeps the fact, because `T` is `Int where positive`.
- **Facts live inside data.** Once `user.age` is known to satisfy `adult`, that is part of `user`'s type for as long as `user` exists.
- **Branches merge by intersection.** After an `if`/`else`, only the facts that hold on every path remain.
- **Zero runtime cost.** Facts are erased when compiling to Go. At runtime, `Int where positive` is just an `int`.

### Facts are derived lazily, on demand

Facts exist only at compile time, and are never computed unless something needs them. The compiler does not carry every known fact forward. It works backwards from requirements:

1. **Compile without constraints.** Ordinary type checking first, ignoring all `where` clauses.
2. **Collect obligations at the leaves.** Every call to a function with a constrained parameter (or construction of a constrained record) creates an obligation, e.g. "at this call site, `amount` must be `positive`".
3. **Resolve each obligation backwards** from its call site, through guards, `match`es, bindings, and promised results, until the fact is found (a guard, a literal, a boundary check, a declared result, an inference rule) or it is clear that it cannot be.
4. **Nothing else is derived.** Facts no one asks for are never computed.

This also settles facts about several values (`start <= end`). Such a fact is only ever asked for where some callee requires it, and resolved from there. A value that goes on alone, without the other, causes no trouble, because nothing asks about the relation.

### Callers prove or declare

When an obligation's backward search reaches one of the calling function's own parameters, the caller must do one of two things:

- **Prove it**, with a guard, a `match`, a boundary check, and so on, before the call; or
- **Declare the same requirement** on its own parameter, which passes the obligation on to *its* callers.

There are **no inferred preconditions.** A requirement never climbs silently from a callee to its callers. Every function's requirements are written in its signature, so signatures stay complete and trustworthy, packages compile on their own, and recursion needs no special handling.

```
fn transfer(amount: Money where positive) = ...

fn payOut(amount: Money) = {
  transfer(amount)              // build error: positive not proven
}

fn payOutChecked(amount: Money) = {
  if positive(amount) { transfer(amount) }   // proven here
}

fn payOutDeclared(amount: Money where positive) = {
  transfer(amount)              // declared: payOutDeclared's callers must prove it
}
```

## 1. Core values

In priority order. When two values conflict, the higher one wins.

1. **If it compiles, whole classes of bugs cannot happen.** No null or nil, no unchecked access to optional values, no exceptions, no non-exhaustive matches, no mutable state (v0.1), and no broken contracts.
2. **Rigor must be cheap.** Declaring a rule should cost about as much as writing an `if`. Haskell-level guarantees without Haskell-level ceremony. If a guarantee is hard to express, that is a language bug.
3. **Explicit escape hatches, never silent bypasses.** Whenever a guarantee is weakened, it is visible in the code, greppable, and reviewable.
4. **Readable at 3am during an incident.** Explicit over clever. One obvious way to do common things. **Clarity over conciseness:** agents will write much of the code, so saving keystrokes matters little, while being easy to read and review matters a lot.
5. **Boring, fast tooling.** One `bork` binary, fast builds, and a formatter with no options.
6. **Friendly, specific diagnostics.** Errors say what could not be proven, where, and how to fix it.

## Decided

### Language fundamentals

- **Statically typed.**
- **No null, no nil.** The concept does not exist in the language.
- **`Option` is statically checked.** The value inside an `Option` cannot be extracted without first checking that it is present (via `match`, or a guard the compiler understands). There is no `.get()`-style unchecked extraction.
- **No mutable state in v0.1.** Every value is immutable. This is the foundation of the proof model: a fact proven about a value can never be invalidated later, so proofs are never lost to mutation. Opt-in mutable types may come in a later version.
- **Exhaustive matching is enforced.** A `match` that does not cover every case is a compile error. The compiler never inserts an implicit panic for a missing case.

### Errors and failure

- **No exceptions. At all.** There is no `throw`, no `try`/`catch`. Expected failures are ordinary values, returned as part of a union return type (see section 4).
- **Panics exist but are discouraged.** A panic means a bug, not an expected failure.
- **No `recover`.** A panic cannot be caught in bork code. Erlang-style isolation of routines (a failing routine brought down without taking the rest with it) is planned for later. See crash isolation and supervision under resources and scopes.

### Effects and concurrency

- **Side effects are allowed.** Reading and writing files, sockets, and so on are ordinary operations.
- **No effect or state monads.** No `IO`, `State`, or time monads. Concurrency uses Go's goroutines (virtual threads), so sequential blocking code is the norm, and there is no need to thread effects through types.

### Compilation target and Go

- **Compiles to Go.** Go is purely a compilation target, chosen so bork gets Go's runtime, GC, goroutine scheduler, and cross-platform compilation without building its own backend.
- **No access to Go's standard library or Go packages from bork code.** bork code never calls Go directly. bork has its own standard library. The Go code the compiler generates, and the bork standard library's implementation, can freely use Go's standard library under the hood. That is an implementation detail, invisible to bork programs.
- **No FFI in v0.1.** Calling into Go (or anything else) from bork may come later, as an explicit boundary.
- **bork's syntax is independent of Go.** bork has its own grammar and its own hand-written parser. Go is only the language the first compiler is written in, and the first compilation target.
- **The compiler models bork, not Go.** The compiler's internal model holds the full language and all of its constraints; that is the product. Its Go output is a lowering, not a one-to-one mapping of bork types to Go types. It can drop knowledge once it has been checked (facts are erased entirely), and it can lean on runtime helpers and generated functions where that is simpler. Inspired by TypeScript: the type checker carries the guarantees, and the output just runs. Go is the first target; others may follow.

### General

- **Strict evaluation**, not lazy. Laziness makes memory and performance hard to reason about in backend systems.
- **A short propagation operator** (like Rust's `?`), so handling failures stays cheap. See section 4.
- **No user-defined symbolic operators** (e.g. `|+|`, `>>=`).
- **No hidden resolution magic.** Type class instances are resolved implicitly, but only from an explicitly imported, bounded set of places (see type classes below). Nothing like Scala 2's implicit conversions or whole-program implicit search.
- **Performance target:** roughly Go-level performance, traded away for guarantees where needed.

## 2. Type system

### Decided

- **Records and sum types (ADTs) are the core data types.** Records are product types. Sum types are tagged unions whose variants can carry data.
- **Generics in v0.1.** User code can declare generic types and functions, not just use built-in ones like `Option[T]` and `List[T]`.
- **Variance: planned, not in v0.1.** Scala-style declaration-site variance controls (covariant and contravariant type parameters) are planned for a later version. The syntax should leave room for them.
- **Structural typing for interfaces and constraints.** A type satisfies an interface or generic constraint implicitly, by having the required shape (like Go). No `implements` declarations.
- **Sealed types and traits are opt-in.** Marking a type sealed closes its set of variants to its own declaration. That is what makes exhaustive matching possible without a default case. Matching on an open (unsealed) type always needs a default case.
- **Immutable persistent collections** in the standard library: `List`, `Map`, `Set`.
- **Pragmatic local type inference, like Go and Scala.** Function signatures are written out, which doubles as documentation, and local values are inferred.
- **Scala-style inline lambdas.** Lambda parameter types are inferred from the expected type at the call site, so `users.map(u => u.name)` needs no annotations.

### Type classes

- **Type classes are first-class.** They attach behaviour to a *type*, not just to values of it: type-level operations like `empty`, `decode`, `parse`, or `default` that need no value to call them on.
- **Instances can be declared for constrained types.** For example, `instance Decode[Int where positive]` decodes an `Int` and proves `positive` in one step, so a request type with refined fields decodes into already-proven values.
- **More than one instance per (class, type) may exist.** For example, several JSON encodings or orderings of the same type.
- **The instance search space is explicit.** Instances are looked up only in explicitly imported instance scopes, never by scanning the whole program. Which instance applies is determined by what the file imports.
- **Both structural interfaces and type classes exist**, as different tools for different situations. Structural interfaces describe what a value can do. Type classes attach behaviour to a type, including types you don't own.
- **No automatic default instances.** The compiler never picks up an instance on its own, not even one declared next to the type or the type class. A library may expose a default set of instances, but it must still be imported explicitly. This is an experiment: we'll try it and see how it feels in practice.
- **Ambiguous instances are a compile error.** If more than one in-scope instance matches, including several instances for constrained types that a value satisfies, the build fails.
- **Automatic derivation in v0.1:** `derive Eq, Show, Decode` (and similar) for records and ADTs.
- **Higher-kinded types (`Functor[List]`): room in the syntax, not implemented in v0.1.**
- **No circular package dependencies**, as in Go. This keeps instance lookup, and compilation in general, bounded and predictable.

Sketch (syntax not final):

```
typeclass Monoid[T] {
  fn empty: T
  fn combine(a: T, b: T): T
}

fn sum[T: Monoid](xs: List[T]): T = xs.fold(Monoid[T].empty, Monoid[T].combine)
```

### Open questions

- Scala-style placeholder shorthand for lambdas (e.g. `_.name`), or always named parameters?
- Can a sealed type's variants be spread over several files in one package, or must they sit in one declaration?
- How is an instance passed explicitly at a call site, when narrowing imports is not enough (e.g. `xs.sorted(using byName)`)?
- Instance-dependent collections: a `Set` or `Map` built with one ordering or hash instance could later be used with a different one, and would then silently misbehave. Should collections capture their instance when they are built, so that cannot happen?

## 3. Contracts and knowledge (in progress)

> Nothing in this section is decided yet. The examples show the direction; the syntax is a sketch.

### Constrained inputs: examples

Parameters carry their requirements in the signature. One parameter per line, with trailing commas, so the formatter can align columns and diffs stay small.

```
fn transfer(
  from:   AccountId,
  to:     AccountId where notEqual(from),
  amount: Money where positive and below(10_000),
  note:   Option[String where nonEmpty and maxLen(280)],
): Receipt | TransferError = {
  ...
}
```

Note that the constraint on `note` applies inside the `Option`. The note may be absent, but if it is present, it is non-empty and at most 280 characters.

Named constrained types keep signatures short and give domain concepts a name:

```
type Port     = Int where between(1, 65535)
type Email    = String where validEmail
type UserName = String where nonEmpty and maxLen(100)

fn connect(
  host: String where nonEmpty,
  port: Port,
): Conn | ConnectError
```

Records with constrained fields, derived decoding, and knowledge that arrives at the boundary:

```
type CreateUser = {
  name:  UserName,
  email: Email,
  age:   Option[Int where between(0, 150)],
} derive (Decode)

fn handleCreate(body: Json): User | ApiError = {
  req = CreateUser.decode(body)?      // every field is proven here, once
  createUser(req)                     // no re-validation downstream
}
```

Postconditions: a function's return type can promise facts too.

```
fn clamp(
  x:  Int,
  lo: Int,
  hi: Int where atLeast(lo),
): Int where between(lo, hi) = ...
```

Generic functions that *add* knowledge (the long-term goal; needs predicates usable in type signatures):

```
fn filter[T](xs: List[T], p: Pred[T]): List[T where p]

positives = numbers.filter(positive)   // List[Int where positive]
```

### Syntax notes

- **`name: Type` with a colon** is the working assumption. Once constraints are attached, types become phrases (`Money where positive and below(10_000)`), and the colon keeps the name clearly apart from the type. It also matches Scala-style lambdas: `(u: User) => u.name`.
  The Go-style alternative, `amount Money where positive`, reads well in short signatures but gets harder to scan as constraints grow.
- **`and` combines predicates**, because a comma would clash with the comma between parameters.
- **Predicates can take arguments** (`maxLen(280)`, `between(1, 65535)`), including other parameters (`notEqual(from)`, `atLeast(lo)`), which is how relations between values are expressed.

### Compile-time evaluation

bork needs compile-time evaluation, in the spirit of [q's `AtCompileTime`](https://github.com/GiGurra/q) and proven's literal checks, to infer that types and constraints are satisfied:

- **Predicates on compile-time-known values are evaluated during compilation.** `connect("db", 5432)` is accepted because `between(1, 65535)(5432)` is computed at build time. `connect("db", 0)` fails the build. Unlike proven, this works for *any* predicate, not just a built-in set.
- **Constants and named constrained values.** `defaultPort: Port = 8080` is checked once, when compiled.
- **Explicit compile-time computation** of lookup tables, constants, and derived data, marked at the call site (as q does), with the result baked into the binary.
- **Compile-time code should only read files inside the module.** This is a convention, not enforced in v0.1 (see purity below).

### Facts, purity, and the outside world

- **No purity tracking in v0.1.** Immutability by design already covers concurrency and the stability of values. The compiler does not check that predicates are free of side effects (database or file I/O, the clock, randomness).
- **Predicates are expected to be deterministic, but this is not checked.** A predicate whose answer can change for the same value (e.g. `isOpenNow(store)`, which reads the clock) produces facts that can go stale. That is a bug in the program, not something the compiler catches. The convention is: **effects produce values, and facts are about values.** For example, read `now = clock.now()` once, then use `isOpenAt(store, now)`.
- **Facts about outside systems are facts about a point in time.** A fact can describe the outside world (a row exists, a file is present), but it is true as of when it was established. Keeping that current is the developer's job, not the compiler's. bork is aimed at typical web backend services, where a request reads its inputs, decides, and writes within a short window. Treating what it loaded as a fixed snapshot is the right model there. Concurrent changes elsewhere are handled with the usual backend tools (transactions, idempotency, optimistic locking), not by the proof system.
- **Compile-time evaluation runs whatever a predicate does.** Without purity tracking, a predicate that does I/O will do it at build time when evaluated on a literal. A purity check limited to code reachable from predicates and compile-time blocks can be added later without affecting ordinary code.
- **No widening syntax.** Facts are forgotten simply by passing a value where a less constrained type is expected (`Int where positive` to an `Int` parameter). No `x as Int` is needed.

### Open questions

- **Inline predicates:** only named predicates (`positive`), or also inline expressions (`where it > 0`)? Inline expressions require the compiler to recognise equivalent expressions.
- **Exported return types:** do callers see only the facts a signature declares, or also facts the compiler derives from the body? A proposal: exported functions expose only the declared facts (the signature is the contract), and private functions may expose derived ones.

## Resources and scopes

Outside resources (files, sockets, database connections, transactions, locks) are the one place where "facts only grow" is under pressure: the handle value never changes, but closing it changes the world it refers to. bork handles this with scopes, in the style of ZIO, so the open state can never end while a resource is still reachable.

### Decided (v0.1)

- **Resources belong to scopes.** Opening a resource requires a scope. A resource can be attached to one or more scopes, and it is **closed when the last of them closes**. There is no manual `close`, so there is neither use-after-close nor forgot-to-close.
- **Scopes are blocks.** `scope s { ... }` opens a scope, which closes when the block ends. Finalizers run in reverse order of acquisition (LIFO), like Go's `defer`.
- **Scope values can be passed down, never escape.** A scope can be passed as a function argument, but it cannot be returned, stored in a record, or captured by anything that outlives it.
- **Using a resource requires proof of an open scope managing it.** Every access to a resource is a proof obligation: the compiler must be able to prove, locally, that the resource is attached to a scope that is still open at that point. It is resolved backwards like any other fact. Only scopes visible locally count. If some other scope elsewhere keeps the resource alive at runtime, that does not help the proof.
- **Otherwise the resource is "possibly released".** When the last locally known scope of a resource has ended, the variable is marked possibly released at type level, and any use is a compile error. Runtime reference counts decide when a resource is actually closed; the compiler only ever relies on what it can prove.
- **Ordinary calls need no annotations.** A synchronous call runs entirely inside the caller's scope, so a function can simply take `conn: Conn`. Only escapes need checking: returning a resource, storing it in something longer-lived, or handing it to a goroutine.
- **Attaching happens where the resource is provably alive.** `attach` adds a scope fact, so it is only allowed where the resource is already known to be attached to an open scope. A goroutine therefore gets a resource attached at handover, never after it has started.
- **Reference counting falls out of the design.** Within one call stack, scopes nest, so the last scope is simply the outermost one. Across goroutines, a resource attached to several scopes stays open until all of them have closed, which is shared ownership without a separate `shared_ptr`-style type.
- **Scopes are passed explicitly in v0.1.** Acquiring functions take the scope as an ordinary argument. Implicit scope passing may be added later.

```
fn main() = scope app {
  pool = openDbPool(cfg, app)?             // lives as long as the app
  serve(pool, app)
}

fn handle(req: Request, pool: DbPool) = scope request {
  conn = pool.connect(request)?            // closed when the request scope ends
  file = openFile(path, request)?
  ...
}                                          // finalizers run LIFO

fn broken(db: Db): Conn = {
  c = scope s { db.connect(s)? }
  c.query(...)                             // build error: c may be released (its scope `s` ended)
}

// Handing a resource to a goroutine that may outlive the spawning scope:
// the new goroutine's scope is attached at the handover, while conn is provably alive.
scope job {
  conn = db.connect(job)?
  spawn scope w (attach conn) {            // syntax not final
    worker(conn)                           // conn stays open until job and w have both closed
  }
}
```

With structured concurrency (see below), a goroutine started inside `job` that must finish before `job` ends needs no attaching at all.

### Two lifetimes of facts

Facts come in two kinds, distinguished by how long they live:

- **Value facts live forever.** They are about immutable values in the program, so nothing can make them false.
- **Scoped facts live as long as a scope.** They are about state outside the program, and hold only while the scope is open. "This resource is open" is the first, built-in scoped fact.

### User-defined scoped facts (planned, not v0.1)

Library authors will be able to declare that acquiring a scope grants facts about the outside world, which hold for as long as the scope is open:

```
fn reserveUnit(unit: UnitId, dc: DcId, s: Scope): Reservation | ReserveError
  grants located(unit, dc) in s            // syntax not final

fn powerCycle(unit: UnitId, dc: DcId) where located(unit, dc) = ...

scope maint {
  reserveUnit(u, dc7, maint)?
  powerCycle(u, dc7)                       // proven: located(u, dc7) holds in maint
}                                          // reservation released, fact gone
```

- **A scoped fact is only honest if acquiring the scope holds something that keeps it true:** a lock, a lease, a reservation, a transaction. Without that, the fact is just an observation at a point in time, which is the developer's responsibility.
- **Granting functions are trust points**, like `trust` and inference rules. The compiler believes the declared `grants`. They should be few, greppable, and live in libraries rather than business code.
- **The outside world can still break a promise** (a lease expiring early, hardware moved despite a lock). Handling that at runtime is part of the granting library's responsibility.
- **Backend examples:** inside a transaction, "this row is locked" or "the balance is at least 100"; within a leader lease, "I am the leader"; on a session, "authenticated as user U"; on an HTTP response, "headers not yet sent".
- **Limit:** scopes model **nested** states well (connection → transaction → savepoint), but not **sequential** state changes (a protocol going A → B → C, where each step replaces the previous one). Those would need consumable values (affine types), which may be considered later.

### To settle with concurrency

- **Cancellation and deadlines through scopes.** A request scope is the natural carrier for what Go's `context.Context` does today: when a request is cancelled, its scope closes, its resources are released, and its goroutines stop.
- **Structured concurrency:** must goroutines started inside a scope finish before the scope ends? That would let them use the scope's resources with no extra attaching.

### Crash isolation and supervision (planned, not v0.1)

Erlang-style isolation, where a crashing routine is cleaned up and handled by a supervisor without taking the rest of the program down, becomes cheap in bork, because the two hard parts are solved by design:

- **No shared state can be left half-changed.** Nothing is mutable, so a routine that crashes cannot leave corrupt shared data behind. Its values are simply never used again, and the GC reclaims the memory. No memory tracking is needed.
- **Every resource is owned by a scope on some routine's stack.** When a routine dies, the runtime unwinds its stack and closes every scope on it, running finalizers in reverse order as usual. A resource also attached to a scope in another routine stays open; its count drops by one. No separate resource tracking is needed.
- **A supervisor sees a child's crash as an ordinary value** (e.g. `Crashed(reason)`), and decides what to do: restart, give up, or escalate. By then, the child's resources are already released. User code still has no `recover`. Only the runtime catches the crash at the routine boundary and turns it into that value.

Remaining work:

- **How a bork panic is implemented is a code-generation choice**, not part of the language. Options: a Go panic caught at the routine boundary; `runtime.Goexit()` after recording the reason (it ends the goroutine, still runs deferred cleanup, and cannot be caught by any code); or an explicit extra return path in every function.
- **Go's own runtime panics must still be caught at every routine boundary**, whichever option is chosen (e.g. index out of range, division by zero). Some Go failures cannot be caught at all (out of memory, stack overflow, deadlock) and kill the process. For those, the backstop is restarting the process (systemd, Kubernetes).
- **Facts can rule out many runtime panics.** For example, indexing could require proof that the index is within bounds, and division could require proof of a non-zero divisor, so fewer panics ever come from the Go layer. To be decided with the standard library.
- **Cooperative cancellation.** Go cannot kill a goroutine from outside. A supervisor stops a stuck routine by closing its scope, and the routine stops at its next cancellation point. Since bork owns its standard library, every blocking operation (I/O, sleep, channel receive) can be made cancellation-aware. A CPU-bound loop that never makes such a call cannot be interrupted.
- **Finalizer failures and timeouts.** Finalizers can fail or hang (e.g. closing a stuck socket). They need timeouts, and a rule for what happens when cleanup itself fails.
- **Effects in the outside world are not undone.** A message already sent or a half-written file is a consistency problem, not a resource leak. Scopes help: a transaction scope's finalizer rolls back unless the transaction was committed, so a crash mid-transaction rolls back automatically. Beyond that, the usual backend tools apply (idempotency, outbox).
- **Messages to a dead routine**, and messages still queued for it, need defined semantics once channels or mailboxes are designed.
- **A supervision API:** restart policies, escalation, and how supervisors relate to scopes and structured concurrency.

## 4. Errors and results (in progress, to be tried out)

> The direction below is agreed, to be validated by trying it in real code. Syntax is a sketch.

### Direction

- **No separate error concept.** Failures are ordinary types. There is no `error` kind, no `Error` base type, and no `Result` wrapper.
- **Functions that can fail return union types.** For example: `fn loadUser(id: UserId): User | NotFound | DbError`. Matching on a union is exhaustive, and checking a member narrows the type, just like any other fact.
- **`?` keeps the leftmost member and returns the rest.** `user = loadUser(id)?` binds `User`, and returns `NotFound` or `DbError` from the enclosing function. By convention, the leftmost member is the main result. What `?` does can be read from the callee's signature alone.
- **Returned members must fit the enclosing function's return type.** This is checked, so nothing slips through unhandled.
- **An annotation overrides the default.** `x: B = foo()?` keeps `B` and returns everything else, including what would otherwise be the main result. That is useful for early returns that are not errors (e.g. `miss: CacheMiss = cache.get(k)?` returns a cache hit early).
- **`a?.b?.c` applies `?` at each step** (the Rust reading), not safe navigation.
- **`?` also works on sealed types.** `Option[T]` is `Some[T] | None`, so `v = maybeUser?` keeps the value and returns `None`.
- **Adding context (q's `Wrapf` semantics) is required.** Wrapping attaches to a single `?` and transforms what that `?` returns.
- **Chaining is not a priority.** Clarity beats conciseness. Writing one step per line is fine.

```
fn loadUser(id: UserId): User | NotFound | DbError = ...
fn loadOrders(user: User): List[Order] | DbError | Timeout = ...

fn summary(id: UserId): Summary | NotFound | DbError | Timeout = {
  user   = loadUser(id)?             // keeps User; returns NotFound or DbError
  orders = loadOrders(user)?         // keeps List[Order]; returns DbError or Timeout
  Summary(user, orders)
}
```

### Consequences

- **Union order has meaning for `?`.** For subtyping, `A | B` and `B | A` are still the same type. But reordering a published signature changes what callers' `?` keeps, so it is an API change. The formatter never reorders unions, and a future API-diff tool should flag it.
- **Type parameters are not flattened.** In `fn retry[T, E](...): T | E | Timeout`, the leftmost member is `T` as a whole, even if `T` is itself a union at the call site.

### Open questions

- **Wrap syntax**, e.g. `loadUser(id)?{ e => LoadFailed(id, e) }`, or something else?
- **`?` inside lambdas** returns from the lambda, which makes the lambda's inferred return type a union. Is that what we want, or should `?` require a declared lambda return type?
- **Representation in Go:** an interface and a type switch, a tagged struct, or a runtime helper. This is a lowering choice inside the compiler, not part of the language.

## Language basics

### Syntax

- **Braces and explicit control symbols.** Blocks use `{ }`. No significant indentation, and no Scala-3-style `then`/`do` syntax. Clear delimiters are preferred over terse keywords.
- **Everything is an expression.** `if`, `match`, and blocks produce values.
- **Bindings have no keyword:** `x = ...`. Values are immutable, so there is no `var`/`val` distinction to make.
- **Go-style statement endings.** Line ends terminate statements where that is unambiguous (automatic semicolon insertion). No semicolons in ordinary code.
- **A grammar draft (EBNF) comes before the parser.** See [grammar.md](grammar.md).
- **`fn` declares functions, and parameters are written `name: Type`.** The colon keeps the name apart from the type once constraints are attached.
- **Conditions are parenthesized:** `if (cond) { ... } else { ... }`. `else` goes on the same line as the closing `}`, as in Go.
- **No shadowing.** A name cannot be bound again while it is visible, in the same or an enclosing scope (including function names). Sibling blocks can reuse names.
- **Comments** are `// ...` and `/* ... */`. **String literals** use double quotes with Go's escape sequences.
- **Identifiers cannot start with `_`.** That prefix is reserved for the compiler.

### Numbers

- **Fixed-width integers, as in Go.** `Int` is a 64-bit integer with Go's wrapping arithmetic.
- **Facts respect overflow.** `a > 10` and `b > 10` do not prove `a + b > 10`, because the sum can wrap. Arithmetic implications need upper bounds that rule out overflow.
- **Native big integers.** An arbitrary-precision integer type is built in, for when wrapping is not acceptable.

### Equality

- **Structural equality is generated** for records and unions. Equality compares values, not identities.
- **Comparing incompatible types is a compile error.**
- **Constrained and unconstrained versions of a type are comparable.** `Int == (Int where positive)` is fine, because both are `Int` values.

### Packages and modules

- **Go style.** A directory is a package, a module file at the root names the module, imports use module paths, and there are no circular package dependencies.
- **Visibility follows Go:** names starting with an upper-case letter are exported.

### Open questions

- **`match (x) { ... }` or `switch (x) { ... }`** for pattern matching (with parentheses, like `if`).
- **A decimal or money type** in the standard library. Backends need exact decimal arithmetic, and `Float` is wrong for money.
- **Big integer literals and conversions** between `Int` and the big integer type.
- **String interpolation.**

## Open questions

- Should "rigor must be cheap" rank above "if it compiles, bugs cannot happen", meaning a guarantee is dropped if it cannot be made cheap?
- Limited operator overloading (e.g. `+` for money or vector types), or none at all?
- Other principles to adopt: strong backwards-compatibility promises? No macros?

## Topics still to discuss

1. ~~Core values~~ (above)
2. ~~Type system~~ (above)
3. Contract and proof model: bringing proven's ideas into the language (in progress, above)
4. Errors and results: union return types and `?` (in progress, above)
5. Concurrency: goroutines, channels, structured concurrency, cancellation through scopes, and the (later) isolation model
6. Go interop: no FFI in v0.1; what the future boundary looks like
7. Tooling: the `bork` CLI, formatter, tests, and modules (packages are decided, above)

See also [roadmap.md](roadmap.md) for the implementation plan.

## Explicitly not in v0.1

- Mutable state of any kind
- FFI and importing Go packages
- `recover`
- Erlang-style routine isolation and supervision
