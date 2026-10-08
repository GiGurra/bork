# Cookbook

These recipes are complete single-file programs. Save a block as `recipe.bork`, then run `bork run recipe.bork`; use `bork test recipe.bork` for its tests. The file recipes create temporary fixtures and clean them up automatically. Commands with arguments, stdin, external executables or network access state those requirements beside the code.

For a complete HTTP and database application, follow the [service tour](tour-service.md). The [language pages](README.md#the-language) and [standard packages](std/README.md) explain the APIs in depth.

## Exit on an error

Use an explicit match in `main`; `?` belongs in a function that returns the failure. Successful completion exits with 0, usage errors with 2, and this operational failure with 1. Run with `bork run recipe.bork -- 42`.

```bork
import "bork/process"
fn main() {
  match (process.Args()) {
    [text] => match (parseInt(text)) {
      n: Int => println(n)
      error: ParseError => {
        eprintln(error.message)
        process.Exit(1)
      }
    }
    _ => { eprintln("usage: recipe NUMBER"); process.Exit(2) }
  }
}
```

## Read standard input

On Linux or macOS, use the installed `cat` command to read inherited stdin. Save as `recipe.bork` and run `printf "hello\n" | bork run recipe.bork`. The child output is captured; `.Inherit` passes the parent stdin through without buffering it in bork.

```bork
import "bork/process"
fn main() {
  scope s {
    match (process.Run(s, "cat", stdin: .Inherit)) {
      result: process.Result => println(result.StdoutText().trim())
      error => eprintln(toString(error))
    }
  }
}
```

## Repeat N times

`range(start, end)` excludes the end. A loop variable is local to the loop.

```bork
fn main() {
  for (i in range(0, 3)) { println(s"attempt ${i + 1}") }
}
```

## Carry a value through a loop

Rebind an existing name inside the loop to carry its new value to the next iteration and out of the loop. Use `fold` for a compact collection reduction.

```bork
fn main() {
  total = 0
  for (n in [2, 3, 5]) { total = total + n }
  println(total)
}
```

## Parse a number

Parsing returns a union. Handle both outcomes before using the number.

```bork
fn main() {
  match (parseInt("123")) {
    value: Int => println(value + 1)
    error: ParseError => eprintln(error.message)
  }
}
```

## Validate at the boundary

A guard proves a fact; later functions can require it in their parameter type.

```bork
pred positive(n: Int) { n > 0 }
fn double(n: Int where positive): Int { n * 2 }
fn main() {
  n = 12
  if (positive(n)) { println(double(n)) } else { eprintln("must be positive") }
}
```

## Collect successful results

Use `flatMap` to keep the success variants and discard failures deliberately.

```bork
fn main() {
  numbers = ["10", "bad", "20"].flatMap(text => match (parseInt(text)) {
    n: Int => [n]
    _: ParseError => []
  })
  println(numbers)
}
```

## Group values in a map

Maps are immutable. `put` returns the updated map; an absent key is `Option.None`.

```bork
fn main() {
  counts: Map[String, Int] = {:}
  for (word in ["red", "blue", "red"]) {
    n = match (counts.get(word)) { Option.Some(value) => value, Option.None => 0 }
    counts = counts.put(word, n + 1)
  }
  println(counts.sorted())
}
```

## Write and read a file

A temporary directory provides its own fixture and disappears with the scope, including its contents. `?` propagates each file error.

```bork
import "bork/encoding"
import "bork/fs"
fn roundTrip(s: Scope) uses io: String | fs.Error {
  directory = fs.TempDir(s)?
  path = fs.Join([fs.DirectoryPath(directory), "note.txt"])
  fs.Write(path, encoding.Utf8("hello"))?
  fs.ReadAllText(fs.Open(path, s)?)
}
fn main() { scope s { println(roundTrip(s)) } }
```

## Read a file line by line

`ForEachLine` keeps processing inside the file scope. This example creates its input, so no working-directory fixture is needed.

```bork
import "bork/fs"
fn lines(s: Scope) uses io: Ok | fs.Error {
  file = fs.TempFile(s)?
  _ = fs.WriteText(file, "red\nblue\n")?
  reader = fs.Open(fs.Path(file), s)?
  fs.ForEachLine(reader, line => println(line))
}
fn main() { scope s { println(lines(s)) } }
```

## Encode and decode JSON

Derive the codec classes, then select their instances with `use codec.Defaults`. Syntax errors and decoded field errors are separate result types.

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults
type Person = { name: String, age: Int } derive (codec.Encode, codec.Decode)
fn main() {
  text = match (json.Encode(Person { name: "Ada", age: 37 })) { text: String => text; error: json.JsonError => { eprintln(error.message); return } }
  println(text)
  decoded: Person | json.JsonError | codec.DecodeError = json.Decode(text)
  println(decoded)
}
```

## Load environment configuration

Run with `APP_PORT=9000 bork run recipe.bork`. An absent variable uses its field default; malformed values and failed facts produce `env.ConfigError`.

```bork
import "bork/codec"
import "bork/env"
use codec.Defaults
pred port(n: Int) { n > 0 && n <= 65535 }
type Config = { port: Int where port = 8080 } derive (codec.Decode)
fn main() {
  match (env.Load[Config]("APP")) {
    config: Config => println(config.port)
    error: env.ConfigError => eprintln(toString(error))
  }
}
```

## Test configuration without changing the environment

`LoadWith` takes a pure lookup function. Tests do not need to mutate process-global environment variables.

```bork
import "bork/codec"
import "bork/env"
use codec.Defaults
type Config = { host: String } derive (codec.Decode)
fn main() { println("run with bork test recipe.bork") }
test "loads a supplied host" {
  loaded = env.LoadWith[Config]("APP", key => if (key == "APP_HOST") { Option.Some("localhost") } else { Option.None })
  assertEqual(loaded, Config { host: "localhost" })
}
```

## Run a subprocess

On Linux or macOS, this invokes `printf` directly, without a shell. Captured output and the child exit code are separate from startup and cancellation failures.

```bork
import "bork/process"
fn main() {
  scope s {
    match (process.Run(s, "printf", ["hello\n"])) {
      result: process.Result => {
        println(result.StdoutText().trim())
        println(result.code)
      }
      error => eprintln(toString(error))
    }
  }
}
```

## Sleep cooperatively

`delay` takes milliseconds and returns `Ok | Cancelled`. A cancelled scope interrupts the wait.

```bork
fn nap(s: Scope) uses clock + state: Ok | Cancelled { delay(s, 10.millis())? }
fn main() { scope s { println(nap(s)) } }
```

## Give work a timeout

`withTimeout` supplies a child scope; pass that scope to every cancellable operation. The parent remains usable.

```bork
fn answer(child: Scope) uses clock + state: Int | Cancelled {
  delay(child, 1000.millis())?
  42
}
fn main() {
  scope app { println(withTimeout(app, 5.millis(), answer)) }
}
```

## Map in parallel

A pure parallel map preserves result order. For cancellable work, use the variant that takes an explicit scope and a worker limit.

```bork
fn square(s: Scope, n: Int) uses clock + state: Int | Cancelled {
  delay(s, 1.millis())?
  n * n
}
fn main() {
  scope s { println([1, 2, 3].parMapUntilIn[Int, Cancelled](s, square, workers: 2)) }
}
```

## Await tasks before leaving a scope

Await inside the scope if you want the task result. Scope exit cancels outstanding tasks before joining them.

```bork
fn main() {
  scope s {
    tasks = [1, 2, 3].map(n => fork(s, () => n * n))
    println(tasks.awaitAll())
  }
}
```

## Retry an HTTP request with a shared budget

Replay only operations that your application considers safe. This GET uses one budget for all retries, at most three attempts, and a caller deadline. It requires network access.

```bork
import "bork/http"
fn main() {
  scope s {
    cancelAfter(s, 1000.millis())
    budget = http.OpenRetryBudget(s, capacity: 2, refill: 1000.millis())
    println(http.Retry(s, budget, child => http.Get("https://example.com", child), maxAttempts: 3))
  }
}
```

## Build a small CLI

Save as `recipe.bork`; run with `bork run recipe.bork -- --name Ada`, or add `--help`. For larger command-line tasks, see the [CLI cookbook](std/cli-cookbook.md). The CLI validates the derived record before calling the handler. Format an error with `toString` before passing it to `eprintln`.

```bork
import "bork/cli"
import "bork/codec"
import "bork/process"
use codec.Defaults
type Options = { name: String } derive (codec.Decode)
fn main() {
  match (cli.Run[Options]("greet", "Print a greeting", (options, s) => {
    println(s"Hello, ${options.name}!")
  })) {
    Ok => {}
    error: cli.Error => { eprintln(toString(error)); process.Exit(2) }
  }
}
```

## Mock an HTTP dependency

Run with `bork test recipe.bork`. The mock replaces the real function within this test, including calls in tasks. Its expectation verifies that the call happened once.

```bork
import "bork/http"
fn healthy(s: Scope) uses net + clock + state: Bool {
  match (http.Get("https://upstream.example/health", s)) {
    response: http.Response => response.status == 200
    _ => false
  }
}
fn main() { println("run with bork test recipe.bork") }
test "an upstream is healthy" {
  calls = mock http.Get(url, s, timeout, maxBodyBytes) { http.Text(200, "ok") }
  calls.expect(times: 1)
  scope s { assert(healthy(s)) }
}
```

## Format an instant

Use a fixed instant in tests and examples; `time.Now()` reads the system clock and therefore needs the `clock` effect.

```bork
import "bork/time"
fn main() {
  epoch = time.Instant { unixNanos: 0 }
  println(time.Format(epoch, time.RFC3339()))
}
```

## Change shared state atomically

An atom protects shared updates. The callback to `update` is pure and may run more than once, so perform I/O after the update.

```bork
fn main() {
  counter = atom(0)
  scope s {
    tasks = range(0, 10).map(n => fork(s, () => { _ = update(counter, value => value + 1) }))
    for (task in tasks) { await(task) }
  }
  println(current(counter))
}
```

## Pass values through a channel

The producer closes the channel after sending. Await it inside the scope so scope exit does not cancel the remaining sends.

```bork
fn main() {
  scope s {
    values = channel[Int](s, 2)
    producer = fork(s, () => {
      _ = values.send(s, 7)
      _ = values.send(s, 9)
      values.close()
    })
    for (value in values.values(s)) { println(value) }
    await(producer)
  }
}
```

## Test a pure function

Tests live in the same file or in `_test.bork` files beside it. `bork test recipe.bork` runs these assertions; the normal executable contains only the program.

```bork
fn double(n: Int): Int { n * 2 }
fn main() { println(double(21)) }
test "doubles positive and zero values" {
  assertEqual(double(21), 42)
  assertEqual(double(0), 0)
}
```
