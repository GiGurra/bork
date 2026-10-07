"""Exercise pin checks with real Git histories, including squash-only commits."""
import contextlib
import importlib.util
import io
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("grammar_pin", Path(__file__).with_name("check-grammar-pin.py"))
pin = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pin)


class GrammarPinTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name) / "repo"
        self.root.mkdir()
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "test@example.com")
        self.git("config", "user.name", "Test")
        self.write(f"{pin.GRAMMAR}/grammar.js", "grammar")
        self.write(f"{pin.GRAMMAR}/src/parser.c", "parser")
        self.write(f"{pin.GRAMMAR}/src/tree_sitter/parser.h", "header")
        self.rev = self.commit()
        self.git("update-ref", "refs/remotes/origin/main", self.rev)
        self.set_pins(self.rev)
        self.fetched = Path(self.temp.name) / "fetched"
        shutil.copytree(self.root / pin.GRAMMAR, self.fetched / pin.GRAMMAR)

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.root), *args], stderr=subprocess.DEVNULL).decode().strip()

    def write(self, name, text):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "fixture")
        return self.git("rev-parse", "HEAD")

    def set_pins(self, rev):
        self.write("editors/helix/languages.toml", f'''[[grammar]]
name = "bork"
source = {{ git = "{pin.REPOSITORY}", rev = "{rev}", subpath = "{pin.GRAMMAR}" }}
''')
        self.write("editors/zed/extension.toml", f'''[grammars.bork]
repository = "{pin.REPOSITORY}"
rev = "{rev}"
path = "{pin.GRAMMAR}"
''')
        self.write("editors/upstream/helix.patch", f'+source = {{ rev = "{rev}" }}\n')
        self.write("editors/upstream/nvim-treesitter.patch", f"+ revision = '{rev}',\n")

    def check(self, **kwargs):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            pin.check(self.root, **kwargs)
        return output.getvalue()

    def test_main_pin_and_fetched_sources(self):
        self.assertIn("Fetched sources match", self.check(fetched=self.fetched))

    def test_branch_only_pin_is_rejected(self):
        self.write(f"{pin.GRAMMAR}/grammar.js", "branch grammar")
        self.set_pins(self.commit())
        with self.assertRaisesRegex(SystemExit, "commit on origin/main"):
            self.check()

    def test_old_main_pin_allows_new_grammar_and_warns_only_when_requested(self):
        self.write(f"{pin.GRAMMAR}/grammar.js", "merged grammar")
        self.git("update-ref", "refs/remotes/origin/main", self.commit())
        self.assertNotIn("::warning::", self.check(fetched=self.fetched))
        self.assertIn("::warning::", self.check(fetched=self.fetched, warn_stale=True))

    def test_current_main_pin_does_not_warn(self):
        self.assertNotIn("::warning::", self.check(warn_stale=True))

    def test_generated_header_mismatch_is_rejected(self):
        (self.fetched / pin.GRAMMAR / "src/tree_sitter/parser.h").write_text("wrong header")
        with self.assertRaisesRegex(SystemExit, "Fetched grammar differs.*parser.h"):
            self.check(fetched=self.fetched)

    def test_missing_fetched_source_is_rejected(self):
        (self.fetched / pin.GRAMMAR / "grammar.js").unlink()
        with self.assertRaisesRegex(SystemExit, "Fetched grammar differs.*grammar.js"):
            self.check(fetched=self.fetched)

    def test_divergent_consumers_are_rejected(self):
        path = self.root / "editors/zed/extension.toml"
        path.write_text(path.read_text().replace(self.rev, "0" * 40))
        with self.assertRaisesRegex(SystemExit, "Helix and Zed"):
            self.check()

    def test_full_sha_is_required(self):
        self.set_pins(self.rev[:8])
        with self.assertRaisesRegex(SystemExit, "full commit SHA"):
            self.check()

    def test_comment_with_correct_sha_does_not_hide_stale_draft(self):
        self.write("editors/upstream/helix.patch", f'+source = {{ rev = "{"0" * 40}" }}\n# {self.rev}\n')
        with self.assertRaisesRegex(SystemExit, "Stale grammar pin in helix"):
            self.check()

    def test_missing_main_history_is_rejected(self):
        self.git("update-ref", "-d", "refs/remotes/origin/main")
        with self.assertRaisesRegex(SystemExit, "fetch main"):
            self.check()


if __name__ == "__main__":
    unittest.main()
