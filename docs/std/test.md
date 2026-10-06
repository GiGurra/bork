# bork/test

`bork/test` checks types and facts in tests and replaces typed positions in tuples.

```bork
import "bork/test"

pred positive(n: Int) { n > 0 }
fn number(): Int | String { 3 }

fn main() {
  n = test.AssertIs[Int where positive](number())
  println(n)
}

test "a positive number" {
  n = test.AssertIs[Int where positive](number())
  assertEqual(n, 3)
}
```

```text
3
```

Run the test with `bork test`. The assertion evaluates its input once, then checks
its runtime type and the target's facts. Failure is an ordinary test panic.

## API

These helpers are pure direct-call compiler intrinsics:

| Signature | Meaning |
| --- | --- |
| `AssertIs[T](value: A): T` | Explicit target T; infer input type A independently, then check type and facts. |
| `Swap(tuple: T, replacement: R): T` | Replace the unique tuple element with exactly R's checked type. |
| `SwapAt(tuple: T, index: Int, replacement: R): T` | Replace a position selected by a pure compile-time index. |

`T`, `A`, and `R` represent checked types, not names to import. AssertIs takes
exactly one explicit type argument; Swap and SwapAt infer all their types and
reject explicit type arguments. Call each helper directly with positional
arguments.

## Check facts at runtime

```bork
import "bork/test"

pred positive(n: Int) { n > 0 }

test "reject a negative number" {
  _ = test.AssertIs[Int where positive](-1)
}
```

Its failure diagnostic contains:

```text
expected Int where positive, got -1 (Int)
```

The diagnostic identifies the expected type and predicates,
actual value and type, and source location. Predicates run after the type check
in source order, with ordinary short-circuiting; their panics propagate. The
returned value carries the checked facts; the input binding gains no narrowing
or facts. Relational predicate arguments retain their identities.

Runtime checks cannot recover erased generic union members or effect annotations.
A target containing type parameters must already accept the input statically.
Generic inputs support concrete targets whose representations are distinguishable.
Failure messages use concrete bork names when runtime information retains them,
and identify erased annotations otherwise.

For arbitrary shapes use `assert(value is Pattern)`. See
[pattern tests](../language/matching.md#testing-a-pattern) and
[typed assertions](../language/testing.md#typed-assertions).

## Replace a tuple element

```bork
import "bork/test"

fn real(): Int { 1 }
fn fake(): Int { 2 }

fn main() {
  functions = (real, "label")
  replaced = test.Swap(functions, fake)
  selected = test.SwapAt(functions, 0, fake)
  println(replaced.0 (), selected.0 ())
  println(functions.0 ())
}
```

```text
2 2
1
```

The original tuple stays unchanged. Both helpers evaluate the tuple and then the
replacement once and never invoke stored functions. Swap requires a unique
identical checked type; missing or repeated matches are compile errors. Repeated
matches report indices and suggest SwapAt.

SwapAt checks the replacement against the selected position and requires an
exact type match. Function types include parameters, result/failure unions and
effects. To change an element's type or function signature, build a new tuple.

```bork fails
import "bork/test"

fn main() {
  println(test.SwapAt((1, "label"), 0, "wrong type"))
}
```

```text
test.SwapAt slot 0 requires exact type Int, found String
```

A tuple of functions can be passed to `assemble`, `assembleAll`, or
`assembleRecord`; assembly splices one level in positional order. See
[packages](../language/packages.md).

Run `bork doc bork/test` for the generated reference.

[All standard packages](README.md)
