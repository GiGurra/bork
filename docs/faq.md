# Frequently asked questions

Bork's diagnostics point to the unmet rule. These examples show the failing
program, the compiler's actual message, and a checked fix. See the
[cheat sheet](cheatsheet.md) for common syntax and
[coming from another language](coming-from.md) for familiar-language comparisons.

## How do I start or inspect an unfamiliar API?

Run `bork new app`, then `bork run app` and `bork test app`.
`bork check --json app` gives structured diagnostics;
`bork describe app/main.bork:1:1` queries a source position.
`bork doc bork/time` renders a package's public API. The
[command reference](cli.md) explains these commands. For built-ins and methods,
use the language pages and the compiler's describe output.

## Why is Some undefined?

```bork fails
fn main() { println(Some(3)) }
```

```text
undefined function: Some
```

Qualify the variant with its owner. Option.None is bare; Some takes a payload. A leading .Some(3) also works where an expected Option type supplies the owner.

```bork
fn main() { println(Option.Some(3)) }
```

## Why does a named argument fail to parse?

```bork fails
fn greet(name: String): String { name }
fn main() { println(greet(name = "Ada")) }
```

```text
named arguments use ':'; write `name: value`
```

Named arguments use a colon. Equals introduces a binding or default, not an argument label.

```bork
fn greet(name: String): String { name }
fn main() { println(greet(name: "Ada")) }
```

## Why is an unused local an error?

```bork fails
fn main() {
  answer = 42
}
```

```text
binding answer is never read; discard explicitly with _
```

Read the binding, remove it, or use _ = expression when discarding the result is intentional. Every replaced same-block binding must also have been read.

```bork
fn main() {
  answer = 42
  println(answer)
}
```

## Can I reuse a name?

```bork fails
fn main() {
  name = "Ada"
  if (true) {
    name = "Grace"
    println(name)
  }
  println(name)
}
```

```text
name is already defined in an enclosing scope (bork does not allow shadowing)
```

Same-block rebinding is allowed and creates a new immutable binding. Inner blocks cannot shadow outer names; choose a distinct name there. Loops have explicit carrying rules.

```bork
fn main() {
  name = "Ada"
  println(name)
  name = "Grace"
  println(name)
}
```

## Why is a declared effect rejected?

```bork fails
fn greet() uses io + net { println("hello") }
fn main() { greet() }
```

```text
greet declares net, but never uses it
```

A private function must declare exactly the effects it uses. Remove unused effects. Exported functions and main may retain broader effect declarations as an API contract.

```bork
fn greet() uses io { println("hello") }
fn main() { greet() }
```

## Why must I handle a returned union?

```bork fails
type Failure = { message: String }
fn answer(ok: Bool): Int | Failure {
  if (ok) { 42 } else { Failure { message: "no answer" } }
}
fn main() { answer(false) }
```

```text
value of type Int | Failure is not used (function main returns no value)
```

Handle every alternative with match, or propagate failures from a helper whose result includes them. A bare call returning a value or failure cannot silently disappear.

```bork
type Failure = { message: String }
fn answer(ok: Bool): Int | Failure {
  if (ok) { 42 } else { Failure { message: "no answer" } }
}
fn main() {
  match (answer(false)) {
    value: Int => println(value)
    error: Failure => println(error.message)
  }
}
```

## Why does ? fail in main?

```bork fails
type Failure = { message: String }
fn answer(): Int | Failure { Failure { message: "no answer" } }
fn main() { println(answer()?) }
```

```text
? would return Failure from main, but main returns Ok
```

The enclosing function must be able to return propagated failure types. main returns Ok, so translate failure values there into messages and, when needed, a nonzero process.Exit. In helpers, the first union member is success.

```bork
type Failure = { message: String }
fn answer(): Int | Failure { Failure { message: "no answer" } }
fn main() {
  match (answer()) {
    value: Int => println(value)
    error: Failure => eprintln(error)
  }
}
```

## Why does printing require uses io?

```bork fails
fn greet() { eprintln("hello") }
fn main() { greet() }
```

```text
greet uses io (it calls eprintln), but its signature allows no effects; declare it: uses io
```

Declare the effects a private function uses. Omitting uses makes it pure; main
and tests may use runtime effects without declaring them.

```bork
fn greet() uses io { eprintln("hello") }
fn main() { greet() }
```

## Why does match need another arm?

