# Collections

bork has lists, maps, lazy sequences, and bytes built in. All of them are immutable: an operation gives a new collection and leaves the old one as it was.

[Tuples](types.md#tuples) group a fixed number of values whose types may differ;
lists hold any number of values of one element type. Comparable tuples can also
be map keys.

## Lists

```bork
fn main() {
  numbers = [3, 1, 4, 1, 5, 9]
  empty: List[String] = []
  println(numbers.length(), empty.isEmpty())
  println(numbers.filter(n => n > 2).map(n => n * 10))
  println(numbers.fold(0, (sum, n) => sum + n))
  println(numbers.sorted().distinct())
  println(numbers.get(0), numbers.get(99))
  println(numbers.append(2).take(3))
  println(range(0, 5))
}
```

```text
6 true
[30, 40, 50, 90]
23
[1, 3, 4, 5, 9]
Option.Some { value: 3 } Option.None
[3, 1, 4]
[0, 1, 2, 3, 4]
```

A list literal is written with square brackets. An empty one needs its type from somewhere, such as the binding's annotation.

Reading by position gives an `Option`, since the index may be out of range: `get(index)`, `head()`, and `last()`. `first()` gives the element itself, and so requires proof that the list is not empty. See [facts](facts.md).

The list methods:

| Group | Methods |
| --- | --- |
| Size | `length`, `isEmpty` |
| Reading | `get`, `head`, `last`, `first`, `find`, `includes` |
| Transforming | `map`, `flatMap`, `filter`, `fold`, `reverse`, `distinct` |
| Asking | `any`, `all`, `count` |
| Slicing and joining | `take`, `drop`, `concat`, `append`, `join` (on a list of strings) |
| Ordering | `sorted`, `sortBy`, `sortWith` |
| Grouping | `groupBy`, `toMap` (on a list of entries) |
| Doing | `forEach` |

`range(start, end)` builds the list of integers from `start` up to, but not including, `end`.

## Loops

Most work on lists is done with methods. There is also a `for` loop, with `break` and `continue`:

```bork
fn main() {
  for (n in [1, 2, 3, 4, 5, 6]) {
    if (n == 2) { continue }
    if (n == 5) { break }
    println(n)
  }
}
```

`for (x in xs)` goes through a `List` or a `Seq`. The other forms are Go's. `for { ... }` loops until `break` or `return`. `for (cond) { ... }` checks a condition before each round. `for (init; cond; post) { ... }` counts:

```bork
fn main() {
  for (i = 0, j = 10; i < j; i = i + 3, j = j - 1) {
    println(s"$i $j")
  }
  for (i = 1;; i = i * 2) {
    if (i > 50) { break }
    println(i)
  }
}
```

The header names (`i`, `j`) belong to the loop. Each round has its own values: a closure made in one round keeps that round's. The post clause gives the next round's values, computed together from this round's, so `a = b, b = a` swaps them. `continue` runs it too. It cannot `return`, `?`, `break` or `continue`. A header name may declare facts (`i: Int where nonNegative = 0`), which every first and next value must prove. The condition is known in the body. A `for` without a condition and without a `break` never ends normally, so code after it is unreachable.

A loop builds up values by rebinding names from outside it. Values never change: `total = total + x` binds a new `total`, which the loop *carries* to the next round, and after the loop the name has the value the last round left it with. The body may rebind such a name at its top level and inside the blocks of `if` and `match` statements in it. Where the branches meet, the name has the value of the branch that ran, and a branch that does not rebind it keeps the value it had:

```bork
fn main() {
  count = 0
  total = 0
  best = 0
  for (x in [3, -1, 4, -5, 9]) {
    if (x < 0) { continue }
    count = count + 1
    total = total + x
    if (x > best) { best = x }
  }
  println(s"$count values, total $total, best $best")
}
```

```text
3 values, total 16, best 9
```

A loop can carry a name that the block containing it may rebind, including a name an enclosing loop carries; `break` and `continue` carry the values current where they are. The post clause may rebind carried names too. A carried name keeps the type of its first binding, and the facts that binding declares hold in every round and after the loop. Rebinding it in a lambda, a `scope` or `with` block, or a branch whose value is used is an error, as is a carried value that is only read to compute its own next value: `count = count + 1` alone does not count as using `count`.

A function that calls itself in tail position also compiles to a loop (see [tail calls](basics.md#recursion-and-tail-calls)).

## Maps

A `Map[K, V]` goes from keys to values.

```bork
fn main() {
  stock = { "apple": 3, "pear": 0 }
  more = stock.put("fig", 12).remove("pear")
  println(stock)
  println(more)
  println(more.get("fig"), more.get("kiwi"), more.getOr("kiwi", 0))
  println(more.keys(), more.has("apple"), more.size())
  none: Map[String, Int] = {:}
  println(none.isEmpty())
}
```

```text
{"apple": 3, "pear": 0}
{"apple": 3, "fig": 12}
Option.Some { value: 12 } Option.None 0
["apple", "fig"] true 2
true
```

- A map literal is `{ key: value, ... }`. The empty map is `{:}`.
- `put` and `remove` give a new map. The old one is unchanged, and the two share most of their storage.
- A map remembers the order in which keys were added. `m.sorted()` gives a map that keeps its keys in sorted order.
- Two maps are equal when they hold the same entries.

The map methods: `get`, `getOr`, `has`, `put`, `remove`, `size`, `isEmpty`, `keys`, `values`, `entries`, `merge`, `mapValues`, `filter`, `forEach`, `sorted`, `sortedBy`, `inOrder`, and `unordered`.

Lists and maps work together. `groupBy` makes a map from a list, and `entries()` and `toMap()` convert in both directions:

```bork
fn main() {
  words = ["apple", "avocado", "banana", "blueberry", "cherry"]
  byLength = words.groupBy(w => w.byteLength())
  println(byLength)
  counts = byLength.mapValues(List.length)
  println(counts.entries().filter(e => e.value > 1).toMap())
}
```

## Lazy sequences

A `Seq[T]` describes a series of values without computing them. Nothing runs until something consumes the sequence, and only as much as is needed.

```bork
fn squares(): Seq[Int] {
  generate[Int] {
    for (n in Seq.range(1, 1_000_000)) {
      yield n * n
    }
  }
}

fn main() {
  println(squares().filter(n => n % 2 == 0).take(3).toList())
  for (n in squares()) {
    if (n > 20) { break }
    println(n)
  }
}
```

- `generate[T] { ... }` writes a sequence as a block that calls `yield` for each value.
- `map`, `filter`, `flatMap`, `take`, and `drop` build a new sequence and do no work yet.
- `for`, `forEach`, `fold`, `first`, and `toList` consume the sequence.
- `xs.toSeq()` makes a sequence from a list, and `Seq.range(start, end)` one of integers.

The first line of `main` computes only as many squares as it takes to find three even ones. A sequence can be consumed more than once, and each time it starts from the beginning.

Standard packages use sequences for input that should not be loaded all at once, such as the lines of a file or the rows of a query. Such a sequence does I/O as it is consumed, and its type says so: `Seq[String] uses io`. The function that consumes it needs that [effect](effects.md).

## Bytes

`Bytes` is an immutable sequence of bytes, for binary data.

```bork
import "bork/encoding"

fn main() {
  data = encoding.Utf8("héllo")
  println(data, data.length())
  println(encoding.ParseUtf8(data))
  println([toByte(0), toByte(255)].toBytes())
}
```

```text
Bytes(68c3a96c6c6f) 6
héllo
Bytes(00ff)
```

`encoding.ParseUtf8` returns `String | ParseError`, because not every byte sequence is valid text. Methods are `length`, `isEmpty`, `get`, `slice`, `concat`, and `toList`. The [bork/encoding](../std/encoding.md) package has UTF-8, hex and base64. `List[Byte].toBytes()` copies a byte list.

## Parallel list operations

`parMap` does what `map` does, but runs the function on several elements at once. The result keeps the order of the input.

```bork
fn slowSquare(n: Int): Int {
  range(0, 1000).fold(n * n, (acc, i) => acc)
}

fn main() {
  println(range(1, 9).parMap(slowSquare, workers: 4))
}
```

The function given to `parMap` must be pure, meaning it has no [effects](effects.md). `parFilter`, `parFlatMap`, and `parForEach` work the same way. `workers` limits how many run at a time, and defaults to the number of CPUs.

For work that has effects, such as calling a service for each element, use the `In` forms. They take a [scope](scopes.md), so the work can be cancelled, and they return `Cancelled` if it was. The [parallel_lists example](../../examples/parallel_lists/main.bork) shows them.

---

Previous: [Matching and errors](matching.md) · Next: [Facts](facts.md) · [All pages](../README.md#the-language)
