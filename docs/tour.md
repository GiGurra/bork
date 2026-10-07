# A tour of bork

This tour builds a small program that reads a file of expenses and adds them up. Each step introduces one part of the language. Every block of bork code is a complete file that you can run, except the one marked as not compiling.

## Install

bork compiles through Go. Install [Go](https://go.dev/dl/) 1.21 or later with automatic toolchain switching enabled (the default); Go downloads the required 1.26 or newer toolchain when needed. With `GOTOOLCHAIN=local`, install Go 1.26 or newer yourself. Then:

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
bork version
```

Homebrew, release archives, and upgrades are covered in [installing bork](install.md).

## A first program

Create a project, then run its program and first test:

```sh
bork new expenses
cd expenses
bork run .
bork test .
```

The project includes `bork.mod`, `main.bork`, `main_test.bork`, `.gitignore`, and a short README. Other starting points are `bork new --template cli greeter`, `bork new --template http service`, and `bork new --template lib library`. See [creating projects](cli.md#new) for module names and templates.

A program is a file, or a directory of files, with a `main` function. Delete the generated `main_test.bork` (it tests the generated `greeting` function), then replace `main.bork` with:

```bork
fn main() {
  println("Hello from bork!")
}
```

```sh
bork run .
```

`bork run` compiles and runs it. `bork build` leaves an executable instead. Both take a directory, as here, or a single file such as `bork run main.bork`.

## Values and functions

```bork
fn double(n: Int): Int {
  n * 2
}

fn size(n: Int): String {
  if n > 100 { "big" } else { "small" }
}

fn main() {
  prices = [450, 1250, 80]
  doubled = prices.map(double)
  total = doubled.fold(0, (sum, n) => sum + n)
  println(s"total: $total, which is ${size(total)}")
}
```

A few things to notice:

- A function's value is the last expression in its body. There is a `return`, but it is rarely needed.
- `if` is an expression, so it gives a value.
- `prices = ...` names an immutable value. A later binding in the same block may reuse the name and refer to its previous value; earlier values do not change. Nested blocks cannot shadow an outer name. See [names and values](language/basics.md#names-and-values).
- Lists have methods such as `map` and `fold`. `map` is given the function `double` by name. `(sum, n) => sum + n` is a small function written in place, called a lambda.
- `s"..."` inserts values into a string, with `$name` or `${expression}`.

Tuples group values by position. Each position keeps its own type; use `.0`,
`.1`, and so on, or unpack the tuple into names:

```bork
fn label(): (Int, String) { (3, "items") }

fn main() {
  pair = label()
  (count, text) = pair
  println(count, text, pair.0)
}
```

## Your own types

Records and sealed variants can also carry [typed package tags](language/types.md#typed-package-tags), such as `codec { name: "login" }` for a field’s wire name. These configure derivation while keeping the program’s source names.

A record groups named fields. A function that can fail says so in its result type, with `|` between the outcomes: `parseExpense` below returns an `Expense` or a `BadLine`.

To find out which one it got, a caller uses `match`. Each arm has a pattern on the left of `=>` and a result on the right, and the first pattern that fits is taken.

```bork
type Expense = { label: String, cents: Int }
type BadLine = { line: String, reason: String }

fn parseExpense(line: String): Expense | BadLine {
  match line.fields() {
    [label, amount] => match parseInt(amount) {
      n: Int => Expense { label: label, cents: n }
      e: ParseError => BadLine { line: line, reason: e.message }
    }
    _ => BadLine { line: line, reason: "expected a label and an amount" }
  }
}

fn main() {
  println(parseExpense("coffee 450"))
  println(parseExpense("coffee"))
}
```

```text
Expense { label: "coffee", cents: 450 }
BadLine { line: "coffee", reason: "expected a label and an amount" }
```

- `line.fields()` splits the line at spaces and gives a list.
- The outer `match` looks at the shape of that list. `[label, amount]` fits a list of exactly two items and names them. `_` fits anything else.
- The built-in `parseInt` returns `Int | ParseError`. The inner `match` looks at which it is: `n: Int` fits an `Int` and names it `n`.

A `match` must cover every case. Remove the `ParseError` arm and the compiler says `match is not exhaustive: missing ParseError`. That is how bork handles failure: there are no exceptions and no null, only values that you have to look at.

## Facts

An expense of zero or less makes no sense. A predicate, declared with `pred`, names that rule, and `where` attaches it to the field. From then on the compiler tracks where the rule is known to hold. Such a piece of knowledge is called a fact.

```bork
pred positive(x: Int) { x > 0 }

type Expense = { label: String, cents: Int where positive }
type BadLine = { line: String, reason: String }

fn checked(label: String, n: Int): Expense | BadLine {
  if positive(n) {
    Expense { label: label, cents: n }
  } else {
    BadLine { line: label, reason: "the amount must be positive" }
  }
}

fn main() {
  println(checked("coffee", 450))
  println(checked("tea", 0))
}
```

Now an `Expense` can only be built where the compiler can see that `cents` is positive. Inside the `if`, it can. Without the `if`, the program does not compile:

```bork fails
pred positive(x: Int) { x > 0 }

type Expense = { label: String, cents: Int where positive }

fn unchecked(label: String, n: Int): Expense {
  Expense { label: label, cents: n }
}
```

```text
Expense requires cents to be positive, but that is not proven for n
```

The check happens once, where the data comes in. Every function that later receives an `Expense` knows the amount is positive without checking again. [Facts](language/facts.md) covers this in full.

## Files and effects

Reading a file is an effect, and a function that has effects declares them with `uses`:

```bork
import "bork/fs"

fn readLines(path: String) uses io: List[String] | fs.Error {
  scope s {
    fs.ReadAllText(fs.Open(path, s)?)?.lines()
  }
}

fn main() {
  match readLines("expenses.txt") {
    lines: List[String] => println(s"${lines.length()} lines")
    e: fs.Error => eprintln(fs.ErrorInfo(e).message)
  }
}
```

Three new things appear here:

- **`uses io`** says that `readLines` touches the outside world. A function without `uses` is pure: it cannot print, read files, or call anything that does. `main` may do anything.
- **`scope s { ... }`** owns the file. `fs.Open` takes the scope, and the file is closed when the scope ends, on every path out of it. A resource can also be handed over to another scope with `move`, and the compiler then rejects any further use of the original ([Scopes and tasks](language/scopes.md#longer-lived-resources)).
- **`?`** keeps the successful value and returns the failure to the caller. `fs.Open(path, s)?` gives the file, or makes `readLines` return the `fs.Error`.

`import "bork/fs"` brings in a [standard package](std/README.md). Its functions are called with the package name in front.

Run it before `expenses.txt` exists and it prints `no such file or directory`. The next step creates the file.

## The whole program

```bork
import "bork/fs"
import "bork/process"

pred positive(x: Int) { x > 0 }

type Expense = { label: String, cents: Int where positive }
type BadLine = { line: String, reason: String }

fn parseExpense(line: String): Expense | BadLine {
  match line.fields() {
    [label, amount] => match parseInt(amount) {
      n: Int => if positive(n) {
        Expense { label: label, cents: n }
      } else {
        BadLine { line: line, reason: "the amount must be positive" }
      }
      e: ParseError => BadLine { line: line, reason: e.message }
    }
    _ => BadLine { line: line, reason: "expected a label and an amount" }
  }
}

fn total(expenses: List[Expense]): Int {
  expenses.fold(0, (sum, e) => sum + e.cents)
}

fn readLines(path: String) uses io: List[String] | fs.Error {
  scope s {
    fs.ReadAllText(fs.Open(path, s)?)?.lines()
  }
}

fn report(path: String) uses io: Ok | fs.Error {
  results = readLines(path)?.map(parseExpense)
  expenses = results.flatMap(r => match r {
    e: Expense => [e]
    _: BadLine => []
  })
  results.forEach(r => match r {
    e: Expense => println(s"${e.label}: ${e.cents}")
    b: BadLine => eprintln(s"skipped \"${b.line}\": ${b.reason}")
  })
  println(s"total: ${total(expenses)}")
}

fn main() {
  match process.Args() {
    [path] => match report(path) {
      e: fs.Error => eprintln(s"cannot read $path: ${fs.ErrorInfo(e).message}")
      _ => {}
    }
    _ => eprintln("usage: expenses FILE")
  }
}

test "parses a label and an amount" {
  assertEqual(parseExpense("coffee 450"), Expense { label: "coffee", cents: 450 })
}

test "rejects an amount that is not positive" {
  assertEqual(parseExpense("tea 0"), BadLine { line: "tea 0", reason: "the amount must be positive" })
}
```

A few things here are new:

- `report` returns `Ok | fs.Error`: it either finishes, or fails with the file error. `Ok` is the success value of a function that has nothing else to return, and a body that ends without a value gives it.
- `process.Args()` gives the command-line arguments as a list.
- `flatMap` builds a list from the lists its function returns, which here keeps the expenses and drops the bad lines. `forEach` calls a function for each element.
- `eprintln` prints to standard error.
- `_ => {}` is an arm that fits anything and does nothing.

With this `expenses.txt`:

```text
coffee 450
lunch 1250
book x
tea 0
```

the program prints:

```sh
$ bork run . -- expenses.txt
coffee: 450
lunch: 1250
skipped "book x": invalid syntax
skipped "tea 0": the amount must be positive
total: 1700
```

Arguments after `--` go to the program.

## Tests

The two `test` blocks at the end are part of the same file. They are left out of the built program and run with `bork test`:

```sh
$ bork test .
ok    parses a label and an amount
ok    rejects an amount that is not positive
2 passed, 0 failed
```

[Testing](language/testing.md) covers snapshots, mocks, and property tests.

## The everyday commands

```sh
bork run .          # compile and run
bork test .         # run the tests
bork check .        # type-check only, the fastest feedback
bork fmt .          # format the source files
bork build .        # compile to an executable
bork install .      # compile and install the executable
```

See the [command-line reference](cli.md) for all of them.

## Where to go next

- **Next:** [Build a SQLite-backed JSON API](tour-service.md), with configuration, validated requests, logging, tests and shutdown.
- The [language pages](README.md#the-language) explain each area in more depth, starting with [the basics](language/basics.md).
- The [examples](examples.md) are complete programs, each with its expected output.
- The [standard packages](std/README.md) cover files, HTTP, JSON, SQL, and more.
