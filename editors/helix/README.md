# bork for Helix

Install `bork` on PATH and merge [`languages.toml`](languages.toml) into your
Helix configuration at `~/.config/helix/languages.toml` (or the corresponding
`XDG_CONFIG_HOME` path). Preserve your other language settings.

Install the queries from a complete bork checkout:

```sh
mkdir -p ~/.config/helix/runtime/queries
cp -r ~/src/bork/editors/helix/queries/bork ~/.config/helix/runtime/queries/
hx --grammar fetch
hx --grammar build
hx --health bork
```

The grammar source is pinned to a bork repository revision and uses its
`editors/tree-sitter-bork` subdirectory. A C compiler and Git are needed to fetch
and build it. Install Helix's Go grammar for embedded `unsafe go` highlighting.
The generated queries provide highlighting, lexical locals, indentation and
Go injections. A folds query is included for consumers that support folding.

The configuration launches `bork lsp` over stdio and enables LSP formatting on
save. `.bork` files and bork shebang scripts receive compiler diagnostics,
navigation, completion, hover, references, rename and code actions. Connect only
to projects you trust: checking can execute compile-time code and Go tools.
Change `language-server.bork.command` for a compiler outside PATH.

An upstream Helix integration draft is in [`../upstream`](../upstream/README.md).
It has not been published. See the [server notes](../vscode/README.md) for limits.
