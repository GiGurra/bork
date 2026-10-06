"""Conservatively recognize documentation-only pull requests."""
import os
from pathlib import Path
import subprocess


def docs_only(paths):
    return bool(paths) and all(
        path.startswith(b"docs/") or (b"/" not in path and path.endswith(b".md")) or path == b"mkdocs.yml"
        for path in paths
    )


def classify(base, head):
    if not base or not head:
        return False
    # PRs compare with the merge base, not unrelated changes on main. Disabling
    # rename detection checks both old and new paths when code moves into docs.
    diff = subprocess.check_output(
        ["git", "diff", "--name-only", "--no-renames", "-z", f"{base}...{head}"]
    )
    return docs_only(diff.rstrip(b"\0").split(b"\0") if diff else [])


if __name__ == "__main__":
    result = classify(os.environ.get("PR_BASE"), os.environ.get("PR_HEAD"))
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        output.write(f"docs-only={str(result).lower()}\n")
