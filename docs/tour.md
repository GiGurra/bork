# A tour of bork

This tour builds a small program that reads a file of expenses and adds them up. Each step introduces one part of the language. Every code block is a complete file that you can run.

## Install

bork compiles through Go, so install [Go](https://go.dev/dl/) first. Then:

```sh
go install github.com/GiGurra/bork/cmd/bork@latest
bork version
```

## A first program

A program is a file, or a directory of files, with a `main` function. Save this as `main.bork` in an empty directory:

```bork
fn main() {
  println("Hello from bork!")
}
```

```sh
bork run .
```

`bork run` compiles and runs it. `bork build` leaves an executable instead.

## Values and functions

```bork
fn double(n: Int): Int {
  n * 2
}

fn size(n: Int): String {
  if (n > 100) { "big" } else { "small" }
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
- `prices = ...` names a value. A name keeps its value: it cannot be assigned again or reused for something else.
- Lists have methods such as `map`, `filter`, and `fold`. `n => n * 2` is a small function written in place.
- `s"..."` inserts values into a string, with `$name` or `${expression}`.

## Your own types

A record groups named fields. A function that can fail says so in its result type, with `|` between the outcomes.

```bork
type Expense = { label: String, cents: Int }
type BadLine = { line: String, reason: String }

fn parseExpense(line: String): Expense | BadLine {
  match (line.fields()) {
    [label, amount] => match (parseInt(amount)) {
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

`line.fields()` splits the line at spaces. The outer `match` looks at the shape of that list: exactly two items, or anything else (`_`). The inner one looks at what `parseInt` returned, which is `Int | ParseError`.

A `match` must cover every case. Remove the `ParseError` arm and the compiler says `match is not exhaustive: missing ParseError`. That is how bork handles failure: there are no exceptions and no null, only values that you have to look at.

## Facts

An expense of zero or less makes no sense. A predicate names that rule, and `where` attaches it to the field:

```bork
pred positive(x: Int) { x > 0 }

type Expense = { label: String, cents: Int where positive }
type BadLine = { line: String, reason: String }

fn checked(label: String, n: Int): Expense | BadLine {
  if (positive(n)) {
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

The check happens once, where the data comes in. Every function that later receives an `Expense` knows the amount is positive without checking again.

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
  match (readLines("expenses.txt")) {
    lines: List[String] => println(s"${lines.length()} lines")
    e: fs.Error => eprintln(fs.ErrorInfo(e).message)
  }
}
```

Three new things appear here:

- **`uses io`** says that `readLines` touches the outside world. A function without `uses` is pure: it cannot print, read files, or call anything that does. `main` may do anything.
- **`scope s { ... }`** owns the file. `fs.Open` takes the scope, and the file is closed when the scope ends, on every path out of it.
- **`?`** keeps the successful value and returns the failure to the caller. `fs.Open(path, s)?` gives the file, or makes `readLines` return the `fs.Error`.

`import "bork/fs"` brings in a [standard package](std/README.md). Its functions are called with the package name in front.

## The whole program

```bork
import "bork/fs"
import "bork/process"

pred positive(x: Int) { x > 0 }

type Expense = { label: String, cents: Int where positive }
type BadLine = { line: String, reason: String }

fn parseExpense(line: String): Expense | BadLine {
  match (line.fields()) {
    [label, amount] => match (parseInt(amount)) {
      n: Int => if (positive(n)) {
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
  expenses = results.flatMap(r => match (r) {
    e: Expense => [e]
    b: BadLine => []
  })
  results.forEach(r => match (r) {
    e: Expense => println(s"${e.label}: ${e.cents}")
    b: BadLine => eprintln(s"skipped \"${b.line}\": ${b.reason}")
  })
  println(s"total: ${total(expenses)}")
}

fn main() {
  match (process.Args()) {
    [path] => match (report(path)) {
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

`report` returns `Ok | fs.Error`: it either finishes, or fails with the file error. `Ok` is the success value of a function that has nothing else to return.

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

- The [examples](examples.md) are complete programs, each with its expected output.
- The [standard packages](std/README.md) cover files, HTTP, JSON, SQL, and more.
