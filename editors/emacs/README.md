# bork for Emacs

Emacs 29.1 or later is required. Add the package from a complete bork checkout:

```elisp
(add-to-list 'load-path (expand-file-name "~/src/bork/editors/emacs"))
(require 'bork-mode)
```

`bork-mode` provides font-lock, delimiter-based indentation, imenu, comments and
`.bork`/shebang detection. It recognizes `bork.mod` project roots. Its indentation
is a typing aid; use compiler formatting for canonical layout.

## Tree-sitter

If Emacs was built with tree-sitter support, build the shared parser:

```sh
~/src/bork/editors/install-parser.sh ~/.emacs.d/tree-sitter/libtree-sitter-bork.so
```

Adjust the destination if your `user-emacs-directory` differs. Then enable
`bork-ts-mode`:

```elisp
(add-to-list 'treesit-extra-load-path
             (expand-file-name "tree-sitter" user-emacs-directory))
(add-to-list 'major-mode-remap-alist '(bork-mode . bork-ts-mode))
```

This supplies structural bork highlighting and indentation. Raw Go bodies remain
opaque in this mode; Neovim, Helix and Zed can use the shared Go injection query.

## Eglot

Install `bork` on Emacs's PATH and run `M-x eglot` in a bork buffer. The package
registers both modes with the command `bork lsp`. Eglot handles diagnostics,
hover, completion, navigation, references, rename, formatting and code actions
using the compiler. Use `M-x eglot-format-buffer` for formatting. Connect only in
projects you trust: checking can execute compile-time code and Go tools.

For an explicit compiler path:

```elisp
(with-eval-after-load 'eglot
  (add-to-list 'eglot-server-programs
               '(((bork-mode :language-id "bork") (bork-ts-mode :language-id "bork")) "/absolute/path/to/bork" "lsp")))
```

## lsp-mode

As an alternative to Eglot, register a client after installing
[lsp-mode](https://emacs-lsp.github.io/lsp-mode/):

```elisp
(with-eval-after-load 'lsp-mode
  (add-to-list 'lsp-language-id-configuration '(bork-mode . "bork"))
  (add-to-list 'lsp-language-id-configuration '(bork-ts-mode . "bork"))
  (lsp-register-client
   (make-lsp-client :new-connection (lsp-stdio-connection '("bork" "lsp"))
                    :major-modes '(bork-mode bork-ts-mode)
                    :server-id 'bork)))
```

Run `M-x lsp` in a buffer. Use one LSP client for a buffer at a time.
See the [server notes](../vscode/README.md) for supported operations and limits.
MELPA publication is a separate future step.
