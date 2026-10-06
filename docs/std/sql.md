# bork/sql

`bork/sql` runs parameterized SQL against SQLite or Postgres and decodes rows into checked bork values.

This program opens an in-memory SQLite database, inserts a bound value, and reads it back. Save it as `main.bork` and run `bork run main.bork`:

```bork
import "bork/codec"
import "bork/sql"
use codec.Defaults

type User = { name: String } derive (codec.Decode)

fn demo() uses io + net: Ok | sql.Error | codec.DecodeError {
  scope app {
    db = sql.OpenSqlite(":memory:", app)?
    _ = sql.SQL"CREATE TABLE users (name TEXT)".Exec(db)?
    name = "Ada"
    _ = sql.SQL"INSERT INTO users(name) VALUES ($name)".Exec(db)?
    users = sql.SQL"SELECT name FROM users".Query[User](db)?
    for (user in users) { println(user.name) }
  }
}

fn main() {
  match (demo()) {
    _: Ok => {}
    error: sql.Error => eprintln(error.operation + ": " + error.message)
    error: codec.DecodeError => eprintln(error.path + ": " + error.message)
  }
}
```

The inserted user is read back:

```text
Ada
```

A database error is `sql.Error { operation, message }`; a row that does not fit the requested type produces `codec.DecodeError { path, message }`. Handle both at the application boundary. `?` returns errors from `demo`; `main` handles them explicitly.

## API

Names in this table belong to `sql`. `Connection` and `Transaction` are scope-owned resources. All database operations use `io + net`, including SQLite operations.

| Signature | Meaning |
| --- | --- |
| `OpenSqlite(dataSource: String, s: Scope) uses io + net: Connection \| Error` | Open and ping SQLite; `":memory:"` is an in-memory database. |
| `OpenPostgres(dataSource: String, s: Scope) uses io + net: Connection \| Error` | Open and ping Postgres using pgx. |
| `Open(driver: String, dataSource: String, s: Scope) uses io + net: Connection \| Error` | Select a registered driver; typed Statements support `"sqlite"` and `"pgx"`. |
| `Begin(connection: Connection, s: Scope) uses io + net: Transaction \| Error` | Start a transaction owned by `s`. |
| `Commit(transaction: Transaction) uses io + net: Ok \| Error` | Commit; successful commit prevents automatic rollback. |
| `Rollback(transaction: Transaction) uses io + net: Ok \| Error` | Roll back; harmless after commit or rollback. |
| `Name(value: String): Identifier \| Error` | Validate one nonempty SQL name without NUL. |
| `Unsafe(text: String): UnsafeQuery` | Explicitly trust runtime SQL text. |

For a `statement: sql.Statement`, these methods accept `connection: sql.Connection | sql.Transaction`:

| Method signature | Meaning |
| --- | --- |
| `Exec(connection) uses io + net: sql.Result \| sql.Error` | Execute a statement and return the changed-row count. |
| `Query[T: codec.Decode](connection) uses io + net: List[T] \| sql.Error \| codec.DecodeError` | Materialize and decode every row. |
| `QueryJson(connection) uses io + net: codec.Value \| sql.Error` | Return the first result set as a codec array of row objects. |
| `Rows[T: codec.Decode](connection): Seq[T \| sql.Error \| codec.DecodeError] uses io + net` | Decode rows during traversal. |
| `RowsJson(connection): Seq[codec.Value \| sql.Error] uses io + net` | Traverse row objects without typed decoding. |
| `Render(dialect: sql.Dialect): sql.Rendered \| sql.Error` | Inspect query text and bound parameters without executing it. |

| Type | Fields or variants |
| --- | --- |
| `sql.Value` | `String \| Int \| Float \| Bool \| Bytes \| sql.Null` |
| `sql.Null` | `{}`; binds SQL NULL. |
| `sql.Result` | `{ rowsAffected: Int, lastInsertId: Option[Int] }`; ID is None when the driver does not provide one. |
| `sql.Error` | `{ operation: String, message: String }` |
| `sql.Rendered` | `{ query: String, params: List[sql.Value] }` |
| `sql.Dialect` | `Sqlite`, `Postgres` |
| `sql.Statement`, `sql.Identifier`, `sql.UnsafeQuery` | Private values created through the SQL literal, `Name`, and `Unsafe` APIs. |

