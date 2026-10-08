# Types

Programs describe their data with records, sealed types, unions, and tuples. All values are immutable.

## Records

A record has named fields.

```bork
type Address = { city: String, zip: String }
type User = { name: String, age: Int = 18, address: Address }

fn main() {
  ada = User { name: "Ada", age: 36, address: Address { city: "London", zip: "N1" } }
  tim = User { name: "Tim", address: Address { city: "Oslo", zip: "0150" } }
  println(ada.name, ada.address.city)
  println(tim.age)
  println(ada == ada.copy(age: 36))
}
```

- A record is built by naming its type and its fields. A field with a default (`age: Int = 18`) can be left out.
- Fields are read with a dot.
- `==` compares records by their contents.
- Printing a record shows its type name and fields, much as it is written in code.

When every field has a closed, eager default, the compiler checks the completed
default value against the type's whole-value invariant at its declaration. This
also applies to sealed variant defaults and concrete generic specializations.
Partial defaults and lazy/computed recipes are checked when a value is built.

Record fields and sealed variants can be separated by commas, newlines, or
semicolons. Leading, repeated, and trailing semicolons are accepted, even in
empty bodies. Blocks also accept these semicolons, but use newlines or
semicolons between statements rather than commas.

### Changed copies

Values never change. `copy` makes a new record with some fields replaced, and it can reach into nested records:

```bork
type Address = { city: String, zip: String }
type User = { name: String, address: Address }

fn main() {
  ada = User { name: "Ada", address: Address { city: "London", zip: "N1" } }
  moved = ada.copy(address.city: "Oslo", address.zip: "0150")
  println(ada.address.city, moved.address.city)
}
```

### Lazy and computed fields

A `lazy` field evaluates when first read and remembers its value. An independent
lazy field is supplied by the caller, or has a default that does not read other
fields. A computed lazy field has a default that reads sibling fields:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type Person = {
  first: String,
  last: String,
  lazy note: String,
  lazy full: String = s"$first $last",
} derive (codec.Encode, codec.Decode)

fn main() {
  ada = Person { first: "Ada", last: "Lovelace", note: "mathematician" }
  println(ada.full)
  renamed = ada.copy(last: "Byron")
  println(renamed.full, ada.full)
  println(renamed)
  println(json.Encode(renamed))
}
```

```text
Ada Lovelace
Ada Byron Ada Lovelace
Person { first: "Ada", last: "Byron", note: "mathematician" }
{"first":"Ada","last":"Byron","note":"mathematician"}
```

`full` is computed, so callers cannot supply it or override it with `copy`.
Change `first` or `last` instead. The changed copy recomputes `full` on demand;
the original keeps its value. Copies share unchanged independent lazy fields.
Computed fields share their cached value only when their dependencies are
unchanged, including dependencies through other computed fields.

Printing and derived encoding include independent lazy fields such as `note`
and force them if needed. Computed fields such as `full` are omitted from
printing, equality, hashing, and encoded data. Derived decoding reads the
independent data and reconstructs computed fields from their defaults.

Computed defaults must be pure, may read sibling fields, and cannot have a
dependency cycle. Lazy field types and [record rules](#records-with-rules) are
checked when constructing or copying the record. Laziness does not bypass a
fact requirement. Decoding validates the completed record before returning it;
validation can force a computed field used by a rule, and a failure is returned
as a decode error rather than delayed until a later read.

### Converting between records

`into` builds a record of another type from the fields that match, with overrides for the rest:

```bork
type User = { name: String, email: String, passwordHash: String }
type PublicUser = { name: String, email: String }
type Greeting = { name: String, text: String }

fn main() {
  user = User { name: "Ada", email: "ada@example.com", passwordHash: "x" }
  println(user.into[PublicUser]())
  println(user.into[Greeting](text: "Welcome back"))
}
```

See the runnable [record conversion example](../../examples/record_conversion/main.bork) for defaults,
renamed and nested overrides, fallible conversions, and a private target with a
whole-record invariant. Run it with `bork run examples/record_conversion`.

### Shorthand when the type is known

Where the expected type is already known, `.{ ... }` builds the record without repeating its name:

```bork
type Point = { x: Int, y: Int }

