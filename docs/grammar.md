# bork grammar

> The grammar of what the compiler accepts today (milestone M0 so far: functions and expressions, data types, numbers, and `unsafe go`). It grows with each milestone.

## Lexical structure

- **Source files** are UTF-8, with the `.bork` extension. A directory of `.bork` files is one package. Diagnostic positions use one-based lines and byte columns (see [JSON diagnostics](diagnostics.md)).
- **Comments:** `// to end of line` and `/* block */`. They are ignored by the parser, but kept by the lexer and formatter.
- **Identifiers:** a letter followed by letters, digits, or `_`. Identifiers cannot start with `_`, which is reserved for the compiler.
- **Keywords:** `fn`, `pred`, `type`, `sealed`, `match`, `if`, `else`, `return`, `true`, `false`, `unsafe`, `where`, `and`, `or`, `trust`, `rule`. `import`, `use`, `class`, `instance`, `test`, `instances`, `scope`, `with`, `resource`, `derive`, `uses`, and `nothing` are keywords only where they start a declaration, a scope block (or its policy), a resource type, a derive list, or a list of effects, and can otherwise be used as names.
- **`_`** on its own is the wildcard pattern.
- **Integer literals:** decimal (`10_000`), hex (`0xFF`), binary (`0b1010`), or octal (`0o17`), with `_` allowed between digits, as in Go.
- **Float literals:** `1.5`, `2e10`, `1.5e-3`. A `.` must be followed by a digit (so `5.copy(...)` is a selector).
- **Rune literals:** one Unicode code point in single quotes, with Go's escapes: `'a'`, `'\n'`, `'\u00e5'`.
- **Interpolated strings:** `s"Hello, $name! Next year: ${age + 1}"`. `$name` inserts a name and `${...}` any expression (which may contain string literals). `$$` is a dollar sign. Plain strings never interpolate.
- **String literals:** double-quoted, with Go's escape sequences (`\n`, `\t`, `\"`, `\\`, ...).
- **`unsafe go { ... }`:** after `unsafe go`, everything up to the matching `}` is raw Go, not bork tokens (braces inside Go strings, runes, and comments do not count).
- **Statement endings:** a newline ends a statement when the line's last token is an identifier, a literal, `true`/`false`, `return`, `_`, `)`, `]`, `}`, or `?`, as in Go. A `;` can also separate statements on one line. Newlines inside parentheses are ignored, so argument and parameter lists can span lines. A line starting with `|>` continues the previous one.

## Syntax

