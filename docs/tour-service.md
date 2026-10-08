# A service tour

Build a SQLite-backed JSON API for notes. The application loads configuration, validates JSON into fact-constrained types, commits writes, maps failure unions to HTTP responses, and cleans up its database and listener on shutdown. No external database service is required.

Start with the [first tour](tour.md) if records, `match`, `uses`, or scopes are new to you. This tour's complete source and direct handler tests are in [examples/service_tour](../examples/service_tour/main.bork).

## Run it first

From a checkout of the bork repository:

```sh
bork run examples/service_tour -- demo
bork test examples/service_tour
```

The demo starts on an available localhost port with an in-memory database. It makes real HTTP requests and closes the application scope before returning:

```text
GET /health -> 200 ok
level=INFO msg="note created" id=1
POST /notes -> 201 {"id":1,"title":"learn bork"}
empty title -> 422 {"path":".title","message":"must be nonEmpty"}
broken JSON -> 400 {"path":"","message":"unexpected end of JSON: expected a field name"}
GET /notes -> 200 [{"id":1,"title":"learn bork"}]
application scope closed
```

Logs go to stderr; the remaining lines go to stdout. The [checked transcript](../testdata/examples/service_tour.txt) includes both streams. The demo disables timestamps so it is reproducible.

For a persistent database and a listener that stays running:

```sh
TOUR_ADDRESS=127.0.0.1:8080 TOUR_DATABASE=notes.db bork run examples/service_tour -- serve
```

Both settings have defaults: `127.0.0.1:8080` and `notes.db`. The database path is relative to your working directory. SQLite creates the file; its parent directory must exist. The first build may download the bundled SQLite driver dependencies.

In another terminal:

```sh
curl -i -H 'Content-Type: application/json' -d '{"title":"learn bork"}' http://127.0.0.1:8080/notes
curl -i http://127.0.0.1:8080/notes
curl -i -H 'Content-Type: application/json' -d '{"title":""}' http://127.0.0.1:8080/notes
```

The responses are 201 with the saved note, 200 with the list, and 422 with the field error. The first note has id 1 in a fresh database; an existing database keeps its earlier notes. Ctrl+C stops the listener and cleans up the application.

## Validate data once

The service declares `Title = String where nonEmpty and short`. `NewNote` derives `codec.Decode`, so `http.Body[NewNote](req)` checks both predicates before returning a `NewNote`. An empty title or one longer than 100 bytes fails decoding; byte length is not a character count.

The following standalone program demonstrates the same boundary without HTTP:

```bork
import "bork/codec"
import "bork/json"
use codec.Defaults
pred nonEmpty(value: String) { value.byteLength() > 0 }
pred short(value: String) { value.byteLength() <= 100 }
type Title = String where nonEmpty and short
type NewNote = { title: Title } derive (codec.Decode)
fn main() {
  result: NewNote | json.JsonError | codec.DecodeError = json.Decode("{\"title\":\"learn bork\"}")
  println(result)
}
```

After decoding, `insert` can use the title without checking it again. SQL rows are decoded into `Note`, which carries the same title facts. Invalid stored data therefore returns `codec.DecodeError` instead of silently constructing an invalid note. See [facts](language/facts.md) and [JSON](std/json.md).

## Configure the application

`Config` derives `codec.Decode`. `env.Load[Config]("TOUR")` maps `address` to `TOUR_ADDRESS` and `database` to `TOUR_DATABASE`. Missing variables use record defaults. An explicitly empty variable violates `nonEmpty`; invalid settings produce `env.ConfigError` before any resources are opened.

The example's configuration test uses `env.LoadWith` and a pure lookup function, so it never changes the process environment. See [environment configuration](std/env.md).

## Keep SQL typed and transactional

`sql.SQL"INSERT INTO notes(title) VALUES (${note.title})"` binds the title as a parameter. It does not paste the title into SQL text. Quotes in a title cannot change the query's structure.

`Query[Note]` decodes column names into record fields. The query selects `id` and `title` explicitly, matching the record. `last_insert_rowid()` runs on the same transaction as the insert, so another request cannot replace the id being read.

A write begins a request-owned transaction, inserts, reads back the row, and commits. Every earlier return rolls the transaction back when the request scope closes. Listing uses its own request-owned transaction too; its read-only transaction rolls back on scope exit. SQLite's pool uses one connection, which preserves the in-memory database and serializes database access. This example does not implement schema migrations or database-level enforcement of the title predicates; applications that change the schema need their own migration policy. See [SQL](std/sql.md).

## Map results to responses

The handler matches every outcome at the boundary:

