# Collections

bork has lists, maps, lazy sequences, and bytes built in. All of them are immutable: an operation gives a new collection and leaves the old one as it was.

For a fixed number of values of different types, use a [tuple](types.md#tuples).

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
Option.Some(3) Option.None
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
| Transforming | `map`, `flatMap`, `filter`, `fold`, `reverse`, `distinct`, `indexed` |
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

`for x in xs` goes through a `List` or a `Seq`. A header can bind a tuple
with names, `_`, and nested tuple patterns. Each name is fresh for that round;
a tuple pattern must match the element’s tuple shape. Indexes are explicit:

```bork
fn main() {
  for (index, text) in ["one", "two"].indexed() { println(index, text) }
  for (key, value) in { "apple": 3, "pear": 0 }.pairs() { println(key, value) }
}
```

`List[T].indexed()` gives `List[(Int, T)]`, starting at zero.
For condition loops,
counting loops, early exits, and accumulating values across rounds, see
[control flow](basics.md#control-flow) and [loop carrying](basics.md#loop-carrying).

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
Option.Some(12) Option.None 0
["apple", "fig"] true 2
true
```

- A map literal is `{ key: value, ... }`. The empty map is `{:}`.
- `put` and `remove` give a new map. The old one is unchanged, and the two share most of their storage.
- A map remembers the order in which keys were added. `m.sorted()` gives a map that keeps its keys in sorted order.
- Two maps are equal when they hold the same entries.
- Comparable [tuples](types.md#tuples) can be keys, such as `(x, y)` for a grid position.

The map methods: `get`, `getOr`, `has`, `put`, `remove`, `size`, `isEmpty`, `keys`, `values`, `entries`, `pairs`, `merge`, `mapValues`, `filter`, `forEach`, `sorted`, `sortedBy`, `inOrder`, and `unordered`.

`Map[K, V].pairs()` gives `List[(K, V)]` in the map’s traversal order,
including insertion order and the order selected by `sorted` or `sortedBy`.

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
- `map`, `filter`, `flatMap`, `take`, `drop`, and `indexed` build a new sequence and do no work yet.
- `for`, `forEach`, `fold`, `first`, and `toList` consume the sequence.
- `xs.toSeq()` makes a sequence from a list, and `Seq.range(start, end)` one of integers.

`Seq[T].indexed()` gives a lazy `Seq[(Int, T)]` with the source’s effects.
The index starts at zero on each traversal. It visits each source element once
and stops as soon as its consumer stops.

The first line of `main` computes only as many squares as it takes to find three even ones. A sequence can be consumed more than once, and each time it starts from the beginning.

Standard packages use sequences for input that should not be loaded all at once, such as the lines of a file or the rows of a query. Such a sequence does I/O as it is consumed, and its type says so: `Seq[String] uses io`. The function that consumes it needs that [effect](effects.md).

### Comprehensions

A comprehension writes a sequence as generator lines, filters and bindings, then the value to yield:

```bork
type Item = { name: String, active: Bool, qty: Int }
type Order = { id: Int, items: List[Item] }

fn activeItems(orders: List[Order]): Seq[(Int, String, Int)] {
  for {
    order in orders
    item in order.items
    if item.active
    total = item.qty * 10
  } yield (order.id, item.name, total)
}

fn main() {
  orders = [
    Order { id: 1, items: [Item { name: "pen", active: true, qty: 2 }, Item { name: "ink", active: false, qty: 1 }] },
    Order { id: 2, items: [Item { name: "pad", active: true, qty: 3 }] },
  ]
  println(activeItems(orders).toList())
  stock = { "apple": 3, "pear": 0 }
  println((for { (name, count) in stock.pairs(); if count > 0 } yield name).toList())
}
```

```text
[(1, "pen", 20), (2, "pad", 30)]
["apple"]
```

- `x in xs` goes through a `List` or `Seq`, and its name can be a [loop pattern](#loops) such as `(k, v)`. A later line can use earlier names, as `item in order.items` does.
- `if cond` skips the values for which it is false. What it proves holds in the lines after it and in the yield.
- `name = value` binds a name for the lines after it.
- The first line is a generator, and `yield` goes on the line of the closing `}`.

A comprehension is a `generate` block written another way. It is lazy, including its first source: nothing runs until the sequence is consumed, and each consumption starts again. It has the effects of what it runs. When its consumer stops early, it stops its sources, so a generator that holds a resource releases it then. The element type is the yield's type, or the one the context expects (`xs: Seq[Int | String] = for { ... } yield n`).

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
