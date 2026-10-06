"""Regressions for docs-only CI detection and required-check aggregation."""
from pathlib import Path
import json
import os
import subprocess
import unittest
from unittest.mock import patch

import ci_changes


class ChangesTests(unittest.TestCase):
    def test_docs_and_mixed_paths(self):
        self.assertTrue(ci_changes.docs_only([b"README.md", b"docs/tour.md", b"docs/assets/a.png", b"mkdocs.yml"]))
        for paths in ([], [b"go.mod"], [b"docs/tour.md", b"internal/driver/a.go"],
                      [b"scripts/docs-requirements.txt"], [b".github/workflows/ci.yml"], [b"docs.go"]):
            with self.subTest(paths=paths):
                self.assertFalse(ci_changes.docs_only(paths))

    def test_non_pr_does_not_diff(self):
        with patch("ci_changes.subprocess.check_output") as diff:
            self.assertFalse(ci_changes.classify(None, None))
            self.assertFalse(ci_changes.classify("base", ""))
            diff.assert_not_called()

    def test_diff_handles_newlines_and_disables_renames(self):
        with patch("ci_changes.subprocess.check_output", return_value=b"docs/a\nb.md\0") as diff:
            self.assertTrue(ci_changes.classify("base", "head"))
            diff.assert_called_once_with(["git", "diff", "--name-only", "--no-renames", "-z", "base...head"])
        with patch("ci_changes.subprocess.check_output", return_value=b"code.go\0docs/code.md\0"):
            self.assertFalse(ci_changes.classify("base", "head"))
        with patch("ci_changes.subprocess.check_output", side_effect=subprocess.CalledProcessError(1, "git")):
            with self.assertRaises(subprocess.CalledProcessError):
                ci_changes.classify("base", "head")

    def test_required_ci_gate(self):
        workflow = (Path(__file__).parent.parent / ".github/workflows/ci.yml").read_text()
        gate = workflow.split("python3 - <<'PY'\n", 1)[1].split("          PY", 1)[0]
        gate = "\n".join(line[10:] for line in gate.splitlines())
        jobs = ["changes", "test", "docs", "lint", "grammars", "editors", "toolchain", "cache-maintenance"]
        for docs_only in (False, True):
            needs = {job: {"result": "success"} for job in jobs}
            needs["changes"]["outputs"] = {"docs-only": str(docs_only).lower()}
            needs["test" if docs_only else "docs"]["result"] = "skipped"
            cases = [(needs, True)]
            for job in jobs:
                for result in ("failure", "cancelled", "skipped"):
                    changed = json.loads(json.dumps(needs))
                    changed[job]["result"] = result
                    cases.append((changed, result == needs[job]["result"]))
            for case, success in cases:
                with self.subTest(docs_only=docs_only, needs=case):
                    completed = subprocess.run(["python3", "-c", gate], env={**os.environ, "NEEDS": json.dumps(case)},
                                               capture_output=True, text=True)
                    self.assertEqual(completed.returncode == 0, success, completed.stdout + completed.stderr)
