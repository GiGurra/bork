# bork/log

## Structured logging

`bork/log`, structured logging through Go's `log/slog`: `log.Info(msg)` or `log.Info(msg, {"user": name, "age": 37})` (also `Debug`, `Warn`, `Error`), attributes a `log.Attrs` (`Map[String, String | Int | Float | Bool]`) kept in order, loggers that carry attributes (`log.With(attrs)`, `log.Extend`, `logger |> log.Log(level, msg, attrs)`), and `log.Configure(log.Defaults().copy(...))` for the level, text or JSON, stdout or stderr, and timestamps. The runtime's own records (orphaned tasks, a scope's failures under `logFailures()`) go through the same configuration.
