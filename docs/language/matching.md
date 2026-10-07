# Matching and errors

`match` takes a value apart by its shape. It is also how a program handles failure, because failures are ordinary values.

## Match

A `match` tries its arms from top to bottom and gives the value of the first one that fits.
An arm already covered by earlier arms is a compile error.

```bork
type Shape = sealed {
  Circle { radius: Int }
  Rect { width: Int, height: Int }
}

fn describe(shape: Shape): String {
  match shape {
    .Circle { radius: 0 } => "a point"
    .Circle { radius } => s"a circle of radius $radius"
    .Rect { width, height: h } => s"a $width by $h rectangle"
  }
}

fn main() {
  println(describe(Shape.Circle { radius: 0 }))
  println(describe(Shape.Rect { width: 2, height: 3 }))
}
```

Inside the braces, `{ radius }` binds the field to a name of its own, `{ height: h }` binds it to `h`, and `{ radius: 0 }` requires it to equal a value.

A leading dot omits the type name: `.Circle` uses the type of `shape`.
Write `Shape.Circle` when several types in a union have a `Circle` variant.

## Positional payloads

A sealed variant can declare ordered payload types. Construct and match it with
parentheses, keeping the declared order and exact number of values:

```bork
type Reply[T] = sealed { Found(T, String), Missing }
fn text(reply: Reply[Int]): String {
  match reply {
    .Found(number, label) => s"$label: $number"
    .Missing => "missing"
  }
}
fn main() {
  println(text(Reply.Found(3, "count")))
  println(text(Reply[Int].Missing))
  value = Option[Int].Some(4)
  println(match value { Option[Int].Some(n) => n, Option[Int].None => 0 })
}
```

`Option[T]` declares `Some(T)` and `None`. Use `Option.Some(value)` or
`.Some(value)` with an expected Option type, and `.Some(name)` to bind its
payload. A fieldless variant stays bare: `.None`. Explicit type arguments go
on the owner, as in `Option[Int].None`, including inside patterns.

Named variants keep braces. Positional variants accept no names, defaults or
spread, and reject braces. A bare variant pattern ignores all its payloads.
`One((Int, String))` has one tuple-valued payload; `Pair(Int, String)` has two.
Nested patterns may inspect each slot, and refutable slots require further
arms to cover the remaining values.

Derived codecs represent positional payloads as a tagged object with a `values`
array, such as `{"type":"Found","values":[3,"count"]}`. Decoding requires the
exact payload count and reports a failing element at `.values[0]`, for example.
Option uses null or the bare value. See [codecs](../std/codec.md) for deriving
encoders and decoders.

## Patterns

| Pattern | Matches |
| --- | --- |
| `_` | anything |
| `42`, `"yes"`, `'a'`, `true` | that exact value, when the value being matched has that type |
| `n` | anything, and names it `n` |
| `n: Int` | a value of that type, named `n` |
| `NotFound` | a value of that type, without naming it |
| `User { name, age: 0 }` | a record, looking at the fields listed |
| `.Circle { radius }`, `Shape.Circle { radius }` | a variant with named fields |
| `.Some(value)`, `Option[Int].Some(value)` | a variant with positional payloads |
| `(left, right)`, `(only,)` | a tuple with that exact number of elements |
| `[]`, `[x]`, `[first, ...rest]` | a list by its length, naming the elements |

Tuple patterns can combine literal and nested patterns, and the compiler checks
that the arms cover every possibility:

```bork
fn describe(pair: (Bool, Int)): String {
  match pair {
    (true, n) => s"enabled: $n"
    (false, _) => "disabled"
  }
}
```

A tuple pattern also selects the unique compatible tuple member of a union. If
several tuple members have the same arity, use a typed element or an explicit variant to select a member,
for example `(number: Int, text: String)`. A typed tuple arm can use
`pair: (Int, String) => pair.0`. A function type can use extra
parentheses to make its two arrows clear: `callback: ((Int) => Int) => callback(1)`.

Patterns nest, so a field can be matched against another pattern:

```bork
type Token = { text: String, quoted: Bool }

fn first(tokens: List[Token]): String {
  match tokens {
    [] => "nothing"
    [Token { text, quoted: true }, ...] => s"the quoted word $text"
    [Token { text }, ...rest] => s"$text, then ${rest.length()} more"
  }
}

fn main() {
  println(first([]))
  println(first([Token { text: "hi", quoted: false }, Token { text: "there", quoted: true }]))
}
```

## Testing a pattern

Use `is` when you need a Bool instead of extracting values:

