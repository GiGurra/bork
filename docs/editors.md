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
indent = { tab-width = 4, unit = "    " }
```

## Emacs

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

Bork support is coming with the Zed extension, which registers the Bork
language, Tree-sitter grammar and `bork lsp` server together. Zed needs that
extension before a language server can attach to `.bork` files; settings alone
cannot register a new language. Package installation instructions will live
under `editors/zed/` when that extension lands.
