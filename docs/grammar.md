# bork grammar

> The syntax the compiler accepts, followed by a concise semantic reference. For a learning path and checked examples, start with the [language guide](language/basics.md).

## Lexical structure

- **Source files** are UTF-8, with the `.bork` extension. A directory of `.bork` files is one package. LSP positions use zero-based lines and UTF-16 columns. Diagnostic and `bork describe` positions use one-based lines and byte columns (see [JSON diagnostics](diagnostics.md) and [compiler code queries](describe.md)).
- **Shebang:** a first-line `#!...` is retained as a comment and selects script mode; other lines cannot contain a shebang.
- **Comments:** `// to end of line` and `/* block */`. They are kept by the lexer and formatter; consecutive `//` lines directly above a field also become its doc comment.
- **Identifiers:** a letter followed by letters, digits, or `_`. Identifiers cannot start with `_`, which is reserved for the compiler.
- **Keywords:** `fn`, `pred`, `type`, `sealed`, `match`, `if`, `else`, `return`, `true`, `false`, `unsafe`, `where`, `and`, `or`, `trust`, `rule`, `generate`, `yield`, `for`, `break`, `continue`. `import`, `use`, `class`, `instance`, `test`, `instances`, `scope`, `with`, `resource`, `private`, `derive`, `uses`, `nothing`, `in`, `ambient`, `logged`, `propagated`, `needs` are keywords only where they start a declaration, a scope block (or its policy), a with block (`with (` where an expression starts), a resource type, a derive list or declaration, a list of effects or needs, or the scope a parameter belongs to, and can otherwise be used as names (a function named `with` cannot be called as `with(...)` where an expression starts). `is` is contextual between a value and a pattern, at comparison precedence; declarations, calls and selectors named `is` remain valid. `select` is a keyword only where an expression starts and `{` follows. `comptime` is contextual before `{` where an expression starts, and before `if`, `for`, or `match` within derivation templates and helpers. `lazy` is contextual before a local or package binding name, and before a record field name. `async` is a keyword only at a local binding head, `async(scopeExpression) name = expr`; ordinary calls and names `async` remain legal. `mock` is a keyword only at the start of a statement or after a binding's `=`, followed by a name.
- **`_`** on its own is the wildcard pattern.
- **Integer literals:** decimal (`10_000`), hex (`0xFF`), binary (`0b1010`), or octal (`0o17`), with `_` allowed between digits, as in Go.
- **Float literals:** `1.5`, `2e10`, `1.5e-3`. A `.` must be followed by a digit (so `5.copy(...)` is a selector).
- **Rune literals:** values of the distinct `Rune` type, one Unicode scalar in single quotes, with Go's escapes: `'a'`, `'\n'`, `'\u00e5'`.
- **Interpolated strings:** `s"Hello, $name! Next year: ${age + 1}"`. `$name` inserts a name and `${...}` any expression (which may contain string literals). `$$` is a dollar sign. Plain strings never interpolate.
- **Typed interpolators:** an adjacent local or imported prefix, `Tag"... $value ..."` or `sql.SQL"... $value ..."`, constructs the prefix library's result type. The same holes and escapes apply, and `s` remains the built-in String prefix. See the protocol below.
- **String literals:** double-quoted, with Go's escape sequences (`\n`, `\t`, `\"`, `\\`, ...).
- **`unsafe go { ... }`:** after `unsafe go`, everything up to the matching `}` is raw Go, not bork tokens (braces inside Go strings, runes, and comments do not count).
- **Statement endings:** a newline ends a statement when the line's last token is an identifier, a literal, `true`/`false`, `return`, `break`, `continue`, `_`, `)`, `]`, `}`, or `?`, as in Go. A `;` can also separate statements on one line. Newlines inside parentheses are ignored, so argument and parameter lists can span lines. A line starting with `|>` or a selector (`.method`) continues the previous expression across blank lines and comments. A leading-dot variant pattern followed by `in` starts a comprehension generator line, and one followed by `=>` starts a match arm; these end the previous line rather than continue a selector. The optional pattern payload may contain nested delimiters, strings, comments and line breaks. An explicit semicolon ends the statement. Method-chain continuation lines format one level deeper; trailing-dot continuation remains valid.
- **List and block separators:** record fields, sealed variants, tag entries, record/list literals, field/payload/list patterns, match/select arms, instance bundles, and `with` bindings require a comma, semicolon, or newline between items. Leading, repeated, and trailing semicolons are accepted, including in empty lists and blocks. A comma must follow an item and may precede semicolons; leading or repeated commas and a comma after a semicolon are invalid. Inside list and positional payload patterns, a newline before a leading-dot variant separates items. This pattern rule does not change selector continuation in expressions. Keep qualified pattern names on one line, or break after the dot. Blocks require semicolons or newlines between statements.

## Syntax

