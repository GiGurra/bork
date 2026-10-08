# Coming from Go, TypeScript, Rust or Python

Bork compiles to Go and uses immutable values, typed failure unions, checked
effects and scopes. Start with the table for your language, then try the examples.
For syntax at a glance, see the [cheat sheet](cheatsheet.md).

## From Go

| Go habit | Bork equivalent |
| --- | --- |
| `var`, `:=`, assignment | `name = value` binds an immutable value. Reusing a name in the same block creates a new binding. |
| `(T, error)` | `T \| ErrorType`; match each alternative or propagate with `?`. |
| `nil` for absence | `Option[T]`, constructed with `Option.Some(value)` or `Option.None`. |
| Mutable struct fields | Record `.copy(field: value)` returns a new record. |
| `go f()` | `fork(s, () => ...)` runs in a scope. Await before leaving if you want completion. |
| `defer` and cancellation contexts | Scope-owned resources and cancel → join → cleanup at scope exit. |
| Implicit side effects | Function signatures declare `uses io`, `net`, `clock`, `random`, or `state`. |
| `[]T`, `map[K]V`, channels | Immutable `List[T]`, `Map[K, V]`, and scope-owned `Channel[T]`. |

Go is also available through checked [interop](language/go-interop.md). A bork
package containing `unsafe go` functions must be listed as `unsafe "package/path"`
in its owning module's `bork.mod`. Importing an authorized wrapper does not
require another permission in the caller's module.

## From TypeScript

| TypeScript habit | Bork equivalent |
| --- | --- |
| `let` / `const` | Bind with `name = value`; all ordinary values are immutable. |
| `{ name: string }` | `type Person = { name: String }`, then `Person { name: "Ada" }`. |
| `T \| undefined` | `Option[T]`. |
| Discriminated unions | `type Reply = sealed { Found { name: String }, Missing }`, then exhaustive `match`. |
| Exceptions / rejected promises | Typed result unions; each failure alternative must be handled or propagated. |
| `Promise<T>` / `async` | Scope-owned tasks and explicit `await(task)`. |
| `function f({ count })` or object argument conventions | Named arguments use `f(count: 2)`; positional arguments also work. |

Union values retain their types when matched. `match` also handles records,
lists and tuples; see [matching](language/matching.md).

## From Rust

| Rust habit | Bork equivalent |
| --- | --- |
| `let` / `let mut` | Immutable bindings; same-block rebinding and loop carrying. |
| `Result<T, E>` | `T \| E`; `?` propagates alternatives the enclosing function can return. |
| `Some(x)`, `None` | `Option.Some(x)`, `Option.None`; variant owners are explicit unless the expected type permits `.Some(x)`. |
| `enum` | `sealed` types with named or positional payloads. |
| `match` | Exhaustive `match value { pattern => expression }`. |
| Ownership and borrow lifetimes | Scope-owned resources and checked resource lifetime contracts; ordinary immutable values can be shared. |
| Generic `<T>` | Generic `[T]`, including `List[Int]` and `fn identity[T](value: T): T`. |

Bork's resource guarantees concern lifetimes of files, tasks and other resources.
See [scopes](language/scopes.md) before returning resource-bearing values from helpers.

## From Python

| Python habit | Bork equivalent |
| --- | --- |
| Indentation blocks | Braces; function bodies and branches can return their final expression. |
| `None` | `Option.None`, with an `Option[T]` type. |
| Exceptions | Typed failure unions and exhaustive matching. |
| Mutable lists/dictionaries | Immutable List/Map methods return new values. |
| `for x in xs`, `range(n)`, `while condition` | `for x in xs`, `range(0, n)` (exclusive end), and `for condition { ... }`. |
| Keyword arguments `f(count=2)` | `f(count: 2)`. |
| `f"Hello {name}"` | `s"Hello $name"` or `s"Hello ${expression}"`. |

## Bindings and record updates

A new binding does not change an earlier value or a closure that captured it.
Nested blocks cannot shadow enclosing names. Loops may carry eligible bindings
to their next iteration and out to the enclosing block.

```bork
type Person = { name: String }

fn main() {
  person = Person { name: "Ada" }
  earlier = () => person.name
  person = person.copy(name: "Grace")
  println(earlier(), person.name)
  total = 0
  for n in range(0, 4) { total = total + n }
  println(total)
}
```

## Failure values and absence

A record error describes failure; Option describes absence. Match their actual
constructors and types rather than assuming exceptions or null checks.

```bork
type Missing = { name: String }

fn lookup(name: String): Int | Missing {
  if name == "Ada" { 42 } else { Missing { name: name } }
}

fn main() {
  answer = match lookup("Ada") {
    value: Int => value
    error: Missing => { println(error.name); 0 }
  }
  optional = Option.Some(answer)
  println(match optional { .Some(value) => value, .None => 0 })
}
```

See [matching and errors](language/matching.md) for propagation, and the
[FAQ](faq.md) for common diagnostics and fixes.
