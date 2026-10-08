# Adding error context

Attach a mapper to one `?` when a caller needs to know which operation failed:

```bork
type ReadError = { message: String }
type LoadError = { path: String, cause: ReadError }

fn read(path: String): String | ReadError {
  if path == "config" { "port=8080" } else { ReadError { message: "not found" } }
}

fn load(path: String): String | LoadError {
  text = read(path)?{ error => LoadError { path: path, cause: error } }
  text.trim()
}

fn main() uses io {
  println(load("config"))
  println(load("missing"))
}
```

`?{ error => value }` keeps the same success value as bare `?`. On failure,
it binds `error`, evaluates the mapper's body, and returns its result from
`load` immediately. The low-level `ReadError` need not appear in `load`'s
result type. `LoadError` is an ordinary record; there is no special error type.

A direct binding annotation selects the same member for wrapped and bare `?`.
For example, `miss: CacheMiss = cache.get(key)?{ hit => Saved { value: hit } }`
keeps `CacheMiss`; the mapper receives all other members, including the usual
first member. Facts on the selected member remain available after the binding.

The operand runs once. Success skips the mapper's body. Failure runs it once,
including any calls that build context. Captures are available as in an ordinary
lambda; effects such as logging must be declared by the enclosing function.

When an operand returns `Value | Missing | Busy`, the mapper parameter has type
`Missing | Busy`. It receives whichever failure occurred. Use `match` in the
body to treat the failures differently, or put the entire failure union in a
wrapper field. The mapper's result may itself be a union, provided every member
fits the enclosing function's result. It may also return a success-shaped value:
that still exits the enclosing function immediately, rather than resuming after
`?`.

For `Option`, discard the parameter and construct the context you need:

```bork
type NotFound = { index: Int }

fn first(words: List[String]): String | NotFound {
  words.get(0)?{ _ => NotFound { index: 0 } }
}

fn main() uses io {
  println(first(["hello"]))
  println(first([]))
}
```

The successful `Some` payload is kept; `None` runs the mapper. Use `_` for a
payloadless failure, including a sole `Ok` failure in an ordinary union.

Facts on the kept value remain available after `?`. The mapped return must
satisfy the enclosing function's result predicates, and constructing a wrapper
must prove its field predicates. Facts established about a failure are available
through the mapper parameter. Resource lifetimes and moves obey the same rules
as explicit matching and returning.

The mapper is a lambda: its trailing expression supplies its result. An explicit
`return`, nested `?`, or `break`/`continue` targeting an outer loop is rejected.
Use `match` or `if` inside the body when it needs several paths. Panic is allowed.

Write the opening brace immediately after `?`, as `?{`. Outside control heads,
`? {` produces a diagnostic with a fix to attach the brace. In an
unparenthesized control head, a brace after `?` always opens the control body,
even when attached: `match value?{ _ => ... }` is a match on bare `value?`.
Parenthesize the wrapped expression instead:
`match (value?{ e => Wrapped { cause: e } }) { ... }`.

See [Matching and errors](matching.md) for bare `?` and
[an error-context example](../../examples/error_context/main.bork) you can run.

---

Previous: [Matching and errors](matching.md) · Next: [Collections](collections.md) · [All pages](../README.md#the-language)
