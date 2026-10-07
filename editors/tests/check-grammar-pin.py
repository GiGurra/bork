#!/usr/bin/env python3
"""Require stable main-history pins; optionally verify a fetched grammar."""
import os
import re
import subprocess
import sys
import tomllib
from pathlib import Path

GRAMMAR = "editors/tree-sitter-bork"
REPOSITORY = "https://github.com/GiGurra/bork"


def git(root, *args):
    return subprocess.check_output(["git", "-C", str(root), *args])


def check(root, fetched=None, warn_stale=False):
    helix = tomllib.loads((root / "editors/helix/languages.toml").read_text())
    zed = tomllib.loads((root / "editors/zed/extension.toml").read_text())
    source = next(grammar["source"] for grammar in helix["grammar"] if grammar["name"] == "bork")
    zed_source = zed["grammars"]["bork"]
    rev = source["rev"]
    if (rev != zed_source["rev"] or source["subpath"] != GRAMMAR
            or zed_source["path"] != GRAMMAR or source["git"] != REPOSITORY
            or zed_source["repository"] != REPOSITORY):
        raise SystemExit("Helix and Zed grammar pins must agree on the bork repository, revision and subdirectory")
    if not re.fullmatch(r"[0-9a-f]{40}", rev):
        raise SystemExit("Grammar revision must be a full commit SHA")
    for name, pattern in [
        ("helix", r'\+.*\brev\s*=\s*"([^"]+)"'),
        ("nvim-treesitter", r"\+.*\brevision\s*=\s*'([^']+)'"),
    ]:
        patch = (root / "editors/upstream" / f"{name}.patch").read_text()
        if re.findall(pattern, patch) != [rev]:
            raise SystemExit(f"Stale grammar pin in {name} draft")
    if subprocess.run(["git", "-C", str(root), "merge-base", "--is-ancestor", rev, "origin/main"],
                      capture_output=True).returncode:
        raise SystemExit("Grammar pin must be a commit on origin/main; fetch main and repin after the grammar PR merges")

    # Compare all generated sources and headers, not just parser.c. Queries are
    # maintained separately as editor snapshots and checked by sync-queries.py.
    paths = git(root, "ls-tree", "-r", "--name-only", rev, f"{GRAMMAR}/grammar.js", f"{GRAMMAR}/src").decode().splitlines()
    if not paths:
        raise SystemExit("Pinned revision does not contain the grammar sources")
    if fetched is not None:
        for path in paths:
            actual = fetched / path
            if not actual.is_file() or actual.read_bytes() != git(root, "show", f"{rev}:{path}"):
                raise SystemExit(f"Fetched grammar differs from pinned revision: {path}")
    if warn_stale and git(root, "diff", "--name-only", rev, "origin/main", "--",
                          f"{GRAMMAR}/grammar.js", f"{GRAMMAR}/src"):
        print("::warning::Editor grammar pins lag main; open a follow-up PR repinning Helix, Zed and upstream drafts to the merged grammar commit (see editors/README.md).")
    print("Grammar pins agree and belong to main history." + (" Fetched sources match the pinned revision." if fetched else ""))


if __name__ == "__main__":
    check(Path(__file__).resolve().parents[2], Path(sys.argv[1]) if len(sys.argv) > 1 else None,
          warn_stale=os.environ.get("GITHUB_REF") == "refs/heads/main")
