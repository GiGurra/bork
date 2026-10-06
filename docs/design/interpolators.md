# Typed string interpolators

Implemented design for typed string interpolators and `bork/sql`. For current
usage, see [typed interpolators](../language/interpolators.md) and
[SQL](../std/sql.md). Incomplete code below is marked as a protocol sketch.

## Syntax and result types

Use an adjacent named prefix, with optional package qualification:

```bork fragment
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

```bork fragment
SQL"a $x b ${y()} c"
// Equivalent call structure:
SQL(/* compiler-created StaticParts: ["a ", " b ", " c"] */)
  .Interpolate(x).Interpolate(y()).Finish()
```

The prefix receives a prelude `StaticParts` capability containing decoded literal
parts, with exactly one
more element than the number of holes. The returned value has an exported
`Interpolate` method for each hole and an exported zero-argument `Finish` method.
The builder tracks which literal follows each hole. Hole values retain their
ordinary types; there is no heterogeneous Any list, Show conversion, or SQL
name inside the compiler. The type of `Finish()` is the literal expression's
type, including a failure union if the library chooses one.

For example, a library can use bork's existing generic/class machinery:

```bork fragment
class Encodable[T] { fn encodePart(value: T): String }
type Builder = { parts: List[String], values: List[String] }
fn Html(parts: StaticParts): Builder { Builder { parts: parts.values, values: [] } }
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
  `sql.Name(name)` returning `Identifier | Error`; reject empty names
  and NUL, and double embedded double quotes when rendering. Quoting, not an
  allowlist of letters, provides injection safety. A dot belongs to a single
  name: `${schema}.${table}` composes two components; sql.Name("a.b") denotes
  one name. Table and column wrappers can follow if they add useful guarantees.
- A Statement is a composed fragment. Statements store structured literal,
  identifier, and bound-value parts, so nested parameters are flattened in
  source order and numbered only at execution. Statement doubles as Fragment;
  there is no second representation to convert or accidentally stringify.

```bork fragment
table = sql.Name(tableName)?
column = sql.Name(columnName)?
filter = sql.SQL"$column = $name"
statement = sql.SQL"SELECT * FROM $table WHERE $filter"
```

There is no raw hole escape hatch in the first version. Runtime SQL text remains
possible only through the explicit `sql.Unsafe(text)` conversion for the raw
query/parameter APIs, outside the safe Statement construction guarantee. Statement internals are
private, and there is no public constructor taking arbitrary SQL text.
`StaticParts` is a private prelude record with read-only `values: List[String]`.
Only the compiler creates it at prefixed literal sites; there is no public
constructor from String or List[String], including an empty/default constructor.
Reading its segments does not confer permission to reconstruct it. The existing
private-construction checks also cover copy, nested update, decoding, derivation,
and into conversions. The compiler marks only its generated construction as
trusted; source record literals never receive that exemption. Unsafe Go remains
an explicitly authorized escape from the language's guarantees. The SQL library
provides an explicit, greppable `sql.Unsafe(text): UnsafeQuery` to opt into
runtime raw SQL. Every raw execution/stream entry point requires this opaque
wrapper; a plain String cannot reach the driver through those entry points. The SQL factory accepts StaticParts, so an agent cannot
accidentally call it with a list of untrusted runtime strings.

## SQL boundaries, dialects, and execution

Statement builds a structured representation; executing or explicitly rendering
it can fail with sql.Error. Reject holes inside single/double-quoted text,
SQLite backtick/bracket identifiers, line/block comments (including nested
Postgres block comments), and Postgres dollar quotes and E-prefixed escape
strings. Postgres plain quoted strings containing backslashes are rejected
conservatively, so session standard_conforming_strings cannot change the
scanner's boundary interpretation. E-prefixed strings use explicit backslash
escape rules. SQLite plain strings do not treat backslashes as escapes. Reject a hole embedded within an unquoted token, rather than silently
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
Rows/RowsJson render on traversal, yield rendering Error as the first element
and stop without executing a query; each traversal remains fresh.
Provide `statement.Render(dialect)` returning a public rendered query/params
record or Error for testing and inspection. Rendering is pure; execution and
stream traversal retain their current effects and scope ownership.
The raw `sql.Exec/Query/QueryJson/Rows/RowsJson(connection, query, params)`
entry points require `query: UnsafeQuery`, constructed with `sql.Unsafe(text)`.
Parameters remain bound by the driver; UnsafeQuery makes trust in runtime SQL
text explicit at the call site.

