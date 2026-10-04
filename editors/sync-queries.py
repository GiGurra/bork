#!/usr/bin/env python3
"""Adapt shared tree-sitter queries to each editor's capture vocabulary."""
import argparse
import os
import re
from pathlib import Path

root = Path(__file__).resolve().parent
source = Path(os.environ.get("BORK_GRAMMAR_DIR", root / "tree-sitter-bork")) / "queries"
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--check", action="store_true")
args = parser.parse_args()
zed_names = {
    "@function.method": "@function", "@function.call": "@function",
    "@type.definition": "@type", "@type.enum.variant": "@variant",
    "@variable.member": "@property", "@character": "@string",
}
for editor, names in [("nvim", {"@indent": "@indent.begin", "@outdent": "@indent.end", "@ignore": "@indent.ignore"}), ("helix", {
    "@variable.member": "@variable.other.member", "@number": "@constant.numeric",
    "@boolean": "@constant.builtin.boolean", "@character": "@constant.character",
    "@string.escape": "@constant.character.escape",
}), ("zed", zed_names)]:
    target = root / editor / ("languages/bork" if editor == "zed" else "queries/bork")
    target.mkdir(parents=True, exist_ok=True)
    for query in source.glob("*.scm"):
        text = query.read_text()
        text = re.sub(r"@[A-Za-z][A-Za-z0-9_.]*", lambda match: names.get(match[0], match[0]), text)
        if editor == "nvim" and query.name == "indents.scm":
            text = text.replace('@indent.end', '@indent.end @indent.branch')
        destination = target / query.name
        if args.check:
            if not destination.exists() or destination.read_text() != text:
                raise SystemExit(f"Stale editor query: {destination.relative_to(root)}")
        else:
            destination.write_text(text)
