# tree-sitter-bork

The shared bork highlighting grammar for Neovim, Helix, Zed and Emacs.
It lives in the bork repository; consumers should use the
`editors/tree-sitter-bork` subdirectory. Generated C sources are committed,
so installing the parser requires a C compiler but no JavaScript runtime.
The parser uses tree-sitter ABI 14 for compatibility with older
tree-sitter-enabled editor builds, including Emacs 29.

The grammar covers declarations, records, sealed variants, unions, contracts,
effects, scopes, lambdas, patterns, scripts, leading-dot chains, compile-time
blocks, and typed interpolation. Queries provide highlights, lexical locals,
indentation, folds, and Go injection inside `unsafe go { ... }` bodies. Install
the Go parser to highlight injected bodies. Go content remains opaque to the
bork parser; the external scanner balances braces outside Go comments and
literals. `unsafe go "package.Function"` is highlighted as a string.

SQL injection is deliberately deferred: interpolation holes are bork expressions,
and removing them can change SQL token boundaries. Typed SQL prefixes and their
holes receive highlighting without pretending the entire body is plain SQL.

These are editor queries, not a compiler API. The compiler owns validation,
types, facts, diagnostics and formatting. Configure your editor's LSP client
with the command `bork lsp` over stdio for those features. Tree-sitter accepts
some structurally meaningful incomplete/invalid forms to support editing;
`bork check` remains the source of truth for language validity.

## Develop

```sh
npm ci --prefix editors/tree-sitter-bork
npm run generate --prefix editors/tree-sitter-bork
npm test --prefix editors/tree-sitter-bork
node scripts/check-editor-keywords.cjs
```

Generation uses the pinned tree-sitter CLI; commit generated `src/` changes.
Tests assert representative syntax trees, compile all five query files, and
parse every `.bork` file in `examples/` and `testdata/cases/` recursively.
A small explicit allowlist in `test/repository.cjs` contains intentionally
broken parser fixtures; each must still exist and produce ERROR or MISSING
nodes. All other files must parse cleanly. CI checks generated-source drift
and that every keyword from `internal/syntax/token.go` appears in each editor
grammar. Add new grammars to `scripts/check-editor-keywords.cjs`.

The parser and queries use the repository's [MIT license](../../LICENSE).
Tree-sitter's grammar DSL and scanner APIs are described in the
[official parser guide](https://tree-sitter.github.io/tree-sitter/creating-parsers/).
