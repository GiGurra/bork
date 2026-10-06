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

- It can use literals, names bound to literals, functions, pure package values, and the results of other `comptime` blocks.
- It cannot use run-time values, such as function parameters or the result of reading input.
- It cannot have effects such as `io` or `net`. The one exception is reading build files, described below.

```bork
fn main() {
  squares = comptime { range(1, 6).map(n => n * n) }
  total = comptime { squares.fold(0, (sum, n) => sum + n) }
  println(squares, total)
}
```

Package values read by `comptime` are evaluated during compilation, including through helper functions and imports. Their dependencies may be declared later or in another file. The compiler evaluates dependencies first, checks their facts, and stores the finished values as data. A dependency cycle is a compile error.

```bork
Squares = comptime { range(1, 6).map(n => n * n) }
Total = comptime { Squares.fold(0, (s, n) => s + n) }

fn main() {
  println(Total)
  println(comptime { Squares.length() })
}
```

Here `Total` is `55`. Package initializers need not contain `comptime` themselves: `Limit = 6` can also be read by a block. Values that no compile-time computation reads keep their usual lazy runtime initialization. A package value read at compile time must have one of the data types supported for results below.

The result can be made of numbers, strings, Bools, runes, lists, tuples, records, options and other sealed or union values, and maps. `Bytes`, functions, and resources cannot be stored.

Tuples can combine several computed results in one value:

```bork
fn main() {
  summary = comptime { (range(1, 6).fold(0, (sum, n) => sum + n), "ready") }
  println(summary.0, summary.1)
}
```

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

```text
comptime failed: panic: bad port: 80a
```

## Reading files at build time

The `bork/build` package reads a file during compilation. Together with `comptime`, this parses and validates a configuration file once, at build time:

Use `build.ReadString("config.json")` inside `comptime`, decode it with
`json.Decode[Config]`, and panic if decoding fails. The
[comptime example](../../examples/comptime/README.md) contains the complete
program and its configuration file, with facts on the decoded record.

The executable carries the decoded `Config`. It does not read or parse `config.json` when it runs, and a malformed file stops the build.

- `build.ReadString(path)` returns the file as a `String`, and `build.ReadBytes(path)` as a `List[Byte]`.
- ReadBytes returns a list of bytes rather than `Bytes`, because `List[Byte]` can be stored as compile-time data.
- The path must be a constant. It is relative to the module root, which is the directory holding `bork.mod`, or to the source directory for a program without one. It cannot point outside that directory.
- These functions can only be called at compile time.

Multiple blocks share the compiler's evaluation program to reduce build time. Each block still runs after its dependencies have passed their checks, with its own time and result limits.

Derivation templates also use `comptime if`, `comptime for`, and `comptime match`
to select or repeat typed source code. See [derivation templates](derivation.md).

## Limits

An evaluation may take at most ten seconds and produce at most 16 MiB of data. A file read at build time may be at most 16 MiB.

To include files as they are, without processing them, [bork/embed](../std/embed.md) is simpler.

---

Previous: [Libraries](libraries.md) · Next: [Typed interpolation](interpolators.md) · [All pages](../README.md#the-language)
