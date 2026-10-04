# Changing the syntax

Several components know bork's syntax. When you add, remove, or change syntax (a keyword, an operator, a literal form, a declaration, a statement rule), go through this list so nothing is left behind.

Everything about *meaning* comes from the compiler packages. The language server, the playground, and the editor plugins never re-implement parsing, types, facts, or formatting. Only the highlighting grammars are separate descriptions of the syntax, because editors require their own formats.

## 1. The compiler

| Component | Where | What to update |
| --- | --- | --- |
| Tokens and keywords | `internal/syntax/token.go`, `internal/syntax/lexer.go` | New token kinds, the `keywords` map, literal forms |
| Parser and syntax tree | `internal/syntax/parser.go` and the AST files in `internal/syntax/` | Parsing, error recovery, positions |
| Checker | `internal/check/` | Rules, types, facts, effects, diagnostics |
| Code generation | `internal/gen/` | Emitted Go and debug source mapping in `debug.go`; new runtime statements must carry source positions |
| Formatter | `internal/format/` | Layout and indentation; `bork fmt` output must stay idempotent |
| Describe | `internal/describe/` | `bork describe` output for the new construct |
| Prelude and standard library | `internal/prelude/`, `internal/std/` | Any bork sources that should use or exercise the new syntax |
| Generated std validators | `internal/stdvalidators/` | Run `go generate ./internal/stdvalidators` if `TestGeneratedArtifactCurrent` reports stale sources |

## 2. Tooling built on the compiler

These use the compiler packages directly, so they follow automatically. Still check them, because some carry construct-specific features:

| Component | Where | What to check |
| --- | --- | --- |
| Completion metadata and context recovery | `internal/driver/editor_completion*.go`, `internal/driver/editor_imports.go`, `internal/check/queries.go` | Compiler-owned symbol, field, variant and callable queries; token-based recovery of unfinished edits |
| Import organization | `internal/driver/editor_organize.go`, `internal/lsp/organize_imports.go` | Parsed import source ranges, compiler unused-import diagnostics, comment preservation and validated workspace edits |
| LSP completion presentation | `internal/lsp/completion.go` | Declaration snippets (`fn`, `match`, `type`, `test`); reserved keywords come directly from `syntax.Keywords()` |
| Language server | `internal/lsp/` | Construct-specific features: completion snippets and keywords, rename rules, code actions, inlay hints, semantic token classes, navigation |
| Lint warnings and suppression | `internal/check/lint.go`, `internal/check/facts.go`, `internal/driver/lint.go` | Compiler-backed rules and source ranges; `// lint:ignore` comment handling; JSON diagnostic and LSP fix parity |
| Browser playground | `internal/playground/`, `web/playground/`, `cmd/bork-playground/` | Compiles the real checker to WebAssembly; rebuild and run `internal/playground` parity tests |
| `bork new` templates | `internal/project/templates/` | Templates must still check, test and fmt-check (CI covers this) |

## 3. Highlighting grammars (separate syntax descriptions)

| Editor | Where | What to update |
| --- | --- | --- |
| VS Code, and GitHub/Sublime via TextMate | `editors/vscode/syntaxes/bork.tmLanguage.json` | Patterns for the new keyword or construct; every reserved keyword in the compiler map must receive a highlighting scope; tests in `editors/vscode/test/grammar.test.cjs` (CI job "TextMate grammar") |
| VS Code editing behavior | `editors/vscode/language-configuration.json` | Brackets, comments, auto-closing, indentation rules |
| Tree-sitter (Neovim, Helix, Zed, Emacs 29+) | `editors/tree-sitter-bork/` | `grammar.js`, `src/scanner.c`; regenerate `src/` with the pinned CLI and update `queries/` (highlights, locals, indents, folds, injections); CI parses every `.bork` file in `examples/` and `testdata/cases/` with an explicit checked recovery allowlist |
| Vim and Neovim lexical highlighting | `editors/vim/syntax/bork.vim` (sourced by Neovim) | Keywords, literal patterns, interpolation and Go regions; `editors/tests/vim.vim` loads every example/case |
| Emacs highlighting and editing | `editors/emacs/bork-mode.el` | Keyword list, font-lock patterns, tree-sitter captures, indentation, imenu; `editors/tests/emacs.el` highlights every example/case in both modes |
| Editor tree-sitter snapshots | `editors/{nvim,helix}/queries/bork/`, `editors/zed/languages/bork/*.scm`, `editors/sync-queries.py` | Update shared queries, run the adapter, then compile all snapshots and highlight every example/case in `editors/tests/nvim.lua` |
| Grammar source pins | `editors/helix/languages.toml`, `editors/zed/extension.toml` | Pin both consumers and upstream drafts to a commit containing the updated grammar; `editors/tests/check-grammar-pin.py` compares fetched sources with the shared grammar |
| Upstream integration drafts | `editors/upstream/*.patch` | Refresh copied queries and grammar revisions; `editors/tests/check-upstream.py` checks query drift |

Compiler keyword coverage for each highlighting grammar is checked by
`scripts/check-editor-keywords.cjs` in CI. Register new grammars there.

## 4. Documentation and examples

| What | Where |
| --- | --- |
| Formal grammar | `docs/grammar.md` (EBNF) |
| Specification | `docs/requirements.md` |
| Reader pages | `docs/language/*.md`, `docs/tour.md`, `README.md`; `TestDocSnippets` compiles every `bork` block |
| Examples | `examples/`; run `bork fmt` over them if the formatting changed |
| Golden tests | `testdata/cases/`; add a case for the new syntax (and a `fails` case for each new error), then `go test ./internal/driver -update` and review every diff |

## 5. Before merging

- `go test ./internal/syntax ./internal/format ./internal/check ./internal/lsp` and the affected driver cases pass locally; CI runs the rest.
- `bork fmt --check` is clean on `examples/` and `testdata/cases/`.
- The TextMate, tree-sitter and native editor package tests pass, so every `.bork` file in the repository still highlights without errors.
- If the change breaks existing programs, say so in the PR description, and migrate the examples, docs, std, and prelude in the same PR.
