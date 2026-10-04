# Calling Go

bork programs compile to Go, and a bork package can use Go code directly. This is how the standard packages are built. It is also the one place where the compiler's guarantees depend on what you write, so it is fenced in and has to be switched on.

Most programs never need this page.

## Switching it on

Go code is only allowed in packages that the module's `bork.mod` lists as `unsafe`:

```text
module example.com/tools
unsafe "example.com/tools/text"
```

So a look at `bork.mod` shows where Go code can be, and a new entry there shows up in review. A program without a `bork.mod` cannot contain Go at all.

## Binding a Go function

The shortest form names an existing Go function. The compiler reads the Go function's signature and checks that the bork one fits it.

```bork fragment
fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"

fn Getenv(key: String) uses io: String unsafe go "os.Getenv"
```

The string is the Go import path, a dot, and the function name. Values are converted at the boundary:

| Go | bork |
| --- | --- |
| integers, floats, `string`, `bool` | the number types, `String`, `Bool` |
| slices and arrays | `List[T]` |
| maps | `Map[K, V]` |
| `[]byte` | `Bytes` |
| pointers | `Option[T]` |
| an `error` result | `GoError` in the result union |
| a `(value, bool)` result | `Option[T]` |

A Go function that returns `(int, error)` therefore becomes `Int | GoError`, and the caller handles the error like any other bork failure.

## Writing Go inline

A function body can also be written in Go:

```bork fragment
// Title upper-cases the first letter of each word.
fn Title(s: String): String unsafe go {
  import "strings"
  words := strings.Fields(s)
  for i, w := range words {
    words[i] = strings.ToUpper(w[:1]) + w[1:]
  }
  return strings.Join(words, " ")
}
```

Imports go on the first lines of the body. The parameters are available under their own names. bork does not check the Go code. The Go compiler does, and its errors are reported at the right place in the `.bork` file.

Other packages call these like any bork function:

```bork fragment
import "example.com/tools/text"

fn main() {
  println(text.Title("hello from go"))
  println(text.Atoi("42"), text.Atoi("x"))
}
```

## What you are responsible for

The signature of an `unsafe go` function is a promise that the compiler takes on trust:

- **Effects.** Declare what the Go code does. The compiler catches the obvious cases, such as Go code that uses `os` or `net` without `uses io` or `uses net`, but it cannot see everything.
- **Facts.** If the result type has a `where` clause, the Go code must make it true. `bork test` checks such promises as the code runs.
- **Immutability.** bork values must not be modified from Go.

## Go types

A Go type can be given a bork name and passed around without bork looking inside it:

```bork fragment
type Request = go "*net/http.Request"
```

A value that must be closed, such as a file or a connection, is declared as a `resource`. It then belongs to a [scope](scopes.md), which closes it.

For Go libraries that work on structs, a record can `derive (GoStruct)`. The compiler then generates a matching Go struct and the conversions in both directions. A record can also mirror an existing Go struct by listing the fields it wants: `type Url = go "net/url.URL" { host: String }`.

## Third-party Go modules

To use a Go library that is not in Go's standard library, pin it for the module with `bork deps`:

```sh
bork deps init
bork deps get github.com/google/uuid@v1.6.0
```

This writes `go-deps.mod` and `go-deps.sum` next to `bork.mod`. Commit them. See [the deps command](../cli.md#deps).

## More

The [Go helper reference](../std-go.md) describes the helper API that Go bodies can use for options, maps, and scopes. The standard packages under `internal/std` are full-size examples of all of the above.

---

Previous: [Packages](packages.md) · Next: [Testing](testing.md) · [All pages](../README.md#the-language)
