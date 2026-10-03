# bork/sql

## SQL resources

- **SQL (implemented):** `bork/sql` owns database pools and transactions as scope resources, using `database/sql` with pinned pure-Go SQLite and pgx Postgres drivers. `OpenSqlite(dataSource, s)` and `OpenPostgres(dataSource, s)` connect and ping. `Begin(db, transactionScope)` rolls back when that scope closes unless `Commit` succeeds. `Exec` binds typed scalar/Bytes/Null parameters, and `Query[T: Decode]` decodes column-name row objects into proven values. Operations use their connection or transaction owner's cancellation context; attachment rebinds cancellation to the destination scope through the general resource hook. `QueryJson` exposes the JSON representation for custom decoding. SQLite uses one connection; Postgres uses Go's default pool. See [examples/sql](../../examples/sql/main.bork).

## SQL API

`bork/sql` adds scope-owned `Connection` and `Transaction` resources. Open with `OpenSqlite(dataSource, s)` or `OpenPostgres(dataSource, s)`, start a transaction with `Begin(connection, s)`, and `Commit(tx)` before that scope closes; otherwise it rolls back. `Exec(connectionOrTx, query, params)` and `Query[T: Decode](connectionOrTx, query, params)` bind `String | Int | Float | Bool | Bytes | sql.Null` values, using the owner's cancellation context; attachment switches that context's cancellation source to the destination scope. Column aliases map to record fields; SQL NULL decodes as Option.None, binary columns as JSON byte-integer arrays, and timestamps as RFC3339 strings. Queries return the first result set; duplicate column names are errors. See [examples/sql](../../examples/sql/main.bork).

## Examples

`bork/sql` opens SQLite or Postgres connections in scopes, rolls uncommitted transactions back on scope exit, binds query parameters, and decodes rows into proven records. See [examples/sql](../../examples/sql/main.bork).
