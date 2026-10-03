# bork grammar

> The grammar of what the compiler accepts today (milestone M0 so far: functions and expressions, data types, numbers, and `unsafe go`). It grows with each milestone.

## Lexical structure

- **Source files** are UTF-8, with the `.bork` extension. A directory of `.bork` files is one package. Diagnostic and `bork describe` positions use one-based lines and byte columns (see [JSON diagnostics](diagnostics.md) and [compiler code queries](describe.md)).
- **Comments:** `// to end of line` and `/* block */`. They are kept by the lexer and formatter; consecutive `//` lines directly above a field also become its doc comment.
- **Identifiers:** a letter followed by letters, digits, or `_`. Identifiers cannot start with `_`, which is reserved for the compiler.
- **Keywords:** `fn`, `pred`, `type`, `sealed`, `match`, `if`, `else`, `return`, `true`, `false`, `unsafe`, `where`, `and`, `or`, `trust`, `rule`, `generate`, `yield`, `for`, `break`, `continue`. `import`, `use`, `class`, `instance`, `test`, `instances`, `scope`, `with`, `resource`, `private`, `derive`, `uses`, `nothing`, `in`, `ambient`, `needs`, and `providers` are keywords only where they start a declaration, a scope block (or its policy), a with block (`with (` where an expression starts), a resource type, a derive list, a list of effects or needs, or the scope a parameter belongs to, and can otherwise be used as names (a function named `with` cannot be called as `with(...)` where an expression starts). `mock` is a keyword only at the start of a statement or after a binding's `=`, followed by a name.
- **`_`** on its own is the wildcard pattern.
- **Integer literals:** decimal (`10_000`), hex (`0xFF`), binary (`0b1010`), or octal (`0o17`), with `_` allowed between digits, as in Go.
- **Float literals:** `1.5`, `2e10`, `1.5e-3`. A `.` must be followed by a digit (so `5.copy(...)` is a selector).
- **Rune literals:** one Unicode code point in single quotes, with Go's escapes: `'a'`, `'\n'`, `'\u00e5'`.
- **Interpolated strings:** `s"Hello, $name! Next year: ${age + 1}"`. `$name` inserts a name and `${...}` any expression (which may contain string literals). `$$` is a dollar sign. Plain strings never interpolate.
- **String literals:** double-quoted, with Go's escape sequences (`\n`, `\t`, `\"`, `\\`, ...).
- **`unsafe go { ... }`:** after `unsafe go`, everything up to the matching `}` is raw Go, not bork tokens (braces inside Go strings, runes, and comments do not count).
- **Statement endings:** a newline ends a statement when the line's last token is an identifier, a literal, `true`/`false`, `return`, `break`, `continue`, `_`, `)`, `]`, `}`, or `?`, as in Go. A `;` can also separate statements on one line. Newlines inside parentheses are ignored, so argument and parameter lists can span lines. A line starting with `|>` continues the previous one.

## Syntax