fn length2(p: Point): Int {
  p.x * p.x + p.y * p.y
}

fn main() {
  origin: Point = .{ x: 0, y: 0 }
  println(origin, length2(.{ x: 3, y: 4 }))
}
```

## Sealed types

A sealed type lists all its variants. A value is exactly one of them.

```bork
type Shape = sealed {
  Circle { radius: Float }
  Rect { width: Float, height: Float }
  Empty
}

fn area(shape: Shape): Float {
  match (shape) {
    Shape.Circle { radius } => 3.14159 * radius * radius
    Shape.Rect { width, height } => width * height
    Shape.Empty => 0.0
  }
}

fn main() {
  shapes: List[Shape] = [Shape.Circle { radius: 1.0 }, .Rect { width: 2.0, height: 3.0 }, .Empty]
  println(shapes.map(area))
}
```

Variants are written with the type's name in front: `Shape.Circle { radius: 1.0 }`. Where the type is known, the short form `.Rect { ... }` works too.

Because the list of variants is closed, `match` knows when every one has been handled. Add a variant and the compiler points at every `match` that needs a new arm. See [matching](matching.md).

Fieldless sealed types can derive `enum.Enum` from [bork/enum](../std/enum.md)
for declaration-order values, canonical wire names, exact lookup, and indices.
The derive template requires public variants; codec naming policies also apply
to enum lookup. Indices change when variants are reordered.

## Deriving instances

Records and sealed types can derive codecs with an inline list, or with one
package declaration per class:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type Settings = { name: String, retries: Int = 3 }
derive codec.Decode for Settings
derive codec.Encode for Settings

fn main() {
  println(json.Encode(Settings { name: "worker" }))
}
```

The declarations may live in a different file of the same package. They must
be in the package defining the underlying type or the class; an alias does not
change that owner. Private fields and variants remain private. Each stored
field needs the requested class's instance in scope. Defaults and facts are
checked by derived codec.Decode before it returns a value.

For a generic type, `derive codec.Decode for Box` derives an instance for all supported
element types. `derive codec.Decode for Box[Int]` requests just that specialization.
Requesting the same class and head twice, including once inline and once
standalone, is an error. Instances use the existing explicit `use` rules in
other packages. GoStruct supports standalone declarations for the record itself.

## Unions

A union type is written with `|` and holds a value of any one of its members. Unlike a sealed type, it needs no declaration: it combines types that already exist.

```bork
type NotFound = { id: Int }
type Forbidden = { reason: String }

fn load(id: Int): String | NotFound | Forbidden {
  if (id == 1) {
    "the document"
  } else if (id < 0) {
    Forbidden { reason: "negative ids are reserved" }
  } else {
    NotFound { id: id }
  }
}

fn main() {
  println(load(1))
  println(load(2))
}
```

Unions are how functions report failure. The result type lists the success value and every way the call can fail, and the caller must deal with each. See [matching and errors](matching.md).

A union can be given a name: `type Lookup = String | NotFound`.

A generic type parameter keeps its union membership. If `T` is `Int | String`,
a `t: T` pattern in a function taking `T | NotFound` matches integers and
strings; `NotFound` still reaches its own arm. The same applies to `?`: when
`T` is the success member, it keeps either integer or string values and
propagates `NotFound`.

## Option

`Option[T]` is a value that may be missing. It is an ordinary sealed type with the variants `Some(T)` and `None`. There is no null.

```bork
type User = { name: String, nickname: Option[String] }

fn display(u: User): String {
  u.nickname.getOr(u.name)
}

fn main() {
  ada = User { name: "Ada", nickname: "The Countess" }
  tim = User { name: "Tim", nickname: Option.None }
  println(display(ada), display(tim))
  scores = [10, 20]
  println(scores.get(5))
  println(scores.get(1).map(n => n + 1))
}
```

