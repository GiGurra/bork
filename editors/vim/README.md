# bork for Vim

Add the package from a complete checkout to your `vimrc`:

```vim
set runtimepath+=~/src/bork/editors/vim
filetype plugin indent on
syntax enable
```

This provides `.bork` and bork-shebang detection, lexical highlighting (including
typed strings and nested Go blocks), comments, and basic C-style indentation.
Compiler formatting is available through an LSP client. Install `bork` on PATH,
then choose one client below. Checking can execute compile-time code and Go tools,
so connect only to projects you trust.

## vim-lsp

After installing [vim-lsp](https://github.com/prabirshrestha/vim-lsp):

```vim
augroup bork_lsp
  autocmd!
  autocmd User lsp_setup call lsp#register_server({
        \ 'name': 'bork',
        \ 'cmd': {server_info -> ['bork', 'lsp']},
        \ 'allowlist': ['bork'],
        \ })
augroup END
```

## coc.nvim

Merge this into `:CocConfig`:

```json
{
  "languageserver": {
    "bork": {
      "command": "bork",
      "args": ["lsp"],
      "filetypes": ["bork"],
      "rootPatterns": ["bork.mod", ".git"]
    }
  }
}
```

## ALE

After installing [ALE](https://github.com/dense-analysis/ale), register its LSP
linter from `vimrc`:

```vim
call ale#linter#Define('bork', {
      \ 'name': 'bork',
      \ 'lsp': 'stdio',
      \ 'executable': 'bork',
      \ 'command': '%e lsp',
      \ 'project_root': {buffer -> fnamemodify(
      \   ale#path#FindNearestFile(buffer, 'bork.mod'), ':h')},
      \ })
let g:ale_linters = get(g:, 'ale_linters', {})
let g:ale_linters.bork = ['bork']
```

For a standalone file outside a module, set ALE's project root to the file's
parent directory instead. All three clients receive compiler diagnostics, hover,
navigation, completion, references, rename, formatting and code actions supported
by their client version. The [language-server notes](../vscode/README.md) describe
server limits. Vim highlighting and indentation do not perform compiler checks.