```ebnf
Package    = { File } .
File       = { Import EOL } { Use EOL } { ( FuncDecl | PredDecl | TypeDecl | AmbientDecl | RuleDecl | TestDecl | ClassDecl | InstanceDecl | Instances | Providers ) EOL } .
AmbientDecl = "ambient" Ident ":" Type .     (* ambient traceId: String: a value functions read with needs, bound by with *)
Use        = "use" UseItem .                (* use money.DecodeAmount, use money.*, use api.Json *)
UseItem    = Ident | Ident "." ( Ident | "*" ) .
Providers  = "providers" Ident "=" "{" [ ProviderEntry { Sep ProviderEntry } [ Sep ] ] "}" .
ProviderEntry = Ident ":" ( Ident | QualIdent ) . (* monomorphic declared function *)
Instances  = "instances" Ident "{" [ UseItem { Sep UseItem } [ Sep ] ] "}" .
                                             (* instances Json { ItemDecode, ItemEncode, money.Defaults } *)
ClassDecl  = "class" Ident "[" Ident "]" "{" { MethodSig EOL } "}" .  (* class Show[T] { fn show(x: T): String } *)
MethodSig  = "fn" Ident "(" [ Params ] ")" [ FunctionWhere ] [ Uses ] [ ":" Type ] .
InstanceDecl = "instance" Ident [ TypeParams ] ":" Ident "[" Type "]" "{" { FuncDecl EOL } "}" .
                                             (* instance showBox[T: Show]: Show[Box[T]] { fn show(x: Box[T]): String { ... } } *)
Import     = "import" [ Ident ] StringLit .  (* import "example.com/shop/money", or import cash "..." *)
QualIdent  = Ident "." Ident .               (* money.Cents, money.Amount: a name of an imported package *)
TestDecl   = "test" StringLit [ "(" Params ")" ] Block .  (* test "adds numbers" { ... }; parameters (no defaults) make a property test *)
PredDecl   = "pred" Ident "(" Params ")" Block .  (* always returns Bool *)
RuleDecl   = "rule" Ident "(" Params ")" "{" Premises "=>" Conclusions "}" .
Premises   = Expr { "and" Expr } .  (* predicate calls on the variables, and conditions *)
Conclusions = Call { "and" Call } .

TypeDecl   = "type" Ident [ TypeParams ] "=" ( ( [ "private" ] Fields | Sealed | GoName Fields ) [ Where ] | "resource" [ GoName ] | GoName | Type ) [ Derive ] .
                                             (* type Pair[A, B] = { ... }; type File = resource: values made by unsafe go *)
Derive     = "derive" "(" Ident { "," Ident } ")" .  (* derive (Decode, Encode, GoStruct): instances written by the compiler *)
Fields     = "{" [ Field { Sep Field } [ Sep ] ] "}" .
Field      = Ident ":" Type [ "=" Expr ] [ GoTags ] . (* defaults are closed values; preceding // lines are field docs *)
GoTags     = "go" "{" [ GoTag { Sep GoTag } [ Sep ] ] "}" .
GoTag      = Ident ":" String .
Sealed     = "sealed" "{" [ Variant { Sep Variant } [ Sep ] ] "}" .
Variant    = Ident [ Fields ] [ Where ] .
Sep        = "," | newline .                 (* commas or one item per line *)

FuncDecl   = "fn" [ Receiver ] Ident [ TypeParams ] "(" [ Params ] ")" [ FunctionWhere ] [ Uses ] [ Needs ] [ ":" Type ] ( Block | GoBody ) .
(* FunctionWhere is the bork-3ly6p0 design; implementation follows separately. *)
FunctionWhere = "where" RequirementGroup .
RequirementGroup = Requirement [ ( "and" Requirement { "and" Requirement } )
                              | ( "or" Requirement { "or" Requirement } ) ] .
Requirement = RequirementCall | StableArg CompareOp StableArg
            | "(" RequirementGroup ")" .
(* Mixing and/or at the same nesting level requires parentheses. *)
RequirementCall = ( Ident | QualIdent ) "(" [ StableArg { "," StableArg } ] ")" .
StableArg  = Ident { "." Ident } | Literal .  (* parameters/receiver, field projections, constants *)
CompareOp  = "==" | "!=" | "<" | "<=" | ">" | ">=" .
Uses       = "uses" ( "nothing" | Ident { "+" Ident } ) .  (* uses io + net: the effects io, net, clock, random, state *)
Needs      = "needs" Need { "+" Need } .     (* needs traceId + locale?: the ambient values it reads *)
Need       = ( Ident | QualIdent ) [ "?" ] . (* locale? is read as an Option[String] *)
Receiver   = "(" Ident ":" Type ")" .   (* a method: fn (xs: List[T]) second[T](): Option[T] { ... } *)
TypeParams = "[" TypeParam { "," TypeParam } "]" .   (* fn map[A, B](...) *)
TypeParam  = Ident [ ":" Ident { "+" Ident } ] .     (* T: Show + Eq: T needs instances of Show and Eq *)
GoName     = "go" StringLit .               (* go "*net/http.Request" *)
GoBody     = "unsafe" "go" ( "{" { GoImport } GoStatements "}" | StringLit ) .
                                             (* unsafe go "os.Getenv": a binding to a Go function *)
GoImport   = "import" StringLit newline .     (* import "strings" *)
Params     = Param { "," Param } [ "," ] .
Param      = Ident ":" Type [ "in" Ident ] [ "=" Expr ] .   (* a default: a literal; only on the last parameters. conn: Conn in prev: it belongs to the scope of parameter prev *)
Type       = Constrained { "|" Constrained } .  (* a union: Int | NotFound *)
Constrained = TypeAtom [ Where ] .
Where      = "where" Clause { "and" Clause } .
Clause     = PredRef { "or" PredRef }          (* alone: p or q *)
           | "(" PredRef { "or" PredRef } ")" .  (* with and: (p or q) and r *)
PredRef    = Ident [ "(" Expr { "," Expr } ")" ] .  (* positive, between(1, 65535), atLeast(lo) *)
TypeAtom   = Ident [ "[" Type { "," Type } "]" ] [ Uses ] | "(" Type ")" | FuncType .
FuncType   = "(" [ Type { "," Type } ] ")" [ Uses ] "=>" Type .  (* (Int, String) => Bool, (String) uses io => Unit; TypeAtom Uses applies only to Seq *)

Block      = "{" { Stmt EOL } [ Expr ] "}" .
Stmt       = Binding | Trust | Mock | Expr .
Mock       = [ Ident "=" ] "mock" ( Ident | QualIdent ) [ "." Ident ] "(" [ ( Ident | "_" ) { "," ( Ident | "_" ) } ] ")" Block .
                                             (* in tests: mock payments.Charge(card, amount) { ... }, calls = mock Store.save(s, x) { ... } *)
Trust      = "trust" Call .                  (* trust positive(x) *)
Binding    = ( Ident [ ":" Type ] | "_" ) "=" Expr .   (* x = 1, x: Int8 = 1, or _ = write(f, s)? to drop a value *)

Expr       = PipeExpr .
PipeExpr   = OrExpr { "|>" OrExpr } .        (* x |> f(a) is f(x, a); x |> f is f(x) *)
OrExpr     = AndExpr { "||" AndExpr } .
AndExpr    = CmpExpr { "&&" CmpExpr } .
CmpExpr    = AddExpr { ( "==" | "!=" | "<" | "<=" | ">" | ">=" ) AddExpr } .
AddExpr    = MulExpr { ( "+" | "-" ) MulExpr } .
MulExpr    = Unary { ( "*" | "/" | "%" ) Unary } .
Unary      = ( "-" | "!" ) Unary | Postfix .
Postfix    = Primary { [ "[" Type { "," Type } "]" ] "(" [ Args ] ")"
                     | "[" Type { "," Type } "]" (* only on a constructor owner, followed by RecordLit or .Variant *)
                     | "." Ident
                     | ".copy" "(" Update { Sep Update } [ Sep ] ")"
                     | "?"
                     | RecordLit } .
RecordLit  = "{" [ FieldInit { Sep FieldInit } [ Sep ] ] "}" .  (* after User, Box[Int], Shape[Int].Circle, ".", or "." Ident *)
FieldInit  = Ident ":" Expr .
Update     = Ident { "." Ident } ":" Expr .  (* u.copy(address.city: "Oslo") *)
Args       = Argument { "," Argument } [ "," ] .
Argument   = [ Ident ":" ] Expr .

Primary    = IntLit | FloatLit | RuneLit | StringLit | InterpString | "true" | "false" | Ident
           | "." [ Ident ]
           | "(" Expr ")" | Block | If | Match | Return | Lambda | ListLit | MapLit | ScopeExpr | Generate | Yield | For | LoopControl | WithExpr .
(* A bare leading "." must be followed by RecordLit: .{ field: value }.
   .Variant and .Variant { field: value } need an expected sealed type;
   .{ field: value } needs an expected record type. Patterns stay explicit. *)
Generate   = "generate" "[" Type "]" Block .
Yield      = "yield" Expr .
For        = "for" "(" Ident "in" Expr ")" Block .
LoopControl = "break" | "continue" .
WithExpr   = "with" "(" WithBind { Sep WithBind } [ Sep ] ")" Block .  (* with (traceId: id, principal: p) { ... } *)
WithBind   = ( Ident | QualIdent ) ":" Expr .
ScopeExpr  = "scope" Ident [ "with" Expr { "," Expr } ] Block .  (* scope s { f = fs.Open(path, s)? ... }; scope s with taskTimeout(100), cleanupTimeout(500) { ... } *)
Lambda     = ( Ident | "(" [ LParam { "," LParam } ] ")" ) "=>" Expr .  (* x => x + 1 *)
LParam     = Ident [ ":" Type ] .
ListLit    = "[" [ Expr { Sep Expr } [ Sep ] ] "]" .
MapLit     = "{" ":" "}" | "{" Entry { Sep Entry } [ Sep ] "}" .  (* {"a": 1, "b": 2}; {:} is the empty map *)
Entry      = Expr ":" Expr .
If         = "if" "(" Expr ")" Block [ "else" ( If | Block ) ] .
Return     = "return" [ Expr ] .
Match      = "match" "(" Expr ")" "{" [ Arm { Sep Arm } [ Sep ] ] "}" .
Arm        = Pattern "=>" Expr .
Pattern    = "_"                             (* anything *)
           | Literal                         (* 1, -1, 1.5, 'a', "a", true *)
           | "[" [ ListElems ] "]"            (* [], [x], [first, ...rest], [0, ...] *)
           | Ident ":" Type                  (* n: Int, e: NotFound | DbError *)
           | Ident [ "." Ident ] [ "{" FieldPat { Sep FieldPat } [ Sep ] "}" ] .
                                             (* Shape.Circle { radius }, NotFound, User { name }, n *)
ListElems  = ( Pattern { Sep Pattern } [ Sep "..." [ Ident ] ] | "..." [ Ident ] ) [ Sep ] .
FieldPat   = Ident [ ":" Pattern ] .         (* radius, radius: r, radius: 0, center: Point { x: 0 } *)

EOL        = newline | ";" .
```

