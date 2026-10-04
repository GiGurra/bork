# Typed string interpolators

Design for bork-kgrtcn. This document proposes the language mechanism and the
first library, `bork/sql`; implementation follows in a separate PR.

## Syntax and result types

Use an adjacent named prefix, with optional package qualification:

```bork
import "bork/sql"

name = "Ada"
statement = sql.SQL"SELECT id, name FROM users WHERE name = $name"
users = statement.Query[User](db)?
```

A prefix names an ordinary function. `sql.SQL` uses the import alias; a local
`SQL` function permits `SQL"..."`. There is no implicit import or registration.
The prefix is a name or a package-qualified name, not an arbitrary expression.
It must be adjacent to the opening quote. `s"..."` remains the reserved built-in
String renderer. Plain `"..."` never interpolates.

All prefixes share `$name`, `${expression}`, `$$` for a literal dollar, and the
existing string escapes. `$name.field` still means a hole for `name` followed
by literal `.field`; use `${name.field}` for a projection. Neither `$$name`
nor SQL's syntactic position selects a different hole mode. Hole types choose
the behavior. A literal without holes still invokes its prefix and returns its
result type; `sql.SQL"SELECT 1"` is a Statement, never a String.

Alternatives: `SQL(s"...")` has already lost the text/value boundary. A second
sigil conflicts with `$$` and places security policy at each call site. JS-style
backticks introduce another string syntax. An expected-type-driven conversion
makes the meaning depend on surrounding overloads or annotations. An explicit
prefix makes the security boundary visible and works with bork's current imports.

## Library protocol and desugaring

The language knows only this call protocol:

```bork
SQL"a $x b ${y()} c"
// Equivalent call structure:
SQL(["a ", " b ", " c"]).Interpolate(x).Interpolate(y()).Finish()
```

The prefix receives a `List[String]` of decoded literal parts, with exactly one
more element than the number of holes. The returned value has an exported
`Interpolate` method for each hole and an exported zero-argument `Finish` method.
The builder tracks which literal follows each hole. Hole values retain their
ordinary types; there is no heterogeneous Any list, Show conversion, or SQL
name inside the compiler. The type of `Finish()` is the literal expression's
type, including a failure union if the library chooses one.

For example, a library can use bork's existing generic/class machinery:

```bork
class Encodable[T] { fn encodePart(value: T): String }
type Builder = { parts: List[String], values: List[String] }
fn Html(parts: List[String]): Builder { Builder { parts: parts, values: [] } }
fn (builder: Builder) Interpolate[T: Encodable](value: T): Builder {
  Builder { parts: builder.parts, values: builder.values.append(encodePart(value)) }
}
// Finish combines builder.parts and builder.values into the library's result.
```

The sketch demonstrates dispatch, not a complete HTML escaping implementation.
Instance visibility stays exactly as for an ordinary generic call (including
explicit `use` for external instance sets). Libraries can instead take a closed
union of supported hole types, as SQL initially does. Stateful builders declare
state; persistent builders may be pure. The compiler does not add an effect
exemption or manufacture instance imports.

Evaluate the prefix call first, then each hole and its Interpolate call from
left to right, exactly once, then Finish. Empty literal parts are preserved.
Effects, constraints, generic inference, access checks, and resource lifetimes
are those of the equivalent calls. There is no lazy skipping of holes or
compiler-provided short-circuiting. The initial implementation permits ordinary
method result types to determine the next receiver, rather than adding a
separate associated-type system.

## SQL values and identifiers

`bork/sql` supplies a pure `SQL(parts)` builder and an immutable Statement.
Its Interpolate parameter accepts `Value | Identifier | Statement`, where Value
is the existing `String | Int | Float | Bool | Bytes | Null`. Unsupported records,
functions, resources, and numeric widths are rejected at the hole; callers
explicitly convert other scalar widths or provide an allowed SQL value.
A closed union covers today's driver contract without making every caller
import a class instance set. A later open SQL conversion class can extend this
without changing the interpolation syntax or compiler protocol.

