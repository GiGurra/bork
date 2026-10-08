# Testing

Tests live in the package they test and run with `bork test`. Put them beside
the implementation in the same `.bork` file, or in a separate `_test.bork`
file such as `slug_test.bork`. Files in a package share declarations, so tests
can call private functions. Test bodies are left out of the built program.

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

## Test every package

`bork test .` runs tests declared in the current package. Importing another
package does not run its tests. There is no recursive test flag: list your
packages explicitly in CI, including library packages without `main`.

For a module with tests in the root, `money` and `api` packages:

```sh
set -eu
bork deps download
for package in . ./money ./api; do
  bork check "$package"
  bork fmt --check "$package"
  bork test --hermetic "$package"
done
```

A package without tests or rules makes `bork test` fail; check and format it,
but leave it out of the test list. Keep this list current when adding packages.
Use `--filter "exact test name"` to run one test, `--parallel 8` to run tests
concurrently, and `--json` for machine-readable results. See [test commands](../cli.md#test).

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

### Mock handle methods

`A` is the generated call-record type. Fields use the target's parameter names,
but omit parameters that can carry scope lifetimes and parameters whose types
mention the target's type parameters. For `http.Get`, the record has `url` and
`timeoutMs`, but no `s`. Inspect omitted arguments inside the mock body.
Matchers have type `(A) uses nothing => Bool`.

| Method | Effects | Meaning |
| --- | --- | --- |
| `count(): Int` | `state` | Calls answered so far |
| `args(): List[A]` | `state` | Argument records in call-start order |
| `calls(): List[String]` | `state` | Formatted calls in call-start order |
| `expect(times: Option[Int] = .None, atLeast: Option[Int] = .None, atMost: Option[Int] = .None)` | `state` | Check the count when the mock's block ends |
| `expectWhere(matches, times: Option[Int] = .None, atLeast: Option[Int] = .None, atMost: Option[Int] = .None)` | `state` | Check the count of matching calls at block exit |
| `waitFor(calls: Int, ms: Int = 5000)` | `state + clock` | Wait for a count, failing on timeout |
| `waitForWhere(matches, calls: Int, ms: Int = 5000)` | `state + clock` | Wait for a matching count, failing on timeout |

Give `times` for an exact count, or `atLeast`/`atMost` for bounds; do not mix
those forms. With no bounds, `expect()` requires at least one call.
`times: 0` forbids calls. Counts and timeouts must be nonnegative.
Expectations must be declared while the mock is active. `waitFor` observes
calls starting; it does not join a task or wait for the mocked body to finish.

A mock belongs to its test, and is seen by the tasks that test starts. Tests running in parallel do not see each other's mocks. Built programs contain no trace of them.

### Mock an HTTP request

```bork
import "bork/http"

fn healthy(url: String, s: Scope) uses net + clock + state: Bool {
  match (http.Get(url, s)) {
    response: http.Response => response.status == 200
    _ => false
  }
}

test "health check uses the requested URL" {
  gets = mock http.Get(url, s, timeoutMs, maxBodyBytes) { http.Text(200, "ok") }
  gets.expect(times: 1)
  gets.expectWhere(call => call.url == "https://service.example/health", times: 1)
  scope s {
    assert(healthy("https://service.example/health", s))
  }
}
```

The [mocking example](../../examples/mocking/main.bork) also checks concurrent
requests, nested mocks and calls that pass through to the real function.
Calling a mocked function's name inside its own mock body calls the function
that was in force before that mock.

### Unmet expectations

This test compiles but fails when its mock ends:

```bork
fn fetch(url: String) uses io: String {
  println(url)
  url
}

test "two fetches" {
  requests = mock fetch(url) { "ok" }
  requests.expect(times: 2)
  assertEqual(fetch("a"), "ok")
}
```

```text
FAIL  two fetches
      main.bork:8:3: the mock of fetch expected exactly 2 calls, but answered 1:
      fetch("a")
0 passed, 1 failed
```

### Hermetic tests

`bork test --hermetic` rejects tests that can reach the network without a mock,
before those tests execute. It checks reachable calls, including class methods
and callbacks. It does not block disk I/O. Put mocks at the start of the test
when tasks or callbacks need them; a mock declared later covers only calls
written after it in its block.

The [hermetic fixture](../../testdata/hermetic/main.bork) includes this failure
from a network call before its mock. From that fixture directory, run `bork test --hermetic --filter "a call before the mock" .`
(the excerpt below omits the final count):

```text
FAIL  a call before the mock
      not hermetic (bork test --hermetic): it can reach the network with no mock in force:
      main.bork:41:11: page -> Get; mock Get
      A mock covers a call written in its block after it; a call in a lambda, or a function passed as a value, may run elsewhere, and only mocks at the start of the test cover it.
```

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

### Read a failure and replay it

This intentionally false property compiles, then fails after shrinking:

```bork
pred positive(n: Int) { n > 0 }

test "short lists" (xs: List[Int where positive]) {
  assert(xs.length() < 3)
}
```

Save it as `main.bork` and run `bork test --seed 42 .`:

```text
FAIL  short lists
      main.bork:4:3: assertion failed
      for xs = [1, 1, 1] (shrunk from xs = [7, 4, 6])
      case 9, seed 42 (bork test --seed 42)
0 passed, 1 failed
```

Run the seed command in the report against the same sources to replay the
failure. The `for` line shows the smaller failing input, and `shrunk from`
shows the original generated input. Seeds reproduce the run, rather than
injecting the displayed values. A custom `--cases` value also appears in the
replay command.

Some parameter types have no generator. For example, this compiles but is
skipped rather than counted as a successful property:

```bork
test "callback property" (f: (Int) => Int) {
  assert(f(1) == 1)
}
```

```text
skip  callback property (no values are generated for (Int) => Int)
0 passed, 0 failed, 1 skipped
```

If generation cannot find enough values satisfying the facts, it reports a
failure instead. Simplify the constraint, use explicit test values, or test a
construction that produces valid values. Skipped properties do not prove the
assertion.

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
