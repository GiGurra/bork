# Compile-time evaluation

A `comptime` block runs while the program is being compiled. Its result is stored in the executable as data, so the running program does none of the work.

## Computing a value

```bork
fn isPrime(n: Int): Bool {
  n > 1 && range(2, n).all(d => n % d != 0)
}

fn main() {
  primes = comptime { range(1, 50).filter(isPrime) }
  println(primes)
}
```

The block calls ordinary bork functions. The compiler evaluates it, and the generated program contains the finished list `[2, 3, 5, ...]`. You can see this with `bork emit`.

Use it for lookup tables, parsed constants, and anything else that is known before the program starts.

## What a block may contain

A `comptime` block is closed and pure:

- It can use literals, names bound to literals, functions, and the results of earlier `comptime` blocks.
- It cannot use run-time values, such as function parameters or the result of reading input.
- It cannot have effects such as `io` or `net`. The one exception is reading build files, described below.

```bork
fn main() {
  squares = comptime { range(1, 6).map(n => n * n) }
  total = comptime { squares.fold(0, (sum, n) => sum + n) }
  println(squares, total)
}
```

The result can be made of numbers, strings, Bools, runes, lists, records, options and other sealed or union values, and maps. `Bytes`, functions, and resources cannot be stored.

## Failing the build

If a `comptime` block panics, compilation fails with the message. That turns a mistake in built-in data into a compile error:

```bork fails
fn main() {
  port = comptime {
    match (parseInt("80a")) {
      n: Int => n
      e: ParseError => panic(s"bad port: ${e.input}")
    }
  }
  println(port)
}
```

## Reading files at build time

The `bork/build` package reads a file during compilation. Together with `comptime`, this parses and validates a configuration file once, at build time:

```bork fragment
import "bork/build"
import "bork/json"

type Config = { name: String, limit: Int } derive (Decode)

fn main() {
  config = comptime {
    match (json.Decode[Config](build.ReadString("config.json"))) {
      value: Config => value
      error => panic(s"invalid build configuration: $error")
    }
  }
  println(config.name)
}
```

The executable carries the decoded `Config`. It does not read or parse `config.json` when it runs, and a malformed file stops the build.

- `build.ReadString(path)` returns the file as a `String`, and `build.ReadBytes(path)` as a `List[Byte]`.
- The path must be a constant. It is relative to the module root, which is the directory holding `bork.mod`, or to the source directory for a program without one. It cannot point outside that directory.
- These functions can only be called at compile time.

The [comptime example](../../examples/comptime/README.md) is a runnable version of this, with facts on the decoded record.

## Limits

An evaluation may take at most ten seconds and produce at most 16 MiB of data. A file read at build time may be at most 16 MiB.

To include files as they are, without processing them, [bork/embed](../std/embed.md) is simpler.

---

Previous: [Scopes and tasks](scopes.md) · Next: [Typed interpolation](interpolators.md) · [All pages](../README.md#the-language)