- A Value is a bound parameter, including String. No value becomes SQL text.
- An Identifier is a quoted identifier component. Construct one using
  `sql.Identifier.new(name)` returning `Identifier | Error`; reject empty names
  and NUL, and double embedded double quotes when rendering. Quoting, not an
  allowlist of letters, provides injection safety. A dot belongs to a single
  name: `${schema}.${table}` composes two components; Identifier("a.b") denotes
  one name. Table and column wrappers can follow if they add useful guarantees.
- A Statement is a composed fragment. Statements store structured literal,
  identifier, and bound-value parts, so nested parameters are flattened in
  source order and numbered only at execution. Statement doubles as Fragment;
  there is no second representation to convert or accidentally stringify.

```bork
table = sql.Identifier.new(tableName)?
column = sql.Identifier.new(columnName)?
filter = sql.SQL"$column = $name"
statement = sql.SQL"SELECT * FROM $table WHERE $filter"
```

There is no raw hole escape hatch in the first version. Runtime SQL text remains
possible through the existing explicit string/parameter API; that legacy API is
outside the safe Statement construction guarantee. Statement internals are
private, and there is no public constructor taking arbitrary SQL text.
The protocol's literal List is ordinary runtime data for arbitrary library
prefixes, so the SQL prefix factory must remain a documented trusted-text
entry point: manually calling `sql.SQL(untrustedParts)` can supply raw SQL.
The guarantee is for prefixed literals and their composition, not an assertion
that every public library function can prove its String inputs came from syntax.
Preventing the latter would require a compiler-created literal-parts capability;
that stronger origin guarantee is deferred and must not be implied by the docs.

## SQL boundaries, dialects, and execution

Statement builds a structured representation; executing or explicitly rendering
it can fail with sql.Error. Reject holes inside single/double-quoted text,
SQLite backtick/bracket identifiers, line/block comments, and Postgres dollar
quotes. Reject a hole embedded within an unquoted token, rather than silently
turning `prefix$value` into a new token. This includes literal text on either
side, consecutive holes, and nested fragments. Flatten fragments before these
checks so composition cannot hide a broken lexical boundary. Quoted identifier
holes may be followed or preceded by a literal dot for qualified names.

Literal placeholder tokens are rejected in the Statement API: SQLite `?`,
`?NNN`, `:name`, `@name`, `$name`, and Postgres `$NNN` outside quotes/comments
must not capture generated parameters. A PostgreSQL `?` JSON operator is literal
SQL and is allowed for that dialect. SQL operators and dollar-quote delimiters
need dialect-aware scanning. Literal placeholders in quoted text/comments are
ordinary bytes. Unsupported driver dialects fail explicitly before execution.
This scanner validates safe hole/parameter boundaries, not SQL grammar, table
existence, or column types. A String used where SQL needs a table still becomes
a parameter and gets a database syntax error; it never becomes an identifier.

Render structured parameters to `?` for SQLite or `$1`, `$2`, ... for Postgres,
selected by the connection's driver, also retained by its transactions. Never
renumber by scanning/substituting arbitrary SQL text. Identifiers use standard
double quotes for both supported dialects. Rendering a composed Statement twice
is deterministic and does not mutate it or consume its parameters.

Expose `statement.Exec(connectionOrTx)`, `statement.Query[T: Decode](connectionOrTx)`,
`statement.QueryJson(connectionOrTx)`, and the streaming `statement.Rows[T: Decode]`
and `statement.RowsJson`. These methods use existing execution/decoding/context
logic after rendering, and preserve the existing failure unions and effects.
Provide `statement.Render(dialect)` returning a public rendered query/params
record or Error for testing and inspection. Rendering is pure; execution and
stream traversal retain their current effects and scope ownership.
The legacy `sql.Exec/Query/QueryJson/Rows/RowsJson(query, params)` entry points
remain compatible. Migrate examples/sql to Statement methods and use actual
value holes for inserts and predicates.