The formatter writes LF line endings, including after line comments, while
preserving bytes inside block comments, literals, interpolations and raw Go bodies.
Source spans follow the lexer's token ends, independently of printed token text.

## Semantics in brief

- **Everything is an expression.** A block's value is its last expression. An empty block or a block that ends with a statement has type `Unit`; in a function returning a union containing `Unit`, it returns that member, including in an `if` branch or `match` arm.
- **`if` with `else`** produces a value; both branches must have the same type. **`if` without `else`** is only run for its effect.
- **`return`** has type `Never`, which fits wherever any type is expected, so `x = if (c) { return 0 } else { 1 }` works. Code after a `return` is a compile error.
- **Bindings are immutable**, and names cannot be shadowed, except prelude functions: a local or package function can use their names. Methods have a separate namespace, so a free `fn find` and `xs.find(test)` can coexist.
- **A value that is computed but never used is a compile error** (e.g. calling a function that returns `Int` as a statement).
- **Numbers:** `Int8`, `Int16`, `Int32`, `Int` (= `Int64`), `Uint8` (= `Byte`), `Int32` (= `Rune`), `Uint16`, `Uint32`, `Uint64`, `Float32`, `Float` (= `Float64`). Integers wrap on overflow, like Go.
- **Operators:** `+ - * /` on two numbers of the same type, `%` on two integers of the same type; `+` also concatenates `String`s; `< <= > >=` on numbers or `String`s; `== !=` on two values of the same type, or a union and a value of one of its members; unary `-` on signed numbers; `&& || !` on `Bool`, with short-circuiting. Types never mix implicitly. Dividing by a constant zero is a compile error.
- **Constants:** number and rune literals, and `+ - * / %` on them, are computed exactly at compile time (`0.1 + 0.2` is exactly `0.3`). A constant takes its type from where it is used (`x: Uint8 = 255`, `small + 1`); otherwise it is a `Float` if it contains a float literal, a `Rune` if it contains a rune literal, and an `Int` otherwise. It is computed as its type computes: `7 / 2` is `3` as an `Int`, and `x: Float = 1 / 3` is `0.333...`. It must fit its type.
- **Conversions:** `toInt8(x)`, `toInt16`, `toInt32`, `toInt` (`toInt64`), `toUint8` (`toByte`), `toUint16`, `toUint32`, `toUint64`, `toFloat32`, `toFloat` (`toFloat64`), from any number type. If every value of x's type fits, the result is the target type; otherwise it is `Target | OutOfRange` (float to integer drops the fraction, and NaN or infinities never fit). A constant argument is converted at compile time and must fit.
- **Interpolation** renders each value as `toString` does, so any value can go in a string: `s"user: $u"`. There is no printf-style formatting.
- **`panic(message)`** stops the program with a message. It is for bugs, not expected failures (those are union results). Its type is `Never`, so it can end any branch.
- **Development markers:** `dbg(expr)` evaluates a value once, prints `file:line expr = value` to stderr using the same Show renderer as `toString`, and returns the value with its type, facts, and lifetime intact. The probe itself has no tracked effect (so it works in pure functions and predicates); its argument retains its ordinary effects. `todo()` or `todo("message")` has type `Never` and panics with its source location if reached. `bork check` reports both as nonfatal warnings; `--json` marks them with `severity: "warning"`, codes `debug.dbg` / `debug.todo`, and a fix for removing a dbg wrapper. Builds permit both markers.
- **`println(args...)`** prints its arguments separated by spaces, followed by a newline. **`toString(x)`** renders any value the way `println` prints it. The prelude `Show[T] { fn show(x: T): String }` customizes both, interpolation, snapshots, and nested values. Every type has default text, and `show(x)` equals `toString(x)`, including in unbounded generic code. Custom Show instances are coherent: exactly one may be declared for a record or sealed type, in that type's own package. A generic instance must cover every instantiation (`Show[Box[T]]`), with only optional `Show` bounds; specializations, fact constraints, and instances for basic types, `List`, `Map` or `Option` are rejected (wrap them in a declared type instead). No `use` is needed for custom Show. Unlike other classes, Show has one program-wide renderer so printing never changes with the caller's imports or generic context. Default floats print as floats: `3.0`, `0.25`, `1e+21`.
- **The prelude** ([topic files](../internal/prelude/README.md)) is available everywhere: the records `OutOfRange` and `ParseError`; `parseInt`, `parseFloat`, `parseBool` (returning `T | ParseError`); the predicate `notEmpty`; list constructors `range` and `prepend`; and rune helpers `runeToString`, `isDigit`, `isLetter`, `isSpace`, `isUpper`, `isLower`. A `Rune` prints as its number; `runeToString` gives the character. Operations on lists, strings, maps, options, and bytes are methods (listed below), with no duplicate free functions. Prelude types and the compiler's own functions (`println`, `toString`, `panic`, `toInt8`, ...) cannot be redefined. Concurrency: `spawn(s, () => value)` gives a `Task[T]`, `await(task)` its result, and `launch(s, () => ...)` runs work without a result; scope `s` waits for its tasks before it closes. `bork/tasks` supplies an explicit bounded Pool with TrySpawn/TryLaunch admission failures; ordinary spawn/launch retain their signatures. Scope policies (`ScopePolicy`): `taskTimeout(ms)`, `cleanupTimeout(ms)`, `logFailures()`. `sleep(ms)` pauses. Cancellation: `cancel(s)`, `cancelAfter(s, ms)`, `cancelled(s)`, and the cancellation points `delay(s, ms)` and `checkpoint(s)` (`Unit | Cancelled`). Resources: `attach(r, s)` keeps `r` open until `s` closes too. For generated context-bound Go bindings, attachment also extends the union of contributing scope lifetimes; sibling results share cancellation, and explicit contexts remain fixed limits. Owned child scopes (`OwnedScope`): `b = openScope(s)` (or `openScope(s, [taskTimeout(100)])`) opens a child of `s` with an explicit end, `b.scope` borrows its `Scope`, and `closeScope(b)` ends it; see [owned child scopes](requirements.md#partially-overlapping-scopes-owned-child-scopes). Channels: `channel[T](s, capacity)`, `send`, `receive`, `closeChannel`, `received`. Shared state: `atom(x)` makes an `Atom[T]`, `current(a)` reads it, and `update(a, f)` (giving the new value) or `swap(a, f)` (giving the old one) replaces its value atomically, retrying f if another task got there first.
- **Effects** are declared after the parameters, before the result: `fn save(path: String, text: String) uses io: Unit | IoError`, and in function types, before the `=>`: `(String) uses io => Unit`. The effects are `io`, `net`, `clock`, `random`, and `state` (see [requirements.md](requirements.md#effects-in-signatures)). A function type's effects are part of it: a function that uses less fits where more is allowed (a pure `(Int) => Int` can be passed as `(Int) uses io => Int`), and a lambda's effects are what the calls in its body use. A function-typed parameter or result written without `uses` is *open*: it takes a function with any effects, and a call is charged with what its arguments to open parameters use (or, if the result is open too, the function it gives carries them). `uses nothing` makes such a parameter strictly pure. A function's body may use only what it declares: calling `println` in a function without `uses io` is an error that names the call and the fix. An unexported function may not declare effects it never uses. `main` and test bodies may use every effect (unless `main` declares `uses`, which is then checked), and `main` cannot be called or used as a value. A `pred` cannot declare effects, and its body must be pure. Logging (`bork/log`) is not an effect.
- **Ambient values** are declared with a type, `ambient traceId: String`, which must be data (no scopes, resources, tasks, atoms, channels, or functions). A function reads them by declaring them after its effects, `fn audit(e: Event) uses io needs traceId + principal: Unit`, and then uses the names as values; `needs locale?` reads an `Option[String]`. `with (traceId: id) { ... }` binds values for its block (an expression giving the block's value); an inner `with` may bind a value again. A call of a function that needs a value must be inside a `with` that binds it or in a function that needs it; an optional need is given `Option.Some` of a bound value, the caller's own `Option`, or `Option.None`. Lambdas and functions used as values keep the bindings in force where they are made. `main`, tests, predicates, class and instance methods, and Go bindings cannot declare needs; an unexported function may not declare needs it never reads. An `unsafe go` body sees its needs as Go parameters named after them (another package's with that package's Go prefix). See [requirements.md](requirements.md#ambient-values-design-bork-j68yln).
- **`unsafe go` bodies** are Go statements implementing the function. Parameters are visible under their own names, and values have the Go representations listed at the top of the prelude. Standard packages use the stable [Go helper API](std-go.md) for maps, options, and scope contexts. Imports go on the first lines (`import "strings"`). bork does not type-check the body; the Go compiler does, and reports errors at the bork positions. A package may contain `unsafe go` only if its `bork.mod` lists it (`unsafe "example.com/shop/ffi"`; the prelude and the standard library always may), so a program without a `bork.mod` cannot. The body must declare the effects of the Go it obviously uses: `os`, `os/exec`, `syscall`, and `fmt`'s printing are `io`; `net` (but not `net/url`) is `net`; `time.Now`, `time.Sleep`, and timers are `clock`; `math/rand` and `crypto/rand` are `random`; goroutines, channels, and `sync` are `state`. A package name counts even when another body imports it (the Go imports are shared), and so does a call of a bork function by name. A function value the body gives counts too, if its type declares the effect. `module` paths starting with `bork` are reserved for the standard library.
- **Bindings** call a Go function directly: `fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`. The string names the function with its import path (`"net/url.QueryEscape"`). The signature is checked against the Go function's (with go/types), and values convert at the boundary: numbers to any Go integer or float type that holds every value (`Uint8` to `int`), and from Go checked where they may not fit; `String`, `Bool`; `List[T]` from and to slices (and variadic parameters), and from arrays; `Map[K, V]` from and to Go maps with integer, string, or bool keys (a map from Go is unordered); `Bytes` and `[]byte`; and `Option[T]` from and to pointers. Go results: none (`Unit`), a value, an `error` (`Unit | GoError`), a value and an `error` (`T | GoError`), or a value and a `bool` (`Option[T]`). A Go error is the prelude's `GoError { message, goType }`; a value that does not fit its bork type (a number out of range, a nil pointer where bork has no `Option`) is a `GoValueError { path, message }`, which the result must have exactly when that can happen. Lists, maps, records, and value-converting pointers are copied both ways. Opaque Go pointer and interface values are shared. Standard and declared third-party Go packages can be bound, including methods on opaque types; generic functions and callbacks cannot yet be bound; see [Go interop](requirements.md#go-interop). A binding is `unsafe go` too: its package must be listed in `bork.mod`, and it must declare the effects of the Go function it calls, by the rules above (`fn Getenv(key: String) uses io: String unsafe go "os.Getenv"`).
- **Records** (`type User = { name: String, age: Int }`) are built with their required fields named: `User { name: "Ada", age: 36 }`. Fields are read with `u.name`. A field can declare a closed default (`age: Int = 36`), which a literal or derived decoder uses when that field is missing. An explicit value, including JSON null, is decoded normally. Defaults follow parameter defaults' closed-value rules; empty collections work for generic fields. A record cannot contain itself directly. `derive (GoStruct)` exposes a separate Go struct with exported fields and checked conversions; generated fields can declare ordered tags such as `go { json: "port", short: "p" }`. Mirrors use their existing Go struct and cannot add tags. Fields must have a static Go mapping; phantom generic parameters are allowed.
- **Private record construction:** `type Config = private { port: Int }` keeps fields readable and destructurable, while only the declaring package can build a literal or use `copy`. Nested updates cannot enter a foreign private record; replacing a whole private-valued field with an existing value is allowed. Foreign decoding and GoStruct derivation must use an instance provided by the owning package. Read-only Encode derivation can use public fields.
- **Whole-value invariants:** `type Range = { lo: Int, hi: Int } where ordered` requires a pure predicate on the completed value. Every value carries that guarantee; construction, `copy`, decoding, Go conversion, and property generation enforce it. Sealed declarations can constrain every variant, and an individual variant can add a clause on the parent sealed value (`Running { lo: Int, hi: Int } where valid`). That clause becomes available when matching the variant. Clauses use ordinary `and`/`or` and constant arguments. Validators and their declared helpers cannot assume the target type's invariant. A validator must accept candidates without parameter `where` requirements; calls through function values and class methods are conservatively rejected in its validation call graph.
- **Sibling field facts** use the same `where` syntax: `type Range = { lo: Int, hi: Int where atLeast(lo) }`. Predicate arguments can name any sibling in the record or the same sealed variant, regardless of field order. Construction checks the completed field values, including defaults. Selection and destructuring retain these relations. Derived decoding and Go conversions validate them after converting all fields.
- **`copy`** makes a changed copy: `u.copy(age: 37, address.city: "Oslo")`. Paths reach into nested records; two updates may not overlap (`address` and `address.city`). Changing a constrained field or a sibling it refers to rechecks the relation on the resulting values, including nested updates.
- **Sealed types** (`type Shape = sealed { Circle { radius: Int }, Empty }`) list all their variants. Variants are always qualified: `Shape.Circle { radius: 1 }`, `Shape.Empty`. For an exported type, upper-case variants are visible wherever the type is; lower-case variants can only be constructed or matched in their declaring package, including generic variants. Fields keep their existing visibility.
- **Unions** (`Int | NotFound | DbError`) hold a value of any one of their types. A value of a member type, or of a smaller union, can be used where the union is expected. `type Lookup = Int | NotFound` names a union. `Unit` can be a member: `fn save(x: Item): Unit | DbError` either does its work or fails, and a body that ends without a value (or with a call that returns nothing) produces the `Unit`. Unit-valued expressions can also supply that member in arguments, fields, and collection elements; their effects run before the value is passed. With `?`, `save(x)?` is a statement that returns the error.
- **`List[T]`** is built in and immutable: `[1, 2, 3]`, or `[]` where the type is known (`xs: List[Int] = []`). Lists print as `[1, 2, 3]`.
- **Functions are values.** `(Int) => String` is a function type; a lambda is `x => x + 1`, `(a, b) => a + b`, or `(x: Int) => ...` where nothing says the parameter's type. Named functions can be passed (`xs.map(double)`) unless they have `where` requirements. Lambdas see the values around them; `return` and `?` are not allowed in them.
- **Generic functions** take type parameters: `fn first[T](xs: List[T]): Option[T]`. Calls infer them by unification, from the arguments (lambdas last, so `xs.fold(0, (sum, x) => sum + x)` works), the expected result, and what lambdas' bodies give (in `words.fold([], (acc, w) => acc.append(w))` the body decides what `[]` holds). Inside, a type parameter can be passed around, stored, printed, and matched with a type pattern (`v: T`), but not compared with `==`. Lists, functions, and records holding them have no `==` either.
- **Maps:** `Map[K, V]` is built in, written `{key: value, ...}` (`{:}` when empty); keys are expressions (`{"user": name}`), of any type with `==`. Maps have methods too: `m.get(k)`, `m.put(k, v).keys()`. A `{` where an expression starts is a map when its first line is `key: value` followed by `,`, `}`, or a newline, and otherwise a block (`{}` is an empty block). Maps never change and are persistent: putting or removing a key copies only the path to it (a hash trie, log32 of the size) and shares the rest, so old versions are cheap to keep. Two maps are equal when they have the same entries, in any order. List and map keys use structural equality, independently of their text. A map keeps its keys in the order first added; `m.sorted()` (keys with `Ord`) or `m.sortedBy(less)` gives one that keeps them sorted (a balanced tree), `m.unordered()` a hash map that keeps no order (smaller and quicker to change, traversed in an order that differs between runs; printing sorts numeric and string keys by value, and other keys by their text, with mixed kinds grouped), and `m.inOrder()` goes back. Either is a `Map`, and operations keep the order of the map they start from. `entries()` gives a list of the prelude's `Entry[K, V]`; a list of entries builds a map with `entries.toMap()` (a duplicate key keeps its later value). A `Map[String, V]` is a JSON object for `Decode` and `Encode`.
- **Method references** use `Type.method`: `words.map(String.byteLength)` passes a `(String) => Int`, and `groups.map(List.length)` infers the generic receiver from the expected function type. The receiver is the first argument of the reference: `size: (List[Int]) => Int = List.length; size([1, 2])`. Imported types use `model.Point.Value`; references see the same methods as receiver calls, including exported extension methods. Existing sealed variants take precedence, so `Option.None` and `Shape.Circle` keep their meaning. Function-valued fields are not method references; `Type.method` names a declared method. A reference retains declared effects and closes open callback parameters as pure, just like a named function value. Methods with `where` requirements cannot be passed as values; use a lambda that checks them. Concrete aliases fix the receiver type (`type Ints = List[Int]` makes `Ints.length` a `(List[Int]) => Int`). Generic references need a known function type; direct calls such as `List.length(xs)` infer from their arguments, and explicit type arguments include the receiver's parameters (`List.map[Int, String](xs, f)`). Bound references such as `value.method` are not supported; use `x => value.method(x)`.
- **`Ord`:** the prelude class `Ord[T]` (`compare(a, b)`: negative, 0, or positive) has instances for the number types and `String`.
- **Named arguments:** `http.Listen(addr, s, handler, maxBodyBytes: 1048576)` skips an optional parameter; `config(port: 443, host: "example")` labels required parameters too. Positional arguments precede names, names may follow in any order, and each parameter is supplied once. Unfilled optional parameters use their defaults. Supplied arguments evaluate once in source order. Labels are available on direct declared function, class and receiver-method calls, including direct `Type.method(receiver, name: value)` calls; the receiver is always positional. A pipeline fills the first positional parameter. Function values and compiler builtins have no parameter names. Renaming a parameter breaks named callers. `copy` uses the same colon separator and rejects its old `=` form with a structured replacement fix.
- **Default parameter values:** `fn greet(name: String, greeting: String = "Hello")` can be called as `greet("ada")`. Defaults go only on the last parameters, and are closed values: literals (numbers, strings, runes, Bools, and list and map literals of them), which take their type from each call (`xs: List[T] = []`), or record and variant values of closed values (`level: Level = Level.Info`, `retry: Retry = Retry { times: 3 }`), whose names mean what they mean where the function is declared, so they work from other packages. A call that leaves parameters out gets their defaults as arguments, and facts are checked as for written ones.
- **Methods** are the primary API for operations on a value's type. They chain left to right: `users.filter(u => u.age >= 18).map(u => u.name).join(", ")`, `m.get("a").getOr(0)`, `s.trim().split(",")`. A method is declared with a receiver, as in Go: `fn (p: Point) plus(q: Point): Point { ... }`; generic receivers name their type parameters among the method's (`fn (xs: List[T]) second[T](): Option[T]`). The receiver is the method's first parameter, so facts and lifetimes apply as for any argument (`xs.first()` needs `xs` to be known `notEmpty`). Type arguments can be given as for functions; those the receiver decides are left out: `xs.map[String](f)` gives `map[A, B]` its `B` (giving all of them, `xs.map[Int, String](f)`, works too). Methods can be declared on `List`, `Map`, the basic types, and declared types, by any package; each type has its own namespace of methods, apart from functions, so `List`'s `get` and `Map`'s `get` coexist. `x.m(a)` finds `m` among the methods of `x`'s type that the code sees: its own package's first, then the exported ones of the type's package and of the imports, then the prelude's; a record field holding a function is called as before (`r.f()`). The prelude's methods: on `List`, `length`, `isEmpty`, `first`, `get`, `head`, `last`, `map`, `flatMap`, `filter`, `fold`, `find`, `any`, `all`, `count`, `forEach`, `take`, `drop`, `reverse`, `concat`, `append`, `sortWith`, `includes`, `distinct`, `join`, `sorted`, `sortBy`, `groupBy` (a `Map[K, List[T]]`), and `toMap` (of a `List[Entry[K, V]]`); on `String`, `byteLength`, `runeCount`, `contains`, `startsWith`, `endsWith`, `indexOf`, `toUpper`, `toLower`, `trim`, `replaceAll`, `repeat`, `runeAt`, `substring`, `split`, `fields`, `lines`; on `Option`, `map`, `flatMap`, `getOr`, `isSome`, `isNone`; on `Map`, `get`, `getOr`, `has`, `put`, `remove`, `size`, `isEmpty`, `keys`, `values`, `entries`, `merge`, `mapValues`, `filter`, `forEach`, `sorted`, `sortedBy`, `inOrder`, `unordered`.
- **`|>`** passes a value as the first argument of a free function (your own, or generic helpers): `xs |> summarize(prefix)`, or `x |> toString`. Operations on a value's type are methods: `users.filter(u => u.age >= 18).map(u => u.name)`. Piping to a method without a free function of the same name is an error with a suggested rewrite to `x.m(a)`. To pass an operation as a function value, use a method reference (`words.map(String.byteLength)`) or a lambda (`words.map(s => s.byteLength())`).
- **Tests:** `test "name" { ... }` declares a test, run by `bork test` and left out of programs. `assert(cond)` and `assertEqual(actual, expected)` fail a test with the position (and both values); so does a `panic`. `assertSnapshot(x)` compares `toString(x)` with a snapshot file in the `snapshots` directory next to the package's sources (for `bork test file.bork`, next to the file), and fails with a line diff if it differs, or with the value if it is missing; `bork test --update` writes the missing and different ones instead. A test's snapshots are named after it: `test "renders an invoice"` uses `renders_an_invoice.snap`, then `renders_an_invoice.2.snap`, in the order its `assertSnapshot` calls run, so they must run in a fixed order (not from concurrent tasks). Rules and property tests cannot snapshot. Tests run in test mode: facts the compiler takes on trust are checked as they run, so a `trust positive(x)` that does not hold, or an `unsafe go` function that breaks its promise, fails the test with the value. (`test` is not a keyword.)
- **Mocks:** in a test, `mock Fetch(url) { "fake" }` replaces the function `Fetch` from that statement to the end of the block it is in (a nested block can mock it again, or restore it by ending). The target is a function of the test's package, an exported function of an imported package (`http.Get`), or a method (`Store.save`, whose receiver is the first parameter); it must declare effects, and cannot be generic, a predicate, a class method, or part of the prelude. The parameters are names only, one per parameter of the target (`_` ignores one); their types and facts come from the target, and the body is checked as the target's body would be: it must give the target's result, facts included, and may use the target's effects and `state`. Inside the body, naming the target (a call, or a value) means the function before the mock: an outer mock, or the real function. `m = mock ...` binds a handle of the prelude's type `Mock[A]`, where `A` is the target's call record (`FetchCall { url: String }`, a field per parameter, made by the compiler): `m.count()` is how many calls it answered, `m.calls()` lists them as text (`Fetch("a")`), `m.args()` as call records, `m.expect(times: 2)` / `m.expect(atLeast: 1, atMost: 3)` / `m.expectWhere(c => c.url == "a", times: 1)` declare how many calls it must answer (checked when the mock ends), and `m.waitFor(n)` / `m.waitForWhere(matcher, n)` wait for calls from other tasks. Mocks belong to the test: they are seen by the tasks, servers, and Go goroutines started while they are in force, and by nothing else; when the block ends, the mock waits for its calls still running, and a mock written directly in a `scope` block lasts until that scope's tasks are done. `mock` is allowed only in test bodies, not in lambdas or other mocks. Programs (`bork build`, `bork run`) are generated as if there were no mocks; see [requirements.md](requirements.md#mocking-in-tests-design-bork-53lit4).
- **Property tests:** a test with parameters, `test "transfer scales" (amount: Int where positive) { ... }`, runs its body on 100 cases of generated values (`bork test --cases N` changes that), which meet the parameters' where clauses, including those on list elements and record fields; a clause may name another parameter (`hi: Int where atLeast(lo)`), which is then generated first. Values are drawn from edge cases (0, ±1, the type's limits, -0.0, infinities, NaN, "", ...), the constants the clauses and their predicates mention and their neighbours, and random values that grow from case to case. A case fails like a test does; it is then shrunk to a simpler one that still fails (shrunk values keep their facts), and reported with its values and its seed. The seed comes from the test's name, so runs repeat; `bork test --seed N` runs every property with seed N (the printed command includes `--cases` when it matters: values grow with the number of cases). If a where clause rejects most values (100 draws in a row, in most cases), the test fails with which one. What a property prints is not shown. Parameters of types no values are generated for (functions, scopes, resources) skip the test. `bork test --auto-properties` also property-tests every function of the package whose promises are trusted: `unsafe go` functions that promise facts about their result (or return records with field facts), and functions whose body uses `trust`; each is called on generated arguments, and fails if it breaks a promise or panics (or is skipped if its arguments' facts reject most values). It is opt-in for now, because random arguments could make such a function do IO; once effects say which functions are pure, those will be tested by default.
- **Generic types:** records and sealed types can take type parameters: `type Pair[A, B] = { first: A, second: B }`, `type Tree[T] = sealed { Leaf, Node { left: Tree[T], value: T, right: Tree[T] } }`. Literals take their type arguments from the expected type or from their fields (`Pair { first: 1, second: "one" }` is a `Pair[Int, String]`); a variant without fields (`Tree.Leaf`) needs an expected type. Explicit heads name the specialization: `Pair[Int, String] { first: 1, second: "one" }`, `Tree[Int].Leaf`, or `Option[String].Some { value: "trace" }`. Imported heads (`pkg.Box[Int] { value: 1 }`) work too. Explicit arguments supply field context, including empty containers and nested shorthand, and cannot be replaced by the surrounding expected type. A `where` in a type argument applies to the fields declared with that parameter: in `Pair[String, Int where positive]`, to `second`. It is an error if a field holds the parameter inside another type (`items: List[T]`), since the fact would not be checked there. Type aliases cannot have parameters yet.
- **`Option[T]`** is declared in the prelude as `type Option[T] = sealed { Some { value: T }, None }`, an ordinary generic sealed type. `Option.None` takes its type from where it is used.
- **`match (x) { ... }`** tries arms in order. Arms produce a value, like `if`.
  - **List patterns:** `[]` matches the empty list, `[a, b]` a list of exactly two elements, and `[first, ...rest]` one of at least one, binding the remaining elements to `rest` (`[first, ...]` ignores them). Exhaustiveness covers them: `[]` and `[_, ...]` cover every list.
  - **Patterns nest:** a field can be matched against any pattern, as in `Option.Some { value: ')' }` or `Shape.Circle { center: Point { x: 0, y: 0 } }`. `{ radius }` binds the field to its own name, and `{ radius: r }` binds it to `r`.
  - **A bare name** binds the whole value (`n => n * 2`), unless it is a type, in which case it matches values of that type (`NotFound => ...`).
  - **Constrained bound type patterns** (`p: PosInt` for `type PosInt = Int where positive`, or `p: Int where positive`) test the base type first, then run the predicates. Arms are tried in source order; a false predicate falls through, and the first successful arm wins. The subject is evaluated once, `and`/`or` short-circuit in source order, and a predicate panic propagates normally. A successful arm knows those facts about its bound value. Guarded arms never contribute to exhaustiveness or make later arms unreachable: include an ordinary base-type or wildcard fallback. An earlier unguarded arm can still make a guarded arm unreachable. Predicate arguments follow the usual constants-or-function-parameters rule; function-valued predicate parameters must declare `uses nothing`. Only direct whole-value constraints on bound type patterns are supported. Constrained bare type names, constrained record/variant destructuring, and nested constraints such as `xs: List[Int where positive]` remain errors; bind the constrained value first and destructure inside its arm.
  - **Matches must be exhaustive**, also inside nested patterns: the error lists what is missing (`missing Option.Some { value: false }`), with `_` for a field that has values no arm covers. An arm that can never match is an error.
- **`x?`** on a union keeps the leftmost member and returns every other member from the function, which must be able to return them. On an `Option`, it keeps the `Some` value and returns `Option.None`.
- **Equality** is structural: records, variants, Options, and lists compare by their parts. Functions, scopes, and resources have no `==`. The prelude class `Eq` is built in: every type with `==` has it, and a bound `[T: Eq]` lets generic code compare values of `T` (the prelude's `includes` and `distinct` use it). `Eq` instances cannot be declared, and need no `derive`; default rendering needs no `Show` instance or `derive`. A custom `Show[T]` declares `fn show(x: T): String`; `[T: Show]` always holds and uses the same universal renderer. Custom text does not affect equality or map-key identity.
- **Printing** shows values in bork syntax: `User { name: "Ada", age: 36 }`, `Shape.Empty`.
- **Facts.** `x: Int where positive` requires every caller to show that `positive(x)` holds: by a guard (`if (positive(a)) { transfer(a) }`, or `if (!positive(a)) { return ... }` before the call), by declaring the same requirement on its own parameter, by a callee that promises it (`fn validate(raw: Int): Int where positive | NotPositive`), or by `trust positive(x)`. Simple predicate bodies (comparisons, positive predicate calls, comparison negation, `&&` and `||`) unfold against guards, so `if (a > 0)` proves `positive(a)` when its body is `a > 0`. Comparison operands can be parameters, bindings or fields. Reversed operands match; order negation reverses integer and String comparisons, while Float negation retains its polarity because of NaN. Rule conditions can match these branch comparisons on runtime values as well as evaluate constants. On a constant, the predicate is run at compile time. Promised results are checked on every path. Facts are erased in the generated Go. A `where` (or a constrained alias like `type PosInt = Int where positive`) is checked on parameters, results (and the members of a result union), bindings (`n: Int where positive = ...`), property test parameters, bound type patterns, record and variant fields, list elements, and type arguments held as fields (`Option[Int where positive]`). A constrained type argument of a call (`decodeJson[Port](...)`) is checked where the function takes or returns exactly that type. Everywhere else it is a compile error rather than a fact that is quietly not checked: on Map keys and values, members of a union that is not a result, function types, rule variables, lambda parameters, constrained bare/destructuring patterns and nested pattern type arguments, constructor names that use a constrained alias, the methods of classes (and instance methods' parameters, except where the class declares its type parameter and a constrained instance uses its own type), the type arguments of `Atom`, `Task`, `Channel` and generic types whose fields hold the parameter inside another type, the parts of a call's type argument (`id[List[Int where positive]]`), and a call type argument that the function's parameters or result hold inside another type. See [requirements.md](requirements.md#3-contracts-and-knowledge-in-progress).
- **A program** is a package with `fn main()`, which takes no parameters and returns no value, and may use every effect.

- **Binary data:** `Bytes` is a built-in immutable byte sequence, distinct from
  `List[Byte]`. There is no special literal: `bytes([toByte(0), toByte(255)])`
  copies a byte list, and `utf8Bytes("hello")` gives a string's UTF-8 bytes.
  `utf8String(data)` returns `String | ParseError`, rejecting invalid UTF-8.
  Methods: `length`, `isEmpty`, `get(index)` (`Option[Byte]`), `toList`,
  `slice(from, to)` (exclusive end, `Bytes | OutOfRange`), and `concat(other)`.
  Equality compares contents; empty sequences are equal. Bytes prints as
  `Bytes(00ff)`, lower-case hex, including in records, lists, and maps.
Standard packages may ship `go-deps.mod` and `go-deps.sum` files using Go module syntax for pinned dependencies. A user module can also declare these manifests beside `bork.mod`; requirements merge with imported std dependencies by Go minimum version selection. `bork deps init/get/download` maintains user manifests with Go module tools. See [std Go dependencies](std-go.md).


## Dependency assembly

The prelude compiler intrinsics use ordinary generic-call syntax:

```bork
assemble[Server](app, newConfig, openDb, newServer)
assembleAll[Plugin](app, authPlugin, logPlugin)
assembleRecord[Application](app, newConfig, openDb, newServer, newWorker)
```

Supply one explicit concrete target type, a live Scope, then positional provider
functions. Direct declared providers preserve their parameter/result facts;
function values and typed lambdas are also providers. Generic functions require
monomorphic adapters. Inject an existing value with `() => value`.

Resolution uses exact Bork type identity; wrapper records distinguish brands,
while aliases retain their base identity. Providers run once per call, after
their dependencies, and shared dependencies reuse the same value. Every Scope
parameter receives the target scope. Every provider must be needed. Missing,
duplicate, cyclic, invalid and unused providers produce compile errors with the
full dependency tree.

The expression returns the target followed by its providers' failure union;
`?` and `match` work normally. Its effects combine invoked providers and argument
evaluation. Partial acquisitions remain owned by the target scope until it
closes. There is no implicit rollback, cleanup callback or cross-call cache.

`assembleAll[T]` collects all T providers into a List in provider-list order;
there must be at least one. Singular dependencies must still be unambiguous.
`assembleRecord[R]` resolves each record field separately, sharing repeated
field types and checking ordinary literal facts and construction visibility.
Field defaults do not remove provider requirements; list fields do not aggregate
automatically. `bork describe` on an assembly name or opening parenthesis shows
the graph, invocation order, result union and effects, also available as JSON.
See [assembly requirements](requirements.md#compile-time-dependency-assembly).

Provider bundles are static package declarations:

```bork
providers Services = { config: newConfig, database: openDb, server: newServer }
assemble[Server](app, Services)
assemble[Server](app, Services(database: fakeDb))
```

Entries name monomorphic declared functions, and uppercase bundle names are
exported. A bundle can only appear in an assembly provider list, where it expands
in declaration order. Bundle specializations take only named entry replacements;
each replacement is an ordinary provider expression with the original exact
success product. Its dependency signature, effects and failures can differ.
Replacement expressions evaluate in written argument order; graph slots and
collection roots retain declaration order. Multiple bundles and ordinary providers
can be mixed at a call. Ordinary duplicate rules (including collection roots) and
unused-provider checks apply after expansion. Bundle declarations are flat and do
not include other bundles. Providers construct fresh products on each assembly;
bundles carry no runtime values or cached products.

## Standard packages

Standard packages introduce no new grammar. Their API descriptions, examples
and limits are in the [per-package documentation](std/README.md).

Parallel list methods use ordinary method calls and named/defaulted arguments:
`xs.parMap(f, workers: 4)`, `parFilter`, `parFlatMap`, and `parForEach` require
pure callbacks. Their `In` counterparts take a scope and a `(Scope, T)`
callback, charge its effects plus `state`, and return cancellation as a value.
`parMapUntil[B, E]` and `parMapUntilIn[B, E]` stop on the first failure in a
`B | E` callback result. See [parallel collection semantics](requirements.md#parallel-collections-implemented-bork-pd7rjm).

Task fan-in uses ordinary prelude calls: `tasks.awaitAll()`,
`tasks.awaitFirst(s)`, and `tasks.awaitAllUntil[Success, Failure](s)`.
`race(s, [child => work(child)])` runs a list of callbacks in a child scope;
a direct `List[(Scope) => T]` parameter opens its callback effects, while
`List[(Scope) uses nothing => T]` requires pure callbacks. Generic inputs
can forward open callback values when their type parameter does not occur
in the result. List results and nested list parameters remain closed.
`withTimeout(s, ms, child => work(child))` returns `T | Cancelled`;
`withTimeoutDo` accepts a Unit callback. Typed channel selection is
`[channel.receiveCase(value => event(value))].select(s)`, returning
`Option[Event] | Cancelled`. These add no keywords or syntax.
