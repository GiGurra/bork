# bork for Zed

This development extension provides bork tree-sitter highlighting, indentation,
Go injection, file/shebang detection, and the compiler's language server.

Install `bork` on PATH. In Zed's command palette, run **zed: install dev extension**
and select this `editors/zed` directory from a complete bork checkout. Zed builds
the Rust extension and fetches the grammar pinned in `extension.toml`; Rust and a
C compiler are required for development installation. Open a `.bork` file and
select **bork** if needed. Install Zed's Go support for injected raw Go bodies.

The extension launches `bork lsp` through the worktree environment, without
bundling or downloading a compiler. It supplies compiler diagnostics, hover,
completion, navigation, references, rename, formatting and code actions.
Enable the language server only for projects you trust: checking can execute
compile-time code and Go tools.

For a compiler outside PATH, merge this into Zed settings:

```json
{
  "lsp": {
    "bork": {
      "binary": {
        "path": "/absolute/path/to/bork",
        "arguments": ["lsp"]
      }
    }
  }
}
```

The optional `binary.env` map overrides worktree environment variables.
Use `editor: format` for compiler formatting. The [server notes](../vscode/README.md)
describe operation limits. This is a local development extension; registry
publication is a separate step.

To verify the WASM extension without a graphical editor:

```sh
rustup target add wasm32-wasip1
cargo build --manifest-path editors/zed/Cargo.toml --locked --release --target wasm32-wasip1
```

Shared queries are adapted by `editors/sync-queries.py`; CI checks their drift.
See Zed's [language extension guide](https://zed.dev/docs/extensions/languages)
for the host API and extension installation.