```text
The Countess Tim
Option.None
Option.Some(21)
```

Where an `Option[String]` is expected, a plain `String` is accepted and becomes `Some`. Option methods are `map`, `flatMap`, `getOr`, `isSome`, and `isNone`.

## Tuples

A tuple groups values by position, with a separate type for each element:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

fn labeled(n: Int): (Int, String) { (n, "count") }

fn main() {
  pair = labeled(3)
  (number, label) = pair
  println(number, label, pair.0)
  singleton = (true,)
  println(singleton.0)
  println(json.Encode(pair))
}
```

`(Int, String)` is a tuple type. Positions start at zero, and an out-of-range
selector is a compile error. `(value,)` and `(Type,)` are singleton tuples;
`(value)` and `(Type)` keep their grouping meaning. There is no empty tuple.
`() => value` still creates a function with no parameters.

Tuples evaluate their elements once, from left to right, and are immutable.
Their shape and ordered element types determine their type; no declaration is
needed. Generic functions can take or return tuples, and an expected tuple type
helps infer each element's type. Destructuring binds names or `_` and may nest;
refutable patterns belong in [match](matching.md).

Equality and map keys work when every element supports equality. Printing uses
tuple syntax. Codecs use JSON arrays with exactly the tuple's length; decode
errors identify positions such as `[1]`. Each element needs the corresponding
codec instance. Tuple aliases can derive codecs, but cannot derive `GoStruct`.
See [codecs](../std/codec.md) and [Go interop](go-interop.md) for those APIs.

## Ok

`Ok` is the result of a function that succeeds without a value to return. It mostly appears in unions: `Ok | SaveError` means "it worked, or here is why not".

```bork
type TooLong = { limit: Int }

fn validate(name: String): Ok | TooLong {
  if (name.byteLength() > 10) {
    return TooLong { limit: 10 }
  }
}

fn main() {
  println(validate("Ada"))
  println(validate("Augusta Ada King"))
}
```

A body that ends without a value gives `Ok`. It can also be written out as the expression `Ok`.

## Generics

Types and functions can take type parameters, in square brackets.

```bork
type Pair[A, B] = { first: A, second: B }

type Tree[T] = sealed {
  Leaf
  Node { left: Tree[T], value: T, right: Tree[T] }
}

fn swap[A, B](p: Pair[A, B]): Pair[B, A] {
  Pair { first: p.second, second: p.first }
}

fn size[T](tree: Tree[T]): Int {
  match (tree) {
    Tree.Leaf => 0
    Tree.Node { left, right } => size(left) + 1 + size(right)
  }
}

fn main() {
  println(swap(Pair { first: 1, second: "one" }))
  leaf = Tree[Int].Leaf
  println(size(Tree.Node { left: leaf, value: 7, right: leaf }))
}
```

Type arguments are usually worked out from the values. When there is nothing to work them out from, write them: `Tree[Int].Leaf`, or `xs: List[Int] = []`.

Union members can coincide after specialization. For example, `T | Int` becomes `Int` when `T` is `Int`. This also works inside callback signatures and container elements, including when a generic function is saved as a function value. Generic record and sealed-variant fields follow the same rule, including lazy fields and fields replaced by `copy`.

```bork
fn apply[T](witness: T, callback: (T | Int) => Int, value: T | Int): Int {
  callback(value)
}

fn main() {
  println(apply[Int](0, n => n + 1, 2))
}
```

A type parameter can require a type class, as in `fn largest[T: Ord](xs: List[T])`. See [packages](packages.md#type-classes).

## Type aliases

`type` can also name an existing type. This is most useful with a `where` clause, which makes a type that carries a [fact](facts.md):

```bork
pred positive(x: Int) { x > 0 }

type Quantity = Int where positive
type Basket = Map[String, Int]

