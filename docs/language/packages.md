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

A package is imported by the module path plus its directory, and its names are used with the package name in front:

```bork fragment
// shop/money/money.bork
pred positive(x: Int) { x > 0 }

type Cents = Int where positive

type Price = { label: String, amount: Cents }

fn Format(p: Price): String {
  s"${p.label}: ${unit(p.amount)}"
}

fn unit(cents: Int): String {
  s"$cents cents"
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

- **Names that start with an upper-case letter are exported.** `Format`, `Price`, and `Cents` can be used by other packages. `unit` and `positive` cannot be named by them. The fields of an exported record are visible wherever the record is.
- An import can be renamed: `import cash "example.com/shop/money"`.
- An unused import is an error, and packages cannot import each other in a circle.
- The program is run from the root: `bork run shop`.

An optional `bork 0.4` line after `module <path>` declares a minimum compiler version. The CLI selects a suitable compiler automatically when needed; see [compiler versions](../cli.md#compiler-versions) for exact pins, cached downloads and offline use.

[Standard packages](../std/README.md) are imported as `bork/name` and need no `bork.mod`.

## Library dependencies

A bork library is a Go module containing `bork.mod` and `.bork` packages.
Use the same dependency command for Go and bork libraries:

```sh
bork deps get github.com/acme/greeting@v1.0.0
```

Specify the library's module path, not a directory containing only `.bork`
files: Go does not recognize that directory as a Go package. Import its packages
by module path plus directory, just as for local packages:

```bork fragment
import "github.com/acme/greeting/text"

fn main() uses io {
  println(text.Greeting())
}
```

The helper records pinned `require` lines in `bork.mod`, checksums in
`bork.sum`, and a generated `go.mod`. Commit all three. Upgrade with the same
command and a newer version or `@latest`; remove a requirement with `@none`.
Go selects one version of each module path using minimum version selection.
A diamond picks the higher required version. Major version 2 and later use
`/v2`, `/v3`, and so on; different major paths can coexist in one program.
Visibility rules and package import-cycle checks apply across libraries too.
Downloaded sources stay in Go's module cache and are checked against pinned
content hashes before use. A warm cache supports `GOPROXY=off`.

A library author starts with a module and exported functions, without a
`main` entrypoint. `bork deps init` creates generated `go.mod` and empty
`bork.sum`. Use `bork deps get` for its dependencies, and `bork deps download`
before tagging to record dependencies of every authored package, including
imported std packages. Check/test the library, commit its files with a
redistributable license, and push a version tag such as `v1.0.0`. Its generated
Go manifest lets the module proxy and consumers discover transitive requirements.
The `module` and `require` declarations must agree in `bork.mod` and `go.mod`.
Do not edit generated `go.mod` or run `go mod tidy`: Go cannot see bork imports.

Library-owned `unsafe` grants apply to that library's packages. Consumers trust
its implementation without repeating those grants. `bork deps get/download`
prints newly selected library releases containing unsafe Go packages, including
transitive ones; there is no approval prompt. Checksums establish integrity, not
correctness or effect honesty. Unsafe library code can violate guarantees and
can run during compiler evaluation; it is not sandboxed.

Go's normal `GOPROXY` and `GOSUMDB` configuration applies. Use a corporate
proxy if desired, and set `GOPRIVATE` before requesting private module paths.
The published official-service rules contain no Go-only file requirement;
using valid public bork modules is a sensible-use reading, rather than an
explicit Google permission grant. See the [service analysis](../design/library-dependencies.md#official-proxy-feasibility-permission-and-privacy)
and [dependency commands](../cli.md#deps).

The [library/consumer example](https://github.com/GiGurra/bork/tree/main/testdata/libraries) includes a
library test, committed manifests and checksums, and an offline proxy test.
Library authors can bind to Go helper packages in their own repository while
checking or testing before publication. Bork stages a local replacement only in
its temporary Go compilation module; committed manifests keep published paths.

Downloaded sources are read-only in `bork fmt` and editor edit requests. Hover,
completion and go-to-definition use cached source positions. Editor checks do
not install missing modules: run `bork deps download` in the project first.
`bork clean` preserves the shared Go module cache; `bork clean --all` also drops
Bork's script resolution graphs. Use `go clean -modcache` to clear Go's cache.

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

Ordinary tuples hold reusable provider functions. Assembly splices each tuple one
level, in positional order, then checks the complete graph:

```bork fragment
import "bork/test"
Services = (newConfig, openDb, newServer)

server = assemble[Server](s, Services)
testServer = assemble[Server](s, test.Swap(Services, fakeDb))
```

The tuple stores functions; each assembly constructs fresh products. Tuple
expressions evaluate once in written order, before providers run in dependency
order. Package values follow ordinary visibility and may expose private functions.
Computed tuples and local aliases work too; nested tuples are not spliced.

`test.Swap` replaces the unique element with the replacement's exact type.
`test.SwapAt(Services, 1, fakeDb)` selects a zero-based position with a pure
compile-time Int index and also requires its exact type. Function types include
parameters, result/failure union and effects. Repeated matching element types
require SwapAt. A changed signature needs an explicit tuple instead.

Migration: replace `providers Services = { config: newConfig, database: openDb }`
with `Services = (newConfig, openDb)`, and a singleton with `(newConfig,)`.
The compiler offers a comment-preserving fix for safe declarations. Replace
`Services(database: fakeDb)` with Swap/SwapAt or an explicit tuple; Swap requires
the exact element type, unlike the old changed-signature specialization.
Functions with uncaptured ambient needs must stay inline assembly providers or
take explicit parameters: `assemble[Server](s, newServer)`. Other existing
function-value restrictions also apply; providers with parameter facts or
retaining scope contracts can remain inline or use checked adapters.

See the [assemble example](../../examples/assemble/main.bork).

---

Previous: [Typed interpolation](interpolators.md) · Next: [Calling Go](go-interop.md) · [All pages](../README.md#the-language)
