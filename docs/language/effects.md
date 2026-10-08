# Effects

An effect is something a function does besides computing its result: printing, reading a file, calling a server, reading the clock. In bork, a function's signature lists its effects, and the compiler checks the list.

## `uses`

```bork
fn total(prices: List[Int]): Int {
  prices.fold(0, (sum, p) => sum + p)
}

fn printTotal(prices: List[Int]) uses io {
  println(s"total: ${total(prices)}")
}

fn main() {
  printTotal([450, 1250])
}
```

`total` declares no effects, so it is **pure**: given the same arguments it gives the same result, and it does not touch the outside world. `printTotal` prints, so it says `uses io`.

`uses` comes after the parameters and before the result type. Several effects are joined with `+`: `uses io + net`.

The effects are:

| Effect | Meaning |
| --- | --- |
| `io` | Files, standard output, environment variables, processes |
| `net` | Network connections |
| `clock` | Reading the time, sleeping, timers |
| `random` | Random numbers |
| `state` | Shared state that can change: atoms, channels, cancellation |

A sixth effect, `build`, marks functions that read files during compilation. It is only allowed inside [`comptime`](comptime.md).

`tailrec` may also appear in a function's list (`uses io + tailrec`). It is not an effect but a marker: it asks the compiler to guarantee that the function's calls of itself are compiled as jumps (see [recursion and tail calls](basics.md#recursion-and-tail-calls)). `uses tailrec` alone declares a pure function.

## The compiler checks the list

A function may only do what it declares. That includes what the functions it calls do.

```bork fails
fn total(prices: List[Int]): Int {
  println("adding up")
  prices.fold(0, (sum, p) => sum + p)
}
```

```text
total uses io (it calls println), but its signature allows no effects; declare it: uses io
```

Private functions must also use every effect they declare. This catches a
signature that promises more outside work than its implementation performs:

```bork fails
fn announce(text: String) uses io + net {
  println(text)
}
```

```text
announce declares net, but never uses it
```

Remove `net` here. Exported functions may declare unused effects to keep their
public contract stable.

If a function has no `uses`, nothing it calls can read a file or use the network, however deep its call tree goes.

Three things are not tracked as effects: logging with [bork/log](../std/log.md), the development helper `dbg`, and panics. A pure function can do those. Go code is the other limit, since the effects of an [`unsafe go`](go-interop.md) function are declared by its author.

`main` and tests may use every run-time effect without declaring them. If `main` does declare `uses`, it is held to it.

## Functions passed as arguments

Methods such as `map` and `forEach` take a function. They do not have effects of their own: a call has whatever effects the function passed to it has.

```bork
fn lengths(words: List[String]): List[Int] {
  words.map(w => w.byteLength())
}

fn printAll(words: List[String]) uses io {
  words.forEach(w => println(w))
}

fn main() {
  println(lengths(["a", "bc"]))
  printAll(["a", "bc"])
}
```

`lengths` stays pure, because its lambda is pure. `printAll` needs `uses io`, because its lambda prints.

Your own functions work the same way. A function-typed parameter accepts a function with any effects, and the caller is charged for them. To accept only pure functions, write `uses nothing` in the parameter's type. To allow specific effects and no others, name them, as in `(String) uses io => Ok`.

```bork
fn twice(f: (Int) => Int, x: Int): Int {
  f(f(x))
}

fn twicePure(f: (Int) uses nothing => Int, x: Int): Int {
  f(f(x))
}

fn main() {
  println(twice(n => n + 1, 0))
  println(twicePure(n => n * 2, 1))
}
```

## Ambient values

Some values belong to a whole request: a trace id, the signed-in user, a locale. An ambient value is an alternative to passing them through every function as a parameter. It is declared once, bound for a block of code, and read by the functions that say they need it.

```bork
ambient requestId: String

fn log(message: String) uses io needs requestId {
  println(s"[$requestId] $message")
}

fn handle(path: String) uses io needs requestId {
  log(s"handling $path")
}

fn main() {
  with (requestId: "req-42") {
    handle("/users")
  }
}
```

- `ambient requestId: String` declares the value and its type.
- `needs requestId` in a signature lets the function read it by name. It comes after `uses`.
- `with (requestId: ...) { ... }` binds it for the block.

This is still checked. A function that needs a value can only be called from inside a `with` that binds it, or from another function that needs it. `handle` declares the need because it calls `log`. Leaving out the `with` in `main` is a compile error.

### Optional needs

`needs locale?` reads the ambient value as an `Option`. Callers can omit it:

```bork
ambient locale: String

fn greeting() needs locale?: String {
  match locale {
    Option.Some("sv") => "hej"
    _ => "hello"
  }
}

fn main() {
  println(greeting())
  with (locale: "sv") {
    println(greeting())
  }
}
```

### Logged and propagated values

`logged ambient` adds the bound value to log records, including records written
by functions that do not declare a need for it:

```bork
import "bork/log"

logged ambient requestId: String

fn handle() {
  log.Info("handling request")
}

fn main() {
  log.Configure(log.Defaults().copy(timestamps: false))
  with (requestId: "req-42") {
    handle()
  }
}
```

`propagated("header-name") ambient` sends a bound value as a header on outgoing
HTTP calls. Here the binding covers the request, including any helper calls:

```bork
import "bork/http"

propagated("X-Request-Id") ambient requestId: String

fn main() {
  scope app {
    match http.Listen("127.0.0.1:0", app, (request, s) => {
      id = http.HeaderOf(request.headers, "X-Request-Id").getOr("missing")
      http.Text(200, id)
    }) {
      server: http.Server => {
        with (requestId: "req-42") {
          match http.Get("http://" + http.Address(server), app) {
            response: http.Response => println(response.body)
            failure => println(failure)
          }
        }
      }
      failure => println(failure)
    }
  }
}
```

The [service_context example](../../examples/service_context/main.bork) shows a
trace id passing between two services. A bork HTTP server validates incoming
propagated values and binds valid ones around the handler. A missing or invalid header leaves the optional value
unbound; handle that case with `needs requestId?` before calling code that
requires it.

---

Previous: [Facts](facts.md) · Next: [Scopes and tasks](scopes.md) · [All pages](../README.md#the-language)
