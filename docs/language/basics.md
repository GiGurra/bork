# Basics

This page covers the everyday parts of the language: names, functions, expressions, numbers, text, and methods.

## Names and values

A binding gives a value a name. There is no keyword for it.

```bork
fn main() {
  answer = 6 * 7
  greeting: String = "hello"
  println(greeting, answer)
}
```

The type after the name is optional. The compiler works it out when it is left off.

Bindings are immutable. Reusing a name in the same block creates a new binding,
whose type may differ. Its initializer sees the previous binding; closures keep
the value they captured:

```bork
fn main() {
  count = 1
  earlier = () => count
  count = count + 1
  count = s"now $count"
  println(earlier(), count)
}
```

Parameters may be rebound in the function body's top-level block. Inner blocks
and lambdas cannot shadow a name from an enclosing scope. Sibling blocks may
reuse names. Package functions, imports and package values cannot be rebound locally.
Prelude free functions may be shadowed by locals; calling a non-function local
with a prelude function’s name reports the local declaration.

✅ Rebinding in the same block is allowed, as in the `count` example above.
❌ An inner block cannot introduce another `count`:

```bork fails
fn main() {
  count = 1
  if count > 0 {
    count = 2
    println(count)
  }
}
```

```text
count is already defined in an enclosing scope (bork does not allow shadowing)
```