## Compile-time and render-time SQL checks

The SQL validator checks literal parts and hole kinds during compilation. It
rejects a boundary only when both SQLite and Postgres reject it. Each component
starts in an unquoted context: wrapping a locally rejected component in another
fragment cannot make its construction legal. Checking stops before an unknown
or Statement hole and defers its remaining tail. Unterminated literal fragments
can be completed by composition and therefore remain render-time checks.

| Check | Compile time | Render time |
| --- | --- | --- |
| Known holes in ordinary quotes/comments or partial tokens | Reject definite errors in a literal component | Check all flattened boundaries |
| NUL literal text or unambiguous manual `$1` placeholders | Reject | Reject |
| SQLite `?` placeholders, Postgres dollar quotes, arrays, escapes | Defer dialect-dependent errors | Check actual dialect |
| Runtime Statement fragments and open hole kinds | Defer from the first unknown boundary | Check actual flattened text |
| Unterminated quoted/comment fragments | Defer EOF-only errors | Reject unfinished text |

SQLite line comments end only at LF; Postgres also accepts CR. Postgres E
strings, Unicode dollar-quote tags and nested comments are recognized. Ordinary
Postgres strings containing backslashes are rejected because session settings
can change their meaning. The PostgreSQL `?` JSON operator is permitted.
Compile errors name the library validator and point at the hole, or at the
prefix for literal-only errors. Render errors have operation `interpolate`.
Both checks validate interpolation boundaries rather than SQL grammar/schema.

## Diagnostics and tooling

Keep parsed prefixes and hole positions in the AST. Resolve the equivalent call
chain with existing checker paths, and retain its checked form for lowering.
Unknown prefixes report at the prefix; malformed protocols report the missing
method or wrong signature at the prefix; unsupported types and unsatisfied
class bounds report at the originating hole. A failure from SQL rendering is a
normal sql.Error containing the operation and a useful boundary description.
The optional owner-declared `InterpolationValidator[Builder]` protocol validates
compiler-created StaticParts and hole kinds with bounded pure evaluation, without
SQL rules in the compiler or evaluating runtime holes. SQL rejects definite
component-local boundary errors during checking; runtime rendering remains
mandatory for composition and dialect-dependent checks. See the
[validator design](interpolator-validation.md) for metadata, batching, diagnostics,
and the component-local SQL contract.

The formatter preserves the prefix/quote adjacency and the literal's bytes,
including nested interpolation expressions, just as for s"...". Update editor
string-prefix recognition for qualified/custom prefixes while keeping ordinary
names and string tokens distinct. Do not infer SQL syntax highlighting from
arbitrary user names as a language rule. The reader docs describe the
compiler-created literal capability and the difference between String rendering and typed literal construction.

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
explicit Unsafe opt-in, and every quote/comment/placeholder boundary above.
Negative capability tests cover direct literals, defaults, generated constructors,
copy/into, decode/GoStruct derivation, aliases, and generic reconstruction;
reading StaticParts.values must not make reconstruction possible.
Check malformed compositions, dollar quotes, comment delimiters split across
fragments, and both sides of holes. No live Postgres service is needed to verify
rendering; existing driver behavior remains covered by the library checks.

HTML needs context-aware escaping, URL components need explicit encoding roles,
and shell/exec should produce argv rather than a shell string. JSON and regex
have their own structural/escaping rules. Each can use this protocol later;
none is implemented by generic Show rendering. Full SQL grammar and schema validation remain outside this protocol.
