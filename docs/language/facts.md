# Facts

A fact is something the compiler knows to be true about a value: this number is positive, this list is not empty, this string is a valid email address. Functions can require facts of their arguments, and the compiler makes sure every caller has established them.

The effect is that a value gets checked once, where it enters the program, and everything after that can rely on it.

## Predicates and `where`

A predicate is a function that returns a `Bool`, declared with `pred`. A parameter requires one with `where`.

```bork
pred positive(x: Int) { x > 0 }

fn perItem(total: Int, count: Int where positive): Int {
  total / count
}

fn main() {
  println(perItem(100, 4))
}
```

`perItem` can divide without worrying about zero. The requirement has moved to its callers.

`perItem(100, 4)` compiles because `4` is a constant and the compiler runs `positive(4)` itself. `perItem(100, 0)` is a compile error.

## Proving a fact

When the argument is not a constant, the caller needs to show that the predicate holds. There are a few ways.

**Check it.** Inside an `if` that tests the predicate, the fact is known:

```bork
pred positive(x: Int) { x > 0 }

fn perItem(total: Int, count: Int where positive): Int {
  total / count
}

fn average(total: Int, count: Int): Int {
  if (positive(count)) { perItem(total, count) } else { 0 }
}

fn averageOrZero(total: Int, count: Int): Int {
  if (count <= 0) { return 0 }
  perItem(total, count)
}

fn main() {
  println(average(10, 4), averageOrZero(10, 0))
}
```

The second function shows two more things. Returning early when the predicate fails leaves the fact known for the rest of the function. And the compiler looks inside simple predicates: `count <= 0` being false is enough to know `positive(count)`.

**Require it too.** A function can pass the requirement on to its own callers by declaring the same `where`:

```bork
pred positive(x: Int) { x > 0 }

fn perItem(total: Int, count: Int where positive): Int {
  total / count
}

fn perPair(total: Int, pairs: Int where positive): Int {
  perItem(total, pairs) / 2
}

fn main() {
  println(perPair(100, 5))
}
```

**Without either**, the program does not compile:

```bork fails
pred positive(x: Int) { x > 0 }

fn perItem(total: Int, count: Int where positive): Int {
  total / count
}

fn average(total: Int, count: Int): Int {
  perItem(total, count)
}
```

```text
perItem requires count to be positive, but that is not proven for count
(check it first with if (positive(count)) { ... }, or require it: count: Int where positive)
```

## Types that carry a fact

Writing `Int where positive` everywhere gets long. An alias gives the combination a name:

```bork
pred positive(x: Int) { x > 0 }
pred nonBlank(s: String) { s.trim().byteLength() > 0 }

type Quantity = Int where positive
type Name = String where nonBlank

type Line = { item: Name, quantity: Quantity }

fn lineFor(item: String, quantity: Int): Option[Line] {
  if (nonBlank(item) && positive(quantity)) {
    Line { item: item, quantity: quantity }
  } else {
    Option.None
  }
}

fn main() {
  println(lineFor("tea", 2))
  println(lineFor(" ", 2))
}
```

A `Line` can only be built from a proven name and quantity. Any function that takes a `Line` knows both facts for free, however far it is from where the check happened.

Several predicates combine with `and` and `or`, and a predicate can take extra arguments: `Int where positive and atMost(100)`.

## Functions that promise a fact

A `where` on a result type is a promise. The compiler checks that every path through the function keeps it, and callers get the fact.

```bork
pred positive(x: Int) { x > 0 }

type NotPositive = { value: Int }

fn checked(raw: Int): Int where positive | NotPositive {
  if (raw > 0) { raw } else { NotPositive { value: raw } }
}

fn perItem(total: Int, count: Int where positive): Int {
  total / count
}

fn share(total: Int, rawCount: Int): Int | NotPositive {
  count = checked(rawCount)?
  perItem(total, count)
}

fn main() {
  println(share(100, 4))
  println(share(100, 0))
}
```

This is the usual shape of a validation function: raw input goes in, and either a proven value or a reason comes out.

## Facts about lists

The built-in predicate `notEmpty` is required by `first()`, which returns the first element without an `Option`:

```bork
fn smallest(xs: List[Int]): Option[Int] {
  sorted = xs.sorted()
  if (notEmpty(sorted)) { sorted.first() } else { Option.None }
}

fn main() {
  println(smallest([3, 1, 2]))
}
```

A fact can also hold for every element. `filter` records the test it used, so the elements of the result carry it:

```bork
pred positive(x: Int) { x > 0 }

fn total(amounts: List[Int where positive]): Int {
  amounts.fold(0, (sum, n) => sum + n)
}

fn main() {
  raw = [5, -2, 9]
  println(total(raw.filter(positive)))
}
```

