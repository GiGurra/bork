# Testing

Tests are written next to the code they test, in the same files, and run with `bork test`. They are left out of the built program.

## Tests

```bork
fn slug(title: String): String {
  title.trim().toLower().fields().join("-")
}

test "a slug is lower case with dashes" {
  assertEqual(slug("  Hello Big World "), "hello-big-world")
}

test "an empty title gives an empty slug" {
  assert(slug("").byteLength() == 0)
}
```

```sh
$ bork test .
ok    a slug is lower case with dashes
ok    an empty title gives an empty slug
2 passed, 0 failed
```

- `assertEqual(actual, expected)` fails the test and shows both values when they differ.
- `assert(condition)` fails when the condition is false.
- A panic fails the test too.

A test body may use any run-time [effect](effects.md) without declaring it, like `main`.

## Typed assertions

`assert(value is Pattern)` checks any pattern without extracting values. Import
`bork/test` and call `test.AssertIs[T](value)` when you need the checked value:

```bork
import "bork/test"

pred positive(n: Int) { n > 0 }
pred atMost(n: Int, limit: Int) { n <= limit }
fn render(text: Bool): Int | String { if (text) { "hello" } else { 3 } }
fn needsPositive(n: Int where positive): Int { n }

test "a rendered number carries checked facts" {
  n = test.AssertIs[Int where positive and atMost(100)](render(false))
  assertEqual(needsPositive(n), 3)
  assert(render(true) is String)
}
```

Give one explicit target type; the input is inferred independently. The helper
checks the erased type first, then its predicates, and returns the value with
those facts proven. Relational facts retain the identities of their arguments.
An unsuccessful check fails the test with the expected type and predicates,
actual value, actual bork type and source location. Predicate panics propagate.
The input is evaluated once. The original binding gains no facts or narrowing.
Runtime tests reject targets that depend on erased generic unions or effects;
see [bork/test](../std/test.md) for generic restrictions and failure reporting.

## Snapshots

For large values, writing the expected result by hand is tedious. `assertSnapshot(value)` compares the printed value with a file saved earlier:

```bork
type Invoice = { customer: String, lines: List[String], total: Int }

fn invoice(): Invoice {
  Invoice { customer: "Ada", lines: ["tea", "milk"], total: 700 }
}

test "renders an invoice" {
  assertSnapshot(invoice())
}
```

The first run fails because there is no snapshot yet. `bork test --update` writes it to a `snapshots` directory next to the source, here as `renders_an_invoice.snap`. Later runs fail with a line-by-line diff if the value changes. Review the snapshot files and commit them like any other code.

## Mocks

A test can replace a function that has effects, so that it never touches the real network, clock, or disk. Nothing in the code under test has to change.

```bork
import "bork/time"

fn notify(message: String) uses io {
  println(message)
}

fn remind(deadline: time.Instant) uses io + clock {
  remaining = (deadline.unixNanos - time.Now().unixNanos) / 1_000_000_000
  notify(s"$remaining seconds left")
}

test "the reminder says how long is left" {
  mock time.Now() { time.Instant { unixNanos: 0 } }
  sent = mock notify(message) {}
  remind(time.Instant { unixNanos: 30_000_000_000 })
  assertEqual(sent.count(), 1)
  assertEqual(sent.args().map(call => call.message), ["30 seconds left"])
}
```

- `mock notify(message) { ... }` replaces `notify` from that line to the end of the block. The parameters are listed by name, and the body is checked against the real function's signature.
- A mock can target a function in the test's package, an exported function of an imported package such as `time.Now`, or a method.
- Only functions that declare effects can be mocked. Pure functions have nothing to fake.
- Binding the mock to a name gives a handle that records the calls: `count()`, `args()`, and `calls()`. `expect(times: 1)` states how many calls must happen, checked when the mock ends.

A mock belongs to its test, and is seen by the tasks that test starts. Tests running in parallel do not see each other's mocks. Built programs contain no trace of them.

`bork test --hermetic` goes a step further and fails any test that could reach the network without a mock in place.

The [mocking example](../../examples/mocking/main.bork) tests an HTTP health checker this way.

## Property tests

A test with parameters is a property test. It runs many times on generated values, looking for a case that fails.

```bork
pred positive(x: Int) { x > 0 }

fn half(n: Int): Int {
  n / 2
}

test "half is never larger" (n: Int where positive) {
  assert(half(n) < n)
}

test "reversing twice changes nothing" (xs: List[Int]) {
  assertEqual(xs.reverse().reverse(), xs)
}
```

- Generated values respect the `where` clauses on the parameters.
- Edge cases are mixed in often: zero, one, the largest and smallest numbers, empty strings and lists, and the constants that the `where` clauses mention.
- When a case fails, it is shrunk to a simpler one that still fails, and reported with its values and a seed. `bork test --seed N` repeats the run.
- Each property runs 100 cases. `bork test --cases N` changes that.

## Tests check what was trusted

Under `bork test`, the things the compiler took on trust are verified as the code runs:

- Each [`trust`](facts.md#trust) statement is evaluated, and fails the test if it does not hold.
- Each [`rule`](facts.md#rules) is run as a property test of its own.
- A function implemented in Go that promises a fact about its result is checked against the promise.

`bork test --auto-properties` also calls every function that relies on trust with generated arguments.

## Development helpers

Two helpers are for work in progress. `bork check` warns about both, so they do not get left in.

```bork
fn price(quantity: Int, each: Int): Int {
  dbg(quantity * each)
}

fn discount(code: String): Int {
  todo("look the code up")
}

fn main() {
  println(price(3, 250))
}
```

- `dbg(expr)` prints the expression, its value, and its location to standard error, then returns the value. It can wrap any part of an expression, even in a pure function.
- `todo()` stands in for code you have not written. It has whatever type is needed, and panics with its location if it is reached.

---

Previous: [Calling Go](go-interop.md) · [All pages](../README.md#the-language)