```bork fails
fn choose(ok: Bool): Int | String { if (ok) { 42 } else { "failed" } }
fn main() {
  println(match (choose(false)) { n: Int => n })
}
```

```text
match is not exhaustive: missing String
```

Cover every alternative. Guarded arms can fail their guard, so include an unguarded fallback. A wildcard can cover intentionally equivalent remaining cases.

```bork
fn choose(ok: Bool): Int | String { if (ok) { 42 } else { "failed" } }
fn main() {
  println(match (choose(false)) { n: Int => toString(n), text: String => text })
}
```

## Why can the compiler not prove my fact?

```bork fails
pred positive(n: Int) { n > 0 }
fn half(n: Int where positive): Int { n / 2 }
fn unchecked(n: Int): Int { half(n) }
fn main() { println(unchecked(2)) }
```

```text
half requires n to be positive, but that is not proven for n (check it first with if (positive(n)) { ... }, or require it: n: Int where positive)
```

Check the predicate in a guard, require the fact in the helper parameter, or decode through a validated type. Use bork describe to inspect known facts. Use trust only for a deliberate proof boundary backed by tests.

```bork
pred positive(n: Int) { n > 0 }
fn half(n: Int where positive): Int { n / 2 }
fn checked(n: Int): Int {
  if (positive(n)) { half(n) } else { 0 }
}
fn main() { println(checked(2)) }
```

## Why is record construction missing a field?

```bork fails
type User = { name: String, age: Int }
fn main() { println(User { name: "Ada" }) }
```

```text
User is missing field(s): age
```

Supply every required field, or declare an appropriate default in the type. Use Option[T] when absence is part of the data model; a missing required field is not implicitly null.

```bork
type User = { name: String, age: Int = 18 }
fn main() { println(User { name: "Ada" }) }
```

## Why does this field have the wrong type?

```bork fails
type User = { age: Int }
fn main() { println(User { age: "18" }) }
```

```text
field age of User must be Int, found String
```

Bork does not implicitly convert String to Int. Use a typed value or parse external text with bork/strconv and handle its result union.

```bork
type User = { age: Int }
fn main() { println(User { age: 18 }) }
```

## Why may a resource be released?

```bork fails
import "bork/fs"
fn broken(path: String) uses io: String | fs.Error {
  file = scope s { fs.Open(path, s)? }
  fs.ReadAllText(file)
}
fn main() { println(broken("notes.txt")) }
```

```text
file may be released: it belongs to scope s, which ended on line 3
```

Consume the resource while its owning scope is open. Return an ordinary value, take a caller-owned scope when the resource must stay open, or use the documented transfer/attachment operations.

```bork
import "bork/fs"
fn read(path: String) uses io: String | fs.Error {
  scope s {
    file = fs.Open(path, s)?
    fs.ReadAllText(file)
  }
}
fn main() { println(read("notes.txt")) }
```

## Why does a tuple selector fail?

```bork fails
fn main() {
  pair = (42, "answer")
  println(pair.2)
}
```

```text
(Int, String) has no field 2
```

Tuple positions start at zero and their bounds are checked at compile time. List.get(index) instead returns Option because list length may be dynamic.

```bork
fn main() {
  pair = (42, "answer")
  println(pair.1)
}
```

## Why does & reject Bool?

```bork fails
fn main() { println(true & false) }
```

```text
operator & needs integers; use && for Bool operands
```

Use && and || for Bool. Integer bitwise operators include &, |, and ^.

```bork
fn main() { println(true && false) }
```

## Does leaving a scope finish its tasks?

Scope exit cancels tasks, joins them, then cleans up resources. Await inside the
scope when work should finish before leaving. Cooperative operations such as
`delay` return Cancelled when the scope ends; uncooperative work can continue
until it stops, except when `taskTimeout` explicitly limits joining. See
[scopes](language/scopes.md) for runnable examples and resource contracts.

## How do I print a record error to stderr?

Pass the value directly to `eprintln`; this works for user-defined errors and
`cli.Error`, using their `Show` instance when available.

```bork
type Failure = { message: String }
fn main() { eprintln(Failure { message: "failed" }) }
```

## How do I return a nonzero exit code?

Match the failure in main, print it, and call `process.Exit(code)` after any
handler scope has closed. A cli.Error return alone does not choose an exit
status. The [CLI recipe](std/cli-cookbook.md) shows the
complete application, help output and missing-field exit status.