Each loop element retains facts declared for every element of its source list.
A guard on one loop variable proves a fact only about that variable; it does
not prove the same fact about another element, including a nested loop over
the same list.

## Relations between values

A predicate with more than one parameter relates values to each other. The value being constrained is its first argument, and the rest are written in the `where`:

```bork
pred atLeast(x: Int, min: Int) { x >= min }
pred differentFrom(a: String, b: String) { a != b }

type Range = { lo: Int, hi: Int where atLeast(lo) }

fn transfer(from: String, to: String where differentFrom(from)): String {
  s"$from -> $to"
}

fn main() {
  println(Range { lo: 1, hi: 10 })
  println(transfer("alice", "bob"))
}
```

A function can also state a requirement on its parameters as a whole, between the parameter list and the result type:

```bork
fn slice(xs: List[Int], from: Int, to: Int) where from <= to: List[Int] {
  xs.drop(from).take(to - from)
}

fn main() {
  println(slice([1, 2, 3, 4, 5], 1, 3))
}
```

A record can carry a rule about the whole value in the same way, written after its fields. See [records with rules](types.md#records-with-rules).

## Rules

The compiler does not guess how your predicates relate. A `rule` tells it:

```bork
pred positive(x: Int) { x > 0 }
pred nonNegative(x: Int) { x >= 0 }

rule positiveIsNonNegative(x: Int) { positive(x) => nonNegative(x) }

fn root(x: Int where nonNegative): Int {
  x
}

fn use(n: Int where positive): Int {
  root(n)
}

fn main() {
  println(use(4))
}
```

With the rule, a value known to be `positive` is also known to be `nonNegative`. Rules are not taken on faith: `bork test` runs each one on generated values and fails if it finds a counterexample.

## Trust

Sometimes you know something the compiler cannot work out. `trust` states it:

```bork
pred even(n: Int) { n % 2 == 0 }

fn double(n: Int): Int where even {
  result = n * 2
  trust even(result)
  result
}

fn main() {
  println(double(21))
}
```

The compiler does not reason about multiplication, so without the `trust` line this function is rejected: the promised `even` is not proven.

A `trust` is checked when the tests run. Under `bork test`, every `trust` is evaluated, and a false one fails the test with the offending value.

## When the compiler cannot prove a fact

Start with the value named by the diagnostic, and keep the proof close to it:

1. **Guard the exact value.** Check `if (positive(count))` before passing `count`.
   A check on another binding or a different computation proves that other value.
   Repeated pure expressions can share a proof, but the compiler does not infer
   general algebraic equivalence or reuse effectful observations.
2. **Validate at the boundary.** Return `Int where positive | NotPositive` from
   a validator, as in [functions that promise a fact](#functions-that-promise-a-fact).
   Unwrap or match its success result before calling code that requires it.
3. **Keep the contract in the type.** Use a constrained alias such as
   `Quantity` when storing or returning validated data. If one predicate implies
   another, declare and test a `rule`; renaming a predicate does not create that
   implication.
4. **Inspect the proof.** Run [`bork describe`](../cli.md#describe) at the
   binding or expression to see its type and known facts. Compare the identities
   and predicate arguments with those required by the call.
5. **Use `trust` only for the remaining gap, and test it.** State the specific
   fact next to the calculation, and run that path under `bork test` so the
   trusted statement is evaluated.

For example, this calculation has a tested trust point:

```bork
pred even(n: Int) { n % 2 == 0 }

fn double(n: Int): Int where even {
  result = n * 2
  trust even(result)
  result
}

test "doubling is even" (n: Int) {
  assert(even(double(n)))
}

fn main() {
  println(double(21))
}
```

`trust` supplies a promise, not a runtime repair. Test the input range your
application uses, including boundaries; a trusted fact on an untested path
still depends on your reasoning.

## What facts cost

Proving a fact costs nothing at run time. Facts exist only during compilation, and the generated program does not carry them.

The checks that do run are the ones written in the code, such as the `if` in a validation function. Derived decoders are the other case: when JSON or another input is [decoded into a type](packages.md#derived-instances), the predicates on that type run on the incoming data.

## Where facts can be written

`where` works on parameters, results, bindings, record and variant fields, list elements, and type aliases. In a place where the compiler could not enforce it, such as the keys or values of a `Map`, writing a `where` is a compile error. A fact is never silently ignored.

Tuple element facts are checked like record field facts. A parameter of type
`(Int where positive, String)` requires the first element to satisfy positive;
`.0` access and tuple destructuring preserve the proof. Decode validates constrained
elements before returning success and reports JSON index paths.

To see what the compiler knows at some point in your code, ask it with [`bork describe`](../cli.md#describe).

The [payments example](../../examples/payments/main.bork) uses facts end to end.

---

Previous: [Collections](collections.md) · Next: [Effects](effects.md) · [All pages](../README.md#the-language)
