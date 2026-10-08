# Bork cheat sheet

Copy complete examples below, or follow their links for the rules. Bindings and
ordinary values are immutable; errors are values; resources belong to scopes.

## Everyday syntax

| Task | Syntax |
| --- | --- |
| Bind a value; annotate when needed | `name = "Ada"`; `count: Int = 3` |
| Rebind in the same block | `count = count + 1` creates a new binding |
| Ignore a result or pattern slot | `_ = expression`; `(name, _) = pair` |
| Declare a function | `fn add(a: Int, b: Int): Int { a + b }` |
| Default and named arguments | `fn add(a: Int, b: Int = 1): Int { a + b }`; `add(2, b: 3)` |
| Generic type/function | `List[Int]`; `fn identity[T](value: T): T { value }` |
| Lambda | `value => value * 2`; `(a, b) => a + b` |
| Conditional expression | `if ready { "yes" } else { "no" }` |
| List/map/tuple | `[1, 2]`; `{ "Ada": 3 }`; `(3, "Ada")` |
| Empty List/Map | `xs: List[Int] = []`; `counts: Map[String, Int] = {:}` |
| Record/convenient construction | `Person { name: "Ada" }`; `.{ name: "Ada" }` when expected type is known |
| Changed record | `person.copy(name: "Grace")` |
| Absence | `Option.Some(3)`; `Option[Int].None` |
| String interpolation | `s"Hello $name"`; `s"Count: ${xs.length()}"` |
| Predicate / required fact | `pred positive(n: Int) { n > 0 }`; `n: Int where positive` |
| Effect signature | `fn greet() uses io { println("hello") }` |
| Scope/task | `scope s { ... }`; `fork(s, () => work())`; `await(task)` |
| Import and select instances | `import "bork/codec"`; `use codec.Defaults` |

### Bindings, functions and collections

```bork
fn greet(name: String, punctuation: String = "."): String {
  s"Hello, ${name}${punctuation}"
}

fn main() {
  name = "Ada"
  earlier = () => name
  name = "Grace"
  println(greet(earlier()), greet(name, punctuation: "!"))
  numbers = [1, 2, 3]
  println(numbers.map(n => n * 2), numbers.get(99))
  counts = { "Ada": 1 }.put("Grace", 2)
  println(counts.getOr("Linus", 0))
  (number, label) = (3, "count")
  println(label, number)
}
```

An inner block cannot shadow an outer binding. Unused locals are errors;
parameters may be unused. A collection lookup returns Option when the key or
index can be absent. See [basics](language/basics.md),
[types](language/types.md), and [collections](language/collections.md).

## Loops

There is no `while` keyword. `for` supports collection, condition, three-part,
and unconditional forms. `range(start, end)` excludes end.

```bork
fn main() {
  total = 0
  for n in range(0, 4) { total = total + n }
  println(total)
  for i = 0; i < 3; i = i + 1 { println(i) }
  remaining = 2
  for remaining > 0 {
    println(remaining)
    remaining = remaining - 1
  }
  for {
    println("once")
    break
  }
}
```

A comprehension builds a lazy sequence:
`for { n in xs; if n > 0; sq = n * n } yield sq` is a `Seq[Int]`. See
[comprehensions](language/collections.md#comprehensions).

Rebinding eligible outer names carries their values to the next round and out
of the loop. `continue` runs the three-part loop's post clause; `break` ends the
loop. See [loops](language/collections.md#loops) for carrying restrictions.

## Records, variants and errors

```bork
type User = { name: String, active: Bool = true }
type Missing = { name: String }
type Reply = sealed { Found(User), Gone }

fn find(name: String): User | Missing {
  if name == "Ada" { User { name: name } } else { Missing { name: name } }
}

fn greeting(name: String): String | Missing {
  user = find(name)?
  s"Hello, ${user.name}"
}

fn main() {
  match greeting("Ada") {
    text: String => println(text)
    error: Missing => println(s"Unknown: ${error.name}")
  }
  reply = Reply.Found(User { name: "Ada" })
  println(match reply { .Found(user) => user.name, .Gone => "gone" })
  option: Option[Int] = Option.Some(3)
  println(match option { .Some(n) => n, .None => 0 })
}
```

Match every alternative. `?` keeps the first union member and returns the
others from the enclosing function; that function's result must include them.
On Option it unwraps Some and propagates None. Handle failures explicitly in
`main`. See [matching and errors](language/matching.md).

## Facts

Guards prove facts for later calls. External data is checked with derived
[decoders](std/codec.md); declaring a requirement alone does not validate input.

```bork
pred positive(n: Int) { n > 0 }

fn average(total: Int, count: Int where positive): Int { total / count }

fn printAverage(total: Int, count: Int) uses io {
  if positive(count) { println(average(total, count)) } else { println("count must be positive") }
}

fn main() { printAverage(10, 2) }
```

See [facts](language/facts.md) and use `bork describe` to inspect what the
compiler knows.

## Effects, scopes and tasks

```bork
fn work(s: Scope) uses clock + state: Int | Cancelled {
  delay(s, 1.millis())?
  42
}

fn main() {
  result = scope s {
    pending = fork(s, () => work(s))
    await(pending)
  }
  println(result)
}
```

Runtime effects are `io`, `net`, `clock`, `random`, and `state`. The `build`
effect is restricted to [comptime](language/comptime.md). Private functions must
declare exactly the effects they use; `main` can infer them. Pure functions have
no effects. Leaving a scope cancels its tasks, joins them, then cleans up its
resources; await inside the scope to let work finish. The explicit `taskTimeout`
limit can stop waiting for an uncooperative task. See
[effects](language/effects.md) and [scopes](language/scopes.md).

## Decode JSON

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type User = { name: String } derive (codec.Decode, codec.Encode)

fn main() {
  match json.Decode[User]("{\"name\":\"Ada\"}") {
    user: User => println(json.Encode(user))
    error: codec.DecodeError => println(error)
    error: json.JsonError => println(error)
  }
}
```

See [JSON](std/json.md), [CLI applications](std/cli.md),
[testing](language/testing.md), [coming from another language](coming-from.md),
and the [FAQ](faq.md).

## Commands

```sh
bork new my-app
bork run my-app
bork check my-app
bork fmt my-app
bork test my-app
bork build my-app -o my-app-bin
bork check --json my-app
bork describe my-app/main.bork:1:1
bork doc bork/time
```

Run `bork <command> --help` for command options. `bork test` runs one package;
see [testing](language/testing.md) for projects with several packages.