## Bound values, names and fragments

A `sql.SQL"..."` literal keeps SQL text separate from its holes. A scalar hole becomes a driver parameter; even a hostile String remains data. Write `name = $name`, without quotes around the hole. Use `${expression}` for a projection or other expression and `$$` for a literal dollar.

Use `sql.Name` when a table or column name is dynamic. It quotes one identifier component, escaping embedded double quotes. A dot is part of that name: compose qualified names with `$schema.$table`. SQL names still need application authorization when a user chooses which table to access.

```bork
import "bork/sql"

fn inspect(tableName: String, name: String): sql.Rendered | sql.Error {
  table = sql.Name(tableName)?
  filter = sql.SQL"name = $name"
  statement = sql.SQL"SELECT name FROM $table WHERE $filter"
  statement.Render(sql.Dialect.Postgres)
}

fn main() {
  match (inspect("users", "Ada")) {
    rendered: sql.Rendered => {
      println(rendered.query)
      println(rendered.params)
    }
    error: sql.Error => eprintln(error.message)
  }
}
```

Rendering keeps query text and values separate:

```text
SELECT name FROM "users" WHERE name = $1
["Ada"]
```

A Statement hole splices a structured fragment and preserves bound-value order. Rendering uses `?` for SQLite and `$1`, `$2`, … for Postgres; execution chooses the connection's dialect. Statements are reusable and never consume their values.

The library checks interpolation boundaries at compile time when it can prove they are invalid, and checks the complete flattened statement when rendering. Holes inside quotes/comments or partial tokens, unfinished quotes/comments, NUL literal text, and conflicting manual placeholders are rejected. These checks do not validate SQL grammar or database schema: those errors come from the driver. Render failures have operation `"interpolate"` and execute no SQL. See [the interpolation design](../design/interpolators.md) for dialect-specific rules and the compile/render check matrix.

A quoted hole is a compile error:

```bork fails
import "bork/sql"

fn main() {
  name = "Ada"
  statement = sql.SQL"SELECT '$name'"
  println(statement.Render(sql.Dialect.Sqlite))
}
```

```text
SQL hole is inside quoted text or an identifier (validator bork/sql.sqlInterpolationValidator)
```

## Decode rows and handle failures

Derive `codec.Decode` on the row record and select `codec.Defaults`. Column names map to fields; use SQL aliases when they differ. Field facts and record invariants must hold before `Query` returns any rows.

| SQL value | Value passed to the decoder |
| --- | --- |
| NULL | null; an `Option[T]` becomes None. |
| Integer / float / Boolean | Number / Number / Bool; integers keep exact digits. |
| Text | String |
| Binary | Array of byte integers |
| Timestamp | RFC3339 String |
| Postgres NUMERIC | String |
| Postgres JSON/JSONB driver bytes | Array of byte integers |

Cast columns or use `QueryJson` with a custom decoder when a driver's mapping does not match your model. Duplicate column names are errors; only the first result set is returned.

This complete program produces a row-decoding failure rather than a partial result:

```bork
import "bork/codec"
import "bork/sql"
use codec.Defaults

type Row = { count: Int } derive (codec.Decode)

fn main() {
  scope app {
    match (sql.OpenSqlite(":memory:", app)) {
      db: sql.Connection => {
        match (sql.SQL"SELECT 'many' AS count".Query[Row](db)) {
          rows: List[Row] => println(rows)
          error: codec.DecodeError => eprintln(error.path + ": " + error.message)
          error: sql.Error => eprintln(error.operation + ": " + error.message)
        }
      }
      error: sql.Error => eprintln(error.message)
    }
  }
}
```

Output on stderr:

```text
[0].count: expected a whole number, found a string
```

## Commit or roll back

