# bork/sql

## SQL resources

- **SQL (implemented):** `bork/sql` owns database pools and transactions as scope resources, using `database/sql` with pinned pure-Go SQLite and pgx Postgres drivers. `OpenSqlite(dataSource, s)` and `OpenPostgres(dataSource, s)` connect and ping. `Begin(db, transactionScope)` rolls back when that scope closes unless `Commit` succeeds. `Exec` binds typed scalar/Bytes/Null parameters, and `Query[T: Decode]` decodes column-name row objects into proven values. Operations use their connection or transaction owner's cancellation context; attachment rebinds cancellation to the destination scope through the general resource hook. `QueryJson` exposes the JSON representation for custom decoding. SQLite uses one connection; Postgres uses Go's default pool. See [examples/sql](../../examples/sql/main.bork).

## SQL API

`bork/sql` adds scope-owned `Connection` and `Transaction` resources. Open with `OpenSqlite(dataSource, s)` or `OpenPostgres(dataSource, s)`, start a transaction with `Begin(connection, s)`, and `Commit(tx)` before that scope closes; otherwise it rolls back. `Exec(connectionOrTx, query, params)` and `Query[T: Decode](connectionOrTx, query, params)` bind `String | Int | Float | Bool | Bytes | sql.Null` values, using the owner's cancellation context; attachment switches that context's cancellation source to the destination scope. Column aliases map to record fields; SQL NULL decodes as Option.None, binary columns as JSON byte-integer arrays, and timestamps as RFC3339 strings. Queries return the first result set; duplicate column names are errors. See [examples/sql](../../examples/sql/main.bork).

## Typed SQL literals

```bork
import "bork/sql"

statement = sql.SQL"SELECT id, name FROM users WHERE name = $name"
rows = statement.Query[User](connection)?
```

SQL constructs a private, immutable Statement, preserving literal text and hole
values separately. String, Int, Float, Bool, Bytes, and sql.Null holes become
bound parameters. Other types are compile errors at the hole. Do not quote holes:
write `name = $name`, rather than `name = '$name'`. A plain String never becomes
a SQL name or raw text, even when placed where SQL expects an identifier.

`sql.Name(text): Identifier | Error` accepts one nonempty identifier component
without NUL. Identifier holes use double quotes and double embedded quotes.
Dots are part of a component; use `$schema.$table` to compose qualified names.
Nested Statement holes splice structured fragments and preserve parameter order:

```bork
table = sql.Name(tableName)?
column = sql.Name(columnName)?
filter = sql.SQL"$column = $name"
query = sql.SQL"SELECT * FROM $table WHERE $filter"
result = query.QueryJson(connection)?
```

The prefix factory accepts compiler-created `StaticParts`, whose immutable
`values` may be read but whose private representation cannot be constructed or
updated by user code. Passing a runtime String list to sql.SQL is a compile error.
There is no raw-text hole or Unsafe constructor. Existing explicit query/parameter
functions remain available for manually assembled SQL; those callers control and
must trust their query text. Explicit unsafe Go remains outside these guarantees.

Statement methods `Exec`, `Query[T: Decode]`, and `QueryJson` take a Connection or
Transaction and preserve the existing results, errors, effects, cancellation,
and scope lifetimes. `Rows[T: Decode]` and `RowsJson` remain lazy sequences:
each traversal renders and queries afresh. Rendering failures yield one Error
and stop without executing SQL. Reusing a Statement never consumes its values.

`statement.Render(dialect: sql.Dialect)` returns
`sql.Rendered { query: String, params: List[sql.Value] } | sql.Error`; use sql.Dialect.Sqlite or sql.Dialect.Postgres. SQLite renders `?`; Postgres renders `$1`, `$2`, ...
from the flattened value order. Execution picks the connection's driver dialect,
also preserved through transactions and scope attachment. Other Open drivers
cannot execute a Statement. Render is pure and useful for inspection and testing.

Rendering rejects holes in quoted text/identifiers, comments, dollar quotes, or
partial tokens, including boundaries hidden by nested fragments. Postgres E
strings, Unicode dollar-quote tags, and nested comments are recognized; ordinary
Postgres strings containing backslashes are rejected because session settings
can change their meaning. SQLite line comments end only at LF, while Postgres
also accepts CR. Unterminated quoted text/comments and NUL literal text fail.
Literal driver placeholders are rejected outside quotes/comments so they cannot
capture bound values; the PostgreSQL `?` JSON operator remains permitted.
This validates interpolation boundaries, not SQL grammar or schema. All such
failures are sql.Error with operation `interpolate`; SQL syntax/schema errors
still come from the driver.

The syntax, eager evaluation, typed builder protocol, and capability guarantees
are described in [the grammar](../grammar.md) and [design](../design/interpolators.md).

## Examples

`bork/sql` opens SQLite or Postgres connections in scopes, rolls uncommitted transactions back on scope exit, binds query parameters, and decodes rows into proven records. See [examples/sql](../../examples/sql/main.bork).

`Rows[T: Decode](connection, query, params): Seq[T | Error | DecodeError] uses io + net` decodes one row at a time. `RowsJson` yields `Json | Error`. Construction performs no query; each traversal executes it afresh using the connection or transaction context. Stopping closes active rows. Returned values copy driver buffers, and the sequence retains the connection/transaction lifetime. Handle errors per element.