```ebnf
Package    = { File } .
File       = { Import EOL } { Use EOL } { ( FuncDecl | PredDecl | TypeDecl | RuleDecl | TestDecl | ClassDecl | InstanceDecl | Instances ) EOL } .
Use        = "use" UseItem .                (* use money.ShowAmount, use money.*, use api.Json *)
UseItem    = Ident | Ident "." ( Ident | "*" ) .
Instances  = "instances" Ident "{" [ UseItem { Sep UseItem } [ Sep ] ] "}" .
                                             (* instances Json { ItemDecode, ItemEncode, money.Defaults } *)
ClassDecl  = "class" Ident "[" Ident "]" "{" { MethodSig EOL } "}" .  (* class Show[T] { fn show(x: T): String } *)
MethodSig  = "fn" Ident "(" [ Params ] ")" [ Uses ] [ ":" Type ] .
InstanceDecl = "instance" Ident [ TypeParams ] ":" Ident "[" Type "]" "{" { FuncDecl EOL } "}" .
                                             (* instance showList[T: Show]: Show[List[T]] { fn show(xs: List[T]): String { ... } } *)
Import     = "import" [ Ident ] StringLit .  (* import "example.com/shop/money", or import cash "..." *)
QualIdent  = Ident "." Ident .               (* money.Cents, money.Amount: a name of an imported package *)
TestDecl   = "test" StringLit [ "(" Params ")" ] Block .  (* test "adds numbers" { ... }; parameters (no defaults) make a property test *)
PredDecl   = "pred" Ident "(" Params ")" Block .  (* always returns Bool *)
RuleDecl   = "rule" Ident "(" Params ")" "{" Premises "=>" Conclusions "}" .
Premises   = Expr { "and" Expr } .  (* predicate calls on the variables, and conditions *)
Conclusions = Call { "and" Call } .

TypeDecl   = "type" Ident [ TypeParams ] "=" ( Fields | Sealed | "resource" | Type ) [ Derive ] .
                                             (* type Pair[A, B] = { ... }; type File = resource: values made by unsafe go *)
Derive     = "derive" "(" Ident { "," Ident } ")" .  (* derive (Decode, Encode): instances written by the compiler *)
Fields     = "{" [ Field { Sep Field } [ Sep ] ] "}" .
Field      = Ident ":" Type .
Sealed     = "sealed" "{" [ Variant { Sep Variant } [ Sep ] ] "}" .
Variant    = Ident [ Fields ] .
Sep        = "," | newline .                 (* commas or one item per line *)

FuncDecl   = "fn" [ Receiver ] Ident [ TypeParams ] "(" [ Params ] ")" [ Uses ] [ ":" Type ] ( Block | GoBody ) .
Uses       = "uses" ( "nothing" | Ident { "+" Ident } ) .  (* uses io + net: the effects io, net, clock, random, state *)
Receiver   = "(" Ident ":" Type ")" .   (* a method: fn (xs: List[T]) second[T](): Option[T] { ... } *)
TypeParams = "[" TypeParam { "," TypeParam } "]" .   (* fn map[A, B](...) *)
TypeParam  = Ident [ ":" Ident { "+" Ident } ] .     (* T: Show + Eq: T needs instances of Show and Eq *)
GoBody     = "unsafe" "go" "{" { GoImport } GoStatements "}" .
GoImport   = "import" StringLit newline .     (* import "strings" *)
Params     = Param { "," Param } [ "," ] .
Param      = Ident ":" Type [ "=" Expr ] .   (* a default: a literal; only on the last parameters *)
Type       = Constrained { "|" Constrained } .  (* a union: Int | NotFound *)
Constrained = TypeAtom [ "where" Clause { "and" Clause } ] .
Clause     = PredRef { "or" PredRef }          (* alone: p or q *)
           | "(" PredRef { "or" PredRef } ")" .  (* with and: (p or q) and r *)
PredRef    = Ident [ "(" Expr { "," Expr } ")" ] .  (* positive, between(1, 65535), atLeast(lo) *)
TypeAtom   = Ident [ "[" Type { "," Type } "]" ] | "(" Type ")" | FuncType .
FuncType   = "(" [ Type { "," Type } ] ")" [ Uses ] "=>" Type .  (* (Int, String) => Bool, (String) uses io => Unit *)

Block      = "{" { Stmt EOL } [ Expr ] "}" .
Stmt       = Binding | Trust | Expr .
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
                     | "." Ident
                     | ".copy" "(" Update { Sep Update } [ Sep ] ")"
                     | "?"
                     | RecordLit } .
RecordLit  = "{" [ FieldInit { Sep FieldInit } [ Sep ] ] "}" .  (* after User or Shape.Circle *)
FieldInit  = Ident ":" Expr .
Update     = Ident { "." Ident } "=" Expr .  (* u.copy(address.city = "Oslo") *)
Args       = Expr { "," Expr } [ "," ] .

Primary    = IntLit | FloatLit | RuneLit | StringLit | InterpString | "true" | "false" | Ident
           | "(" Expr ")" | Block | If | Match | Return | Lambda | ListLit | MapLit | ScopeExpr .
ScopeExpr  = "scope" Ident [ "with" Expr { "," Expr } ] Block .  (* scope s { f = openFile(path, s)? ... }; scope s with taskTimeout(100), cleanupTimeout(500) { ... } *)
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

## Semantics in brief

- **Everything is an expression.** A block's value is its last expression. A block that ends with a statement has type `Unit`.
- **`if` with `else`** produces a value; both branches must have the same type. **`if` without `else`** is only run for its effect.
- **`return`** has type `Never`, which fits wherever any type is expected, so `x = if (c) { return 0 } else { 1 }` works. Code after a `return` is a compile error.
- **Bindings are immutable**, and names cannot be shadowed, except prelude functions: a local may be called `count`, and a package's own `fn find` replaces the prelude's (for the package; the prelude keeps using its own).
- **A value that is computed but never used is a compile error** (e.g. calling a function that returns `Int` as a statement).
- **Numbers:** `Int8`, `Int16`, `Int32`, `Int` (= `Int64`), `Uint8` (= `Byte`), `Int32` (= `Rune`), `Uint16`, `Uint32`, `Uint64`, `Float32`, `Float` (= `Float64`). Integers wrap on overflow, like Go.
- **Operators:** `+ - * /` on two numbers of the same type, `%` on two integers of the same type; `+` also concatenates `String`s; `< <= > >=` on numbers or `String`s; `== !=` on two values of the same type, or a union and a value of one of its members; unary `-` on signed numbers; `&& || !` on `Bool`, with short-circuiting. Types never mix implicitly. Dividing by a constant zero is a compile error.
- **Constants:** number and rune literals, and `+ - * / %` on them, are computed exactly at compile time (`0.1 + 0.2` is exactly `0.3`). A constant takes its type from where it is used (`x: Uint8 = 255`, `small + 1`); otherwise it is a `Float` if it contains a float literal, a `Rune` if it contains a rune literal, and an `Int` otherwise. It is computed as its type computes: `7 / 2` is `3` as an `Int`, and `x: Float = 1 / 3` is `0.333...`. It must fit its type.
- **Conversions:** `toInt8(x)`, `toInt16`, `toInt32`, `toInt` (`toInt64`), `toUint8` (`toByte`), `toUint16`, `toUint32`, `toUint64`, `toFloat32`, `toFloat` (`toFloat64`), from any number type. If every value of x's type fits, the result is the target type; otherwise it is `Target | OutOfRange` (float to integer drops the fraction, and NaN or infinities never fit). A constant argument is converted at compile time and must fit.
- **Interpolation** renders each value as `toString` does, so any value can go in a string: `s"user: $u"`. There is no printf-style formatting.
- **`panic(message)`** stops the program with a message. It is for bugs, not expected failures (those are union results). Its type is `Never`, so it can end any branch.
- **`println(args...)`** prints its arguments separated by spaces, followed by a newline. **`toString(x)`** renders any value the way `println` prints it. Floats always print as floats: `3.0`, `0.25`, `1e+21`.
- **The prelude** ([prelude.bork](../internal/prelude/prelude.bork)) is available everywhere: the records `OutOfRange` and `ParseError`; `parseInt`, `parseFloat`, `parseBool` (returning `T | ParseError`); and `byteLength`, `contains`, `startsWith`, `endsWith`, `indexOf` (an `Option[Int]`), `toUpper`, `toLower`, `trim`, `replaceAll`, `repeat`, which count bytes; and `runeCount`, `runeAt` (an `Option[Rune]`), `substring` (a `String | OutOfRange`), `runeToString`, `isDigit`, `isLetter`, `isSpace`, `isUpper`, `isLower`, which count runes. A `Rune` prints as its number; `runeToString` gives the character. For lists: `length`, `isEmpty`, the predicate `notEmpty`, `first` (for a list known to be `notEmpty`), `get`, `head`, `last` (each an `Option[T]` where it may be missing), `map`, `flatMap`, `filter`, `fold`, `find`, `any`, `all`, `count`, `forEach`, `take`, `drop`, `reverse`, `concat`, `append`, `prepend`, `sortWith`, `range`, and `join`/`split` for Strings. Prelude types and the compiler's own functions (`println`, `toString`, `panic`, `toInt8`, ...) cannot be redefined. Concurrency: `spawn(s, () => value)` gives a `Task[T]`, `await(task)` its result, and `launch(s, () => ...)` runs work without a result; scope `s` waits for its tasks before it closes. Scope policies (`ScopePolicy`): `taskTimeout(ms)`, `cleanupTimeout(ms)`, `logFailures()`. `sleep(ms)` pauses. Cancellation: `cancel(s)`, `cancelAfter(s, ms)`, `cancelled(s)`, and the cancellation points `delay(s, ms)` and `checkpoint(s)` (`Unit | Cancelled`). Resources: `attach(r, s)` keeps `r` open until `s` closes too. Channels: `channel[T](s, capacity)`, `send`, `receive`, `closeChannel`, `received`. Shared state: `atom(x)` makes an `Atom[T]`, `current(a)` reads it, and `update(a, f)` (giving the new value) or `swap(a, f)` (giving the old one) replaces its value atomically, retrying f if another task got there first.
- **Effects** are declared after the parameters, before the result: `fn save(path: String, text: String) uses io: Unit | IoError`, and in function types, before the `=>`: `(String) uses io => Unit`. The effects are `io`, `net`, `clock`, `random`, and `state` (see [requirements.md](requirements.md#effects-in-signatures-proposal)). They are parsed and named in types, but not checked yet. A `pred` cannot declare effects.
- **`unsafe go` bodies** are Go statements implementing the function. Parameters are visible under their own names, and values have the Go representations listed at the top of the prelude. Standard packages use the stable [Go helper API](std-go.md) for maps, options, and scope contexts. Imports go on the first lines (`import "strings"`). bork trusts the function's signature and does not check the body; the Go compiler does, and reports errors at the bork positions.
- **Records** (`type User = { name: String, age: Int }`) are built with all their fields named: `User { name: "Ada", age: 36 }`. Fields are read with `u.name`. A record cannot contain itself directly.
- **`copy`** makes a changed copy: `u.copy(age = 37, address.city = "Oslo")`. Paths reach into nested records; two updates may not overlap (`address` and `address.city`).
- **Sealed types** (`type Shape = sealed { Circle { radius: Int }, Empty }`) list all their variants. Variants are always qualified: `Shape.Circle { radius: 1 }`, `Shape.Empty`.
- **Unions** (`Int | NotFound | DbError`) hold a value of any one of their types. A value of a member type, or of a smaller union, can be used where the union is expected. `type Lookup = Int | NotFound` names a union. `Unit` can be a member: `fn save(x: Item): Unit | DbError` either does its work or fails, and a body that ends without a value (or with a call that returns nothing) produces the `Unit`. With `?`, `save(x)?` is a statement that returns the error.
- **`List[T]`** is built in and immutable: `[1, 2, 3]`, or `[]` where the type is known (`xs: List[Int] = []`). Lists print as `[1, 2, 3]`.
- **Functions are values.** `(Int) => String` is a function type; a lambda is `x => x + 1`, `(a, b) => a + b`, or `(x: Int) => ...` where nothing says the parameter's type. Named functions can be passed (`map(xs, double)`) unless they have `where` requirements. Lambdas see the values around them; `return` and `?` are not allowed in them.
- **Generic functions** take type parameters: `fn first[T](xs: List[T]): Option[T]`. Calls infer them from the arguments (lambdas, `[]` and `Option.None` last, so `fold(xs, 0, (sum, x) => sum + x)` works) or from the expected result. Inside, a type parameter can be passed around, stored, printed, and matched with a type pattern (`v: T`), but not compared with `==`. Lists, functions, and records holding them have no `==` either.
- **Maps:** `Map[K, V]` is built in, written `{key: value, ...}` (`{:}` when empty); keys are expressions (`{"user": name}`), of any type with `==`. Maps have methods too: `m.get(k)`, `m.put(k, v).keys()`. A `{` where an expression starts is a map when its first line is `key: value` followed by `,`, `}`, or a newline, and otherwise a block (`{}` is an empty block). Maps never change and are persistent: putting or removing a key copies only the path to it (a hash trie, log32 of the size) and shares the rest, so old versions are cheap to keep. Two maps are equal when they have the same entries, in any order. List and map keys use structural equality, independently of their text. A map keeps its keys in the order first added; `maps.Sorted(m)` (keys with `Ord`) or `maps.SortedBy(m, less)` gives one that keeps them sorted (a balanced tree), `maps.Unordered(m)` a hash map that keeps no order (smaller and quicker to change, traversed in an order that differs between runs; printing sorts numeric and string keys by value, and other keys by their text, with mixed kinds grouped), and `maps.InOrder` goes back. Either is a `Map`, and operations keep the order of the map they start from. `bork/maps` has `Get`, `GetOr`, `Has`, `Put`, `Remove`, `Size`, `IsEmpty`, `Keys`, `Values`, `Entries` (a list of the prelude's `Entry[K, V]`), `FromEntries`, `Merge`, `MapValues`, `Filter`, `ForEach`, `Sorted`, `SortedBy`, `Unordered`, and `InOrder`. A `Map[String, V]` is a JSON object for `Decode` and `Encode`.
- **`Ord`:** the prelude class `Ord[T]` (`compare(a, b)`: negative, 0, or positive) has instances for the number types and `String`.
- **Default parameter values:** `fn greet(name: String, greeting: String = "Hello")` can be called as `greet("ada")`. Defaults go only on the last parameters, and are closed values: literals (numbers, strings, runes, Bools, and list and map literals of them), which take their type from each call (`xs: List[T] = []`), or record and variant values of closed values (`level: Level = Level.Info`, `retry: Retry = Retry { times: 3 }`), whose names mean what they mean where the function is declared, so they work from other packages. A call that leaves parameters out gets their defaults as arguments, and facts are checked as for written ones.
- **Methods** chain left to right: `users.filter(u => u.age >= 18).map(u => u.name).join(", ")`, `m.get("a").getOr(0)`, `s.trim().split(",")`. A method is declared with a receiver, as in Go: `fn (p: Point) plus(q: Point): Point { ... }`; generic receivers name their type parameters among the method's (`fn (xs: List[T]) second[T](): Option[T]`). The receiver is the method's first parameter, so facts and lifetimes apply as for any argument (`xs.first()` needs `xs` to be known `notEmpty`). Type arguments can be given as for functions; those the receiver decides are left out: `xs.map[String](f)` gives `map[A, B]` its `B` (giving all of them, `xs.map[Int, String](f)`, works too). Methods can be declared on `List`, `Map`, the basic types, and declared types, by any package; each type has its own namespace of methods, apart from functions, so `List`'s `get` and `Map`'s `get` coexist. `x.m(a)` finds `m` among the methods of `x`'s type that the code sees: its own package's first, then the exported ones of the type's package and of the imports, then the prelude's; a record field holding a function is called as before (`r.f()`). The prelude's methods: on `List`, those of the list functions (`length`, `isEmpty`, `first`, `get`, `head`, `last`, `map`, `flatMap`, `filter`, `fold`, `find`, `any`, `all`, `count`, `forEach`, `take`, `drop`, `reverse`, `concat`, `append`, `sortWith`, `includes`, `distinct`, `join`) and `sorted`, `sortBy`, `groupBy` (a `Map[K, List[T]]`), and `toMap` (of a `List[Entry[K, V]]`); on `String`, those of the string functions (`byteLength`, `runeCount`, `contains`, `startsWith`, `endsWith`, `indexOf`, `toUpper`, `toLower`, `trim`, `replaceAll`, `repeat`, `runeAt`, `substring`, `split`, `fields`, `lines`); on `Option`, `map`, `flatMap`, `getOr`, `isSome`, `isNone`; on `Map`, everything in `bork/maps` (`get`, `getOr`, `has`, `put`, `remove`, `size`, `isEmpty`, `keys`, `values`, `entries`, `merge`, `mapValues`, `filter`, `forEach`, `sorted`, `sortedBy`, `inOrder`, `unordered`).
- **`|>`** passes a value as the first argument of a function: `users |> filter(u => u.age >= 18) |> map(u => u.name)`. It chains free functions the way methods chain methods.
- **Tests:** `test "name" { ... }` declares a test, run by `bork test` and left out of programs. `assert(cond)` and `assertEqual(actual, expected)` fail a test with the position (and both values); so does a `panic`. `assertSnapshot(x)` compares `toString(x)` with a snapshot file in the `snapshots` directory next to the package's sources (for `bork test file.bork`, next to the file), and fails with a line diff if it differs, or with the value if it is missing; `bork test --update` writes the missing and different ones instead. A test's snapshots are named after it: `test "renders an invoice"` uses `renders_an_invoice.snap`, then `renders_an_invoice.2.snap`, in the order its `assertSnapshot` calls run, so they must run in a fixed order (not from concurrent tasks). Rules and property tests cannot snapshot. Tests run in test mode: facts the compiler takes on trust are checked as they run, so a `trust positive(x)` that does not hold, or an `unsafe go` function that breaks its promise, fails the test with the value. (`test` is not a keyword.)
- **Property tests:** a test with parameters, `test "transfer scales" (amount: Int where positive) { ... }`, runs its body on 100 cases of generated values (`bork test --cases N` changes that), which meet the parameters' where clauses, including those on list elements and record fields; a clause may name another parameter (`hi: Int where atLeast(lo)`), which is then generated first. Values are drawn from edge cases (0, ±1, the type's limits, -0.0, infinities, NaN, "", ...), the constants the clauses and their predicates mention and their neighbours, and random values that grow from case to case. A case fails like a test does; it is then shrunk to a simpler one that still fails (shrunk values keep their facts), and reported with its values and its seed. The seed comes from the test's name, so runs repeat; `bork test --seed N` runs every property with seed N (the printed command includes `--cases` when it matters: values grow with the number of cases). If a where clause rejects most values (100 draws in a row, in most cases), the test fails with which one. What a property prints is not shown. Parameters of types no values are generated for (functions, scopes, resources) skip the test. `bork test --auto-properties` also property-tests every function of the package whose promises are trusted: `unsafe go` functions that promise facts about their result (or return records with field facts), and functions whose body uses `trust`; each is called on generated arguments, and fails if it breaks a promise or panics (or is skipped if its arguments' facts reject most values). It is opt-in for now, because random arguments could make such a function do IO; once effects say which functions are pure, those will be tested by default.
- **Generic types:** records and sealed types can take type parameters: `type Pair[A, B] = { first: A, second: B }`, `type Tree[T] = sealed { Leaf, Node { left: Tree[T], value: T, right: Tree[T] } }`. Literals take their type arguments from the expected type or from their fields (`Pair { first: 1, second: "one" }` is a `Pair[Int, String]`); a variant without fields (`Tree.Leaf`) needs an expected type. A `where` in a type argument applies to the fields declared with that parameter: in `Pair[String, Int where positive]`, to `second`. Type aliases cannot have parameters yet.
- **`Option[T]`** is declared in the prelude as `type Option[T] = sealed { Some { value: T }, None }`, an ordinary generic sealed type. `Option.None` takes its type from where it is used.
- **`match (x) { ... }`** tries arms in order. Arms produce a value, like `if`.
  - **List patterns:** `[]` matches the empty list, `[a, b]` a list of exactly two elements, and `[first, ...rest]` one of at least one, binding the remaining elements to `rest` (`[first, ...]` ignores them). Exhaustiveness covers them: `[]` and `[_, ...]` cover every list.
  - **Patterns nest:** a field can be matched against any pattern, as in `Option.Some { value: ')' }` or `Shape.Circle { center: Point { x: 0, y: 0 } }`. `{ radius }` binds the field to its own name, and `{ radius: r }` binds it to `r`.
  - **A bare name** binds the whole value (`n => n * 2`), unless it is a type, in which case it matches values of that type (`NotFound => ...`).
  - **Matches must be exhaustive**, also inside nested patterns: the error lists what is missing (`missing Option.Some { value: false }`), with `_` for a field that has values no arm covers. An arm that can never match is an error.
- **`x?`** on a union keeps the leftmost member and returns every other member from the function, which must be able to return them. On an `Option`, it keeps the `Some` value and returns `Option.None`.
- **Equality** is structural: records, variants, Options, and lists compare by their parts. Functions, scopes, and resources have no `==`. The prelude class `Eq` is built in: every type with `==` has it, and a bound `[T: Eq]` lets generic code compare values of `T` (the prelude's `includes` and `distinct` use it). `Eq` instances cannot be declared, and need no `derive`; nor does `Show`, since `toString` shows every value.
- **Printing** shows values in bork syntax: `User { name: "Ada", age: 36 }`, `Shape.Empty`.
- **Facts.** `x: Int where positive` requires every caller to show that `positive(x)` holds: by a guard (`if (positive(a)) { transfer(a) }`, or `if (!positive(a)) { return ... }` before the call), by declaring the same requirement on its own parameter, by a callee that promises it (`fn validate(raw: Int): Int where positive | NotPositive`), or by `trust positive(x)`. On a constant, the predicate is run at compile time. Promised results are checked on every path. Facts are erased in the generated Go. See [requirements.md](requirements.md#3-contracts-and-knowledge-in-progress).
- **A program** is a package with `fn main()`, which takes no parameters and returns no value.

`bork/time` exposes `Instant`, `Duration`, and `Clock` records, plus checked arithmetic, parsing/formatting, and scope-aware `Sleep`. `bork/env.Load[T: Decode](prefix)` and `LoadWith` decode environment configuration from derived record fields; `LoadJson` decodes one JSON variable. These packages introduce no new syntax. See [examples/time_env](../examples/time_env/main.bork) and the [Go schema helpers](std-go.md).

- **Binary data:** `Bytes` is a built-in immutable byte sequence, distinct from
  `List[Byte]`. There is no special literal: `bytes([toByte(0), toByte(255)])`
  copies a byte list, and `utf8Bytes("hello")` gives a string's UTF-8 bytes.
  `utf8String(data)` returns `String | ParseError`, rejecting invalid UTF-8.
  Methods: `length`, `isEmpty`, `get(index)` (`Option[Byte]`), `toList`,
  `slice(from, to)` (exclusive end, `Bytes | OutOfRange`), and `concat(other)`.
  Equality compares contents; empty sequences are equal. Bytes prints as
  `Bytes(00ff)`, lower-case hex, including in records, lists, and maps.
- **`bork/encoding`:** `Hex` / `ParseHex`, `Base64` / `ParseBase64`, and
  `Base64URL` / `ParseBase64URL` convert Bytes and String. Parsers return
  `Bytes | ParseError`; hex accepts either case and requires complete pairs.
  Both base64 forms use padding and reject nonzero trailing padding bits;
  parsers accept CR/LF as Go's base64 decoder does. No partial bytes are
  returned after an error. See [examples/bytes_encoding](../examples/bytes_encoding/main.bork).

Standard packages may ship `go-deps.mod` and `go-deps.sum` files using Go module syntax for pinned dependencies. This is internal compiler data, with no new bork syntax; user-package Go dependency declarations remain future work. See [std Go dependencies](std-go.md).