fn main() {
  q: Quantity = 3
  basket: Basket = { "apple": q }
  println(basket)
}
```

Aliases can take type parameters to name a repeated shape:

```bork
type Index[T] = Map[String, List[T]]
type Missing = {}
type Result[T] = T | Missing

fn first[T](items: List[T]): Result[T] {
  match items.get(0) {
    Option.Some(value) => value
    Option.None => Missing {}
  }
}

fn main() {
  index: Index[Int] = { "numbers": [1, 2] }
  println(index, first([3]))
}
```

Supply every type argument when using an alias: `Index[Int]`. An alias is
transparent: it has the identity, construction rules and effects of the type
it expands to, with no wrapper or conversion. Union members keep their order,
so `Result[T]` keeps `T` as the success type for `?`. Alias cycles are errors.
For an alias of a record or sealed type, write explicit arguments on its
constructor head, such as `Alias[Int] { value: 1 }`, or use `.{ value: 1 }`
with an expected type.

Facts follow their positions in the expansion. For `type Items[T] = List[T]`,
`Items[Int where positive]` requires positive elements, just like
`List[Int where positive]`. Expanding a fact into an unsupported position,
such as a `Map` key or value, is still an error.

## Records with rules

A record can state what must be true of its fields. A `where` on a field may refer to the other fields, and a `where` after the record applies to the whole value.

```bork
pred atLeast(x: Int, min: Int) { x >= min }
pred short(r: Range) { r.hi - r.lo <= 100 }

type Range = { lo: Int, hi: Int where atLeast(lo) } where short

fn main() {
  println(Range { lo: 1, hi: 10 })
}
```

The compiler checks these rules wherever a `Range` is built or copied, so every `Range` in the program satisfies them. Writing `Range { lo: 10, hi: 1 }` does not compile. See [facts](facts.md).

## Typed package tags

A package can define `FieldTags`, `VariantTags`, and `TypeTags` records to
configure derivation. Write its tag group after a field, variant, or type body.
The compiler checks the entries as a record literal, including defaults and
field rules. Values must be closed, like field defaults. Each package can have
one group at a given declaration; different packages can have separate groups.

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults

type User = {
  userID: String codec { name: "login", aliases: ["oldLogin"] }
} codec { unknown: codec.Unknown.Reject } derive (codec.Encode, codec.Decode)

type Role = sealed {
  SiteAdmin
  ReadOnly codec { name: "READER" }
} derive (codec.Encode, codec.Decode)

fn main() {
  println(json.Render(codec.encode(User { userID: "42" })))
  println(json.Render(codec.encode(Role.SiteAdmin)))
  println(json.Render(codec.encode(Role.ReadOnly)))
}
```

```text
{"login":"42"}
"SITE_ADMIN"
"READER"
```

Source field and variant names remain unchanged in the program. Codec tags
control wire names and decode-only aliases; payload-free enums use UPPER_SNAKE
names by default. See [codec](../std/codec.md#wire-names) for naming policies,
omission and unknown keys.

A tag group must start on the same line as its declaration, although its body
can span lines. The package name resolves through its import alias, or to the
current package. Resource and alias types do not accept typed tag groups.
`go { ... }` groups keep their String-valued Go struct-tag syntax. Derive
templates read checked groups with `field.tagged[M]()`, `variant.tagged[M]()`,
`shape.tagged[T, M]()`, or the field's `tagGroups` descriptors; see
[shape](../std/shape.md#fields-facts-and-tags).

## Controlling construction

A record marked `private` can be read by everyone but built only by its own package. Other packages get values through the functions that package offers, so it can guarantee how its values are made.

```bork fragment
// in package settings
type Config = private { host: String, port: Int = 8080 }

fn New = Config.new
```

`fn New = Config.new` generates a constructor function from the record's fields, defaults, and rules. Other packages then write `settings.New(host: "example.com")`. The [config example](../../examples/config/README.md) shows this in full.

---

Previous: [Basics](basics.md) · Next: [Matching and errors](matching.md) · [All pages](../README.md#the-language)
