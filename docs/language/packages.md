# Packages

## Packages and modules

A **package** is a directory of `.bork` files. The files share everything they declare, and there is no package line at the top of a file.

A program made of one package needs nothing else. To use several packages, put a `bork.mod` file at the root that names the **module**:

```text
shop/
  bork.mod            module example.com/shop
  main.bork
  money/
    money.bork
```

A package is imported by the module path plus its directory, and its names are used with the package name in front:

```bork fragment
// shop/money/money.bork
pred positive(x: Int) { x > 0 }

type Cents = Int where positive

type Price = { label: String, amount: Cents }

fn Format(p: Price): String {
  s"${p.label}: ${dollars(p.amount)}"
}

fn dollars(cents: Int): String {
  s"$$${cents / 100}.${cents % 100}"
}
```

```bork fragment
// shop/main.bork
import "example.com/shop/money"

fn main() {
  price = money.Price { label: "tea", amount: 450 }
  println(money.Format(price))
}
```

- **Names that start with an upper-case letter are exported.** `Format`, `Price`, and `Cents` can be used by other packages. `dollars` and `positive` cannot.
- An import can be renamed: `import cash "example.com/shop/money"`.
- An unused import is an error, and packages cannot import each other in a circle.
- The program is run from the root: `bork run shop`.

[Standard packages](../std/README.md) are imported as `bork/name` and need no `bork.mod`.

## Type classes

A type class describes operations that a type supports. Generic code can then require it.

```bork
class Monoid[T] {
  fn empty(): T
  fn combine(a: T, b: T): T
}

instance sumInt: Monoid[Int] {
  fn empty(): Int {
    0
  }
  fn combine(a: Int, b: Int): Int {
    a + b
  }
}

instance joinText: Monoid[String] {
  fn empty(): String {
    ""
  }
  fn combine(a: String, b: String): String {
    a + b
  }
}

fn combineAll[T: Monoid](xs: List[T]): T {
  xs.fold(empty[T](), combine)
}

fn main() {
  println(combineAll([1, 2, 3]))
  println(combineAll(["a", "b"]))
}
```

- `class Monoid[T]` lists the functions an instance must provide.
- `instance sumInt: Monoid[Int]` provides them for one type. Instances have names.
- `[T: Monoid]` on a function means "any `T` that has a `Monoid` instance".

A class is separate from the type, so you can give an instance to a type you did not write, including built-in ones.

### Which instance is used

A package sees its own instances and the built-in ones. An instance from another package has to be asked for with `use`:

```bork fragment
import "bork/math"
use math.Codecs
```

Nothing is picked up from other packages behind your back, and if two instances in view fit the same type, the compiler reports the ambiguity. A package can group instances into a named set with `instances Name { ... }`, so that importers get them with a single `use`, as `math.Codecs` does above.

### Built-in classes

| Class | Provides | Notes |
| --- | --- | --- |
| `Eq` | `==` | Every type made of comparable parts has it automatically |
| `Ord` | `compare(a, b)` | Numbers, strings, and runes. Used by `sorted()` |
| `Show` | `show(x)` | How a value prints. Every type has a default |
| `Decode`, `Encode` | Conversion from and to JSON-like data | Usually derived |

To change how one of your types prints, declare a `Show` instance for it in the package that declares the type:

```bork
type Money = { cents: Int }

instance showMoney: Show[Money] {
  fn show(m: Money): String {
    s"$$${m.cents / 100}.${m.cents % 100}"
  }
}

fn main() {
  println(Money { cents: 1250 })
  println(s"prices: ${[Money { cents: 499 }]}")
}
```

## Derived instances

`derive` asks the compiler to write an instance. `Decode` and `Encode` turn records and sealed types into and out of JSON, CSV rows, command-line options, environment variables, and SQL rows.

```bork
import "bork/json"

pred validPort(n: Int) { n > 0 && n < 65536 }

type Server = { host: String, port: Int where validPort = 8080, tags: List[String] = [] } derive (Decode, Encode)

fn main() {
  println(json.Decode[Server]("{\"host\": \"example.com\"}"))
  println(json.Decode[Server]("{\"host\": \"example.com\", \"port\": 0}"))
  match (json.Decode[Server]("{\"host\": \"a\", \"port\": 443}")) {
    server: Server => println(json.Encode(server))
    failure => println(failure)
  }
}
```

Decoding respects everything the type says. Missing fields take their defaults, and a value that breaks a [fact](facts.md) is rejected with the path to it. A decoded `Server` is therefore already validated: its port is known to be valid without a second check.

## Dependency assembly

Larger programs have components that depend on each other: a server needs a database and a configuration, and the database needs the configuration too. `assemble` wires them together from ordinary functions, at compile time.

```bork
type Config = { greeting: String }
type Greeter = { config: Config }
type App = { greeter: Greeter, config: Config }

fn newConfig(): Config {
  Config { greeting: "Hello" }
}

fn newGreeter(config: Config): Greeter {
  Greeter { config: config }
}

fn newApp(greeter: Greeter, config: Config): App {
  App { greeter: greeter, config: config }
}

fn main() {
  scope s {
    app = assemble[App](s, newConfig, newGreeter, newApp)
    println(app.greeter.config.greeting)
  }
}
```

Each function is a **provider**: it makes the type it returns from the types it takes as parameters. `assemble[App]` works out the order to call them in, calls each one once, and shares the results, so both `newGreeter` and `newApp` receive the same `Config`.

The wiring is checked while compiling. A missing provider, two providers for the same type, a cycle, or a provider nothing needs is a compile error. Providers can have effects and can fail, and the `assemble` expression then has those effects and that failure union. Providers that open resources receive the scope given to `assemble`.

A `providers` declaration names a reusable list, and a test can replace one entry:

```bork fragment
providers Services = { config: newConfig, database: openDb, server: newServer }

server = assemble[Server](s, Services)
testServer = assemble[Server](s, Services(database: fakeDb))
```

See the [assemble example](../../examples/assemble/main.bork).

---

Previous: [Typed interpolation](interpolators.md) · Next: [Calling Go](go-interop.md) · [All pages](../README.md#the-language)
