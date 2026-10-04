#!/usr/bin/env python3
"""Check that consumers fetch the shared grammar tested in this checkout."""
import sys
import tomllib
from pathlib import Path

root = Path(__file__).resolve().parents[2]
helix = tomllib.loads((root / "editors/helix/languages.toml").read_text())
zed = tomllib.loads((root / "editors/zed/extension.toml").read_text())
source = next(grammar["source"] for grammar in helix["grammar"] if grammar["name"] == "bork")
zed_source = zed["grammars"]["bork"]
if source["rev"] != zed_source["rev"] or source["subpath"] != zed_source["path"]:
    raise SystemExit("Helix and Zed grammar pins differ")
for name in ["helix", "nvim-treesitter"]:
    if source["rev"] not in (root / "editors/upstream" / f"{name}.patch").read_text():
        raise SystemExit(f"Stale grammar pin in {name} draft")
fetched = Path(sys.argv[1]) / source["subpath"]
local = root / "editors/tree-sitter-bork"
for name in ["grammar.js", "src/scanner.c", "src/parser.c", "src/node-types.json"]:
    if (fetched / name).read_bytes() != (local / name).read_bytes():
        raise SystemExit(f"Pinned grammar differs: {name}; update consumer pins to a revision containing the shared grammar")
print("Fetched grammar matches shared sources; editor and draft pins agree.")
