# bork/log

`bork/log` writes structured text or JSON records with levels, attributes and optional ambient context.

Disable timestamps for repeatable output and select the destination explicitly:

```bork
import "bork/log"

fn main() {
  log.Configure(log.Defaults().copy(output: .Stdout, timestamps: false))
  log.Info("signed up", { "user": "Ada", "age": 37 })
  log.Debug("hidden by the Info threshold")
  log.Error("signup failed", { "reason": "duplicate user" })
}
```

Output:

```text
level=INFO msg="signed up" user=Ada age=37
level=ERROR msg="signup failed" reason="duplicate user"
```

## API

Names below belong to `log`. Writing a log has no declared effect; configuring the global output uses io.

| Signature | Meaning |
| --- | --- |
| `Defaults(): Config` | Info threshold, text, stderr, timestamps enabled. |
| `Configure(config: Config) uses io` | Set the whole program's logging configuration. |
| `Debug(msg: String, attrs: Attrs = {:})` | Debug record. |
| `Info(msg: String, attrs: Attrs = {:})` | Informational record. |
| `Warn(msg: String, attrs: Attrs = {:})` | Warning record. |
| `Error(msg: String, attrs: Attrs = {:})` | Error record. |
| `With(attrs: Attrs): Logger` | Make a logger carrying attributes. |
| `Extend(logger: Logger, attrs: Attrs): Logger` | Add attributes to a logger. |
| `Log(logger: Logger, level: Level, msg: String, attrs: Attrs = {:})` | Write with logger and per-call attributes. |

`Attrs` is `Map[String, String | Int | Float | Bool]`; attributes retain map order. `Logger` has `{ attrs: Attrs }`.

| `Config` field | Values/default |
| --- | --- |
| `level: Level` | `Debug`, `Info`, `Warn`, `Error`; default Info. Records below it are dropped. |
| `format: Format` | `Text`, `Json`; default Text. JSON writes one object per line. |
| `output: Output` | `Stderr`, `Stdout`; default Stderr. |
| `timestamps: Bool` | Default true; adds the record time. |

## Carry attributes

A logger can carry request or component attributes through a helper. Later attributes replace matching keys when extending or writing with the logger.

```bork
import "bork/log"

fn main() {
  log.Configure(log.Defaults().copy(format: .Json, output: .Stdout, timestamps: false))
  logger = log.With({ "component": "signup" })
  request = log.Extend(logger, { "request": "r1" })
  log.Log(request, .Info, "accepted", { "status": 201 })
}
```

## Ambient and runtime records

A `logged ambient` declaration adds its bound value to records even in functions that do not declare typed `needs` for it:

```bork
import "bork/log"
logged ambient requestId: String

fn helper() { log.Info("working") }

fn main() {
  log.Configure(log.Defaults().copy(output: .Stdout, timestamps: false))
  with (requestId: "r1") { helper() }
}
```

Ambient attributes appear before explicit attributes. See [effects](../language/effects.md) for ambient binding. Runtime records, including orphaned tasks and scope failures under `logFailures()`, use the same configuration. Logging delegates to Go's `log/slog`.
