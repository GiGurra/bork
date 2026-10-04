# Editors

Install `bork` and Go, and make `bork` available on your editor's PATH.
The language server runs as `bork lsp` over stdio. It uses the project's
`bork.mod` compiler selection just like the CLI. Diagnostics, navigation,
completion and formatting are provided by the compiler.

## VS Code, Cursor and VSCodium

```sh
bork editor install vscode
bork editor install vscode --editor cursor
bork editor install vscode --editor codium
```

This installs a checksum-verified release VSIX without using the Marketplace.
See [installation details](cli.md#editor-install-vscode) and the
[extension settings](../editors/vscode/README.md).

## Neovim

The [Neovim package](../editors/nvim/README.md) adds file and shebang detection,
shared tree-sitter highlighting and Go injection, plus native LSP setup.
Its README includes parser installation and configuration. The minimal LSP-only
setup below also works without installing the package.

For Neovim 0.11 or newer, add this to `init.lua`, open a `.bork` file, and use
`:checkhealth vim.lsp` to check the server. This uses Neovim's
[built-in LSP configuration](https://neovim.io/doc/user/lsp.html).

```lua
vim.filetype.add({ extension = { bork = 'bork' } })
vim.lsp.config('bork', {
  cmd = { 'bork', 'lsp' },
  filetypes = { 'bork' },
  root_markers = { 'bork.mod', '.git' },
  workspace_required = false,
})
vim.lsp.enable('bork')
```

## Helix

The [Helix package](../editors/helix/README.md) supplies a pinned grammar,
highlighting, indentation and Go injection. Follow its README to merge the
language configuration and install the queries. The minimal LSP-only setup below
works without the grammar.

Add this to `~/.config/helix/languages.toml` (or the project's
`.helix/languages.toml`). Use `hx --health bork` to check the configuration.
No Tree-sitter grammar is required for LSP features; these entries do not add
syntax highlighting. See the [Helix language configuration](https://docs.helix-editor.com/languages.html).

```toml
[language-server.bork]
command = "bork"
args = ["lsp"]

[[language]]
name = "bork"
scope = "source.bork"
file-types = ["bork"]
roots = ["bork.mod", ".git"]
language-servers = ["bork"]
comment-token = "//"
indent = { tab-width = 2, unit = "  " }
```

## Vim

The [Vim package](../editors/vim/README.md) includes syntax highlighting, embedded
Go, file and shebang detection, comments and indentation. Its README provides
vim-lsp, coc.nvim and ALE connection examples.

## Emacs

The [Emacs package](../editors/emacs/README.md) provides `bork-mode`, optional
`bork-ts-mode` with the shared parser, project discovery, and Eglot registration.
Follow its README for installation and lsp-mode setup. The basic mode below is
an alternative when you only need Eglot.

With Emacs 29 or newer, this defines a basic major mode and connects Eglot.
Open a `.bork` file and run `M-x eglot`. This mode provides comment handling;
Eglot provides the compiler features. See
[Eglot server configuration](https://www.gnu.org/software/emacs/manual/html_node/eglot/Setting-Up-LSP-Servers.html).

```elisp
(define-derived-mode bork-mode prog-mode "Bork"
  (setq-local comment-start "// ")
  (setq-local comment-end ""))
(add-to-list 'auto-mode-alist '("\\.bork\\'" . bork-mode))
(with-eval-after-load 'eglot
  (add-to-list 'eglot-server-programs
               '((bork-mode :language-id "bork") . ("bork" "lsp"))))
```

## Zed

The [Zed development extension](../editors/zed/README.md) registers the language,
shared tree-sitter grammar, Go injection and `bork lsp`. Install `bork` on PATH,
clone this repository, run **zed: install dev extension** from Zed's command
palette, and select `editors/zed`. Rust and a C compiler are required to build
the development extension. Its README covers compiler path overrides and
building the WASM artifact. Registry publication is a separate step.

## Navigation

The language server supplies type definitions for checked expressions and written
types, including container elements and union members. Implementation lookup
lists known class instances or method implementations and sealed variants.
Document highlights follow declaration identity, so a same-spelled local or field
in another scope stays separate.

Call hierarchy uses statically resolved compiler calls, including calls folded
away during checking. Concrete class calls lead to their selected implementation;
tests appear as callers and can be expanded. Calls through function values have
no static target. Readable disk dependencies can be navigation targets; embedded
standard-library sources currently have no editor URI.

Workspace symbol search and incoming calls include closed packages in configured
workspace folders. The compiler validates cached analyses against disk changes
and unsaved buffers before each workspace query. Empty workspace folders are
allowed. Broken workspace packages must check before workspace results are
available.
