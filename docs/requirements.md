# bork requirements

> Living document. Each section is worked through in discussion and recorded here once agreed.
> **Decided** items are commitments for v0.1. Open questions are listed per section.

## Scope of v0.1

The toolchain includes `bork new <directory>` with `default`, `cli`, `http`, and `lib` templates, selected with `--template`. Each creates a module, source, a test, `.gitignore`, and a README; libraries include dependency manifests and no main. `--module` selects the import path; it defaults to `example.com/<directory name>`. Existing destinations are refused. Every template is checked, tested, and format-checked in CI.

The toolchain includes `bork fmt [paths...]`: one canonical whitespace format,
with two spaces for indentation and normalized spacing and blank-line grouping.
Existing line breaks, comment text, and raw Go bodies are retained. CRLF line
endings become LF, including line-comment endings; bytes inside block comments,
strings, interpolations, and raw Go bodies stay unchanged. `--check`
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
    Option.Some(n) => greet(n)     // n: String
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
- **Destructuring retains payload promises.** Matching a sealed member of a union preserves its declared payload facts, including fact aliases inside `Option[T]` returned by another package. The payload can be returned as the alias or bound to a constrained ambient with `with`.
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

1. **If it compiles, whole classes of bugs cannot happen.** No null or nil, no unchecked access to optional values, no exceptions, no non-exhaustive matches, no mutation of ordinary bork values, and no broken contracts outside explicit unsafe boundaries.
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
- **Ordinary bork values are immutable.** Opaque Go objects can hold shared state, and explicit state capabilities have declared effects. Facts cannot describe opaque values or values containing them. This is the foundation of the proof model: a fact proven about a value can never be invalidated later, so proofs are never lost to mutation. Opt-in mutable types may come in a later version.
- **Exhaustive matching is enforced.** A `match` that does not cover every case is a compile error. The compiler never inserts an implicit panic for a missing case.

### Errors and failure

- **No exceptions. At all.** There is no `throw`, no `try`/`catch`. Expected failures are ordinary values, returned as part of a union return type (see section 4).
- **Panics exist but are discouraged.** A panic means a bug, not an expected failure.
- **No `recover`.** A panic cannot be caught in bork code. Erlang-style isolation of routines (a failing routine brought down without taking the rest with it) is planned for later. See crash isolation and supervision under resources and scopes.

### Effects and concurrency

- **Side effects are allowed, and declared.** Reading and writing files, sockets, and so on are ordinary operations, and a function's signature says which kinds it does: `fn save(path: String, text: String) uses io: Ok | IoError`. A function that declares nothing is pure (see [Effects in signatures](#effects-in-signatures)).
- **No effect or state monads.** No `IO`, `State`, or time monads. Concurrency uses Go's goroutines (virtual threads), so sequential blocking code is the norm. Effects are checked annotations, not wrapper types: code stays in direct style.

### Compilation target and Go

- **Compiles to Go.** Go is purely a compilation target, chosen so bork gets Go's runtime, GC, goroutine scheduler, and cross-platform compilation without building its own backend.
- **Ordinary bork code does not call Go.** bork has its own standard library. The Go code the compiler generates can freely use Go's standard library under the hood; that is an implementation detail, invisible to bork programs.
- **`unsafe go` is the explicit boundary.** A function's body can be raw Go: `fn f(x: Int): String unsafe go { import "strconv"; return strconv.FormatInt(x, 10) }`. User packages must opt in through `unsafe "<package path>"` in `bork.mod`; the standard library and prelude are exempt. bork trusts the signature: the guarantees (immutability, no null, the declared result) hold outside these bodies, and inside them only as far as the Go code keeps them. The Go code sees bork values as the compiler represents them, which is documented in the prelude. Standard packages use a documented [Go helper API](std-go.md) for maps, options, and scope context/lifecycle operations, with a compilation test covering every helper. Go errors in the body are reported at their bork positions. Unsafe Go import lines accept file-local aliases (`import mathrand "math/rand/v2"`). Unaliased imports retain their shared namespace. Conflicting Go names receive a compiler diagnostic, alias references preserve local shadowing and checked effects, and package metadata supplies declared names even when they differ from path basenames. Importing Go packages as bork modules is not planned for v0.1; instead, bindings call Go functions directly, checked against their Go signatures (`fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`), and opaque types and mirror records are checked as described in [Go interop](#go-interop).
- **bork's syntax is independent of Go.** bork has its own grammar and its own hand-written parser. Go is only the language the first compiler is written in, and the first compilation target.
- **The compiler models bork, not Go.** The compiler's internal model holds the full language and all of its constraints; that is the product. Its Go output is a lowering, not a one-to-one mapping of bork types to Go types. It can drop knowledge once it has been checked (facts are erased entirely), and it can lean on runtime helpers and generated functions where that is simpler. Inspired by TypeScript: the type checker carries the guarantees, and the output just runs. Go is the first target; others may follow.

### General

- **Strict evaluation by default.** Explicit lazy bindings and fields are proposed below; ordinary local expressions remain eager; pure package values are memoized on first read.
- **A short propagation operator** (like Rust's `?`), so handling failures stays cheap. See section 4.
- **Bitwise integers.** `&`, `|`, binary `^`, unary `^x`, `<<`, and `>>` operate on all fixed-width integers (including Byte). Bitwise binary operands must have the same type. Shift counts may have a different integer type: unsigned counts are always allowed; signed counts must be proven nonnegative. Unproven signed counts are a compile error with a suggested guard. Runtime left shifts truncate to the left operand's width; signed right shifts preserve the sign, while unsigned right shifts fill with zero. Oversized shifts follow these rules without masking the count. Constant expressions are evaluated exactly and must fit their contextual type. Unary complement is `^x`, following Go; `~x` gets a replacement fix. Bool operands receive a diagnostic suggesting `&&`, `||`, or `!=`. Shifts and `&` bind like multiplication; `|` and binary `^` bind like addition.
- **Integer bit helpers.** `bork/bits` supplies imported methods on every fixed-width integer. Population count and leading/trailing zero counts carry width-specific range facts. Rotations accept signed counts modulo the width; reversal preserves width. Bit indices and extract/insert fields are checked and return `OutOfRange`, including bounds too large to add safely. Insert rejects values with bits outside the requested width. Zero-width fields are permitted; full-width signed fields preserve their bit pattern. Values remain immutable.
- **Endian binary formats.** `bork/binary` reads and replaces fixed-width signed/unsigned integers in Bytes at checked offsets with explicit big/little endian order. Immutable Reader reads return `(value, nextReader) | BinaryError`; failure preserves the original cursor. An immutable Writer accumulates persistent chunks and produces independent Bytes snapshots. Integer writes preserve their bit patterns. Read/replacement bounds and writer size are checked with subtraction before addition, including extreme offsets and counts. No effects or resources are required.
- **No user-defined symbolic operators** (e.g. `|+|`, `>>=`).
- **No hidden resolution magic.** Type class instances are resolved implicitly, but only from an explicitly imported, bounded set of places (see type classes below). Nothing like Scala 2's implicit conversions or whole-program implicit search.
- **Performance target:** roughly Go-level performance, traded away for guarantees where needed.

### Positional tuples (bork-ys21yg)

Tuples are general immutable heterogeneous values, with structural ordered types,
independent positional inference, zero-based constant `.0` selectors, singleton
trailing commas, irrefutable binding destructuring and exhaustive match patterns.
Parentheses retain grouping and function syntax; there is no empty tuple or named
builtin tuple type. Generic substitution and expected-type inference recurse into
elements. Equality/hash and Show are structural; Encode/Decode require codecs for
every element and use exact-length JSON arrays with index paths. Derived containers
may contain tuples; tuple aliases may request Encode/Decode, but not GoStruct.
Element constraints must be checked at construction/boundaries and decoding, and
tuples must preserve effects and scope lifetimes. Tuples lower to a single anonymous
Go struct value with positional E0/E1 fields. See [design](design/tuples.md).

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
- **Library derivation:** `derive (codec.Decode, codec.Encode)` for records and ADTs, and `derive (GoStruct)` for records with a static Go mapping. Derivation cannot inspect or construct foreign private variants, including variants reachable through fields, containers, and concrete generic specializations. At a field boundary it can delegate to an existing codec provided by the type's owning package, preserving that package's chosen public representation. Structural equality and default text need no derive.
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

- **Class owners can define typed derivation templates** with `derive instance name[T]: Class[T]`. Their methods match the class signatures. `comptime for`, `comptime if`, and `comptime match` consume shape metadata during expansion; list comprehensions collect or filter field results. `derive fn` declares reusable helpers that cannot be called by runtime code or contain unsafe Go. Each expansion is ordinary checked Bork code, with projected field facts, effects and lifetimes preserved. Generic dictionary requirements are solved before ordinary callers and defaults are checked, using fresh discovery state and grounded dependency closure; failed candidate searches cannot introduce bounds. Shape descriptors cannot escape to runtime, private representations cannot be inspected by foreign packages, and expansion has a shared work limit. Undefined global references are diagnosed even in unapplied definitions; target-dependent typing is checked when requested. See [derivation templates](language/derivation.md).

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
- **Standalone derivation:** `derive codec.Decode for Config` requests one class per declaration, in any file of the package and independently of declaration order. Inline derive keeps working. A standalone request must be in the package that owns the underlying record/sealed type or the class; aliases do not change ownership, and private representation rules still apply. `derive codec.Decode for Box` derives universally; `derive codec.Decode for Box[Int]` derives a concrete specialization. Repeated requests, including inline plus standalone, are errors. Constrained aliases retain their result validation. Derived instance names remain `ConfigDecode`; specialized heads have deterministic suffixes shown by the package API. GoStruct is derived for the record declaration, not a specialization. Ordinary selectable instances retain their existing scopes and ownership rules.
- **`derive (codec.Decode, codec.Encode)`** on a record or sealed type asks the compiler to write the instances, named after the type (`CreateUserDecode`). They follow the same rules as written instances: in scope in their own package, used elsewhere with `use api.CreateUserDecode`. Each field needs an instance in scope. The `bork/codec` classes `codec.Decode` and `codec.Encode`, plus the prelude’s `GoStruct`, can be derived.
- **JSON:** `bork/codec` has a `codec.Value` sealed type and the classes `codec.Decode` and `codec.Encode`; select `use codec.Defaults` for instances for the basic types, `Option`, `List`, `Map[String, V]`, and `codec.Value`, while `bork/json` provides `json.Parse`/`json.Render` and `json.Decode[T]`/`json.Encode[T]`. Records are objects, including empty records: their derived decoder accepts objects and rejects other JSON kinds, and their encoder produces `{}`. A sealed value is an object whose `"type"` names the variant (a variant without fields may be just its name, `"Free"`); a missing `Option` field is `None`. A `codec.DecodeError` says where (`.items[1].qty`) and what went wrong.
- **JSON safety:** parsing rejects duplicate object keys (including escaped spellings of the same name) and invalid UTF-8, with line and column diagnostics. Encoding unordered maps sorts keys; insertion-order and sorted maps keep their traversal order. A named sealed payload field called `type` is a compile error when deriving Encode or Decode because it collides with the discriminator. A named field called `values` is allowed.
- **Dynamic JSON and JSON Lines:** see [bork/json](std/json.md).
- **YAML:** `bork/yaml` parses and renders YAML as `codec.Value` trees, and decodes and encodes typed values through the codec Decode/Encode instances (core schema only, duplicate keys and unknown tags rejected, alias expansion bounded); see [bork/yaml](std/yaml.md).

- **Instances on constrained types:** `instance decodePort: codec.Decode[Port]`, with `type Port = Int where between(1, 65535)`, is used for values known to be ports: the fields of derived instances whose where clauses include the instance's; calls whose arguments of the type parameter are all declared ports (a parameter, binding, or field with that where clause): `json.Encode(port)`; and calls with an explicit constrained type argument: `json.Decode[Port](text)`. The most specific instance wins (`Int where positive` over `Int`). Its methods assume the constraints of parameters of the type, and must promise them of results (`fn decode(json: codec.Value): Port | codec.DecodeError`), which the facts checker verifies.
- **Constrained type arguments are facts, by parametricity.** A generic function gets values of a type parameter only from its arguments of that type and from the instances of its bounds. So with `json.Decode[Port](text)`, arguments of type `T` must be ports, every instance for `T` that can produce values must promise `Port` (or it is an error), and the result's `T` member is then proven a port. Functions whose other parameters could produce `T` values (`map`'s `f: (A) => B`) cannot take a constrained type argument.
- **A derived decoder checks the where clauses** of the fields it decodes, so a decoded value is proven, and needs no further checks:

```
type SignUp = {
  name: String where nonEmpty and maxBytes(20),
  age: Option[Int where between(13, 150)],
} derive (Decode, Encode)

r: SignUp | JsonError | DecodeError = json.Decode(line)   // DecodeError { path: ".name", message: "must be nonEmpty" }
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
- **Fact sources:** a predicate call in an `if` condition (in the branch it guards), guards that end in `return` or `panic` (for the code after them), `&&` and `||` (for their right side), `!`, a function's own requirements, a callee's promised result (also through `?` and `match` on a validated union, where the fact holds only for the promising member), field declarations, typed bindings, successful bound type-pattern predicates, and `trust p(x)`.
- **Facts are found by identity.** A fact about `x` also holds for `y = x`, and for field paths like `u.age`. Repeated pure calls and computed expressions share facts when their resolved operation, runtime types, and input identities match. This is structural identity, not an arithmetic theorem.
- **Promised results are verified** against every reachable path of the body. Guards resolved from literal-backed eager bindings and independent record/copy fields prune impossible `if` arms and short-circuit operands. Unknown guards and runtime lazy/async cells retain all possible returns, including `?` failures; deciding a guard never executes a predicate or helper. Typed float rounding is preserved, and overflowing integer intermediates remain unknown (bork-m25mqh).
- **Requirements on constants are decided by running the predicate at compile time**, using the program's own code (including `unsafe go`): `transfer(0)` fails the build with "positive(0) is false". This works for any predicate, and for literals made of constants too: `xs: List[Int] = []; xs.first()` fails with "notEmpty([]) is false", `greet(User { name: "bob", age: 12 })` with "adult(User { name: "bob", age: 12 }) is false".
- **Generic predicates:** `pred notEmpty[T](xs: List[T]) { !xs.isEmpty() }` applies to every list. The prelude has it, with `fn (xs: List[T] where notEmpty) first[T](): T`, which needs no `Option`; `prepend`, `append`, and `split` promise `notEmpty` results.
- **OR:** `x: Int where positive or zero` needs one of the alternatives; `and` and `or` mix only with parentheses (`(positive or zero) and small`). A `||` condition gives an OR fact. An OR obligation may be proven by different alternatives on different branches, and a known OR fact is used by cases: a goal that follows from each alternative follows from the fact.
- **Inference rules** say what follows from what: `rule weaker(x: Int, a: Int, b: Int) { atLeast(x, a) and a >= b => atLeast(x, b) }`. Premises are predicate calls on the rule's variables (constants allowed after the first argument) and conditions on them using only operators and constants, which are decided at compile time or matched against comparisons established in the current branch. A rule may have several premises and conclusions; rules chain, and cycles between them are harmless. Rules are trusted, like `trust`, so `bork test` gives each one a property test: it generates values using the property-test generators (including composites and NaN), with reproducible seeds and shrinking, and reports a counterexample where the premises hold but a conclusion does not.
- **Comparison guards unfold simple predicates:** `if (n > 0)` proves `positive(n)` when its body is `n > 0`; conjunctions, disjunctions, positive predicate calls, and negated comparisons are supported. Integer and String order negation reverses the comparison; Float negation retains its polarity for NaN safety. Facts remain local to the branch, and matching stable fields or aliases is allowed. Relational rules bind subjects mentioned only in premises and try premises in either order; transitivity and symmetry remain user-written rules.
- **Derived results:** a function that declares no promise still passes on what its body proves. With `fn clamp(x: Int): Int { if (positive(x)) { x } else { 1 } }`, `retry(clamp(n))` proves `positive`: every path of the body is checked against the obligation. A path returning a parameter is checked at the call site, for the argument. Helpers chain, in any declaration order. A declared promise is still the way to make a fact part of the contract (and is verified).
- **Implicit failure returns keep result promises.** Union `?` failures and `Option.None` returned by `?` must satisfy the enclosing function's result predicates, just like an explicit return. This also applies inside call arguments and blocks, and when deriving facts from an unannotated function's body. A predicate on the entire result union is checked for each returned member or smaller union; narrowing preserves already-proven facts about the same value.
- **Facts inside type arguments:** `List[Int where positive]` constrains every element, `Option[String where nonEmpty]` the value if there is one. A fact written where it would not be checked (on Map keys or values, a member of a union that is not a result, a function type, a rule variable, a lambda parameter, a constrained bare/destructuring pattern or nested pattern type argument, a constructor name using a constrained alias, or a type argument held inside another type) is a compile error, never silently dropped. A list literal is checked element by element (`[1, 0]` fails with "positive(0) is false"), `Option.None` needs nothing, and a name bound by `Option.Some(v)` has the facts of the value. Rules apply to elements too. Bound type patterns (`n: Int where positive`, or `n: PosInt`) check the erased type first, then evaluate the predicates at runtime. The subject is evaluated once; arms and short-circuiting `and`/`or` clauses follow source order, and predicate panics propagate. Successful predicates establish arm-local facts, including for constrained calls and results; their own requirements must be proven before they run. Guarded arms never count toward exhaustiveness or make later arms unreachable, so an unguarded fallback is required. Earlier unguarded arms can make guarded arms unreachable. Only direct whole-value constraints are supported; constrained bare type names, constrained destructuring, and nested type-argument constraints remain compile errors. Generic and imported predicates use ordinary call checking; function-valued predicate parameters must declare `uses nothing`. To construct a constrained alias, use its base constructor in an annotated binding (`p: PosPoint = Point { x: 1 }`); the binding checks the predicate.
- **Predicate parameters:** a function parameter can be the predicate: `fn (xs: List[T]) filter[T](keep: (T) => Bool): List[T where keep]`. What the argument checks is then known: `xs.filter(positive)` is a `List[Int where positive]`, and `xs.filter(x => positive(x) && small(x))` has both facts. `find` refines its `Option` the same way.
- **Facts flow through generic functions.** A generic function cannot make values of its type parameters, so the ones in its result come from its arguments: what holds for all of those holds for them. `positives.head()` holds a positive number, and so do `reverse`, `take`, `positives.concat([5])`, and so on; a lambda's parameter gets the facts of the values the function can hand it (`positives.map(p => transfer(p))`). This holds for `unsafe go` generic functions by trust, like their signatures.
- **Test mode checks what is trusted.** `bork test` runs the tests with runtime checks of `trust` statements and of what `unsafe go` functions promise (through list elements, Option values, and fields), so trusted facts that drift from the truth are caught by tests: "validate promised a result that is positive, but returned 0".
- **Property tests come from facts.** `where` clauses already say which inputs are valid, so `test "transfer scales" (amount: Int where positive) { ... }` runs on generated values that meet them (list elements' and record fields' facts too), shrinks a failing case to a simpler one with the same facts, and prints the seed that reproduces it. `bork test --auto-properties` property-tests the functions whose promises are trusted rather than proven (`unsafe go`, `trust`): their promises hold, and they don't panic, for generated arguments. It is opt-in until effects mark functions pure, which will then be tested by default.
- **Snapshot tests** are cheap regression tests: `assertSnapshot(render(invoice))` compares the value's text with a file in the package's `snapshots` directory, and a failure shows a line diff. `bork test --update` writes the snapshots that are missing or different, as the compiler's own golden tests do with `-update`, so the change is reviewed in version control.
- **Tests can run in parallel** (bork-gadfaf). `bork test --parallel N` runs up to N tests at a time, each on goroutines of its own, and reports them in the order they are declared, with the same report as one at a time. Each test keeps its own mocks, snapshot numbering, written snapshots and failures: its goroutine carries a `bork.test` profiler label, which the Go runtime copies to every goroutine it starts (tasks, servers, Go code's goroutines), the carrier mocks use. Tests must not share outside state, such as files, ports, environment variables and the working directory, and their printed output interleaves. Property tests do not hide their cases' output in parallel, since `os.Stdout` is shared. `assertSnapshot` on a goroutine that has no test's label (Go code replaced the labels, or the Go runtime started it) fails, saying so. A goroutine Go code starts lazily in one test and reuses in another (a Go worker pool) keeps the first test's label, so its snapshots count as that test's, or fail once that test has ended. A crash that ends the program (a panic on a goroutine Go code started, a fatal error) loses the reports of tests that finished but are not yet reported in order. A test that calls `runtime.Goexit` fails. The default is one test at a time, on the main goroutine, as before.
- **Diagnostics** name the requirer, the parameter or field, and the predicate, and suggest a guard or a declaration. A failing constant from inside a helper says where it came from: "sometimes can return 0, and positive(0) is false".

### Relational facts through rules (bork-uooboq design)

Relations remain ordinary predicates, with one value as the subject and the
others as arguments. `examples/payments` already uses
`to: AccountId where differentFrom(from)`. A caller can pass the same
requirement through its own signature; no tuple type or special relation
registry is needed. This increment extends the existing backward rule search
and guard facts, rather than introducing a second proof engine.

**Comparison guards and simple predicate bodies.** With
`pred positive(n: Int) { n > 0 }`, the successful branch of `if (n > 0)`
proves `positive(n)` without a written rule. A pure predicate consisting of
comparisons, positive predicate calls, comparison negation, `&&`, and `||`
may be unfolded on demand:
all parts of an AND must hold; one part of an OR suffices. Recursive bodies
must terminate proof search through the existing depth and cycle limits.
Negated predicate calls are not proved by a missing positive fact. An opaque
body or `unsafe go` predicate still needs a predicate guard or rule.

In `examples/payments`, `parseAmount` can reject `n <= 0` and
`n > 1_000_000` directly, then return `Cents`: the remaining path establishes
both `positive(n)` and `atMost(n, 1_000_000)`. Similarly, `request` can reject
`to == from` and prove `differentFrom(to, from)` on the remaining path.
In `examples/accounts`, the successful withdrawal branch's
`amount <= balance` can satisfy a helper's `amount: Int where atMost(balance)`.
These implications concern the input values; they do not prove that
`balance - amount` is nonnegative or that `balance + amount` cannot overflow.

**Comparison premises on runtime values.** Existing syntax stays unchanged:

```bork
pred positive(n: Int) { n > 0 }
pred atMost(n: Int, limit: Int) { n <= limit }
pred differentFrom(id: String, other: String) { id != other }

rule positiveGuard(n: Int) { n > 0 => positive(n) }
rule ordered(a: Int, b: Int, c: Int) {
  atMost(a, b) and atMost(b, c) => atMost(a, c)
}
rule differentSymmetric(a: String, b: String) {
  differentFrom(a, b) => differentFrom(b, a)
}
```

A rule condition is satisfied either by constant evaluation, as today, or by
matching comparisons already established in the current branch after binding
rule variables. Negation and reversed operands normalize equivalent
comparisons (`!(a > b)` and `b >= a` both establish `a <= b` for integers
and strings). Float guards retain negation: with NaN, `!(a > b)` does not
establish `a <= b`. No implicit
transitive or arithmetic solver is added: user rules express those
implications. Values bound only in premises must be found in matching facts,
including a subject not present in the conclusion; the order of premises must
not change which inference succeeds. Repeated variables must name the same
value. Facts keep their branch scope, including case splits for OR guards.

**Verification and limits.** `bork test` migrates rule tests to the property
generators introduced in #60, including special floats and composite values,
executing comparison premises on the
generated values before checking conclusions. Include valid transitivity,
symmetry, mixed predicate/comparison rules, and an invalid rule with a
counterexample. Compiler rule fixtures use opaque conclusion predicates so
unfolding cannot mask a broken rule search. Include hidden middle variables,
both premise orders, repeated-variable mismatches, OR cross-branch negatives,
and a NaN guard rejection. Compiler cases cover aliases, several input values, reversed
comparisons, negated guards, early returns, conjunction/disjunction, missing
proofs, and cycles. Keep diagnostics naming the callee and relation arguments,
with the existing guard/declaration suggestion.

Stable parameter, binding, and field identities are extended to pure methods
and computed expressions by the projection identity implementation below.
Function-level `where sameLength(xs, ys)` or `where lo <= hi` and
method projections such as `index < xs.length()` are supported by function-level
clauses (`bork-3ly6p0`) and pure method/computed identities (`bork-rgy4as`).
Arithmetic implications must respect the sized-number overflow rules; no
built-in `lo < hi => lo + 1 <= hi` shortcut is added here (`bork-ggj8ew`).

### Pure projection identities (bork-rgy4as)

Facts established about `xs.length()`, `p.total()`, or `a + b` also apply to
another evaluation of the same pure expression. A binding of such a value
shares its identity, so `size = xs.length()` and a guard `index < size` can
satisfy a helper requiring `index < xs.length()`. Receiver aliases work too.
`trust positive(a + 1)` can likewise name a reusable computed value.
Nested fields, unary expressions and arithmetic preserve this structural
identity, including when substituting parameters into predicates and contracts.

Calls must resolve to the same declaration, generic specialization and class
dictionaries, with the same typed argument identities and ambient inputs.
Effectful calls, mutable Go inputs or results, function-valued inputs (including
pure callbacks with hidden captures), lazy sequences, and unknown generic input
values receive no reusable call identity. Prelude list length is safe even for
generic or opaque element types because it only observes the immutable container.
Calls through function values receive no reusable call identity. Pure projections
with ambient needs can share guard facts, but are not supported inside function-level
requirements in this increment. Reading an
effectful result into a binding still creates a stable immutable value; facts
about that binding do not establish facts about a fresh call.

There is no commutativity, reassociation, method-body equality, or overflow
inference: a guard about `a + b` does not prove a fact about `b + a` or `a - b`.
Sized arithmetic constant evaluation declines overflowing intermediate results,
and Float arithmetic rounds to its runtime precision. Float guards retain NaN
polarity. Branch facts keep their existing scope and OR alternatives.

### Overflow-safe integer arithmetic implications (bork-ggj8ew)

The facts pass may prove comparisons involving sized integer addition and subtraction from established bounds. Arithmetic keeps its ordinary wrapping runtime behavior. An expression is treated as mathematical addition/subtraction only after every intermediate is proven to fit its concrete integer type. Float arithmetic is excluded because of rounding, infinities and NaN.

Use exact compiler constants for proof calculations and the concrete type's minimum/maximum as initial bounds. Branch comparisons add relative bounds; strict integer order supplies the one-unit gap. For `lo < hi`, the domain bound `hi <= max` implies `lo <= max - 1`, so `lo + 1` cannot wrap and is at most `hi`. Similarly, `lo < hi` makes `hi - 1` safe and at least `lo`. A general literal shift requires enough bounds to rule out overflow at each step. Addition/subtraction of two runtime operands uses conservative interval bounds; `a > 10` and `b > 10` alone cannot prove anything about their mathematical sum, while explicit upper bounds can establish a safe positive result.

Arithmetic goals use a bounded difference-constraint graph over stable integer expression identities and a distinguished zero. Comparisons constrain the difference of two values, and type domains constrain each value relative to zero. Safe shifts add equations between the shifted result and its operand. Safe sums/differences add interval bounds. Reachability combines the established constraints; it never normalizes an unchecked wrapping operation. An operation that cannot be normalized keeps its wrapped runtime value as an atom; proving a surrounding operation safe never unwraps that atom. Wrapped values may still be compared by their own stable identity when a guard states that comparison directly.

Proof search unfolds only the existing simple predicate forms and preserves disjunction alternatives separately. Every feasible alternative must establish a requested bound; no facts leak between alternatives or across branches. The graph is limited to 48 nodes and 32 alternatives; reaching a limit returns an unproven result rather than weakening the safety conditions. Opaque/effectful calls, multiplication/division/remainder and nonlinear identities are outside this increment. User-written rules remain trusted statements; their arithmetic premises use the same conservative typed evaluation and bound proof, and rule property tests keep running with runtime sized arithmetic.

Verification covers signed and unsigned domains at their limits, strict-successor/predecessor implications, safe literal shifts, safe bounded sums/differences, unsafe overflow/underflow, intermediate wrap before cancellation, negative literals and negated comparisons, and separate OR alternatives. Floats and unchecked arithmetic must remain unproven.

### Function-level relational requirements (bork-3ly6p0)

A function may require facts relating any of its inputs, without choosing one
parameter as the subject. Place the clause immediately after the parameter
list, before `uses` and the result type:

```bork
pred sameLength[A, B](xs: List[A], ys: List[B]) { xs.length() == ys.length() }
fn zip[A, B](xs: List[A], ys: List[B]) where sameLength(xs, ys): List[Pair[A, B]] {
  // The body knows sameLength(xs, ys).
  ...
}
fn interval(lo: Int, hi: Int) where lo <= hi: Range { Range { lo: lo, hi: hi } }
fn (xs: List[T]) zip[T, U](ys: List[U]) where sameLength(xs, ys): List[Pair[T, U]] { ... }
fn send(lo: Int, hi: Int) where lo <= hi uses io: Ok { ... }
```

The position distinguishes an input requirement from an existing result
constraint: `fn f(x: Int) where positive(x): Int where positive` requires a
positive input and returns a positive result. Parameter clauses remain valid
and combine conjunctively with function clauses. The function clause supplies
all predicate arguments explicitly; it never inserts an implicit subject.
Qualified predicates and locally inferred generic predicates work as ordinary
calls do. Arguments name immutable parameters, the receiver, stable field
projections, constants, or pure computed expressions, including declared
function and method calls. Calls through function values remain unsupported.

**Clauses.** A requirement is a pure Bool predicate call or a comparison using
`==`, `!=`, `<`, `<=`, `>`, or `>=`. Requirements combine with `and` and `or`,
with parentheses for grouping, as existing type constraints do. Mixing `and`
and `or` at the same nesting level is rejected: write
`where (p(x) or q(x)) and r(x)`, not `where p(x) or q(x) and r(x)`. Comparisons
have their ordinary operand typing and Float/NaN semantics. No new arithmetic,
transitivity, or method-identity theorem is implied by the syntax. Rules and
predicate unfolding continue to use the existing proof engine. Constant
requirements must also hold; an impossible clause cannot make a function body
an unchecked source of guarantees.

**Calls and bodies.** At a call, substitute actual arguments for parameter
names and prove every requirement using the caller's facts, guards, constants,
and rules. Named arguments map by parameter identity; reordered named calls
and omitted defaults have the same obligations as positional calls after
completion. Receiver calls, pipes, explicit generic calls, inferred generic
calls, assembly-provided calls, and native function bindings follow the same
checking path. Evaluate argument expressions once, preserving existing source
evaluation order. A failed proof names the callee and the fully substituted
relation, with the existing guard/declaration guidance. In the body, all
parameter requirements and function requirements are available from entry;
OR clauses retain their alternatives rather than publishing both branches.
An early rejection guard may establish a requirement at a later call exactly
as it establishes a parameter constraint today.

**Callable boundaries.** Function and method references with input requirements
continue to be rejected as values; a lambda can perform a guarded direct call.
Predicate calls in a clause must satisfy their own parameter requirements
before the clause is assumed; preceding AND clauses can establish those facts.
The existing function type syntax does not silently erase relational contracts.
Class method signatures may declare requirements in the same position. Each
instance method must declare the same contract after receiver/type/parameter
substitution and parameter renaming; strengthening, weakening, or adding an
undeclared requirement is rejected conservatively. Class dispatch checks the
signature contract before invoking the instance. Generic predicate checks
preserve type arguments and dictionaries: concrete instances keep their
lexical scope, and bound dictionaries follow the enclosing call. Compile-time
checks retain the runtime types of union members, including inside literals.
Auto-property generation
checks the completed argument tuple against all parameter and function
requirements before invoking the function, and shrinking preserves those
requirements. This includes native-binding auto-properties. Rejection sampling
uses the existing bounded generation policy; it does not invent a relational
solver. Tests and generated native bindings retain the same requirements.
Predicates and rules do not gain function-level clauses in this increment.

**Verification.** Compiler cases cover passing a declared relation onward,
predicate guards, comparison guards, early returns, rules, AND/OR branches,
receiver calls, named/defaulted arguments, generics, native bindings, assembly,
and class contract matching. Rejection cases cover absent proofs, mismatched
argument identities, cross-branch OR facts, NaN-invalid implications,
undeclared names, impure or non-Bool predicates, computed requirement arguments,
unparenthesized mixed AND/OR clauses, joint generation/shrink violations,
class contract mismatches, and function-reference erasure. Formatter and query
output show the clause without confusing it with the result's `where`.

### Sibling-field record invariants (bork-k3nrwg)

Field predicates can name sibling fields, including fields declared later:

```bork
pred atLeast(n: Int, lo: Int) { n >= lo }
type Range = { lo: Int, hi: Int where atLeast(lo) }
```

Every construction proves `atLeast(hi, lo)` with the completed field values.
A field default is checked against its actual siblings at each construction,
so `hi: Int where atLeast(lo) = 10` requires the supplied `lo` to be at most
10. Unary constraints on closed defaults are still checked at declaration.
Variant fields use their own variant's sibling scope. Predicate arguments
remain constants or sibling names; expressions and outer function parameters
are not accepted in a record declaration.

`copy` checks relations against the resulting record. Changing `lo` rechecks
the constraint on `hi` even when `hi` is unchanged. Updating both fields
checks their new values together; nested updates such as `r.copy(range.lo:
newLo)` recheck the nested record's relations. Relations whose subject and
arguments are untouched remain valid. Selection and destructuring retain the
relation with projections of the same record substituted for sibling names.
No arithmetic or implicit transitivity is introduced.

Derived `Decode` and Go-to-record conversions validate relations after all
fields have been converted, including defaults. Property generators generate
referenced fields first where possible, defer cyclic dependencies until all
fields exist, and preserve constraints while shrinking. Highly restrictive
cyclic predicates may exhaust generation and report that rejection; invalid
values are never passed to a property. The field-only
schema decoder validates independent constraints; sibling relations require
the full record decoder. Schema metadata still lists all constraints.

### Construction control (bork-kum0ep)

A declared type can guarantee valid states independently of where a caller
obtained its value. Required fields, closed defaults, sibling-field facts
(bork-k3nrwg), and sealed alternatives already express much of this contract.
This increment adds whole-value invariants and ownership of record construction.

**Whole-value invariants.** A record or sealed declaration can carry ordinary
predicate clauses after its body and before `derive`:

```bork
pred ordered(r: Range) { r.lo <= r.hi }
type Range = { lo: Int, hi: Int } where ordered