| Outcome | HTTP status | Response |
| --- | --- | --- |
| Saved `Note` | 201 | Encoded note |
| Listed `List[Note]` | 200 | Encoded list |
| `json.JsonError` | 400 | JSON syntax error |
| Input `codec.DecodeError` | 422 | Field path and failed validation |
| `sql.Error` or stored-row `codec.DecodeError` | 500 | Generic storage error; details go to the log |
| Unsupported method on `/notes` | 405 | Use GET or POST |
| Unknown path | 404 | Not found |

This API uses 422 for structurally valid JSON that fails the record decoder, including missing fields and wrong field types. The error union tells you what happened; your application chooses the HTTP policy. Database internals are logged instead of being sent to clients.

## Own resources at the right lifetime

The application scope owns the database pool and HTTP listener. Each handler receives a separate request scope from `http.Listen`. The handler attaches the pool to that request before passing it to `route`; `db: sql.Connection in request` declares that the connection must stay live until that request closes, because a transaction may retain it.

Attachment adds an owner; it does not open a second pool or move ownership away from the application. The request owns its transaction. The pool remains available for later requests after a request ends. No resource handle is returned in an HTTP response: responses contain immutable values and encoded text.

The listener sets a 2-second request timeout. Both read and write transactions use the request's cancellation context. Cancellation is cooperative: it does not force arbitrary handler code to stop, and a cancelled SQL operation may reach the storage-error response path. Keep cancellable work in the request scope. See [scopes](language/scopes.md) and [HTTP](std/http.md).

## The complete application

This is a complete file. Save it as `main.bork`; `bork run main.bork -- demo` runs the self-contained demonstration, and `bork run main.bork -- serve` starts the persistent service.

```bork
// bork run examples/service_tour -- demo
// TOUR_ADDRESS=127.0.0.1:8080 TOUR_DATABASE=notes.db bork run examples/service_tour -- serve
// curl -H 'Content-Type: application/json' -d '{"title":"learn bork"}' localhost:8080/notes
import "bork/codec"
import "bork/env"
import "bork/http"
import "bork/json"
import "bork/log"
import "bork/process"
import "bork/sql"
use codec.Defaults

pred nonEmpty(value: String) { value.byteLength() > 0 }
pred short(value: String) { value.byteLength() <= 100 }
type Title = String where nonEmpty and short
type NewNote = { title: Title } derive (codec.Decode)
type Note = { id: Int, title: Title } derive (codec.Decode, codec.Encode)
type Problem = { path: String, message: String } derive (codec.Encode)
type Config = {
  address: String where nonEmpty = "127.0.0.1:8080",
  database: String where nonEmpty = "notes.db",
} derive (codec.Decode)

fn setup(db: sql.Connection) uses io + net: Ok | sql.Error {
  _ = sql.SQL"CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, title TEXT NOT NULL)".Exec(db)?
}

// A request owns its transaction. Commit makes the row durable; any earlier
// return rolls it back. The connection pool stays in the application's scope.
fn insert(db: sql.Connection in request, request: Scope, note: NewNote) uses io + net: Note | sql.Error | codec.DecodeError {
  tx = sql.Begin(db, request)?
  _ = sql.SQL"INSERT INTO notes(title) VALUES (${note.title})".Exec(tx)?
  rows = sql.SQL"SELECT id, title FROM notes WHERE id = last_insert_rowid()".Query[Note](tx)?
  match (rows) {
    [saved] => { sql.Commit(tx)?; saved }
    _ => sql.Error { operation: "insert", message: "expected one inserted note" }
  }
}

fn storageError(error: sql.Error | codec.DecodeError): http.Response {
  log.Error("storage failed", { "error": toString(error) })
  http.JsonReply(500, json.Encode(Problem { path: "", message: "storage unavailable" }))
}

fn create(db: sql.Connection in request, req: http.Request, request: Scope) uses io + net: http.Response {
  match (http.Body[NewNote](req)) {
    note: NewNote => match (insert(db, request, note)) {
      saved: Note => {
        log.Info("note created", { "id": saved.id })
        http.JsonReply(201, json.Encode(saved))
      }
      error: sql.Error | codec.DecodeError => storageError(error)
    }
    error: json.JsonError => http.JsonReply(400, json.Encode(Problem { path: "", message: error.message }))
    error: codec.DecodeError => http.JsonReply(422, json.Encode(Problem { path: error.path, message: error.message }))
  }
}

fn list(db: sql.Connection in request, request: Scope) uses io + net: List[Note] | sql.Error | codec.DecodeError {
  tx = sql.Begin(db, request)?
  sql.SQL"SELECT id, title FROM notes ORDER BY id".Query[Note](tx)
}

fn route(db: sql.Connection in request, req: http.Request, request: Scope) uses io + net: http.Response {
  match ((req.method, req.path)) {
    ("GET", "/health") => http.Text(200, "ok")
    ("POST", "/notes") => create(db, req, request)
    ("GET", "/notes") => match (list(db, request)) {
      notes: List[Note] => http.JsonReply(200, json.Encode(notes))
      error: sql.Error | codec.DecodeError => storageError(error)
    }
    (_, "/notes") => http.Text(405, "use GET or POST")
    _ => http.Text(404, "not found")
  }
}

fn start(config: Config, app: Scope) uses io + net + clock + state: http.Server | sql.Error | IoError {
  db = sql.OpenSqlite(config.database, app)?
  setup(db)?
  http.Listen(config.address, app, handler: (req, request) => route(attach(db, request), req, request), drainTimeout: 5.seconds(), requestTimeout: 2.seconds())
}

fn serve() uses io + net + clock + state: Ok | env.ConfigError | sql.Error | IoError {
  config = env.Load[Config]("TOUR")?
  scope app with cleanupTimeout(5.seconds()) {
    server = start(config, app)?
    log.Info("listening", { "address": http.Address(server) })
    http.WaitAny([server])
  }
}

fn show(label: String, result: http.Result) uses io {
  match (result) {
    response: http.Response => println(s"$label -> ${response.status} ${response.body}")
    error => panic(toString(error))
  }
}

fn demo() uses io + net + clock + state: Ok | sql.Error | IoError {
  scope app with cleanupTimeout(5.seconds()) {
    server = start(Config { address: "127.0.0.1:0", database: ":memory:" }, app)?
    base = "http://" + http.Address(server)
    show("GET /health", http.Get(base + "/health", app))
    show("POST /notes", http.Post(base + "/notes", "application/json", "{\"title\":\"learn bork\"}", app))
    show("empty title", http.Post(base + "/notes", "application/json", "{\"title\":\"\"}", app))
    show("broken JSON", http.Post(base + "/notes", "application/json", "{", app))
    show("GET /notes", http.Get(base + "/notes", app))
  }
  println("application scope closed")
}

fn main(): Ok | process.ExitCode {
  log.Configure(log.Defaults().copy(timestamps: false))
  result = match (process.Args()) {
    [] => demo()
    ["demo"] => demo()
    ["serve"] => serve()
    _ => { return process.ExitCode { code: 2, message: "usage: service_tour [demo|serve]" } }
  }
  match (result) {
    Ok => {}
    error => { return process.ExitCode { code: 1, message: toString(error) } }
  }
}
```

