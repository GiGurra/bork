# Built-ins

Built-in types, functions and methods are available in every package without
an import. The API reference below is generated from the checked prelude and
compiler-provided signatures whenever this site is built.

Get the same reference from your installed compiler:

```sh
bork doc builtin
bork doc builtin --html > builtins.html
```

The language chapters introduce [values and printing](../language/basics.md),
[types](../language/types.md), [collections](../language/collections.md),
[scopes](../language/scopes.md) and [channels](../language/channels.md).
[Standard packages](README.md) provide APIs that require imports.

## Reading signatures

- `uses` lists effects; `where` preserves facts and requirements.
- `name: Type = default` shows an optional parameter and its default.
- `value` in compiler-provided signatures accepts any value type;
  `values...` accepts zero or more independently typed arguments.
- `[message: String]` is an optional compiler-provided parameter.
- Conversion result types and assembly effects depend on the supplied inputs;
  each function's comment explains the rule.
- `private { ... }` hides implementation fields of built-in records.
- `unsafe go` identifies a trusted implementation boundary. Calling a built-in
  requires no project unsafe declaration; its effects and resource contracts
  still apply.

```bork
fn main() {
  values = [1, 2, 3]
  println(values.map(n => n * 2))
  eprintln("first:", values.get(0))
}
```

<!-- builtin-api -->
