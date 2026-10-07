# Editor packages

bork's compiler provides language features through `bork lsp`. Highlighting uses
[TextMate](vscode/README.md), [Vim](vim/README.md), or the shared
[tree-sitter grammar](tree-sitter-bork/README.md). Native packages are documented
for [Neovim](nvim/README.md), [Emacs](emacs/README.md), [Helix](helix/README.md),
and [Zed](zed/README.md). [Upstream drafts](upstream/README.md) are kept here too.

## Updating the tree-sitter grammar pins

Helix, Zed and the upstream Helix/nvim-treesitter drafts fetch a full commit SHA
from this repository's **main history**. A PR branch SHA is unsuitable: squash
merging creates a different commit, and deleting the branch can make the old
commit unavailable. Use two PRs when changing the grammar:

1. In the grammar PR, change the shared sources, regenerate `src/`, update the
   corpus and queries, and run `python3 editors/sync-queries.py` as needed. Keep
   the existing consumer pins. CI tests the new grammar locally and verifies
   that consumers still fetch the sources from their stable pinned revision.
2. After the grammar PR merges, open a small follow-up PR updating the same
   revision in all four files:
   - `editors/helix/languages.toml`
   - `editors/zed/extension.toml`
   - `editors/upstream/helix.patch`
   - `editors/upstream/nvim-treesitter.patch`

Choose the squash merge commit, or a later main commit containing the grammar:

```sh
git fetch origin main
# Run from a checkout of the merged grammar.
git rev-parse origin/main
python3 editors/tests/check-grammar-pin.py
python3 -m unittest discover -s editors/tests -p 'test_grammar_pin.py'
```

Copy the full SHA printed above into all four files. To verify an actual Helix
fetch, run `hx --grammar fetch` with the maintained language configuration, then
pass the fetched **repository root** to the check:

```sh
python3 editors/tests/check-grammar-pin.py "$HOME/.config/helix/runtime/grammars/sources/bork"
```

The check requires fetched grammar sources and generated headers to match the
pinned commit, rather than the current PR checkout. CI fetches main history and
rejects pins outside it, inconsistent pins, and malformed revisions. On main,
CI emits a warning while grammar sources differ from the pinned revision; that
warning is a reminder to open the follow-up, not a failure that blocks grammar
changes. Keep this gap short so editor installations receive new syntax promptly.