pred valid(s: State) {
  match (s) {
    State.Idle => true,
    State.Running { lo, hi } => lo <= hi,
  }
}
type State = sealed {
  Idle,
  Running { lo: Int, hi: Int } where valid,
} where valid
```

A clause takes the completed value as its subject, with the existing `and`
and `or` combinations and constant predicate arguments. A record declaration
may also retain sibling-field clauses, such as `hi: Int where atLeast(lo)`;
these are field relations within the same type contract. A clause on a sealed
variant takes a value of the parent sealed type, since variants have no
separate source-language type. It applies only to that variant. A clause on
the sealed declaration applies to every variant, including fieldless ones.
Generic declarations instantiate predicates against the concrete type.
Mutable opaque Go values cannot be subjects of facts.

Construction uses a completed but **unvalidated candidate**, whose underlying
fields have ordinary types but whose own nominal invariants are unavailable.
The facts pass proves its field and type obligations before publishing the
candidate as a value carrying those facts. In particular, the constructor's
own type cannot discharge its goal. Decoders and Go conversions use the same
phases: convert and fill defaults, validate, then expose a valid value.

Invariant predicates inspect this underlying representation without assuming
the invariants they establish. The same restriction follows an unchecked
candidate into predicates or helpers called by the validator: a helper may
not obtain a nominal precondition merely because its argument has the type
currently being validated. Such a requirement needs an independent proof.
The implementation must check this validation call graph, including imported
helpers, or reject a call whose assumptions cannot be checked. Self-dependent
and mutually dependent preconditions are rejected with a cycle diagnostic;
ordinary terminating predicate recursion does not itself establish a fact.
The initial implementation conservatively rejects parameter `where`
requirements on the invariant predicate itself, construction of the target
type within its validation call graph, and calls through function values or
class methods whose implementations cannot be checked. Declared helpers are
checked without the nominal guarantees of every target they help validate;
their explicit requirements still need independent proofs at the call site.
Validation may use a field's already-checked independent constraints, but must
not use the target invariant or a relation that depends on it to establish
itself. Negative cases cover a trivially circular predicate/helper contract,
mutual dependencies and invalid construction or decoding that would otherwise
pass by the candidate's nominal type.

Construction proves all field and whole-value invariants. A record `copy`
rechecks its whole-value clauses against the completed changed record, and
recursively checks changed nested records; unchanged nested values retain
their guarantees. Every value of a declared type carries its type-level
facts, whether it is a parameter, a returned value, a collection element, or
a destructured value. A variant fact is known only after narrowing to that
variant. An alias preserves both these guarantees and construction control.
Invariants are nominal contracts, not extra requirements that must be
repeated on every function parameter.

An unchanged source record keeps its own facts during a copy, while the new
candidate has no whole-value invariant until revalidation. The prover can
unfold established simple invariants to recover comparison and predicate
consequences on source fields. Only field identities whose value is unchanged
survive into the candidate; changing a nested path invalidates facts about
that path and any whole value containing it. Thus `configured(c)` can supply
`admin == false || token != ""` for the unchanged admin/token fields in
`c.copy(hi: hi)`, but cannot itself prove `configured` of the new value.
A user rule can expose consequences of an opaque predicate; no general
logical consequence solver is added. Tests include the shown `WithHi`,
invalid admin/token changes, and nested changes invalidating an enclosing
invariant as well as the nested type's invariant.

The facts pass uses its existing backward search, comparison facts and rules.
Simple predicate bodies can inspect the completed record's fields. A match
on a constructor with a known variant can select its corresponding predicate
branch and substitute that constructor's field values; it must not assume
facts from a different branch. Opaque predicates still require declared facts,
guards or rules. There is no implicit arithmetic solver, and no deferred
runtime validation for ordinary bork construction. A smart constructor checks
its inputs first and returns a typed error when they are invalid.

**Construction belongs to a package.** Choose a contextual `private` modifier
on a record's representation:

```bork
import codec "bork/codec"
use codec.Defaults

type Config = private {
  lo: Int,
  hi: Int,
  admin: Bool = false,
  token: String = "",
  debug: Option[String] = Option.None,
} where configured derive (codec.Decode, codec.Encode, GoStruct)

pred configured(c: Config) {
  c.lo <= c.hi && (c.admin == false || c.token != "")
}
```

`Config` remains exported, and its fields remain readable and destructurable
wherever the type is visible. Only its declaring package can construct a
literal or use `copy` on it. A foreign nested copy cannot modify a private
record's fields either; replacing a whole private-valued field with an
already-valid value is allowed, provided the enclosing record's facts hold.
The modifier is supported on record declarations, rather than aliases,
resources, or sealed declarations. It does not change field visibility or
make a type name private.

The alternative is a sealed type with one lowercase variant:
`type Config = sealed { config { ... } }`. That remains useful for a hidden
representation, but foreign code cannot match its private variant, so it
would need accessor methods for every readable field. It also introduces
sealed dispatch for a value with exactly one shape. The record modifier
expresses construction ownership while preserving the public field API.
Lowercase sealed variants keep their existing stronger representation privacy.

A package can expose smart constructors and checked updates:

```bork
// package settings
type InvalidConfig = {}
fn New(lo: Int, hi: Int, admin: Bool, token: String): Config | InvalidConfig {
  if (lo > hi || (admin && token == "")) { return InvalidConfig {} }
  Config { lo: lo, hi: hi, admin: admin, token: token }
}
fn (c: Config) WithHi(hi: Int): Config | InvalidConfig {
  if (hi < c.lo) { return InvalidConfig {} }
  c.copy(hi: hi)
}
```

An importer can call `settings.New`, read `config.hi`, and pass the value on,
but `settings.Config { ... }` and `config.copy(hi: ...)` report that the
package controls construction. Diagnostics name the owning package and point
to using its exported constructor or update method. The compiler does not
invent a particular constructor name when none is declared. Required fields
still have no default; optional data uses `Option` and can have `Option.None`
as its default. Allowed alternatives still use sealed types: for example,
`Transport.Tls { certificate: String, key: String }` requires both TLS inputs,
while `Transport.Plain` requires neither. The config example will demonstrate
these choices alongside the cross-field and admin-token invariants.

**Runtime boundaries and derivation.** Package-owned `derive (Decode)` may
construct a private record, and an importer can invoke that exported codec.
Its generated decoder validates all field and type invariants after decoding
and filling defaults, returning `DecodeError` before exposing an invalid
value. CSV, environment and CLI decoding must reach the same complete-value
validation through their schema/dictionary paths; field-only validation is
insufficient for a whole-value or sibling constraint. `GoStruct.FromGo` and
mirror conversions likewise validate the completed value and collect
`GoValueError` diagnostics. A failed primitive conversion suppresses invariant
checks that would inspect that invalid value. Exporting an owning package's
codec or conversion dictionary is a deliberate checked construction API.

Foreign derivation cannot manufacture a structural constructor for a private
record, including one reachable through containers or generic specializations.
It may delegate at a field boundary to a codec that the owning package exports,
as existing private-variant derivation does (#105). Encoding may read public
fields but cannot create a foreign decoding route. A foreign alias cannot
remove the restriction. Generated constructors, default values, property
inputs and shrunk property values must satisfy the same invariants. Property
generation uses rejection filtering of completed values and reports exhausted
generation for predicates it cannot satisfy; it must never execute a property
on an invalid value. Test-mode validation also checks trusted `unsafe go`
results. Explicit unsafe Go remains a trust boundary, rather than a way to
obtain a compiler proof of arbitrary construction.

**Acceptance cases.** Cover valid and invalid Range construction, a copy
changing either bound, nested copies, defaults, generic records, known-variant
and fieldless sealed construction, narrowing of per-variant facts, and opaque
predicate requirements. Cover cross-package literal/copy rejection, readable
fields and patterns, aliases, nested private records, smart constructors and
updates. Exercise Decode, CSV/env/cli and Go conversion on invalid completed
values, including defaults; verify owning codecs work across imports while
foreign structural derive fails. Property generation and shrinking preserve
both sibling and whole-value invariants. Update grammar, formatter, editor
grammar, README and config examples with the implementation.

### Generated checked constructors (implemented: bork-aull6h)

An owning package can export a record's checked construction without repeating
its fields, defaults and facts:

```bork
// package settings
fn New = Config.new
```

This is a function declaration, with ordinary uppercase export visibility. The
right side names a record declared in the same package. It does not create an
implicitly callable `Config.new` member: construction APIs exist only when the
owner declares them. Public records may use the declaration too. Aliases,
sealed types, imported records, methods, predicates and class/instance methods
are rejected. The generated function returns the record and is pure.

**Parameters and defaults.** Every field becomes a parameter with the same
name, type, facts, documentation and closed default. Parameter order is field
order; named arguments are recommended since a record's field order is now
part of this explicitly exported API. Fields without defaults remain required.
Generated signatures permit defaults before required fields, preserving record
order; named calls can omit any defaulted field, and positional calls can omit
only a suffix whose fields all have defaults. Supplied arguments retain the
usual source evaluation order. Defaults mean what they mean in the owning
package, including private variants. Adding a required field changes the API;
adding a defaulted field retains existing named calls.

Generic records produce generic functions with the record's type parameters.
Calls infer them from arguments, or supply them explicitly:
`fn NewBox = Box.new` supports `NewBox[Int](value: 3)`. Field facts retain
references to sibling parameters, even when the referenced field is defaulted
or follows the constrained field in declaration order.

**Whole-value requirements.** The generated signature requires every type-level
predicate on the completed candidate, after supplying defaults. These are
function preconditions, proved at the call site with the ordinary facts pass.
They apply to an unvalidated structural candidate: its nominal record invariant
cannot prove itself. Simple invariant predicates unfold against field arguments;
a predicate calling an exported field predicate can unfold to facts from that
field predicate (for example `BudgetOk(bodyLimitBytes)`). An opaque predicate
on the private record itself may require an owning-package handwritten wrapper:
a foreign caller cannot manufacture an unchecked record just to guard it. No
predicate body is copied into the declaration, and ordinary literal checks
remain the source of truth. A caller can therefore prove Configured's body-limit requirement
with a guard or a parameter fact without repeating that requirement in `New`.

Construction still validates field and whole-value obligations before publishing
the result. Importers can call the exported function and read its result, while
foreign literals, copies, aliases and generated-constructor declarations retain
the same private construction restrictions. A constructor with requirements
cannot be used as a function value, just like an ordinary constrained function;
a checked lambda can wrap it. Diagnostics identify the generated function, the
failed field or invariant and the record declaration rather than suggesting a
foreign literal. Formatter and description output retain the short declaration
and expose the generated signature and its obligations, respectively. For
example, the callable description lists `host: String`, every defaulted field,
and `completed Config requires Configured`. A failed call reports
`settings.New requires its completed Config to be Configured`, with the field
arguments available as the subjects of proof; it does not suggest guarding
`Configured` on an inaccessible private literal. Cover an exported field-predicate
guard and rejection of an unprovable opaque record predicate.

This increment supplies all fields and uses compile-time requirements. Selecting
only some fields, returning a generated validation-error value and direct
`Config.new(...)` calls are deferred; a handwritten wrapper or the owning
package's derived decoder handles those APIs today.

**Acceptance.** Cover inferred and explicit generic calls, imported exports,
field/default/fact reuse, sibling constraints (including forward references),
whole-value proof by constants and guards, invalid defaults under supplied
siblings, required fields following defaulted fields, argument evaluation order,
private/default variant ownership, foreign generation rejection, invalid target
kinds, declaration name collisions, function-value restrictions, formatting and
descriptions. Migrate examples/config/settings to `fn New = Config.new`, keeping
Defaults and ForProduction as focused handwritten APIs. Update grammar and README
with the implementation.

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

fn handleCreate(body: codec.Value): User | ApiError = {
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
- **Explicit compile-time computation:** `comptime { ... }` evaluates pure code with closed captures and concrete types at compilation, with its own return boundary. Scalars, lists, records, sealed variants, unions and insertion-ordered Maps become typed literals and ordinary facts. Internal promises check before execution; result constraints check afterward. Native-target execution is bounded by ten seconds per recipe and a 16 MiB result limit. Multiple blocks share one built value evaluator within a compilation, while each dependency's value-type constraints and each block's preflight must pass before that block runs. Contextual result constraints retain the enclosing Facts pass. Runtime behavior and lazy cells cannot be baked; sorted maps require explicit `.inOrder()` export. See [the runnable comptime example](../examples/comptime/README.md) for a lookup table and validated build configuration. Comptime nodes are evaluated once within a compilation; their values are not independently reused across compilations. Compilations that use compile-time evaluation bypass Session and disk complete-result caches. Executable reuse can skip compilation and retain already-baked values when its recorded inputs are unchanged; see [executable reuse](design/executable-reuse.md). Standalone value reuse remains proposed in the [comptime design](design/comptime.md).
- **Explicit build inputs:** `bork/build.ReadString` and `ReadBytes` supply frozen module-relative files within comptime blocks and helpers declaring `uses build`. Calls require constant String paths; guarded sites are captured before execution. Runtime entrypoints, file escapes, operand symlinks, special files and invalid UTF-8 Strings are rejected. Established root symlinks retain tracked identities. Capture limits are 16 MiB per file and 64 MiB total; successful captures include content, negative lookup and component identity evidence. Existing predicate evaluation still trusts pure `unsafe go` signatures (see purity below).

### Facts, purity, and the outside world

- **Predicates are pure, and this is checked** (see [Effects in signatures](#effects-in-signatures)). A predicate whose answer could change for the same value (e.g. `isOpenNow(store)`, which reads the clock) would produce facts that go stale, so a predicate cannot call anything that uses an effect. The rule is: **effects produce values, and facts are about values.** For example, read `now = time.Now()` once, then use `isOpenAt(store, now)`. (`unsafe go` code is trusted to be as pure as its signature says.)
- **Facts about outside systems are facts about a point in time.** A fact can describe the outside world (a row exists, a file is present), but it is true as of when it was established. Keeping that current is the developer's job, not the compiler's. bork is aimed at typical web backend services, where a request reads its inputs, decides, and writes within a short window. Treating what it loaded as a fixed snapshot is the right model there. Concurrent changes elsewhere are handled with the usual backend tools (transactions, idempotency, optimistic locking), not by the proof system.
- **Compile-time evaluation only runs pure code**, since it runs predicates, apart from what `unsafe go` functions claim.
- **No widening syntax.** Facts are forgotten simply by passing a value where a less constrained type is expected (`Int where positive` to an `Int` parameter). No `x as Int` is needed.

### Open questions

- **Inline predicates:** only named predicates (`positive`), or also inline expressions (`where it > 0`)? Inline expressions require the compiler to recognise equivalent expressions.
- **Exported return types:** do callers see only the facts a signature declares, or also facts the compiler derives from the body? A proposal: exported functions expose only the declared facts (the signature is the contract), and private functions may expose derived ones.

## Compile-time dependency assembly

Assembly resolves ordinary provider functions at compile time, then invokes
them at runtime without a container (bork-25nywe).

Inspired by [q.Assemble](https://gigurra.github.io/q/api/assemble/), assembly
uses explicit provider signatures for wiring and Bork's ordinary scopes,
failure unions, facts and effects for its guarantees. An expression fits local
initialization and test fixtures without duplicating signatures in a declaration
language. q's lifetime terminators, cleanup discovery and scope cache are not
needed for this model.

### Syntax and providers

Three prelude compiler intrinsics take one explicit target type, a live scope,
and a positional list of providers:

```bork
assemble[Server](app, newConfig, openDb, newServer)
assembleAll[Plugin](app, newConfig, authPlugin, logPlugin)
assembleRecord[Application](app, newConfig, openDb, newServer, newWorker)
```

The intrinsics cannot be used as function values. Providers are ordinary
functions with concrete signatures: direct declared-function references, typed
function values, and lambdas with explicitly typed parameters. Methods use
lambda adapters; assembly does not add bound method reference syntax. Unspecialized generic
functions are rejected; write a monomorphic adapter (it can call the generic
function with explicit type arguments).
An unresolved open effect signature is rejected. A function value always means
an invoked provider. Inject existing values, including callbacks, with a
zero-argument provider (`() => config`), avoiding value/function ambiguity.
Scope and provider expressions evaluate once, left to right, before invocation.
Named arguments remain available inside providers and adapters; the intrinsic's
heterogeneous provider list is positional.

```bork
type Config = { database: String }
type Database = { connection: sql.Connection }
type Server = { db: Database }
fn newConfig(): Config { Config { database: ":memory:" } }
fn openDb(config: Config, s: Scope) uses io + net: Database | sql.Error {
  Database { connection: sql.OpenSqlite(config.database, s)? }
}
fn newServer(db: Database): Server { Server { db: db } }
fn boot(s: Scope) uses io + net: Server | sql.Error {
  assemble[Server](s, newServer, openDb, newConfig)
}
```

Every `Scope` parameter receives the target scope, in any position. Scope is
an input capability, not a product slot. `OwnedScope` inputs and outputs are
rejected: assembly borrows scopes without transferring closing rights. A helper
requiring two distinct scopes uses an adapter that captures one. All other
parameters resolve through providers, including parameters with defaults;
missing dependencies never silently select a default.

### Graph resolution and order

The product is the result type, or the leftmost member of a result union;
remaining members are failures, following `?`. Union-valued products are outside
this increment: if specialization preserves a union as a success member, reject
that provider rather than flattening successful alternatives into failures. Use
a wrapper record or sealed type as the product. `Option[T]` is a product, with
no automatic unwrapping. Slot matching uses exact Bork type identity, including
package identity and generic arguments, after separating facts. Distinct named
records, sealed types and resources are distinct slots. Transparent aliases,
including constrained aliases, are not brands: use wrapper records for primary
and replica databases. There is no numeric widening, structural record matching,
union-member injection, or class instance search. Facts are checked after
selection and never disambiguate competing providers.

`assemble[T]` requires exactly one provider of T and recursively resolves its
parameters. Missing slots, duplicate products, cycles, invalid signatures and
unused providers fail compilation. Duplicates are rejected throughout the
supplied set, even when otherwise unreachable. Every provider must be reachable
from a root; there is no unused exemption for configuration.

Each provider runs once per call and shared dependencies reuse that result.
Construction is sequential and deterministic: visit roots in their specified
order, dependencies in parameter order, then invoke the provider. This depth
first order defines effects and acquisition order. Provider-list order otherwise
does not constrain execution. The first failure stops the expression; remaining
providers do not run. Panics propagate normally. Separate assembly calls build
separate values, even in the same scope. To share across calls, bind a value and
supply `() => existing`; there is no scope-wide type cache.

### Failures, effects and facts

The expression returns `T | E1 | E2 | ...`, deduplicating failures in invocation
order; without failures it returns T. Collection assembly substitutes `List[T]`
for T, record assembly substitutes its record target. An ordinary `match` handles
failures; `?` propagates them from a function declaring that union. Failure
values are preserved, without a universal assembly error. Reject a failure type
identical to any graph product or the final success type: flattening would
confuse failure with success. Use a distinct failure type or adapter. Repeated
failure types from different providers are allowed.

Effects are the union of invoked provider effects and evaluation effects of the
scope/provider expressions. Evaluating a function reference does not invoke its
effects. There is no additional assembly effect; a pure graph remains pure.
An unused provider is an error rather than an effect silently discarded.

Direct declared-function providers retain their declaration contracts and lower
to direct calls, rather than losing facts through a function-value conversion.
Providers supplied through function values have only the guarantees expressed
by that value's type; they cannot smuggle in stronger declaration facts.
Generated calls obey ordinary parameter/result facts and scope relations.
Promised product facts flow downstream. Unmet requirements fail at the assembly
site with the responsible provider and parameter. Facts about captured values
and parameter relations are checked against resolved arguments, beyond erased
type compatibility. Assembly adds no implicit `trust`. Providers remain
responsible for their result promises and private construction boundaries.

### Process signals

Programs that open scopes cancel every root scope on SIGINT or SIGTERM, with
signal-named cancellation reasons and normal-return exit status 128+n (130/143).
Explicit exits remain authoritative. Copies within 500 ms count as one request;
a subsequent cancelling signal terminates immediately. Other signals retain
Go/OS behavior. Programs that never open scopes install no bork signal handler.

`bork/signal` provides scope-owned process-wide Configure, Ignore and Subscribe
registrations, with newest-live configuration restoration and subscription >
ignore > cancellation precedence. Grace is an optional nonnegative
`bork/time.Duration`, latched on first cancellation and never extended. Event
subscriptions have bounded 16-event buffers, stop on owner cancellation, and
remain registered until final release. MockSubscription and Emit inject events
without OS effects and support function-mocked Subscribe calls. Real operations
use io + state; mock injection uses state. Windows Ctrl+C/Break map to SIGINT,
console close/logoff/shutdown map to SIGTERM cleanup notifications subject to
Windows termination; unsupported Unix-only signals return Error.

See [bork/signal](std/signal.md) for API and examples.

## Resources and scopes

Acquisition providers take the target Scope and use ordinary resource APIs.
Those APIs register cleanup once. Assembly neither detects close methods nor
adds finalizers or attaches borrowed resources. Ordinary LIFO cleanup closes
later acquisitions before earlier dependencies. Cancellation, joins, cleanup
failures and policies remain the scope's semantics.

Products retain the lifetimes of dependencies, captures and scope-dependent
acquisitions, following ordinary call checks. An unrelated pure product does
not acquire a scope lifetime just from the intrinsic's leading argument.
Injected existing values retain their own ownership. Short-lived captures cannot
be retained in a longer-lived resource, just as in a handwritten call.

On partial failure, acquired resources stay owned by the supplied scope and
close when it ends, as with consecutive handwritten acquisitions. Assembly is
not transactional rollback. Use a dedicated lexical scope for prompt cleanup,
or assemble into an owned child's borrowed scope and close that child on
failure. No child is created or closed implicitly. Scope escapes, expired scope
inputs and use after an owned child closes fail ordinary lifetime checks.

### Collections and records

`assembleAll[T]` collects every exact T provider in provider-list order, with
at least one required. Each root and shared dependency runs once. Multiple T
providers are permitted only as collection roots; a dependency parameter of T
is still ambiguous, including a root's own parameter. Duplicate non-target
products remain errors. A `List[T]` provider supplies one list slot and is not
flattened. To consume the collection in another graph, assemble it explicitly
and inject `() => plugins`.

`assembleRecord[R]` requires a concrete record and synthesizes its literal.
Resolve each field as a root, in declaration order; repeated field types share
one value. A provider of R itself is unused. Fields with defaults still require
providers. List fields require a list provider without implicit aggregation.
Field facts and record invariants must hold; construction visibility is checked
at the call site exactly as for a handwritten literal. A `type R = private { ... }`
record can be assembled by fields only in its declaring package. Importers use
`assemble[R]` with an exported factory. Public records remain publicly
constructible; a private factory alone does not restrict literals. For a sealed
representation with private variants, use `assemble[T]` with its public factory.

### Diagnostics and describe

Report independent graph problems together in stable source order, with source
positions and codes `assemble.missing`, `assemble.duplicate`, `assemble.cycle`,
`assemble.unused`, `assemble.provider`, `assemble.failure`, and
`assemble.target`. Facts/effects/lifetimes retain ordinary codes with provider
context. No invalid graph executes.

Include the full rooted dependency tree: types, parameter/field names, provider
labels and positions, and implicit scope inputs. Mark missing slots, ambiguous
candidates, exact cycle paths, and shared nodes referencing their first
occurrence. Append every supplied provider, including invalid and unused ones.
Do not mark ambiguous candidates unused just because selection failed.

```text
Server <- #3 newServer
  db: Database <- #2 openDb
    config: Config ?? missing provider
    s: Scope <- target app
supplied: #1 unrelated -> String (unused), #2 openDb -> Database,
          #3 newServer -> Server
