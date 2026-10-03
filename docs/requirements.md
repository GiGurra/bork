# bork requirements

> Living document. Each section is worked through in discussion and recorded here once agreed.
> **Decided** items are commitments for v0.1. Open questions are listed per section.

## Scope of v0.1

The toolchain includes `bork fmt [paths...]`: one canonical whitespace format,
with two spaces for indentation and normalized spacing and blank-line grouping.
Existing line breaks, comment text, and raw Go bodies are retained. `--check`
reports files that would change and exits non-zero without writing them.

v0.1 is deliberately feature-sparse. It exists to prove the core idea (below): **when everything is immutable, every proven fact stays true**, so correctness checks are cheap, local, and permanent. Anything that doesn't serve that idea waits.

The main advantages over Go are **immutability, facts, and scopes**. Everything else is there to support those three, or to stay out of their way.

## The core idea: immutable values, growing knowledge

**Values never change, but what we know about them only grows, and that knowledge is part of their type.**

When code discovers a property of a value (by a guard, a `match`, a boundary check, or a function's promised result), the property becomes part of that value's type from that point on, as the value is passed forward into other functions, records, and collections. (The compiler derives these facts lazily, only where something needs them; see below.)

```
fn handle(raw: Int, name: Option[String]): Receipt | HandleError = {
  // raw: Int
  if (!positive(raw)) { return NotPositive {} }
  // raw: Int where positive

  match (name) {
    Option.Some { value: n } => greet(n)     // n: String
    Option.None              => greetAnon()
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
6. **Friendly, specific diagnostics.** Errors say what could not be proven, where, and how to fix it. `check`, `build`, and `test` accept `--json` for versioned JSON Lines with stable diagnostic codes and optional suggested text edits; see [the format](diagnostics.md). Text output remains the default. `bork describe` queries types, definitions, methods and facts from the typed compiler tree; `--where` asks the actual prover whether a selected value meets a requirement at that point. Fact enumeration is conservative; see [compiler code queries](describe.md).

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

- **Side effects are allowed, and declared.** Reading and writing files, sockets, and so on are ordinary operations, and a function's signature says which kinds it does: `fn save(path: String, text: String) uses io: Unit | IoError`. A function that declares nothing is pure (see [Effects in signatures](#effects-in-signatures-proposal)).
- **No effect or state monads.** No `IO`, `State`, or time monads. Concurrency uses Go's goroutines (virtual threads), so sequential blocking code is the norm. Effects are checked annotations, not wrapper types: code stays in direct style.

### Compilation target and Go

- **Compiles to Go.** Go is purely a compilation target, chosen so bork gets Go's runtime, GC, goroutine scheduler, and cross-platform compilation without building its own backend.
- **Ordinary bork code does not call Go.** bork has its own standard library. The Go code the compiler generates can freely use Go's standard library under the hood; that is an implementation detail, invisible to bork programs.
- **`unsafe go` is the explicit boundary.** A function's body can be raw Go: `fn f(x: Int): String unsafe go { import "strconv"; return strconv.FormatInt(x, 10) }`. It is allowed anywhere, in user code as well as in the standard library, which is itself written this way (the built-in prelude). bork trusts the signature: the guarantees (immutability, no null, the declared result) hold outside these bodies, and inside them only as far as the Go code keeps them. The Go code sees bork values as the compiler represents them, which is documented in the prelude. Standard packages use a documented [Go helper API](std-go.md) for maps, options, and scope context/lifecycle operations, with a compilation test covering every helper. Go errors in the body are reported at their bork positions. Importing Go packages as bork modules is not planned for v0.1; instead, bindings call Go functions directly, checked against their Go signatures (`fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`), and Go types are proposed in [Go interop](#go-interop-proposal).
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
- **Automatic derivation in v0.1:** `derive (Decode, Encode)` for records and ADTs. Structural equality and default text need no derive.
- **Higher-kinded types (`Functor[List]`): room in the syntax, not implemented in v0.1.**
- **No circular package dependencies**, as in Go. This keeps instance lookup, and compilation in general, bounded and predictable.

Implemented: the prelude class `Show[T] { fn show(x: T): String }` customizes `toString`, `println`, interpolation, snapshots, and nested printing. Every type has a default renderer, so `Show` bounds always hold and `show(x)` equals `toString(x)`, even inside unbounded generic code. Custom Show instances follow a special coherence rule: one renderer for a record or sealed type, declared only in that type's own package, with no `use` needed. For a generic type, the instance must be universal (`Show[Box[T]]`) and may have only `Show` bounds. Specialized or fact-constrained instances cannot define a renderer; basic types, `List`, `Map` and `Option` must be wrapped in a declared type to customize their text. This differs from other classes' selectable instances because a value should print consistently everywhere. Other classes, including a user-defined class named Show, retain the ordinary instance rules. Custom text never changes equality or map-key hashing. Show cannot be derived; default rendering needs no derive.

```
class Monoid[T] {
  fn empty(): T
  fn combine(a: T, b: T): T
}

instance sumInt: Monoid[Int] {
  fn empty(): Int { 0 }
  fn combine(a: Int, b: Int): Int { a + b }
}

instance showBox[T: Show]: Show[Box[T]] { ... }   // universal renderer for an owned type

fn sum[T: Monoid](xs: List[T]): T { xs.fold(empty[T](), combine) }
```

- **Instances are named,** so several can exist per class and type, and `use` can pick them: `use money.DecodeAmount`, or every exported one: `use money.*`.
- **A package suggests instances with a named set,** which importers take with one `use`. This is the "default set of instances" a library may expose, still imported explicitly. A set can hold the package's own instances (exported or not: they come with the set), other packages' (`money.*`), and other sets. A `use` applies to the whole package.

```
// package api
use money.Defaults                     // what api's own code uses
instances Json { ItemDecode, ItemEncode, money.Defaults }   // what api suggests

// an importer
use api.Json
```
- **What is in scope:** a package's own instances, the prelude's, and those it uses. Instances of other packages are never picked up without `use`. (A package's own instances being in scope is a pragmatic exception to "no automatic instances"; to offer alternatives, put them in packages and choose with `use`.)
- **Class methods are called like functions** (`show(x)`, `money.show(x)`); the types decide the instance. (Methods of types, `xs.map(f)`, are another thing: see the grammar's Methods.) Methods of an exported class are visible with it. A method can also be passed as a value (`xs.map(show)`). Explicit type arguments decide what the arguments cannot: `empty[Int]()`. Type arguments are inferred locally, by unification: each call gives its type parameters unknowns, which the arguments (left to right, lambdas last), the expected type, and the lambdas' bodies decide. An empty `[]` or `{:}`, or `Option.None`, whose type is not known yet gets unknowns of its own: in `words.fold({:}, (m, w) => m.put(w, 1))`, `{:}` is a `Map` of two unknowns, and `m.put` in the body decides them. Inference stays within the outermost call: what is still unknown when it has been checked is reported where it came from (the `[]`, or the lambda parameter).
- **Methods can be function values** as `Type.method`, with the receiver first: `strings.map(String.byteLength)`, `lists.map(List.length)`, or `f: (List[Int]) => Int = List.length`. Generic method references infer their type arguments from the expected function type and fill class dictionaries as ordinary function values do. They retain their declared effects, close open callback parameters as pure, and reject `where` requirements. Sealed variants keep precedence over methods with the same name. An imported type's exported methods and visible exported extension methods can be referenced; a value's fields are not method references. Bound references use lambdas for now.
- **Missing and ambiguous instances are errors,** with hints: which `use` would bring one into scope, or which bound a generic function lacks.
- **Lowered to dictionary passing.** A call whose instance is known calls the instance's method directly; only generic code passes instances around.
- **`derive (Decode, Encode)`** on a record or sealed type asks the compiler to write the instances, named after the type (`CreateUserDecode`). They follow the same rules as written instances: in scope in their own package, used elsewhere with `use api.CreateUserDecode`. Each field needs an instance in scope. Only the prelude's `Decode` and `Encode` (JSON) can be derived so far.
- **JSON:** the prelude has a `Json` sealed type, `parseJson`/`renderJson`, the classes `Decode` and `Encode` with instances for the basic types, `Option`, `List`, and `Json`, and `decodeJson[T]`/`encodeJson[T]`. Records are objects, including empty records: their derived decoder accepts objects and rejects other JSON kinds, and their encoder produces `{}`. A sealed value is an object whose `"type"` names the variant (a variant without fields may be just its name, `"Free"`); a missing `Option` field is `None`. A `DecodeError` says where (`.items[1].qty`) and what went wrong.
- **Instances on constrained types:** `instance decodePort: Decode[Port]`, with `type Port = Int where between(1, 65535)`, is used for values known to be ports: the fields of derived instances whose where clauses include the instance's; calls whose arguments of the type parameter are all declared ports (a parameter, binding, or field with that where clause): `encodeJson(port)`; and calls with an explicit constrained type argument: `decodeJson[Port](text)`. The most specific instance wins (`Int where positive` over `Int`). Its methods assume the constraints of parameters of the type, and must promise them of results (`fn decode(json: Json): Port | DecodeError`), which the facts checker verifies.
- **Constrained type arguments are facts, by parametricity.** A generic function gets values of a type parameter only from its arguments of that type and from the instances of its bounds. So with `decodeJson[Port](text)`, arguments of type `T` must be ports, every instance for `T` that can produce values must promise `Port` (or it is an error), and the result's `T` member is then proven a port. Functions whose other parameters could produce `T` values (`map`'s `f: (A) => B`) cannot take a constrained type argument.
- **A derived decoder checks the where clauses** of the fields it decodes, so a decoded value is proven, and needs no further checks:

```
type SignUp = {
  name: String where nonEmpty and maxBytes(20),
  age: Option[Int where between(13, 150)],
} derive (Decode, Encode)

r: SignUp | JsonError | DecodeError = decodeJson(line)   // DecodeError { path: ".name", message: "must be nonEmpty" }
```

- **Not yet:** classes with several type parameters, superclasses, default methods, `where` clauses on methods, and choosing an instance at a call site.

### Open questions

- Scala-style placeholder shorthand for lambdas (e.g. `_.name`), or always named parameters?
- Can a sealed type's variants be spread over several files in one package, or must they sit in one declaration?
- How is an instance passed explicitly at a call site, when narrowing imports is not enough (e.g. `xs.sorted(using byName)`)?
- Instance-dependent collections: a `Set` or `Map` built with one ordering or hash instance could later be used with a different one, and would then silently misbehave. Should collections capture their instance when they are built, so that cannot happen?

## 3. Contracts and knowledge (in progress)

> The examples below show the direction; some of their syntax is still a sketch. What the compiler implements is listed under "Decided so far".

### Decided so far (implemented)

- **Predicates are declared with `pred`:** `pred between(x: Int, lo: Int, hi: Int) { x >= lo && x <= hi }`. A predicate is an ordinary function returning `Bool` (callable in code), whose first parameter is the value it is about.
- **`T where p and q(args)`** constrains parameters, results (also single members of a union result: `Int where positive | NotPositive`), record fields, constrained aliases (`type Port = Int where between(1, 65535)`), and typed bindings. Predicate arguments are constants or parameter names (`hi: Int where atLeast(lo)`).
- **Fact sources:** a predicate call in an `if` condition (in the branch it guards), guards that end in `return` or `panic` (for the code after them), `&&` and `||` (for their right side), `!`, a function's own requirements, a callee's promised result (also through `?` and `match` on a validated union, where the fact holds only for the promising member), field declarations, typed bindings, and `trust p(x)`.
- **Facts are found by identity.** A fact about `x` also holds for `y = x`, and for field paths like `u.age`. A computed value (`a - 1`) has no facts unless something promises them.
- **Promised results are verified** against every path of the body.
- **Requirements on constants are decided by running the predicate at compile time**, using the program's own code (including `unsafe go`): `transfer(0)` fails the build with "positive(0) is false". This works for any predicate, and for literals made of constants too: `xs: List[Int] = []; xs.first()` fails with "notEmpty([]) is false", `greet(User { name: "bob", age: 12 })` with "adult(User { name: "bob", age: 12 }) is false".
- **Generic predicates:** `pred notEmpty[T](xs: List[T]) { !xs.isEmpty() }` applies to every list. The prelude has it, with `fn (xs: List[T] where notEmpty) first[T](): T`, which needs no `Option`; `prepend`, `append`, and `split` promise `notEmpty` results.
- **OR:** `x: Int where positive or zero` needs one of the alternatives; `and` and `or` mix only with parentheses (`(positive or zero) and small`). A `||` condition gives an OR fact. An OR obligation may be proven by different alternatives on different branches, and a known OR fact is used by cases: a goal that follows from each alternative follows from the fact.
- **Inference rules** say what follows from what: `rule weaker(x: Int, a: Int, b: Int) { atLeast(x, a) and a >= b => atLeast(x, b) }`. Premises are predicate calls on the rule's variables (constants allowed after the first argument) and conditions on them using only operators and constants, which are decided at compile time. A rule may have several premises and conclusions; rules chain, and cycles between them are harmless. Rules are trusted, like `trust`, so `bork test` gives each one a property test: it tries many values of the variables (every combination of simple and extreme values, or a fixed random sample of them) and reports a counterexample where the premises hold but a conclusion does not.
- **Derived results:** a function that declares no promise still passes on what its body proves. With `fn clamp(x: Int): Int { if (positive(x)) { x } else { 1 } }`, `retry(clamp(n))` proves `positive`: every path of the body is checked against the obligation. A path returning a parameter is checked at the call site, for the argument. Helpers chain, in any declaration order. A declared promise is still the way to make a fact part of the contract (and is verified).
- **Facts inside type arguments:** `List[Int where positive]` constrains every element, `Option[String where nonEmpty]` the value if there is one. A fact written where it would not be checked (on Map keys or values, a member of a union that is not a result, a function type, a rule variable, a lambda parameter, a type pattern, a constructor name using a constrained alias, or a type argument held inside another type) is a compile error, never silently dropped. A list literal is checked element by element (`[1, 0]` fails with "positive(0) is false"), `Option.None` needs nothing, and a name bound by `Option.Some { value: v }` has the facts of the value. Rules apply to elements too. Constrained type patterns do not test predicates; match the base type and guard with the predicate inside the arm instead. To construct a constrained alias, use its base constructor in an annotated binding (`p: PosPoint = Point { x: 1 }`); the binding checks the predicate.
- **Predicate parameters:** a function parameter can be the predicate: `fn (xs: List[T]) filter[T](keep: (T) => Bool): List[T where keep]`. What the argument checks is then known: `xs.filter(positive)` is a `List[Int where positive]`, and `xs.filter(x => positive(x) && small(x))` has both facts. `find` refines its `Option` the same way.
- **Facts flow through generic functions.** A generic function cannot make values of its type parameters, so the ones in its result come from its arguments: what holds for all of those holds for them. `positives.head()` holds a positive number, and so do `reverse`, `take`, `positives.concat([5])`, and so on; a lambda's parameter gets the facts of the values the function can hand it (`positives.map(p => transfer(p))`). This holds for `unsafe go` generic functions by trust, like their signatures.
- **Test mode checks what is trusted.** `bork test` runs the tests with runtime checks of `trust` statements and of what `unsafe go` functions promise (through list elements, Option values, and fields), so trusted facts that drift from the truth are caught by tests: "validate promised a result that is positive, but returned 0".
- **Property tests come from facts.** `where` clauses already say which inputs are valid, so `test "transfer scales" (amount: Int where positive) { ... }` runs on generated values that meet them (list elements' and record fields' facts too), shrinks a failing case to a simpler one with the same facts, and prints the seed that reproduces it. `bork test --auto-properties` property-tests the functions whose promises are trusted rather than proven (`unsafe go`, `trust`): their promises hold, and they don't panic, for generated arguments. It is opt-in until effects mark functions pure, which will then be tested by default.
- **Snapshot tests** are cheap regression tests: `assertSnapshot(render(invoice))` compares the value's text with a file in the package's `snapshots` directory, and a failure shows a line diff. `bork test --update` writes the snapshots that are missing or different, as the compiler's own golden tests do with `-update`, so the change is reviewed in version control.
- **Diagnostics** name the requirer, the parameter or field, and the predicate, and suggest a guard or a declaration. A failing constant from inside a helper says where it came from: "sometimes can return 0, and positive(0) is false".

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

Generic functions that *add* knowledge, with a predicate parameter (implemented):

```
fn (xs: List[T]) filter[T](keep: (T) => Bool): List[T where keep]

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

- **Predicates are pure, and this is checked** (see [Effects in signatures](#effects-in-signatures-proposal)). A predicate whose answer could change for the same value (e.g. `isOpenNow(store)`, which reads the clock) would produce facts that go stale, so a predicate cannot call anything that uses an effect. The rule is: **effects produce values, and facts are about values.** For example, read `now = time.Now()` once, then use `isOpenAt(store, now)`. (`unsafe go` code is trusted to be as pure as its signature says.)
- **Facts about outside systems are facts about a point in time.** A fact can describe the outside world (a row exists, a file is present), but it is true as of when it was established. Keeping that current is the developer's job, not the compiler's. bork is aimed at typical web backend services, where a request reads its inputs, decides, and writes within a short window. Treating what it loaded as a fixed snapshot is the right model there. Concurrent changes elsewhere are handled with the usual backend tools (transactions, idempotency, optimistic locking), not by the proof system.
- **Compile-time evaluation only runs pure code**, since it runs predicates, apart from what `unsafe go` functions claim.
- **No widening syntax.** Facts are forgotten simply by passing a value where a less constrained type is expected (`Int where positive` to an `Int` parameter). No `x as Int` is needed.

### Open questions

- **Inline predicates:** only named predicates (`positive`), or also inline expressions (`where it > 0`)? Inline expressions require the compiler to recognise equivalent expressions.
- **Exported return types:** do callers see only the facts a signature declares, or also facts the compiler derives from the body? A proposal: exported functions expose only the declared facts (the signature is the contract), and private functions may expose derived ones.

## Resources and scopes

Outside resources (files, sockets, database connections, transactions, locks) are the one place where "facts only grow" is under pressure: the handle value never changes, but closing it changes the world it refers to. bork handles this with scopes, in the style of ZIO, so the open state can never end while a resource is still reachable.

### Decided (v0.1)

- **Resources belong to scopes.** Opening a resource requires a scope. A resource can be attached to one or more scopes, and it is **closed when the last of them closes**. There is no manual `close`, so there is neither use-after-close nor forgot-to-close.
- **Scopes are blocks.** `scope s { ... }` opens a scope, which closes when the block ends. Finalizers run in reverse order of acquisition (LIFO), like Go's `defer`.
- **Scope values can be passed down, never escape.** A scope can be passed as a function argument, and follows the same lifetime rules as resources: it can be returned, stored in a record, or captured by a lambda only where the result cannot outlive the scope.
- **Using a resource requires proof of an open scope managing it.** Every access to a resource is a proof obligation: the compiler must be able to prove, locally, that the resource is attached to a scope that is still open at that point. It is resolved backwards like any other fact. Only scopes visible locally count. If some other scope elsewhere keeps the resource alive at runtime, that does not help the proof.
- **Otherwise the resource is "possibly released".** When the last locally known scope of a resource has ended, the variable is marked possibly released at type level, and any use is a compile error. Runtime reference counts decide when a resource is actually closed; the compiler only ever relies on what it can prove.
- **Ordinary calls need no annotations.** A synchronous call runs entirely inside the caller's scope, so a function can simply take `conn: Conn`. Only escapes need checking: returning a resource, storing it in something longer-lived, or handing it to a goroutine.
- **Attaching happens where the resource is provably alive.** `attach` adds a scope fact, so it is only allowed where the resource is already known to be attached to an open scope. A goroutine therefore gets a resource attached at handover, never after it has started.
- **Reference counting falls out of the design.** Within one call stack, scopes nest, so the last scope is simply the outermost one. Across goroutines, a resource attached to several scopes stays open until all of them have closed, which is shared ownership without a separate `shared_ptr`-style type.
- **Scopes are passed explicitly in v0.1.** Acquiring functions take the scope as an ordinary argument. Implicit scope passing may be added later.
- **How the compiler checks it (implemented).** Every value has a lifetime: the set of scopes that must all be open for it to be usable. A resource returned by a function that was given a scope belongs to that scope; so does anything that holds it (a record, a list, a union, a lambda that uses it). Parameters belong to the caller's scopes, which outlive every scope the function opens. Three checks follow: a value whose scope has ended may not be used ("possibly released"); a function or lambda may not return a value of a scope it opened; and an `unsafe go` function given a scope may keep its other arguments until the scope closes (as `onClose` does), so they must live at least as long. Immutability, and having nowhere global to put values, make these the only escape routes.
- **Resource types are declared, and made in Go.** `type File = resource` declares an opaque handle. `unsafe go` functions create its values (`File{handle: f}`) and register finalizers with the scope (`s.Defer(...)`). The prelude has `onClose(s, f)`; file resources and operations are in `bork/fs`.

```
fn main() = scope app {
  pool = openDbPool(cfg, app)?             // lives as long as the app
  serve(pool, app)
}

fn handle(req: Request, pool: DbPool) = scope request {
  conn = pool.connect(request)?            // closed when the request scope ends
  file = fs.Open(path, request)?
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
- **Structured concurrency (decided, implemented):** goroutines started inside a scope finish before the scope ends, so they can use its resources with no extra attaching. `spawn(s, () => work())` starts a task of scope `s` and gives a `Task[T]`; `await(task)` waits for its result; `launch(s, () => ...)` starts work that gives no value. When a scope ends, however its block ends, it cancels itself, waits for its tasks, and then runs its finalizers. A task that panics panics `await`, or, if no one awaited it, the scope's routine when the scope closes. Lifetimes apply as to any value of a scope: a task cannot be returned from its scope or used after it, and its work may only use what lives as long as the scope. 
- **Finalizer failures and scope policies (implemented):** every finalizer of a scope runs, even when others fail; what failed (finalizers, and tasks that failed without being awaited) is raised as one panic once the scope has closed (`finalizer 4 failed; and then: finalizer 2 failed`). How a scope ends is configured with policies, values of the prelude's sealed type `ScopePolicy`, given after `with`: `scope s with taskTimeout(100), cleanupTimeout(500), logFailures() { ... }`. By default a scope waits for its tasks and finalizers for as long as it takes, and raises failures. `taskTimeout(ms)` bounds the wait for the tasks to stop after the scope is cancelled, overriding whatever cleanup time a task would take on its own: tasks still running then are *orphaned* (they keep running, and may find the scope's resources closed), while the scope's finalizers still run, on its own routine. `cleanupTimeout(ms)` bounds the wait for each finalizer; one that takes longer is orphaned, and the scope goes on closing. `logFailures()` logs failures at error level (through Go's `log/slog`) instead of raising them; a failing orphaned task is always logged so. Orphaning is logged at warning level.
- **Attaching (implemented):** a task of an outer scope may outlive the scope a resource was opened in. `shared = attach(conn, app)` keeps `conn` open until `app` closes too: a resource closes when the last scope it is attached to closes (reference counting, per resource). `attach` is allowed only where the resource is provably alive, and gives it as a value of `app`, which a task of `app` can use. Without it, handing the resource to such a task is a lifetime error that suggests `attach`. Resources made in `unsafe go` register how they close with `s.Own(...)`, which is what makes them attachable.
- **Channels (implemented):** `channel[T](s, capacity)` makes a channel owned by scope `s`, which closes it when it closes (so, like a task, a channel cannot leave its scope). `send(ch, x)` gives `Unit | Cancelled | Closed`, `receive(ch)` gives `T | Cancelled | Closed`, `closeChannel(ch)` closes it (buffered values can still be received), and `received(ch)` collects values until it closes. Operations stop with `Cancelled` when the channel's scope is cancelled; sending to a closed channel gives `Closed` rather than panicking.
- **Cancellation through scopes (implemented):** a scope carries what Go's `context.Context` does. `cancel(s)` cancels it, `cancelAfter(s, ms)` sets a deadline, a task that panics cancels its scope (so its siblings stop), the scope's end cancels it, and a scope nested in another (in the same function) is cancelled with it. Cancellation is cooperative: tasks see it at cancellation points, `delay(s, ms)` and `checkpoint(s)` (both `Unit | Cancelled`, so `checkpoint(s)?` stops a loop), and channel operations. A cancelled scope still waits for its tasks. **The scope does not decide how its tasks stop:** its end is the same signal whether the block finished, returned early, or panicked, and each task chooses what to do with it: stop at once (a worker waiting in `delay` or on a channel), clean up first, or finish its work (code that never checks for cancellation runs to the end). Work the block needs done is awaited before the block ends. In `bork/http`, each request has a scope, cancelled when the client goes away or the server's scope closes.

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
- **Effects in the outside world are not undone.** A message already sent or a half-written file is a consistency problem, not a resource leak. Scopes help: a transaction scope's finalizer rolls back unless the transaction was committed, so a crash mid-transaction rolls back automatically. Beyond that, the usual backend tools apply (idempotency, outbox).
- **Messages to a dead routine**, and messages still queued for it, need defined semantics once channels or mailboxes are designed.
- **A supervision API:** restart policies, escalation, and how supervisors relate to scopes and structured concurrency.

## Effects in signatures (proposal)

> **Accepted and implemented** (bork-ot9ki9): the syntax, effects in function types, open parameters and results, the prelude and standard library's effects, the check of every body against its declaration (with predicates pure, `main` and tests free, and unused effects of unexported functions reported), the `unsafe go` check, `unsafe` in `bork.mod`, and facts named after function arguments only for pure ones. The text below is the design as proposed; where the implementation settled a detail differently, the text says so.

Today any function can print, read files, call the network, or update shared state, and its signature does not say so. For code written by agents, the signature is what a reviewer checks, so it should state what the function can do to the world outside its arguments and result. Below `main`, a function that declares nothing does nothing outside them, and the compiler checks this. The exceptions are deliberate and few: logging, the runtime's own diagnostics, and `unsafe go` code (trusted, and allowed only where `bork.mod` says so).

### The two designs

**A. Capability parameters.** Authority is a value: `fn fetch(net: Net, url: String)`. `main` receives the capabilities and passes them down. Scopes already work this way: only a function given a `Scope` can open a resource in it.

- For: nothing new in the type system. Authority can be narrowed (a file system rooted at one directory, an HTTP client for one host), and tests can pass fakes (a fixed clock).
- Against: a lambda captures capabilities, so a function *type* no longer says anything. `(Int) => Int` may call the network through a captured `net`. Telling them apart needs Scala 3-style capture checking, which is heavy. This is exactly the case that matters for higher-order code and for predicates.
- Against: the capabilities have to be passed through every function between `main` and the one that uses them. In practice code bundles them into one `Sys` record that every function takes, and then signatures no longer show anything.
- Against: `println(console, ...)` on every line.

**B. Effect annotations, pure by default.** `fn fetch(url: String) uses net: Response | IoError`. A function without `uses` is pure. The compiler checks every body against its declaration.

- For: function types carry effects too (`(Int) uses net => Int`), so lambdas and function values are covered.
- For: a missing `uses` costs one word, and the compiler names it. Nothing is passed down.
- For: purity can be required where it matters. Predicates, rules, and compile-time evaluation can require pure code, which the facts model needs (see *Facts, purity, and the outside world*).
- Against: higher-order functions need effect polymorphism, which is the usual cost of effect systems. The rule below keeps that cost to zero annotations, the way Swift's `rethrows` does.

**Recommendation: B, and keep the capability values bork already has.** Scopes, resources, atoms, and channels stay ordinary values. The parameter says *which* resource or lifetime a function works on, and `uses` says *what kind* of outside action it takes. They answer different questions, and neither replaces the other. B also keeps the decided "no effect monads" rule: effects are checked annotations, code stays in direct style, and nothing changes in the generated Go.

### The effects

The set is small and fixed in v0.1: `io`, `net`, `clock`, `random`, and `state`. Libraries cannot declare their own effects. Each effect is coarse enough to annotate cheaply, and fine enough to answer the questions a reviewer asks.

| Effect  | Allows | Prelude and standard library |
|---------|--------|------------------------------|
| `io`    | standard streams, files, the process | `println` (built in), `eprintln`, `fs.Open`, `fs.Create`, `fs.ReadAll`, `fs.WriteTo`, `args`, `exit`, `log.Configure`, `env.Get`, `env.Require`, `env.All`, `env.Load`, `env.LoadJson` |
| `net`   | the network | `http.Listen`, `http.Wait`, `http.Get`, `http.Post`, `http.Send` |
| `clock` | time and waiting | `sleep`, `delay`, `cancelAfter`, `time.Now`, `time.Read` (a `time.Clock`'s `now` uses `clock`), `time.Sleep` |
| `random` | random numbers | none yet (the future random number functions) |
| `state` | state shared between tasks | `current`, `update`, `swap`, `send`, `receive`, `closeChannel`, `received`, `cancel`, `cancelled`, `checkpoint` |

- **Some functions have two effects:** `delay(s, ms)` and `cancelAfter(s, ms)` are `clock + state` (they wait, and they observe or cause cancellation).
- **Pure means deterministic, with no outside action.** Calling a function that uses nothing twice with the same arguments gives the same result, and has no observable effect beyond allocating memory, logging, and maybe panicking. That is why reading an atom, or checking whether a scope was cancelled, is `state`: the answer can change between two calls. One known exception: an unordered map (`m.unordered()`) lists its entries in an order that differs between runs, and listing it stays pure. Seeding that order per program would close the gap, if it turns out to matter.
- **The compiler's built-ins** are classified in the checker: `println` and `assertSnapshot` (which writes snapshot files under `--update`) are `io`, and `toString`, `panic`, the conversions, `assert`, and `assertEqual` are pure.
- **Making things is pure.** `atom(x)`, `channel(s, n)`, `spawn`, `launch`, `await`, `attach`, `onClose`, `scope` blocks and their policies, `http.Address`, `http.Text`, JSON, strings, lists, and maps use nothing on their own. (`spawn` and `onClose` take on the effects of the work they are given; see higher-order functions below.)
- **`panic` is pure.** It signals a bug, not an effect. The same goes for what the runtime does on its own when a scope ends: logging an orphaned task, or a failure under `logFailures()`, and the timing of `taskTimeout` and `cleanupTimeout`. These are runtime diagnostics, not actions of the code.
- **Logging is not tracked.** Writing logs with `bork/log` (`log.Info`, `log.Debug`, `log.Log`, ...) is allowed in any function, pure ones and predicates included, with no `uses`. It is also the way to trace a pure function while debugging. `log.Configure` is `io`, though: it decides for the whole program where logs go (stdout, say), so it belongs in `main`, and pure code cannot turn logging into printing. A log line added deep in a call chain should not ripple a declaration up through every caller, and logs do not change what a program computes. When compile-time evaluation runs a predicate that logs, the log output is discarded.
- **Randomness is its own effect**, `random`, beside `clock`: both make a function nondeterministic without touching anything outside the process. The standard library has no random numbers yet; the effect is reserved for them, and the `unsafe go` check below uses it.

### Syntax

```
fn describe(u: User): String { ... }                          // pure
fn save(path: String, text: String) uses io: Unit | IoError { ... }
fn serve(addr: String) uses net + state: Unit | IoError { ... }
fn main() { ... }                                             // may use every effect
fn now() uses clock: Int unsafe go { ... }

type Job = { name: String, run: () uses io => Unit }         // a function type with effects
fn handler(store: Atom[Store]): (Request, Scope) uses state => Response { ... }
```

- **`uses` comes after the parameters**, before the result, in declarations and in function types alike, the way Swift writes `throws`. Effects are joined with `+`, as type parameter bounds are (`T: Show + Eq`); a comma would clash with the comma between parameters.
- **So it is never unclear what `uses` belongs to.** `handler` above is pure: it builds a function that touches state. Unions in results need no parentheses either (`(String) uses net => Response | IoError`).
- **`uses nothing`** marks a function-typed parameter that must be pure (see below), or a `main` that must be (see below). Elsewhere, leaving `uses` out already means pure, and `uses nothing` is allowed but redundant.
- **Grammar changes:** `FuncDecl` and `MethodSig` get an optional `Uses` after the parameter list, and `FuncType` one before its `=>`: `Uses = "uses" ( "nothing" | Ident { "+" Ident } )`. On a declaration, it comes before the result and before `where` (`fn f(x: Int) uses io: Int where positive unsafe go { ... }`).
- `uses` and `nothing` are keywords only in these positions, and the effect names are only names there, so a package or value may still be called `state` or `random`.
- **An unexported function may not declare effects its body never uses.** This matches unused imports, and keeps `uses net` meaning "may call the network". Exported functions may declare more than they use, to keep their API stable when the body changes. So may class methods, `unsafe go` functions (their bodies are not checked), and function types.

### Higher-order functions: open parameters

A function-typed parameter written without `uses` is **open**: it accepts a function with any effects. A call is then charged the declared effects plus the effects of the arguments given to open parameters. This is Swift's `rethrows`, applied to every effect:

```
fn (xs: List[A]) map[A, B](f: (A) => B): List[B]              // the prelude's method

fn transform[A, B](xs: List[A], f: (A) => B): List[B] { xs.map(f) }

names = users.map(u => u.name)                              // this call is pure
names.forEach(n => println(n))                              // this call uses io
```

- **Inside the body, an open parameter can be called freely.** Its effects belong to the caller.
- **Inside the body, an open parameter's effects are unknown**: one effect variable, `open`, standing for whatever the caller passes. The implementation models it that way. A lambda that calls an open parameter has `open` among its effects, like the parameter itself.
- **What has `open` among its effects can only flow to another open position:** an open parameter of a call, or the open result of its own signature. `open` fits in no fixed set of effects, so such a value cannot be stored in, or passed as, a function type with fixed effects, pure or not (a `run: () uses io => Unit` field could then hold a function that uses `net`). Nor can it become a type argument or an element: `[f]`, `Option.Some { value: f }`, `identity(f)`, `() => f()` in a record field. To store a function, close the parameter: `fn job(name: String, f: () uses io => Unit): Job`.
- **An open parameter can be returned.** A function type without `uses` in the *result* of the same signature is open too: `fn compose[A, B, C](f: (A) => B, g: (B) => C): (A) => C` gives a function that uses what `f` and `g` use. A value returned in an open result may use `open` and nothing else, so `fn wrap(f: () => Unit): () => Unit { () => { println("x"); f() } }` is an error: there is no way to write "io plus what f uses". (Write `wrap(f: () uses io => Unit): () uses io => Unit` instead.)
- **A call is charged the effects of its open arguments, unless the function's result is open.** Then they are carried in the result's type instead: `compose(a, b)` is pure, and the function it gives uses what `a` and `b` use, as a hand-written `handler` closure does. In exchange, a function with an open result may not call its open parameters itself, only return them in its result (or pass them to calls whose results it returns). This keeps the rule a matter of signatures, not of what a body does.
- **Which positions are open:** a parameter (receivers included) or result whose type is a function type written without `uses`, also through a type alias (`handler: Handler`, with `type Handler = (Request, Scope) => Response`). Nothing else: not `List[() => Unit]`, not a union such as `((A) => B) | None`, and not the parameters or results inside a function type (`(A) => (B) => C` has an open result `(B) => C` only at the top). So a middleware type such as `((Request) => Response) => (Request) => Response` takes pure handlers only, and one that wraps effectful handlers names their effects.
- **A function with open parameters used as a value** (`g: (List[Int], (Int) uses nothing => Int) => List[Int] = transform`) has its open positions closed as pure: `g` is `(List[Int], (Int) uses nothing => Int) => List[Int]`, all pure. Calling `transform` directly keeps it open.
- **Generic code needs nothing extra:** if `T` is `() uses io => Int`, the effect is part of `T`, so `identity`, `head`, and `Option[T]` carry it. Since effects are erased in the Go output, a type pattern on a function type (`f: () => Unit`) may only match a union member known statically to have the same effects, as facts are treated.
- **`uses nothing` makes a parameter strictly pure.** `fn update[T](a: Atom[T], f: (T) uses nothing => T) uses state: T` is how the prelude says "f may run more than once, so it must not do anything".
- **A fact named after a function argument needs a pure argument.** `xs.filter(keep)` gives `List[T where keep]`, and that fact, "keep holds", only means something if `keep` gives the same answer every time. So an effectful `keep` still filters, but the result is a plain `List[T]`. The facts its body proves with predicates are kept either way (predicates are pure): `xs.filter(x => { println(x); positive(x) })` uses `io` and still gives positive elements. Inside a function whose parameter is open, a fact named after that parameter holds on the same condition: the prelude's method `fn (xs: List[T]) filter[T](keep: (T) => Bool): List[T where keep]` promises the conditional fact, and at each call the fact is kept only if that argument is pure.

The prelude's list, `Option`, and map methods have open function parameters. Only `update` and `swap` (strictly pure `f`) and the effectful functions in the table change.

**Records of functions made in Go carry their effects.** The prelude's `Atom` and `Channel` are records whose fields are functions made by `unsafe go` (`currentFn`, `sendFn`, ...), visible to any code that has the record. Their types must say what they do, `currentFn: () uses state => T`, or a program could call `store.currentFn()` with no `uses state`. The general rule: an `unsafe go` function that returns or builds function values types them with their effects, as part of the signature bork trusts. (`Task`'s `wait: () => T` stays pure: the work's effects were charged at `spawn`.)

### Lambdas, methods, and classes

- **A lambda's effects are inferred from its body.** Nothing is written. A lambda checked against a function type with `uses X` may use at most `X`. A lambda checked against a pure function type must be pure. One passed to an open parameter may use anything, which charges the call.
- **Named functions as values** have the effects they declare: `lines.forEach(line => println(line))` uses `io`.
- **Function types are ordered by their effects.** A function that uses less fits where more is allowed: a pure `(Int) => Int` can be passed as `(Int) uses io => Int`, but not the other way round.
- **Methods** declare `uses` as functions do. `fn (c: Client) fetch(url: String) uses net: Response | IoError`.
- **Class methods** may declare `uses`, and an instance's method may use at most what the class declares. The prelude's classes (`Eq`, `Ord`, `Decode`, `Encode`) are pure, so their instances, including derived ones, must be pure.

### `unsafe go`

- **An `unsafe go` function declares its effects, and bork trusts them,** as it trusts the rest of the signature. `fn now() uses clock: Int unsafe go { ... }`.
- **`unsafe` is not an effect.** If it were, every caller of the prelude's `length` and `map`, which are `unsafe go`, would be "unsafe", and the word would mean nothing. The trust boundary is the declaration.
- **Which packages may contain `unsafe go` is declared in `bork.mod`:** `unsafe "example.com/shop/ffi"`. The prelude and the standard library always may. Every other package that has an `unsafe go` body is an error ("package example.com/shop/money has unsafe go, but bork.mod does not allow it"). So new Go code shows up in review as a change to `bork.mod`, and code in other packages cannot do what its signature does not allow, without exceptions. The module's root package is named by the module path (`unsafe "example.com/shop"`). A program without a `bork.mod` cannot use `unsafe go`; `bork run` on a single directory still works, it just has to be all bork.
- **A check for honest declarations:** an `unsafe go` body that uses a Go name with an obvious effect must declare that effect. The check looks at the names used (functions and variables), not at the imports, since `time.Duration` alone is pure:
  - `io`: `os` (except its error types and values), `os/exec`, `os/signal`, `syscall`, `io/ioutil`, and `fmt.Print*`, `fmt.Fprint*`, `fmt.Scan*`, `fmt.Fscan*`;
  - `net`: `net` and `net/...`;
  - `clock`: `time.Now`, `Since`, `Until`, `Sleep`, `After`, `AfterFunc`, `Tick`, `NewTimer`, `NewTicker`;
  - `random`: `math/rand`, `math/rand/v2`, `crypto/rand`;
  - `state`: `go` statements, channel operations, `sync`, `sync/atomic`.

  A name counts against the function's own declared effects, or, inside a function value it returns or builds, against that value's declared type (`Atom`'s `currentFn: () uses state => T`). It is an error, not a warning, and applies to user packages; the prelude and the standard library are written by hand to the same rules, and reviewed instead (their runtime plumbing, such as `bork/log` choosing `os.Stdout`, would trip it). It is not a proof (Go code can reach the world in other ways), but it catches an `unsafe go` helper that quietly does I/O while its signature says it is pure.

### Tasks and scopes

- **`spawn`, `launch`, `onClose`, and `http.Listen` take open functions,** so a call is charged with the effects of the work it starts, even though the work runs on another goroutine or later. "This function can cause X" is the question a reviewer asks, and structured concurrency means the work belongs to a scope the caller holds.
- **Scope operations that observe or change cancellation are `state`:** `cancel`, `cancelled`, `checkpoint`, and with `clock`, `delay` and `cancelAfter`. Opening a scope, its policies, and `attach` are pure.
- **A request handler's type shows what it does:** `http.Listen` takes `handler: (Request, Scope) => Response`, which is open, so `Listen(addr, s, handler(store))` uses `net` plus whatever the handler uses.

### `main` and tests

- **`main` and test bodies may use every effect**, with nothing declared (`main` is the one function where no `uses` does not mean pure). They are the program's roots: everything a program does starts there, so a declaration on them would say little, and tests print, read fixtures, and start servers. The functions they call are still checked against their own declarations, which is where the guarantee matters: below `main`, a function that declares nothing does nothing. `main` may still declare `uses`, which is then checked like any other function's (useful for a program that should be provably free of, say, network access), and `fn main() uses nothing` declares a pure program.
- **`main` cannot be called or used as a value**, so no other function can reach "every effect" through it.

### Predicates, rules, and compile-time evaluation

- **Predicates and rules must be pure.** `pred` and `rule` cannot declare `uses`, and calling an effectful function in one is an error. A fact from a predicate that reads the clock goes stale. This was a convention before ("effects produce values, and facts are about values"), and it now becomes a check.
- **So compile-time evaluation only runs pure code** (apart from what `unsafe go` declarations claim), and the open question of predicates doing I/O at build time is settled.
- **Default parameter values** are already closed values, so they are pure.

### No inference for declared functions

As with requirements ("no inferred preconditions"), a function's effects are written in its signature and never climb silently to its callers. Lambdas are the exception, because they have no signature to write them in. Signatures stay complete, packages check on their own, and recursion needs nothing special.

### Diagnostics

Each error names the effect, the call that needs it, and the fix. Where the fix is a signature change, the message ends with the `uses` to declare, and `bork check --json` carries it as a text edit that replaces or inserts the `uses` clause (see [the diagnostic format](diagnostics.md)). The hints on other errors are planned:

```
main.bork:14:3: describe uses io (it calls println), but its signature allows no effects; declare it: uses io

main.bork:30:5: serve uses state (it calls http.Listen, with a lambda that calls update), but its signature allows only net; declare it: uses net + state

main.bork:62:3: handler returns a function that uses state (it calls update), but its result type allows no effects
  hint: declare it: fn handler(store: Atom[Store]): (http.Request, Scope) uses state => http.Response

main.bork:20:11: field run takes a function that uses nothing, but this lambda uses io (it calls println)
  hint: declare the field's effects: run: () uses io => Unit

main.bork:41:7: process uses io (it calls map, with a lambda that calls println), but its signature allows no effects; declare it: uses io

main.bork:22:21: update needs a function that uses nothing (it may run f more than once), but this lambda uses io (it calls println)
  hint: print after update returns, with the value it gives

main.bork:8:3: isOpenNow uses clock (it calls now), but predicates must be pure, or their facts could go stale
  hint: take the time as a parameter: pred isOpenAt(store: Store, now: Int)

main.bork:17:24: f may use whatever effects its caller passes, so it cannot be stored in field run, which allows only io
  hint: give f the field's effects: fn job(name: String, f: () uses io => Unit): Job

main.bork:5:50: save declares net, but never uses it

main.bork:4:10: now's unsafe go body uses clock (it uses time.Now, which reads the clock or waits), but its signature allows no effects; declare it: uses clock

money/money.bork:12:1: package example.com/shop/money has unsafe go (round), but bork.mod does not allow it; if it is meant to, add the line: unsafe "example.com/shop/money"
```

(`now` in the predicate example stands for a clock function the standard library does not have yet.)

- **The reason chain is one step deep.** It names the direct call (and, for an open parameter, the lambda's call). The callee's own signature says why that callee needs the effect.
- **When the fix is a signature change, it comes as an edit**, so an agent can apply it as written. A future `bork fix` could apply these mechanically.

### Migration

- **The prelude and the standard library** get `uses` on the functions in the table above, and `uses nothing` on the function parameters of `update` and `swap`. Their list, `Option`, map, string, and JSON functions do not change.
- **Examples:** `main` needs nothing. Helpers that print, read files, wait, touch atoms, or call the network get what they use: `step` in `accounts` (`io`), `readFile` in `signup` and `countFile` and `report` in `wc` (`io`), and the server helpers. In `signup_api`: `slow`, `route`, and the test helper `get` get `uses clock + state` (`cancelAfter`, `delay`); `show` gets `uses io`; `post` gets `uses io + net`; `serve` and `demo` get `uses io + net + clock + state`. In `http_server`: `list`, `show`, `finish`, `create`, `remove`, `route`, and the test helper `request` get `uses state`; `handler` stays pure and returns `(http.Request, Scope) uses state => http.Response`; `call` gets `uses io + net`; `serve` gets `uses net + state` (it passes the handler to `http.Listen`), and `demo` `uses io + net + state`. `hello`, `calculator`, `orders`, `payments`, and `users` do not change at all: their helpers are pure, which shows that the pure core of a program is the default.
- **Test cases:** only the helpers that print, read files, wait, touch atoms, or serve need `uses`; cases that do all of that in `main` and `test` blocks do not change. Expected outputs of passing cases do not change. Error positions in failing cases shift where a declaration's line gains `uses ...`. The effect check runs after type checking, like the lifetime check, and only when type checking found no errors, so cases that fail with type errors keep their messages. The cases with `unsafe go` get a `bork.mod` that allows it. Their `unsafe go` helpers get what they do: `connect` in `attach` uses `io` (`fmt.Println`), `stubborn` in `scope_policies` uses `clock` (`time.Sleep`), `seed` in `time_env` and `shout` in `unsafe_go_ok` use `io`, and in `http_client` `openServer` uses `net + state` (`httptest`, a channel) and `started` uses `clock + state` (`select`, `time.After`). In `time_env`, `report` gets `uses io + clock`. The change is made by applying the compiler's own hints, and each golden diff is still checked by hand.
- **The check lands in one step** (the check, the prelude, and all migrations in one PR, after the syntax and types PRs), because a half-annotated prelude would make every program fail.

### Implementation plan

1. **This proposal**, as its own PR.
2. **Syntax:** `uses` on function declarations, methods, class methods, and function types, plus `uses nothing`. Parse it, keep it in the AST, show it in types in messages, and update `grammar.md`. No checking yet.
3. **Types:** function types carry an effect set. Assignability follows it, lambdas infer their effects, and open parameters are instantiated per call.
4. **The check, the prelude, and the migration:** a separate pass over the typed tree (`internal/check/effects_check.go`, run before the lifetime pass), with the diagnostics above, the prelude and standard library annotated, and every example and test case migrated.
5. **Predicates, rules, and compile-time evaluation required pure; facts named after function arguments only for pure ones; the `unsafe go` call check.**
6. **`unsafe` in `bork.mod`.**
7. **Docs:** fold this section into the decided parts of this document, and update `grammar.md` and the README.

### Open questions

- **Should cancellation have its own effect?** It is `state` here, so a function that only waits with `delay(s, ms)` shows `state`, though it shares no data. Moving cancellation to `clock` (renamed `time`), or to its own effect, would keep `uses state` meaning "shares data with other tasks".
- **Should `io` be split** into `console`, `files`, and `process`? This proposal starts coarse. Splitting later only adds effects, so it is cheap to do once real code shows the need.
- **Effect aliases** (`effects Backend = io + net + state`) for long lists on handlers? Not needed while there are five effects.
- **Test doubles:** with effects in place of capabilities, a test cannot hand a function a fake clock. Today the workaround is a function parameter (`now: () uses clock => Int`). Effect handlers, which let a test say how an effect is answered, would be the principled answer later.

## Go interop (proposal)

> **Proposal, under review** (bork-e6abw5). Steps 2 and 3 of the plan below (basic bindings and opaque Go types) are implemented; the rest is not yet. If it is accepted, it replaces "importing Go packages as bork modules is not planned" under *Compilation target and Go*, and the matching line under *Explicitly not in v0.1*. It also scopes "no mutable state" (under *Core values*, *Language fundamentals*, and *Explicitly not in v0.1*) to bork values: an opaque Go value (below) is Go state that bork holds but cannot change or see into. It builds on the [effects proposal](#effects-in-signatures-proposal) (`uses`, and the `unsafe` opt-in in `bork.mod`) and on the [Go helper API](std-go.md).

Today Go types never cross into bork. An `unsafe go` body sees bork values in their generated form (the table at the top of the prelude), and calling a Go function means writing that glue by hand, unchecked until the Go compiler runs over the generated code. This proposal makes the common cases declarations that the compiler checks against the real Go packages, with `go/types`:

```
// An opaque Go type: bork can hold it and pass it on, nothing more.
type Request = go "*net/http.Request"

// A record that mirrors a Go struct, converted at the boundary.
type Url = go "net/url.URL" {
  scheme: String
  host: String
  path: String
  rawQuery: String
}

// Go functions and methods, bound by name. No body to write.
fn QueryEscape(s: String): String unsafe go "net/url.QueryEscape"
fn ParseUrl(s: String): Url | GoError unsafe go "net/url.Parse"
fn Getenv(key: String) uses io: String unsafe go "os.Getenv"
fn UserAgent(r: Request): String unsafe go "(*net/http.Request).UserAgent"
```

### Decided in this proposal

- **Three declarations, no new keywords.** `type X = go "<Go type>"` names an opaque Go type. `type X = go "<Go type>" { fields }` is a record that mirrors a Go struct. `fn ... unsafe go "<Go function>"` binds a Go function or method; it is an `unsafe go` function whose body is a name instead of code.
- **Bindings are `unsafe go`.** The ticket sketched `extern fn`. A binding runs Go code that bork does not check, exactly as an `unsafe go` body does, so it is the same boundary with the same word: greppable, allowed only in packages that `bork.mod` lets use `unsafe go`, and declaring its effects with `uses`, which bork trusts.
- **A binding's effects are checked against a table of Go's standard library**, kept with the compiler, per function rather than per package: `os.Getenv` is `io`, `net.Dial` is `net`, `time.Now` is `clock`, and `net/url.Parse` and `(*net/http.Request).UserAgent` are pure. Binding a listed function without its effects is an error. The table is finer than the name check for `unsafe go` bodies (which flags all of `net/...`), since a binding names exactly one function. Functions not in the table (third-party packages, and methods called through an interface such as `(io.Reader).Read`, whose target is not known) are trusted, like `unsafe go` bodies.
- **Go types are named by declarations, not inline.** There is no `go.Type["net/http", "Request"]` in signatures. A declaration gives the type a bork name and a place for a doc comment, and signatures stay bork.
- **Everything is checked against the real Go package**, at `bork check` time, not first by the Go compiler on generated code. Errors are reported at the bork declaration, and show the Go signature.
- **Bindings are checked more than bodies are.** A binding's wrapper is generated, so everything it converts from Go is checked, facts included: a binding can only break its promise through its effects and through what the Go code does to opaque values.
- **bork targets 64-bit platforms.** Go's `int` and `uint` are 64 bits wide there, so they are `Int` and `Uint64`. (This is true of every target bork builds for today; this proposal makes it a rule.)
- **A minimum Go version.** The generated `go.mod` names the oldest Go release bork supports, and the build fails clearly with an older Go. Bindings are checked against that release's standard library, so a binding to a function added later is an error at `bork check`, not a `go build` failure in generated code.

### Syntax

```ebnf
TypeDecl   = "type" Ident [ TypeParams ] "=" ( Fields | Sealed | "resource" [ GoName ] | GoName [ Fields ] | Type ) [ Derive ] .
GoName     = "go" StringLit .               (* go "*net/http.Request" *)
Field      = [ Doc ] Ident ":" Type [ "=" Expr ] [ GoTags ] .
GoTags     = "go" "{" [ GoTag { Sep GoTag } [ Sep ] ] "}" .   (* go { json: "port,omitempty", short: "p" } *)
GoTag      = Ident ":" StringLit .
GoBody     = "unsafe" "go" ( "{" { GoImport } GoStatements "}" | StringLit ) .
```

- `go` stays an identifier everywhere else. After `=` in a type declaration, `go` followed by a string literal is a `GoName` (a type named `go` cannot take a string, so there is no ambiguity). The lexer's raw-Go mode after `unsafe go` starts only at a `{`; `unsafe go "os.Getenv"` is ordinary tokens.
- A field's default is an expression, which ends at the end of the field (newline or `,`) or at `go {`, since an expression never continues with an identifier.
- `Doc` is the `//` comment lines directly above a field. The parser keeps them, like the other doc comments it will keep for tooling.

### Naming Go things

The string after `go` is a Go type or function written with its full import path, the way Go's own tools print them:

- types: `"net/http.Request"`, `"*net/http.Request"`, `"io.Reader"`, `"example.com/x/v2.User"` (the package is everything up to the last `.` after the last `/`);
- functions: `"os.Getenv"`, `"crypto/sha256.Sum256"`;
- methods, as Go method expressions: `"(*net/http.Request).UserAgent"`, `"(time.Time).Unix"`. The receiver is the method's first parameter in the bork signature (a bork method can bind it too: `fn (r: Request) userAgent(): String unsafe go "(*net/http.Request).UserAgent"`). A method with a pointer receiver cannot be bound on an opaque type that is not a pointer: it would change a copy.

Only exported, non-generic package-level functions, methods, and named types can be bound. A field path such as `(*net/http.Request).Header.Get` is not a function and is rejected: write an `unsafe go` body. Bindings take no type parameters, since the Go function has none. Generic Go functions and types are an open question.

A Go package outside Go's standard library must be a declared Go module dependency: of a standard package (bork-8zh4yy), or, later, of the user's module in `bork.mod`.

### Opaque Go types

`type Request = go "*net/http.Request"` makes `Request` a bork type whose values are Go values of that type.

- **Which Go types:** a named Go type (`time.Time`, also one with a basic underlying type such as `time.Duration`), a pointer to a named type (`*net/http.Request`), or a named interface (`io.Reader`). Not a predeclared type (`go "string"` is just `String`), and not an unnamed composite type (`go "[]int"`). Declaring a named type opaque is a choice: without the declaration, `time.Duration` converts as an `Int` (the mapping below), and with it, a binding whose bork type is the opaque `Duration` passes it through as it is. Which applies is decided by the bork type written in each binding, so there is no ambiguity.
- **The Go type is the identity.** Two declarations of the same Go type, in the same package or in two packages, are the same bork type, as if one were an alias of the other. So two libraries that both bind `*net/http.Request` can pass values to each other. Opaque types have no home package for method lookup: methods on them are found in the code's own package, then among the exported methods of its imports, then the prelude's.
- **Resource classification must agree.** Declaring the same Go type as both a resource and a non-resource anywhere in one program is an error, with both declarations named. Declarations of the same classification unify. A binding returning a Go resource needs exactly one `Scope` parameter to own its `Close` method.
- **Shared, not copied.** Passing a `Request` around passes the Go value (for a pointer, the pointer), in a box: a one-field struct the compiler generates per Go type. Always boxed, so that a type switch on a union matches it exactly even when generic code put it there as a type parameter's value. In `unsafe go` bodies, `_borkGo(r)` gives the Go value (`*http.Request`) and `_borkOpaque[Request](v)` boxes one (added to the [helper API](std-go.md)); bindings do both themselves.
- **Never nil.** A binding's result of an opaque pointer or interface type is checked (see *nil results* below). `Option` of an opaque pointer or interface type maps to that same Go type, not to a pointer to it, with `nil` as `None`. So "no null" still holds outside `unsafe go` bodies.
- **Exact in unions.** A union is Go's `any`, matched with a type switch, and the box makes a type pattern match exactly that opaque type: an interface such as `fmt.Stringer` does not match a bork record that happens to have a `String` method, and an `*os.File` in `Reader | Writer` is whichever the code put in.
- **Bork can only hold and pass them.** No fields, no `==`, no `Decode`/`Encode`. `toString` shows `<go *net/http.Request>`. Everything else goes through bindings or `unsafe go` bodies.
- **Not immutable, so no facts.** A Go value can change behind bork's back, which breaks the core idea's "a fact stays true". A predicate cannot take an opaque type as a parameter, no `where` clause can apply to one, and a record, list, map, option, or union holding one loses `==`, `Decode`, `Encode`, and facts about it, as values holding functions do today. Bindings that change a Go value, or read one that can change, should declare `uses state`; bork trusts the declaration, as it trusts every `unsafe go` signature.
- **Not race-free.** Bork values can be shared between tasks because they never change. Opaque values can change, and whether that is safe is up to the Go type (`*http.Client` is safe for concurrent use, `*bytes.Buffer` is not). This is the honest cost of holding Go objects, and the reason they are declared one by one.
- **What the caller must close is a resource.** `type File = resource go "*os.File"` is a resource as today (opened in a scope, closed when the scope closes, `attach`, lifetimes), with a handle of that Go type. A binding whose bork result is such a resource takes a `Scope` parameter; the wrapper registers the value's `Close()` with that scope (`s.Own`), and ignores the error `Close` returns, as the prelude's files do. If the Go function also takes a `context.Context`, the same scope is passed as it (the mapping below); if not, the `Scope` parameter is bork's only, and is not passed to Go. A binding whose Go result type has a `Close` method, but whose bork result is not a resource, is an error, since nothing would close it: so `*sql.Rows` from `(*sql.DB).QueryContext` is a resource, cannot be used after its scope closed, and is always closed. A closeable Go value that something else owns (a request body the server closes) is passed through an opaque, non-resource type that `unsafe go` code made, not through a binding.
- **Interfaces work as in Go.** A binding whose parameter is opaque type `A` checks that `A`'s Go type is *assignable* to the Go parameter's type (`go/types.AssignableTo`), so a `File` (`*os.File`) can be passed to a binding of `io.Copy(dst io.Writer, src io.Reader)` declared with `File` parameters. There is no subtyping between opaque types in bork itself; a value is converted with a binding, not implicitly.

### Records that mirror Go structs

`type Url = go "net/url.URL" { scheme: String, ... }` is an ordinary bork record (immutable, facts, `==`, `copy`, `derive`), whose fields are checked against the Go struct, and which is converted to and from it at the boundary.

- **Fields are matched by name.** A bork field `name` matches the exported Go field `Name` (first letter upper-cased); if there is none, the one exported field whose name is equal ignoring case (`id` matches `ID`, `httpPort` matches `HTTPPort`). Several matches, or none, is an error. Unexported Go fields cannot be matched. Fields of embedded structs are matched through their promoted names.
- **The record may leave Go fields out.** Converting to Go leaves them at Go's zero value, as a Go struct literal does; converting from Go drops them. The record lists what bork needs, not the whole Go type. (Open question: should a mirror that is converted *to* Go be required to cover every field? Zero values are idiomatic Go, but silent.)
- **Each field's type follows the basic mapping below**, recursively. A nested Go struct needs its own mirror record (`owner: User` where `User = go ".../x.User" { ... }`), and the mirror's Go type must be the field's Go type, or its pointer.
- **Fields of embedded pointer structs** are not matched (they may be `nil` in Go). **A field of a plain record type** (not a mirror) needs that record to `derive (GoStruct)`, and the struct it generates is the Go field's type.
- **The Go struct's own tags are its own.** A mirror record cannot add `go { ... }` tags; they are for generated structs (below).
- **The Go side is not the bork representation.** The record keeps its own generated struct (with its `String` method, its field names, `int64` for `Int`), and the compiler generates the two conversion functions. In `unsafe go` code, `_borkToGo(v)` gives the Go struct, and `_borkFromGo[T](g)` gives `(T, []GoValueError)`, with every error found (added to the [helper API](std-go.md)).
- **Directions are checked where they are used.** A mirror whose Go struct has an array field converts only from Go: a binding that would pass one to Go is an error, at that binding.
- **Generic Go structs** cannot be mirrored yet.

### The basic mapping

Bindings and mirror records convert values by their types, at the boundary. A pair of types not in this table does not convert, and the declaration is an error.

| bork | Go | to Go | from Go |
|------|----|-------|---------|
| `Int8` ... `Int`, `Uint8` ... `Uint64` | `int8` ... `int64`, `int`, `uint8` ... `uint64`, `uint` | copy, into any Go integer type that holds every value of the bork type (`Int8` to `int32`, `Uint32` to `int64`) | copy, from any Go integer type; checked at run time when the bork type does not hold every value of the Go type (`int64` to `Int32`, `int` to `Uint64`, `uint64` to `Int`) |
| `Float32`, `Float` | `float32`, `float64` | copy; `Float32` to `float64` too | copy; exact types only (no float narrowing, no integer to float) |
| `Bool` | `bool` | copy | copy |
| `String` | `string` | shared (Go strings are immutable) | shared |
| `List[T]` | `[]T'`, `...T'` (variadic) | a new slice, never `nil` (`[]` stays an empty slice) | copied, each element converted; `nil` is `[]` |
| `List[T]` | `[N]T'` (arrays) | not allowed | copied |
| `Map[K, V]` | `map[K']V'` | a new map, never `nil` | copied into an unordered map (`m.unordered()`), since a Go map has no order; `nil` is `{:}` |
| `Option[T]` | `*T'`, or for an opaque pointer or interface type, that type | `None` is `nil`; `Some` is a pointer to a fresh copy | `nil` is `None`, otherwise a copy of what it points to |
| `T` (not an `Option`) | `*T'` | a pointer to a fresh copy | a copy; `nil` is a `GoValueError` |
| a mirror record | its Go struct, or a pointer to it | field by field, as above | field by field; the record's facts are checked |
| an opaque type | a Go type it is assignable to (parameters), or the same type (results) | shared | shared; `nil` is a `GoValueError` |
| `Unit` | no result | | |

- **Named Go types convert by their underlying type**, in every row: `time.Duration` as `int64`, `os.FileMode` as `uint32`, `url.Values` (a `map[string][]string`) as a `Map[String, List[String]]`, `net.IP` (a `[]byte`) as a `List[Byte]`. Unless the bork type is an opaque declaration of that named type, which passes it through.
- **`uintptr`, `complex64`/`complex128`, channels, `unsafe.Pointer`, `any`, and Go functions do not convert.**
- **Map keys** must be numbers, `String`, or `Bool` (or named Go types of them), so that the key conversion is one to one: two different Go keys never become the same bork key, and the reverse. Records, options, and opaque types cannot be keys at the boundary.
- **`Scope` converts to `context.Context`** (parameters only): the Go function gets the scope's context (`_borkScopeContext`), so it stops when the scope is cancelled. Most I/O in Go's libraries takes a `ctx`, so this is the usual way a binding gets one.

- **Nothing bork gives Go can be changed under bork.** Lists, maps, structs, and pointers are copied into fresh Go values, so a Go function that writes to its argument writes to its own copy. Strings are shared because neither side can change them. Only opaque values are shared, and those are Go's, never bork's.
- **Nothing Go gives bork can be changed under bork either.** Lists, maps, and records are copied out of Go values, so a Go library that keeps and later changes a slice it returned does not change a bork `List`. The cost is a copy per boundary crossing, linear in the size of the bork value it makes. Bork values are trees, so a Go value that reaches one part from several places is copied once per place; for a Go graph with much sharing, the tree can be far larger than the Go value (exponentially, in the worst case), so such data should stay opaque. `[]byte` is a `List[Byte]`, copied element by element; `Bytes` (bork-uwhbyt) gets a direct `[]byte` mapping once it exists.
- **Converting to Go never fails.** A parameter's bork type must convert losslessly: an `Int8` can be passed as a Go `int32`, an `Int` cannot be passed as one (declare the parameter `Int32`, and the caller converts with `toInt32`).
- **Converting from Go can fail**, when a number does not fit (`int64` to an `Int32` result), a pointer or interface is `nil` where bork has no `Option`, a fact does not hold (a mirror record's `where` clauses, and the binding's own result facts, `: Int where positive`), or a Go value is cyclic (a mirror record that can contain itself, through `Option` or `List`, can meet a cyclic Go value, which a tree cannot hold; the conversion tracks the pointers on its current path to find it). Then the bound function returns the prelude record `GoValueError { path: String, message: String }` (`path` is where in the value it failed: `result.items[2].count`). Whether a conversion can fail is known from the types, so a binding whose result can fail must have `GoValueError` in its result type, and the error says so, with the corrected signature as the hint. A binding whose result cannot fail does not mention it. This is the same rule as `toInt8(x)` returning `Int8 | OutOfRange`: a check bork cannot prove is a value the caller handles.

### Bindings: parameters and results

A binding is checked parameter by parameter, and its result against the Go function's results:

| Go results | bork result |
|------------|-------------|
| none | `Unit` (no result type) |
| `T` | `T'` |
| `error` | `Unit | GoError` |
| `T, error` | `T' | GoError` |
| `T, bool` | `Option[T']` (the `comma ok` form: `false` is `None`) |

- **`GoError { message: String, goType: String }`** is a prelude record: a non-nil Go `error`, with its `Error()` text and its dynamic Go type (`*fs.PathError`). It is a value, not a panic, because Go functions return errors for expected failures. A non-nil interface holding a nil pointer is non-nil, as in Go; if its `Error()` panics, the message says so instead. To turn particular errors into particular bork records (`fs.ErrNotExist` into `NotFound`), write an `unsafe go` body; a mapping clause on bindings is an open question.
- **With `T, error`, a non-nil error wins**, and `T` is dropped: a function whose `T` matters even on error (`io.Reader.Read`'s byte count) needs an `unsafe go` body.
- **nil results.** When `T` is a pointer or interface (a `*T` for a mirror record or a non-`Option` value, or an opaque pointer or interface type), a `nil`:
  - is `None` if the bork result is an `Option` there;
  - otherwise, with `T, error`, and a nil error too, is a `GoError` ("nil result without an error"): that is a broken Go function rather than bad data, and `ParseUrl` above stays `Url | GoError`;
  - otherwise (a plain `T` result, or `nil` deeper inside a value) is a `GoValueError`, and the result type must have it.
- **The members after the leftmost are the binding's failures, in any order**: `T' | GoError | GoValueError`. A Go function with an `error` result must have `GoError` in the bork result (bork does not drop errors silently); one without must not.
- **Other result shapes** (three results, or two that are not `T, error` or `T, bool`) cannot be bound: write an `unsafe go` body that builds a record. (A common Go shape is `T, *Response, error` in API clients; an open question below.)
- **Function-typed parameters and results** (Go callbacks) cannot be bound yet. They need a bork function wrapped as a Go `func` with conversions on every call, and an answer to which effects such a callback may have. That is the next step after this proposal.
- **Default parameter values and `where` clauses on parameters** work on bindings as on any function: they are bork's, checked before the call. Facts promised on the result are checked by the wrapper, as above.
- **Compile-time evaluation** treats a binding as it treats an `unsafe go` function.

### Struct tags, field docs, and defaults

Go libraries that work by reflection (`encoding/json`, sql scanners, boa for `bork/cli`) need Go structs with exported fields, tags, and sometimes descriptions and defaults. Bork records cannot give them that today: their generated structs have unexported, lower-case fields, and their field names are what `unsafe go` code relies on (the prelude's table), so they stay as they are. Instead:

- **`derive (GoStruct)` gives a record a Go struct**, with an exported field per bork field (`httpPort` becomes `HttpPort`), mapped as in the table above (`Option[T]` becomes `*T`), and the conversions both ways, the one from Go checking the record's facts and collecting every error. For a mirror record, the struct is the mirrored Go type; for any other record, the compiler generates it. Fields whose types do not convert (unions, sealed types, and functions) make the derive an error, and so does a mirror record with a Go array field, which converts only from Go. Opaque fields are shared, as in the table. Note that `encoding/json` on the generated struct uses `HttpPort` as the key unless a tag says otherwise, unlike the derived `Encode`.
- **`GoStruct` is a prelude class with no bork methods**: it exists for `[T: GoStruct]` bounds in standard packages, whose `unsafe go` code gets its dictionary, as `bork/env` gets `Decode`'s. Like every derived instance, it is written with `derive` and brought into other packages with `use`. The dictionary, in the [helper API](std-go.md): `New()` (a pointer to a fresh Go struct, with the record's defaults filled in), `FromGo(p)` (`(T, []GoValueError)`), `ToGo(v)`, and the record's field schema (as `bork/env`'s `_borkDecodeFields` has it, plus each field's Go name, doc, default, and tags).
- **Fields of a generated struct can carry Go struct tags**, written as a `go { ... }` clause after the field's type and default, with bork string values and no Go quoting: `port: Int go { json: "port,omitempty", short: "p" }` becomes `` Port int64 `json:"port,omitempty" short:"p"` ``, in the written order. On a record without `derive (GoStruct)`, or on a mirror record, they are an error.
- **Fields can have doc comments** (`//` lines directly above the field), given in the schema.
- **Record fields can have defaults**, with the same rules as parameter defaults (closed values): `type Options = { port: Int = 8080, verbose: Bool = false }`. A record literal may leave defaulted fields out, derived `Decode` uses the default for a missing field, and `New()` fills them in. This is a language change of its own (record literals currently name every field), useful without Go, and sequenced as its own step below.
- **The compiler writes no library's tag names itself.** It does not know that boa reads `descr` and `default`. `bork/cli` reads docs and defaults from the schema and gives them to boa, either through boa's API or by building a struct type with those tags at run time (`reflect.StructOf`), so the language does not depend on one library's conventions. User-written tags go into the struct as written.

```
type Options = {
  // Address to listen on.
  host: String = "localhost" go { short: "H", env: "HOST" }
  // Port to listen on.
  port: Int where validPort = 8080 go { short: "p" }
  // Extra log output.
  verbose: Bool = false
} derive (GoStruct)
```

### Checking against Go: how

- **The compiler loads the Go packages a program binds**, with `golang.org/x/tools/go/packages` (types from export data, through Go's build cache), in a temporary module with the same `go.mod` and `go.sum` the build will use. So the check sees exactly the Go code the build compiles, and third-party packages resolve as they will in the build (bork-8zh4yy).
- **The checker does not run Go itself.** It asks an interface for the Go type of a name (`check.GoTypes`), which the driver implements with `go/packages`; tests can give it a fake.
- **The standard library's bindings are checked when the compiler is built**, by a test, and the compiler carries their signatures. So a program whose own packages have no bindings never starts the Go tool for checking, and `bork check` still works without Go installed. Building always needs Go, as now.
- **Checking is part of type checking**, after declarations are collected and before bodies, since a binding's signature is its declaration. Errors come out with the rest, with stable diagnostic codes in a `bind.` category (`bind.no-such-func`, `bind.mismatch`, `bind.result-shape`, ...), apart from `go.error`, which is for Go code in `unsafe go` bodies.
- **The generated code is a wrapper per binding**: the Go function the compiler writes for it converts the arguments, calls the bound function, and converts the results, exactly as a hand-written `unsafe go` body would. Nothing new reaches the runtime besides the conversion helpers.

### Diagnostics

```
url.bork:4:1: ParseUrl is bound to net/url.Parse, which returns (*url.URL, error), but its result type has no GoError
  hint: fn ParseUrl(s: String): Url | GoError unsafe go "net/url.Parse"

url.bork:7:1: QueryEscape is bound to net/url.QueryEscape(s string) string, but parameter s is Int
  hint: a Go string needs a bork String

url.bork:3:3: Url mirrors url.URL, which has no exported field Hostname (or hostname, ignoring case)
  hint: url.URL has a method Hostname; a mirror record has only the struct's fields

stock.bork:12:1: Fetch returns an Item, whose facts (count: Int where nonNegative) are checked when converting from Go, so the result may be a GoValueError
  hint: fn Fetch(id: String) uses net: Item | GoError | GoValueError unsafe go "example.com/stock.Fetch"

env.bork:2:1: Getenv calls os.Getenv, which reads the environment, but declares no effects
  hint: fn Getenv(key: String) uses io: String unsafe go "os.Getenv"

db.bork:9:1: Query returns a *sql.Rows, which has a Close method, but Rows is not a resource, so nothing would close it
  hint: type Rows = resource go "*database/sql.Rows"

pred.bork:5:13: predicate fresh takes a Request, which is a Go value that can change, so a fact about it could go stale
```


### Implementation plan

1. **This proposal**, as its own PR.
2. **Bindings of functions over basic types (implemented):** the syntax, `check.GoTypes` with `go/packages` in the driver, numbers, `String`, `Bool`, `Bytes`, `List`, `Map`, `Option`, `Unit`, `GoError`, `GoValueError`, and the generated wrappers. Standard-library Go packages only. Facts on a binding's result are an error for now; checking them moves to step 4, with the mirror records' facts.
3. **Opaque Go types (implemented)**: identity, boxing, nil checks, method bindings, assignability, `Scope` as `context.Context`, and resources of Go types.
4. **Mirror records**, conversions both ways with fact checks, and the `_borkToGo`/`_borkFromGo` helpers in [std-go.md](std-go.md).
5. **Record field defaults and field doc comments** (useful without Go: literals and `Decode`).
6. **`derive (GoStruct)`**, `go { ... }` tags, and the schema. This is what `bork/cli` (bork-pkk136) builds on; until it lands, `bork/cli` uses a hand-written Go struct.
7. **Third-party Go packages**, with Go module dependencies (bork-8zh4yy), and the standard library's bindings checked by a test.
8. **Docs:** fold this section into the decided parts, and update `grammar.md`, the prelude's table, and the README.

Effects in signatures and bodies have landed (bork-ot9ki9). Bindings declare their effects with `uses`, like `unsafe go` bodies. The standard-library binding effects table and the `bork.mod` gate are still to come.

### Open questions

- **Error mapping on bindings:** `unsafe go "os.Open" errors { fs.ErrNotExist => NotFound }`, through `errors.Is`/`errors.As`, instead of an `unsafe go` body?
- **More result shapes:** `T, X, error` (API clients that return a response beside the value), dropping `X` or giving it as an opaque value?
- **Callbacks:** bork functions passed to Go as `func` values (with conversions per call, and effects declared on the parameter's function type). Needed for `sort.Slice`-style and handler APIs.
- **Go errors from bork:** a `GoError` passed back to a Go `error` parameter (`errors.Is`, wrapping). It would need the record to carry the original Go error, which is not a bork value. Out of scope for now.
- **Generic Go functions and types** (`slices.Index`, `atomic.Pointer[T]`): instantiate them from bork type arguments?
- **Go constants and variables** (`math.MaxInt32`, `os.Args`): bind them as zero-parameter functions?
- **Full mirrors:** require a mirror converted to Go to list every exported field?
- **Opaque values and effects:** should every binding that takes an opaque *pointer* type need `uses state`, rather than trusting the declaration? And should opaque values that are not safe for concurrent use be kept out of `spawn`?
- **User packages with Go module dependencies**, declared in `bork.mod` and gated like `unsafe` (part of bork-8zh4yy, later).

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
- **Scala-style string interpolation, not printf:** `s"Hello, $name! Next year: ${age + 1}"`. Any value can be interpolated, rendered as `toString` renders it. Plain `"..."` strings never interpolate, so `$` needs no escaping there.
- **Comments** are `// ...` and `/* ... */`. **String literals** use double quotes with Go's escape sequences.
- **Identifiers cannot start with `_`.** That prefix is reserved for the compiler.
- **Records have named fields:** `type User = { name: String, age: Int }`, built as `User { name: "Ada", age: 36 }`. There are no positional constructors.
- **Sealed variants are always qualified:** `Shape.Circle { radius: 1 }`, `Shape.Empty`, also in patterns. Unqualified variants may come later, through imports.
- **Pattern matching is `match (x) { pattern => value, ... }`.**
- **Changed copies use `copy`, with nested paths:** `u.copy(age = 37, address.city = "Oslo")`. This replaces Scala's nested `copy(address = u.address.copy(city = ...))`.

### Numbers

- **Fixed-width integers, as in Go.** `Int` is a 64-bit integer with Go's wrapping arithmetic.
- **Go's sized numbers, with Go-like names:** `Int8`, `Int16`, `Int32`, `Int64`, `Uint8`, `Uint16`, `Uint32`, `Uint64`, `Float32`, `Float64`. `Int` is the same type as `Int64`, `Float` the same as `Float64`, `Byte` the same as `Uint8`, and `Rune` the same as `Int32`. Each sized integer wraps on overflow.
- **Numbers never mix implicitly.** `Int + Int8` is a compile error. Literals and arithmetic on literals are exact compile-time constants that take their type from where they are used (`x: Int8 = 100`); a constant that does not fit is a compile error. Unlike Go, a constant is computed as its type computes, so `x: Float = 1 / 3` is `0.333...`, not `0`.
- **Conversions are explicit free functions:** `toInt8(x)`, `toFloat(x)`, and so on. A conversion that always fits (widening, or any integer to a float) returns the plain type. One that may not fit (narrowing, signed to unsigned, float to integer) returns `Target | OutOfRange`, so `?` or `match` must handle it. A constant is converted at compile time. Later, facts can let a value proven to be in range convert directly.
- **Floats print as floats:** `3.0`, `1000000.0`, `0.25`, with an exponent only for very large or small values (`1e+21`). The digits are the shortest that read back as the same value.
- **Facts respect overflow.** `a > 10` and `b > 10` do not prove `a + b > 10`, because the sum can wrap. Arithmetic implications need upper bounds that rule out overflow.
- **Native big integers.** An arbitrary-precision integer type is built in, for when wrapping is not acceptable.

### Equality

- **Structural equality is generated** for records and unions. Equality compares values, not identities.
- **Comparing incompatible types is a compile error.**
- **Constrained and unconstrained versions of a type are comparable.** `Int == (Int where positive)` is fine, because both are `Int` values.

### Packages and modules

- **Go style.** A directory is a package, a module file at the root names the module, imports use module paths, and there are no circular package dependencies.
- **Visibility follows Go:** names starting with an upper-case letter are exported. This applies to top-level declarations (functions, predicates, types); the fields and variants of an exported type are visible wherever the type is.
- **How it works (implemented).** `bork.mod` at the module's root holds `module example.com/shop`, and then a line `unsafe "example.com/shop/ffi"` for each package allowed to contain `unsafe go` (see [Effects in signatures](#effects-in-signatures-proposal)). A file starts with its imports, `import "example.com/shop/money"` or `import cash "example.com/shop/money"`, and refers to the package's names as `money.Cents`, `money.Amount`, `money.Currency.Eur`. Unused imports are errors, import cycles are rejected, and an import name cannot be shadowed. For now the compiler checks the whole program at once (with each package's names kept apart), rather than each package against summaries of its imports.
- **Messages name types as the code would:** another package's types are qualified (`found money.Cents`), relative to the package the error is in.
- **Time and configuration (implemented):** `bork/time` has immutable Unix-nanosecond instants, durations, formatting/parsing, checked arithmetic, scope-cancellable sleep, and a `Clock` whose `now` function tests can replace. `bork/env` distinguishes missing and empty variables, snapshots the environment, and loads derived records with `Load[T: Decode](prefix)`: `httpPort` maps to `PREFIX_HTTP_PORT`, strings are literal, other values use JSON syntax, absent Option fields are None. All invalid/missing variables are collected, including field fact violations; `LoadWith` injects a reader for tests. Custom decoders can use `LoadJson` for a JSON configuration variable. Derived record schemas expose general field metadata to std Go code ([helper API](std-go.md)).

- **Standard packages** are imported as `bork/name` and ship with the compiler (no `bork.mod` needed). So far: `bork/http`, a server whose lifetime is a scope (`http.Listen(addr, s, handler)` serves until `s` closes, each request on its own goroutine, with a scope of its own that the handler gets), a scope-cancellable client (`http.Get(url, s, timeoutMs = 0)`, `http.Post(url, contentType, body, s, timeoutMs = 0)`, `http.Send(method, url, headers, body, s, timeoutMs = 0)`), whose optional millisecond timeout covers both the request and reading the response body; headers are maps of names to value lists, preserving repeated values, and transport/cancellation/timeout failures return `IoError`, and helpers (`http.Text`, `http.JsonReply`, `http.Segments` for matching paths with list patterns). Status codes are facts: `http.Text(42, "x")` does not compile. See [examples/signup_api](../examples/signup_api/main.bork). The built-in, persistent `Map[K, V]` (`{"a": 1}`) has methods and needs no import; it is insertion-ordered (the default), sorted (`m.sorted()`), or unordered (`m.unordered()`, a hash map whose printing sorts numeric and string keys by value, and other keys by their text, with mixed kinds grouped). List and map keys use structural equality independently of their text. And `bork/log`, structured logging through Go's `log/slog`: `log.Info(msg)` or `log.Info(msg, {"user": name, "age": 37})` (also `Debug`, `Warn`, `Error`), attributes a `log.Attrs` (`Map[String, String | Int | Float | Bool]`) kept in order, loggers that carry attributes (`log.With(attrs)`, `log.Extend`, `logger |> log.Log(level, msg, attrs)`), and `log.Configure(log.Defaults().copy(...))` for the level, text or JSON, stdout or stderr, and timestamps. The runtime's own records (orphaned tasks, a scope's failures under `logFailures()`) go through the same configuration.
- **Standard packages can depend on pinned Go modules (implemented):** native Go module/checksum declarations (`go-deps.mod` and `go-deps.sum`) ship with the compiler. Generated builds include only loaded std packages' declarations, use `-mod=readonly`, and use Go's module cache. Warm caches support `GOPROXY=off`; cold offline builds fail clearly. Dependency sources are not vendored. See [the std Go dependency contract](std-go.md).

- **SQL (implemented):** `bork/sql` owns database pools and transactions as scope resources, using `database/sql` with pinned pure-Go SQLite and pgx Postgres drivers. `OpenSqlite(dataSource, s)` and `OpenPostgres(dataSource, s)` connect and ping. `Begin(db, transactionScope)` rolls back when that scope closes unless `Commit` succeeds. `Exec` binds typed scalar/Bytes/Null parameters, and `Query[T: Decode]` decodes column-name row objects into proven values. Operations use their connection or transaction owner's cancellation context; attachment rebinds cancellation to the destination scope through the general resource hook. `QueryJson` exposes the JSON representation for custom decoding. SQLite uses one connection; Postgres uses Go's default pool. See [examples/sql](../examples/sql/main.bork).

- **Another package's functions promise only what their signatures say.** Facts derived from a function's body are used within its package, but not by importers, so a package's body can change without breaking them. (This settles the "exported return types" question below.)
- **Inference rules apply everywhere** their predicates are used, and `bork test` property-tests the rules of the package being tested.

### Open questions

- **A decimal or money type** in the standard library. Backends need exact decimal arithmetic, and `Float` is wrong for money.
- **Big integer literals and conversions** between `Int` and the big integer type.

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
6. Go interop: `unsafe go` function bodies (above); checked bindings to Go types and functions (proposal above)
7. Tooling: the `bork` CLI, formatter, tests, and modules (packages are decided, above)

See also [roadmap.md](roadmap.md) for the implementation plan.

## Explicitly not in v0.1

- Mutable state of any kind
- Importing Go packages as bork modules (calling Go goes through `unsafe go` bodies and bindings)
- `recover`
- Erlang-style routine isolation and supervision

### Binary data and encoding

`Bytes` is built-in immutable binary data, represented by a distinct named Go
byte slice. It has content equality and may be a map key. It remains distinct
from `List[Byte]`, including when both appear in a union. Conversions copy at
Go boundaries; bork has no mutation operations. Construction uses `bytes` or
`utf8Bytes`, without new literal syntax. `utf8String` validates UTF-8 and
returns a union error. Immutable access uses `length`, `isEmpty`, `get`,
`toList`, `slice` (bounds errors are unions), and `concat`. Printing uses
`Bytes(lowercase hex)` rather than guessing text.

`bork/encoding` supplies hex and padded standard/URL-safe base64 encoders and
parsers. Invalid encodings return `ParseError` without partial data. The
base64 parsers enforce zero trailing padding bits and accept CR/LF.

### Filesystem package

`bork/fs` owns `File`, moved from the prelude, and provides whole-file binary
Read/Write/Append, scoped Open/Create/CreateNew, ReadAll/WriteTo on handles,
text compatibility helpers ReadAllText/WriteText, and streaming ForEachLine.
File data is Bytes; caller-visible slices never mutate. Text compatibility
helpers preserve the former prelude behavior; validated UTF-8 decoding is
explicit. Streaming has no Scanner line-size limit. File resource lifetimes
remain enforced, including imported resources returned through generic wrappers.

Directories have lexical listing/walking, MkdirAll, Remove/RemoveAll, Rename,
and Stat with size, time.Instant modification time and kind. Walk and Stat do
not follow symbolic links. Host filepath helpers join and split paths and
resolve absolute paths. TempFile/TempDir resources close and remove themselves
at scope end, including early-return and panic cleanup; TempDir removes its
contents. Cleanup is best effort, as existing scope file finalizers were.
Files create with mode 0666 and directories with 0777, modified by the host
umask. Write truncates; Append appends; CreateNew reports Exists for an
existing path. Operational errors are typed union values NotFound,
PermissionDenied, Exists or IoError with the path and underlying message.
There are no new language constructs.


### CSV encoding

CSV has raw row parsing/writing and typed record parsing/writing through
`Decode`/`Encode`. Typed CSV uses the derived Decode field schema: String
fields are verbatim; numbers, booleans and compound fields use JSON. Empty
optional cells and missing optional columns become None. Nonempty optional
cells become Some, parsed exactly like the required field (no JSON quotes
for String). Encoding Some("") writes empty like None; Some("") cannot
round-trip and decodes as None. Nonempty JSON null in optional non-String cells
is rejected with a per-cell error; use an empty cell for None. The JSON bridge
cannot preserve Some(Json.Null), which encodes like None. Header names match
fields exactly; unknown,
duplicate and missing required headers are errors. Ragged typed rows are
errors. Facts and field types are checked before constructing records, with
all field errors collected in `CsvErrors` by row and column. Malformed CSV
returns `CsvError`; partial records are never returned. Encoding requires both
Encode and Decode for the schema and optional semantics. Zero-field records
are unsupported; empty input encodes to empty text. Raw CSV follows Go CSV
quoting, blank-line skipping, CRLF normalization and LF output, permitting
ragged rows. No new syntax is introduced.

### Processes and shutdown signals

`bork/process` starts argv commands without a shell. Run captures stdout and
stderr as Bytes and returns the exit code, including nonzero exits. Start gives
a scoped Process with repeatable Await, explicit Stop, and Pid. Arguments,
environment inheritance/replacement, working directory and optional Bytes stdin
are supported. Missing stdin is empty. Launch/wait errors are IoError;
explicit or owner cancellation returns Cancelled. Output is retained in memory.
A final resource cleanup cancels, kills and reaps the child, including a child
that was never awaited. Attachment selects the destination cancellation source.
Kill targets the child process, not its descendant process tree.
Args and Exit alias the prelude helpers. Process operations declare io + state;
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

### TCP and UDP sockets

`bork/net` uses Bytes for binary TCP and UDP data and scope-owned Connection,
Server and Socket resources. Dial opens TCP with scope cancellation and an
optional opening timeout. Listen serves each connection on a task in its own
scope; server cleanup cancels active connections and waits for handlers.
Handler errors/panics are logged and isolated. Connections support bounded byte
reads, validated UTF-8 lines, writes, address inspection and per-call deadlines.
Line timeouts preserve consumed prefixes for retry or byte reads. Eof marks
clean stream closure; cancellation is Cancelled and transport/timeouts IoError.
Failed writes can be partial. One reader and one writer may run concurrently.

UDP Bind and Send use numeric IP:port addresses; Receive returns a complete
datagram with its sender, including zero-length data. Resources follow their
owner's cancellation, with attachment rebinding the cancellation source. Pure
host/port helpers parse and join addresses; scope-aware Resolve returns sorted
IP strings. Timeouts and buffer sizes have checked facts; there is no new syntax.
TLS can be added later alongside HTTP.
