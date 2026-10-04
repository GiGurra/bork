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

Bindings are immutable. A name keeps its value, and it cannot be bound a second time, not even in a nested block:

```bork fails
fn main() {
  count = 1
  count = count + 1
  println(count)
}
```

Give the new value a new name instead (`next = count + 1`).

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

A value shared by the whole package is declared at the top level with `lazy`, as in `lazy MaxRetries = 3`. It is computed the first time it is read.

## Everything is an expression

`if`, `match`, and blocks all produce values, so they can be used wherever a value is expected.

```bork
fn describe(n: Int): String {
  sign = if (n < 0) { "negative" } else { "not negative" }
  size = {
    magnitude = if (n < 0) { -n } else { n }
    if (magnitude > 100) { "large" } else { "small" }
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

Number types never mix on their own. Convert with `toInt`, `toFloat`, `toInt8`, and so on. When the value might not fit, the conversion returns either the number or an `OutOfRange`, and `match` tells them apart. [Matching and errors](matching.md) explains this form.

```bork
fn main() {
  n = 300
  f = toFloat(n) / 7.0
  println(f)
  match (toUint8(n)) {
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
| `&&` `||` `!` | `Bool`. The right side of `&&` and `||` runs only when needed |

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

String methods include `byteLength`, `runeCount`, `contains`, `startsWith`, `endsWith`, `indexOf`, `toUpper`, `toLower`, `trim`, `replaceAll`, `repeat`, `substring`, `split`, `fields`, `lines`, and `runeAt`. The ones that may find nothing say so in their result: `indexOf` and `runeAt` return an `Option`, and `substring` returns the text or an `OutOfRange`. To turn text into a value, use `parseInt`, `parseFloat`, or `parseBool`, which return the value or a `ParseError`.

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
Option.Some { value: B }
Option.Some { value: ö }
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

A newline ends a statement, so a chain that continues on the next line must show that it is not finished. End the line with the dot:

```bork
fn main() {
  names = ["tim", "ada"].
  sorted().
  map(n => n.toUpper())
  println(names)
}
```

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

Next: [Types](types.md) · [All pages](../README.md#the-language)