```

`bork describe` at an assembly expression shows mode, target, result union,
effects, tree, invocation order and provider list. Structured describe exposes
the same information with per-call provider IDs, explicit edges, scope inputs
and root field names. This is a compiler artifact, not runtime tracing.

### Test overrides and delivery

Tests replace an entry explicitly:
`assemble[Server](s, newConfig, fakeDb, newServer)`. Listing both real and fake
providers is a duplicate error; list order never overrides. A zero-argument
lambda injects a fixture. Native test mocks of effectful providers apply to generated ordinary calls
using the same test-local dispatch and inherited task context. Pure providers
are replaced explicitly in the provider list. A mock
preserves the declared contract; choosing a distinct fake can change effects
and failure types. Assembly intrinsics are not mockable, and have no separate
override registry.

Implemented: checker resolution, ordinary facts/effects/lifetimes, Go generation,
formatting, describe and structured diagnostics. Goldens cover ordering and
sharing, graph errors with full trees, failure short circuiting, success/failure
cleanup, lifetime escapes, effect/fact violations, generic and constrained
products, collection/record roots and test replacements. Include constrained
direct providers versus saved function values, promised downstream facts, and
monomorphic adapters for generic providers. `examples/assemble`
wires config, a database and HTTP server with scope-owned cleanup. Parallel construction,
assignability matching and cross-call caching are outside this first increment.

### Ordinary tuple provider bundles (bork-ys21yg)

Provider bundles are ordinary immutable tuples, for example
`Services = (newConfig, openDb, newService)`. The three assembly forms accept
statically known tuples in provider positions, including imported package values,
local aliases, function results and Swap results. They splice one level in
positional order, mixing multiple tuples with individual providers. Nested tuples,
lists, unions and nonconcrete function signatures are invalid. All graph checks
run after splicing; duplicated collection roots retain their written positions.

The scope and provider expressions evaluate once in written order before
providers execute in dependency order. Construction effects are checked even for
unused or rejected providers. A tuple stores functions, with ordinary function
value capture, fact and lifetime restrictions; it stores no assembly products.
Every assembly constructs fresh products. Constructors with uncaptured ambient
needs remain inline assembly providers or take explicit parameters. Inline
providers retain their ordinary declaration contracts.

`bork/test.Swap(tuple, replacement)` returns the original tuple type and replaces
the unique element with the replacement's exact checked type. Transparent aliases
resolve normally; nominal types remain distinct. Function type identity includes
parameters, results/failures and effects. No match is an error; multiple matches
report zero-based indices and suggest SwapAt. Lambdas use ordinary contextual
inference, with explicit annotations when needed.

`test.SwapAt(tuple, index, replacement)` requires a pure compile-time Int index
in `[0, arity)` and checks the replacement with that element's expected type.
Its actual type must be identical; no union widening or signature adaptation.
Both helpers require positional argument counts, reject explicit type arguments,
require a nonempty tuple, evaluate tuple then replacement once, and invoke no
providers. Unchanged positions retain provenance, facts and lifetimes; ordinary
fact/lifetime checks apply to the replacement. Helpers must be called directly.

Legacy `providers` declarations have no AST declaration or exported symbol. A
narrow parser recovery emits `migration.providers` with a safe comment-preserving
fix to a tuple package value, including a singleton comma. Empty, duplicate-label
or non-reference entries receive guidance without a fix. Legacy specialization
calls receive `migration.providers-call`: use Swap/SwapAt or an explicit tuple.
Swap requires the full exact element type, unlike old specialization that could
change dependencies, effects or failures while keeping the same success product.
See [package migration guidance](language/packages.md).

Graph descriptions retain provider IDs, dependencies, roots and invocation order.
Tuple providers add `tuple`, zero-based `index` and known `element_position`.
Literal diagnostics point at the element; other tuple arguments point at the use
and descriptions include a defining element position when available. A tuple
binding is described as an ordinary package/local value.

## Resources and scopes

Outside resources (files, sockets, database connections, transactions, locks) are the one place where "facts only grow" is under pressure: the handle value never changes, but closing it changes the world it refers to. bork handles this with scopes, in the style of ZIO, so the open state can never end while a resource is still reachable.

### Decided (v0.1)

- **Resources belong to scopes.** Opening a resource requires a scope. A resource can be attached to one or more scopes, and it is **closed when the last of them closes**. There is no manual `close`, so there is neither use-after-close nor forgot-to-close. (A scope can have an explicit end, as an [owned child scope](#partially-overlapping-scopes-owned-child-scopes), which is checked the same way.)
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

The following is an illustrative resource-contract sketch, including an original
concurrency syntax proposal. Current syntax and checked examples are in
[scopes](language/scopes.md); task creation uses `fork(s, () => work())`.

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
- **Structured concurrency (decided, implemented):** goroutines started inside a scope finish before the scope ends, so they can use its resources with no extra attaching. `fork(s, () => work())` starts a task of scope `s` and gives a `Task[T]`; `await(task)` waits for its result. Work that gives no value gives a `Task[Ok]` (so `T` of `fork`, `await` and `tasks.TryFork` may be `Ok`, unlike other type arguments, and work that never returns, `() => panic(...)`, also gives a `Task[Ok]`). A `Task[Ok]` may be dropped like an `Ok`: `fork(s, () => { ... })` is a statement, with no `_ =`, and the scope still joins the task and reports its panic. Only exactly `Task[Ok]` is relaxed; a `Task[Int]`, or a union such as `Task[Ok] | TaskLimitReached | Cancelled`, must be used. (`fork` replaced `spawn` and `launch`, whose names were found ambiguous; both are reported as `migration.spawn` and `migration.launch` with a fix to `fork`.) When a scope ends, however its block ends, it cancels itself, waits for its tasks, and then runs its finalizers. A task that panics panics `await`, or, if no one awaited it, the scope's routine when the scope closes. Lifetimes apply as to any value of a scope: a task cannot be returned from its scope or used after it, and its work may only use what lives as long as the scope. 
- **Finalizer failures and scope policies (implemented):** every finalizer of a scope runs, even when others fail; what failed (finalizers, and tasks that failed without being awaited) is raised as one panic once the scope has closed (`finalizer 4 failed; and then: finalizer 2 failed`). How a scope ends is configured with policies, values of the prelude's sealed type `ScopePolicy`, given after `with`: `scope s with taskTimeout(100), cleanupTimeout(500), logFailures() { ... }`. By default a scope waits for its tasks and finalizers for as long as it takes, and raises failures. `taskTimeout(ms)` bounds the wait for the tasks to stop after the scope is cancelled, overriding whatever cleanup time a task would take on its own: tasks still running then are *orphaned* (they keep running, and may find the scope's resources closed), while the scope's finalizers still run, on its own routine. `cleanupTimeout(ms)` bounds the wait for each finalizer; one that takes longer is orphaned, and the scope goes on closing. `logFailures()` logs failures at error level (through Go's `log/slog`) instead of raising them; a failing orphaned task is always logged so. Orphaning is logged at warning level.
- **Attaching (implemented):** a task of an outer scope may outlive the scope a resource was opened in. `shared = attach(conn, app)` keeps `conn` open until `app` closes too: a resource closes when the last scope it is attached to closes (reference counting, per resource). `attach` is allowed only where the resource is provably alive, and gives it as a value of `app`, which a task of `app` can use. Without it, handing the resource to such a task is a lifetime error that suggests `attach`. Resources made in `unsafe go` register how they close with `s.Own(...)`, which is what makes them attachable.
- **Channels (implemented; [design](design/channels.md)):** `channel[T](s)` (unbuffered), `channel[T](s, n)` (a fixed buffer, `n` proven `validCapacity`) and `unboundedChannel[T](s)` (a buffer that grows, so sends never wait) make a channel owned by scope `s`, which closes it when it closes (so, like a task, a channel cannot leave its scope). Operations are methods. Those that wait take the scope they wait in: `ch.send(s, x)` gives `Ok | Closed | Cancelled`, `ch.receive(s)` gives `T | Closed | Cancelled`, and both give `Cancelled` once that scope or the channel's own is cancelled, even if they could complete, including while they wait. `trySend(x)` (`Ok | Full | Closed`) and `tryReceive()` (`Option[T] | Closed`) never wait and do not check cancellation. A fixed buffer grows as it fills, up to its capacity. `close()` is idempotent and final: later sends, and senders waiting at the time, give `Closed`; receivers get the buffered values in order, then `Closed`. `length()` and `capacity()` (`None` when unbounded) describe the buffer. `values(s)` is a `Seq[T] uses state` that receives until the channel closes or `s` is cancelled (`for (x in ch.values(s))`), and `toList(s)` gives `List[T] | Cancelled`. `produce(s, capacity, work)` runs `work` with a new channel as a task of `s` and closes the channel when it returns; `merge(s, channels)` passes the values of every channel on to one, closed when all are. Values are received in the order sent, waiting senders and receivers are served first come first served, and selection among ready channels is uniformly random. The removed free functions `send`, `receive`, `closeChannel` and `received` are reported with the method to use.
- **Handoffs (implemented; [design](design/handover.md)):** `handoff[R](s)` and `handoff[R](s, n)` make a `Handoff[R]` owned by `s`, for a resource type `R` only. `h.handOver(s, r)` is checked as `move(r, <the handoff's scope>)`: `r` must be acquired here, from one scope, outside the loops around it and not pinned, but it may already belong to the handoff's scope. It ends `r`'s handles, so `r` and every value holding it are unusable afterwards, and it pins nothing. At run time the registration moves to the handoff's scope before the value is queued, so a receiver can move it on at once. A hand-over that gives `Closed` or `Cancelled` releases that registration, closing `r` unless another scope keeps it. A send keeps the sender's hold, so the receiver of a `Channel` only borrows; a handoff only ever holds handed-over resources, so `h.receive(s)` and the variable of `for (r in h.values(s))` get a fresh handle in the handoff's scope, created where they are received (each round, for the loop), which may be moved, attached or handed over again. This needs the handoff to be a variable or parameter (including one declared `in` another), and a later move finds its scope at run time from the handoff itself. A hand-over into a handoff whose scope has already closed (from an orphaned task) gives `Closed` and releases the resource. `tryReceive` gives borrowed values (an option's payload has no tracked origin). A `select` takes `h.receive(s)` arms, whose value is the receiver's own, and `h.handOver(s, r)` arms, which move `r` only if they are the arm completed: `r` is handed over where that arm's body starts, is still owned in the other arms, and is possibly handed over after the `select`.
- **Channels and atoms keep what is stored (implemented).** A value sent to a channel, or made by the function given to `update` or `swap` of an atom, must outlive the channel or atom, and what comes out (`receive`, `received`, `current`) is a value of the container's lifetime. A channel lives as long as its scope; an atom as long as its first value (so an atom started with a value of `app` can hold values of `app`, while one started with `Option.None` can only hold values that live forever). A resource of a shorter scope can be attached to the container's scope first. A function that stores a parameter declares that it lives as long as the container: `fn put(ch: Channel[Conn], c: Conn in ch)`, or, to store values of a scope parameter, `fn fill(ch: Channel[Conn], s: Scope in ch)`. The other direction, `ch: Channel[Conn] in s`, only says that the channel outlives `s`, so values of `s` cannot be stored in it.
  - **The checks are strict where lifetimes mix.** A stored value must outlive *every* scope the container's lifetime depends on, so a container mixed with shorter values (`if (c) { a } else { b }`, a record holding both, a generic call given both) is checked as the longest of them: it is still the same channel or atom. An atom started with a value of no scope belongs to the whole program, which a mix keeps, so it still takes only values of no scope.
  - **What keeps values is kept only by containers of the same lifetime.** What comes out of a container is treated as ending with it. For a resource that is safe (it is only usable for less long), but a channel, an atom, a scope, or a function that comes out could then be given values of the container's scopes while it really lives longer. So a `Channel[Channel[Conn]]` (or of scopes, or of functions) takes only channels of its own lifetime, and a parameter of such a type declared `in` such a container must be given one of the same lifetime; in the callee, where it is only known to outlive the container, it cannot be stored there.
  - **A scope, a function, a channel or an atom declared `in` another value only outlives it.** Values of the scope `s: Scope in c` live at least as long as `c`, so they can be stored where `c`'s can; but Go code given `s` (`onClose(s, ...)`, a task of `s`) may keep its arguments until `s` closes, which can be later than `c`. Such code is given only values that outlive every scope its scope argument depends on.
  - **No back doors.** The fields of `Channel` and `Atom` are internal to the prelude: they cannot be read, called (`ch.sendFn(x)`), matched, copied, or built elsewhere, so storing always goes through these checks. `send`, `update`, `swap`, and functions with a parameter declared `in` another cannot be used as function values when that parameter's type can belong to a scope (`send` of `Channel[Int]` can), since a call through a function value is not checked; a lambda that calls them is.
  - **A lambda given to a generic function gets its parameters' lifetimes from the other arguments.** A generic function cannot make a value of its type parameters' types, so what it passes its lambda as one comes from its arguments: `conns.forEach(c => { _ = send(ch, c) })` sends values that live as long as `conns`, which it may when `conns` outlives `ch`. This holds for a parameter whose type is built only from the function's type parameters (`T`, `List[T]`, `Option[T]`, records of them; not `Conn` or `Channel[T]`, which the function could make in a scope of its own), which then lives as long as the arguments whose types mention those type parameters. Those arguments must hold the values as data, or in a channel or an atom (`List[T]`, `Map[K, V]`, `Channel[T]`): one holding a function (`(Scope) => T`, `(Conn) => T`) could make a value from what the callee gives it, in a scope of the callee's own. It also needs those arguments to come before the lambda, the lambda's result not to mention them (`fold`'s accumulator may hold what the lambda captured), and the type parameters' bounds to have no method that could make a value of the type: none whose result mentions it, or that takes it inside a function or a channel (`Eq`, `Ord` and `Show` qualify). Code in `unsafe go` bodies is trusted to keep to this, as it is to keep a result to its arguments' lifetime: a generic Go function that keeps a value from one call (a cache, say) and passes it to a later call's lambda is not checked.
  - **Known limits.** Other lambda parameters (of a function taking `(Conn) => Ok`, of a function value, or when the conditions above do not hold) are not known to outlive anything, so storing them is rejected (store the value directly, or use a helper with `x: Conn in ch`). An atom started empty (`atom(Option.None)`) can only hold values that live forever; start it with a value of the scope instead. `unsafe go` code given a channel or an atom can store anything, as it can keep any argument.
- **Cancellation through scopes (implemented):** a scope carries what Go's `context.Context` does. `cancel(s)` cancels it, `cancelAfter(s, ms)` sets an observable deadline that only ever shortens (existing children observe later ancestor deadlines too), a task that panics cancels its scope (so its siblings stop), the scope's end cancels it, and a scope nested in another (in the same function) is cancelled with it. Cancellation is cooperative: tasks see it at cancellation points, `delay(s, ms)` and `checkpoint(s)` (both `Ok | Cancelled`, so `checkpoint(s)?` stops a loop), and channel operations. A cancelled scope still waits for its tasks. **The scope does not decide how its tasks stop:** its end is the same signal whether the block finished, returned early, or panicked, and each task chooses what to do with it: stop at once (a worker waiting in `delay` or on a channel), clean up first, or finish its work (code that never checks for cancellation runs to the end). Work the block needs done is awaited before the block ends. In `bork/http`, each request has a scope, cancelled when the client goes away or the server's scope closes. Scope contexts preserve external deadline limits and context values. Nonpositive `cancelAfter` delays cancel immediately; huge positive millisecond values saturate instead of overflowing. Deadline expiry has the Go `context.DeadlineExceeded` cause and error, whose text is "context deadline exceeded".

HTTP applications can run several listeners in one scope. `http.WaitAny`
returns when any listener stops or is cancelled; `http.WaitAll` waits for all
of them. Both use `net`, return `Ok` for cancellation or an empty list, and
report unexpected stops as `IoError` naming the listener address. WaitAll
selects its first failure in list order. Scope cleanup drains the listeners.
See [Multiple listeners](std/http.md#multiple-listeners).

### Backpressure

HTTP admission before body buffering, typed overload/deadline
failures, and shared retry budgets are described in
[the backpressure design](design/backpressure.md) (bork-l0kn5g). Ordinary
`fork` keeps its signature; the explicit bounded task pool below
is implemented. HTTP retries are opt-in, limited by a shared
budget and remaining deadline. HTTP admission and typed client failure results are implemented; retry APIs remain planned.

### Bounded task pools (implemented, bork-l0kn5g)

`bork/tasks` adds explicit capacity shared across submitting scopes.
`tasks.Open(s, maxTasks: n)` creates a scope-owned Pool with a proven positive
limit. TryFork returns `Task[T] | TaskLimitReached | Cancelled` (`Task[Ok]` for
an Ok callback, which the caller may drop once admitted); it charges state plus
callback effects and never waits. Rejected callbacks are not called. A task holds its slot
until the callback ends or panics, and its explicit scope owns and joins it.
The compiler checks that pool and captures outlive that task scope. Resource
attachment extends pool ownership without moving existing tasks. Ordinary
`fork` and parallel collections retain their APIs and do not consume an
implicit pool. See [the package documentation](std/tasks.md). HTTP admission also limits work before body reads and queues waiters in bounded
FIFO order, rejecting with 429/503 and Retry-After. AdmissionState exposes load.
Clients return Response, Overloaded (retaining the original 429/503 response),
DeadlineExceeded, Cancelled, or IoError, with safe Retry-After parsing.
Shared HTTP retry budgets charge clock + state for creation and operation effects plus clock + random + state for explicit retries. Initial attempts are free; retries atomically consume a shared, lazily refilled token, preserve Retry-After minimums, and stop when waits cannot fit the scope deadline. Budgets and injected clocks have checked scope lifetimes, and last-owner closure cancels waits and active attempt scopes. See [HTTP documentation](std/http.md#shared-retries) and [the backpressure design](design/backpressure.md).

### Partially overlapping scopes: owned child scopes

> **Implemented** (bork-u6nhjb). Lexical scopes stay the default, and the
> enclosing-scope operations below cover most cases. Owned child scopes, with
> an explicit start and end, cover the rest.

The question is whether B can begin while A is open, then remain open after A
ends. Distinguish keeping a resource usable from releasing its previous owner
promptly, and keeping a task running from moving its ownership.

| Concrete case | Enclosing-scope solution | What remains |
|---------------|--------------------------|--------------|
| A request creates a pool that must survive the request | Give the acquiring function the application's scope, or `attach(pool, app)` while the pool is live and return that attached value | Already supported; the caller must supply the application's capability |
| Stream an old file into a new file, release the old file, then keep writing the new one | Open the destination in an enclosing scope, open the source in a nested scope, copy, then leave the source block | Already supported; choose the destination owner before the copy |
| Prepare a connection in a setup scope, then run a worker after setup ends | Attach the connection to the worker's enclosing scope before `fork` there; capture the attached value | Already supported; setup's tasks still finish with setup |
| Transfer an already running task out of a request | Start it in the longer-lived scope initially, with captures proven to live that long | Moving the task handle cannot migrate its captured scopes, channels, cancellation points, or failures |
| Repeatedly acquire the next lock before releasing the previous lock, or rotate sessions with at most two live connections | An enclosing scope keeps every attached acquisition until that scope ends | Prompt release of each old acquisition is missing; retaining all generations changes lock behavior and grows live resources |

For example, copying into a destination that survives the source needs no new
scope construct (the helpers here stand for ordinary scoped resource functions):

```
fn replace(path: String, app: Scope) uses io: File | IoError {
  output = createReplacement(path, app)?
  scope inputScope {
    input = openPrevious(path, inputScope)?
    copy(input, output)?
  }                                        // input released here
  output                                   // owned by the caller's app
}
```

A finite number of handovers can similarly be rearranged into nested blocks.
The unbounded rolling case is different: A must end with B still open, then B
must end with C still open, with cleanup at each transition. Opening every
successor in the application scope, or recursively retaining all predecessors,
does not meet that bound. A worker routine owning each generation could provide
independent lexical lifetimes, but requires a handover/acknowledgement protocol,
cancellation and failure handling; it is an alternative to evaluate, not a
transparent resource move.

**What `attach` means today.** It adds shared ownership; it does not remove the
source owner's finalizer. The resource closes only when every retained owner
ends. Its returned value carries the destination lifetime; older aliases do not
acquire a new lifetime. For handles using the [rebinding hook](std-go.md),
cancellation follows every owning scope, including the one it was opened in: the
resource is cancelled once all of them are cancelled, not when one of them is,
so attaching to a shorter scope cannot narrow its cancellation (bound I/O with
timeouts instead). An already cancelled resource cannot be revived. Any proposed move must
state both its ownership and cancellation semantics; copying the lifetime of a
value alone would be insufficient.

**Resource handoff before scope values.** A move (now implemented, see
[Moving resources](#moving-resources-between-scopes)) has to retain the
destination before removing the source registration, rebind cancellation, and
preserve one final cleanup even on failure. It could simplify ownership
bookkeeping for the first three cases, but `attach` already supplies their
required lifetime. It does not solve rolling cleanup: the destination still
retains the old acquisition until it closes. A sibling destination is not a
capability visible from the source in ordinary lexical code. Task migration is
excluded: moving a task's registration without rechecking every capture and
changing its cancellation and failure ownership breaks structured concurrency.

**Owned child scopes (implemented).** The rolling case needs an explicit
start and end, restricted so that the right to end a scope cannot be confused
with an ordinary borrowed `Scope`, nor copied:

```
fn roll(prev: OwnedScope in app, conn: Conn in prev, app: Scope, n: Int) uses io + state: Ok | Failed {
  next = openScope(app)                    // opens while prev is open
  nextConn = connect(next.scope)?          // b.scope borrows the child's Scope
  handOver(conn, nextConn)?
  closeScope(prev)                         // cancels, joins, releases conn
  if (n > 1) {
    roll(next, nextConn, app, n - 1)       // passes next on, with nextConn
  } else {
    closeScope(next)
  }
}
```

- **Opening and closing.** `openScope(parent)` (or `openScope(parent, policies)`)
  opens a child of `parent`, a `Scope` the caller already has, and gives an
  `OwnedScope`. `b.scope` borrows its `Scope`, for acquisitions and tasks.
  `closeScope(b)` ends it as a scope block's end does: it cancels it, waits
  for its tasks (under its own `taskTimeout`), closes its own children, runs
  its finalizers, and raises their failures as one panic (or logs them, under
  `logFailures()`). There is no `Scope.close`: an ordinary `Scope` remains a
  borrowed capability, and cannot close a caller's scope.
- **Owners are affine.** An owner is consumed exactly once on every path that
  finishes normally: by `closeScope`, by passing it to a parameter declared
  exactly `OwnedScope`, or by returning it (a function's whole result can be
  `OwnedScope`). Anything else is a compile error: binding it again (`c = b`),
  closing or passing it on in a lambda (a lambda can borrow `b.scope`, and then
  belongs to the child),
  holding it in a record, list, union, option, map or function type, giving it
  as a type argument or to a function value, or to user `unsafe go`. Owners
  are flow-checked: a use after close or hand-over is an error, an `if` or
  `match` must consume the same owners on every branch that goes on (a
  branch that returns or panics does not count), and a match guard or the
  right side of `&&` or `||` cannot consume one, since it may not run. An
  owner still open where its block (or, for a parameter, its function) ends
  normally is an error: the end is explicit. A path that ends early (`?`,
  `return`, a panic) closes the owners still open in the function, as a scope
  block's end does: those bound inside a scope block close before it, newest
  first, and the rest as the function ends. In Go, each owner variable defers
  a fallback that a hand-over disarms; an owner is taken out of its variable
  only when its call starts, so an argument after it that returns early or
  panics still leaves it to the fallback.
- **Close invalidates every dependent value.** The child of an owner is a
  scope in the lifetimes: values made with `b.scope` (resources, tasks,
  channels, records and lists holding them, lambdas using them) belong to it,
  and are "possibly released" once `b` is closed or passed on, including after
  a branch that did so, or once an owner it belongs to is (closing an owner
  closes its children: their owners must close first). A resource attached to another open scope
  (`attach(r, other)`) keeps that proof. Values of a child never leave the
  function owning it: by the time it returns, the owner is closed or returned
  instead.
- **Outlives is only the parent chain.** A child is outlived by its parent,
  and by what outlives the parent: a scope that outlives every scope the
  owner value belongs to (an owner returned by a call belongs to all of its
  arguments' scopes, so only those that outlive all of them count).
  Function parameters and enclosing scope blocks do not outlive it
  automatically, because an owner can be returned and so outlive them. A task
  of the child can use values of its parent; a value of a sibling or of an
  enclosing scope block needs `attach(r, b.scope)` first. A task of an
  enclosing scope cannot use the child's values or its borrowed scope, which
  could close under it. A child's children must be closed before it.
- **Belonging is declared at calls.** A parameter can say which scope its value
  belongs to: `conn: Conn in prev`, where `prev` is a parameter of type
  `OwnedScope` (the value belongs to its child), `Scope` (the value belongs to
  that scope), or any other that can belong to scopes (the value lives as long
  as that one, as a value stored in a channel `ch: Channel[Conn]` must). The
  caller must give a value that outlives every scope the target depends on.
  A target that cannot belong to a scope (`xs: List[Int]`) is an error, as are
  parameters that belong to each other in a cycle. A
  call that takes an owner (directly, or inside an argument, as `{ b }` or
  `pass(b)`), or whose arguments close one, cannot also be given a value of
  its child, unless that parameter is declared `in` the owner's: the callee
  could close the owner and go on using the value. `prev: OwnedScope in app`
  says that `app` outlives the child, so its tasks may use `app`'s values:
  the caller must give an owner whose scope `app` outlives (its child, or a
  child of it, not a sibling); when `app` is itself an `OwnedScope`
  parameter, it must be given as an owner variable. A function value called
  after an argument closed an owner it belongs to is also rejected. When the
  function ends early, a parameter's fallback closes it before the
  parameter it is declared in. For a
  `Scope` it also lets a task of `app` use the value without `attach`
  (`pool: Pool in app`).
- **Cleanup ordering.** Closing a scope cancels it, waits for its own tasks,
  closes its owned children still open (oldest first), and then runs its
  finalizers (LIFO). Children close after the tasks, because a task may own a
  child of its scope, and before the finalizers, because the children's
  tasks and resources may use what they release. Owners close their children
  before that, so children still open then are those an orphaned task (left
  running by `taskTimeout`) owns; this backstop closes them under it, oldest
  first (a task of a child can own a newer child, which it closes itself once
  the older child's close cancels and waits for it). Their failures join the
  parent's: task failures, then the children's, then the finalizers', as one
  panic. A second close of a scope already closing returns at once, possibly before
  the first has finished (only an orphaned task can race its parent so).
- **Cancellation.** A child is cancelled with its parent (its context derives
  from the parent's); `cancel(b.scope)` cancels only the child. Cancellation
  alone neither closes a child nor proves it closed. A child opened in a
  cancelled parent starts cancelled.
- **Effects.** `openScope` is pure, like a scope block; `closeScope` uses
  `state`, as `cancel` does. The effects of tasks and finalizers are charged
  where they are started or registered, so passing an owner on does not hide
  any.
- **Not done.** Moving a running task between scopes is not offered: a task's
  captures, cancellation and failures stay with the scope it started in. A
  resource registration can be moved (`move(r, b.scope)`, or out of a child;
  see below). A worker routine owning each generation was the
  alternative; it needs a hand-over protocol, and its owned resources still
  could not leave its routine. Rolling uses recursion, so an unbounded roll
  grows the Go stack, as any bork recursion does.
- **Tests.** `testdata/cases/owned_scopes` (hand-over-hand rolling, early exits,
  attach, cancellation, returned owners, parent closing children first),
  `owned_scopes_testing` (panics in the owner and in the receiver, failures of
  finalizers and tasks, parent aggregation, `logFailures`), and
  `owned_scopes_fail` / `owned_scopes_type_fail` (rejections).

### Moving resources between scopes

> **Implemented** (bork-u8ndmy). The design, with a comparison to Rust, C++,
> Swift and Go, is in [design/move.md](design/move.md).

`move(r, s)` hands the registration of resource `r` with the scope it belongs
to here over to scope `s`: the source no longer keeps it open, and it closes
when `s` closes (or later, if it is attached to other scopes still open).
`attach` adds an owner; `move` replaces one. Afterwards `r`, and every value
holding it (records, lists, lambdas, values made from it), is a compile error
to use, and a branch that moved it leaves it possibly moved.

- **What can be moved.** A resource acquired in this function or lambda:
  the result of an `unsafe go` function, or of a bork function that the
  compiler verifies acquires into its one `Scope` parameter, given exactly one
  scope (a scope variable or `b.scope`); or of `attach` or `move`. It flows
  through bindings, `?`, blocks, `if`, `match`, a pattern binding the whole
  value, and generic bork functions that give back a `T` they were given
  (`fs.Open`'s `result(...)`). Anything else is borrowed and cannot be moved:
  parameters, lambda parameters, record fields, values from channels, atoms or
  tasks, and results of other calls. The error suggests `attach`.
- **Pins.** A value that a task, a channel, an atom, Go code given a scope,
  a parameter declared `in` another, an async binding or a mock may still use
  cannot be moved, until the scope that keeps it has ended (a scope block, or
  an owned child opened here and closed with `closeScope`, whose policies
  cannot orphan tasks or finalizers: none, or `logFailures`). Policies are
  given only where a scope starts; `setScopePolicy` cannot be called
  directly.
- **Rules.** One source scope; a different target; not inside a lambda or a
  loop body for a value acquired outside it; a direct call (not a function
  value); no other argument of the same call may hold the moved value.
- **Runtime.** Each registration is named, and a move transfers one
  atomically: a failure (already closed, not owned by the source, target
  finished closing) panics and changes nothing. Cancellation follows every
  owning scope, so it follows the target instead of the source. Registrations
  moved out of a long-lived scope are compacted away.
- **Trust.** Go code given a scope that returns a resource returns a fresh
  registration in that scope, and keeps no task using it (see
  [std-go.md](std-go.md)). As for `attach`, a resource is usable while it has
  a live registration.
- **Tooling.** A use after a move offers the fix "attach instead of moving"
  (`bork check --json`, and an editor quick fix). Hover and `bork describe`
  show a resource variable's ownership (`ownership` in JSON): owned and
  acquired here (so movable), borrowed, kept by a task, channel or Go code,
  moved, or possibly moved; a definition says how the variable ends up.
- **Tests.** `testdata/cases/move`, `move_fail`, `move_type_fail`, the Go
  runtime test `internal/gen/move_test.go`, `TestDescribeOwnership` and
  `TestHoverOwnership`.

### Crash isolation and supervision (planned, not v0.1)

Erlang-style isolation, where a crashing routine is cleaned up and handled by a supervisor without taking the rest of the program down, becomes cheap in bork, because the two hard parts are solved by design:

- **Immutable values cannot be left half-changed.** A routine that crashes cannot corrupt ordinary bork values. Shared opaque Go state remains governed by its Go API. Its values are simply never used again, and the GC reclaims the memory. No memory tracking is needed.
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

## Effects in signatures

> **Implemented** (bork-ot9ki9). The syntax is in [grammar.md](grammar.md).

Any function can print, read files, call the network, or update shared state, so its signature says which of these it does. For code written by agents, the signature is what a reviewer checks, so it should state what the function can do to the world outside its arguments and result. Below `main`, a function that declares nothing does nothing outside them, and the compiler checks this. The exceptions are deliberate and few: logging, the runtime's own diagnostics, and `unsafe go` code (checked only for obvious effects, and allowed only where `bork.mod` says so).

### Annotations, not capability parameters

Two designs were weighed. **Capability parameters** make authority a value (`fn fetch(net: Net, url: String)`), passed down from `main`, as scopes already are. They need nothing new in the type system, can be narrowed (a file system rooted at one directory), and let tests pass fakes. But a lambda captures them, so a function *type* says nothing: `(Int) => Int` may call the network through a captured `net`, and telling them apart needs Scala 3-style capture checking. They must also be threaded through every function between `main` and their use, which in practice ends in one `Sys` record that every function takes, and `println(console, ...)` on every line.

**Effect annotations, pure by default,** were chosen: `fn fetch(url: String) uses net: Response | IoError`, and a function without `uses` is pure. Function types carry effects too (`(Int) uses net => Int`), so lambdas and function values are covered. A missing `uses` costs one word, and the compiler names it. Purity can be required where it matters: predicates must be pure, which the facts model needs (see *Facts, purity, and the outside world*). The usual cost, effect polymorphism for higher-order functions, is kept to zero annotations by open parameters (below), the way Swift's `rethrows` does.

The capability values bork already has stay: scopes, resources, atoms, and channels are ordinary values. The parameter says *which* resource or lifetime a function works on, and `uses` says *what kind* of outside action it takes. Effects are checked annotations, not wrapper types: code stays in direct style ("no effect monads"), and nothing changes in the generated Go.

### The effects

The set is small and fixed in v0.1: `io`, `net`, `clock`, `random`, `state`, and compile-time-only `build`. Libraries cannot declare their own effects. Each effect is coarse enough to annotate cheaply, and fine enough to answer the questions a reviewer asks.

| Effect  | Allows | Prelude and standard library |
|---------|--------|------------------------------|
| `io`    | standard streams, files, the process | `println` (built in), `eprintln`, `args`, `exit`, the `bork/fs` functions that touch the file system, `process.Args`, `process.Exit`, `log.Configure`, `env.Get`, `env.Require`, `env.All`, `env.Load`, `env.LoadJson` |
| `net`   | the network | `http.Listen`, `http.Wait`, `http.WaitAny`, `http.WaitAll`, `http.Get`, `http.Post`, `http.Send`, and the `bork/net` sockets |
| `clock` | time and waiting | `sleep`, `delay`, `cancelAfter`, `time.Now`, `time.Read` (a `time.Clock`'s `now` uses `clock`), `time.Sleep`, `time.After`, `time.Tick`, HTTP listener admission waits and client Retry-After dates |
| `random` | random numbers | none yet (the future random number functions) |
| `build` | captured module files inside comptime blocks | `bork/build.ReadString`, `bork/build.ReadBytes` |
| `state` | state shared between tasks | `current`, `update`, `swap`, channel operations, `cancel`, `cancelled`, `checkpoint`, HTTP admission, `http.AdmissionState`, and client cancellation |

- **Some functions have two or more effects:** `delay(s, ms)`, `cancelAfter(s, ms)`, and `time.Sleep`, `time.After` and `time.Tick` are `clock + state` (they wait, and they observe or cause cancellation); `process.Run`, `Start` and the Process methods that wait or signal (`Wait`, `TryWait`, `Signal`, `Stop`, `Kill`) are `io + state`; the `bork/sql` functions are `io + net`, since a database may be a local file or a server; and the `bork/net` socket functions are `net + state` (`Listen`, `Wait`, `Bind`, `Resolve`), or `net + state + clock` where they read, write, or dial with a deadline.
- **Pure means deterministic, with no outside action.** Calling a function that uses nothing twice with the same arguments gives the same result, and has no observable effect beyond allocating memory, logging, and maybe panicking. That is why reading an atom, or checking whether a scope was cancelled, is `state`: the answer can change between two calls. One known exception: an unordered map (`m.unordered()`) lists its entries in an order that differs between runs, and listing it stays pure. Seeding that order per program would close the gap, if it turns out to matter.
- **Memo/task observations account for effects at creation.** Task.wait observes work charged at fork. The proposed [lazy value](design/lazy.md) read similarly keeps type T and charges initializer effects at declaration/construction, although the first read can run that work. Such access is a stable memo observation, not permission to treat the initializer as a repeatable pure call. Compile-time predicate evaluation must never force a runtime lazy cell, including through an otherwise pure accessor or validator.
- **The compiler's built-ins:** `println` and `assertSnapshot` (which writes snapshot files under `--update`) are `io`, and `toString`, `panic`, the conversions, `assert`, and `assertEqual` are pure.
- **Making things is pure.** `atom(x)`, `channel(s, n)`, `fork`, `await`, `attach`, `onClose`, `scope` blocks and their policies, `http.Address`, `http.Text`, path functions such as `fs.Join`, JSON, CSV, strings, lists, and maps use nothing on their own. (`fork` and `onClose` take on the effects of the work they are given; see open parameters below.)
- **`panic` is pure.** It signals a bug, not an effect. The same goes for what the runtime does on its own when a scope ends: logging an orphaned task, or a failure under `logFailures()`, and the timing of `taskTimeout` and `cleanupTimeout`. These are runtime diagnostics, not actions of the code.
- **Logging is not tracked.** Writing logs with `bork/log` (`log.Info`, `log.Debug`, `log.Log`, ...) is allowed in any function, pure ones and predicates included, with no `uses`. It is also the way to trace a pure function while debugging. `log.Configure` is `io`, though: it decides for the whole program where logs go (stdout, say), so it belongs in `main`, and pure code cannot turn logging into printing. A log line added deep in a call chain should not ripple a declaration up through every caller, and logs do not change what a program computes. When compile-time evaluation runs a predicate that logs, the log output is discarded.
- **Randomness is its own effect**, `random`, beside `clock`: both make a function nondeterministic without touching anything outside the process. The standard library has no random numbers yet; the `unsafe go` check below uses the effect.

### Syntax

```
fn describe(u: User): String { ... }                          // pure
fn save(path: String, text: String) uses io: Ok | IoError { ... }
fn serve(addr: String) uses net + state: Ok | IoError { ... }
fn main() { ... }                                             // may use every runtime effect
fn now() uses clock: Int unsafe go { ... }

type Job = { name: String, run: () uses io => Ok }         // a function type with effects
fn handler(store: Atom[Store]): (Request, Scope) uses state => Response { ... }
```

- **`uses` comes after the parameters**, before the result, in declarations and in function types alike, the way Swift writes `throws`. Effects are joined with `+`, as type parameter bounds are (`T: Show + Eq`); a comma would clash with the comma between parameters.
- **So it is never unclear what `uses` belongs to.** `handler` above is pure: it builds a function that touches state. Unions in results need no parentheses either (`(String) uses net => Response | IoError`).
- **`uses nothing`** marks a function-typed parameter that must be pure (see below), or a `main` that must be (see below). Elsewhere, leaving `uses` out already means pure, and `uses nothing` is allowed but redundant.
- **Grammar** (see [grammar.md](grammar.md)): `FuncDecl` and `MethodSig` have an optional `Uses` after the parameter list, and `FuncType` one before its `=>`: `Uses = "uses" ( "nothing" | Ident { "+" Ident } )`. On a declaration, it comes before the result and before `where` (`fn f(x: Int) uses io: Int where positive unsafe go { ... }`).
- `uses` and `nothing` are keywords only in these positions, and the effect names are only names there, so a package or value may still be called `state` or `random`.
- **An unexported function may not declare effects its body never uses.** This matches unused imports, and keeps `uses net` meaning "may call the network". Exported functions may declare more than they use, to keep their API stable when the body changes. So may `main`, class methods, `unsafe go` functions (whose Go is checked only for missing effects, below), and function types.

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
- **What has `open` among its effects can only flow to another open position:** an open parameter of a call, or the open result of its own signature. `open` fits in no fixed set of effects, so such a value cannot be stored in, or passed as, a function type with fixed effects, pure or not (a `run: () uses io => Ok` field could then hold a function that uses `net`). Nor can it escape through a generic result or an untyped element: `[f]`, `Option.Some(f)`, `identity(f)`, `() => f()` in a record field. To store a function, close the parameter: `fn job(name: String, f: () uses io => Ok): Job`.
- **An open parameter can be returned.** A function type without `uses` in the *result* of the same signature is open too: `fn compose[A, B, C](f: (A) => B, g: (B) => C): (A) => C` gives a function that uses what `f` and `g` use. A value returned in an open result may use `open` and nothing else, so `fn wrap(f: () => Ok): () => Ok { () => { println("x"); f() } }` is an error: there is no way to write "io plus what f uses". (Write `wrap(f: () uses io => Ok): () uses io => Ok` instead.)
- **A call is charged the effects of its open arguments, unless the function's result is open.** Then they are carried in the result's type instead: `compose(a, b)` is pure, and the function it gives uses what `a` and `b` use, as a hand-written `handler` closure does. In exchange, a function with an open result may not call its open parameters itself, only return them in its result (or pass them to calls whose results it returns). This keeps the rule a matter of signatures, not of what a body does.
- **Which positions are open:** a parameter (receivers included) or result whose type is a function type written without `uses`, also through a type alias (`handler: Handler`, with `type Handler = (Request, Scope) => Response`). A direct `List[() => Ok]` parameter also opens its element callback and charges all its callbacks at the call; an explicit `uses nothing` keeps the elements pure. List results, nested lists, and list aliases stay closed. Nothing else: not a union such as `((A) => B) | None`, and not the parameters or results inside a function type (`(A) => (B) => C` has an open result `(B) => C` only at the top). So a middleware type such as `((Request) => Response) => (Request) => Response` takes pure handlers only, and one that wraps effectful handlers names their effects.
- **A function with open parameters used as a value** (`g: (List[Int], (Int) uses nothing => Int) => List[Int] = transform`) has its open positions closed as pure: `g` is `(List[Int], (Int) uses nothing => Int) => List[Int]`, all pure. Calling `transform` directly keeps it open.
- **Generic inputs may forward open callbacks when their type parameter does not occur in the result.** For example, `fs.map(f => f(x))` can consume an open callback list and return closed values. A generic result containing that parameter still rejects the open type argument, preventing it from escaping.
- **Generic code needs nothing extra:** if `T` is `() uses io => Int`, the effect is part of `T`, so `identity`, `head`, and `Option[T]` carry it. Since effects are erased in the Go output, a type pattern on a function type (`f: () => Ok`) may only match a union member known statically to have the same effects, as facts are treated.
- **`uses nothing` makes a parameter strictly pure.** `fn update[T](a: Atom[T], f: (T) uses nothing => T) uses state: T` is how the prelude says "f may run more than once, so it must not do anything".
- **A fact named after a function argument needs a pure argument.** `xs.filter(keep)` gives `List[T where keep]`, and that fact, "keep holds", only means something if `keep` gives the same answer every time. So an effectful `keep` still filters, but the result is a plain `List[T]`. The facts its body proves with predicates are kept either way (predicates are pure): `xs.filter(x => { println(x); positive(x) })` uses `io` and still gives positive elements. Inside a function whose parameter is open, a fact named after that parameter holds on the same condition: the prelude's method `fn (xs: List[T]) filter[T](keep: (T) => Bool): List[T where keep]` promises the conditional fact, and at each call the fact is kept only if that argument is pure.

The prelude's list, `Option`, and map methods have open function parameters; `update` and `swap` take strictly pure ones.

**Records of functions made in Go carry their effects.** The prelude's `Atom` and `Channel` are records whose fields are functions made by `unsafe go` (`currentFn`, `sendFn`, ...), visible to any code that has the record. Their types must say what they do, `currentFn: () uses state => T`, or a program could call `store.currentFn()` with no `uses state`. The general rule: an `unsafe go` function that returns or builds function values types them with their effects, as part of the signature bork trusts. (`Task`'s `wait: () => T` stays pure: the work's effects were charged at `fork`.)

### Lambdas, methods, and classes

- **A lambda's effects are inferred from its body.** Nothing is written. A lambda checked against a function type with `uses X` may use at most `X`. A lambda checked against a pure function type must be pure. One passed to an open parameter may use anything, which charges the call.
- **Named functions as values** have the effects they declare: `lines.forEach(line => println(line))` uses `io`.
- **Function types are ordered by their effects.** A function that uses less fits where more is allowed: a pure `(Int) => Int` can be passed as `(Int) uses io => Int`, but not the other way round.
- **Methods** declare `uses` as functions do. `fn (c: Client) fetch(url: String) uses net: Response | IoError`.
- **Class methods** may declare `uses`, and an instance's method may use at most what the class declares (an error with no automatic fix, since the fix may belong on the class). The prelude’s `Eq` and `Ord` classes and `codec.Decode` and `codec.Encode` are pure, so their instances, including derived ones, must be pure.

### `unsafe go`

- **An `unsafe go` function declares its effects, and bork trusts them** as far as the check below can see, as it trusts the rest of the signature. `fn now() uses clock: Int unsafe go { ... }`.
- **`unsafe` is not an effect.** If it were, every caller of the prelude's functions written in Go would be "unsafe", and the word would mean nothing. The trust boundary is the declaration.
- **Which packages may contain `unsafe go` is declared in `bork.mod`:** `unsafe "example.com/shop/ffi"`. The prelude and the standard library always may. Every other package that has an `unsafe go` body is an error. So new Go code shows up in review as a change to `bork.mod`. The module's root package is named by the module path (`unsafe "example.com/shop"`). A program without a `bork.mod` cannot use `unsafe go`; `bork run` on a single directory still works, it just has to be all bork. Module paths `bork` and `bork/...` are reserved for the standard library.
- **A check for honest declarations:** an `unsafe go` body that uses a Go name with an obvious effect must declare that effect. The check looks at the names used, not at the imports, since `time.Duration` alone is pure:
  - `io`: `os` (except its error types and values, `Is...` functions, `FileMode`, `FileInfo`, `DirEntry`, `ModeDir`, and `ModePerm`), `os/exec`, `os/signal`, `syscall`, `io/ioutil`, and `fmt.Print*`, `fmt.Fprint*`, `fmt.Scan*`, `fmt.Fscan*`;
  - `net`: `net` and `net/...`, except `net/url`, `net/netip`, and `net/mail`, which only parse and format;
  - `clock`: `time.Now`, `Since`, `Until`, `Sleep`, `After`, `AfterFunc`, `Tick`, `NewTimer`, `NewTicker`;
  - `random`: `math/rand`, `math/rand/v2`, `crypto/rand`;
  - `state`: `go` statements, channel sends and receives, `select`, `sync`, `sync/atomic`;
  - and a call of a bork function by name, which uses what that function declares.

  A binding (`fn Getenv(key: String) uses io: String unsafe go "os.Getenv"`, see [Go interop](#go-interop)) is checked by the same rules, against the Go function it calls, and its package must be allowed in `bork.mod` like any `unsafe go`.

  The Go imports of all bodies share one generated file, so a package name counts even when another body imports it, unless the body declares the name itself (parameters included). A name counts against the function's own declared effects, or against the declared type of a function value it gives (`Atom`'s `currentFn: () uses state => T`). It is an error, not a warning, and applies to user packages; the prelude and the standard library are written by hand to the same rules, and reviewed instead (their runtime plumbing, such as `bork/log` choosing `os.Stdout`, would trip it). It is an honesty check, not a proof: Go code can reach the world in other ways (a `for range` over a channel is not seen), and a local `sync.Mutex` counts as `state`. It catches an `unsafe go` helper that quietly does I/O while its signature says it is pure.

### Tasks and scopes

- **`fork`, `onClose`, and `http.Listen` take open functions,** so a call is charged with the effects of the work it starts, even though the work runs on another goroutine or later. "This function can cause X" is the question a reviewer asks, and structured concurrency means the work belongs to a scope the caller holds.
- **Scope operations that observe or change cancellation are `state`:** `cancel`, `cancelled`, `checkpoint`, and with `clock`, `delay` and `cancelAfter`. Opening a scope, its policies, and `attach` are pure.
- **A request handler's type shows what it does:** `http.Listen` takes `handler: (Request, Scope) => Response`, which is open, so `Listen(addr, s, handler(store))` uses `net + clock + state` plus whatever the handler uses.

### `main` and tests

- **`main` and test bodies may use every runtime effect**, with nothing declared (`main` is the one function where no `uses` does not mean pure). They are the program's roots: everything a program does starts there, so a declaration on them would say little, and tests print, read fixtures, and start servers. The functions they call are still checked against their own declarations, which is where the guarantee matters: below `main`, a function that declares nothing does nothing. The `build` effect is permitted only inside comptime blocks, whose enclosing function does not acquire it. Build helpers declare `uses build`; runtime entrypoints and predicates cannot use it. `main` may still declare `uses`, which is then checked like any other function's (useful for a program that should be provably free of, say, network access), and `fn main() uses nothing` declares a pure program.
- **`main` cannot be called or used as a value**, not even from a test or a lambda, so no other code can reach "every effect" through it.

### Predicates and compile-time evaluation

- **Predicates must be pure.** A `pred` cannot declare `uses`, and calling an effectful function in one is an error, as is calling, or passing on, an open parameter (declare it `uses nothing`). A fact from a predicate that reads the clock goes stale. `assertSnapshot` in a predicate gets its own message: it can only be used in tests. Rules are implications between predicates, so they are pure too.
- **So compile-time evaluation only runs pure code** (apart from what `unsafe go` declarations claim).
- **Default parameter values** are closed values, so they are pure.

### No inference for declared functions

As with requirements ("no inferred preconditions"), a function's effects are written in its signature and never climb silently to its callers. Lambdas are the exception, because they have no signature to write them in. Signatures stay complete, packages check on their own, and recursion needs nothing special. (One limit: for the rule that an unexported function declares exactly what it uses, a call of itself, or of a function that calls it, counts as a use.)

### Diagnostics

Each error names the effect and the call that needs it. Where the fix is a signature change, the message ends with the `uses` to declare, and `bork check --json` carries it as a text edit that replaces, inserts, or removes the `uses` clause (see [the diagnostic format](diagnostics.md)), so an agent can apply it as written:

```
main.bork:14:3: describe uses io (it calls println), but its signature allows no effects; declare it: uses io
main.bork:30:5: serve uses clock + state (it calls http.Listen, with a lambda that calls update), but its signature allows only net; declare it: uses net + clock + state
main.bork:41:7: process uses io (it calls map, with a lambda that calls println), but its signature allows no effects; declare it: uses io
main.bork:5:50: save declares net, but never uses it
main.bork:8:3: isOpenNow uses clock (it calls now), but predicates must be pure, or their facts could go stale
main.bork:47:5: show uses io (it calls println), but class Show allows no effects
main.bork:4:10: now's unsafe go body uses clock (it uses time.Now, which reads the clock or waits), but its signature allows no effects; declare it: uses clock
main.bork:2:1: package example.com/shop has unsafe go (shout), but bork.mod does not allow it; if it is meant to, add the line: unsafe "example.com/shop"
```

Function values that do not fit are type errors, which say what the value uses and what was expected:

```
main.bork:5:3: function handler returns () => Int, but its body produces () uses state => Int (it uses state, but an open result can only use what the open parameters use; to allow more, write the parameters' and the result's effects)
main.bork:9:15: field run of Pure must be () => Ok, found () uses io => Ok (it uses io, where a function that uses nothing is expected)
main.bork:13:13: argument 2 to update must be (Int) => Int, found (Int) uses io => Int (it uses io, where a function that uses nothing is expected)
main.bork:17:26: field run of Job must be () uses io => Ok, found () => Ok (it uses what an open parameter uses, which its caller chooses: it can only be passed to an open parameter, or returned as an open result)
```

- **The reason chain is one step deep.** It names the direct call (and, for an open parameter, the lambda's call). The callee's own signature says why that callee needs the effect.
- **The effect check runs after type checking**, before the lifetime check, and only when type checking found no errors.

### Open questions

- **Should cancellation have its own effect?** It is `state` here, so a function that only waits with `delay(s, ms)` shows `state`, though it shares no data. Moving cancellation to `clock` (renamed `time`), or to its own effect, would keep `uses state` meaning "shares data with other tasks".
- **Should `io` be split** into `console`, `files`, and `process`? The set starts coarse. Splitting later only adds effects, so it is cheap to do once real code shows the need.
- **Effect aliases** (`effects Backend = io + net + state`) for long lists on handlers? Not needed while there are five effects.
- **Test doubles:** with effects in place of capabilities, a test cannot hand a function a fake clock. [Mocking in tests](#mocking-in-tests-design-bork-53lit4) replaces the function that reads the clock instead. Effect handlers, which let a test say how an effect is answered, may still be worth it later.

## Loops (design: bork-8jiark)

`for` has Go's forms. `for (x in xs)` goes through a `List` or `Seq`. `for { }` loops until `break`, `return`, `?` or a panic. `for (cond) { }` checks a `Bool` before each round. `for (init; cond; post) { }` counts. See [the design](design/loops.md).

- **Header names** are bound by the init bindings, in order, in a scope of the loop's own. They cannot shadow enclosing names, and sibling loops may reuse them. Each round binds fresh values, so closures capture the round's values.
- **The post clause** runs after each round and on `continue`. Its values are computed together from the values the round ended with (`a = b, b = a` swaps). It can rebind only the names the loop carries (header names, and names from outside it, below), must keep each name's type (the first binding's, or its annotation), and cannot use `return`, `?`, `break` or `continue`. An empty condition means `true`.
- **Facts:** a header name's declared facts are its invariant. Its first value and every next value must prove them, and they are known in the body. The condition is known in the body and the post clause.
- **Lifetimes:** a header name lives as long as its first value; a next value belonging to another scope is an error.
- **Types:** a loop is an `Ok` expression whose body must be `Ok`. A loop without a condition and without a `break` that leaves it has type `Never`, so code after it is unreachable.
- **Formatting:** the formatter keeps the clauses as written (it never changes tokens), so `for (;;)` stays, written without spaces between empty clauses; `for` and `for (cond)` are the idiomatic spellings.
- **Code generation:** header names are carried names (below), bound before the loop for the header names after them. A condition is checked at the top of the body, on the round's values. Loop exits reuse the for-in machinery for returns, scopes and owners.
- **Carried rebinding:** a loop body may rebind a name that the block containing the loop may rebind (bound in that block, or carried by an enclosing loop), at its top level and in the blocks of `if`, `match` and block statements in it. The loop carries it: each round starts with the value the previous one ended with (at the end of the body, a `continue`, or after the post clause, which may rebind carried names too), and after the loop the name has the value the loop ended with (between rounds, or at a `break`). Where a statement's branches meet, the name has the value of the branch that ran, or keeps its value. A carried name keeps its first binding's type; rebinding it to another type is an error that says to widen the first binding. Rebinding it in a lambda, `scope` or `with` block, initializer, or a branch whose value is used is an error, and so is carrying a lazy, async or owned-scope binding. Outside loops, nested blocks still cannot rebind outer names.
- **Unused carried values:** a carried value is used if, on some path from it, it is observed: read other than to compute the next value of a carried name that is itself never observed (a fixpoint, so `count = count + 1` alone, or two names that only feed each other, are errors). A header name only the post clause reads is an error that suggests leaving it out.
- **Facts of carried names:** the facts the first binding declares hold at the start of every round and after the loop. The value before the loop, each round's next value and each `break`'s value must prove them; values in between need not. Where branches meet, the name has a declared fact when every branch's value proves it there.
- **Code generation of carried names:** each carried name has a state variable (its latch), assigned where a round ends or a `break` leaves; each round binds its own copy, which closures capture, and the Go loop's post statement computes next values in a function literal over copies of the latches. A statement whose branches rebind carried names declares a join variable before it, which the branches that rebind assign. Every other bork binding stays a Go variable assigned once.

## Tail calls (design: bork-8jiark)

Go does not eliminate tail calls, and its goroutine stacks grow until a 1 GB limit kills the process. bork compiles a function's calls of itself in tail position as jumps, so recursion used as a loop runs in constant stack. See [the design](design/loops.md).

- **Tail positions:** the function body's value and the operand of `return` (including a `return` inside a `for` loop), and within those, a block's last expression, both branches of an `if`/`else` (in a function without a result, the branch of an `if` without `else` too), and every `match` arm.
- **Not tail positions**, with the reason the compiler gives: inside a `scope` block (it closes after the call returns), a `with` block (its ambient values end after the call), a block with a mock in force, the operand of `?`, any other expression that uses the result, a lambda, generator, lazy or async initializer, or comptime block (separate functions). A lazy field's initializer is a function of its own too. A self call with other type arguments, and calls of functions that take an `OwnedScope` (each call drops its owner) or their caller's location (internal helpers), are ordinary calls, and so are calls in instance methods, which dispatch through their class.
- **Code generation:** such a function's body runs in a labelled Go loop. Each round binds fresh copies of the parameters, which closures capture, so a closure made in one call keeps that call's values. A jump evaluates the arguments in order and assigns them all at once. Scopes and owners bound in the body close through one cleanup root declared outside the loop, rather than through per-call Go defers, so the defer list does not grow either. A jump from inside a `for` loop leaves the loop through its exit, as a `return` does.
- **Mocks:** in a test build, a jump in a mocked function first asks whether a mock of it is in force on the goroutine; if so, the call goes through the mock as an ordinary call. A spy mock that delegates to the real function therefore uses stack per call.
- **Stack traces and debugging:** a jump reuses the frame, so a panic shows one frame for the whole recursion. The jump carries the call's position, so stepping stops on the call line. Stepping over it then lands on the function's first line, since no new frame exists.
- **`tailrec`:** a marker in a declared function's `uses` list (`uses io + tailrec`, or `uses tailrec` for a pure function). Compilation fails unless the function calls itself (`tailrec.not-recursive`), each such call is a jump (`tailrec.not-tail`, naming the reason), and no cycle of direct calls through another function reaches it (`tailrec.mutual`, naming the cycle). It is not an effect: callers do not declare it, it is not part of the function's type, the effect checks ignore it, and fixes that rewrite a uses list keep it. It is rejected in function types, `Seq` types, class and instance methods, `unsafe go` functions and bindings, and `main` (`tailrec.position`). The promise covers direct calls of the function itself; recursion through function values, class methods or other functions is not covered.
- **Mutual recursion** is not optimized. A state machine written as functions that call each other is written as a loop over a state value instead.
- **Tooling:** `bork describe` at a call of a function by itself reports `tail_call: {jump, reason}`, and editor hovers say whether the call is compiled as a jump or why not.

## Mocking in tests (design: bork-53lit4)

> **Implemented** (bork-53lit4). The syntax is in [grammar.md](grammar.md); [examples/mocking](../examples/mocking/main.bork) mocks `http.Get` and `time.Now` across tasks.

A test can replace a declared function for part of its run, with no interfaces, no
dependency injection, and no change to the code under test. This is
[rewire](https://gigurra.github.io/rewire/) built into the compiler: bork owns
code generation, so only test builds route calls through a dispatch point, and
production builds are unchanged.

```
import "bork/http"
import "bork/time"

test "a slow upstream is reported" {
  noon = time.Instant { unixNanos: 1_760_000_000_000_000_000 }
  mock time.Now() { noon }
  calls = mock http.Get(url, s, timeoutMs) { http.Text(503, "") }

  assertEqual(checkUpstream("https://status.example.com"), Health.Down { since: noon })
  assertEqual(calls.count(), 1)
}
```

### Syntax

```ebnf
Stmt     = Binding | Trust | MockStmt | Expr .
MockStmt = [ Ident "=" ] "mock" MockTarget "(" [ Ident { "," Ident } ] ")" Block .
MockTarget = Ident | Ident "." Ident | Ident "." Ident "." Ident .
             (* charge, payments.Charge, User.greet, model.Point.Value *)
```

- **The target is named as in a call or a method reference:** `charge` (a
  function of the test's package), `payments.Charge` (an imported package's
  exported function), `User.greet` or `model.Point.Value` for a method, whose
  receiver is then the first parameter, as in a [method reference](grammar.md).
  A test can mock what it can name: anything of its own package, and what
  other packages export. (rewire can reach unexported functions of other
  packages; bork keeps a package's private functions its own business, and a
  test that needs to replace one should mock the exported function that
  calls it.)
- **The parameters are names only.** Their types, facts and the result come from
  the target's declaration, so a mock never restates a signature, and a change
  to the target's types shows up as a type error in the mock's body instead of a
  stale copy. There must be one name per parameter (`_` ignores one). Default
  parameters are included: callers have already filled them in.
- **The body is a block**, checked like a function body against the target's
  result (see *Contracts* below).
- **`mock` is a keyword only where a statement starts, or right after the
  `=` of a binding, and only when a name follows it** (`mock fetch(...)`,
  `calls = mock fetch(...)`), so `mock` stays usable as a name elsewhere.
- `bork fmt` formats it like a function declaration: `mock payments.Charge(card, amount) {`.

### Lifetime: a mock lasts until the end of its block

A `mock` statement works like a binding: it is in force from that statement to
the end of the block that contains it, and is undone when the block ends,
whether it ends normally, returns, or panics. Nothing needs to be restored by
hand. To change a mock mid-test, or to restore the real function, use a nested
block:

```
test "retries after a failure" {
  mock fetch(url) { FetchError { message: "down" } }
  assertEqual(load(), Status.Unavailable)
  {
    mock fetch(url) { "ok" }        // replaces the outer mock in this block
    assertEqual(load(), Status.Up)
  }
  assertEqual(load(), Status.Unavailable)   // the outer mock again
}
```

- **A mock written directly in a `scope` block's body lasts until the scope's
  tasks are done.** The scope ends it as one of its finalizers, after waiting
  for its tasks (and before the finalizers of what was opened before the
  mock), so a task launched there sees the mock however late it calls. A
  panic in the block closes the scope first, so a task stuck in such a mock
  is cancelled, not waited for forever.
- **One mock per target per block.** Mocking the same target twice in one block
  is an error (as rebinding a name is); a nested block may mock it again, and
  wins there.
- **Only in tests.** `mock` is allowed in test bodies, including property tests
  and blocks nested in them (`if`, `match`, `scope`), but not inside lambdas,
  which can run later or elsewhere, and not in functions, which tests could
  share only by giving up the block lifetime. So there is no reusable
  `withFixedClock()` fixture that installs mocks: that is deliberate, since a
  reader of the test then sees every mock in force. A shared fake is an
  ordinary function the mock's body calls: `mock time.Now() { fixedNoon() }`.

### Propagation: mocks follow the work, not the code

Each test has its own mocks. They belong to the goroutine that runs the test
and are inherited by every goroutine started from it, at the moment it is
started:

- **Tasks** (`fork`), servers started with `http.Listen` and their
  request handlers, and goroutines started by `unsafe go` code (a raw `go`
  statement, or a Go library's) all see the mocks in force where they were
  started, and keep seeing them while they run.
- **A mock that ends ends for everyone.** When its block ends, a task that is
  still running gets the meaning from outside the block (an outer mock or the
  real function) on its next call. The block's end first waits for calls of
  the mock that are still running (on other tasks) to finish, so a mock's body
  never runs after its block, and never sees a resource of that block closed.
  (A mock body that waits for something the test does only after the block
  would then wait forever: a test bug, like awaiting a task that waits for
  its awaiter.)
- **Data does not carry mocks.** A value sent on a channel, stored in an atom,
  or captured by a function value does not take the sender's mocks along. Work
  runs with the mocks of the goroutine it runs on: a worker task receiving jobs
  from a channel uses the mocks in force where the worker was started.
- **So tests do not see each other's mocks,** also when they run in parallel
  (`bork test --parallel N`). A server one test starts answers with that
  test's mocks, whoever sends the request.
- **Outside any test, nothing is mocked.** A goroutine started before the tests
  ran (by a Go package's `init`, or a pool a library created earlier) has no
  test, so its calls run the real functions. So does any goroutine still
  running after its test ended (an orphaned task, a leaked Go goroutine): the
  test's mocks have all ended by then. Goroutines the Go runtime starts on its
  own carry no labels either: `time.AfterFunc` callbacks, finalizers, and cgo
  callbacks run the real functions. This is the documented fallback: a mock
  can only be seen by work that started under it, while it is in force.
  (When tests run in parallel, a goroutine a Go library starts lazily during
  one test and then reuses for another, such as a worker pool, carries
  the first test's mocks (and label) while that test runs. Pools in bork code are tasks of
  a scope, which cannot outlive their test.)
- **Implementation.** The runtime keeps mocks as a chain of frames, one per
  `mock` statement, each pointing to the frame that was in force when it was
  made. Each frame gets a real profiler label set of its own
  (`pprof.SetGoroutineLabels(pprof.WithLabels(...))`, label `bork.mock`), and
  a side table maps that label set's pointer to the frame. The Go runtime
  copies a goroutine's labels to every goroutine it starts, including those
  bork does not start itself. A dispatcher reads the current goroutine's label
  pointer (one load: no labels means no mocks) and looks it up in the table.
  A frame is marked ended when its block ends, and lookups skip ended frames;
  the block's end puts back the very label set it found. A `with` that
  publishes [logged or propagated](#logged-and-propagated-values) values
  makes a label set of its own, which the table maps to the frame in force.
  The labels stay valid for profilers. `unsafe go` code that sets its own
  profiler labels (`pprof.Do`) hides the test's mocks from what runs under
  them; it then gets the real functions. On the test's own goroutine that
  holds until the block of the next mock ends (which restores the labels it
  found); mocks written later still pass calls on to the ones around them,
  since each frame's parent is the mock the compiler sees around it.

### Contracts: a mock must keep the target's promises

A mock replaces a function that callers were checked against, so its body is
checked as if it were that function's body:

- **Types and facts.** The parameters have the target's types including their
  `where` facts (callers proved them), and the body must produce the target's
  result type including its facts. `mock http.Get(url, s, timeoutMs) { http.Text(42, "") }`
  fails to compile, since `42` is not a `ValidStatus`, and a mock of `fn validate(raw: Int): Int where positive | NotPositive`
  that returns `0` is a compile error, so no mock can produce a value the
  checker assumed impossible. `trust` works as in any test code, and is checked
  at runtime there.
- **Only functions with effects can be mocked.** A pure function gives the
  same answer for the same arguments, which the compiler relies on: predicates
  (pure) are run at compile time to prove facts, a fact can be named after a
  pure function (`xs.filter(keep)` gives `List[T where keep]`), and default
  values and constants are computed once. A test that changed a pure
  function's answers, in one block or one task, would make those facts false.
  Pure code is deterministic anyway, so a test can run it as it is; mocks are
  for the outside world. A function counts as pure when it declares no
  effects, even if it has open parameters (their effects are its callers').
  This also rules out predicates, rules, and every function a predicate can
  call.
- **Effects.** The body may use what the target declares, and `state`. Only
  `state` is added, because a fake usually needs memory (a clock that
  advances, a sequence of answers, an in-memory store in an atom) and nothing
  else: a mock of `http.Get` that read a fixture file would make the code
  under test do I/O it does not declare, so fixtures are read by the test and
  captured. Values captured from the test are immutable and need nothing. An
  open parameter of the target can be called in the mock, as in the target's
  own body.
- **Lifetimes.** The body is checked like a lambda written at the `mock`
  statement: it may capture what lives at least as long as the block.
- **Declared facts only.** A caller in the same package can usually prove
  facts about a call's result from the callee's body (`fn Get(): Int { 1 }`
  gives a positive result). A function a test mocks gives its callers only
  what its signature promises, as another package's function does, in
  programs and tests alike: a mock keeps the signature's promises, not the
  body's. Code that needs more says so in the signature.
- **Names.** In a mock's body, the target's facts are about the mock's names
  for the parameters (`mock Between(low, high)` reads `hi: Int where
  atLeast(lo)` as `high` being at least `low`), and a `where` written in the
  body names the mock's parameters, or the test's.
- **Results live as long as the arguments.** Callers give a mock's result the
  lifetime the target's signature implies, so the result may not hold a
  resource or scope the mock captured from the test.

### What can be mocked

| Target | Mockable | Why |
|--------|----------|-----|
| A function with effects, of the test's package or exported by an imported package, including the standard library | yes | |
| A method with effects (`Store.save`), the receiver being the first parameter | yes | |
| An `unsafe go` function or a binding to a Go function (`time.Now`, `http.Get`) | yes | the dispatch point is the bork declaration, so the Go code is simply not called |
| Pure functions, `pred`, and `rule` | no | their answers are facts; see *Contracts* |
| Another package's unexported functions | no | mock the exported function that calls them |
| The prelude's functions and methods (`fork`, `atom`, list and string methods, ...) and the compiler's built-ins (`println`, `toString`, `assert`, ...) | no | they implement the language; mock the function that calls them |
| Class methods and instances (`Show`, `Decode`, ...) | no | instances are resolved per type; mock the function that uses them |
| Generic functions and methods | yes (bork-7gpl00) | one generic mock answers every instantiation; see below |
| `main` and tests | no | `main` cannot be called; tests are roots |
| A function value or a record field holding one | no | pass a different value instead |

**A generic function's mock is generic** (bork-7gpl00). Bork generics compile
to Go generics, so a call inside other generic code only knows the
instantiation at runtime, in Go's terms, where facts are erased and every union
is `any`. A mock written for `decode[Int]` would then also answer
`decode[Port]` (`Port = Int where positive`) and break its promise. So a mock of
a generic function or method is one body for every instantiation: it is checked
against the target's generic signature, with the target's type parameters in
scope under their names (`mock Load(key, fallback) { fallback }` gives a `T`;
`{ 0 }` is rejected: "the mock of Load must give T, but its body produces
Int"). Whatever it gives keeps the promise of each instantiation, as the
target's own body does. In test builds the body is a generic Go function of
its own (Go has no generic function values), which the dispatcher selects by
the mock's environment: the test's values the body uses, copied when the
`mock` statement runs (bork values never change, so the copy is the value).
Its Go type parameters get names of their own, so they never hide the test's
types or values. Parameters of a type that mentions a type parameter get no
field in the call record (one record serves every instantiation); `calls()`
shows them. One limit comes from Go: the body cannot reach its target again,
directly or through other functions, at a type built from its type
parameters (`Describe([x])` in a mock of `Describe[T]`), since each Go instance
of the body would need another; `bork test` reports that
(`mock.generic-recursion`), naming the function it goes through. Like any
function's body, a mock's body cannot `break` or `continue` a loop of the
test around it.

### Calling the real function

Inside a mock's body, the target's own name means what it meant before the
mock: an outer mock, or the real function. This is how a mock passes calls
through, or wraps the real behaviour (a spy):

```
mock http.Get(url, s, timeoutMs) {
  if (url.startsWith("https://weather.example.com/")) {
    http.Text(200, "{\"celsius\": 21}")
  } else {
    http.Get(url, s, timeoutMs)    // the real http.Get (or an outer mock)
  }
}
```

Everything written in the mock's body that names the target skips the mock:
a call (`http.Get(...)`, or `s.save(x)` for a mocked method `Store.save`),
the target used as a value (`urls.map(fetch)`), and the same in lambdas
written in the body. A call that reaches the target through other functions
gets the mock again, as any call under it does.

### Recording calls

A `mock` statement can bind a handle, of the prelude's type `Mock[A]`, that
records the calls the mock answered, from every goroutine. `A` is the
target's *call record* (bork-pvmyos): a record the compiler makes, with a
field per parameter of the target, named as the target names them (a
method's receiver first): `FetchCall { url: String, retries: Int }`,
`StoreSaveCall { s: Store, key: String }`, `HttpGetCall { ... }` for
another package's function. Its name cannot clash with a declared type or
function (a number is added if it would), and it exists only in test builds;
the test reads its fields, and does not write its name. A parameter whose
value can belong to a scope (a scope, a resource, a Go value, a function, or
anything holding one) gets no field: the handle outlives the test's scopes,
so the record would hand back a closed resource, or a function that uses one.
Reading such a field says so, and suggests checking the value inside the
mock's body, while it is valid; `calls()` still shows it as text. Fields do
not carry the parameters' facts.

- `m.count(): Int` is how many calls it answered so far (always the length
  of `m.calls()`).
- `m.calls(): List[String]` lists them in the order they were recorded (as they
  started, give or take calls racing on other tasks), each as the
  call's text with its arguments rendered as `toString` does (which gives
  every value a text, functions, scopes, and Go values included):
  `["payments.Charge(Card { last4: \"4242\" }, 100)"]`, so `assertEqual` and
  `assertSnapshot` can check them.
- `m.args(): List[A]` lists the same calls as call records, so a test checks
  arguments with their types: `m.args().map(c => c.amount)`.
- `m.expect(times: n)`, `m.expect(atLeast: a, atMost: b)` (either bound
  alone), and `m.expect()` (at least once) declare how many calls the mock
  must answer; `times: 0` means never. The bounds are `Option[Int]` with
  `.None` defaults; a supplied nonnegative integer promotes to Some, and
  explicit `.Some(n)` also works. Every supplied negative integer,
  including -1, fails the test. `m.expectWhere(c => c.url == "a",
  times: 1)` counts only the calls whose record matches; the matcher is pure
  (`(A) uses nothing => Bool`), so it can run when the check does. They are
  declared up front, as rewire's `expect` is, and checked when the mock ends:
  at the normal end of its block, or, for a mock written directly in a
  `scope` block, when that scope's tasks are done. An unmet one fails the
  test, naming the mock statement, what was expected, what was answered, and
  the calls (`main.bork:12:11: the mock of Fetch expected exactly 2 calls,
  but answered 1:` and then `Fetch("a")`); every unmet expectation of every
  mock ending there is reported. A test that already failed reports only its
  own failure: on a panic, or a failed task of the scope a mock is checked
  with, the mock ends without checking. An expectation declared after its
  mock ended fails the test, since it could never be checked, and so does a
  matcher that panics.
- `m.waitFor(n)` waits until the mock has answered n calls, from any task, and
  fails the test if that takes longer than `ms` (default 5000)
  milliseconds; `m.waitForWhere(matcher, n)` counts matching calls. This is
  how a test waits for work it started asynchronously (a server's handler, a
  background task) without sleeping.

All of them use `state` (the answer changes as calls happen), and the waits
`clock`. Calls passed through to the real function by name are recorded by
the outer mock or not at all. Bad bounds (negative, `times` together with a
range, `atLeast` above `atMost`, a negative wait) fail the test at the
`expect` or `waitFor` call. Unmet expectations report the position of each
`expect` or `expectWhere` call, including expectations checked after tasks finish.
Wait failures and matcher panics likewise report the helper call rather than
the mock declaration.

### Caller locations for internal helpers (implemented)

Prelude and standard-library helpers can call `compilerCallerLocation(): String`
to opt into compiler-supplied `file:line:column` locations. This internal intrinsic
is rejected in ordinary packages, predicates and class methods. It adds no
public argument or function-type change: the compiler supplies a hidden String
at direct calls. An opted-in helper forwards its caller's location when it calls
another opted-in helper, and its `assert`, `assertEqual`, `assertSnapshot` and
`dbg` use that location too. Ordinary wrappers keep their own call positions.
A saved helper function captures the position where it was named; calling that
value later cannot recover a different call site from its ordinary function type.
Lambdas inside a helper capture the same location. Native mocks and passthrough
preserve the hidden argument. The four Mock expectation and wait helpers use
this mechanism; delayed checks store a location for each expectation. It neither
walks Go stacks nor depends on a goroutine's current diagnostic context.

### Hermetic tests

`bork test --hermetic` (bork-x0g9au) fails, without running it, every test
that can reach the network with no mock in force, so a suite that passes with
it runs offline. The network is a function that declares `net` and does it
in Go code (an `unsafe go` body or a Go binding: `http.Get`, `http.Listen`,
`net.Dial`); a bork function that uses `net` only reaches one. The check is
static, on the checker's call graph:

- The checker records each call in a test (its body, lambdas, and mock
  bodies) together with the mocks that cover it. Mocks apply by goroutine,
  where code runs, so a call written directly in the test (or a mock's body)
  is covered by the mocks of the enclosing blocks, from their `mock`
  statement on: a call before a `mock`, or after its block, is not. A call
  in a lambda, or a function used as a value, may run later or on another
  task (one started before the mock, receiving work on a channel), so only
  the mocks in force for all of the test cover it: those in the test's own
  block, before its first call with effects.
- From each call it follows what the called functions call (function values
  included, and for a class method, its instances' methods), stopping at a
  function a mock covering the call replaces: the mock answers instead. A
  mock of any function on the way covers what is below it (`mock page(url)`
  covers the `http.Get` that `page` calls).
- Inside a mock's body its own target means the function before the mock, so
  a mock that calls the real function is not hermetic, unless an outer mock
  of it is in force.
- The failure lists each unmocked network function once, with the call in the
  test that reaches it, the functions on the way, and the last of them the
  test can mock: `main.bork:41:11: page -> Get; mock Get`.
- With `--auto-properties`, a trusted function whose property test would reach
  the network (it is called on generated arguments, with no mocks) fails the
  same way. A test that does not run keeps its snapshot names, so the others'
  do not shift.

It errs on the side of reporting: a call on a branch the test never takes
counts, and so does a function value passed around but never called. Calls
through Go code that bork does not see (a callback a Go library runs) are not
followed. Only `net` is checked; `io` (files, processes) is left out on
purpose, since tests often use temporary files. By that definition an
in-memory database opened through `bork/sql` (which declares `net`) or a
server on the loopback interface is not hermetic; mock its functions, or
leave such tests out of an offline run.

### Production builds pay nothing

`bork build` and `bork run` generate exactly what they did before. In the
program `bork test` builds, each function that some test of the package mocks
is generated as a small dispatcher that looks up the current goroutine's
mocks (one pointer read when there are none) and otherwise calls the real
body, renamed. Functions no test mocks are generated as before, so a test
build pays only for what its tests mock. Because the dispatcher replaces the
declaration, every way of reaching the function is covered: direct calls,
calls from other packages, the function passed as a value, and calls by name
from `unsafe go` code.

### Diagnostics

```
main_test.bork:4:3: mock can only be used in a test body
main_test.bork:4:3: mock can only be used in a test body, not in a lambda
main_test.bork:4:8: payments.Charge takes 2 parameters (card, amount), but the mock names 1; write: mock payments.Charge(card, amount)
main_test.bork:6:5: mock of validate must return Int where positive | NotPositive, found 0 (positive(0) is false)
main_test.bork:7:5: mock of Charge uses io (it calls println), but a mock of Charge may use only net + state
main_test.bork:4:8: total is pure, and only functions with effects can be mocked: the compiler relies on pure functions giving the same answers
main_test.bork:4:8: positive is a predicate, and only functions with effects can be mocked: the compiler relies on predicates' answers to prove facts
main_test.bork:4:8: payments.charge is not exported, so the test cannot mock it
main_test.bork:4:8: println is built into the compiler and cannot be mocked; mock the function that calls it
main_test.bork:4:8: map is a prelude method and cannot be mocked; mock the function that calls it
main_test.bork:9:3: fetch is already mocked in this block (at 5:3); mock it again in a nested block
```

- The wrong-arity error carries a JSON text edit that replaces the parameter
  list with the target's parameter names.
- Type and fact errors in a mock's body are ordinary errors, worded with "mock
  of f" where they would name the function.

### Tooling

- **`bork describe`** on a mock's target describes the target function
  (signature and definition); on a parameter name, its type and facts from the
  target; inside the body, values as in any function.
- **`bork fmt`** keeps `mock` statements in the canonical layout.

### Decisions recorded

- **A statement that lasts until its block ends,** rather than `mock ... { body }`
  wrapping the code it applies to, or rewire's install-then-restore calls:
  it reads like a binding, needs no restore, and a nested block gives the
  mid-test restore. It is also what makes the frame chain simple.
- **The signature is taken from the target,** not restated, so the contract
  check is exact and refactoring the target cannot leave a mock that quietly no
  longer matches.
- **The target's own name inside its mock means the real function,** rather than a
  `real(...)` keyword or rewire's `Real(t, f)`: there is nothing new to learn,
  and it matches how a binding's own name is read on its right-hand side
  elsewhere in languages with shadowing.
- **Profiler labels as the carrier,** rather than a hidden context parameter
  (every function would take one in test builds, including function values, and
  `unsafe go` callbacks would lose it) or a goroutine-ID table (Go hides
  goroutine IDs, and a goroutine bork did not start would have no entry). Labels
  are inherited by every goroutine the Go runtime starts. Reading them uses
  `runtime/pprof`'s internal `runtime_getProfLabel` (through `go:linkname`), in
  test builds only; it is the hook profilers and tracing libraries use, and a
  test of the runtime guards it on each Go upgrade.
- **Only effectful functions can be mocked, and mocks keep the target's
  effects plus `state`.** The test body may use everything, but the code under
  test was checked against the target's declaration, and pure code, whose
  answers facts rely on, should not change in tests.
- **Frames end by waiting for calls in flight,** so the lifetime check of a
  mock's body is the one of a lambda written at the `mock` statement.

### Follow-ups

- Mocking class instances, if real code shows the need.

## Ambient values (design: bork-j68yln)

> **Implemented** (bork-j68yln; the `logged` and `propagated` markers:
> bork-avr3ns). Typed request-scoped values: bork's answer to the
> value half of Go's `context.Context`. The other half, cancellation and
> deadlines, is already [scopes](#resources-and-scopes).

A trace id, the signed-in principal, a tenant or a locale belongs to a request,
not to any one function's job, and threading it through every signature
between the handler and the log line is the noise Go's `ctx.Value(key)` avoids.
But `ctx.Value` is untyped, can be missing at runtime, and does not show in a
signature. In bork an ambient value is declared with a type, a function says
which ones it reads (as it says which effects it uses), and the compiler checks
that every caller provides them. The dependency is visible, not magic.

```
ambient traceId: String
ambient principal: auth.Principal
ambient locale: String

fn audit(event: Event) uses io needs traceId + principal: Ok | IoError {
  fs.Append(auditLog, s"${traceId} ${principal.name} ${event.kind}\n")
}

fn greeting(name: String) needs locale?: String {
  match (locale.getOr("en")) {
    "sv" => s"Hej ${name}"
    _ => s"Hello ${name}"
  }
}

fn handle(req: http.Request, s: Scope) uses io + state: http.Response {
  with (traceId: req.header("X-Trace-Id").getOr(newTraceId()), principal: authenticate(req)) {
    _ = audit(Event.Viewed { path: req.path })
    http.Text(200, greeting(req.query("name").getOr("you")))
  }
}
```

### Declaring

```ebnf
AmbientDecl = { "logged" | "propagated" "(" String ")" } "ambient" Ident ":" Type .
```

- **An ambient value is a package-level name with a type.** It has no value of
  its own, and no default: it is unbound until a `with` binds it. It lives in
  the value namespace, so the usual rules apply: an upper-case name is exported
  and read as `trace.Id` from other packages, and no local can reuse the name.
- **Its type must be data:** no `Scope`, `OwnedScope`, resource, task, atom,
  channel, or function type, nor a type that contains one. An ambient value
  describes the request; capabilities (a database, a clock, a client) stay
  parameters, or come from [assembly](#compile-time-dependency-assembly).
  This also keeps ambient values out of the lifetime check: a binding can be
  captured by tasks that outlive its `with` block (below), which is only safe
  for values that hold no lifetime.
- **Its type may have facts** (`ambient attempt: Int where positive`, or a
  constrained alias): a `with` must prove them for the value it binds, as an
  annotated binding does, and a function that needs the value knows them. An
  optional need (`attempt?`) is an `Option`, and is not known to have them.
- **Markers** `logged` and `propagated("header")` publish the value beyond
  `needs`; see [below](#logged-and-propagated-values).
- **No defaults in v1.** "Unbound" has one meaning everywhere; a function that
  can do without a value reads it as optional and picks its own fallback
  (`locale.getOr("en")`), so the fallback is visible where it is used.

### Reading: `needs`

```ebnf
Needs = "needs" Need { "+" Need } .
Need  = ( Ident | Ident "." Ident ) [ "?" ] .
```

- **A function names what it reads after `uses`, before the result**, in
  function and method declarations (not class methods, below): `fn f(x: Int) uses io needs traceId: Int`.
  Names are joined with `+`, as effects are. Inside the body the name is an
  ordinary immutable value of the declared type.
- **Reading is pure.** A needed value behaves exactly like a hidden parameter:
  the same arguments and the same bindings give the same result. So `needs`
  adds no effect, and a pure function may need values.
- **Checked like `uses`.** A function's body may read only what it declares or
  binds itself, and calling a function that needs `traceId` counts as reading
  it. A missing `needs` is an error that names the call and the fix, carried as
  a text edit in `bork check --json`. An unexported function may not declare a
  need it never reads; exported functions may, to keep their API stable.
  Needs never climb silently to callers, as with effects. An exported function
  cannot need an unexported ambient value: other packages could not bind it, so
  they could not call the function.
- **Optional reads: `needs locale?`.** The body sees `locale: Option[String]`.
  A call never fails for an optional need: the caller passes `Some` when it has
  the value (bound by a `with` around the call, or needed itself), passes its
  own `Option` when it needs it optionally too, and `None` otherwise. So adding
  an optional need to a function breaks no caller, and only the functions that
  care say so.
- **A caller provides a need if it declares it or binds it.** A `needs locale?`
  does not provide `locale` to a callee that requires it: the value may be
  missing.

### Binding: `with`

```ebnf
WithExpr = "with" "(" WithBind { Sep WithBind } [ Sep ] ")" Block .
WithBind = ( Ident | Ident "." Ident ) ":" Expr .
```

- **`with (traceId: id, principal: p) { ... }` binds values for the block**,
  and is an expression whose value is the block's, like a `scope` block. `?`
  and `return` leave it as they leave any block. The names are ambient values
  (`trace.Id` for another package's); the values are checked against their
  declared types, facts included.
- **Bindings are immutable and nest.** An inner `with` may bind a name again for
  its own block (the one place a name is bound again, as a nested `mock` may);
  nothing can change a binding in place. Binding one name twice in one `with`
  is an error. The values are evaluated left to right before any of them is
  bound, so `with (traceId: s"${traceId}/retry") { ... }` reads the outer one.
- **`with` is a keyword where an expression starts and `(` follows.** A
  function named `with` cannot be called as `with(...)` at the start of an
  expression; elsewhere `with` stays usable as a name (`scope s with ...` is
  unchanged).

### Where values come from

- **`main` and tests cannot declare `needs`.** They are the roots, so they bind
  what the code below them reads. A test binds the values it wants
  (`with (principal: admin) { ... }`) instead of mocking a `currentUser()`
  function; that is the reason to prefer an ambient value over a function that
  looks it up.
- **Predicates and rules cannot read ambient values**, neither by `needs` nor
  through a call that needs one. A fact is a property of a value that
  travels with it: `doc: Doc where visible` proved while one principal is bound
  must not still hold after a `with` binds another. A predicate may still bind
  values with `with` and call functions that need them, since the result then
  depends on its arguments alone.
- **Class methods cannot declare `needs` in v1**, and neither can instances of
  them: the class fixes the signature, and `Eq` or `Encode` reading the request
  is not something a reader of `a == b` expects. A free function or a method
  declared with a receiver can.
- **Default parameter values** are closed values and cannot read ambient
  values. Compile-time evaluation only runs code whose needs are bound in the
  code being evaluated.
- **`unsafe go` functions may declare `needs`.** The values are visible in the
  Go body by their names (an optional one as the prelude's `Option` in Go); a
  need of another package's value is named with that package's Go prefix, as
  its functions are. A binding to a Go function (`unsafe go "os.Getenv"`)
  cannot declare needs.

### Function values, lambdas, and tasks

- **A lambda captures the bindings in force where it is created**, as it
  captures locals: a lambda that calls `audit` needs `traceId` and `principal`
  from the enclosing function, which must need or bind them. So function types
  do not carry needs, and a callback passed down through code that knows
  nothing about trace ids keeps the request's values.
- **A named function used as a value is bound where it is named.**
  `events.forEach(audit)` reads `traceId` and `principal` at that point, like
  `events.forEach(e => audit(e))`. The same holds for method references.
- **So tasks inherit the bindings.** `fork(s, () => audit(e))` captures them
  when the lambda is made, whatever goroutine runs it, and `http.Listen`'s
  handler captures what is bound where the server starts. Per-request values
  are bound by the handler (`handle` above), so each request has its own.
- **Lexical, not dynamic.** A value reaches a function along the calls and
  closures that the source shows, never through what happened to be bound on
  a goroutine. This is where ambient values differ from mocks, which follow the
  goroutine: a mock replaces code for a test, while an ambient value is an
  input the code computes with, and must be the same on every path the checker
  sees.

### Logged and propagated values

```
logged ambient requestId: String
propagated("traceparent") logged ambient trace: TraceParent   // String where validTraceParent
```

- **Markers on the declaration opt a value in.** `logged` adds it to every log
  line written while a `with` binds it, in every function, not only those that
  need it. `propagated("traceparent")` lets boundary code send it across
  process boundaries under that header (or message metadata) name. Values of
  unmarked declarations never leave through logs or the network implicitly.
- **A marked value is a `String`, `Int`, `Float` or `Bool`**, facts allowed:
  logs and headers carry it as text, and an incoming value is read back from
  its text and checked against its facts. A `String` goes as it is; numbers
  and `Bool` go as bork shows them (`3.0`, `NaN`, `true`), and only that
  text is read back (no `+`, `_` or hex; `Bool` only `true` or `false`). Records would need a codec per declaration; none is needed yet.
- **A header name is an HTTP token, used once per program** (ignoring case):
  two declarations sent under one header would be confused on the receiving
  side.
- **One dynamic mechanism.** `with` also publishes its marked bindings in the
  goroutine's profiler labels (as
  [mocks](#propagation-mocks-follow-the-work-not-the-code) are carried), until
  its block ends on any path (`return`, `?`, `break`, a panic). Goroutines
  started meanwhile inherit them: tasks, and goroutines of Go code. Unlike
  needs, this follows the goroutine, not the source: a lambda made inside a
  `with` and run outside it logs without the value, a task started inside
  logs with it after the block ended, and the loop body consuming a
  `generate` whose producer is inside a `with` runs with the producer's
  values (the producer calls it). That is right for labels of logs and
  outgoing calls, and is why nothing a function computes may depend on them.
- **Only effectful standard-library boundary code reads them**, through the
  helpers in [the Go helpers for standard packages](std-go.md#ambient-values):
  `bork/log` adds logged values (under the declaration's name, or
  `audit.requestId` where two packages log one name, or the whole package
  path where their paths end alike, before a record's own attributes); `net` clients send `_borkPropagated()`, and
  servers bind an incoming request's values with `_borkBindPropagated`. Bork
  code reads ambient values only through `needs`.
- **An incoming request is a boundary.** Binding its values first clears every
  propagated value the server's goroutine had bound, before reading any, so a
  request never forwards the server's own trace, and nothing logged while its
  values are read carries it. A missing value stays unbound; one that is
  not of its type or lacks its facts (or whose predicate panics) stays
  unbound and is logged at warning level, without its text. Logged values that are not propagated are not
  cleared.
- **Labels never reach `needs`.** A handler's code computes with the bindings
  captured where it was made (lexically, above). For application code to
  *need* an incoming value, the handler decodes it from the request with the
  checked `Decode` boundary and binds it: `with (trace: decoded) { ... }`
  (bork-gqxe4s).
- **Cost.** Only a `with` that binds a marked value touches labels, and a Go
  function holding one defers a single restore for panics, however many
  times a loop runs the `with`; a program without marked declarations has
  none of this runtime.

### Implementation: hidden parameters

The compiler threads each need through the functions that declare it as an
extra Go parameter, after the ordinary ones, sorted by package and name so the
Go signature does not depend on the order the `needs` clause is written in. A
`with` block binds Go locals of its own (`_with1_traceId := ...`), a call passes the locals
or the caller's own hidden parameters, and a lambda or function value closes
over them as it closes over any local. An optional need is an `Option` parameter.

The alternative, a map of values carried by the scope (or the goroutine) and
looked up at each read, was rejected:

- **Hidden parameters are typed and static.** There is no lookup, no type
  assertion, and no way to read a missing value. A map needs both a lookup and
  a check that it was bound, at runtime, for what the compiler already knows.
- **They cost nothing where they are not used.** Only functions that need a
  value take it; a pure helper three calls down is unchanged. A carried map is
  passed (or looked up from goroutine state) everywhere.
- **Pure functions take no scope**, so a scope-carried map would not reach
  them, and a goroutine-carried one would make reading depend on where code
  runs, which breaks the purity that facts need.
- **Capture comes for free:** a Go closure over a parameter is exactly the
  lexical rule above.

The cost: a function's Go signature changes when its needs do, which matters
only for Go code that calls bork functions directly; and needs, like effects,
are written by hand along a call chain (the compiler names each one, with the
fix).

### Interplay

- **Effects.** `needs` is independent of `uses`: it adds no effect, and
  `uses nothing` parameters and predicates' purity are unaffected (predicates
  are excluded for facts, above, not for effects).
- **Logging and propagation: one dynamic mechanism.** Two kinds of code need a
  value without being able to declare `needs` for it: a log line in a function
  that does not need the trace id, and `bork/http`'s client, which cannot name
  a user's ambient declarations. Both are served by the opt-in markers
  [above](#logged-and-propagated-values), which share goroutine labels that
  only effectful standard-library boundary code reads, so `needs` remains the
  only way a value reaches what a function computes.
- **Needs versus markers: the trade-off.** A value read through `needs` must be
  declared on every function between the `with` and the read, even those that
  only pass it on. That visibility is the point for values that change what
  code does, such as `principal` or `tenant`: a reviewer sees which functions
  act on whose behalf. A trace id usually only labels logs and outgoing calls,
  so it should be `logged` and `propagated` rather than needed, and then no
  signature carries it.
- **Mocking** ([bork-53lit4](#mocking-in-tests-design-bork-53lit4)). A mock's
  body is checked as the target's body, so it may read the target's needs;
  a test binds values with `with` rather than mocking where they come from.
- **Assembly** ([bork-25nywe](#compile-time-dependency-assembly)).
  A provider may need ambient values; an `assemble` call then needs the union
  of its providers' needs, as it uses the union of their effects. Ambient
  values are never products: they are not resolved by type.
- **Lifetimes and owned scopes.** None: ambient types hold no lifetime.
- **Propagation across process boundaries** (bork-gqxe4s) builds on the
  `propagated` marker: `bork/http` sends and binds the values with the
  helpers above. The approved [HTTP boundary design](design/http-propagation.md)
  specifies remaining deadline budgets (carried with scopes, not ambient
  values), server policy caps, W3C trace forwarding, and the distinction
  between incoming labels and explicit checked bindings for typed `needs`.
  Deadline budgets are implemented before admission and body reads; client
  redirects refresh the effective budget and server requestTimeoutMs caps it.
  Marked values and checked W3C TraceParent/TraceState are forwarded with
  request label isolation; typed needs still require explicit checked binding.
  The [two-service example](../examples/service_context/main.bork) demonstrates
  both trace continuity and shrinking budgets.
- **describe** shows a function's needs beside its effects, in text and JSON
  (`needs: [traceId, locale?]`), and `ambient` declarations with their types.

### Diagnostics

```
main.bork:14:5: record needs traceId (it calls audit), but its signature does not provide it; declare it: needs traceId, or bind it with with (traceId: ...) { ... }
main.bork:20:3: main cannot declare needs; bind the values instead: with (traceId: ...) { ... }
main.bork:8:3: visible reads principal (it calls canSee), but predicates cannot read ambient values, or their facts would depend on what is bound
main.bork:2:1: ambient session cannot hold Scope: ambient values are data, so pass scopes, resources and functions as parameters
main.bork:5:40: save declares needs traceId, but never reads it
main.bork:31:9: with binds ambient values, and total is not one
main.bork:33:9: traceId is bound twice in one with
main.bork:21:24: cannot bind trace: the expression never produces a value
main.bork:42:22: attempt must be positive, but positive(0) is false
main.bork:6:22: logged ambient user must hold a String, Int, Float or Bool (facts allowed), found User: logs and headers carry it as text
main.bork:11:12: propagated("x trace"): a header name is letters, digits and !#$%&'*+-.^_`|~, and not empty
other/other.bork:2:12: header TraceParent already carries markfail.trace (declared at main.bork:12:1)
```

### Decisions recorded

- Declaration `ambient name: Type`, data types only (facts allowed), no
  defaults.
- `needs` after `uses`, joined with `+`; `name?` reads an `Option` and never
  fails a caller.
- `with (name: value, ...) { ... }` as an expression; immutable, nesting
  shadows.
- `main` and tests bind and cannot need; predicates, rules, class methods and
  defaults cannot read.
- Lexical capture: lambdas, function values and tasks keep the bindings in
  force where they are made.
- Hidden Go parameters, not a carried map.
- `logged` and `propagated("header")` are opt-in markers on the declaration,
  for `String`, `Int`, `Float` and `Bool` values, sharing one goroutine-label
  mechanism that only effectful standard-library boundary code reads; trace
  ids should usually use them rather than `needs`. An incoming request is a
  boundary for propagated values; labels never reach `needs`.

### Open questions

- **Defaults on declarations** (`ambient locale: String = "en"`)? Left out so
  "unbound" has one meaning; optional reads cover it for now.
- **Bundles of needs** (`needs Request` for `traceId + principal + tenant`)?
  Like effect aliases, not needed until real handlers show long lists.
- **Unbinding** (`with (principal: none)`) for code that must run as nobody?
  Rare; a fresh function that does not need the value does the same.

## No-value success spelling (bork-btbg6y)

The no-value type and its explicit success expression are both `Ok`:
`fn save(): Ok | SomeError { Ok }`. Empty blocks and calls returning nothing
also produce `Ok`. This keeps success and failure unions concise without
changing the no-value rules: standalone bindings, fields, parameters, and
generic value arguments cannot have type `Ok`; callbacks may return it.
`Ok` has no payload and is separate from user-defined variants such as
`Result.Ok { value }`.

For one release, `Unit` remains a deprecated type alias. Type descriptions,
diagnostics, and printed success union members use `Ok`. `bork check` reports
nonfatal `migration.unit` warnings; its JSON form includes precise replacement
edits. Strings, comments, and unrelated names are not migrated. The old Go
representation `_Unit` also remains an alias, but new unsafe Go bodies use
`_borkOk()` to construct a union's success member. Entire `Ok` results still
map to Go functions without results; error-only Go bindings produce
`Ok | GoError`.

## Go interop

Checked Go interop is implemented (bork-e6abw5). Bork declares the Go functions
and types it uses, and the compiler checks their signatures with `go/types`.
Bindings and raw Go bodies share the package-level `unsafe` opt-in and declared
effects. Opaque values hold shared Go state; ordinary bork values remain
immutable, and conversions copy collections and records at the boundary.

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

### Boundary rules

- **Three declarations, no new keywords.** `type X = go "<Go type>"` names an opaque Go type. `type X = go "<Go type>" { fields }` is a record that mirrors a Go struct. `fn ... unsafe go "<Go function>"` binds a Go function or method; it is an `unsafe go` function whose body is a name instead of code.
- **Bindings are `unsafe go`.** A binding runs Go code that bork does not check, exactly as an `unsafe go` body does, so it is the same boundary with the same word: greppable, allowed only in packages that `bork.mod` lets use `unsafe go`, and declaring its effects with `uses`, which bork trusts.
- **Binding effects use the same heuristics as unsafe Go bodies.** Package and function names charge known effects: `os.Getenv` is `io`, `net.Dial` is `net`, `time.Now` is `clock`, while `net/url.Parse` is pure. A missing required effect is an error. Third-party code and mutation through opaque values still rely on the declared signature; a finer per-function effects table is deferred.
- **Go types are named by declarations, not inline.** There is no `go.Type["net/http", "Request"]` in signatures. A declaration gives the type a bork name and a place for a doc comment, and signatures stay bork.
- **Everything is checked against the real Go package**, at `bork check` time, not first by the Go compiler on generated code. Errors are reported at the bork declaration, and show the Go signature.
- **Bindings are checked more than bodies are.** A binding's wrapper is generated, so everything it converts from Go is checked, facts included: a binding can only break its promise through its effects and through what the Go code does to opaque values.
- **bork targets 64-bit platforms.** Go's `int` and `uint` are 64 bits wide there, so they are `Int` and `Uint64`. Bindings reject Go targets with narrower `int` or `uint`.
- **Go version selection.** The generated module starts at Go 1.23 and raises that requirement to the highest `go` version in imported dependency manifests. Signature checks use the installed Go toolchain's standard library, rather than a snapshot of an older release. The compiler itself requires the Go version in its own `go.mod`.

### Syntax

```ebnf
TypeDecl   = "type" Ident [ TypeParams ] "=" ( ( Fields | Sealed | GoName Fields ) { TagGroup } | "resource" [ GoName ] | GoName | Type ) [ Derive ] .
GoName     = "go" StringLit .               (* go "*net/http.Request" *)
Field      = [ Doc ] Ident ":" Type [ "=" Expr ] { TagGroup } .
TagGroup   = Ident "{" [ TagEntry { Sep TagEntry } [ Sep ] ] "}" .
TagEntry   = Ident ":" Expr . (* go values remain StringLit only *)
GoBody     = "unsafe" "go" ( "{" { GoImport } GoStatements "}" | StringLit ) .
```

- `go` stays an identifier everywhere else. After `=` in a type declaration, `go` followed by a string literal is a `GoName` (a type named `go` cannot take a string, so there is no ambiguity). The lexer's raw-Go mode after `unsafe go` starts only at a `{`; `unsafe go "os.Getenv"` is ordinary tokens.
- A field's default is an expression, which ends at the end of the field (newline or `,`) or at a same-line package tag group such as `go {`, since an expression never continues with an identifier.
- Package tag groups are retained in order on fields, variants (before `where`), and type bodies (before `where` and `derive`). A group starts on the same line as the token it follows. The built-in `go` group keeps its string-only field semantics. Other package groups currently produce an unsupported-tag error; their typing and shape access are a separate implementation step.
- `Doc` is the `//` comment lines directly above a field. The parser keeps them, like the other doc comments it will keep for tooling.

### Naming Go things

The string after `go` is a Go type or function written with its full import path, the way Go's own tools print them:

- types: `"net/http.Request"`, `"*net/http.Request"`, `"io.Reader"`, `"example.com/x/v2.User"` (the package is everything up to the last `.` after the last `/`);
- functions: `"os.Getenv"`, `"crypto/sha256.Sum256"`;
- methods, as Go method expressions: `"(*net/http.Request).UserAgent"`, `"(time.Time).Unix"`. The receiver is the method's first parameter in the bork signature (a bork method can bind it too: `fn (r: Request) userAgent(): String unsafe go "(*net/http.Request).UserAgent"`). A method with a pointer receiver cannot be bound on an opaque type that is not a pointer: it would change a copy.

Only exported, non-generic package-level functions, methods, and named types can be bound. A field path such as `(*net/http.Request).Header.Get` is not a function and is rejected: write an `unsafe go` body. Bindings take no type parameters, since the Go function has none. Generic Go functions and types are an open question.

A third-party Go package must be declared with a canonical pinned `require`
line in `bork.mod`, with checksums in `bork.sum`, or in an imported standard
package's embedded manifests. `bork deps` also writes a generated `go.mod` with
the same module and requirements for Go module consumers; check/build diagnoses
drift and suggests `bork deps download`. Legacy `go-deps.mod`/`go-deps.sum`
projects remain supported and can be converted with `bork deps migrate`. The compiler merges requirements using Go's minimum version
selection; user requirements can raise a standard package's pinned version.
Unsupported module directives and conflicting checksums are errors.
`bork deps init`, `bork deps get <package@version>`, and `bork deps download`
create and maintain the pinned user manifests using Go module tools. See
[the dependency manifest format](std-go.md#user-go-dependencies).

Bork libraries are source-containing Go modules with matching root `bork.mod`
and generated `go.mod` declarations. The Go command selects and downloads their
complete graph; bork expands source-only requirements hidden by Go pruning.
Loading, bindings, evaluations and generated builds share that selection.
Package visibility and import cycles are checked across module boundaries;
ambiguous package providers are errors. Extracted library sources, markers and
assets must match pinned hashes on cold and warm compiler caches. Dependency
unsafe grants authorize only their own packages, are trusted by consumers, and
are reported for newly selected releases by `bork deps get/download` without
an approval step. See [library dependencies](language/packages.md#library-dependencies).

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
- **Context-bound Go resources can attach.** A binding that receives contexts and returns resources passes stable generated contexts to Go. The opening ownership scope and every Scope converted to a context join a lifetime union; attaching any returned resource adds its destination scope to that union. Cancellation occurs only when all those scopes have ended or cancelled, or the last returned resource releases ownership. The final member cancels the group before Close runs, so that final Close may wait for cancellation. A binding author must ensure an earlier member's Close does not await the shared cancellation while another sibling remains owned; otherwise scope cleanup can block. `cleanupTimeout(ms)` bounds a misbehaving finalizer and lets the scope run the remaining finalizers. Resources from one call share cancellation, while each distinct resource retains its own ownership and Close. Explicit opaque contexts are fixed external limits: any of them cancelling cancels the group, and attach cannot extend a passed context's deadline. Context values are preserved, and Deadline reports the earliest deadline of still-live scopes and explicit contexts. Go libraries that snapshot a deadline may retain that earlier limit. In these context-bound bindings, returned raw resources are closed once on Go errors or conversion failures, including values conversion has not reached; unused raw results such as a false `(value, ok)` also close. Duplicate comparable Go handles from one call share one close owner. A context hidden inside an opaque struct cannot be rewritten: expose its fields in a mirror record or use an unsafe wrapper.
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
- **Resource fields are not supported in mirrors yet.** The standalone `_borkFromGo` helper has no ownership scope to register their cleanup. Keep the outer Go object opaque or use an `unsafe go` resource wrapper. This applies to resource fields inside containers and nested records too.

### The basic mapping

Bindings and mirror records convert values by their types, at the boundary. A pair of types not in this table does not convert, and the declaration is an error.

| bork | Go | to Go | from Go |
|------|----|-------|---------|
| `Int8` ... `Int`, `Uint8` ... `Uint64` | `int8` ... `int64`, `int`, `uint8` ... `uint64`, `uint` | copy, into any Go integer type that holds every value of the bork type (`Int8` to `int32`, `Uint32` to `int64`) | copy, from any Go integer type; checked at run time when the bork type does not hold every value of the Go type (`int64` to `Int32`, `int` to `Uint64`, `uint64` to `Int`) |
| `Float32`, `Float` | `float32`, `float64` | copy; `Float32` to `float64` too | copy; exact types only (no float narrowing, no integer to float) |
| `Bool` | `bool` | copy | copy |
| `String` | `string` | shared (Go strings are immutable) | shared |
| `Bytes` | `[]byte` | copied | copied; `nil` is empty; byte arrays also convert from Go |
| `List[T]` | `[]T'`, `...T'` (variadic) | a new slice, never `nil` (`[]` stays an empty slice) | copied, each element converted; `nil` is `[]` |
| `List[T]` | `[N]T'` (arrays) | not allowed | copied |
| `Map[K, V]` | `map[K']V'` | a new map, never `nil` | copied into an unordered map (`m.unordered()`), since a Go map has no order; `nil` is `{:}` |
| `Option[T]` | `*T'`, or for an opaque pointer or interface type, that type | `None` is `nil`; `Some` is a pointer to a fresh copy | `nil` is `None`, otherwise a copy of what it points to |
| `T` (not an `Option`) | `*T'` | a pointer to a fresh copy | a copy; `nil` is a `GoValueError` |
| a mirror record | its Go struct, or a pointer to it | field by field, as above | field by field; the record's facts are checked |
| an opaque type | a Go type it is assignable to (parameters), or the same type (results) | shared | shared; `nil` is a `GoValueError` |
| `Ok` | no result | | |

- **Named Go types convert by their underlying type**, in every row: `time.Duration` as `int64`, `os.FileMode` as `uint32`, `url.Values` (a `map[string][]string`) as a `Map[String, List[String]]`, `net.IP` (a `[]byte`) as a `List[Byte]`. Unless the bork type is an opaque declaration of that named type, which passes it through.
- **`uintptr`, `complex64`/`complex128`, channels, `unsafe.Pointer`, `any`, and Go functions do not convert.**
- **Map keys** must be integers, `String`, or `Bool` (or named Go types of them), so that the key conversion is one to one: two different Go keys never become the same bork key, and the reverse. Records, options, and opaque types cannot be keys at the boundary.
- **`Scope` converts to `context.Context`** (parameters only): the Go function gets the scope's context (`_borkScopeContext`), so it stops when the scope is cancelled. Most I/O in Go's libraries takes a `ctx`, so this is the usual way a binding gets one.

- **Nothing bork gives Go can be changed under bork.** Lists, maps, structs, and pointers are copied into fresh Go values, so a Go function that writes to its argument writes to its own copy. Strings are shared because neither side can change them. Only opaque values are shared, and those are Go's, never bork's.
- **Nothing Go gives bork can be changed under bork either.** Lists, maps, and records are copied out of Go values, so a Go library that keeps and later changes a slice it returned does not change a bork `List`. The cost is a copy per boundary crossing, linear in the size of the bork value it makes. Bork values are trees, so a Go value that reaches one part from several places is copied once per place; for a Go graph with much sharing, the tree can be far larger than the Go value (exponentially, in the worst case), so such data should stay opaque. `[]byte` maps to a `List[Byte]`, copied element by element, or to immutable `Bytes` through a direct copy.
- **Converting to Go never fails.** A parameter's bork type must convert losslessly: an `Int8` can be passed as a Go `int32`, an `Int` cannot be passed as one (declare the parameter `Int32`, and the caller converts with `toInt32`).
- **Converting from Go can fail**, when a number does not fit (`int64` to an `Int32` result), a pointer or interface is `nil` where bork has no `Option`, a fact does not hold (a mirror record's `where` clauses, and the binding's own result facts, `: Int where positive`), or a Go value is cyclic (a mirror record that can contain itself, through `Option` or `List`, can meet a cyclic Go value, which a tree cannot hold; the conversion tracks the pointers on its current path to find it). Then the bound function returns the prelude record `GoValueError { path: String, message: String }` (`path` is where in the value it failed: `result.items[2].count`). Whether a conversion can fail is known from the types, so a binding whose result can fail must have `GoValueError` in its result type, and the error says so, with the corrected signature as the hint. A binding whose result cannot fail does not mention it. This is the same rule as `toInt8(x)` returning `Int8 | OutOfRange`: a check bork cannot prove is a value the caller handles.

### Bindings: parameters and results

A binding is checked parameter by parameter, and its result against the Go function's results:

| Go results | bork result |
|------------|-------------|
| none | `Ok` (no result type) |
| `T` | `T'` |
| `error` | `Ok | GoError` |
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
- **Function-typed parameters and results** (Go callbacks) cannot be bound yet. They need a bork function wrapped as a Go `func` with conversions on every call, and an answer to which effects such a callback may have. Callbacks remain deferred.
- **Default parameter values and `where` clauses on parameters** work on bindings as on any function: they are bork's, checked before the call. Facts promised on the result are checked by the wrapper, as above.
- **Compile-time evaluation** treats a binding as it treats an `unsafe go` function.

### Struct tags, field docs, and defaults

Go libraries that work by reflection (`encoding/json`, sql scanners, boa for `bork/cli`) need Go structs with exported fields, tags, and sometimes descriptions and defaults. The ordinary bork representation has unexported fields, whose names are used by `unsafe go` code (the prelude's table). A separate Go struct supplies the reflection boundary:

- **`derive (GoStruct)` gives a record a Go struct**, with an exported field per bork field (`httpPort` becomes `HttpPort`), mapped as in the table above (`Option[T]` becomes `*T`), and the conversions both ways, the one from Go checking the record's facts and collecting every error. For a mirror record, the struct is the mirrored Go type; for any other record, the compiler generates it. Fields whose types do not convert (unions, sealed types other than `Option`, and functions) make the derive an error, and so does a mirror record with a Go array field, which converts only from Go. Opaque fields are shared, as in the table. Resources remain unsupported because conversions have no ownership scope. Fields containing unresolved type parameters are rejected; phantom type parameters with concrete fields are allowed. Note that `encoding/json` on the generated struct uses `HttpPort` as the key unless a tag says otherwise, unlike the derived `Encode`.
- **`GoStruct` is a prelude class with no bork methods**: it exists for `[T: GoStruct]` bounds, whose `unsafe go` code gets its dictionary. Its prelude derive template publishes the Go layout as `shape.ForeignRecord` metadata, and any class whose template does the same gets the same dictionary. Like every derived instance, it is written with `derive` and brought into other packages with `use`. The dictionary, in the [helper API](std-go.md): `New()` (a pointer to a fresh Go struct, with the record's defaults filled in), `FromGo(p)` (`(T, []GoValueError)`), `ToGo(v)`, and `Fields()`, which lists each field's name, type, facts, doc, default, Go name and tags from the declaration and layout. `GoStruct` does not require `Decode`; field decoders and kinds come from the typed `codec.Schema[T]()` of a derived `Decode` instance.
- **Fields of a generated struct can carry Go struct tags**, written as a `go { ... }` clause after the field's type and default, with bork string values and no Go quoting: `port: Int go { json: "port,omitempty", short: "p" }` becomes `` Port int64 `json:"port,omitempty" short:"p"` ``, in the written order. On a record without `derive (GoStruct)`, or on a mirror record, they are an error.
- **Fields can have doc comments** (`//` lines directly above the field), given in the schema.
- **Record fields can have defaults**, with the same rules as parameter defaults (closed values): `type Options = { port: Int = 8080, verbose: Bool = false }`. A record literal may leave defaulted fields out, derived `Decode` uses the default for a missing field, and `New()` fills them in. Defaults are checked against their field types and facts, and cannot call functions or depend on other fields. Empty collections can default generic fields. Facts independent of generic parameters are checked once per declaration for every user package in a build or check graph, including imported packages and unused defaulted fields; importers rely on that proof without rerunning its default predicates at each use. Embedded standard-package defaults are proved by compiler tests with their package as root, so importing them alone does not require Go. Sibling-field constraints remain checked on the constructed record. Facts that depend on those parameters are checked at each concrete specialization. A failed specialization points to its use and names the field declaration. Explicit values (including JSON null) do not use the default. Derived field schemas expose the doc, whether a default exists, and a function producing it; environment and CSV loading use defaults for absent variables or columns.
- **CLI configuration preserves proven values.** `bork/cli` loads JSON config files through boa, then merges CLI flags/positionals over explicitly mapped environment values over selected/later/earlier files over declared defaults. `Flag.configFile` exposes a user-selected path such as `--config`; `Parse` and `Run` also accept an explicit `configFiles` list. Final field decoding and fact validation happen before handlers run. See [CLI configuration](std/cli.md#configuration-files).
- **CLI metadata policies are explicit.** Per-command `cli.Settings` controls automatic long/short/env mappings and prefixes. `Mapping.Auto`, `Disabled` and `Named` distinguish inheritance, source disabling and exact names; defaults keep env/short bindings explicit. Pure enrichers transform `FieldSpec` values without changing decoding guarantees. Field comments provide help text with optional overrides; hidden/deprecated flags remain validated, and `ParseDetailed` preserves warnings. See [CLI policies](std/cli.md#names-environment-and-source-policies).
- **CLI examples exercise complete applications.** The [application cookbook](std/cli-cookbook.md) combines nested aliases, reusable metadata policy, JSON settings, proven fields, ordered positionals, scoped handlers, and catalog-backed dynamic completion. Focused examples include embedded tests, while driver integration tests cover source precedence and protocol/error behavior.
- **CLI dynamic completion uses proven field decoding.** The With APIs store effectful completers with a closed callback bound. An opaque immutable Partial.Get uses original field decoders and the requested Decode dictionary; omitted defaults remain Missing, sibling relations remain the complete decoder's responsibility, and each callback owns a fresh cleanup scope. Source precedence matches normal parsing; config/callback failures return an error directive and separate diagnostics. See [dynamic completion](std/cli.md#dynamic-completion-from-partial-inputs).
- **CLI completion shares the command tree.** Nested groups, aliases, help and dispatch use the same boa/Cobra leaves. Shell generators support Bash, zsh, fish and PowerShell; static choices supply described flag/positional values, optional strict String membership, and filename/directory/order directives. Help, scripts and static completion skip config reads and handlers. Per-entrypoint settings can disable completion. Native Cobra completion error behavior is preserved. See [completion](std/cli.md#shell-completion-and-static-choices).
- **CLI positional layouts preserve input words.** Indexed scalar positions are contiguous and independent of record order, with a final optional List collector. Ambiguous optional/default-before-required scalar layouts are rejected. Completion advances by position and can inspect prior fields; literal String list words and JSON compound elements keep their boundaries. Deprecated commands remain callable with warnings separated from stdout. See [positionals](std/cli.md#ordered-positional-arguments).
- **CLI subcommands keep handlers typed.** `bork/cli.Subcommand[T: Decode]` captures each command's derived option decoder and `(T, Scope)` handler in a heterogeneous `List[Command]`. Dispatch selects one command, validates its options, and runs its handler in a fresh scope; errors/help run no handler. Stored callbacks have a closed `io + net + clock + random + state` bound, conservatively charged by dispatch. See [subcommands](std/cli.md#subcommands).
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
- **Shipped code currently uses unsafe Go bodies, without literal bindings or Go type declarations.** A guard test enforces this so checking a program without its own bindings does not invoke Go for signature resolution. Checked signature snapshots are deferred until shipped packages use bindings. Building and compile-time predicate evaluation still need Go.
- **Checking is part of type checking**, after declarations are collected and before bodies, since a binding's signature is its declaration. Errors come out with the rest, at the bork declaration, with the Go signature and conversion mismatch described. Generated Go compiler errors retain their bork source positions.
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

### Codec privacy

Derived codecs cannot structurally inspect another package's private variants.
This applies transitively through record fields, containers, and concrete generic
specializations. At a field boundary, a derive can use the field type's existing
codec from its owning package. A direct alias derive still cannot inspect the
private variants. This preserves public codecs such as `math.Decimal` while
keeping private representations such as HTTP certificate keys inaccessible.

### Open questions

- **Error mapping on bindings:** `unsafe go "os.Open" errors { fs.ErrNotExist => NotFound }`, through `errors.Is`/`errors.As`, instead of an `unsafe go` body?
- **More result shapes:** `T, X, error` (API clients that return a response beside the value), dropping `X` or giving it as an opaque value?
- **Callbacks:** bork functions passed to Go as `func` values (with conversions per call, and effects declared on the parameter's function type). Needed for `sort.Slice`-style and handler APIs.
- **Go errors from bork:** a `GoError` passed back to a Go `error` parameter (`errors.Is`, wrapping). It would need the record to carry the original Go error, which is not a bork value. Out of scope for now.
- **Generic Go functions and types** (`slices.Index`, `atomic.Pointer[T]`): instantiate them from bork type arguments?
- **Go constants and variables** (`math.MaxInt32`, `os.Args`): bind them as zero-parameter functions?
- **Full mirrors:** require a mirror converted to Go to list every exported field?
- **Opaque values and effects:** should every binding that takes an opaque *pointer* type need `uses state`, rather than trusting the declaration? And should opaque values that are not safe for concurrent use be kept out of `fork`?
- **Dependency tooling:** a `bork deps` helper to add or update pinned user manifests (bork-gnyc4e).

## 4. Errors and results (in progress, to be tried out)

> The direction below is agreed, to be validated by trying it in real code. Syntax is a sketch.

### Direction

- **No separate error concept.** Failures are ordinary types. There is no `error` kind, no `Error` base type, and no `Result` wrapper.
- **Functions that can fail return union types.** For example: `fn loadUser(id: UserId): User | NotFound | DbError`. Matching on a union is exhaustive, and checking a member narrows the type, just like any other fact.
- **Union match arms follow source order.** Overlapping type patterns are allowed: the first matching arm wins. A later arm completely covered by earlier unguarded arms is a compile error.
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
- **Same-block rebinding; no nested shadowing.** `x = f(x)` creates a new immutable binding, with any type; its initializer sees the preceding binding and closures retain captured values. Parameters share the function body’s top-level block and may be rebound there. Inner blocks and lambdas cannot shadow enclosing names; sibling blocks may reuse names. Package functions, imports and package values cannot be rebound locally. The existing exception for prelude free functions remains: a local may shadow them, and a call to a non-function local names its declaration in the error. Every local binding must be read, including a replaced binding and destructured names; unused locals are compiler errors (`binding.unused`). Parameters are exempt. Discard explicitly with `_ = expr`, `_: T = expr`, `_` in patterns, or `for (_ in xs)`. Safe compiler fixes replace a name with `_` or remove a binding without dropping effects. Rebinding declarations have a semantic-token modifier and hover links to the preceding declaration.
- **Scala-style string interpolation, not printf:** `s"Hello, $name! Next year: ${age + 1}"`. Any value can be interpolated, rendered as `toString` renders it. Plain `"..."` strings never interpolate, so `$` needs no escaping there.
- **Typed library interpolators (implemented):** `Prefix"... $value ..."` and imported `sql.SQL"... $value ..."` call a prefix factory with a compiler-created private `StaticParts`, then typed Interpolate methods and Finish. Runtime Strings cannot become literal parts; holes retain ordinary types, effects, generic bounds, and resource lifetimes. Evaluation is eager, once per hole, in source order. `$$` stays a literal dollar. Raw execution and stream functions require an explicit `sql.Unsafe(text): UnsafeQuery`; ordinary Strings cannot become driver query text through those APIs. SQL returns a private Statement: scalar/Bytes/Null values bind, validated Identifier values quote, and nested Statements compose. SQLite/Postgres rendering numbers structured parameters at execution, rejects literal placeholders and holes inside quotes/comments/partial tokens, and preserves scoped execution and streaming errors. Ordinary Postgres strings with backslashes are rejected to avoid session-dependent escape rules; explicit E strings are supported. See [the design](design/interpolators.md) and [SQL API](std/sql.md).
- **Library interpolation validators (implemented):** a builder owner's optional pure `InterpolationValidator[Builder]` receives compiler-created StaticParts and hole-kind metadata, never runtime values. The compiler resolves owner dictionaries, batches distinct calls per package, and uses bounded native comptime execution; invalid indices, panic and timeout fail checking. Diagnostics point to a zero-based hole or the prefix and name the validator. Identical parts/kinds memoize within a build; persistent result reuse requires comptime certification. SQL checks definite component-local boundaries rejected by both supported dialects, then keeps mandatory render-time checks for dialect differences, runtime fragments and unterminated components. See [the validator design](design/interpolator-validation.md).
- **Comments** are `// ...` and `/* ... */`. **String literals** use double quotes with Go's escape sequences.
- **Identifiers cannot start with `_`.** That prefix is reserved for the compiler.
- **Records have named fields:** `type User = { name: String, age: Int }`, built as `User { name: "Ada", age: 36 }`. There are no positional constructors.
- **Sealed variants may omit their owner in an expected type or match context:** `Shape.Circle { radius: 1 }`, `Shape.Empty`. Patterns may use `.Circle { radius }` or `.Empty` when the scrutinee selects exactly one sealed owner; bare unqualified variant names are not introduced.
- **Pattern matching is `match (x) { pattern => value, ... }`.**
- **Changed copies use `copy`, with nested paths:** `u.copy(age: 37, address.city: "Oslo")`. Paths reach nested records directly.

### Named call arguments (implemented: bork-e48in4)

Named arguments complement parameter defaults: a small API can expose readable
options without a separate config record or functional options. This section is
the implementation contract; examples below use the implemented syntax.

```bork
// internal/std/http/http.bork: skip drainTimeoutMs, retaining its default.
http.Listen(addr, s, handler, maxBodyBytes: 1048576)
http.ListenTLS(addr, s, routes, keyFile: key, certFile: cert)

// examples/cli/main.bork: label the flags without obscuring the callback.
cli.Run[Options]("greet", "A proven command-line configuration", (options, s) => {
  println(s"Hello ${options.name} on port ${options.port}")
}, flags: [cli.Flag { field: "port", short: "p" }])

// internal/std/sql/sql.bork: required parameters can be named too.
sql.Query[Row](connection, query: sql.Unsafe("select name from users where id = ?"), params: [id])
process.Run(s, "tool", directory: "/tmp", arguments: ["--version"])
```

- **Syntax is `name: expression`.** A label is a single parameter identifier,
  scoped to the selected declaration; it is not a binding or a field path.
  `copy` uses that same colon: `config.copy(port: 9000)`, including nested
  paths such as `config.copy(tls.certFile: cert)`. Its labels still select
  fields, rather than function parameters. Record and map literals inside
  arguments retain their ordinary braces and colons. Replace the existing
  `copy(field = value)` form throughout the corpus; the old form becomes an
  error with a `bork check --json` edit replacing its `=` with `:`. This
  pre-1.0 change leaves one syntax for naming records, call arguments and
  copy updates. Ordinary bindings still use `=`.
- **A positional prefix, then named arguments in any order.** Positional
  arguments fill consecutive parameters from the start. Named arguments fill
  the matching remaining parameter. Required parameters may be named; every
  unfilled required parameter is an error. Any unfilled defaulted parameter
  receives its declared default, including gaps between explicitly supplied
  parameters. A parameter may be filled exactly once, whether positionally or
  by name. A positional argument after the first named argument is an error.
- **Only direct calls expose names.** Unqualified and imported declared
  functions, declared receiver methods, and declared type-class methods accept
  labels, including generic and checked Go bindings. Function values, lambdas,
  function-valued fields, and method references passed as values accept positional
  arguments only: parameter names and defaults are not part of function types.
  Binding `f = http.Listen` does not preserve its parameter names. Names are
  taken from the selected declaration, never inferred from a function value's
  implementation. Class calls use the class declaration's parameter names,
  even if an instance implementation chooses different local names.
  Compiler built-ins without a declared signature (such as `println` and
  `assertEqual`) accept positional arguments only. Callable declarations must
  have unique parameter names, including class signatures without bodies.
- **The method receiver cannot be named.** `items.take(n: 3)` labels an
  ordinary method parameter; the receiver remains `items`. A pipeline's input
  fills the first positional parameter before labels are resolved:
  `connection |> sql.Query[Row](query: sql.Unsafe(text), params: [])`. Explicitly naming
  that same parameter is a duplicate. Existing restrictions on pipeline
  targets remain in force.
  A directly invoked method reference, `List.take(items, n: 3)`, still selects
  a declaration and accepts names for ordinary parameters; its first argument
  must supply the receiver positionally. Saving `f = List.take` erases names
  and defaults, as for other function values.
- **Evaluation follows source order.** Evaluate the callee or method receiver,
  then each supplied argument once, from left to right as written (including
  a pipeline's input before its target's arguments). Arrange the resulting
  values in declaration order for the call. Thus `f(b: readB(), a: readA())`
  reads B before A even though `a` is declared first. Defaults remain closed,
  pure values; omitted defaults introduce no effects. The typed call keeps
  both parameter mapping and source evaluation order so generated Go and
  compile-time evaluation preserve this rule.
- **Checking uses parameter order and preserves source locations.** Each
  supplied expression receives the expected type of its resolved parameter,
  including callback types, constrained types and, in bork-vk07ec, context
  literals. Generic inference uses that same mapping, with the existing shared
  inference session and explicit type arguments; labels add no overload
  resolution or inference rules. Effects and ownership are checked for the
  supplied expressions as usual. Facts, parameter-dependent facts and function
  contracts substitute by parameter identity, so changing argument order does
  not change obligations or promised facts. Diagnostics point at the caller's
  expression, rather than a reordered or synthesized argument.
- **Parameter names are public API.** Renaming a callable parameter can break
  named callers, even if its position and type are unchanged. The receiver
  name and names in function types are excluded. `bork describe` shows callable
  parameter names and defaults and identifies direct-call name support;
  structured descriptions expose this distinction alongside the signature.
  Existing positional calls remain valid.
- **Diagnostics explain the mapping.** An unknown label names the declaration
  and offers a replacement edit only when one close parameter name is
  unambiguous. Duplicate arguments identify the parameter and earlier value;
  a safe removal edit includes the redundant argument's delimiter. Missing
  required arguments list the absent names and types; no edit invents values.
  Positional-after-named errors recommend moving that argument before the
  named suffix, with an edit only when doing so preserves its intended mapping
  and evaluation order. Named calls through function values explain that
  function types do not carry names and recommend a direct declaration call;
  removing labels is only safe when their order is already declaration order.
  Errors and available edits are exposed by `bork check --json`.
- **Formatting preserves argument order and labels.** `bork fmt` uses one space
  after the colon and its existing call layout, retaining comments and line
  breaks. It never sorts labels. Grammar, README, describe documentation and
  std examples must document the implemented behavior. Migrate readable HTTP,
  CLI, process and SQL call sites; do not rename exported parameters or invent
  SQL options solely to exercise the syntax.

Acceptance includes direct/imported functions, required and skipped-default
parameters, receiver and class methods, generic inference with callbacks and
facts, pipelines, checked Go bindings, and rejected function-value calls.
Runtime and compile-time cases must catch evaluation reordering and duplicate
evaluation, including blocks requiring setup statements and an earlier supplied
argument that terminates with `?`, `return` or `panic` before a later argument.
Negative cases cover unknown/duplicate labels (including collisions
with positional arguments), missing parameters and positional-after-named;
formatter, describe and structured diagnostics cover the same source forms.
Copy cases cover nested paths, rejected `=` syntax and its structured fix.

### Context-typed record and variant literals (implemented; config showcase: bork-vk07ec)

Named arguments identify an option; context literals let its expected type name
the value. Keep explicit constructors available wherever they clarify a boundary.
The compiler implements the syntax and inference rules below; the package-owned
config showcase follows the construction/invariant work in bork-kum0ep.

```bork
// internal/std/log/log.bork: the function result names Config; fields name enums.
fn Defaults(): Config {
  .{ level: .Info, format: .Text, output: .Stderr, timestamps: true }
}

// examples/http_server/main.bork: a concrete response type supplies the record.
fn notFound(id: Int): http.Response {
  .{ status: 404, headers: {:}, body: s"no todo $id" }
}

// examples/cli/main.bork: named flags have expected type List[cli.Flag].
cli.Run[Options]("greet", "A proven command-line configuration", (options, s) => {
  println(s"Hello ${options.name} on port ${options.port}")
}, flags: [.{ field: "port", short: "p" }])

// Config API in the new examples/config; TLS fields have type TlsMode.
start(config: .{ host: "localhost", tls: .Files { cert: "server.pem", key: "server.key" } })
staging: Config = .{ host: "staging", tls: .Disabled }
production = staging.copy(host: "production", tls: .Files { cert: cert, key: key })
```

The alternatives on the same logging API are
`Config { level: Level.Info, format: Format.Text, output: Output.Stderr, timestamps: true }`,
`.{ level: .Info, format: .Text, output: .Stderr, timestamps: true }`, and
`{ level: Info, format: Text, output: Stderr, timestamps: true }`.
The explicit form is already supported and remains useful for inferred bindings.
Bare braces would conflict with existing map and block syntax, and bare variant
names would collide with ordinary values and functions (`log.Info` is a function
as well as a variant name). A leading dot distinguishes omitted constructor names
without changing name lookup. Choose `.{ ... }`, `.Variant`, and
`.Variant { ... }`.

- **Syntax preserves named record construction.** `.{ field: expression }`
  constructs a record; `.Variant` constructs a fieldless sealed variant, and
  `.Variant { field: expression }` constructs a variant with fields. Fields
  retain defaults, ordering and diagnostics of explicit literals.
  Option uses `.Some(x)` and `.None`; `.Some(x)` is rejected with a
  suggested rewrite to the named field form. It does not introduce positional
  constructors for arbitrary variants. Context variant shorthand also applies to
  match patterns, with the scrutinee
  type supplying context under the same uniqueness and visibility rules. Nested
  field and element patterns use their matched type. Qualified patterns remain
  available to disambiguate unions; context record patterns are not supported.
  `.Empty {}`
  is allowed for a fieldless variant. Bare `.WithDefaults` is rejected when the
  variant has fields, even when every field has a default; write `.WithDefaults {}`.
  These rules match explicit variant construction.
- **Expected types come from actual value positions.** Function and method
  parameters (including named arguments), typed bindings, function returns and
  block tails, record/variant fields, typed list elements, map keys/values,
  callback results, `if`/`match` branches and `copy` updates provide their normal
  expected type. Nested literals use their enclosing field or element type.
  A typed default expression may use the shorthand; its type is the declared
  field or parameter type. Defaults remain closed values checked at declaration
  or generic specialization as usual. Shorthand inherits existing default
  admissibility: a generic parameter default may still only be a literal,
  so `value: Option[T] = .None` remains rejected just like `Option.None`.
  Generic record field defaults retain their existing specialization rules.
  A match scrutinee and a copy receiver
  have no expected type merely because they are matched or copied.
- **Select a unique nominal constructor.** `.{ ... }` requires one expected
  record type; `.Variant` requires one expected sealed type containing that
  variant. Aliases use their resolved type, preserving declared constraints.
  In an expected union, count nominal candidates: record members for `.{ ... }`,
  and sealed members declaring the written variant for `.Variant`. Exactly one
  candidate is usable; zero or several is an error. A single expected sealed
  type still supplies context for an unknown-variant diagnostic and typo fix.
  Delay selection when an unresolved union member could change the candidates:
  `A | T` is not uniquely record-shaped until `T` is solved. Late resolution
  to another record or a sealed type with the same variant must report ambiguity.
  Do not choose a record by matching its fields, choose a sealed type by searching imports, or silently
  prefer one union member. Several specializations of the same generic type
  are several candidates. Check variant visibility after selection, so private
  variants remain private. A record shorthand cannot name a sealed variant;
  write `.Variant { ... }` instead.
- **No expected nominal type means an error.** `config = .{ port: 8080 }`,
  `println(.Disabled)` and an untyped list made entirely of context literals
  cannot infer their constructor.
  Use `config: Config = .{ port: 8080 }` or `Config { port: 8080 }` instead.
  Branches may acquire context from another explicit, typed branch using the
  existing branch inference rules; a set of shorthand-only branches requires
  outside context. Containers may acquire element context from explicit sibling
  elements using the existing collection inference rules; no field-shape search
  or global constructor search is added. Context dependence propagates through
  nested containers: `[[.{ port: 1 }], [Config { port: 2 }]]` and
  `pair([.{ port: 1 }], [Config { port: 2 }])` work in either order. A pipeline
  target's first parameter supplies context to its input (`.{ host: "local" }
  |> start`); selecting a method on an untyped shorthand receiver supplies no
  context because method lookup requires the receiver's type first.
- **Shared generic inference may supply context later.** For
  `fn pair[T](first: T, second: T): List[T]`,
  `pair(first: .{ port: 9000 }, second: Config { port: 8080 })` gets `Config`
  from `second`, independent of the supplied argument order. A known nominal
  head with unresolved type arguments still gives field context:
  `Box[T]` and `Option[T]` let ordinary field values constrain `T`. A bare
  unsolved `T` must be solved by other arguments, callbacks or expected results
  before a shorthand can select its constructor. Integrate this with the shared
  inference session; do not eagerly reject an argument just because its type
  is solved later. Unresolved generic parameters at session close remain an
  error. Explicit type arguments work as usual.
- **Construction guarantees are unchanged.** Reuse explicit record/variant
  checking and the typed tree targets: required fields, defaults, duplicate
  fields, private variants and field facts apply equally. Sibling-field invariants and package-controlled construction use their shared
  checks. Preserve whole-value type invariants when that feature lands;
  its integration is tested before the complete config showcase is reported. `type Config = private { ... }`
  permits literals and copies only in its owning package, including nested copy
  paths that modify its fields; reading fields and replacing a whole field with
  an already valid private value remain allowed. An omitted type name cannot
  bypass its declaring package's construction boundary. Facts and defaults
  retain source positions and declaration provenance. Effects, ownership and
  source evaluation order follow ordinary construction; named calls retain the
  source-order rules from bork-e48in4.
- **Tooling reports the resolved meaning.** `bork fmt` prints `.{ field: value }`,
  `.Variant`, and `.Variant { field: value }`, retaining comments, field order
  and line breaks. `bork describe` at the leading dot or variant name reports
  the resolved nominal type and constructor definition; its fields and facts
  behave as for explicit construction. Constructor omissions remain visible
  in the source, while the typed tree and generated Go contain resolved targets.
  Grammar, README and examples document all three forms.
- **Structured errors offer explicit spelling.** Missing context identifies the
  value position that lacks an expected type and suggests a typed binding or an
  explicit constructor. Ambiguous context lists the candidate types and offers
  one explicit-constructor edit per candidate, rather than selecting one for the
  user. Generic constructors include explicit type arguments when their expected
  specialization is known and visibly nameable; otherwise the edit marks
  `requires_input` and explains the needed name or import. Explicit specialized
  literal heads and complete edits are specified in the next section. Unknown variants
  search only visible variants of expected sealed types.
  Offer `.Variant` when that corrected spelling selects a unique candidate;
  otherwise offer separate explicitly qualified constructors for the close
  candidates. Never suggest a spelling that remains ambiguous.
  Wrong record/variant kind, private variants, required fields and failed facts
  use ordinary construction diagnostics. An edit requiring a type name marks
  `requires_input`; edits with a known candidate use its visible qualified name.
  `bork check --json` exposes these ranges and fixes. Apply-and-recheck tests
  cover useful edits, including nested contexts and imported types.

Add `examples/config` as a complete config story: required host, default port and
timeouts, colon-style `copy`, `TlsMode = sealed { Disabled, Files { cert: String, key: String } }`
for partially overlapping options, validated port/body-limit fields, named
arguments and nested context literals. Include a package-owned config type with
type-level/sibling invariants and a public validated factory when bork-kum0ep is
available; demonstrate that clients use the factory instead of constructing a
private config directly. The example should run deterministically without network
or local certificate dependencies, print the resolved configs, and include a
short commented invalid construction showing the compiler guarantee. Migrate
existing std and example constructors where the expected type is clear, retaining
explicit names at boundaries that supply inference context.

Acceptance covers every expected-type position, recursive nesting, aliases,
unique and ambiguous union candidates, shared generic inference (including
callbacks and result context), field defaults/facts and constructor visibility.
Include rejected no-context/all-shorthand containers, unknown/fieldless variants,
invalid payload forms and inaccessible private variants;
test record/variant effects, lifetime checks, formatter, resolved descriptions,
structured fixes and the config example's output.

### Positional variant payloads (bork-3ic73j)

A sealed variant may declare a nonempty ordered payload list: `Some(T)` or
`Pair(A, B)`. Construction and nested patterns use parentheses, including
context heads `.Some(x)` and specialized owners `Option[Int].Some(x)`.
Type arguments belong to the owner; `Option[Int].None` remains bare.
Named variants retain braces. Payload forms cannot be mixed, and positional
payloads reject names, defaults, spread and incorrect arity. Bare variant
patterns may ignore their payload. `One((A, B))` has one tuple-valued slot;
`Pair(A, B)` has two slots.

Each slot receives its instantiated type and facts. Constructors evaluate
arguments once in source order; nested pattern checking and exhaustiveness
use the ordered slots. Show prints `Owner.Variant(a, b)`. Derived codecs use
`{"type":"Variant","values":[a,b]}` with exact array arity and error paths
`.values[index]`. Named and fieldless wire formats retain their behavior.
Option declares `Some(T)` and keeps its explicit null/value codec and optional
field behavior. Legacy Some brace forms receive migration fixes; missing owner
type arguments receive fixes preserving inferred arguments and placeholders.

### Explicit type arguments on literal heads (design: bork-idtpbx)

A constructor can name the generic specialization it builds, so empty/defaulted
fields and fieldless variants do not need another value position to supply type
arguments. This also makes context-literal ambiguity edits complete.

```bork
ints = Box[Int] { values: [] }
some = Option[String].Some("trace")
none = Option[String].None
imported = settings.Box[String] { value: "value" }
```

- **The brackets specialize the owner.** Support `Record[Args] { fields }`,
  `Sealed[Args].Variant { fields }`, positional `Sealed[Args].Variant(values)`,
  and fieldless `Sealed[Args].Variant`.
  Qualified imported owners work with the same syntax. `Sealed.Variant[Args]`
  is not a literal head. Existing generic call syntax stays unchanged; a
  bracket list followed by a call remains that call's type arguments.
- **Use ordinary type resolution.** Resolve the named owner and its type
  arguments with the same arity, aliases, visibility, argument facts, and
  bounds as a type annotation. A concrete alias already supplies its arguments
  and cannot be given another list.
  Parameterized aliases remain unsupported, as they are today; this ticket
  does not add them. Type arguments can themselves be generic or composite types
  permitted in annotations. No partial argument list or placeholder inference is added.
- **The head supplies field context.** The explicit specialization determines
  each field's expected type, including shorthand literals nested in fields,
  empty lists/maps, lambdas, and defaulted fields. A surrounding expected type
  cannot replace explicitly written arguments. Ordinary assignability checks
  reject a conflicting result annotation or field value. Unspecialized heads
  keep existing field/result inference behavior.
- **Construction rules still apply.** Reuse required/default/duplicate field
  handling, sibling and whole-value facts, private records/variants, source
  order, and lifetime checks. Argument constraints remain obligations rather
  than becoming assumed facts of an unvalidated constructor. Parent and variant
  invariants validate the completed value as for ordinary construction.
- **Keep this expression syntax.** It is a constructor head, not a new value
  representing a type. A head without braces or a sealed variant is rejected.
  Patterns keep their current syntax; typed binding patterns already accept
  generic type annotations. This does not introduce specialized method-owner
  syntax beyond the existing concrete-alias method behavior.
- **Tooling preserves the specialization.** Format brackets and dotted variants
  consistently, retaining comments and argument order. Describe the owner and
  variant as the resolved specialization with their existing definitions; type
  arguments have the same queries as other type uses. Grammar and README show
  record, fielded-variant, fieldless-variant, and imported forms.
- **Ambiguity fixes become complete.** For an expected `Box[Int] | Box[String]`,
  `.{ values: [] }` offers separate
  `Box[Int] { values: [] }` and `Box[String] { values: [] }` edits. The same rule
  applies to generic sealed candidates and imported aliases. Prefer a visible
  concrete alias that is legal in constructor position when it supplies the full
  specialization; constrained aliases cannot serve as constructor names. Otherwise
  use owner and argument names that can actually be resolved in the calling package. Do
  not render unimported packages or unexported underlying names exposed through
  public aliases. If no owner/argument spelling is accessible, mark the edit
  `requires_input` and explain what visible name or import is needed. Keep
  inaccessible/private candidates in the ambiguity explanation, but omit
  constructor edits that would violate private construction or variant
  visibility; advise using the owning package's public construction API instead,
  naming a factory only when one is known. Apply
  each usable alternative independently and recheck it.

Acceptance covers constructor kinds, fieldless/defaulted variants, nested
shorthand, concrete aliases (and rejection of parameterized aliases), imported
types and alias-exposed inaccessible owners, constrained-alias fix fallbacks,
argument arity/kinds,
conflicting context, bounds/argument facts, defaults and invariants, private
construction, source order/lifetimes, formatting/comments, descriptions, and
independent ambiguity-fix rechecks. Existing generic calls and pattern behavior
must remain compatible.

### Expected-type Option promotion

A known optional value position accepts its ordinary value directly. The compiler
builds `Some(...)`, so config overrides can read
`settings.New(host: "localhost", debug: "requests")`. Explicit `.Some(x)`
and `.None` remain valid and useful when the wrapper supplies inference context.

- **Promotion needs a known target.** The expected type must be exactly the
  prelude's `Option[T]`, with no unsolved inference variables in `T`. A concrete
  alias naming it counts. A union containing Option does not trigger promotion:
  an existing compatible union member keeps its ordinary meaning; otherwise
  write the wrapper explicitly. No global change to type assignability,
  conversions, overload lookup, or generic constraint solving is introduced.
- **The value must be an ordinary non-optional value.** After checking with the
  applicable expectation described below, its type must be assignable to `T` and have a
  known outer shape other than Option. A source union is eligible only when
  every member has a known non-optional outer shape; a member that is Option or
  an unresolved/rigid bare parameter prevents promotion. An expression already
  producing an Option keeps its type. `Option[Option[Int]]` therefore requires explicit outer `.Some`
  when given an `Option[Int]`; promotion never silently adds another optional
  layer. A bare rigid type parameter is excluded because it could be an Option
  in a specialization. A known outer shape such as `List[T]` is safe even with a
  rigid parameter inside it. `Never` keeps ordinary control-flow behavior.
- **Normal expected positions participate.** Named and positional function/method
  arguments, typed bindings, results/returns and block tails, record/variant
  fields, copy updates, typed list/map elements, callback results, and if/match
  branches use the rule. Untyped bindings, a match scrutinee, and a copy receiver
  gain no expectation merely from later use. A generic call cannot infer `T`
  just by deciding to wrap its raw argument; once existing inference independently
  establishes a closed target, the normal delayed argument check can promote.
  Delay ordinary raw arguments too, not only contextual literals: both argument
  orders of `choose[T](value: Option[T], witness: T)` can use a raw value after
  the witness or independently known call result establishes T.
- **Preserve constructor and literal context.** A shorthand record in
  `Option[Config]` can use Config as its context, then become Some. Numeric
  literals, empty containers and lambdas use `T`'s normal literal typing rules.
  Direct `.Some`/`.None` and explicit Option constructors retain the outer
  optional context; they are not checked as constructors of `T`. Calls with a
  declared optional result, or a bare generic result parameter that could be
  optional, retain the outer Option expectation for existing result inference.
  Calls with a definitely non-optional declared result head use payload context.
  Transparent expressions such as `dbg(...)`, blocks and branches forward the
  applicable expectation to their value expressions. Thus `none[T](): Option[T]`
  still infers correctly in `x: Option[Int] = none()` and `dbg(none())`.
  Classify context from existing syntax/declaration types and check once; do not
  retry checking after diagnostics or choose constructors from field shape.
- **Facts and construction remain obligations.** `Option[Int where positive]`
  still requires the promoted value to prove positive. Generic field argument
  facts, sibling predicates, type-level invariants, private construction,
  defaults, effects and lifetimes use the same checks as explicit Some. Do not
  assume a payload predicate because the target asks for it. A guarded or already
  validated payload carries its proven facts into the wrapper. Predicates on the
  completed Option value are checked after construction as usual.
- **Evaluate exactly once.** Promotion adds a wrapper around the checked value;
  source order stays unchanged, including named arguments and copy fields.
  Each branch or callback returning a payload uses its declared expected type;
  branches already returning an Option retain their wrapper. Closed defaults
  keep existing admissibility and declaration/specialization validation rules.
- **Tooling shows the resulting type.** Formatting preserves the written value
  and does not insert or remove wrappers. Describe reports the promoted Option
  type at the promoted expression, with facts/lifetimes projected consistently;
  names inside its payload keep their normal definition links. Existing explicit
  Some remains valid code, so this ticket adds no simplification warning or
  automatic wrapper-removal fix. Such edits can be a future refactoring feature.

Acceptance covers every expected position, config's scalar debug override,
explicit wrappers, nested Option rejection, known aliases and union exclusions,
generic inference in both argument orders and from result context, generic
optional-return calls, `identity(1)` versus argument-free generic results,
mixed payload/None branches and transparent nesting, rigid parameters and
source-union exclusions, shorthand payloads, literal/container/lambda context, source-order effects and exactly-once evaluation, payload facts
and private construction, sibling/whole invariants, defaults, lifetimes,
formatting, and source queries. Rejected promotion suggests explicit Some when
it would make the intended optional layer or inference context clear.

### Numbers

- **Fixed-width integers, as in Go.** `Int` is a 64-bit integer with Go's wrapping arithmetic.
- **Go's sized numbers, with Go-like names:** `Int8`, `Int16`, `Int32`, `Int64`, `Uint8`, `Uint16`, `Uint32`, `Uint64`, `Float32`, `Float64`. `Int` is the same type as `Int64`, `Float` the same as `Float64`, `Byte` the same as `Uint8`, and `Rune` is a distinct Unicode scalar type backed by Go int32. Each sized integer wraps on overflow.
- **Numbers never mix implicitly.** `Int + Int8` is a compile error. Literals and arithmetic on literals are exact compile-time constants that take their type from where they are used (`x: Int8 = 100`); a constant that does not fit is a compile error. Unlike Go, a constant is computed as its type computes, so `x: Float = 1 / 3` is `0.333...`, not `0`.
- **Conversions are explicit free functions:** `toInt8(x)`, `toFloat(x)`, and so on. A conversion that always fits (widening, or any integer to a float) returns the plain type. One that may not fit (narrowing, signed to unsigned, float to integer) returns `Target | OutOfRange`, so `?` or `match` must handle it. A constant is converted at compile time. Later, facts can let a value proven to be in range convert directly.
- **Integer radix conversion (implemented: bork-ai2sdx).** `bork/strconv` supplies pure integer `Hex`, `Binary`, `Octal`, and arbitrary-base `Format` methods returning `String`. Base 2..36 and zero-padding width 0..4096 are checked facts. Formatting uses signed magnitude, optional binary/octal/hex prefixes, and optional uppercase letters. String parsing methods cover every integer width and Byte, return `ParseError` on malformed input or overflow, and require base 0 or 2..36. Base zero detects explicit prefixes and otherwise uses decimal; explicit bases strip only matching prefixes. Separators may appear between digits or once after a recognized prefix. See [the API](std/strconv.md).
- **Floats print as floats:** `3.0`, `1000000.0`, `0.25`, with an exponent only for very large or small values (`1e+21`). The digits are the shortest that read back as the same value.
- **Facts respect overflow.** `a > 10` and `b > 10` do not prove `a + b > 10`, because the sum can wrap. Integer addition/subtraction facts use bounds that rule out overflow for each intermediate; strict order can supply those bounds (`lo < hi` proves `lo + 1 <= hi`). See [overflow-safe implications](#overflow-safe-integer-arithmetic-implications-bork-ggj8ew).
- **Native big integers.** An arbitrary-precision integer type is built in, for when wrapping is not acceptable.

### Equality

- **Structural equality is generated** for records and unions. Equality compares values, not identities.
- **Comparing incompatible types is a compile error.**
- **Constrained and unconstrained versions of a type are comparable.** `Int == (Int where positive)` is fine, because both are `Int` values.

### Compiler environment and installation (implemented: bork-xvuvia)

`bork env [-json] [VAR...]` reports effective `BORKCACHE`, `BORKBIN`, and
`BORK_CACHE` values with sources (`environment`, `config`, or `default`).
`bork env -w VAR=value...` persists settings in `os.UserConfigDir()/bork/env.json`;
`-u VAR...` removes saved values. Environment variables win over configuration,
which wins over defaults; empty values fall through. Writes validate all edits
before atomically replacing the file; reads never rewrite or migrate settings.
Unknown saved keys survive edits for forward compatibility, while unknown requested
names and invalid values are errors.

`BORKCACHE` defaults to `os.UserCacheDir()/bork` and selects the shared root for
compiler results, staging and locks, including `bork clean`. Cache namespace and
locking layouts remain unchanged; root relocation is operational rather than a
semantic compiler input. `BORK_CACHE=off` disables compiler result reuse and
persistent staging, including when saved in the configuration. Go's cache is
independent. Linux/macOS CLI commands also retain bounded results of audited
closed predicate batches in their staging entries. Reuse still performs current
Facts checking and preserves diagnostics; explicit comptime values execute afresh.
Stale, corrupt or unavailable entries always fall back to fresh evaluation.

Linux/macOS `build`, `run` and `script` reuse a bounded executable recipe before
compiler or Go startup. Mutable recorded files are hashed; recorded directory
identities detect membership changes without warm directory scans. This covers
foreign Go dependencies, embeds, assembly, cgo, and compile-time code. Source and
manifest/checksum inputs, Go settings and toolchain identity, selected Go/asm/C/
embed files, cgo compiler identity, known project compile-time inputs, and output
bytes/mode are validated. Warm hits preserve inode and mtime; completed
replacements publish atomically, including while older copies run.

System C headers/libraries and external state read by foreign compile-time code
are untracked by default. Warm reuse with those inputs warns once on stderr per
invocation. The C warning names only nonstandard cgo packages; standard-library
cgo alone is covered by the Go toolchain identity and does not warn.
`--fast`, `BORKFAST=1`, or `fast` in bork.mod suppresses that warning;
`--rebuild` or `BORKREBUILD=1` forces full checking, reevaluation and Go rebuild,
and takes precedence. Programs with no untracked inputs do not warn. Cached CLI
executions replace the process on Unix; library execution APIs, Windows and
temporary fallback builds wait for the child. See
[executable reuse](design/executable-reuse.md) for the trust argument.

`bork install [path]` defaults to `.` and builds into `BORKBIN`, whose default is
Go's effective `GOBIN`, else the first effective `GOPATH` entry plus `/bin`
(normally `~/go/bin`). Explicit settings always win and are never moved. Paths
must be absolute. The installed name follows `bork build`'s default name, with
`.exe` for Windows targets; replacement happens only after a successful build.
Output placement and cache root do not change generated program semantics; Go
configuration and captured program inputs remain part of existing receipts.

The documented bork settings are the supported compiler environment API. Test-runner
`BORK_SEED`, `BORK_CASES`, `BORK_PARALLEL`, `BORK_SNAPSHOTS`, and
`BORK_UPDATE_SNAPSHOTS` are protocol values controlled by CLI flags. Internal,
test and benchmark controls are unsupported and cannot be persisted by `bork env`.

### Packages and modules

- **Compiler requirements (implemented).** An optional `bork 0.4` or `bork 0.4.2` line after the module declaration sets a minimum compiler version. The CLI selects a suitable immutable compiler through the Go module proxy and a versioned local cache when needed. `BORKTOOLCHAIN=local` disables switching; an exact version override pins the compiler and must meet the project requirement. `bork env BORKVERSION` and `bork version` explain selection. See [compiler versions](cli.md#compiler-versions).

- **Go style.** A directory is a package, a module file at the root names the module, imports use module paths, and there are no circular package dependencies.
- **Record construction can belong to a package:** `type Config = private { ... }` restricts literals and `copy` to its declaring package. Fields stay readable and destructurable by importers. This includes nested copy paths and aliases. Package-owned Decode and GoStruct dictionaries are checked construction APIs that importers can use; foreign derivation cannot reconstruct a private record or nested private representation without delegating to a codec provided by its owner. Encode can read its public fields. Whole-value invariants are specified separately in bork-kum0ep.
- **Visibility follows Go:** names starting with an upper-case letter are exported. This applies to top-level declarations (functions, predicates, types); the fields of an exported type are visible wherever the type is. Upper-case sealed variants are visible there too; lower-case variants are private to their declaring package and cannot be constructed or matched elsewhere, including generic variants.
- **How it works (implemented).** `bork.mod` at the module's root holds `module example.com/shop`, and then a line `unsafe "example.com/shop/ffi"` for each package allowed to contain `unsafe go` (see [Effects in signatures](#effects-in-signatures)). A file starts with its imports, `import "example.com/shop/money"` or `import cash "example.com/shop/money"`, and refers to the package's names as `money.Cents`, `money.Amount`, `money.Currency.Eur`. Unused imports are errors, import cycles are rejected, and an import name cannot be shadowed. For now the compiler checks the whole program at once (with each package's names kept apart), rather than each package against summaries of its imports.
- **Messages name types as the code would:** another package's types are qualified (`found money.Cents`), relative to the package the error is in.

- **Built-in maps.** The built-in, persistent `Map[K, V]` (`{"a": 1}`) has methods and needs no import; it is insertion-ordered (the default), sorted (`m.sorted()`), or unordered (`m.unordered()`, a hash map whose printing sorts numeric and string keys by value, and other keys by their text, with mixed kinds grouped). List and map keys use structural equality independently of their text.

- **Standard packages can depend on pinned Go modules (implemented):** native Go module/checksum declarations (`go-deps.mod` and `go-deps.sum`) ship with the compiler. Generated builds include only loaded std packages' declarations, use `-mod=readonly`, and use Go's module cache. Warm caches support `GOPROXY=off`; cold offline builds fail clearly. Dependency sources are not vendored. See [the std Go dependency contract](std-go.md).

- **Another package's functions promise only what their signatures say.** Facts derived from a function's body are used within its package, but not by importers, so a package's body can change without breaking them. (This settles the "exported return types" question below.)
- **Inference rules apply everywhere** their predicates are used, and `bork test` property-tests the rules of the package being tested.

### Standard packages

Standard packages are imported as `bork/name` and ship with the compiler (no
`bork.mod` needed). API descriptions, examples and limits live in the
[per-package documentation](std/README.md). The [Go helper API](std-go.md)
documents the shared implementation boundary.

### Open questions

- **A decimal or money type** in the standard library. Backends need exact decimal arithmetic, and `Float` is wrong for money.
- **Big integer literals and conversions** between `Int` and the big integer type.

### Debug probes and unfinished code (implemented)

`dbg(expr)` is a compiler built-in for inspecting a value in place. It evaluates
its argument exactly once, prints `file:line expr = value` to stderr (capturing
the original expression text at compile time), then returns that same value.
Rendering uses the coherent Show machinery, including custom instances and
generic values. Probing preserves the value's type, proven facts and scope
lifetime. Like logging, the probe's output is deliberately outside the effect
system; it may appear in pure functions and predicates. The argument's own
effects still count. Ok is not a printable value.

`todo()` and `todo("message")` are compiler built-ins of type `Never`: they fit
any expected result type and panic with the source file and line when reached.
The optional message is a String expression, evaluated normally. They are useful
for sketching incomplete functions and branches; they are not recoverable errors.

`bork check` emits nonfatal warnings for both markers, including in unused
functions and checked imports. In `--json` output they use `severity: "warning"`
and the codes `debug.dbg` and `debug.todo`. A dbg warning supplies a removal fix
which retains grouping, argument evaluation and nested expressions; pipelines
are supported too. A todo warning has no automatic fix, since the missing
implementation requires a decision. Warnings do not change check's exit status.
Build, run and test continue to accept these markers. We do not introduce a
release build mode or marker rejection in this change: check surfaces unfinished
code, and automated consumers can choose to enforce those warning codes.

### Debugger value presentation (implemented: bork-4a53ht)

`bork debug build` retains a versioned compiler debug map alongside its generated
Go source. `bork debug dap` relays Delve's protocol and uses that map to present
bork type and field names, sealed variants such as `Shape.Circle { radius: 2.0 }`,
and options as `Some(3)` or `None`, including nested records. Expansion remains
lazy; generated temporaries are hidden and helper frames are marked subtle.
Inspection does not execute custom Show methods. Clients without the sidecar map
retain Delve's Go views. Debug Console expressions still use Go syntax.

### Lazy bindings and record fields (implemented: bork-9zpf2t)

`lazy name = expr` defers one initializer until its first read, memoizes its
result and keeps the static type T. Local bindings are implemented, including
concurrent readers, cached panics, scoped captures and initializer-local return/?.
An Option ? needs an annotated lazy result type; union ? and explicit return
can contribute to an inferred result. Lazy record fields provide a passable lazy
value through `type Lazy[T] = { lazy value: T }`. Pure sibling-dependent defaults
are computed fields: construction creates their cells, copies invalidate affected
dependencies, and structural equality, Show, encoding and writable schemas omit
them. Pure package `Name = expr`, `Name: T = expr`, and `lazy Name = expr` bindings use process-lifetime memo cells,
ordinary export visibility and forward references. Initializers cannot retain
scopes or require ambient values. Direct and helper-induced dependency cycles
are rejected. Comptime reads evaluate the needed pure package values in dependency
order, batch evaluator builds, and bake their checked data; other values remain
lazy at runtime. The complete design, including
effects, facts, scopes, copies, derivation and tooling, is in
[lazy bindings and record fields](design/lazy.md). Transparent async local bindings
(bork-mais5u, implemented) share access machinery under the
[transparent async design](design/async.md): `async(s) name = expr` starts a task
now, keeps type T, and awaits on read. Unread tasks follow ordinary scope
cancellation, joining and panic reporting. The cell retains its owning scope
and captured lifetimes, even for scalar results; eagerly resolved data follows
its ordinary lifetime rules. Both lazy and async initializers are local result
boundaries for return/?.

### Generators and lazy sequences (implemented)

`Seq[T]` is a lazy, ordered stream of values, constructed with a producer expression:

```bork
fn greetings(names: List[String]): Seq[String] {
  generate[String] {
    for (name in names) {
      yield s"Hello $name"
    }
  }
}

fn main() {
  lines = greetings(["Ada", "Linus"]).take(1).toList()
  println(lines)
}
```

`generate`, `yield`, `for`, `break` and `continue` are implemented syntax. The sequence runs synchronously on the consuming goroutine.
It does not spawn a task, allocate a channel, or start work when created. Each
traversal starts a fresh invocation of the producer body. Immutable captures
are retained, not copied from changing local variables. Pure sequences can be
traversed repeatedly with the same results; an effectful sequence repeats its
effects, and a captured file cursor may therefore produce different values on
the next traversal. There is no hidden memoization or consumed-state flag.

The surface follows [q's Generator](https://gigurra.github.io/q/api/generator/)
and [Go's iter.Seq](https://pkg.go.dev/iter): a producer calls a yield callback,
and a false result stops production. Bidirectional coroutines, generator-send,
manual Resume/Close handles and goroutine-backed suspended stacks are deferred.
Bork already has scoped tasks and channels for concurrent conversations; this
feature supplies synchronous lazy traversal.

**Producer boundary and immutable iteration.** `generate[T] { ... }` binds
`yield` in its body and infers the body's latent effects. `yield expr` checks
expr against T, emits it once, and terminates the current producer if its
consumer stops. `return` with no value ends that producer. A value return is
an error. The producer is a function boundary: neither return nor `?` escapes
the enclosing function that created it. A break/continue in a producer cannot target a loop
outside its generate boundary. Initially, `?` in this Ok-returning
body is rejected by the ordinary return-type rules; producers of fallible
items yield error alternatives explicitly, then return when terminal.

Add `for (name in values) { ... }` for List and Seq, binding a fresh immutable
name on each iteration. It returns Ok, and its body follows ordinary
statement/unused-value rules. `break` stops the nearest loop, and `continue`
skips to its next item. A return from an ordinary consuming loop returns from
the surrounding function; a return from a loop inside generate ends the
producer. Existing map/fold pipelines remain the default way to transform
finite lists. Mutable accumulators and general while loops are not required
by this change; `Seq.range(start, end)` and `Seq.unfold(seed, step)` cover large
and unbounded streams without allocating a List. `unfold` takes a pure step
returning `Option[SeqStep[T, S]]`, where each SeqStep carries a value and next
immutable state; `None` ends it. Range retains the existing range convention
and checks its increment for overflow rather than wrapping to an infinite loop.

Yield is legal only in the lexical producer body (including its if/match,
loop and scope blocks). It is rejected in nested lambdas, declared functions,
callbacks, spawned tasks, and outside a producer. A nested generate introduces
its own yield boundary. This restriction keeps the stop signal synchronous
and prevents a nested callback or goroutine from continuing after the consumer
has stopped. To forward another sequence, iterate it and yield each value.

**Latent effects are part of the type.** `Seq[T]` means a strictly pure
producer. `Seq[T] uses io`, `Seq[T] uses io + clock`, and `Seq[T] uses nothing`
spell the effects that traversing it may perform; `uses nothing` equals the
unqualified type. The qualifier follows the sequence type application, wherever
that type occurs (parameters, results, fields, collection elements and unions).
It is distinct from a function's immediate effect declaration:

```bork
fn delayedPrint(text: String): Seq[String] uses io {
  generate[String] {
    println(text)
    yield text
  }
}

fn consume(items: Seq[String] uses io) uses io: List[String] {
  items.toList()
}
```

Calling delayedPrint only captures text and returns a sequence, so its function
signature has no immediate effect. Traversing the result uses io. Construction
expressions and capture expressions are evaluated immediately and their own
effects still count. Ordinary producer factories may capture only callbacks with fixed, known
effects. An unresolved open callback cannot be converted to a fixed latent
qualifier: `fn delayed(f: () => Ok): Seq[Int] { generate[Int] { f(); yield 1 } }`
is rejected. Require `f: () uses nothing => Ok` for a pure producer, or declare
its fixed effects and return `Seq[Int] uses io` when it uses io. A wrapper that
returns `items.map(f)` has the same restriction if f's effects remain open.
Built-in adapters can infer latent effect unions from concrete callbacks at
the call site; this does not introduce symbolic effect parameters for ordinary
functions. Diagnostics suggest the needed callback qualifier and Seq result
qualifier instead of charging a deferred call as an immediate effect.
Effects cannot be erased by aliases, generic containers, records, interface
bindings or function returns. Pure sequences fit an effectful sequence slot;
sequences with more effects do not fit one with fewer. Union alternatives with
the same element type join their latent effects into one Seq type; effects are
not a runtime discriminator. A sequence type pattern must retain or broaden
the incoming latent qualifier, and cannot filter effectful values into a pure
slot. The element type follows
ordinary assignability, with no extra structural or numeric conversion.

`map`, `filter` and other lazy adapters capture callbacks without invoking them.
Their result stores the union of source and callback latent effects. A call
that computes a callback still charges that call's immediate effects. Terminal
consumers and `for` charge the sequence's latent effects, plus consumer callback
effects, to their surrounding function. The compiler must model these adapter
signatures explicitly; existing eager list methods charge open callback effects
at the call site, and cannot be reused unchanged. Do not represent an effectful
Seq as a pure opaque wrapper or assume the source effect from T. Generic
functions can carry sequence types with their known effect qualifier as a
whole type argument; a pure-only Seq parameter cannot accept an inferred
unknown effect. A later effect-polymorphism surface may generalize adapters,
but the initial built-in methods infer this union without an `any` effect or
an unchecked escape hatch.

A sequence is an execution recipe, not a stable factual value: no structural
Eq/Hash, no derived codec, and default Show prints a descriptor without
traversing. This includes pure Seq values. Facts about yielded elements are
supported; predicates cannot consume an effectful sequence. Facts referring to
a traversal's transient cardinality/content are not inferred or accepted as
nominal invariants in the initial implementation. A predicate over Seq itself
is rejected until its termination and stable-observation contract is defined.

**Methods and demand.** Initial methods are `List.toSeq()`, `map`, `filter`,
`flatMap`, `take`, `drop`, `forEach`, `fold`, `toList`, and `first` (returning
Option). Static constructors belong to Seq: `Seq.empty[T]()`, `Seq.range(...)`
and `Seq.unfold(...)`. Callback invocation and item order are deterministic and
match eager list methods. `flatMap` traverses depth-first until the active inner sequence is exhausted
or the downstream consumer stops, and combines source and inner latent effects.
Downstream stop propagates immediately to both inner and outer producers,
closing both scope stacks; no later outer callback runs. Thus an unbounded inner
sequence followed by `.take(1)` emits one item and terminates normally. `take(0)`
starts no producer and invokes no callbacks; `take(n)` with n <= 0 is empty.
`take(n)` stops immediately after n emitted items without asking the source for
an extra item. `drop(n)` treats n <= 0 as zero. Integer overflow is checked in
range/unfold user arithmetic under the normal arithmetic guarantees. `toList`
consumes to completion and stores only values; consumers of unbounded sequences
must use take/first or explicit loop termination. There is no background
prefetching or eager adapter evaluation.

Filter passes on its predicate facts like List.filter; mapping passes only
facts that the callback promises/proves under the existing rules. Element
constraints on a Seq type are checked at every yield and preserved through
adapters that retain the same elements. A built-in range does not claim facts
about arithmetic results that the facts checker cannot soundly establish.
`Seq[T | E]` contains both T and E as ordinary elements. No alternative is
implicitly terminal or guessed to be an error by its name; an API such as
file.lines may document that it yields one read error and then stops. A
consumer handles that union per item, or explicitly maps/collects it according
to its desired error policy. `toList` returns List[T | E], not List[T] | E.

**Scopes, termination and cancellation.** A sequence captures the lifetimes
of every resource, scope and callback it closes over, just as a closure does.
Lazy adapters retain those dependencies even if a particular traversal would
emit zero items. Consuming it after any required scope closes is a compile-time
error; storing it in a record, returning it or passing it to a task cannot erase
that dependency. Obtaining a valid element during consumption preserves its
own lifetime too. A yielded value cannot depend on a scope opened inside the
producer, because the consumer may store it beyond that scope; strings and
copied decoded rows are safe, resource handles into producer-local scopes are
rejected. A resource from an externally captured scope may be yielded, with
that scope retained on the resulting List/Option or downstream sequence.

Scopes opened inside a producer close on normal exhaustion, consumer stop,
break, return, `?` in the consumer, cancellation and panic. An early consumer
return must stop the active producer before leaving the enclosing scope. A
failed yield callback must never resume production; its inner and outer scopes
unwind in normal nesting order. Cancellation remains cooperative: calling
checkpoint/delay on a captured scope retains its ordinary behavior, and a
producer using them carries the relevant scope/effects. Yield does not silently
insert cancellation exceptions or stop signals observable as ordinary values.
Synchronous infinite work that never yields cannot be forcibly cancelled by
this feature. Interop producers that ignore a stop callback violate the unsafe
boundary's contract and fail loudly at runtime if they yield again after stop.

**Go representation and interop.** Lower Seq[T] to a compiler-owned wrapper
around `iter.Seq[Go(T)]`, retaining latent effect and lifetime metadata in the
checker. Producer code calls yield and immediately returns when false. Adapter
methods use synchronous wrappers; traversal uses Go range-over-function or an
equivalent callback with a typed control-flow result. No worker goroutine or
channel is introduced. The lowering must correctly translate return/`?` from
a consuming bork function across the Go callback boundary, rather than returning
only from the callback or losing the enclosing function's result.

Checked unsafe Go bindings can receive/return `iter.Seq[T]` for supported
bork/Go element mappings. A returned Seq's declared latent effects are a trust
contract of the binding, distinct from the effects of obtaining it; captured
resource dependencies follow binding parameter lifetimes conservatively. The
binding generator wraps/unpacks the iter representation rather than treating it
as an ordinary function value and dropping its metadata. Iterators must call
yield synchronously, stop at false, and not retain yield for later invocation.
Mappings that cannot prove the element representation or lifetime are rejected.
Standard-library producers include `fs.Lines(path)`, `fs.Entries(path)`,
`sql.Rows[T](connection, sql.Unsafe(query), params)` and `sql.RowsJson(connection, sql.Unsafe(query), params)`. Each traversal reopens the file/directory or executes a fresh query. Directory entries follow filesystem order; file lines have no scanner token-size limit and strip LF/CRLF. SQL sequences retain the connection/transaction lifetime. Errors are explicit final elements. Row buffers must be
copied/decoded before yielding; no borrowed Go scanner buffer may escape as an
immutable bork value. APIs must state whether repeat traversal reopens an input
or continues a captured cursor. Scope-bound operations never silently open a
resource with a lifetime longer than their declared scope.

**Implementation and acceptance.** First implement Seq types/latent effects,
producer and loop checking/lowering, lexical yield boundaries and scope
unwinding; then adapters/facts; then Go iter binding and standard-library
producers. Keep `bork describe` able to display element type, latent effects
and known dependencies. Stable diagnostics distinguish invalid yield, result
and callback types, forbidden fact observation, missing consumer effects,
effect erasure (including union type patterns) and scope escape. Grammar, formatter, editor syntax, README and
requirements must be updated alongside each implementation increment.

Tests cover laziness, exact callback counts (including take(0)/take(1)), repeated
pure/effectful traversal, adapter order, flatMap nesting, explicit error unions,
nested flatMap followed by take/first/break (including an unbounded inner),
whole-function return/`?` from consumers, nested producers, rejected nested
callback/task yields, break/continue across a generate boundary, and no extra
item after early stop. Effect negatives cover
pure consumers, alias/record/generic erasure and returned callbacks; lifetime
negatives cover escaping captures and producer-local resource yields. Runtime
cases verify resource cleanup on exhaustion/break/return/panic/cancellation,
retained values after consumption, and foreign iterators honoring stop. Infinite
unfold plus take verifies bounded demand without accumulating an eager source.

The compiler provides these constructors and adapters with inferred callback types;
`Seq.empty[T]()` requires an element type, and unfold also accepts explicit `[T, S]`.
The initial checked Go bridge requires infallible element conversions: a fallible
Go-to-bork element mapping is rejected rather than introducing a hidden traversal
error. Direct checked binding Seq types require an explicit latent qualifier,
including `uses nothing`. A nil Go iterator maps to an empty sequence. Loops may
borrow an outside owned scope but cannot consume its owner repeatedly. Cleanup
inside loops retains only active scopes and owners, avoiding one deferred cleanup
per produced item. Generated modules require Go 1.23 for range-over-function.
### Record conversion (implemented: bork-2zn4s1)

A compiler-provided record method converts a source into a named target record:

```bork
type User = { id: Int, name: String, internal: Bool }
type UserDto = { id: Int, name: String, city: String = "unknown" }

fn publicUser(user: User): UserDto {
  user.into[UserDto](city: "Springfield")
}
```

The conversion is target-driven. Extra source fields disappear. For each target
field, an explicit named override wins; otherwise a same-named source field is
copied when assignable; otherwise a same-named record is converted recursively.
A target default is used only when that field is absent from the source. A
present incompatible source field is an error, rather than silently discarded
in favor of a default. An absent required field is an error naming its full
path. No runtime reflection, numeric coercion, name matching heuristics or
implicit `T` to `Option[T]` lifting is involved. Aliases are resolved without
forgetting their underlying nominal construction owner or field requirements.
The target must be a record, not a resource, opaque Go type, sealed type or
union; concrete instantiations of generic records are supported.

Overrides are ordinary expressions with the target field's expected type:
`user.into[UserDto](name: user.name.toUpper())`. A lambda is a value only for a
function-valued target field; it is never implicitly invoked on the source.
This keeps the surface consistent with named function arguments and `copy`,
and avoids ambiguity between a callback override and a function-valued field.
Renaming a field uses an override. Nested paths use the existing `copy` syntax:
`address.city: "Springfield"`. Ancestor/descendant overrides cannot overlap.
A nested path forces validation of the completed nested candidate even when
that source field was otherwise directly assignable; unchanged fields retain
their values, not a blanket assumption of whole-value validity. If the source
parent is absent, a default on that whole parent field supplies the baseline
before applying nested overrides. For example, an absent `address` with default
`Address { city: "A", zip: 123 }` and override `address.city: "B"` retains
`zip: 123`. Without a whole-parent default, complete the nested candidate from
its explicit overrides and nested type defaults, reporting missing required
leaves normally. A whole nested
conversion can also be supplied explicitly:
`address: user.address.into[AddressDto](city: "Springfield")`. A builtin
conversion cannot be shadowed by a user method named `into`; diagnostics point
to the declaration and suggest another method name.

**Evaluation and failures.** Evaluate the receiver once, then explicit
overrides in source order, before filling the remaining fields. Every explicit
override is evaluated once. Automatic recursive fields and default expressions
are then processed in target declaration order. Ordinary effects of receiver,
overrides and defaults are charged to the enclosing function; mapping fields
itself has no effect. A conversion that reaches a bottom-typed expression
terminates at that expression and does not evaluate later work.

A fallible override can be written explicitly with `?`:
`user.into[UserDto](id: parseInt(text)?)`. Its error leaves the enclosing
function, as usual. We also accept a field override of type `V | E` when `V`
is assignable to the target field and `E` is not: conversion returns
`Target | E` without constructing a target on that failure. If the full union
is assignable to the field, it is a field value, not a failure channel. All
union members not admitted by the target field are error alternatives, and
all errors are ordinary bork values. The compiler rejects an override with no
success member and reports the expected field type. It does not guess based
on names such as `Error`. A target `Int | ParseError` therefore retains that
whole union as data, whereas a target `Int` lifts `ParseError` to the conversion
result. Error alternatives equal to the target record type would collapse the
success/error distinction and are rejected; use `?` or a wrapper error type.

Receiver evaluation occurs before overrides. Within the overrides, the first
failure in source order stops conversion; later override expressions, defaults
and recursive conversions are not evaluated. This is an intentional difference
from ordinary function argument evaluation, made explicit by the conversion's
union result. Automatic recursive mapping is infallible in this initial design: it performs
no callbacks, defaults are closed values, and only explicit overrides lift
errors. Per-element fallible mapping must be written as explicit user code returning
`List[B] | E` and supplied as a complete field override; `.map` alone would
produce `List[B | E]`, not lift element failures into the outer result. Only successfully evaluated
overrides enter the final candidate. An explicit
`?`, `return` or panic inside any expression retains its existing enclosing
function semantics. Diagnostics and `bork describe` show the inferred result
union; no hidden exceptions or zero-filled target escape.

**Nested records and containers.** Initial implementation includes recursive
record fields and `List[A]` to `List[B]` / `Option[A]` to `Option[B]` when the
element conversion is a record conversion under these same rules. Preserve
list order; map a present Option once and keep `None` unchanged. Directly
assignable containers are reused as values. Nested collection shapes can
recurse through these two containers. Automatic element conversion introduces no runtime failure channel; a future
per-element override API would need its own ordering and early-failure contract. Do not auto-convert Map keys/values,
sealed variants or numeric types; explicit `.map` or an override supplies
those policies. Repeated source/target pairs on the active conversion path
produce a cycle diagnostic, rather than unbounded compiler recursion; a
completed pair can be reused at a sibling path. Nested overrides apply only at their explicitly named paths; they do not
propagate by matching leaf names elsewhere.

**Guarantees and private construction.** Conversion is record construction,
not a cast. The completed candidate is unvalidated until the same checker
used for record literals has proved every target field constraint, sibling
relation and whole-value invariant. Source field identities and already-known
source facts can support those proofs; the target's nominal facts cannot prove
its own validity. Overridden fields invalidate source relationships involving
those identities. Copying an existing nested target value preserves that
value's established guarantees. Converting a different nested record creates
a new candidate and checks its obligations independently. There is no fallback
that assumes a target's invariants because its Go struct has been allocated.

The initial conversion does not invent a target validation-error type or
silently turn failed proof into runtime validation. Facts must be statically
proven, including for the successful branch of a fallible override. Guard the
input, use an owning smart constructor, or expose a suitable promise on the
override function when proof is unavailable. Runtime decoders and the owning
package's explicit fallible factories remain the checked boundary for arbitrary
external data. Automatic conversions may become fallible through explicit
overrides; that does not relax facts on their successful values.

`type Config = private { ... }` can be constructed only in its own package,
including through `into`. Foreign conversion into Config is rejected even
through an alias, generic wrapper, nested field or collection conversion. The
compiler never searches for or calls a constructor implicitly. Foreign code
can reuse an already-valid Config field without rebuilding it, or explicitly
call the package's exported factory in an override. When source and target are
identical and there are no overrides, return the existing value unchanged;
this identity operation performs no private construction. It still proves any
additional target-alias constraints: identical representation does not imply
identical guarantees. With any overrides,
a private record needs the owning package, just like `copy`. Readable source
fields may be projected out of a private record into a public DTO. The result
retains the scope dependencies of all source values and overrides it contains.

**Diagnostics and acceptance.** Use stable `conversion.*` diagnostics for
invalid target, missing field, incompatible field, duplicate/unknown override,
ambiguous failure member and recursive conversion cycle. Construction privacy
uses the existing `construction.private_record` diagnostic. Fact failures use
the ordinary fact diagnostics, with a conversion field path or completed target
as the subject. Missing/incompatible-field diagnostics offer an illustrative
named override at the call site (`requires_input: true`); unknown labels offer
a unique likely target-field spelling. Fixes must not duplicate evaluation of
the receiver or invent a factory name.

Acceptance cases cover flat/nested projection, generic records and aliases,
default precedence (including whole-parent defaults plus nested overrides),
missing and incompatible fields, overrides in source order,
single receiver/override evaluation, direct versus lifted unions, early failure,
`?`/return/panic control flow, List/Option recursion and order, function-valued
fields, scope escape prevention, and source mutation opacity. Guarantee cases
cover target field/sibling/whole-value facts, invalid overridden relationships,
independent candidate validation, private targets through every recursive path,
allowed reuse and public projection of private source fields. Grammar, formatter,
editor syntax, `describe`, requirements and README must match the shipped shape.
Conversion reuses bork-kum0ep's construction ownership and candidate/invariant
validation checks, with regression tests proving conversion cannot bypass them.

The inspiration is [q's compile-time record conversion](https://gigurra.github.io/q/api/convert/),
which resolves matching fields and overrides without runtime reflection. Bork
adds ordinary named-expression overrides, union results, facts and package
construction ownership rather than importing q's Go-specific callback API.

The compiler implements record conversion by expanding each call into ordinary
bindings, matches, record candidates, and eager List/Option mappings. This preserves
source-order failures and runs the existing fact, construction-privacy, and lifetime
checks on every newly constructed candidate. Identity reuse is annotated with the
written target type so additional alias requirements are checked.

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
6. Go interop: `unsafe go` function bodies (above); checked bindings, opaque types, mirror records, and reflection schemas (implemented above)
7. Tooling: the `bork` CLI, formatter, tests, and modules (packages are decided, above)

See also [roadmap.md](roadmap.md) for the implementation plan.

## Explicitly not in v0.1

- Mutable ordinary bork values (opaque Go objects and explicit state capabilities are separate boundaries)
- Importing Go packages as bork modules (calling Go goes through `unsafe go` bodies and bindings)
- `recover`
- Erlang-style routine isolation and supervision

### Binary data

`Bytes` is built-in immutable binary data, represented by a distinct named Go
byte slice. It has content equality and may be a map key. It remains distinct
from `List[Byte]`, including when both appear in a union. Conversions copy at
Go boundaries; bork has no mutation operations. Construction uses `List[Byte].toBytes()` or
`encoding.Utf8` from `bork/encoding`, without new literal syntax. `encoding.ParseUtf8` validates UTF-8 and
returns a union error. Immutable access uses `length`, `isEmpty`, `get`,
`toList`, `slice` (bounds errors are unions), and `concat`. Printing uses
`Bytes(lowercase hex)` rather than guessing text.

## Parallel collections (implemented, bork-pd7rjm)

Lists expose `parMap`, `parFilter`, `parFlatMap` and `parForEach`. Each takes
an explicitly pure callback (`uses nothing`), needs no scope, and joins its
workers before returning. Results retain input order, including flattened
sublists and filtered elements; pure `parFilter` retains its predicate fact.
For successful pure computations, scheduling does not change the result.
`workers: n` bounds concurrency; omitted or nonpositive values use Go's
`GOMAXPROCS` (the CPUs available to the process), and the count is capped at
the input length. Empty inputs start no workers; singleton pure inputs and
`workers: 1` run on the calling goroutine. Parallel overhead makes ordinary
`map` preferable for cheap callbacks and short lists.

Effects require `parMapIn(s, f)`, `parFilterIn`, `parFlatMapIn`, or
`parForEachIn`. The `In` suffix avoids adding method overloading. Their
callbacks take `(Scope, T)` so cancellation-aware work receives its scope
explicitly. Calls charge `state` plus the open callback's effects and return
`List[U] | Cancelled` (or `Ok | Cancelled` for for-each). Cancellation stops
scheduling more elements, and a call waits for callbacks already started.
Those callbacks cooperate through `checkpoint`, `delay`, channel operations,
or scoped standard-library I/O. Partial results are discarded. Side-effect
order is unspecified even though result order is stable. Worker panics stop
scheduling, cancel an effectful worker's scope, join all workers, and panic
the caller. Cancellation never forcibly terminates a callback.

`parMapUntil[B, E](f)` maps a pure callback returning `B | E` into
`List[B] | E`. Supply the success and failure type arguments explicitly when
inference cannot separate them. `B` must be a concrete, non-union type,
distinguishable from every member of `E` in Go; the result `List[B]`
must also be distinguishable from failures; `E` can itself be a union.
Both types must be concrete, including any nested type arguments. This restriction
keeps success distinguishable in Go's erased union representation. By
convention `B` is the callback's leftmost result, as with `?`. The first
observed failure stops further scheduling; callbacks already running finish
before the call returns. If failures race, which failure wins is unspecified.

`parMapUntilIn[B, E](s, f)` also returns `Cancelled`. Its callback receives
an internal child cancellation scope: the first observed failure cancels
siblings without cancelling `s`. The call joins its workers, while the child
scope's resources and any tasks a callback explicitly spawned remain owned
until `s` closes. This allows successful callbacks to return resources whose
checked lifetime is `s`. Only operations bound to the callback's child scope
see early-stop cancellation. Parent-owned channels and I/O handles still
follow their parent's cancellation, so a blocking operation on one may keep
the call waiting. Use child-owned handles or child checkpoints for work that
must stop on an element failure. Callbacks should await their own spawned work if its
completion is part of the element operation. An observed callback failure
wins over concurrent external cancellation; otherwise cancellation returns
`Cancelled` and discards partial results.

Maps can use `entries().parMap(...)` or `values().parMap(...)`; no parallel
map mutation API is introduced. See [the runnable example](../examples/parallel_lists/main.bork)
for pure mapping and an eight-wait comparison with four scoped workers:
`bork run examples/parallel_lists -- --benchmark`.

## Task fan-in, typed selection, and timeouts (bork-58t786)

Task lists provide `awaitAll()` (ordered results, joining every task even if
one panics), `awaitFirst(s)` (first observed completion), and
`awaitAllUntil[B, E](s)` (first observed failure or all ordered successes).
Existing tasks retain their original owners; early return leaves pending
tasks there, and their owners still join them. First-completion and
failure-aware waiting use `state` and return cancellation as a value.
`awaitFirst` returns `Option[T] | Cancelled`, with `None` for an empty list.
The Until variant follows parallel mapping's concrete, distinguishable
success/failure restrictions and returns `List[B] | E | Cancelled`.

`race(s, [work...])` starts callbacks taking a scope in an internal child
scope. It returns the first observed value as `Option[T] | Cancelled`,
cancels the child, and joins all work before returning. Empty work lists give
`None`. A callback that ignores cancellation can delay completion. Tasks
explicitly spawned by callbacks are also joined, and worker panics propagate.
The join is unconditional: `taskTimeout` limits scope closure, not these calls.
Cancellation follows the callback scope: parent-owned channels and resources
continue to follow the parent. Consume a response or other cancellation-aware
resource inside the callback before returning the winning value.

`withTimeout(s, ms, work)` passes a child scope to work and gives
`T | Cancelled`. The deadline includes callback-spawned tasks, all of which
are joined. Cancellation is cooperative; it does not terminate the callback.
Nonpositive timeouts cancel before calling work. Oversized millisecond values
are saturated to Go's maximum duration. A successful operation stops its
deadline, so its returned resources remain usable; internal child resources
stay owned until `s` closes. `withTimeoutDo` is the Ok-callback counterpart,
returning `Ok | Cancelled`, as Ok is not a generic value argument.

Ready tasks have no specified tie order. The task selector supports up to
65,535 task completions in one wait; exceeding that bound panics.

### Select (implemented, bork-73dn9h)

`select { arms }` waits until one of its arms' channel operations can
complete, completes exactly that one, and gives the value of that arm's
body. An operation arm is
`[name =] ch.receive(s) => body` or `[name =] ch.send(s, x) => body`: the
same calls as outside a select, written with positional arguments. The
channel, scope and value expressions are evaluated once, in source order,
before waiting. `name` (or `_`) binds the operation's result, without
`Cancelled`: `T | Closed` for a receive, `Ok | Closed` for a send. A
closed channel's receive is always ready. At most one `_ => body` arm
makes the select not wait: it runs when no operation is ready. Among ready
operations one is chosen uniformly at random. If a scope an arm waits in,
or a channel's own scope, is cancelled before an operation completes, the
select completes none and gives `Cancelled`, even with a `_` arm. The
select's type is its arm bodies' types joined as match arms are, and
`Cancelled`, so `select { ... }?` passes cancellation on. Arm bodies are
code of the enclosing function: `return`, `?`, `break` and `continue`
mean what they mean around the select. The checker lowers a select to
ordinary code calling internal prelude functions, which programs cannot
name, so effects, facts and lifetimes are checked as for the operations
written out: a value sent must outlive the channel, and a received value
lives as long as its channel. `select` is a keyword only where an
expression starts and `{` follows. The list form
`[ch.receiveCase(f)].select(s)` was removed.

Effects of work callbacks inside a list are open in parameter declarations
such as `List[(Scope) => T]`. Calls charge the effects of the actual elements,
including when an open callback list is forwarded. Explicit `uses nothing`
still requires purity; function values close open list positions as pure.
This extends the existing direct-callback rule just to lists. Task, channel,
and receive-case representations have package-controlled construction so
native completion/selection handles cannot be paired with the wrong types.
Generic result unions that specialize to one type retain that type at their
Go call boundary, including references to the specialized function.

### Invariant failure diagnostics (bork-lajucp)

`DecodeError` and `GoValueError` retain their `{path, message}` shape. A failed
transparent comparison or AND group reports the first failed condition in
source order, its primary candidate field path, and the invariant's name.
For example, a configuration budget failure is
`DecodeError { path: ".bodyLimitBytes", message: "must satisfy bodyLimitBytes >= 1024 (Configured)" }`.
Relations name every compared field in the message. Field and sibling facts
use the same diagnostics, with list indices and nested fields in the path.
Decode, environment/CLI loading, and checked Go conversion share this behavior.

Diagnostic expansion supports comparisons of parameters, field projections,
and constants, and AND groups of those comparisons. Native/opaque bodies,
statements, OR groups, and other expressions retain the named-predicate
fallback. A single scalar unary comparison also retains its existing concise
predicate message. Validation still runs the original predicate first;
diagnostics explain a failure without changing which values are accepted.

### Distinct Rune values

Rune literals have type `Rune`, never a contextual number type. Rune has Eq,
Show (the character), and Ord (code-point order); arithmetic requires `r.code()`.
`Int32.rune()` returns `Option[Rune]` and rejects negative code points,
U+D800–U+DFFF, and code points above U+10FFFF. `runeToString` is removed;
migrate to `toString(r)` or string interpolation. JSON Encode/Decode use a
string containing exactly one Unicode scalar, including in derived records.
The generated shared runtime defines Rune once as a named type backed by Go
int32. Go bindings convert scalar Rune arguments to int32 and validate int32
results (invalid scalars return GoValueError). Collection bindings copy and
convert elements at the Go interop boundary; these are O(n) conversions, as
with other collection bindings. Inline unsafe Go uses `rune(r)`/`int32(r)`
when calling Go and `_Rune(n)` when explicitly constructing a trusted Rune.


## Editor language server

`bork lsp` implements LSP over stdio, with full-document synchronization and
UTF-16 positions. Diagnostics use the in-process compiler Session and unsaved
source overlays for package siblings and imports. Hover shows types and proven
facts; definitions, references, completion, document symbols, formatting and
compiler suggested-edit quick fixes share compiler and formatter behavior.
Broken edits retain the last successful analysis for navigation, with stale
hover/completion labels. References and rename use a compiler-owned declaration index across local
workspace packages, including closed importers. Rename includes exported
functions, types, record fields, variants, predicates, package values, locals
and parameters. Proposed edits recheck in memory and preserve checked bindings;
libraries and standard sources remain read-only. Broken workspace packages and
opaque raw Go bodies prevent edits. Classes, instances and ambient values are
not yet renameable. The packageable VS Code client locates a
configured or PATH-installed compiler. Packaging does not publish it.

The in-repository tree-sitter grammar provides structural highlighting, locals,
indentation, folds and Go injection for other editors. CI checks every example
and compiler case, requiring clean trees except for an explicit recovery-fixture
allowlist whose entries must exist and remain malformed. Generated parser sources
and compiler-keyword coverage are checked for drift. This grammar supplies editor
structure; compiler diagnostics and formatting come from `bork lsp`.

Native packages under `editors/` cover Vim, Neovim 0.11+, Emacs 29+, Helix and
Zed. CI highlights every example/case with their lexical or adapted tree-sitter
queries, tests real Neovim/Eglot compiler attachment, builds the Helix grammar and
Zed WASM extension, and checks keyword/query drift. Upstream integration patches
are review drafts; registry publication and third-party submissions are separate
steps. See [editor setup](editors.md) for package installation.

### Single-file scripts (implemented)

A `.bork` file beginning with a shebang is a script; `bork script <file>` also selects script mode without a shebang. Top-level ordinary bindings and executable statements run eagerly, in order, as locals in an implicit main with entrypoint effects. Helper functions cannot capture these locals; they take parameters or read explicit pure `lazy` package values. An explicit main and importing scripts as packages are rejected. Source positions, formatter output and describe queries retain the original file.

Standalone script header comments `// bork:require <Go module> <canonical pinned version>` and `// bork:unsafe` resolve a cached manifest/checksum graph and allow this script's unsafe Go, respectively. Effects remain checked. Inline directives are compile errors in bork.mod projects, where manifests govern dependencies and unsafe grants; no allowlist is defined. Scripts reuse the default Linux/macOS compile cache and normal executable staging. Runtime arguments and effects are never cached. See [scripts](language/scripts.md).

### Method-chain continuation (bork-e6seng)

Method and field selectors can continue an expression on a following line with a leading dot, across whitespace and comments. Trailing-dot continuations remain accepted. The formatter indents continuation lines one level and distinguishes contextual variant dots from receiver selectors (bork-e6seng).

Standalone script requirements also resolve Bork libraries, including the
complete transitive graph, into the script resolution cache. Editor checks use
cached dependencies and diagnose missing downloads without implicit module
installation. Formatting and editor edit requests reject shared module-cache
sources, while navigation and hover retain their original positions. Library
authors can test their own local Go helper packages through a temporary compiler
replacement that never enters committed manifests. Cache cleaning preserves
shared Go module downloads. A committed library/consumer fixture is verified and
built offline with a local file proxy.


### Advisory linting

`bork lint` reports compiler-backed advisory warnings for unused
parameters and private declarations, redundant proved predicate checks, boolean
simplifications and needless declared effects. The LSP publishes the same warnings
and safe fixes. `// lint:ignore <rule-code> reason` on the same or preceding line
suppresses these warnings (comma-separated codes or `all` are allowed). Exported
functions may deliberately reserve effects for API compatibility; this is a
reason to suppress that advisory warning. Existing compiler errors, including
nested shadowing, unused locals, unreachable match arms, unused imports and private-function effect
checks, retain their enforcement and cannot be suppressed.

- **Explicit update notice (implemented).** Released compilers can check the
  module proxy once per UTC day in a detached worker and show a cached newer
  version notice after useful interactive work. CI, JSON, machine output and
  redirected output suppress it. `BORKUPDATECHECK=off` disables it, and offline
  failures are silent. Updates remain explicit. See [update notices](cli.md#update-notices).
- **Go toolchain minimum (implemented).** Go 1.21+ launchers can automatically
  select Go 1.26.0+ for compiler metadata, generated builds, evaluators and
  dependency commands. Explicit `GOTOOLCHAIN=local` and pinned older SDKs
  report the minimum version error. Selected SDK settings appear in `bork env`;
  compilation identity follows the selected SDK. See [Go toolchains](cli.md#go-toolchains).

### Checked package documentation (bork-khnvl1)

`bork doc [package]` renders the checker-owned public API as Markdown;
`--html` produces a standalone escaped HTML page and `--all` includes packages
below the target in the same module, excluding hidden directories, vendor and
nested modules. File arguments document their whole package. Local packages,
embedded standard packages and cached pinned libraries use the normal compiler
checks and selected dependency graph. Documentation does not download missing
modules or Go toolchains, and failures produce no partial document.
Bork compiler selection follows the same rule as bork check.

Signatures preserve facts/where constraints, effects, ambient needs, generic
bounds, defaults and construction privacy. Private declarations and variants,
tests and implementation bodies are omitted. Generated constructors include
completed-value obligations. Source labels are relative to the module rather
than guessed repository URLs. Adjacent whole-line comments and fenced examples
share one renderer with LSP hover. Authors can commit generated API.md or HTML;
a hosted documentation workflow and library index remain deferred.

### Pattern tests and typed assertions (bork-878uk7)

`value is Pattern` is a Bool expression at comparison precedence. It supports
match patterns without bindings, and evaluates the operand once. Unknown or
private variants, unknown fields and unsupported constraints are errors; a valid
disjoint test returns false. `is` does not narrow source bindings or establish
facts around an enclosing branch. Type tests may include direct predicates and
constrained aliases. Structural checks precede predicates; `and` and `or`
short-circuit in source order, predicate preconditions must be proven, and
predicate panics propagate. Relational arguments follow existing constraint
scope rules. Statically certain tests receive suppressible lint warnings
`lint.pattern-always-true` or `lint.pattern-always-false` without eliminating
operand or predicate evaluation.

`test.AssertIs[T](value)` from `bork/test` requires one explicit target type and
infers its input independently. It returns the checked value narrowed to T,
including proven direct and relational facts. Failure reports the target and its
predicates, the actual value, its bork type and the caller's source location,
using the ordinary test panic path. The compiler recognizes the resolved standard
package function, and other generic functions keep ordinary constraint rules.
Runtime tests must not establish guarantees erased by Go representation,
including different function or sequence effects with the same representation.
