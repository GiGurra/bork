# Bork API

## example.com/docs

Module: `example.com/docs`.

This package demonstrates facts, effects and ambient APIs.

<a id="api-c4d83b852cf0f0dbdf20936c"></a>

### Trace

```bork
propagated("x-trace") logged ambient Trace: String where NonEmpty
```

Source: `api.bork:29`.

<a id="api-aa1cfad4a11d346689292e18"></a>

### Render

```bork
class Render[T] {
  // Render converts a value to display text.
  fn render(value: T): String
}
```

Source: `api.bork:41`.

<a id="api-d907cc4bc95ceeff6ca69103"></a>

### NewItem

```bork
fn NewItem(value: Count = 1, computed: Int = 2): Item
```

Source: `api.bork:17`.

<a id="api-960ded964cf55db91a24b643"></a>

### NewRange

```bork
fn NewRange(lo: Int, hi: Int): Range
// completed Range requires Ordered
```

Source: `api.bork:24`.

<a id="api-6ed7c09dd5a7512a568a6863"></a>

### BoundedValue

```bork
fn BoundedValue(value: Int where Above((1 + 1))): Int where Above((1 + 1))
```

Source: `api.bork:8`.

<a id="api-52fc778a95a88ec5b6b93519"></a>

### Identity

```bork
fn Identity[T: Show](value: T): T
```

Source: `api.bork:51`.

<a id="api-e69df2014d84919627bbc710"></a>

### Read

```bork
fn Read(value: Count) where (value < 100) uses io needs Trace?: Count
```

Read keeps its positive input and uses the optional trace.
Its documentation contains &lt;script&gt;literal text&lt;/script&gt;.

Source: `api.bork:33`.

<a id="api-bd3f985553a8bee70a9164bb"></a>

### ItemRender

```bork
instance ItemRender: Render[Item]
```

Source: `api.bork:45`.

<a id="api-b8cfafda832176fc25636c07"></a>

### Default

```bork
instances Default { ItemRender }
```

Source: `api.bork:46`.

<a id="api-996c7dfa0f0d7c3e9f84ac2f"></a>

### Item.Value

```bork
fn (self: Item) Value(): Count
```

Source: `api.bork:38`.

<a id="api-c31c2e5e31e210e453462e92"></a>

### Above

```bork
pred Above(value: Int, floor: Int)
```

Source: `api.bork:6`.

<a id="api-deff8f2d4b68c741462790fa"></a>

### NonEmpty

```bork
pred NonEmpty(value: String)
```

Source: `api.bork:28`.

<a id="api-43e9e28367795d572ee5f376"></a>

### Ordered

```bork
pred Ordered(r: Range)
```

Source: `api.bork:22`.

<a id="api-aa863ad54ca565d9f0b7451c"></a>

### Positive

```bork
pred Positive(n: Int)
```

Positive is the promise used by Count.

Source: `api.bork:4`.

<a id="api-871de18a80b580a144a8e16d"></a>

### Wiring

```bork
providers Wiring {
  number(n: Int = 7) where (n > 0) uses io needs Trace?: Int
}
```

Source: `api.bork:49`.

<a id="api-078bdd1077b8d1cddd762cd6"></a>

### Bounded

```bork
type Bounded = Int where Above((1 + 1))
```

Source: `api.bork:7`.

<a id="api-5afee2a4aaff94e3b3969601"></a>

### Computed

```bork
type Computed = {
  n: Int,
  lazy doubled: Int = <computed>,
  lazy values: List[Int] = <computed>,
  lazy nested: Nested = <computed>
}
```

Source: `api.bork:19`.

<a id="api-81e92001c2c27222437bceeb"></a>

### Count

```bork
type Count = Int where Positive
```

Source: `api.bork:5`.

<a id="api-5c8b3280ef4fbd7433e2f272"></a>

### Item

```bork
type Item = private {
  // The item count.
  value: Count = 1,
  // A lazy computed default.
  lazy computed: Int = 2
}
```

Item has package-owned construction.

Source: `api.bork:11`.

<a id="api-6e7ce7144add61e03388f8db"></a>

### Nested

```bork
type Nested = {
  value: Int
}
```

Source: `api.bork:20`.

<a id="api-caced6860b1431b484b96406"></a>

### Range

```bork
type Range = {
  lo: Int,
  hi: Int
} where Ordered
```

Source: `api.bork:23`.

<a id="api-f16d696fc72d432cd482bfb6"></a>

### State

```bork
type State = sealed {
  Shown {
  value: Int
},
  // additional variants are private
}
```

Source: `api.bork:26`.

<a id="api-b7b399bbbeab51692d04c03a"></a>

### Version

```bork
Version: Int
```

Source: `api.bork:52`.