```ebnf
Package    = { File } .
Script     = [ Shebang EOL ] { HeaderDirective EOL } { Import EOL } { Use EOL } { ( Decl | Binding | Expr ) EOL } . (* fn main excludes executable top-level statements; otherwise statements become implicit-main locals; explicit lazy remains package-level *)
HeaderDirective = "// bork:require" ModulePath PinnedVersion | "// bork:unsafe" . (* standalone script header only *)
File       = { Import EOL } { Use EOL } { ( FuncDecl | PredDecl | TypeDecl | AmbientDecl | RuleDecl | TestDecl | ClassDecl | InstanceDecl | DeriveDecl | DeriveTemplate | DeriveHelper | Instances | PackageBinding ) EOL } .
PackageBinding = [ "lazy" ] Ident [ ":" Type ] "=" Expr . (* pure memo; comptime reads bake data; uppercase names are exported *)
AmbientDecl = { "logged" | "propagated" "(" String ")" } "ambient" Ident ":" Type .     (* ambient traceId: String: a value functions read with needs, bound by with *)
Use        = "use" UseItem .                (* use money.DecodeAmount, use money.*, use api.Json *)
UseItem    = Ident | Ident "." ( Ident | "*" ) .
Instances  = "instances" Ident "{" { EOL } { UseItem Sep } [ UseItem ] "}" .
                                             (* instances Json { ItemDecode, ItemEncode, money.Defaults } *)
ClassDecl  = "class" Ident "[" Ident "]" "{" { EOL } [ MethodSig { EOL { EOL } MethodSig } { EOL } ] "}" .  (* class Show[T] { fn show(x: T): String } *)
MethodSig  = "fn" Ident "(" [ Params ] ")" [ FunctionWhere ] [ Uses ] [ ":" Type ] .
InstanceDecl = "instance" Ident [ TypeParams ] ":" Ident "[" Type "]" "{" { EOL } [ InstanceMember { EOL { EOL } InstanceMember } { EOL } ] "}" .
                                             (* instance showBox[T: Show]: Show[Box[T]] { fn show(x: Box[T]): String { ... } } *)
InstanceMember = FuncDecl | "metadata" Type "=" Expr .
Import     = "import" [ Ident ] StringLit .  (* import "example.com/shop/money", or import cash "..." *)
QualIdent  = Ident "." Ident .               (* money.Cents, money.Amount: a name of an imported package *)
TestDecl   = "test" StringLit [ "(" Params ")" ] Block .  (* test "adds numbers" { ... }; parameters (no defaults) make a property test *)
PredDecl   = "pred" Ident "(" Params ")" Block .  (* always returns Bool *)
RuleDecl   = "rule" Ident "(" Params ")" "{" Premises "=>" Conclusions "}" .
Premises   = Expr { "and" Expr } .  (* predicate calls on the variables, and conditions *)
Conclusions = Call { "and" Call } .

TypeDecl   = [ "metadata" ] "type" Ident [ TypeParams ] "=" ( ( [ "private" ] Fields | Sealed | GoName Fields ) { TagGroup } [ Where ] | "resource" [ GoName ] | GoName | Type ) [ Derive ] .
                                             (* type Pair[A, B] = { ... }; type File = resource: values made by unsafe go *)
Derive     = "derive" "(" ( Ident | QualIdent ) { "," ( Ident | QualIdent ) } ")" .  (* derive (Decode, Encode, GoStruct): instances written by the compiler *)
DeriveDecl = "derive" ( Ident | QualIdent ) "for" Type . (* package declaration; bare generic names request universal instances *)
DeriveTemplate = "derive" InstanceDecl . (* one unconstrained target parameter, declared by the class owner *)
DeriveHelper = "derive" FuncDecl . (* expansion-only Bork helper *)
Fields     = "{" { EOL } { Field Sep } [ Field ] "}" .
Field      = [ "lazy" ] Ident ":" Type [ "=" Expr ] { TagGroup } . (* eager defaults are closed values; pure lazy defaults may depend on siblings; preceding // lines are field docs *)
TagGroup   = Ident "{" { EOL } { TagEntry Sep } [ TagEntry ] "}" . (* starts on the same line as the token it follows; go values remain String only *)
TagEntry   = Ident ":" Expr .
Sealed     = "sealed" "{" { EOL } { Variant Sep } [ Variant ] "}" .
Variant    = Ident [ Fields | "(" Type { "," Type } [ "," ] ")" ] { TagGroup } [ Where ] .
Sep        = ( "," | EOL ) { EOL } .        (* commas, semicolons, or newlines; only one comma per separator *)

FuncDecl   = "fn" [ Receiver ] Ident [ TypeParams ] "(" [ Params ] ")" [ FunctionWhere ] [ Uses ] [ Needs ] [ ":" Type ] ( Block | GoBody )
           | "fn" Ident "=" Ident "." "new" .
FunctionWhere = "where" RequirementGroup .
RequirementGroup = Requirement [ ( "and" Requirement { "and" Requirement } )
                              | ( "or" Requirement { "or" Requirement } ) ] .
Requirement = RequirementCall | StableArg CompareOp StableArg
            | "(" RequirementGroup ")" .
(* Mixing and/or at the same nesting level requires parentheses. *)
RequirementCall = ( Ident | QualIdent ) "(" [ StableArg { "," StableArg } ] ")" .
StableArg  = Ident { "." Ident } | Literal .  (* parameters/receiver, field projections, constants *)
CompareOp  = "==" | "!=" | "<" | "<=" | ">" | ">=" .
Uses       = "uses" ( "nothing" | Ident { "+" Ident } ) .  (* uses io + net: the effects io, net, clock, random, state; tailrec is a marker on declared functions *)
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
Param      = Ident ":" Type [ "in" Ident ] [ "=" Expr ] .   (* a default: a literal; only on the last parameters. conn: Conn in prev: it belongs to the scope of parameter prev, or lives as long as it; s: Scope in ch lets values of s be stored in channel ch *)
Type       = Constrained { "|" Constrained } .  (* a union: Int | NotFound *)
Constrained = TypeAtom [ Where ] .
Where      = "where" Clause { "and" Clause } .
Clause     = PredRef { "or" PredRef }          (* alone: p or q *)
           | "(" PredRef { "or" PredRef } ")" .  (* with and: (p or q) and r *)
PredRef    = Ident [ "(" Expr { "," Expr } ")" ] .  (* positive, between(1, 65535), atLeast(lo) *)
TypeAtom   = Ident [ "[" Type { "," Type } "]" ] [ Uses ] | "(" Type ")" | TupleType | FuncType .
TupleType  = "(" Type "," [ Type { "," Type } [ "," ] ] ")" .
FuncType   = "(" [ Type { "," Type } ] ")" [ Uses ] "=>" Type .  (* (Int, String) => Bool, (String) uses io => Ok; TypeAtom Uses applies only to Seq *)

Block      = "{" { EOL } { Stmt EOL { EOL } } [ Stmt ] "}" .  (* the final expression is the block value; commas do not separate statements *)
Stmt       = Binding | Trust | Mock | Expr .
Mock       = [ Ident "=" ] "mock" ( Ident | QualIdent ) [ "." Ident ] "(" [ ( Ident | "_" ) { "," ( Ident | "_" ) } ] ")" Block .
                                             (* in tests: mock payments.Charge(card, amount) { ... }, calls = mock Store.save(s, x) { ... } *)
Trust      = "trust" Call .                  (* trust positive(x) *)
Binding    = TuplePat "=" Expr | ( Ident | "_" | ( "lazy" | "async" "(" Expr ")" ) Ident ) [ ":" Type ] "=" Expr .   (* x = 1, x: Int8 = 1, or _ = write(f, s)? to drop a value *)

Expr       = PipeExpr .
PipeExpr   = OrExpr { "|>" OrExpr } .        (* x |> f(a) is f(x, a); x |> f is f(x) *)
OrExpr     = AndExpr { "||" AndExpr } .
AndExpr    = CmpExpr { "&&" CmpExpr } .
CmpExpr    = AddExpr { ( "==" | "!=" | "<" | "<=" | ">" | ">=" ) AddExpr | "is" TestPattern } .
TestPattern = Pattern | Type . (* no binding names, shorthand fields, or named list rests *)
AddExpr    = MulExpr { ( "+" | "-" | "|" | "^" ) MulExpr } .
MulExpr    = Unary { ( "*" | "/" | "%" | "&" | "<<" | ">>" ) Unary } .
Unary      = ( "-" | "!" | "^" ) Unary | Postfix .
Postfix    = Primary { [ "[" Type { "," Type } "]" ] "(" [ Args ] ")"
                     | "[" Type { "," Type } "]" (* only on a constructor owner, followed by RecordLit or .Variant *)
                     | "." ( Ident | IntLit )
                     | ".copy" "(" Update { Sep Update } [ Sep ] ")"
                     | ".into" "[" Type "]" "(" [ Update { Sep Update } [ Sep ] ] ")"
                     | "?" [ TryMapper ] (* mapper brace must attach: ?{ *)
                     | RecordLit } .
(* In an unparenthesized control head, a brace after ? starts the body.
   Parenthesize a wrapped expression in a control head. *)
TryMapper  = "{" ( Lambda | "_" "=>" Expr ) "}" .
RecordLit  = "{" { EOL } { FieldInit Sep } [ FieldInit ] "}" .  (* after User, Box[Int], Shape[Int].Circle, ".", or "." Ident *)
FieldInit  = Ident ":" Expr .
Update     = Ident { "." Ident } ":" Expr .  (* u.copy(address.city: "Oslo") *)
Args       = Argument { "," Argument } [ "," ] .
Argument   = [ Ident ":" ] Expr .

Primary    = IntLit | FloatLit | RuneLit | StringLit | InterpString | TypedInterp | "true" | "false" | Ident
           | "." [ Ident ]
           | "(" Expr ")" | TupleLit | Block | If | Match | Select | Return | Lambda | ListLit | MapLit | ScopeExpr | Generate | Yield | For | LoopControl | WithExpr | StagedControl .
(* A bare leading "." must be followed by RecordLit: .{ field: value }.
   .Variant and .Variant { field: value } need an expected sealed type;
   .{ field: value } needs an expected record type. Variant patterns may also omit their owner using scrutinee context. *)
TypedInterp = ( Ident | QualIdent ) InterpBody . (* prefix and opening quote must be adjacent; s is reserved *)
(* InterpBody is the double-quoted body with $name, ${Expr}, $$, and string escapes. *)
Generate   = "generate" "[" Type "]" Block .
Yield      = "yield" Expr .
For        = "for" [ LoopHeader | "(" LoopHeader ")" ] Block
           | "for" "{" { EOL } Generator { Sep Clause } { Sep } "}" "yield" Expr .  (* a comprehension; yield on the line of the '}' *)
Clause     = Generator | "if" Expr | Ident [ ":" Type ] "=" Expr | TuplePattern "=" Expr .
Generator  = ( IterationPattern | Pattern ) "in" Expr .  (* a for { } loop's body cannot start this way; a Pattern that can fail to match skips those values *)
IterationPattern = Ident | "_" | "(" IterationPattern "," { IterationPattern "," } [ IterationPattern ] ")" | "(" IterationPattern ")" .
LoopHeader = IterationPattern "in" Expr                  (* for (x in xs) *)
           | Expr                                  (* a Bool condition *)
           | [ LoopInit { "," LoopInit } ] ";" [ Expr ] ";" [ LoopPost { "," LoopPost } ] .
LoopInit   = Ident [ ":" Type ] "=" Expr .         (* in order: each sees the names before it *)
LoopPost   = Ident "=" Expr .                      (* the next values of header names, computed together *)
StagedControl = "comptime" ( If | For | Match ) . (* inside derive definitions only; comptime for { ... } yield v is a staged List, with generator and filter lines only *)
LoopControl = "break" | "continue" .
WithExpr   = "with" "(" { EOL } { WithBind Sep } [ WithBind ] ")" Block .  (* with (traceId: id, principal: p) { ... } *)
WithBind   = ( Ident | QualIdent ) ":" Expr .
ScopeExpr  = "scope" Ident [ "with" Expr { "," Expr } ] Block .  (* scope s { f = fs.Open(path, s)? ... }; scope s with taskTimeout(100.millis()), cleanupTimeout(500.millis()) { ... } *)
Lambda     = ( Ident | "(" [ LParam { "," LParam } ] ")" ) "=>" Expr .  (* x => x + 1 *)
LParam     = Ident [ ":" Type ] .
ListLit    = "[" ( { EOL } { Expr Sep } [ Expr ] | ListComprehension ) "]" .
ListComprehension = "comptime" "for" "(" Ident "in" Expr ")" [ "comptime" "if" "(" Expr ")" ] Expr .
MapLit     = "{" ":" "}" | "{" Entry { Sep Entry } [ Sep ] "}" .  (* {"a": 1, "b": 2}; {:} is the empty map *)
Entry      = Expr ":" Expr .
If         = "if" HeadExpr Block [ "else" ( If | Block ) ] .
Return     = "return" [ Expr ] .
Match      = "match" HeadExpr "{" { EOL } { Arm Sep } [ Arm ] "}" .
HeadExpr   = Expr . (* includes parenthesized expressions; bare Name { starts the body *)
Arm        = Pattern "=>" Expr .
TupleLit   = "(" Expr "," [ Expr { "," Expr } [ "," ] ] ")" .
TuplePat   = "(" Pattern "," [ Pattern { "," Pattern } [ "," ] ] ")" .
Select     = "select" "{" { EOL } { SelectArm Sep } [ SelectArm ] "}" .  (* "select" before "{" where an expression starts *)
SelectArm  = ( [ ( Ident | "_" ) "=" ] Expr | "_" ) "=>" Expr .
                                             (* n = ch.receive(s) => n; out.send(s, x) => Ok; _ => "none ready" *)
Pattern    = TuplePat | "(" Pattern ")" | "_"                             (* anything *)
           | Literal                         (* 1, -1, 1.5, 'a', "a", true *)
           | "[" { EOL } [ ListElems ] "]"            (* [], [x], [first, ...rest], [0, ...] *)
           | ( Ident | "_" ) ":" Type                  (* n: Int, e: NotFound | DbError *)
           | ( "." Ident | Ident [ "." Ident ] | Ident TypeArgs "." Ident )
             [ "{" { EOL } { FieldPat Sep } [ FieldPat ] "}" | "(" { EOL } { Pattern Sep } [ Pattern ] ")" ] .
                                             (* Shape.Circle { radius }, NotFound, User { name }, n *)
ListElems  = ( Pattern { Sep Pattern } [ Sep "..." [ Ident ] ] | "..." [ Ident ] ) [ Sep ] .
FieldPat   = Ident [ ":" Pattern ] .         (* radius, radius: r, radius: 0, center: Point { x: 0 } *)

EOL        = newline | ";" .
```