## Shut down and report startup failures

`start` opens the pool, creates the table if needed, and then opens the listener. `?` propagates database or listener failures through `serve`. The application scope still closes resources acquired before the failure. Only after the scope has closed does `main` print the error and exit with code 1. Unknown command arguments exit with code 2 before an application scope is opened.

For example, an empty configured address fails before listening:

```sh
TOUR_ADDRESS='' bork run examples/service_tour -- serve
```

```text
ConfigError { errors: [DecodeError { path: "TOUR_ADDRESS", message: "must be nonEmpty" }] }
```

The program exits with code 1. An unavailable database path or occupied listener address follows the same nonzero startup-error path.

SIGINT and SIGTERM cancel root scopes by default. `http.WaitAny` returns when the listener stops or its scope is cancelled. Leaving `app` cancels its work and runs resource cleanup. The listener stops accepting requests, cancels request scopes, and drains active handlers for at most 5 seconds. Shutdown lets cooperative handlers finish cancellation and cleanup; it does not promise that an in-flight write succeeds. `cleanupTimeout(5.seconds())` bounds each resource cleanup wait. See [signal handling](std/signal.md) for process exit codes, repeated signals and custom policies.

## Test handlers directly

Run `bork test examples/service_tour`. The source contains tests that open an in-memory SQLite database and call `route` with constructed requests and a nested request scope. They cover successful persistence, malformed JSON, empty and oversized titles, method/path errors, rollback, storage-error privacy, configuration validation, database startup failure, table-setup failure, listener startup failure, and port reuse after scope cleanup. These tests need no running HTTP server or external database, except the listener-specific tests, which bind available localhost ports.

The demo adds an end-to-end check through the real HTTP listener, and the examples test compares its output with the checked transcript. Direct tests pinpoint handler behavior; the demo checks that the pieces work together.

Continue with the [cookbook](cookbook.md) for small tasks, or the [HTTP](std/http.md), [SQL](std/sql.md), [logging](std/log.md), and [testing](language/testing.md) references for more detail.
