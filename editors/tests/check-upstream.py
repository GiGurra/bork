#!/usr/bin/env python3
"""Ensure upstream draft query additions match the maintained snapshots."""
from pathlib import Path

root = Path(__file__).resolve().parents[1]
for patch, editor in [("nvim-treesitter", "nvim"), ("helix", "helix")]:
    text = (root / "upstream" / f"{patch}.patch").read_text()
    checked = set()
    for section in text.split("diff --git ")[1:]:
        header, *lines = section.splitlines()
        target = header.split(" b/", 1)[1]
        if not target.startswith("runtime/queries/bork/"):
            continue
        name = Path(target).name
        content = "\n".join(line[1:] for line in lines if line.startswith("+") and not line.startswith("+++")) + "\n"
        expected = (root / editor / "queries/bork" / name).read_text()
        if content != expected:
            raise SystemExit(f"Stale {patch} draft: {name}")
        checked.add(name)
    expected_names = {p.name for p in (root / editor / "queries/bork").glob("*.scm")}
    if checked != expected_names:
        raise SystemExit(f"Missing queries in {patch} draft: {expected_names - checked}")
print("Upstream draft queries match maintained editor snapshots.")
