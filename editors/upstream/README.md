# Upstream integration drafts

These patches are prepared for review; no third-party pull requests have been
opened. Submission requires the maintainer's approval. Apply them from a clean
checkout of the named upstream revision with `git apply --check` followed by
`git apply`. Rebase the drafts and rerun the upstream's checks before submitting.

| Patch | Upstream | Tested base revision |
| --- | --- | --- |
| [nvim-lspconfig.patch](nvim-lspconfig.patch) | [neovim/nvim-lspconfig](https://github.com/neovim/nvim-lspconfig) | `3e8d598d3b5f8338a41699c436e5fa11d2666cf0` |
| [nvim-treesitter.patch](nvim-treesitter.patch) | [nvim-treesitter/nvim-treesitter](https://github.com/nvim-treesitter/nvim-treesitter) | `e289100ff98969e118c702199d88b764ce9e7fdf` |
| [helix.patch](helix.patch) | [helix-editor/helix](https://github.com/helix-editor/helix) | `ba40e547426b0f9896c8bdc699a4ab11f2b37dbc` |

The grammar uses a pinned revision and subdirectory of the bork repository.
Keep the grammar revision in both patches, `../helix/languages.toml`, and
`../zed/extension.toml` in sync when upgrading it. Pins must name a full commit
SHA from main history. Grammar PRs retain the previous pin; after squash merge,
open a follow-up repin PR as described in the [editor workflow](../README.md#updating-the-tree-sitter-grammar-pins).
CI verifies fetched sources against the pinned revision and warns on main when
a repin is due. Query additions are copied
from the generated editor snapshots; `../tests/check-upstream.py` checks their
contents. These drafts do not introduce another maintained grammar.

## nvim-lspconfig

Proposed title: **feat: add bork language server**

Proposed description: Add the compiler's `bork lsp` command, recognizing `bork.mod`
and Git roots while allowing standalone files. Diagnostics, types/facts on hover,
completion, navigation, formatting and code actions come from the compiler.
Install bork and Go on PATH. Filetype detection must be provided by bork's
Neovim package, or `vim.filetype.add({ extension = { bork = 'bork' } })`.

Validation: the same native configuration attaches in headless Neovim 0.11.6,
receives compiler diagnostics, and returns formatting edits for modules and
extensionless scripts. Run nvim-lspconfig's formatting and generated documentation
checks before submission.

## nvim-treesitter

Proposed title: **feat: add bork parser and queries**

Proposed description: Register the bork monorepo grammar as tier 3 and add shared
highlight, local, indent, fold and raw Go injection queries. The grammar parses
all bork examples and compiler cases in CI, with a strictly checked allowlist for
intentionally malformed recovery fixtures. Highlighting is independent of the
compiler; validation and formatting use the compiler.

Validation: all queries compile through Neovim's tree-sitter engine and highlights
run across every example/case source. The generated supported-language table is updated. Install the parser with the upstream
installer and run its query/style and generated-language-list checks before
submission. Filetype detection is supplied by bork's package as above.

## Helix

Proposed title: **Add bork language support**

Proposed description: Register `bork lsp`, file and shebang detection, project
roots, a pinned monorepo grammar, and adapted queries for highlighting, lexical
locals, indentation and embedded Go. Compiler formatting is enabled on save.
Install bork and Go on PATH; Git and a C compiler build the grammar.

Validation: Helix 25.07.1 fetches and builds this grammar and reports the server,
parser, highlighting and indentation in `hx --health bork`. Query compilation
and the complete example/case corpus are checked in bork CI. Run Helix's query,
configuration and generated-language-support checks before submission.
