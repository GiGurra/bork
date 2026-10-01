# bork grammar

> The grammar of what the compiler accepts today (the first two slices of milestone M0: functions and expressions, then data types). It grows with each milestone.

## Lexical structure

- **Source files** are UTF-8, with the `.bork` extension. A directory of `.bork` files is one package.
- **Comments:** `// to end of line` and `/* block */`. They are ignored by the parser, but kept by the lexer for future tooling.
- **Identifiers:** a letter followed by letters, digits, or `_`. Identifiers cannot start with `_`, which is reserved for the compiler.
- **Keywords:** `fn`, `type`, `sealed`, `match`, `if`, `else`, `return`, `true`, `false`.
- **`_`** on its own is the wildcard pattern.
- **Integer literals:** decimal digits, with `_` allowed between digits (`10_000`). They must fit in a 64-bit `Int`.
- **String literals:** double-quoted, with Go's escape sequences (`\n`, `\t`, `\"`, `\\`, ...).
- **Statement endings:** a newline ends a statement when the line's last token is an identifier, a literal, `true`/`false`, `return`, `_`, `)`, `]`, `}`, or `?`, as in Go. A `;` can also separate statements on one line. Newlines inside parentheses are ignored, so argument and parameter lists can span lines.

## Syntax

```ebnf
Package    = { File } .
File       = { ( FuncDecl | TypeDecl ) EOL } .

TypeDecl   = "type" Ident "=" ( Fields | Sealed | Type ) .
Fields     = "{" [ Field { Sep Field } [ Sep ] ] "}" .
Field      = Ident ":" Type .
Sealed     = "sealed" "{" [ Variant { Sep Variant } [ Sep ] ] "}" .
Variant    = Ident [ Fields ] .
Sep        = "," | newline .                 (* commas or one item per line *)

FuncDecl   = "fn" Ident "(" [ Params ] ")" [ ":" Type ] Block .
Params     = Param { "," Param } [ "," ] .
Param      = Ident ":" Type .
Type       = TypeAtom { "|" TypeAtom } .     (* a union: Int | NotFound *)
TypeAtom   = Ident [ "[" Type { "," Type } "]" ] | "(" Type ")" .

Block      = "{" { Stmt EOL } [ Expr ] "}" .
Stmt       = Binding | Expr .
Binding    = Ident "=" Expr .

Expr       = OrExpr .
OrExpr     = AndExpr { "||" AndExpr } .
AndExpr    = CmpExpr { "&&" CmpExpr } .
CmpExpr    = AddExpr { ( "==" | "!=" | "<" | "<=" | ">" | ">=" ) AddExpr } .
AddExpr    = MulExpr { ( "+" | "-" ) MulExpr } .
MulExpr    = Unary { ( "*" | "/" | "%" ) Unary } .
Unary      = ( "-" | "!" ) Unary | Postfix .
Postfix    = Primary { "(" [ Args ] ")"
                     | "." Ident
                     | ".copy" "(" Update { Sep Update } [ Sep ] ")"
                     | "?"
                     | RecordLit } .
RecordLit  = "{" [ FieldInit { Sep FieldInit } [ Sep ] ] "}" .  (* after User or Shape.Circle *)
FieldInit  = Ident ":" Expr .
Update     = Ident { "." Ident } "=" Expr .  (* u.copy(address.city = "Oslo") *)
Args       = Expr { "," Expr } [ "," ] .

Primary    = IntLit | StringLit | "true" | "false" | Ident
           | "(" Expr ")" | Block | If | Match | Return .
If         = "if" "(" Expr ")" Block [ "else" ( If | Block ) ] .
Return     = "return" [ Expr ] .
Match      = "match" "(" Expr ")" "{" [ Arm { Sep Arm } [ Sep ] ] "}" .
Arm        = Pattern "=>" Expr .
Pattern    = "_"                             (* anything *)
           | Literal                         (* 1, -1, "a", true *)
           | Ident ":" Type                  (* n: Int, e: NotFound | DbError *)
           | Ident [ "." Ident ] [ "{" FieldPat { Sep FieldPat } [ Sep ] "}" ] .
FieldPat   = Ident [ ":" Ident ] .           (* radius, or radius: r *)

EOL        = newline | ";" .
```

## Semantics in brief

- **Everything is an expression.** A block's value is its last expression. A block that ends with a statement has type `Unit`.
- **`if` with `else`** produces a value; both branches must have the same type. **`if` without `else`** is only run for its effect.
- **`return`** has type `Never`, which fits wherever any type is expected, so `x = if (c) { return 0 } else { 1 }` works. Code after a `return` is a compile error.
- **Bindings are immutable**, and names cannot be shadowed.
- **A value that is computed but never used is a compile error** (e.g. calling a function that returns `Int` as a statement).
- **Operators:** `+ - * / %` on `Int` (64-bit, wrapping like Go); `+` also concatenates `String`s; `< <= > >=` on `Int` or `String`; `== !=` on two values of the same type; `&& || !` on `Bool`, with short-circuiting.
- **`println(args...)`** is built in. It prints its arguments separated by spaces, followed by a newline.
- **Records** (`type User = { name: String, age: Int }`) are built with all their fields named: `User { name: "Ada", age: 36 }`. Fields are read with `u.name`. A record cannot contain itself directly.
- **`copy`** makes a changed copy: `u.copy(age = 37, address.city = "Oslo")`. Paths reach into nested records; two updates may not overlap (`address` and `address.city`).
- **Sealed types** (`type Shape = sealed { Circle { radius: Int }, Empty }`) list all their variants. Variants are always qualified: `Shape.Circle { radius: 1 }`, `Shape.Empty`.
- **Unions** (`Int | NotFound | DbError`) hold a value of any one of their types. A value of a member type, or of a smaller union, can be used where the union is expected. `type Lookup = Int | NotFound` names a union.
- **`Option[T]`** is built in: `sealed { Some { value: T }, None }`. `Option.None` takes its type from where it is used.
- **`match (x) { ... }`** tries arms in order and must be exhaustive: every variant, union member, or `true`/`false` must be handled, or there must be a `_` or a pattern covering the whole type. An arm that can never match is an error. Arms produce a value, like `if`.
- **`x?`** on a union keeps the leftmost member and returns every other member from the function, which must be able to return them. On an `Option`, it keeps the `Some` value and returns `Option.None`.
- **Equality** is structural: records, variants, and Options compare by their fields.
- **Printing** shows values in bork syntax: `User { name: "Ada", age: 36 }`, `Shape.Empty`.
- **A program** is a package with `fn main()`, which takes no parameters and returns no value.
