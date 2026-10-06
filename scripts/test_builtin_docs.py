from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from mkdocs.structure.files import File, Files

import builtin_docs
import gen_llms


class Config(dict):
    def __init__(self, root):
        super().__init__(extra={"existing": "kept"})
        self.config_file_path = str(root / "mkdocs.yml")


class BuiltinDocsTests(unittest.TestCase):
    def test_compiler_command_uses_checkout_and_local_toolchain(self):
        with patch("builtin_docs.subprocess.run") as run:
            run.return_value = subprocess.CompletedProcess([], 0, "# Bork API\n\n## builtin\n\n### println\n", "")
            self.assertEqual(builtin_docs.builtin_reference(Path("checkout")), "## API reference\n\n### println\n")
            self.assertEqual(run.call_args.args[0], ["go", "run", "./cmd/bork", "doc", "builtin"])
            self.assertEqual(run.call_args.kwargs["cwd"], Path("checkout"))
            self.assertEqual(run.call_args.kwargs["env"]["BORKTOOLCHAIN"], "local")
            self.assertTrue(run.call_args.kwargs["check"])

    def test_compiler_diagnostic_is_preserved(self):
        failure = subprocess.CalledProcessError(1, ["go", "run"], stderr="compiler diagnostic")
        with patch("builtin_docs.subprocess.run", side_effect=failure):
            with self.assertRaisesRegex(RuntimeError, "compiler diagnostic"):
                builtin_docs.builtin_reference(Path("checkout"))

    def test_bad_compiler_header_and_generation_marker_fail(self):
        for source in ["# Built-ins", builtin_docs.MARKER * 2]:
            with self.subTest(source=source), self.assertRaises(ValueError):
                builtin_docs.expand_entry(source, "reference")
        with patch("builtin_docs.subprocess.run") as run:
            run.return_value = subprocess.CompletedProcess([], 0, "wrong document", "")
            with self.assertRaises(ValueError):
                builtin_docs.builtin_reference(Path("checkout"))

    def test_hook_expands_once_and_shares_exact_source_without_writing(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = "# Built-ins\n\n" + builtin_docs.MARKER + "\n"
            page = root / "docs" / builtin_docs.ENTRY
            page.parent.mkdir(parents=True)
            page.write_text(source)
            entry = File(builtin_docs.ENTRY, str(root / "docs"), str(root / "site"), True)
            files = Files([entry])
            config = Config(root)
            reference = "## API reference\n\n" + "full API line\n" * 3000
            with patch("builtin_docs.builtin_reference", return_value=reference) as build:
                self.assertIs(builtin_docs.on_files(files, config), files)
                build.assert_called_once_with(root)
            expanded = entry.content_string
            self.assertEqual(expanded, config["extra"]["llms_source_overrides"]["docs/std/builtins.md"])
            self.assertEqual(config["extra"]["existing"], "kept")
            self.assertEqual(page.read_text(), source)
            self.assertNotIn(builtin_docs.MARKER, expanded)
            self.assertEqual(expanded.count("full API line"), 3000)

    def test_llms_publishes_the_same_expanded_reference(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            pages = {
                "README.md": "# bork\n",
                "docs/README.md": "# Documentation\n\n[Built-ins](std/builtins.md).\n",
                "docs/std/README.md": "# Standard packages\n",
                "docs/std/builtins.md": "# Built-ins\n\n" + builtin_docs.MARKER + "\n",
            }
            for name, source in pages.items():
                page = root / name
                page.parent.mkdir(parents=True, exist_ok=True)
                page.write_text(source)
            entry = File(builtin_docs.ENTRY, str(root / "docs"), str(root / "site"), True)
            config = Config(root)
            reference = "## API reference\n\n### eprintln\n\n" + "signature line\n" * 3000
            with patch("builtin_docs.builtin_reference", return_value=reference):
                builtin_docs.on_files(Files([entry]), config)
            outputs = gen_llms.generate(root, "https://gigurra.github.io/bork/",
                                        "https://github.com/GiGurra/bork", "main",
                                        source_overrides=config["extra"]["llms_source_overrides"])
            self.assertIn(entry.content_string, outputs["llms-full.txt"])
            self.assertNotIn(builtin_docs.MARKER, outputs["llms-full.txt"])
            self.assertIn("https://gigurra.github.io/bork/std/builtins/", outputs["llms.txt"])

    def test_missing_page_fails(self):
        with self.assertRaises(ValueError):
            builtin_docs.on_files(Files([]), Config(Path("checkout")))


if __name__ == "__main__":
    unittest.main()
