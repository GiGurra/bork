# bork for Neovim

Neovim 0.11 or later, a C compiler, and `bork` on PATH are required.
Clone [bork](https://github.com/GiGurra/bork) to `~/src/bork`, then build the parser:

```sh
~/src/bork/editors/install-parser.sh ~/.local/share/nvim/site/parser/bork.so
```

In `init.lua`, add the package before plugins load:

```lua
vim.opt.runtimepath:append(vim.fn.expand('~/src/bork/editors/nvim'))
require('bork').setup()
```

The package detects `.bork` files and extensionless bork shebang scripts,
loads the shared tree-sitter queries, and starts `bork lsp` over stdio.
It uses `bork.mod` or `.git` as project markers and supports standalone files.
A lexical Vim highlighter and basic C-style indentation work without the parser.
Install a Go tree-sitter parser to highlight injected `unsafe go` bodies.
Canonical formatting comes from the language server.

Configure the compiler path or other native LSP settings through `setup`:

```lua
require('bork').setup({ cmd = { '/absolute/path/to/bork', 'lsp' } })
```

Native hover, navigation, references, completion, rename, formatting and code
actions use compiler results. Neovim's usual LSP mappings work; completion is
available through `<C-x><C-o>`. For example, format with
`:lua vim.lsp.buf.format({ name = 'bork' })`. Enable LSP only for projects you
trust: checking can execute compile-time code and Go tools.

With current nvim-treesitter, use its indentation engine by setting:

```lua
vim.api.nvim_create_autocmd('FileType', {
  pattern = 'bork',
  callback = function()
    vim.bo.indentexpr = "v:lua.require'nvim-treesitter'.indentexpr()"
  end,
})
```

Upstream nvim-lspconfig and nvim-treesitter integration drafts live in
[`../upstream`](../upstream/README.md). They are prepared patches, not published PRs.
See the [VS Code README](../vscode/README.md) for the shared language server's
capabilities and current limits. All packages use the repository's MIT license.
