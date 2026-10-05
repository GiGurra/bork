# bork/test

`test.AssertIs[T](value)` checks a value against an explicit target type and
returns it with the target's facts proven. The input type is inferred
independently. A failed check reports the expected type and predicates, the
actual value and type, and its source location through the ordinary test panic.

```bork
import "bork/test"

pred positive(n: Int) { n > 0 }
fn number(): Int | String { 3 }

test "a positive number" {
  n = test.AssertIs[Int where positive](number())
  assertEqual(n, 3)
}
```

The input is evaluated once. Predicates run after the type check, in source
order with ordinary short-circuiting, and their panics propagate. The returned
value carries the checked facts; the input binding gains no facts or narrowing.
Relational predicate arguments retain their identities. Call this helper
directly with one type argument and one value argument.

Runtime tests cannot recover erased generic union members or effect
annotations. A target containing type parameters must already accept the input
statically. Generic inputs support concrete targets whose representations are
distinguishable. Generic failure messages use concrete bork names where the
runtime retains them, and explicitly identify erased annotations otherwise.

For arbitrary shapes, use `assert(value is Pattern)`. See
[pattern tests](../language/matching.md#testing-a-pattern) and
[typed assertions](../language/testing.md#typed-assertions).
