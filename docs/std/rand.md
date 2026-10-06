# bork/rand

`bork/rand` samples values with an effectful random source or a pure, replayable generator.

```bork
import "bork/rand"

fn main() {
  generator = rand.Seed(42)
  draw = generator.IntBetween(0, 100)
  println(draw.value == generator.IntBetween(0, 100).value)
  println(generator.Pick(["a"]).value)
  empty: List[String] = []
  println(generator.Pick(empty).value)
}
```

```text
true
Option.Some("a")
Option.None
```

## API

| Signature | Meaning |
| --- | --- |
| `Seed(first: Int, second: Int = 0): Generator` | Create an immutable PCG generator. |
| `IntBetween(lo: Int, hi: Int where Above(lo)) uses random: Int` | Draw an integer in [lo, hi). |
| `Float01() uses random: Float` | Draw a float in [0, 1). |
| `Shuffle[T](values: List[T]) uses random: List[T]` | Return a shuffled copy. |
| `Pick[T](values: List[T]) uses random: Option[T]` | Select an element, or None if empty. |
| `IntFrom(generator: Generator, lo: Int, hi: Int where Above(lo)): Draw[Int]` | Draw a bounded integer and the next generator. |
| `FloatFrom(generator: Generator): Draw[Float]` | Draw a float and the next generator. |
| `ShuffleFrom[T](generator: Generator, values: List[T]): Draw[List[T]]` | Shuffle and return the next generator. |
| `PickFrom[T](generator: Generator, values: List[T]): Draw[Option[T]]` | Pick and return the next generator. |
| `(generator: Generator) IntBetween(lo: Int, hi: Int where Above(lo)): Draw[Int]` | Draw an integer in [lo, hi). |
| `(generator: Generator) Float01(): Draw[Float]` | Draw a float in [0, 1). |
| `(generator: Generator) Shuffle[T](values: List[T]): Draw[List[T]]` | Return a shuffled copy. |
| `(generator: Generator) Pick[T](values: List[T]): Draw[Option[T]]` | Select an element, or None if empty. |

Random functions use Go's `math/rand/v2` default source for functions declaring
`uses random`. `Seed(first, second = 0)` creates an opaque immutable PCG
generator. `IntFrom`, `FloatFrom`, `ShuffleFrom`, and `PickFrom` return a
`Draw[T]` with `value` and `next`; the corresponding generator methods do the
same. Reusing the input replays a draw; using `next` advances it. Seeded draws
are pure and require no effects. Do not rely on identical bounded draws across
32-bit and 64-bit platforms or future runtime versions.

Integer ranges include `lo` and exclude `hi`, require `hi > lo`, and support
ranges spanning the signed Int boundary. Floats lie in `[0, 1)`. Shuffle copies
its input. Pick returns None for an empty list, preserving the generator state.
This package is for simulations and sampling; use `bork/crypto` for secrets.


## Advance or replay a generator

```bork
import "bork/rand"

fn main() {
  initial = rand.Seed(42)
  first = initial.IntBetween(0, 100)
  second = first.next.IntBetween(0, 100)
  println(first.value == initial.IntBetween(0, 100).value)
  println(second.value == first.next.IntBetween(0, 100).value)
}
```

```text
true
true
```

`Generator` has a private representation; `Draw[T]` has fields `value: T` and
`next: Generator`. A pure operation never changes its input generator. Pass the
returned `next` to keep drawing; save an input to replay a draw.

## Guard a dynamic range

```bork
import "bork/rand"

fn sample(lo: Int, hi: Int) uses random: Int | OutOfRange {
  if (rand.Above(hi, lo)) {
    rand.IntBetween(lo, hi)
  } else {
    OutOfRange { value: toString(hi), target: "upper bound above lower bound" }
  }
}

fn main() {
  println(sample(2, 2))
}
```

The predicate `Above(value: Int, lower: Int): Bool` proves `value > lower`.
A constant invalid range is rejected at compile time. See the
[random example](../../examples/rand/main.bork).


Run `bork doc bork/rand` for the generated reference.

[All standard packages](README.md)
