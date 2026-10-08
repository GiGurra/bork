# Packages and type classes

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

A package is imported by its module path plus directory, such as
`import "example.com/shop/money"`. Use its names with the package name in
front: `money.Price`.

The [two-package fixture](../../testdata/cases/classes_bundles) contains a
complete module, a reusable `money` package and an `api` package that imports it.
Run it from a checkout with `bork run testdata/cases/classes_bundles`.

- **Names that start with an upper-case letter are exported.** Other packages use qualified names such as `money.Cents`. Lower-case functions and predicates remain private. Fields of exported records are visible wherever the record is.
- An import can be renamed: `import cash "example.com/shop/money"`.
- An unused import is an error, and packages cannot import each other in a circle.
- Run a package from its parent with `bork run shop`, or from its own directory with `bork run .`.

An optional `bork 0.4` line after `module <path>` declares a minimum compiler version. The CLI selects a suitable compiler automatically when needed; see [compiler versions](../cli.md#compiler-versions) for exact pins, cached downloads and offline use.

[Standard packages](../std/README.md) are imported as `bork/name` and need no `bork.mod`.

## Library dependencies

See [Libraries](libraries.md) for dependency commands, publishing and private
modules. Local packages and downloaded packages follow the same import and
visibility rules.

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

Class and instance members can be separated by newlines or semicolons. Leading,
repeated, and trailing semicolons are allowed; commas are not member separators.

A class is separate from the type, so you can give an instance to a type you did not write, including built-in ones.

### Which instance is used

The runnable [instance-bundle fixture](../../testdata/cases/classes_bundles)
shows how two library packages share a bundle:

1. [`money/money.bork`](../../testdata/cases/classes_bundles/money/money.bork) declares `Cents`, derives `codec.Decode` and `codec.Encode`, and exports `instances Defaults { showDollars, CentsDecode, CentsEncode }`. An exported bundle can include private instances.
2. [`api/api.bork`](../../testdata/cases/classes_bundles/api/api.bork) imports `money` and selects `use money.Defaults`. It derives codecs for `Item`, then exports `instances Json { ItemDecode, ItemEncode money.Defaults }`. Bundles can include other bundles.
3. [`main.bork`](../../testdata/cases/classes_bundles/main.bork) imports `api` and selects `use api.Json`. That brings in the codecs for both records, including the nested `money.Cents`.

A package sees its own instances and the built-in ones. Importing a package
alone does not select its instances: use an exported instance or bundle with
`use`. If two selected instances fit the same request, the compiler reports an
ambiguity. The class name itself is not an instance import.

### Built-in classes

| Class | Provides | Notes |
| --- | --- | --- |
| `Eq` | `==` | Every type made of comparable parts has it automatically |
| `Ord` | `compare(a, b)` | Numbers, strings, and runes. Used by `sorted()` |
| `Show` | `show(x)` | How a value prints. Every type has a default |

Imported `codec.Decode` and `codec.Encode` convert between typed data and
`codec.Value`. They are usually derived; import `bork/codec` and select
`use codec.Defaults` for standard instances.

To change how one of your types prints, declare a `Show` instance for it in the package that declares the type:

```bork
type Money = { cents: Int }

instance showMoney: Show[Money] {
  fn show(m: Money): String {
    s"${m.cents} cents"
  }
}

fn main() {
  println(Money { cents: 1250 })
  println(s"prices: ${[Money { cents: 499 }]}")
}
```

A generic `Show` instance must work for every instantiation. It cannot test
membership in a type parameter inside a union, either directly or through a
helper or callback. For example, matching `_: T` in a `T | Error` field is a
compile error: implicit rendering cannot retain that membership information.
Match a concrete error member and use a wildcard for the remaining values,
or give success and failure separate sealed variants:

```bork
type Error = {}
type Box[T] = { value: T | Error }
instance showBox[T]: Show[Box[T]] {
  fn show(box: Box[T]): String {
    match box.value {
      _: Error => "error"
      _ => "success"
    }
  }
}
fn main() { println(Box[Int | String] { value: Error {} }) }
```

## Derived instances

`derive` asks the compiler to write an instance. A class owner can also provide a [derivation template](derivation.md) for a custom class. `codec.Decode` and `codec.Encode` from [bork/codec](../std/codec.md) turn records and sealed types into and out of JSON, CSV rows, command-line options, environment variables, and SQL rows. Select `use codec.Defaults` for standard primitive and container instances.

```bork
import codec "bork/codec"

import "bork/json"
use codec.Defaults

pred validPort(n: Int) { n > 0 && n < 65536 }

type Server = { host: String, port: Int where validPort = 8080, tags: List[String] = [] } derive (codec.Decode, codec.Encode)

fn main() {
  println(json.Decode[Server]("{\"host\": \"example.com\"}"))
  println(json.Decode[Server]("{\"host\": \"example.com\", \"port\": 0}"))
  match json.Decode[Server]("{\"host\": \"a\", \"port\": 443}") {
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

Ordinary tuples hold reusable provider functions. Assembly splices each tuple one
level, in positional order, then checks the complete graph:

```bork
import "bork/test"

type Config = { greeting: String }
type Greeter = { config: Config }

fn newConfig(): Config { Config { greeting: "Hello" } }
fn fakeConfig(): Config { Config { greeting: "Test" } }
fn newGreeter(config: Config): Greeter { Greeter { config: config } }

Services = (newConfig, newGreeter)

fn main() {
  scope s {
    app = assemble[Greeter](s, test.Swap(Services, fakeConfig))
    println(app.config.greeting)
  }
}
```

The tuple stores functions; each assembly constructs fresh products. Tuple
expressions evaluate once in written order, before providers run in dependency
order. Package values follow ordinary visibility and may expose private functions.
Computed tuples and local aliases work too; nested tuples are not spliced.

`test.Swap` replaces the unique element with the replacement's exact type.
`test.SwapAt(Services, 0, fakeConfig)` selects a zero-based position with a pure
compile-time Int index and also requires its exact type. Function types include
parameters, result/failure union and effects. Repeated matching element types
require SwapAt. A changed signature needs an explicit tuple instead.

Functions with uncaptured ambient needs must stay inline assembly providers or
take explicit parameters: `assemble[Server](s, newServer)`. Other existing
function-value restrictions also apply; providers with parameter facts or
retaining scope contracts can remain inline or use checked adapters.

### Several roots and record assembly

`assembleAll[T]` constructs every provider that returns `T`, in the supplied
order, and returns a `List[T]` or the providers' failure union. Repeated `T`
providers are allowed here; their dependencies still need unique providers.
`assembleRecord[R]` constructs a record from providers for its stored fields,
without a provider for `R` itself. Defaults do not replace missing providers.

```bork
type Config = { greeting: String }
type Greeter = { config: Config }
type App = { config: Config, greeter: Greeter }

fn supplyConfig(): Config { Config { greeting: "Hello" } }
fn greeter(config: Config): Greeter { Greeter { config: config } }
fn first(): String { "first" }
fn second(): String { "second" }

fn main() {
  scope s {
    app = assembleRecord[App](s, supplyConfig, greeter)
    println(app.greeter.config.greeting)
    println(assembleAll[String](s, first, second))
  }
}
```

See the [assemble example](../../examples/assemble/main.bork) for resources and
failures, and the [bundle fixture](../../testdata/cases/assemble_bundles) for
cross-package provider tuples.

---

Previous: [Channels](channels.md) · Next: [Libraries](libraries.md) · [All pages](../README.md#the-language)