Open a transaction in its own scope. Commit before leaving that scope to keep the changes; scope cleanup rolls back an uncommitted transaction, including on early error return.

```bork
import "bork/codec"
import "bork/sql"
use codec.Defaults

type Count = { count: Int } derive (codec.Decode)

fn demo() uses io + net: Ok | sql.Error | codec.DecodeError {
  scope app {
    db = sql.OpenSqlite(":memory:", app)?
    _ = sql.SQL"CREATE TABLE users (name TEXT)".Exec(db)?
    scope transaction {
      tx = sql.Begin(db, transaction)?
      _ = sql.SQL"INSERT INTO users(name) VALUES (${"kept"})".Exec(tx)?
      sql.Commit(tx)?
    }
    scope transaction {
      tx = sql.Begin(db, transaction)?
      _ = sql.SQL"INSERT INTO users(name) VALUES (${"discarded"})".Exec(tx)?
    }
    rows = sql.SQL"SELECT count(*) AS count FROM users".Query[Count](db)?
    for (row in rows) { println(row.count) }
  }
}

fn main() {
  match (demo()) {
    _: Ok => {}
    error: sql.Error => eprintln(error.operation + ": " + error.message)
    error: codec.DecodeError => eprintln(error.path + ": " + error.message)
  }
}
```

Only the committed insert remains:

```text
1
```

A pool closes when its last resource owner closes. Operations use the connection or transaction owner's cancellation context. Attachment adds an owner/cancellation source; cancellation occurs once every owner is cancelled. See [scopes](../language/scopes.md) for resource attachment.

SQLite uses one connection, preserving in-memory databases and serializing access. While a transaction owns that connection, use the transaction for its queries rather than querying the pool. Postgres uses Go's default pool.

## Stream rows

`Rows` and `RowsJson` are lazy: constructing a sequence executes no query, and each traversal queries afresh. Handle errors per element. Breaking traversal closes active rows, and returned values copy driver buffers. Keep the sequence within its connection/transaction lifetime.

```bork
import "bork/codec"
import "bork/sql"
use codec.Defaults

type Row = { count: Int } derive (codec.Decode)

fn main() {
  scope app {
    match (sql.OpenSqlite(":memory:", app)) {
      db: sql.Connection => {
        for (result in sql.SQL"SELECT 1 AS count UNION ALL SELECT 2 AS count".Rows[Row](db)) {
          match (result) {
            row: Row => println(row.count)
            error: sql.Error => eprintln(error.message)
            error: codec.DecodeError => eprintln(error.path + ": " + error.message)
          }
        }
      }
      error: sql.Error => eprintln(error.message)
    }
  }
}
```

Streaming visits both rows:

```text
1
2
```

## Explicit raw SQL

Use `sql.Unsafe(text)` only when you must trust SQL assembled as a runtime String. It does not validate, escape, or sanitize the text. Bind values separately, using `?` for SQLite or numbered `$1` placeholders for Postgres.

```bork
import "bork/codec"
import "bork/sql"
use codec.Defaults

type User = { name: String } derive (codec.Decode)

fn rawUsers(connection: sql.Connection, queryText: String, params: List[sql.Value]) uses io + net: List[User] | sql.Error | codec.DecodeError {
  sql.Query[User](connection, sql.Unsafe(queryText), params)
}
```

The raw free functions take `(connection: Connection | Transaction, query: UnsafeQuery, params: List[Value])`. `Exec`, `Query[T]`, `QueryJson`, `Rows[T]` and `RowsJson` have the same effects/results as the corresponding Statement methods. Passing a plain String as the query is a compile error, including through a function reference. There is no raw-text Statement hole, and the compiler-created `StaticParts` required by the prefix cannot be forged from runtime strings. Explicit unsafe Go lies outside these guarantees.

## Larger examples

- [SQLite transactions](../../examples/sql/main.bork): insert, query, commit and automatic rollback.
- [SQL interpolation](../../examples/sql_interpolation/README.md): rendered parameters, dynamic names, composition, hostile input and streaming.
- [Typed interpolators](../language/interpolators.md): literal syntax and evaluation order.