Loops have a specific exception for [carrying values](#loop-carrying).

Every local binding must be read, including a binding replaced by another one.
Unused locals are compiler errors; parameters are exempt. Use `_ = expression`
(or `_: Type = expression` when an annotation is needed) to discard a result.
In patterns, use `_`, `{ field: _ }`, `_: Type`, or `[_, ...]` to discard values.
A deferred value can be captured by a discarded closure (`_ = () => value`)
without forcing it. Editors mark rebinding declarations and show the earlier
binding's line in hover text.

## Functions

```bork
fn area(width: Int, height: Int): Int {
  width * height
}

fn greet(name: String, greeting: String = "Hello"): String {
  greeting + ", " + name + "!"
}

fn main() {
  println(area(3, 4))
  println(greet("Ada"))
  println(greet("Ada", greeting: "Welcome"))
  println(area(height: 2, width: 5))
}
```

- Parameters and the result have types. The result type comes after the `:`.
- The value of the last expression is the result. `return` exists for leaving early.
- The last parameters can have default values.
- Arguments can be passed by name, in any order, after the positional ones.

A function that returns nothing leaves out the result type, as `main` does.

A value shared by the whole package is declared at the top level, as in `MaxRetries = 3` or `MaxRetries: Int = 3`. Its initializer must be pure: no effects or ambient values. It is computed once, the first time it is read, and is never computed if unused. A value read by `comptime` is instead evaluated during compilation and baked as data. `lazy MaxRetries = 3` means the same thing. Local bindings inside functions still evaluate eagerly. Inside a `lazy` or `async(s)` initializer, use `match` to handle a union or Option: `?` is rejected there, as in a lambda.

Package values can refer to values declared later or in another file in the same package. Dependency cycles are compile errors, including through helper functions. An uppercase name is exported, as with functions and types. An annotation can require facts (`Limit: Int where positive = 3`); the initializer must prove them and reads retain those promises. A package initializer can itself use `comptime { ... }` and read other pure package values; see [compile-time evaluation](comptime.md).

## Everything is an expression

`if`, `match`, and blocks all produce values, so they can be used wherever a value is expected.

```bork
fn describe(n: Int): String {
  sign = if n < 0 { "negative" } else { "not negative" }
  size = {
    magnitude = if n < 0 { -n } else { n }
    if magnitude > 100 { "large" } else { "small" }
  }
  s"$sign and $size"
}

fn main() {
  println(describe(-250))
}
```

A block `{ ... }` is a sequence of bindings and expressions, and its value is its last expression.

An `if` without an `else` gives no value. It is used as a statement, to do something or to `return` early.

A value that is computed and then ignored is a compile error. This catches a forgotten result. To drop one on purpose, bind it to `_`:

```bork
fn compute(): Int {
  42
}

fn main() {
  _ = compute()
}
```

An expression that gives exactly `Ok`, which has no value, needs no `_`. A union that includes `Ok`, such as `Ok | IoError`, still has to be handled or dropped with `_ =`. A task with no value, `Task[Ok]`, is the one other exception (see [tasks](scopes.md#tasks)).

## Control flow

Use `if` to choose between expressions or run a statement conditionally.
Parentheses around `if`, `match` and `for` heads are optional; the bare form
is idiomatic. Parentheses remain useful for grouping. A bare name followed by
`{` in a head starts the body, so wrap a record literal explicitly:
`match (Row { value: 1 }) { ... }`. Calls such as `if check(Row { value: 1 }) { ... }`
already delimit their arguments. Normal formatting preserves your choice;
`bork fmt --simplify` removes redundant head parentheses.
Use `match` to choose by a value's type or shape and handle every possibility:

```bork
fn label(n: Int): String {
  if n < 0 { return "negative" }
  match n {
    0 => "zero"
    _ => "positive"
  }
}

fn main() {
  println(label(-1), label(0), label(3))
}
```

`return` leaves the current function immediately. It is useful for a guard
before the main work. See [matching and errors](matching.md) for patterns and
returning failures with `?`.

### Loop forms

All loops use `for`. `break` leaves the nearest loop; `continue` skips to its
next round. A loop has no result value.

```bork
fn main() {
  // Visit each list element. Seq works here too.
  for n in [1, 2, 3] {
    println(n)
  }

  // Check the condition before each round.
  remaining = 3
  for remaining > 0 {
    println(remaining)
    remaining = remaining - 1
  }

  // Initialize once, check the condition, then run the post clause.
  for i = 0; i < 4; i = i + 1 {
    if i == 1 { continue }
    println(i)
  }

  // With no condition, stop explicitly.
  for {
    println("done")
    break
  }

  // A counting header can also omit its condition.
  for i = 1;; i = i * 2 {
    if i > 8 { break }
    println(i)
  }
}
```

Header names belong to the loop. Each round has its own values, and closures
keep the round they captured. `continue` still runs the post clause. Several
header bindings can advance together:

```bork
fn main() {
  for a = 1, b = 2; a < 10; a = b, b = a + b {
    println(a, b)
  }
}
```

The post expressions all read this round's values, so `a = b, b = a` swaps
them. A post clause cannot use `return`, `?`, `break`, or `continue`. Facts on a
header binding must hold for its initial value and every next value. An
unconditional loop with no `break` cannot finish normally; code after it is
unreachable.

### Loop carrying

✅ A loop can rebind a name from its surrounding block. `total = total + x`
binds a new `total`, which the loop carries to the next round. After the loop,
the name has the last value reached. This is how a program accumulates a result
while keeping its values immutable.

The body can rebind that name directly or inside an `if` or `match` statement.
A branch that does not rebind it keeps the previous value:

```bork
fn main() {
  count = 0
  total = 0
  best = 0
  for x in [3, -1, 4, -5, 9] {
    if x < 0 { continue }
    count = count + 1
    total = total + x
    if x > best { best = x }
  }
  println(s"$count values, total $total, best $best")
}
```

```text
3 values, total 16, best 9
```

A nested loop can carry a name that its enclosing loop carries. `break` and
`continue` keep the values reached before the jump. The post clause can rebind
carried names too. Their original types and declared facts must hold in every
round and after the loop.

A lambda, `scope`, `with`, or a branch used as a value cannot rebind a carried
name. Read the accumulated result somewhere: `count = count + 1` alone does
not count as using `count`.

❌ Carrying preserves the original type, even though ordinary same-block
rebinding can change types:

```bork fails
fn main() {
  total = 0
  for n in [1, 2, 3] {
    total = s"$total + $n"
  }
  println(total)
}
```

```text
total is carried to the next iteration of the loop at line 3, so its new value must be Int (its type before the loop), found String; declare its first binding with a type that holds both
```

## Recursion and tail calls

A call is a *tail call* when its result is the function's result, with nothing left to do after it. A function's calls of itself in tail position are compiled as jumps to the top of the function, so they use no stack:

```bork
fn gcd(a: Int, b: Int) uses tailrec: Int {
  if b == 0 { a } else { gcd(b, a % b) }
}

fn countdown(n: Int) uses io {
  if n > 0 {
    countdown(n - 1)
  }
}

fn main() {
  println(gcd(48, 18))
  countdown(10000000)
}
```

This needs no marker: `countdown` runs ten million rounds without growing its stack. Writing `tailrec` in the `uses` list asks the compiler to guarantee it. The function must call itself, every call of itself must be a tail call, and it must not be mutually recursive with another function. Otherwise compilation fails, and the error names each call and the reason. `tailrec` is not an effect. Callers do not declare it, and it is not part of the function's type.

Positions that are not tail positions include a call whose result is used afterwards (`n * fact(n - 1)`), the operand of `?`, and a call inside a `scope` or `with` block, which ends after the call returns. Only direct calls of the function itself are optimized; calls between two functions are ordinary calls. `bork describe` and editor hovers show how a call of a function by itself is compiled.

## Numbers

`Int` is a 64-bit integer and `Float` is a 64-bit float. Sized types exist for when the width matters: `Int8`, `Int16`, `Int32`, `Uint8` (also called `Byte`), `Uint16`, `Uint32`, `Uint64`, and `Float32`.

```bork
fn main() {
  big = 1_000_000
  mask = 0xFF
  half = 7 / 2
  exact = 0.1 + 0.2
  println(big, mask, half, exact)
}
```

This prints `1000000 255 3 0.3`. Arithmetic on number literals is exact, which is why `0.1 + 0.2` is `0.3`. That applies to literals only. Floats held in names are ordinary 64-bit floats, and adding those two gives `0.30000000000000004`.

Integer literals can also use binary (`0b1010`) and octal (`0o17`) prefixes, with `_` separators. Import [bork/strconv](../std/strconv.md) for `n.Hex()`, `n.Binary()`, `n.Octal()`, and `n.Format(base)` formatting, plus String parsing methods such as `text.ParseInt(base: 16)` and `text.ParseByte(base: 0)`. Formatting returns `String`; parsing returns the number or `ParseError`, including on overflow.
Integers support `&` (AND), `|` (OR), and `^` (XOR). **Bitwise NOT is `^x`**, following Go; `~x` is an error. Both operands of AND, OR, and XOR have the same integer type. Bool values use `&&`, `||`, and `!=` instead.

`<<` and `>>` keep the left operand's width. A shift count can be unsigned, a nonnegative constant, or a signed value proven nonnegative by a guard or predicate. Runtime left shifts discard bits beyond that width. Signed right shifts preserve the sign; unsigned right shifts fill with zero. Counts at least the width give zero, except a negative signed right shift gives -1. Constants must fit, as with arithmetic.

```bork
fn main() {
  flags: Byte = 240
  println(flags & 15, flags | 3, flags ^ 15, ^flags)
  count = 2
  println(flags << count, flags >> count)
}
```

Shifts and `&` bind like multiplication; `|` and binary `^` bind like addition. Use parentheses to make bit fields easy to read.

Number types never mix on their own. Convert with `toInt`, `toFloat`, `toInt8`, and so on. When the value might not fit, the conversion returns either the number or an `OutOfRange`, and `match` tells them apart. [Matching and errors](matching.md) explains this form.

```bork
fn main() {
  n = 300
  f = toFloat(n) / 7.0
  println(f)
  match toUint8(n) {
    b: Uint8 => println("fits:", b)
    e: OutOfRange => println(s"${e.value} does not fit in ${e.target}")
  }
}
```

Integers wrap around on overflow. Dividing by a constant zero is a compile error.

## Operators

| Operators | Work on |
| --- | --- |
| `+` `-` `*` `/` | two numbers of the same type. `+` also joins strings |
| `%` | two integers of the same type |
| `<` `<=` `>` `>=` | numbers, strings, and runes |
| `==` `!=` | any two values of the same type that can be compared |
| `&&` `\|\|` `!` | `Bool`. The right side of `&&` and `\|\|` runs only when needed |

`==` compares by content: two records, lists, or maps are equal when their parts are. Functions cannot be compared.

## Text

A `String` is UTF-8 text. `+` joins strings, and `s"..."` builds one from values:

```bork
type Point = { x: Int, y: Int }

fn main() {
  name = "Ada"
  p = Point { x: 1, y: 2 }
  println(s"Hello, $name! Next year you are ${36 + 1}.")
  println(s"Any value can be inserted: $p")
  println(s"A dollar sign is written $$.")
  println("  padded text ".trim().toUpper())
  println("a,b,c".split(","))
  println(parseInt("42"))
}
```

```text
Hello, Ada! Next year you are 37.
Any value can be inserted: Point { x: 1, y: 2 }
A dollar sign is written $.
PADDED TEXT
["a", "b", "c"]
42
```

Only strings with the `s` prefix interpolate. `toString(x)` gives the same text as printing `x`.

String indexes count Unicode code points: `indexOf` returns an offset that can be passed to `substring` or `runeAt`. Regex match offsets use the same units. Code points are not grapheme clusters: a letter followed by a combining accent counts as two. Byte operations are explicit: `byteLength`, `byteIndexOf` and `byteSubstring` use UTF-8 bytes. See [String indexing](../std/strings.md).

String methods include `byteLength`, `runeCount`, `contains`, `startsWith`, `endsWith`, `indexOf`, `byteIndexOf`, `toUpper`, `toLower`, `capitalize` (upper-cases the first character), `trim`, `replaceAll`, `repeat`, `substring`, `byteSubstring`, `split`, `fields`, `lines`, and `runeAt`. The ones that may find nothing say so in their result: `indexOf` and `runeAt` return an `Option`, and `substring` returns the text or an `OutOfRange`. To turn text into a value, use `parseInt`, `parseFloat`, or `parseBool`, which return the value or a `ParseError`.

### Printing

`println` writes values to standard output, separated by spaces, then adds a
newline. `eprintln` accepts the same values and writes to standard error.
Both use a value's `Show` instance when available:

```bork
fn main() {
  count = 3
  println("count:", count)
  eprintln(count)
  eprintln("count:", count)
}
```

Calling either function with no arguments prints a newline. Use `toString` or
interpolation when you need the rendered value as a `String`.
Functions that print declare `uses io`; `main` can use the entry point's effects.

## Runes

A `Rune` is one Unicode character. It is written in single quotes, and it is its own type, not a number.

```bork
fn main() {
  letter = 'å'
  println(letter, letter.isLetter(), letter.isUpper())
  println(letter.code())
  println(toInt32(66).rune())
  println("smörgåsbord".runeAt(2))
}
```

```text
å true false
229
Option.Some(B)
Option.Some(ö)
```

- `r.code()` gives the code point as an `Int32`.
- `n.rune()` on an `Int32` goes the other way and returns an `Option[Rune]`, because not every number is a valid character. An [`Option`](types.md#option) is a value that may be missing.
- `isDigit`, `isLetter`, `isSpace`, `isUpper`, and `isLower` test what kind of character it is.

## Functions as values

A lambda is a function written in place. Functions can be stored, passed, and returned.

```bork
fn applyTwice(f: (Int) => Int, x: Int): Int {
  f(f(x))
}

fn double(n: Int): Int {
  n * 2
}

fn main() {
  addTen = (n: Int) => n + 10
  println(applyTwice(double, 3))
  println(applyTwice(addTen, 3))
  println(applyTwice(n => n * n, 3))
}
```

`(Int) => Int` is the type of a function from `Int` to `Int`. A lambda can use the names around it. `return` is not allowed inside a lambda.

## Methods

Operations on a value are methods, called with a dot, and they chain from left to right. The built-in ones need no import.

```bork
type User = { name: String, age: Int }

fn (u: User) isAdult(): Bool {
  u.age >= 18
}

fn main() {
  users = [User { name: "Ada", age: 36 }, User { name: "Tim", age: 9 }]
  names = users.filter(u => u.isAdult()).map(u => u.name).join(", ")
  println(names)
  println(["one", "three"].map(String.byteLength))
}
```

You declare a method by putting a receiver in parentheses before the name: `fn (u: User) isAdult()`. Methods can be added to your own types and to built-in ones such as `List` and `String`.

`Type.method` names a method as a function value, as in `String.byteLength` above.

Continue a chain on the next line by starting with a dot. The formatter indents continuation lines one level:

```bork
fn main() {
  names = ["tim", "ada"]
    .sorted()
    .map(n => n.toUpper())
  println(names)
}
```

Ending a line with the dot, then writing the method name on the next line, also works. Blank lines and comments between chain links do not end the chain. A semicolon explicitly ends a statement.

Inside parentheses and brackets, newlines are ignored.

## Pipes

`|>` passes the value on its left as the first argument of a function. It lets your own free functions join a chain:

```bork
fn shout(text: String, mark: String): String {
  text.toUpper() + mark
}

fn main() {
  "hello" |> shout("!") |> println
}
```

## Comments

```bork
// A line comment.

/* A block comment. */
fn main() {}
```

---

Previous: [Scripts](scripts.md) · Next: [Types](types.md) · [All pages](../README.md#the-language)