## Diagnostics and tooling

Keep parsed prefixes and hole positions in the AST. Resolve the equivalent call
chain with existing checker paths, and retain its checked form for lowering.
Unknown prefixes report at the prefix; malformed protocols report the missing
method or wrong signature at the prefix; unsupported types and unsatisfied
class bounds report at the originating hole. A failure from SQL rendering is a
normal sql.Error containing the operation and a useful boundary description.
Compile-time SQL lexical validation is a possible later optimization; it must
agree with the runtime renderer and cannot be required for runtime composition.

The formatter preserves the prefix/quote adjacency and the literal's bytes,
including nested interpolation expressions, just as for s"...". Update editor
string-prefix recognition for qualified/custom prefixes while keeping ordinary
names and string tokens distinct. Do not infer SQL syntax highlighting from
arbitrary user names as a language rule. Update grammar, requirements, README,
and SQL API docs when implementation lands, describing the origin limitation
and the difference between String rendering and typed literal construction.

## Prior art and decisions

[Scala StringContext](https://docs.scala-lang.org/scala3/book/string-interpolation.html)
uses named prefixes and separates literal parts from arguments. Bork adopts
that visible prefix and separation, with a typed method call per hole to fit
its existing generics.
[JavaScript tagged templates](https://tc39.es/ecma262/2023/#sec-tagged-templates)
pass literal segments and unconverted values to a tag. Bork keeps the arbitrary
result type, but needs compile-time hole checks rather than dynamic rest values.

[Swift's interpolation protocol](https://docs.swift.org/latest/documentation/diagnostics/string-interpolation-conformance/)
selects appendInterpolation overloads using argument types.
[C# handlers](https://learn.microsoft.com/dotnet/csharp/whats-new/tutorials/interpolated-string-handler)
use append operations and can conditionally skip evaluation. Bork adopts ordinary
typed operations but guarantees eager evaluation; adding skip behavior would
need an explicit language contract for effects.

Java [JEP 430](https://openjdk.org/jeps/430) and [JEP 459](https://openjdk.org/jeps/459)
previewed String Templates in Java 21 and 22. The proposed follow-up was
withdrawn and the feature removed from Java 23; the Java team revisited the
processor/template design rather than finalizing it, as discussed in
[the Oracle Java team's account](https://inside.java/2024/06/20/newscast-71/).
This is a warning against treating a processor-object abstraction as settled
merely because safe interpolation is desirable. Bork's decision is an ordinary
function/method protocol without processor classes, implicit conversion, or
new declarations. SQL security belongs to its library's structured values and
boundary checks; prefix syntax alone cannot supply it.

## Validation and later work

Test syntax, escapes, qualified and aliased imports, empty parts, nested literals,
formatter idempotence, and exact diagnostic positions. A custom test library
must produce a non-String result, use mixed hole types through generic bounds,
and prove once-only, ordered evaluation and effect propagation. Include missing
methods, inaccessible methods, unsupported holes, and resource-lifetime failures.

SQL tests cover scalar/Bytes/Null bindings, hostile String and Identifier values,
identifier dots/quotes, nested fragments and parameter ordering, SQLite execution,
Postgres rendering and numbering, transaction dialect retention, repeated render,
legacy compatibility, and every quote/comment/placeholder boundary above.
Check malformed compositions, dollar quotes, comment delimiters split across
fragments, and both sides of holes. No live Postgres service is needed to verify
rendering; existing driver behavior remains covered by the library checks.

HTML needs context-aware escaping, URL components need explicit encoding roles,
and shell/exec should produce argv rather than a shell string. JSON and regex
have their own structural/escaping rules. Each can use this protocol later;
none is implemented by generic Show rendering. SQL parsing/schema checks and
compile-time template validation remain separate follow-ups.
