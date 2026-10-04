# Changelog

The extension has its own version and `vscode-v<version>` release tags, independent of compiler releases.

## 0.1.0

- Format bork files on save with two-space indentation by default.
- Show compiler version and server status, with restart and output commands.

- Highlight bork source, interpolated strings, and embedded Go.
- Start `bork lsp` for diagnostics, hover, definitions, references, completion, symbols, formatting, and compiler quick fixes.
- Check unsaved package files and scripts; support local and package-private rename.
- Configure the compiler path with `bork.serverPath`.
- Require workspace trust for language-server activation.
