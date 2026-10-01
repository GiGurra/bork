# bork grammar

> The grammar of what the compiler accepts today (the first slice of milestone M0). It grows with each milestone.

## Lexical structure

- **Source files** are UTF-8, with the `.bork` extension. A directory of `.bork` files is one package.
- **Comments:** `// to end of line` and `/* block */`. They are ignored by the parser, but kept by the lexer for future tooling.
- **Identifiers:** a letter followed by letters, digits, or `_`. Identifiers cannot start with `_`, which is reserved for the compiler.
- **Keywords:** `fn`, `if`, `else`, `return`, `true`, `false`.
- **Integer literals:** decimal digits, with `_` allowed between digits (`10_000`). They must fit in a 64-bit `Int`.
- **String literals:** double-quoted, with Go's escape sequences (`\n`, `\t`, `\"`, `\\`, ...).
- **Statement endings:** a newline ends a statement when the line's last token is an identifier, a literal, `true`/`false`, `return`, `)`, or `}`, as in Go. A `;` can also separate statements on one line. Newlines inside parentheses are ignored, so argument and parameter lists can span lines.

## Syntax

```ebnf
Package    = { File } .
File       = { FuncDecl EOL } .

FuncDecl   = "fn" Ident "(" [ Params ] ")" [ ":" Type ] Block .
Params     = Param { "," Param } [ "," ] .
Param      = Ident ":" Type .
Type       = Ident .                         (* Int, Bool, String, Unit *)

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
Postfix    = Primary { "(" [ Args ] ")" } .
Args       = Expr { "," Expr } [ "," ] .

Primary    = IntLit | StringLit | "true" | "false" | Ident
           | "(" Expr ")" | Block | If | Return .
If         = "if" "(" Expr ")" Block [ "else" ( If | Block ) ] .
Return     = "return" [ Expr ] .

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
- **A program** is a package with `fn main()`, which takes no parameters and returns no value.