The formatter writes LF line endings, including after line comments, while
preserving bytes inside block comments, literals, interpolations and raw Go bodies.
Source spans follow the lexer's token ends, independently of printed token text.

## Semantics in brief

- **Everything is an expression.** A block's value is its last expression. An empty block or a block that ends with a statement has type `Ok`; in a function returning a union containing `Ok`, it returns that member, including in an `if` branch or `match` arm.
- **`Ok` is the no-value type and success expression.** Write `fn save(): Ok | Error { Ok }`, `() => Ok`, or omit the result type for a function that just completes. `Ok` carries no payload; it is not a generic Result constructor. It can inhabit a union, but cannot be a standalone binding, parameter, field, or generic value argument (`Channel[Ok]` and `List[Ok]` are invalid). The one exception is a task: `Task[Ok]`, from `fork` or `tasks.TryFork` with work that gives no value, and `await` of it, which gives Ok. `Unit` is a deprecated type alias for one release; `bork check --json` offers edits to replace its type uses with `Ok`.
- **`if` with `else`** produces a value; both branches must have the same type. **`if` without `else`** is only run for its effect.
- **`return`** has type `Never`, which fits wherever any type is expected, so `x = if (c) { return 0 } else { 1 }` works. Code after a `return` is a compile error.
- **Tuples:** `(a, b)` groups heterogeneous positional values; `(a,)` is a singleton, while `(a)` groups an expression. Types use `(Int, String)` / `(Int,)`. There is no empty tuple; `() => expr` keeps its function meaning. `pair.0` selects a statically checked zero-based position. `(a, b) = pair` destructures names, wildcards and nested tuples; match patterns may test elements. Types are structural by arity and ordered element types. Elements evaluate once from left to right. Equality/hash recurse over comparable elements; Encode/Decode use exact-arity JSON arrays and index error paths. Facts, effects and scope ownership recurse through elements. See [tuples](language/types.md#tuples).
- **Bindings are immutable.** Same-block rebinding creates a distinct value of any type, including rebinding parameters in the function body. Nested shadowing is forbidden, except that a loop body carries the names it rebinds (see Loops). Every local must be read or explicitly discarded; parameters are exempt. Prelude functions are an exception to name protection: a local or package function can use their names. Methods have a separate namespace, so a free `fn find` and `xs.find(test)` can coexist.
- **A value that is computed but never used is a compile error** (e.g. calling a function that returns `Int` as a statement). Only a result of exactly `Ok`, or a `Task[Ok]` (whose scope still joins it and reports its panic), may be used as a statement, or end a block, function, lambda or branch that gives no value (an `if` or `match` whose branches give `Ok` and `Task[Ok]` gives `Ok`); unions such as `Ok | IoError` or `Ok | Cancelled` must be handled, or dropped with `_ =`.
- **Numbers:** `Int8`, `Int16`, `Int32`, `Int` (= `Int64`), `Uint8` (= `Byte`), `Uint16`, `Uint32`, `Uint64`, `Float32`, `Float` (= `Float64`). Integers wrap on overflow, like Go.
- **Operators:** `+ - * /` on two numbers of the same type, `%` on two integers of the same type; `+` also concatenates `String`s; `< <= > >=` on numbers or `String`s; `== !=` on two values of the same type, or a union and a value of one of its members; unary `-` on signed numbers; `&& || !` on `Bool`, with short-circuiting. Types never mix implicitly. Dividing by a constant zero is a compile error.
- **Bitwise integers:** `&`, `|`, and `^` require two integers of the same type. Unary `^x` complements the bits within x's width; `~x` is an error with a replacement fix. `<<` and `>>` retain the left operand's type. The count can be any unsigned integer or a signed integer proven nonnegative by a constant, guard, or predicate. Left shifts discard overflowing bits at runtime; right shifts fill with the sign bit for signed integers and zero for unsigned integers. Counts at least the width give zero, or -1 for a negative signed right shift. Shifts and `&` have multiplication precedence; `|` and binary `^` have addition precedence. Bool values use `&&`, `||`, and `!=`.
- **Bit helpers:** import `bork/bits` for integer methods `OnesCount`, `LeadingZeros`, `TrailingZeros`, `RotateLeft`, `Reverse`, `ReverseBytes`, `TestBit`, `SetBit`, `ClearBit`, `Extract`, and `Insert`. The checked methods return `OutOfRange` for invalid indices, fields, or field values; count methods carry range facts. See [integer bits](std/bits.md).
- **Binary formats:** import `bork/binary` for endian integer reads and immutable writes on Bytes, plus immutable Reader cursors and Writer builders. Cursor reads return `(value, nextReader) | BinaryError`. ByteOrder is explicit. See [binary formats](std/binary.md).
- **Constants:** number literals, and `+ - * / %` on them, are computed exactly at compile time (`0.1 + 0.2` is exactly `0.3`). A constant takes its type from where it is used (`x: Uint8 = 255`, `small + 1`); otherwise it is a `Float` if it contains a float literal, an `Int` otherwise. It is computed as its type computes: `7 / 2` is `3` as an `Int`, and `x: Float = 1 / 3` is `0.333...`. It must fit its type.
- **Radix conversion:** importing `bork/strconv` adds integer `Hex`, `Binary`, `Octal`, and `Format` methods returning `String`, with proven width (0..4096) and base (2..36). String `ParseInt`, sized `ParseIntN`/`ParseUintN`, and `ParseByte` methods return the integer or `ParseError`; a proven base is 0 or 2..36, with 0 detecting explicit prefixes. See [the package API](std/strconv.md).
- **Conversions:** `toInt8(x)`, `toInt16`, `toInt32`, `toInt` (`toInt64`), `toUint8` (`toByte`), `toUint16`, `toUint32`, `toUint64`, `toFloat32`, `toFloat` (`toFloat64`), from any number type. If every value of x's type fits, the result is the target type; otherwise it is `Target | OutOfRange` (float to integer drops the fraction, and NaN or infinities never fit). A constant argument is converted at compile time and must fit.
- **Interpolation** renders each value as `toString` does, so any value can go in a string: `s"user: $u"`. There is no printf-style formatting.
- **`panic(message)`** stops the program with a message. It is for bugs, not expected failures (those are union results). Its type is `Never`, so it can end any branch.
- **Development markers:** `dbg(expr)` evaluates a value once, prints `file:line expr = value` to stderr using the same Show renderer as `toString`, and returns the value with its type, facts, and lifetime intact. The probe itself has no tracked effect (so it works in pure functions and predicates); its argument retains its ordinary effects. `todo()` or `todo("message")` has type `Never` and panics with its source location if reached. `bork check` reports both as nonfatal warnings; `--json` marks them with `severity: "warning"`, codes `debug.dbg` / `debug.todo`, and a fix for removing a dbg wrapper. Builds permit both markers.
- **Debugger expressions:** the Debug Console uses the existing expression grammar for locals/parameters, scalar literals, parentheses, eager record fields, integer arithmetic and scalar comparison/boolean/bitwise operators. A standalone `xs.get(i)` preserves list bounds and Option results through staged reads when concrete Option payload types are available. Float/Float32 basic arithmetic uses compiler-owned typed scalar operations from raw IEEE operand reads, including special values and lazy boolean short-circuit expressions. It rejects other calls, map/Unicode string access and other execution-requiring forms; see [debugging](debugging.md#debug-console-expressions).
- **Typed interpolation protocol:** `Prefix"a $x b $y c"` calls the ordinary function Prefix with a compiler-created `StaticParts`, then `.Interpolate(x).Interpolate(y).Finish()`. `StaticParts.values` is the immutable list `["a ", " b ", " c"]`; private construction prevents producing it from runtime Strings through literals, copy, conversion, decoding, or generated constructors. Each hole keeps its type and is checked as an ordinary method argument (including generic bounds, effects, and scope lifetimes). Evaluate the factory first, then each hole and method in source order exactly once, then Finish; the expression has Finish's result type. There is no registration, Show conversion, or implicit import. Libraries expose exported Interpolate and Finish methods on their builder. `sql.SQL"SELECT * FROM users WHERE name = $name"` returns a Statement whose values remain bound parameters; see [bork/sql](std/sql.md).
- **`println(args...)`** prints its arguments separated by spaces, followed by a newline. **`toString(x)`** renders any value the way `println` prints it. The prelude `Show[T] { fn show(x: T): String }` customizes both, interpolation, snapshots, and nested values. Every type has default text, and `show(x)` equals `toString(x)`, including in unbounded generic code. Custom Show instances are coherent: exactly one may be declared for a record or sealed type, in that type's own package. A generic instance must cover every instantiation (`Show[Box[T]]`), with only optional `Show` bounds; specializations, fact constraints, and instances for basic types, `List`, `Map` or `Option` are rejected (wrap them in a declared type instead). No `use` is needed for custom Show. Unlike other classes, Show has one program-wide renderer so printing never changes with the caller's imports or generic context. Default floats print as floats: `3.0`, `0.25`, `1e+21`.
- **Durations:** `Duration { nanos: Int }` is in the prelude; `time.Duration` is the same type. Int methods `nanos`, `micros`, `millis`, `seconds`, `minutes`, and `hours` construct signed spans with saturating overflow; `0.seconds()` is zero. Duration methods `add`, `subtract`, and `multiply(Int)` saturate too, with coherent `Show` and `Ord` instances. All prelude and std timeout/delay/grace arguments use Duration; see [time](std/time.md).
- **The prelude** ([topic files](../internal/prelude/README.md)) is available everywhere: the records `OutOfRange` and `ParseError`; `parseInt`, `parseFloat`, `parseBool` (returning `T | ParseError`); the predicate `notEmpty`; list constructors `range` and `prepend`; and Rune methods `isDigit`, `isLetter`, `isSpace`, `isUpper`, `isLower`. A `Rune` prints as its character (including in nested and generic values). `r.code()` gives its `Int32` code point; `n.rune()` on `Int32` returns `Option[Rune]`, rejecting negative values, surrogates, and values above U+10FFFF. Rune literals are always `Rune`; numeric conversions require `.code()`. Rune ordering compares code points. JSON encodes a Rune as a string containing exactly one Unicode scalar; decoding rejects empty or multi-scalar strings. Operations on lists, strings, maps, options, and bytes are methods (listed below), with no duplicate free functions. Prelude types and the compiler's own functions (`println`, `toString`, `panic`, `toInt8`, ...) cannot be redefined. Concurrency: `fork(s, () => work)` starts a task of scope `s` and gives a `Task[T]`, and `await(task)` its result; work with no value gives a `Task[Ok]`, which may be dropped (`fork(s, () => { ... })` as a statement), and scope `s` cancels its tasks on exit, joins them, then runs cleanup (a `taskTimeout` policy grants a bounded wait before cancellation). `bork/tasks` supplies an explicit bounded Pool whose `TryFork` reports admission failures; ordinary `fork` is unlimited. Scope policies (`ScopePolicy`): `taskTimeout(duration)`, `cleanupTimeout(duration)`, `logFailures()`. `time.Sleep(s, duration)` from `bork/time` pauses until the duration elapses or the scope is cancelled (`Ok | Cancelled`). Cancellation: `cancel(s)`, `cancelAfter(s, duration)`, `cancelled(s)`, and the cancellation points `delay(s, duration)` and `checkpoint(s)` (`Ok | Cancelled`). Resources: `attach(r, s)` keeps `r` open until `s` closes too. For generated context-bound Go bindings, attachment also extends the union of contributing scope lifetimes; sibling results share cancellation, and explicit contexts remain fixed limits. Owned child scopes (`OwnedScope`): `b = openScope(s)` (or `openScope(s, [taskTimeout(100.millis())])`) opens a child of `s` with an explicit end, `b.scope` borrows its `Scope`, and `closeScope(b)` ends it; see [owned child scopes](requirements.md#partially-overlapping-scopes-owned-child-scopes). Channels: `channel[T](s)` (unbuffered), `channel[T](s, capacity)` (a fixed buffer; the capacity must be `validCapacity`, not negative) and `unboundedChannel[T](s)` (a growing buffer), with the methods `ch.send(s, x)`, `ch.receive(s)` (waiting in scope `s`), `trySend`, `tryReceive`, `close`, `length`, `capacity`, `values(s)` (a `Seq` for `for` loops) and `toList(s)`; `forkProducer(s, capacity, out => ...)` closes its channel when the producer returns, and `merge(s, channels)` fans in. Handoffs pass resources between tasks: `handoff[R](s)` or `handoff[R](s, capacity)` (`R` a resource type), whose `h.handOver(s, r)` moves `r` to the handoff's scope and sends it (a failed hand-over releases it), and whose `receive(s)` and `values(s)` give the receiver a resource it may move on; `tryReceive`, `close`, `length` and `capacity` as for channels. Shared state: `atom(x)` makes an `Atom[T]`, `current(a)` reads it, and `update(a, f)` (giving the new value) or `swap(a, f)` (giving the old one) replaces its value atomically, retrying f if another task got there first.
- **Effects** are declared after the parameters, before the result: `fn save(path: String, text: String) uses io: Ok | IoError`, and in function types, before the `=>`: `(String) uses io => Ok`. The effects are `io`, `net`, `clock`, `random`, `state`, and compile-time-only `build` (see [requirements.md](requirements.md#effects-in-signatures)). A function type's effects are part of it: a function that uses less fits where more is allowed (a pure `(Int) => Int` can be passed as `(Int) uses io => Int`), and a lambda's effects are what the calls in its body use. A function-typed parameter or result written without `uses` is *open*: it takes a function with any effects, and a call is charged with what its arguments to open parameters use (or, if the result is open too, the function it gives carries them). `uses nothing` makes such a parameter strictly pure. A function's body may use only what it declares: calling `println` in a function without `uses io` is an error that names the call and the fix. An unexported function may not declare effects it never uses. `main` and test bodies may use every runtime effect (unless `main` declares `uses`, which is then checked), and `main` cannot be called or used as a value. A `pred` cannot declare effects, and its body must be pure. Logging (`bork/log`) is not an effect.
- **Ambient values** are declared with a type, `ambient traceId: String`, which must be data (no scopes, resources, tasks, atoms, channels, or functions). A function reads them by declaring them after its effects, `fn audit(e: Event) uses io needs traceId + principal: Ok`, and then uses the names as values; `needs locale?` reads an `Option[String]`. `with (traceId: id) { ... }` binds values for its block (an expression giving the block's value); an inner `with` may bind a value again. A call of a function that needs a value must be inside a `with` that binds it or in a function that needs it; an optional need is given `Option.Some` of a bound value, the caller's own `Option`, or `Option.None`. Lambdas and functions used as values keep the bindings in force where they are made. `main`, tests, predicates, class and instance methods, and Go bindings cannot declare needs; an unexported function may not declare needs it never reads. An `unsafe go` body sees its needs as Go parameters named after them (another package's with that package's Go prefix). The type may have facts (`ambient attempt: Int where positive`), which a `with` must prove and a function that needs the value knows. The markers `logged` and `propagated("traceparent")`, before `ambient`, also publish a `String`, `Int`, `Float` or `Bool` value bound by `with`: a logged one in every log line written meanwhile, a propagated one under that header for standard-library network code (a header name is an HTTP token, used by one declaration per program). Bork code never reads the published values. See [requirements.md](requirements.md#ambient-values-design-bork-j68yln).
- **`unsafe go` bodies** are Go statements implementing the function. Parameters are visible under their own names, and values have the Go representations listed at the top of the prelude. Standard packages use the stable [Go helper API](std-go.md) for maps, options, and scope contexts. Imports go on the first lines (`import "strings"` or `import text "strings"`). Explicit aliases are visible to unsafe Go bodies throughout their Bork file; separate files may reuse an alias for another package. Unaliased imports retain their shared namespace. Two packages claiming the same name produce a compiler error with guidance to choose distinct aliases. Dot and blank imports are not supported. bork does not type-check the body; the Go compiler does, and reports errors at the bork positions. A package may contain `unsafe go` only if its `bork.mod` lists it (`unsafe "example.com/shop/ffi"`; the prelude and the standard library always may), so a program without a `bork.mod` cannot. The body must declare the effects of the Go it obviously uses: `os`, `os/exec`, `syscall`, and `fmt`'s printing are `io`; `net` (but not `net/url`) is `net`; `time.Now`, `time.Sleep`, and timers are `clock`; `math/rand` and `crypto/rand` are `random`; goroutines, channels, and `sync` are `state`. A package name counts even when another body imports it (the Go imports are shared), and so does a call of a bork function by name. A function value the body gives counts too, if its type declares the effect. `module` paths starting with `bork` are reserved for the standard library.
- **Bindings** call a Go function directly: `fn Atoi(s: String): Int | GoError unsafe go "strconv.Atoi"`. The string names the function with its import path (`"net/url.QueryEscape"`). The signature is checked against the Go function's (with go/types), and values convert at the boundary: numbers to any Go integer or float type that holds every value (`Uint8` to `int`), and from Go checked where they may not fit; `String`, `Bool`; `List[T]` from and to slices (and variadic parameters), and from arrays; `Map[K, V]` from and to Go maps with integer, string, or bool keys (a map from Go is unordered); `Bytes` and `[]byte`; and `Option[T]` from and to pointers. Go results: none (`Ok`), a value, an `error` (`Ok | GoError`), a value and an `error` (`T | GoError`), or a value and a `bool` (`Option[T]`). A Go error is the prelude's `GoError { message, goType }`; a value that does not fit its bork type (a number out of range, a nil pointer where bork has no `Option`) is a `GoValueError { path, message }`, which the result must have exactly when that can happen. Lists, maps, records, and value-converting pointers are copied both ways. Opaque Go pointer and interface values are shared. Standard and declared third-party Go packages can be bound, including methods on opaque types; generic functions and callbacks cannot yet be bound; see [Go interop](requirements.md#go-interop). A binding is `unsafe go` too: its package must be listed in `bork.mod`, and it must declare the effects of the Go function it calls, by the rules above (`fn Getenv(key: String) uses io: String unsafe go "os.Getenv"`).
- **Records** (`type User = { name: String, age: Int }`) are built with their required fields named: `User { name: "Ada", age: 36 }`. Fields are read with `u.name`. A field can declare a closed default (`age: Int = 36`), which a literal or derived decoder uses when that field is missing. An explicit value, including JSON null, is decoded normally. Defaults follow parameter defaults' closed-value rules; empty collections work for generic fields. A record cannot contain itself directly. `derive (GoStruct)` exposes a separate Go struct with exported fields and checked conversions; generated fields can declare ordered tags such as `go { json: "port", short: "p" }`. Mirrors use their existing Go struct and cannot add tags. Fields must have a static Go mapping; phantom generic parameters are allowed.
- **Private record construction:** `type Config = private { port: Int }` keeps fields readable and destructurable, while only the declaring package can build a literal or use `copy`. Nested updates cannot enter a foreign private record; replacing a whole private-valued field with an existing value is allowed. Foreign decoding and GoStruct derivation must use an instance provided by the owning package. Read-only Encode derivation can use public fields.
- **Generated checked constructors:** an owning package can declare `fn New = Config.new` for its own record. The pure function derives every field's name, type, facts and default, returns Config, and requires proof of its completed-value invariants at each call. Generic records produce generic functions; named calls can omit any defaulted field, including defaults before required fields. Positional arguments follow field order and can omit only a defaulted suffix. This declaration deliberately exports construction; `Config.new` is not an implicit callable member. Aliases, sealed types and foreign records cannot be targets. Functions carrying generated field or whole-value requirements cannot be passed as values; use a checked lambda. An opaque record invariant may need an owning-package handwritten wrapper; an invariant delegating to an exported field predicate can use a caller's guard on that predicate.
- **Arithmetic facts:** comparisons involving integer addition and subtraction can be proved from branch and declared bounds only when every intermediate fits its sized type. Strict integer order provides a one-unit gap (`lo < hi` proves `lo + 1 <= hi`); Float arithmetic and unchecked wrapping operations gain no algebraic facts.
- **Whole-value invariants:** `type Range = { lo: Int, hi: Int } where ordered` requires a pure predicate on the completed value. Every value carries that guarantee; construction, `copy`, decoding, Go conversion, and property generation enforce it. Sealed declarations can constrain every variant, and an individual variant can add a clause on the parent sealed value (`Running { lo: Int, hi: Int } where valid`). That clause becomes available when matching the variant. Clauses use ordinary `and`/`or` and constant arguments. Validators and their declared helpers cannot assume the target type's invariant. A validator must accept candidates without parameter `where` requirements; calls through function values and class methods are conservatively rejected in its validation call graph.
- **Sibling field facts** use the same `where` syntax: `type Range = { lo: Int, hi: Int where atLeast(lo) }`. Predicate arguments can name any sibling in the record or the same sealed variant, regardless of field order. Construction checks the completed field values, including defaults. Selection and destructuring retain these relations. Derived decoding and Go conversions validate them after converting all fields.
- **`copy`** makes a changed copy: `u.copy(age: 37, address.city: "Oslo")`. Paths reach into nested records; two updates may not overlap (`address` and `address.city`). Changing a constrained field or a sibling it refers to rechecks the relation on the resulting values, including nested updates.
- **`into`** converts records with `user.into[UserDto](name: "Ada", address.city: "Oslo")`. It copies compatible target fields, converts nested records/lists/options, and uses defaults for absent fields. Named overrides run after the receiver in source order; an override union's members outside its field type become conversion failures, stopping later work. Every new candidate proves its target facts and obeys private construction. Identical values without overrides are reused, with additional target-alias facts still checked. See [record conversion](requirements.md#record-conversion-implemented-bork-2zn4s1).
Package tag groups are parsed on fields, variants and record/sealed bodies. Typed package groups currently report an unsupported-tag error; type checking and shape access follow separately. The built-in `go` group remains usable only on fields with string values. Several groups may follow a declaration in order. A group must start on the same line as the token it follows, including after a multiline body closes; a default expression ends before its group identifier.

- **Sealed types** (`type Shape = sealed { Circle { radius: Int }, Empty }`) list all their variants. Variants are always qualified: `Shape.Circle { radius: 1 }`, `Shape.Empty`. For an exported type, upper-case variants are visible wherever the type is; lower-case variants can only be constructed or matched in their declaring package, including generic variants. Fields keep their existing visibility.
- **Match payload facts:** destructuring a sealed union member retains the member's declared field facts. For `Option[Text] | Error`, where `Text` is a fact alias, `Option.Some(value)` binds a value with that fact, including for imported function results.
- **Unions** (`Int | NotFound | DbError`) hold a value of any one of their types. A value of a member type, or of a smaller union, can be used where the union is expected. `type Lookup = Int | NotFound` names a union. `Ok` can be a member: `fn save(x: Item): Ok | DbError` either does its work or fails, and a body that ends without a value (or with a call that returns nothing) produces the `Ok`. Ok-valued expressions can also supply that member in arguments, fields, and collection elements; their effects run before the value is passed. With `?`, `save(x)?` is a statement that returns the error.
- **`List[T]`** is built in and immutable: `[1, 2, 3]`, or `[]` where the type is known (`xs: List[Int] = []`). Lists print as `[1, 2, 3]`.
- **Functions are values.** `(Int) => String` is a function type; a lambda is `x => x + 1`, `(a, b) => a + b`, or `(x: Int) => ...` where nothing says the parameter's type. Named functions can be passed (`xs.map(double)`) unless they have `where` requirements. Lambdas see the values around them; `return` and `?` are not allowed in them.
- **Generic functions** take type parameters: `fn first[T](xs: List[T]): Option[T]`. Calls infer them by unification, from the arguments (lambdas last, so `xs.fold(0, (sum, x) => sum + x)` works), the expected result, and what lambdas' bodies give (in `words.fold([], (acc, w) => acc.append(w))` the body decides what `[]` holds). Inside, a type parameter can be passed around, stored, printed, and matched with a type pattern (`v: T`), and compared with `==` when it has an `Eq` bound (`[T: Eq]`). Lists and records are comparable when all their parts are comparable; function values have no `==`.
- **Maps:** `Map[K, V]` is built in, written `{key: value, ...}` (`{:}` when empty); keys are expressions (`{"user": name}`), of any type with `==`. Maps have methods too: `m.get(k)`, `m.put(k, v).keys()`. A `{` where an expression starts is a map when its first line is `key: value` followed by `,`, `}`, or a newline, and otherwise a block (`{}` is an empty block). Maps never change and are persistent: putting or removing a key copies only the path to it (a hash trie, log32 of the size) and shares the rest, so old versions are cheap to keep. Two maps are equal when they have the same entries, in any order. List and map keys use structural equality, independently of their text. A map keeps its keys in the order first added; `m.sorted()` (keys with `Ord`) or `m.sortedBy(less)` gives one that keeps them sorted (a balanced tree), `m.unordered()` a hash map that keeps no order (smaller and quicker to change, traversed in an order that differs between runs; printing sorts numeric and string keys by value, and other keys by their text, with mixed kinds grouped), and `m.inOrder()` goes back. Either is a `Map`, and operations keep the order of the map they start from. `entries()` gives a list of the prelude's `Entry[K, V]`; a list of entries builds a map with `entries.toMap()` (a duplicate key keeps its later value). A `Map[String, V]` is a JSON object for `codec.Decode` and `codec.Encode`.
- **Method references** use `Type.method`: `words.map(String.byteLength)` passes a `(String) => Int`, and `groups.map(List.length)` infers the generic receiver from the expected function type. The receiver is the first argument of the reference: `size: (List[Int]) => Int = List.length; size([1, 2])`. Imported types use `model.Point.Value`; references see the same methods as receiver calls, including exported extension methods. Existing sealed variants take precedence, so `Option.None` and `Shape.Circle` keep their meaning. Function-valued fields are not method references; `Type.method` names a declared method. A reference retains declared effects and closes open callback parameters as pure, just like a named function value. Methods with `where` requirements cannot be passed as values; use a lambda that checks them. Concrete aliases fix the receiver type (`type Ints = List[Int]` makes `Ints.length` a `(List[Int]) => Int`). Generic references need a known function type; direct calls such as `List.length(xs)` infer from their arguments, and explicit type arguments include the receiver's parameters (`List.map[Int, String](xs, f)`). Bound references such as `value.method` are not supported; use `x => value.method(x)`.
- **`Ord`:** the prelude class `Ord[T]` (`compare(a, b)`: negative, 0, or positive) has instances for the number types, `String`, and `Rune`.
- **Named arguments:** `http.Listen(addr, s, handler, maxBodyBytes: 1048576)` skips an optional parameter; `config(port: 443, host: "example")` labels required parameters too. Positional arguments precede names, names may follow in any order, and each parameter is supplied once. Unfilled optional parameters use their defaults. Supplied arguments evaluate once in source order. Labels are available on direct declared function, class and receiver-method calls, including direct `Type.method(receiver, name: value)` calls; the receiver is always positional. A pipeline fills the first positional parameter. Function values and compiler builtins have no parameter names. Renaming a parameter breaks named callers. `copy` uses the same colon separator and rejects its old `=` form with a structured replacement fix; a call argument written `name = value` gets the same fix.
- **Default parameter values:** `fn greet(name: String, greeting: String = "Hello")` can be called as `greet("ada")`. Defaults go only on the last parameters, and are closed values: literals (numbers, strings, runes, Bools, and list and map literals of them), which take their type from each call (`xs: List[T] = []`), or record and variant values of closed values (`level: Level = Level.Info`, `retry: Retry = Retry { times: 3 }`), whose names mean what they mean where the function is declared, so they work from other packages. A call that leaves parameters out gets their defaults as arguments, and facts are checked as for written ones.
- **String indexes** count Unicode code points, including `indexOf`, `substring`, `runeAt` and regex match offsets. Explicit `byteLength`, `byteIndexOf` and `byteSubstring` use UTF-8 bytes. Code points are not grapheme clusters; see [String indexing](std/strings.md).
- **Methods** are the primary API for operations on a value's type. They chain left to right: `users.filter(u => u.age >= 18).map(u => u.name).join(", ")`, `m.get("a").getOr(0)`, `s.trim().split(",")`. A method is declared with a receiver, as in Go: `fn (p: Point) plus(q: Point): Point { ... }`; generic receivers name their type parameters among the method's (`fn (xs: List[T]) second[T](): Option[T]`). The receiver is the method's first parameter, so facts and lifetimes apply as for any argument (`xs.first()` needs `xs` to be known `notEmpty`). Type arguments can be given as for functions; those the receiver decides are left out: `xs.map[String](f)` gives `map[A, B]` its `B` (giving all of them, `xs.map[Int, String](f)`, works too). Methods can be declared on `List`, `Map`, the basic types, and declared types, by any package; each type has its own namespace of methods, apart from functions, so `List`'s `get` and `Map`'s `get` coexist. `x.m(a)` finds `m` among the methods of `x`'s type that the code sees: its own package's first, then the exported ones of the type's package and of the imports, then the prelude's; a record field holding a function is called as before (`r.f()`). The prelude's methods: on `List`, `length`, `isEmpty`, `first`, `get`, `head`, `last`, `map`, `flatMap`, `filter`, `fold`, `find`, `any`, `all`, `count`, `forEach`, `take`, `drop`, `reverse`, `concat`, `append`, `sortWith`, `includes`, `distinct`, `join`, `sorted`, `sortBy`, `groupBy` (a `Map[K, List[T]]`), and `toMap` (of a `List[Entry[K, V]]`); on `String`, `byteLength`, `runeCount`, `contains`, `startsWith`, `endsWith`, `indexOf`, `byteIndexOf`, `toUpper`, `toLower`, `trim`, `replaceAll`, `repeat`, `runeAt`, `substring`, `byteSubstring`, `split`, `fields`, `lines`; on `Option`, `map`, `flatMap`, `getOr`, `isSome`, `isNone`; on `Map`, `get`, `getOr`, `has`, `put`, `remove`, `size`, `isEmpty`, `keys`, `values`, `entries`, `merge`, `mapValues`, `filter`, `forEach`, `sorted`, `sortedBy`, `inOrder`, `unordered`.
- **`|>`** passes a value as the first argument of a free function (your own, or generic helpers): `xs |> summarize(prefix)`, or `x |> toString`. Operations on a value's type are methods: `users.filter(u => u.age >= 18).map(u => u.name)`. Piping to a method without a free function of the same name is an error with a suggested rewrite to `x.m(a)`. To pass an operation as a function value, use a method reference (`words.map(String.byteLength)`) or a lambda (`words.map(s => s.byteLength())`).
- **Tests:** `test "name" { ... }` declares a test, run by `bork test` and left out of programs. `assert(cond)` and `assertEqual(actual, expected)` fail a test with the position (and both values); so does a `panic`. `assertSnapshot(x)` compares `toString(x)` with a snapshot file in the `snapshots` directory next to the package's sources (for `bork test file.bork`, next to the file), and fails with a line diff if it differs, or with the value if it is missing; `bork test --update` writes the missing and different ones instead. A test's snapshots are named after it: `test "renders an invoice"` uses `renders_an_invoice.snap`, then `renders_an_invoice.2.snap`, in the order its `assertSnapshot` calls run, so they must run in a fixed order (not from concurrent tasks). Rules and property tests cannot snapshot. Tests run in test mode: facts the compiler takes on trust are checked as they run, so a `trust positive(x)` that does not hold, or an `unsafe go` function that breaks its promise, fails the test with the value. (`test` is not a keyword.)
- **Mocks:** in a test, `mock Fetch(url) { "fake" }` replaces the function `Fetch` from that statement to the end of the block it is in (a nested block can mock it again, or restore it by ending). The target is a function of the test's package, an exported function of an imported package (`http.Get`), or a method (`Store.save`, whose receiver is the first parameter); it must declare effects, and cannot be a predicate, a class method, or part of the prelude; a generic target gets one generic mock for every instantiation, which sees its type parameters by name. The parameters are names only, one per parameter of the target (`_` ignores one); their types and facts come from the target, and the body is checked as the target's body would be: it must give the target's result, facts included, and may use the target's effects and `state`. Inside the body, naming the target (a call, or a value) means the function before the mock: an outer mock, or the real function. `m = mock ...` binds a handle of the prelude's type `Mock[A]`, where `A` is the target's call record (`FetchCall { url: String }`, a field per parameter, made by the compiler): `m.count()` is how many calls it answered, `m.calls()` lists them as text (`Fetch("a")`), `m.args()` as call records, `m.expect(times: 2)` / `m.expect(atLeast: 1, atMost: 3)` / `m.expectWhere(c => c.url == "a", times: 1)` declare how many calls it must answer (checked when the mock ends), and `m.waitFor(n)` / `m.waitForWhere(matcher, n)` wait for calls from other tasks. Mocks belong to the test: they are seen by the tasks, servers, and Go goroutines started while they are in force, and by nothing else; when the block ends, the mock waits for its calls still running, and a mock written directly in a `scope` block lasts until that scope's tasks are done. `mock` is allowed only in test bodies, not in lambdas or other mocks. Programs (`bork build`, `bork run`) are generated as if there were no mocks; see [requirements.md](requirements.md#mocking-in-tests-design-bork-53lit4).
- **Property tests:** a test with parameters, `test "transfer scales" (amount: Int where positive) { ... }`, runs its body on 100 cases of generated values (`bork test --cases N` changes that), which meet the parameters' where clauses, including those on list elements and record fields; a clause may name another parameter (`hi: Int where atLeast(lo)`), which is then generated first. Values are drawn from edge cases (0, ±1, the type's limits, -0.0, infinities, NaN, "", ...), the constants the clauses and their predicates mention and their neighbours, and random values that grow from case to case. A case fails like a test does; it is then shrunk to a simpler one that still fails (shrunk values keep their facts), and reported with its values and its seed. The seed comes from the test's name, so runs repeat; `bork test --seed N` runs every property with seed N (the printed command includes `--cases` when it matters: values grow with the number of cases). If a where clause rejects most values (100 draws in a row, in most cases), the test fails with which one. What a property prints is not shown. Parameters of types no values are generated for (functions, scopes, resources) skip the test. `bork test --auto-properties` also property-tests every function of the package whose promises are trusted: `unsafe go` functions that promise facts about their result (or return records with field facts), and functions whose body uses `trust`; each is called on generated arguments, and fails if it breaks a promise or panics (or is skipped if its arguments' facts reject most values). It is opt-in for now, because random arguments could make such a function do IO; once effects say which functions are pure, those will be tested by default.
- **Generic types:** records and sealed types can take type parameters: `type Pair[A, B] = { first: A, second: B }`, `type Tree[T] = sealed { Leaf, Node { left: Tree[T], value: T, right: Tree[T] } }`. Literals take their type arguments from the expected type or from their fields (`Pair { first: 1, second: "one" }` is a `Pair[Int, String]`); a variant without fields (`Tree.Leaf`) needs an expected type. Explicit heads name the specialization: `Pair[Int, String] { first: 1, second: "one" }`, `Tree[Int].Leaf`, or `Option[String].Some("trace")`. Imported heads (`pkg.Box[Int] { value: 1 }`) work too. Explicit arguments supply field context, including empty containers and nested shorthand, and cannot be replaced by the surrounding expected type. A `where` in a type argument applies to the fields declared with that parameter: in `Pair[String, Int where positive]`, to `second`. It is an error if a field holds the parameter inside another type (`items: List[T]`), since the fact would not be checked there. Transparent aliases can take parameters too: `type Index[T] = Map[String, List[T]]`, `type Result[T] = T | ApiError`. An alias use supplies all its arguments, including in a constructor head (`Alias[Int] { ... }`); aliases preserve the expanded type's identity, construction ownership, effects and union member order. Alias cycles are errors. Facts in alias arguments follow their positions in the expansion, with the same restrictions as directly written types.
- **`Option[T]`** is declared in the prelude as `type Option[T] = sealed { Some(T), None }`, an ordinary generic sealed type. `Option.None` takes its type from where it is used.
- **`match (x) { ... }`** tries arms in order. Arms produce a value, like `if`.
  - **List patterns:** `[]` matches the empty list, `[a, b]` a list of exactly two elements, and `[first, ...rest]` one of at least one, binding the remaining elements to `rest` (`[first, ...]` ignores them). Exhaustiveness covers them: `[]` and `[_, ...]` cover every list.
  - **Patterns nest:** a field can be matched against any pattern, as in `Option.Some(')')` or `Shape.Circle { center: Point { x: 0, y: 0 } }`. `{ radius }` binds the field to its own name, and `{ radius: r }` binds it to `r`.
  - **A bare name** binds the whole value (`n => n * 2`), unless it is a type, in which case it matches values of that type (`NotFound => ...`).
  - **Constrained bound type patterns** (`p: PosInt` for `type PosInt = Int where positive`, or `p: Int where positive`) test the base type first, then run the predicates. Arms are tried in source order; a false predicate falls through, and the first successful arm wins. The subject is evaluated once, `and`/`or` short-circuit in source order, and a predicate panic propagates normally. A successful arm knows those facts about its bound value. Guarded arms never contribute to exhaustiveness or make later arms unreachable: include an ordinary base-type or wildcard fallback. An earlier unguarded arm can still make a guarded arm unreachable. Predicate arguments follow the usual constants-or-function-parameters rule; function-valued predicate parameters must declare `uses nothing`. Only direct whole-value constraints on bound type patterns are supported. Constrained bare type names, constrained record/variant destructuring, and nested constraints such as `xs: List[Int where positive]` remain errors; bind the constrained value first and destructure inside its arm.
  - **Matches must be exhaustive**, also inside nested patterns: the error lists what is missing (`missing Option.Some(false)`), with `_` for a field that has values no arm covers. An arm that can never match is an error.
- **`x?`** on a union keeps the leftmost member and returns every other member from the function, which must be able to return them. On an `Option`, it keeps the `Some` value and returns `Option.None`. `?` is rejected inside lambdas and lazy/async initializers (including lazy field recipes and package bindings); use `match` there. The scope expression in `async(scopeExpression)` retains the enclosing function boundary and may use `?`. These implicit returns must satisfy the function's result predicates, including when `?` is nested in an argument or block. Predicates on an entire union apply to each returned member; member-specific promises apply only to that member.
- **Pattern tests.** `value is Pattern` returns Bool and evaluates the value once. It accepts match patterns without bindings, including contextual variants, nested fields and lists. Bare constrained types and direct `where` clauses run their predicates after the structural test. They do not narrow existing bindings or establish surrounding facts. Valid disjoint tests evaluate false; statically certain tests warn. `test.AssertIs[T](value)` from `bork/test` checks the target type and predicates, returns the narrowed value with proven facts, and fails with the expected type, actual value and actual bork type. Its input type is inferred independently of the single explicit target.

- **Equality** is structural: records, variants, Options, and lists compare by their parts. Functions, scopes, and resources have no `==`. The prelude class `Eq` is built in: every type with `==` has it, and a bound `[T: Eq]` lets generic code compare values of `T` (the prelude's `includes` and `distinct` use it). `Eq` instances cannot be declared, and need no `derive`; default rendering needs no `Show` instance or `derive`. A custom `Show[T]` declares `fn show(x: T): String`; `[T: Show]` always holds and uses the same universal renderer. Custom text does not affect equality or map-key identity.
- **Printing** shows values in bork syntax: `User { name: "Ada", age: 36 }`, `Shape.Empty`.
- **Facts.** `x: Int where positive` requires every caller to show that `positive(x)` holds: by a guard (`if (positive(a)) { transfer(a) }`, or `if (!positive(a)) { return ... }` before the call), by declaring the same requirement on its own parameter, by a callee that promises it (`fn validate(raw: Int): Int where positive | NotPositive`), or by `trust positive(x)`. Simple predicate bodies (comparisons, positive predicate calls, comparison negation, `&&` and `||`) unfold against guards, so `if (a > 0)` proves `positive(a)` when its body is `a > 0`. Comparison operands can be parameters, bindings, fields, or pure computed expressions such as `xs.length()` and `a + b`. Repeated expressions share identity only when resolved operations, runtime types, generic dictionaries and inputs match; no algebraic equivalence is inferred. Function-level `where index < xs.length()` uses the same identities. Effectful calls, mutable Go values, unknown generic inputs, callbacks and lazy sequences do not have reusable computed call identities. Prelude list length can still observe containers with generic or opaque elements. Reversed operands match; order negation reverses integer and String comparisons, while Float negation retains its polarity because of NaN. Rule conditions can match these branch comparisons on runtime values as well as evaluate constants. On a constant, the predicate is run at compile time. Promised results are checked on every path. Facts are erased in the generated Go. A `where` (or a constrained alias like `type PosInt = Int where positive`) is checked on parameters, results (and the members of a result union), bindings (`n: Int where positive = ...`), property test parameters, bound type patterns, record and variant fields, list elements, and type arguments held as fields (`Option[Int where positive]`). A constrained type argument of a call (`json.Decode[Port](...)`) is checked where the function takes or returns exactly that type. Everywhere else it is a compile error rather than a fact that is quietly not checked: on Map keys and values, members of a union that is not a result, function types, rule variables, lambda parameters, constrained bare/destructuring patterns and nested pattern type arguments, constructor names that use a constrained alias, the methods of classes (and instance methods' parameters, except where the class declares its type parameter and a constrained instance uses its own type), the type arguments of `Atom`, `Task`, `Channel` and generic types whose fields hold the parameter inside another type, the parts of a call's type argument (`id[List[Int where positive]]`), and a call type argument that the function's parameters or result hold inside another type. See [requirements.md](requirements.md#3-contracts-and-knowledge-in-progress).
- **Result proof reachability:** guards decided by literal-backed eager values and independent record/copy fields prune impossible `if` arms and short-circuit operands. Unknown guards keep all paths, including implicit `?` returns. This proof step does not execute predicates or helpers and does not inspect runtime lazy/async cell recipes.
- **Control heads:** parentheses around `if`, `match`, and all three nonempty `for` heads are optional, including staged `comptime` controls. In a bare head, a name followed by `{` starts the body: wrap record literals and braced test patterns in parentheses. Delimited call arguments, lists and grouped expressions allow record literals normally. The formatter preserves head parentheses by default; `bork fmt --simplify` removes redundant ones, including around a comprehension's `if` filter. Comptime list-comprehension headers and guards keep their required parentheses because no brace terminates them.
- **Loops:** `for x in xs` goes through a `List` or `Seq`; iteration bindings also accept irrefutable tuple patterns, such as `for (key, value) in m.pairs()` and nested tuples with `_`. Tuple elements bind fresh names per round and cannot shadow enclosing names. `Map.pairs()` returns a list of tuples in map traversal order; `List.indexed()` and lazy `Seq.indexed()` pair each element with its zero-based index (reset on each traversal); `for { }` loops until `break`, `return`, `?` or a panic; `for cond` checks a `Bool` before each round; `for init; cond; post` binds header names in a scope of the loop's own (they cannot shadow enclosing names, and sibling loops may reuse them), checks the condition (none means `true`), and after each round and on `continue` binds the post clause's next values, computed together from the round's values. Each round's header values are fresh bindings, so closures capture that round's. The post clause can only rebind the names the loop carries, must keep each name's type, and cannot use `return`, `?`, `break` or `continue`. A loop carries the names its body rebinds that the block containing it may rebind: at the body's top level and in the blocks of `if`, `match` and block statements in it, with branch values joined where branches meet, through `break` and `continue`, to the next round and after the loop. A carried name keeps its type, its declared facts hold every round and after the loop, and a carried value only read to compute its own next value is unused. A header name's declared facts (`i: Int where nonNegative = 0`) must hold for its first and every next value, and are known in the body; the condition is known in the body and the post clause. A loop is an `Ok` expression with an `Ok` body; a loop without a condition and without a `break` has type `Never`. See [the design](design/loops.md).
- **Comprehensions:** `for { x in xs; y in x.ys; if p(y); z = f(y) } yield (x, z)` is a lazy `Seq[T]`, the generator `generate[T] { for x in xs { for y in x.ys { if p(y) { z = f(y); yield (x, z) } } } }`: each generator line is a loop around the lines after it, each `if` line a filter whose facts hold after it, and each binding a statement before them. A generator's pattern can be any match pattern (`.Some(v) in xs`, `u: User in mixed`, `Shape.Circle { radius } in shapes`): its line becomes `for e in source { match e { pattern => { ... }, _ => {} } }` with a hidden name `e`, so values it does not match are skipped and no exhaustiveness is needed. `T` is the yield's type, or `U` when the context expects `Seq[U]`. Laziness, effects and early-exit cleanup are generate's: nothing runs, not even the first source, until the sequence is consumed, and each consumption starts again. Names are new (they cannot shadow or rebind), and a clause or the yield cannot use `break`, `continue`, `return`, `?` or another `yield`. A filter condition, like an `if` head, cannot start a bare `Name {` record literal; wrap it in parentheses.
- **Tail calls:** a function's calls of itself in tail position (the body's value, a `return` operand, and, within those, block tails, both `if`/`else` branches and `match` arms) compile to jumps, with fresh parameter copies per call for closures. A call inside a `scope` or `with` block, a block with a mock in force, the operand of `?`, a lambda, a generator, a lazy or async initializer, a lazy field, a comptime block, instance methods, a call with other type arguments, and calls of functions taking an `OwnedScope` or their caller's location are ordinary calls. In tests, a mock in force answers a self call as it would an ordinary one. `tailrec` in a declared function's `uses` list (`uses io + tailrec`) is a marker, not an effect: compilation fails unless the function calls itself, every such call is a jump, and no cycle of direct calls through another function reaches it. It is not part of the function's type, callers do not declare it, and it is rejected in function types, class methods, `unsafe go` functions and `main`. `uses tailrec` alone declares no effects. See [the design](design/loops.md).
- **A program** is a package with `fn main()`, which takes no parameters and returns `Ok` or a union beginning with `Ok` (`Ok | E`), and may use every runtime effect. A returned failure is reported after scope cleanup and exits nonzero.

- **Binary data:** `Bytes` is a built-in immutable byte sequence, distinct from
  `List[Byte]`. There is no special literal: `[toByte(0), toByte(255)].toBytes()`
  copies a byte list. Import `bork/encoding`: `encoding.Utf8("hello")` gives a string's UTF-8 bytes.
  `encoding.ParseUtf8(data)` returns `String | ParseError`, rejecting invalid UTF-8.
  Methods: `length`, `isEmpty`, `get(index)` (`Option[Byte]`), `toList`,
  `slice(from, to)` (exclusive end, `Bytes | OutOfRange`), and `concat(other)`.
  Equality compares contents; empty sequences are equal. Bytes prints as
  `Bytes(00ff)`, lower-case hex, including in records, lists, and maps.
Standard packages may ship `go-deps.mod` and `go-deps.sum` files using Go module syntax for pinned dependencies. User modules declare `require <module> <canonical version>` lines in `bork.mod`, with Go-format checksums in `bork.sum` and a generated `go.mod`. Check/build diagnoses requirement drift from the generated manifest. Requirements merge with imported std dependencies by Go minimum version selection. `bork deps init/get/download` maintains them with Go module tools; `bork deps migrate` converts legacy `go-deps.mod`/`go-deps.sum` projects, which remain readable. See [std Go dependencies](std-go.md). External bork libraries are Go modules with matching `bork.mod` and generated `go.mod` declarations; imports resolve from the Go-selected module graph and retain ordinary export and cycle rules. Dependency-owned unsafe grants are scoped to their own module. The helper reports new unsafe library releases without prompting.


## Module compiler requirements

`bork.mod` may include one `bork <version>` directive after its `module` line. A minor version such as `0.4` declares a minimum of `v0.4.0` and resolves its latest patch when the CLI needs another compiler; a full version such as `0.4.2` declares that minimum and download target. Compiler selection settings and cache behavior are described in [the CLI](cli.md#compiler-versions).

## Internal helper locations

`compilerCallerLocation()` is an internal zero-argument intrinsic accepted only
in prelude and standard-library helpers. It opts the enclosing helper into a
hidden caller location without changing public call or function-type syntax.
Ordinary packages cannot call it. See the caller-location contract in
[requirements.md](requirements.md#caller-locations-for-internal-helpers-implemented).

## Dependency assembly

The prelude compiler intrinsics use ordinary generic-call syntax:

```bork
assemble[Server](app, newConfig, openDb, newServer)
assembleAll[Plugin](app, authPlugin, logPlugin)
assembleRecord[Application](app, newConfig, openDb, newServer, newWorker)
```

Supply one explicit concrete target type, a live Scope, then positional provider
functions. Direct declared preserve their parameter/result facts;
function values and typed lambdas are also providers. Generic functions require
monomorphic adapters. Inject an existing value with `() => value`.

Resolution uses exact Bork type identity; wrapper records distinguish brands,
while aliases retain their base identity. Providers run once per call, after
their dependencies, and shared dependencies reuse the same value. Every Scope
parameter receives the target scope. Every provider must be needed. Missing,
duplicate, cyclic, invalid and unused produce compile errors with the
full dependency tree.

The expression returns the target followed by its providers' failure union;
`?` and `match` work normally. Its effects combine invoked and argument
evaluation. Partial acquisitions remain owned by the target scope until it
closes. There is no implicit rollback, cleanup callback or cross-call cache.

`assembleAll[T]` collects all T into a List in provider-list order;
there must be at least one. Singular dependencies must still be unambiguous.
`assembleRecord[R]` resolves each record field separately, sharing repeated
field types and checking ordinary literal facts and construction visibility.
Field defaults do not remove provider requirements; list fields do not aggregate
automatically. `bork describe` on an assembly name or opening parenthesis shows
the graph, invocation order, result union and effects, also available as JSON.
See [assembly requirements](requirements.md#compile-time-dependency-assembly).

Provider bundles are ordinary tuple values:

```bork fragment
import "bork/test"
Services = (newConfig, openDb, newServer)
assemble[Server](app, Services)
assemble[Server](app, test.SwapAt(Services, 1, fakeDb))
```

Assembly splices one level of statically known tuple shape in positional order.
Tuple expressions evaluate once in written order, then execute in graph
order and construct fresh products. Nested tuples are invalid provider elements.
Ordinary function-value capture and contract restrictions apply to tuple entries.
`test.Swap` uses a unique exact element type; `test.SwapAt` uses a pure constant
zero-based Int position and requires the same exact element type. Neither adapts
changed dependencies, failures or effects. See [package migration](language/packages.md)
for removed `providers` declarations and specialization calls.

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
`withTimeout(s, duration, child => work(child))` returns `T | Cancelled`;
`withTimeoutDo` accepts an Ok callback. These add no keywords or syntax.
Selection among channels is the `select` expression (see the grammar).

Transparent async local bindings start a task immediately: `async(scopeExpression) name [ : T ] = expr`.
The scope expression evaluates once, and every read awaits the shared result of
type T. Initializer effects count at declaration; unread tasks follow scope
cancellation, joining and panic policies. Explicit return stays inside the
initializer result boundary; `?` is rejected. See [the async design](design/async.md).

## Compile-time computation

`comptime { ... }` evaluates a closed pure block during compilation. Blocks have
an independent return/`?` boundary, like lazy initializers. Runtime parameters,
calls, lazy cells and ambient needs cannot be captured; literals, composite
literals and earlier computed bindings can. Types must be concrete, including
recipe type arguments and capture types.

The evaluator bakes scalars, lists, plain records, sealed variants, unions and
insertion-ordered Maps. Sorted Map outputs retain a comparator closure and must
use `.inOrder()` before export; unordered map outputs and iteration are rejected.
Runtime closures, resources, opaque handles and lazy cells cannot be baked.
Internal helper promises and recipe preconditions must check before execution,
independently of enclosing runtime guards; constraints on the resulting value
check afterward. Recipes run only for
native targets, have a ten-second evaluation timeout and a 16 MiB result limit.
The compiler trusts pure `unsafe go` signatures; this is not process isolation.
`bork/build.ReadString(path)` returns String and `ReadBytes(path)` returns
List[Byte]. Both require a direct call with a constant String path and
`uses build` in helpers. A comptime block permits that effect; runtime entrypoints and
predicates do not. Files are relative to the declaring source module root, or
the source directory for a standalone package. Absolute/parent paths, operand
symlinks and special files are rejected; Strings require UTF-8. Established
module-root symlinks are accepted and their identities are tracked. The compiler
captures every read site before execution, including guarded sites, and gives
evaluators frozen bytes. Files are limited to 16 MiB each and 64 MiB together.
A computation is evaluated once per node within one compilation. Its value is
not cached independently across compilations. Compilations that use compile-time
evaluation bypass Session and disk complete-result caches. [Executable reuse](design/executable-reuse.md)
can skip compilation entirely for unchanged inputs, retaining baked values in
the executable. Standalone comptime-value reuse remains a proposal in the
[comptime design](design/comptime.md).

### Writing an interpolation validator

The builder's own package may declare an optional pure
`InterpolationValidator[Builder]` instance. No caller `use` is required:

```bork
instance tagValidator: InterpolationValidator[Builder] {
  fn validateInterpolation(parts: StaticParts, holes: List[InterpolationHole]): List[InterpolationIssue] {
    if (parts.values.get(0).getOr("").contains("forbidden")) {
      [InterpolationIssue { hole: Option.None, message: "forbidden literal text" }]
    } else { [] }
  }
}
```

The compiler supplies literal parts and type metadata, never hole values or a
runtime builder. Each hole lists `InterpolationKind.Builtin { name }`,
`Named { packagePath, name }`, or `Unknown`; unions list all possible kinds.
Return `[]` to accept, `Option.Some(index)` to report at a zero-based
hole, or `Option.None` at the prefix. Messages include the validator's name.
Use Unknown conservatively when the property depends on runtime data.

Validators and their helpers must be pure. Concrete generic instances resolve
all dictionaries in the builder owner's scope; constrained heads and unresolved
runtime dictionaries are rejected. The compiler batches distinct calls per
package, evaluates them with a bounded evaluator, and memoizes identical
parts/kinds within a build. Eligible standard-library validators ship as generated
compiler intrinsics; user-library validators use the ordinary comptime process.
Both paths run fresh validation on each check. Panics, invalid indices and timeouts fail
checking. Runtime hole/factory evaluation remains once-only in source order.
Libraries must keep runtime checks for properties metadata cannot prove; see
[SQL's compile-time and render-time checks](std/sql.md).

## Compiler command settings

These are CLI forms, independent of source syntax:

```text
bork env [-json | --json] [VAR...]
bork env -w VAR=value...
bork env -u VAR...
bork install [path]
```

Settings are `BORKCACHE`, `BORKBIN`, and `BORK_CACHE` (`on` or `off`).
Environment variables override saved values in `os.UserConfigDir()/bork/env.json`,
which override defaults. Directory settings require absolute paths; empty values
fall through. See [the compiler settings reference](cli.md#settings)
for defaults and installation behavior.

Script `bork:require` headers use the same Go module paths and canonical versions
for both Go and Bork libraries. A module-root import may use an explicit valid
Bork alias when its final path component contains punctuation.


Advisory lint suppression uses ordinary comments, not a new grammar production:
`// lint:ignore lint.unused-parameter reason` suppresses that rule on the same or
following line. Comma-separated rule codes and `all` are supported. The compiler's
syntax and safety errors remain errors.

## Editor grammar

The [tree-sitter grammar](../editors/tree-sitter-bork/README.md) describes source
structure for editor highlighting, indentation, folds and Go injection. CI parses
every example and compiler case; deliberately malformed syntax cases form an
explicit, checked recovery allowlist. Compiler syntax and validation remain the
authority. Keyword drift is checked against `internal/syntax/token.go`.
[Native editor packages](editors.md) adapt the shared queries and provide Vim and
Emacs lexical highlighting; CI exercises every example and case in each format.

`bork.mod` may also include one `fast` directive after its module line. It accepts
fast executable reuse with untracked external inputs; it has no arguments. See
[executable reuse](design/executable-reuse.md). This is manifest syntax and does
not add a keyword to `.bork` programs.

In a typed match pattern, an arrow after a top-level tuple type separates the
arm unless a function result type followed by another arrow is present. A function
type can use an extra pair of parentheses to make the boundary explicit, for example `callback: ((Int) => Int) => callback(1)`.
