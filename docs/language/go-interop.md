# Calling Go

bork programs compile to Go, and a bork package can use Go code directly. Here the compiler's guarantees depend on what you write, so Go code has to be switched on per package.

Most programs never need this page.

## Switching it on

Go code is only allowed in packages that the module's `bork.mod` lists as `unsafe`:

```text
module example.com/tools
unsafe "example.com/tools/text"
```

`bork.mod` therefore lists every place where Go code can be. A program without a `bork.mod` cannot contain Go at all.

## Binding a Go function

The shortest form names an existing Go function. The compiler reads the Go function's signature and checks that the bork one fits it.

The declaration `fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`
binds to Go's `strconv.Atoi`. For an effectful function, include its effects:
`fn Getenv(key: String) uses io: String unsafe go "os.Getenv"`.

The complete [binding fixture](../../testdata/cases/go_bindings/main.bork)
includes the module's [unsafe grant](../../testdata/cases/go_bindings/bork.mod).
Run it with `bork run testdata/cases/go_bindings` from a checkout.

The string is the Go import path, a dot, and the function name. Values are converted at the boundary:

| Go | bork |
| --- | --- |
| integers, floats, `string`, `bool` | the number types, `String`, `Bool` |
| slices, and arrays coming from Go | `List[T]` |
| maps | `Map[K, V]` |
| `[]byte` | `Bytes` |
| pointers | `Option[T]` |
| an `error` result | `GoError` in the result union |
| a `(value, bool)` result | `Option[T]` |

A Go function that returns `(int, error)` therefore becomes `Int | GoError`, and the caller handles the error like any other bork failure.

## Writing Go inline

Use `unsafe go { ... }` after a function signature to write its body in Go.
The [inline-Go fixture](../../testdata/cases/unsafe_go_ok/main.bork) provides
complete examples: a numeric calculation, an error union, an optional result
and a function that prints. Its [bork.mod](../../testdata/cases/unsafe_go_ok/bork.mod)
grants unsafe access to the package. Run it with
`bork run testdata/cases/unsafe_go_ok` from a checkout.

Imports go on the first lines of the body. Parameters are available under their
own names. The Go compiler checks the body and reports errors at the relevant
place in the `.bork` file. Other packages call these functions like ordinary
bork functions; only the implementing package needs an unsafe grant.

## What you are responsible for

The signature of an `unsafe go` function is a promise that the compiler takes on trust:

- **Effects.** Declare what the Go code does. The compiler catches the obvious cases, such as Go code that uses `os` or `net` without `uses io` or `uses net`, but it cannot see everything.
- **Facts.** If the result type has a `where` clause, the Go code must make it true. `bork test` checks such promises as the code runs.
- **Immutability.** bork values must not be modified from Go.

## Go types

A Go type can be given a bork name and passed around without bork looking inside it.

For example, `type Request = go "*net/http.Request"` names an opaque Go request
pointer. This declaration also needs the package's unsafe grant.

A value that must be closed, such as a file or a connection, is declared as a `resource`. It then belongs to a [scope](scopes.md), which closes it.

## Generated structs and Go tags

`derive (GoStruct)` gives a record a generated Go struct and checked conversions
in both directions. Stored fields become exported Go fields; `go { ... }` adds
struct tags. Here `User.name` becomes Go's `Name` with `json:"display_name"`.

```bork
type User = {
  name: String go { json: "display_name" }
  age: Int go { json: "age,omitempty" }
} derive (GoStruct)

fn main() {
  println(User { name: "Ada", age: 36 })
}
```

Deriving the layout needs no unsafe grant. Calling Go with that layout does.
In an unsafe-enabled package, a generic adapter with `[T: GoStruct]` can pass
`_d_T_GoStruct.ToGo(value)` to a Go library. For example, `encoding/json.Marshal`
sees the exported fields and their JSON tags. `FromGo` returns a bork value and
conversion errors, including failed field facts; publish the value only when
the error list is empty. See [Generated Go structs](../std-go.md#generated-go-structs)
for `New`, `ToGo`, `FromGo` and `Fields`. The runnable
[GoStruct fixture](../../testdata/cases/go_struct/main.bork) uses Go
`encoding/json.Unmarshal` through `New` and `FromGo`, and prints field tags
through `Fields`.

Nested ordinary records also need `GoStruct`. Optional fields map to nil
pointers. Computed fields are omitted. Resources and fields with unresolved
type parameters cannot become generated Go fields.

A record can instead mirror an existing struct by listing the fields it needs,
as in `type Url = go "net/url.URL" { host: String }`. A mirror keeps the Go
struct's existing field names and tags; it cannot add tags.

## Third-party Go modules

To use a Go library that is not in Go's standard library, pin it for the module with `bork deps`:

```sh
bork deps init
bork deps get github.com/google/uuid@v1.6.0
```

This records `require` lines in `bork.mod`, checksums in `bork.sum`, and a generated `go.mod`. Commit all three. If generated requirements drift (for example after a manual `go get`), run `bork deps download` to restore them from `bork.mod`. See [the deps command](../cli.md#deps).

## More

The [Go helper reference](../std-go.md) describes the helper API that Go bodies can use for options, maps, and scopes. The standard packages under [internal/std](../../internal/std) are full-size examples of all of the above.

---

Previous: [Derivation templates](derivation.md) · Next: [Testing](testing.md) · [All pages](../README.md#the-language)