```bork
type Reply = sealed { Found { text: String }, Missing }
fn matches(reply: Reply): Bool {
  reply is .Found { text: "hello" }
}
fn positiveNumber(value: Int | String): Bool {
  value is Int where positive and atMost(100)
}
pred positive(n: Int) { n > 0 }
pred atMost(n: Int, limit: Int) { n <= limit }

fn main() {
  println(matches(Reply.Found { text: "hello" }), matches(Reply.Missing))
  println(positiveNumber(42), positiveNumber("42"))
}
```

```text
true false
true false
```

`is` returns a Bool and evaluates the value once. It cannot bind names; write
explicit field patterns such as `{ text: "hello" }`, or `_` to ignore a value.
Patterns can nest and test types, literals, and predicates, as in `match`.

An `is` condition does not narrow the value's type or establish facts inside an
`if`. Use `match` to extract a value with its type and facts, or
[test.AssertIs](testing.md#typed-assertions) in a test. Combine Bool tests with
`&&` and `||`; use parentheses when they make the condition clearer.

## Every case must be handled

A `match` has to cover every possible value. This one forgets a variant:

```bork fails
type Light = sealed { Red, Amber, Green }

fn go(light: Light): Bool {
  match light {
    Light.Red => false
    Light.Green => true
  }
}
```

```text
match is not exhaustive: missing Light.Amber
```

The check looks inside nested patterns too, and reports the exact shape that is missing. An arm that can never be reached is also an error.

Add a variant to a sealed type, or a new failure to a function's result, and the compiler lists every `match` that has to change.

## Failures are values

There are no exceptions. A function that can fail returns a union: the success value, or a value describing the failure.

```bork
type Account = { id: Int, balance: Int }
type NotFound = { id: Int }
type Insufficient = { missing: Int }

fn find(id: Int): Account | NotFound {
  if id == 1 { Account { id: 1, balance: 100 } } else { NotFound { id: id } }
}

fn withdraw(id: Int, amount: Int): Account | NotFound | Insufficient {
  account = find(id)?
  if account.balance < amount {
    return Insufficient { missing: amount - account.balance }
  }
  account.copy(balance: account.balance - amount)
}

fn main() {
  message = match withdraw(1, 250) {
    a: Account => s"new balance: ${a.balance}"
    NotFound { id } => s"no account $id"
    Insufficient { missing } => s"short by $missing"
  }
  println(message)
}
```

The signature of `withdraw` lists everything that can happen, and the `match` in `main` must handle all of it.

## The `?` operator

The `?` operator passes a failure up to the caller. `find(id)?` means: if the result is the success value, keep it and carry on. Otherwise return the failure from this function right away.

"Success" is the first member of the union. Every other member must be something the enclosing function is able to return, so `withdraw` has `NotFound` in its own result type.

On an `Option`, `?` keeps the value inside `Some` and returns `Option.None`:

```bork
fn secondWord(text: String): Option[String] {
  words = text.fields()
  first = words.get(0)?
  second = words.get(1)?
  s"$second (after $first)"
}

fn main() {
  println(secondWord("hello there"))
  println(secondWord("hello"))
}
```

## Matching on types

When a value is a union, a type pattern picks out a member. One arm can take several members at once:

```bork
type Timeout = { ms: Int }
type Refused = { host: String }

fn fetch(ok: Bool): String | Timeout | Refused {
  if ok { "body" } else { Timeout { ms: 500 } }
}

fn main() {
  text = match fetch(false) {
    body: String => body
    e: Timeout | Refused => s"failed: $e"
  }
  println(text)
}
```

## Matching with a condition

A type pattern can also carry a `where` clause. The arm is taken only when the predicate holds, and inside it the compiler knows the [fact](facts.md).

```bork
pred positive(x: Int) { x > 0 }

fn half(n: Int where positive): Int {
  n / 2
}

fn main() {
  result = match 7 {
    n: Int where positive => half(n)
    _ => 0
  }
  println(result)
}
```

An arm with a condition might not be taken, so the `match` still needs an arm without one.

## Panics

`panic("message")` stops the program. It is for bugs: a state that should be impossible. It cannot be caught. Anything a caller might reasonably want to handle should be a value in the result type instead.

```bork
fn percent(part: Int, whole: Int): Int {
  if whole == 0 {
    panic("percent: whole is zero")
  }
  part * 100 / whole
}

fn main() {
  println(percent(1, 4))
}
```

For unfinished code there is `todo()`, described under [development helpers](testing.md#development-helpers).

---

Previous: [Types](types.md) · Next: [Collections](collections.md) · [All pages](../README.md#the-language)
