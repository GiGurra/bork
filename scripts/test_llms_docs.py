"""Focused tests for complete, deterministic agent references and site publication."""
from html.parser import HTMLParser
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from urllib.parse import unquote, urlsplit

import markdown
import gen_llms
import docs_site

SITE = "https://example.com/project/"
REPO = "https://github.com/example/project"


class Links(HTMLParser):
    def __init__(self):
        super().__init__()
        self.links = []
        self.ids = set()

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "a" and "href" in attrs:
            self.links.append(attrs["href"])
        if "id" in attrs:
            self.ids.add(attrs["id"])


class LlmsTests(unittest.TestCase):
    def make_sources(self, root):
        pages = {
            "README.md": '# Repository\n\n[Quick reference](docs/cheatsheet.md#named-args).\n',
            "docs/README.md": '# Reader home\n\n## Start\n\n[Quick reference](cheatsheet.md).\n',
            "docs/cheatsheet.md": '# Cheat sheet\n\n## Named `args`\n\n[Self](#named-args) and [Types](language/types.md#records).\n',
            "docs/language/types.md": '# Types\n\n## Records\n\n[Packages](../std/README.md) and [source](../../source.bork).\n',
            "docs/std/README.md": '# Standard packages\n\n[Builtins](builtins.md).\n',
            "docs/std/builtins.md": '# Builtins\n\nAll builtins.\n',
            "docs/grammar.md": '# Contributor grammar\n\nExcluded from agent reference.\n',
            "docs/design/sketch.md": '# Design sketch\n\nAlso excluded.\n',
            "source.bork": 'fn main() {}\n',
        }
        for name, source in pages.items():
            path = root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(source, encoding="utf-8")
        return pages

    def test_deterministic_complete_discovery(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pages = self.make_sources(root)
            # Future pages are discovered, even with no index/navigation link.
            large = '# Cookbook\n\n' + ('Recipe prose with no truncation.\n' * 10000)
            (root / "docs/cookbook.md").write_text(large, encoding="utf-8")
            outputs = gen_llms.generate(root, SITE, REPO, "revision")
            self.assertEqual(outputs, gen_llms.generate(root, SITE, REPO, "revision"))
            expected = ["README.md", "docs/README.md", "docs/cheatsheet.md", "docs/cookbook.md",
                        "docs/language/types.md", "docs/std/README.md", "docs/std/builtins.md"]
            self.assertEqual([p.relative_to(root).as_posix() for p in gen_llms.reader_pages(root)], expected)
            full = outputs["llms-full.txt"]
            self.assertIn(large, full)
            for name in expected:
                self.assertEqual(full.count(f"## Source: {name}\n"), 1)
                if name in pages:
                    self.assertIn(gen_llms.rewrite_links(pages[name], root / name, root, SITE, REPO, "revision"), full)
            self.assertNotIn("Excluded from agent reference", full)
            self.assertNotIn("Also excluded", full)
            self.assertIn(f"[Complete reader reference]({SITE}llms-full.txt)", outputs["llms.txt"])
            for url in [SITE, SITE + "cheatsheet/", SITE + "language/types/", SITE + "std/", SITE + "std/builtins/"]:
                self.assertIn(f"]({url})", outputs["llms.txt"])
            self.assertIn(f"{REPO}/blob/revision/README.md", outputs["llms.txt"])

    def test_expanded_source_overrides(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            pages = self.make_sources(root)
            expanded = '# Full builtin API\n\n' + ('All function signatures.\n' * 2852)
            expanded += '\n[Types](../language/types.md#records)\n'
            overrides = {"docs/std/builtins.md": expanded}
            outputs = gen_llms.generate(root, SITE, REPO, "revision", overrides)
            full = outputs["llms-full.txt"]
            self.assertEqual(full.count('All function signatures.\n'), 2852)
            self.assertIn(f'[Types]({SITE}language/types/#records)', full)
            self.assertIn(f'[Full builtin API]({SITE}std/builtins/)', outputs["llms.txt"])
            self.assertEqual((root / 'docs/std/builtins.md').read_text(), pages['docs/std/builtins.md'])
            self.assertNotIn('All builtins.\n', full)
            self.assertEqual(outputs, gen_llms.generate(root, SITE, REPO, "revision", overrides))
            with self.assertRaisesRegex(ValueError, 'not reader pages'):
                gen_llms.generate(root, SITE, REPO, "revision", {'docs/std/typo.md': expanded})

    def test_link_spellings_and_code_preservation(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.make_sources(root)
            page = root / "docs/cheatsheet.md"
            source = ('[A](language/types.md?plain=1#records "title")\n'
                      '[B](<language/types.md#records>) [C]( #named-args )\n'
                      '[Source](../source.bork) [External](https://elsewhere.test/a) [Email](mailto:a@b.test)\n'
                      '`` `[nested](missing.md)` ``\n'
                      '``multiline\n`[nested](missing.md)` ``\n'
                      '`[inline](missing.md)`\n'
                      '```bork fails\n[code](missing.md)\n```\n'
                      '~~~~text\n[fenced](missing.md)\n~~~\n[still fenced](missing.md)\n~~~~\n')
            result = gen_llms.rewrite_links(source, page, root, SITE, REPO, "rev/with space")
            self.assertIn(f'[A]({SITE}language/types/?plain=1#records "title")', result)
            self.assertIn(f'[B](<{SITE}language/types/#records>) [C]( {SITE}cheatsheet/#named-args )', result)
            self.assertIn(f'[Source]({REPO}/blob/rev%2Fwith%20space/source.bork)', result)
            self.assertIn('[External](https://elsewhere.test/a) [Email](mailto:a@b.test)', result)
            self.assertIn(source[source.index('`` `[nested]'):], result)
            self.assertEqual(gen_llms.resolve_link('..', root / 'docs/README.md', root, SITE, REPO, 'revision'),
                             REPO + '/tree/revision')
            for target in ["missing.md", "../../escape.md"]:
                with self.assertRaises(ValueError):
                    gen_llms.resolve_link(target, page, root, SITE, REPO, "revision")

    def test_nested_backticks_preserve_code(self):
        root = Path(__file__).resolve().parent.parent
        source = "`` `[example](../README.md)` ``\n"
        self.assertEqual(docs_site.rewrite_links(source, "README.md", root,
                         "https://github.com/GiGurra/bork", "main"), source)

    def test_site_hook_writes_resolvable_artifacts(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            self.make_sources(root)
            hook = Path(__file__).with_name("docs_site.py").resolve()
            provider = root / "expanded_builtin.py"
            provider.write_text('def on_config(config):\n'
                                '    config["extra"]["llms_source_overrides"] = {"docs/std/builtins.md": "# Builtins\\n\\nExpanded API for agents.\\n"}\n'
                                '    return config\n', encoding="utf-8")
            config = root / "mkdocs.yml"
            config.write_text(f'site_name: Fixture\nsite_url: {SITE}\nrepo_url: {REPO}\n'
                              f'hooks:\n  - {hook}\n  - {provider}\nvalidation:\n  nav:\n    omitted_files: ignore\n'
                              'markdown_extensions:\n  - fenced_code\n  - toc\n', encoding="utf-8")
            built = subprocess.run([sys.executable, "-m", "mkdocs", "build", "--strict", "-f", str(config)],
                                   capture_output=True, text=True)
            self.assertEqual(built.returncode, 0, built.stdout + built.stderr)
            for name in ["llms.txt", "llms-full.txt"]:
                artifact = root / "site" / name
                self.assertTrue(artifact.is_file())
                links = Links()
                links.feed(markdown.markdown(artifact.read_text(encoding="utf-8"), extensions=["fenced_code", "tables"]))
                for url in links.links:
                    if not url.startswith(SITE):
                        continue
                    parsed = urlsplit(url)
                    relative = unquote(parsed.path[len(urlsplit(SITE).path):])
                    target = root / "site" / relative
                    if parsed.path.endswith("/"):
                        target /= "index.html"
                    self.assertTrue(target.is_file(), url)
                    if parsed.fragment:
                        html = Links()
                        html.feed(target.read_text(encoding="utf-8"))
                        self.assertIn(unquote(parsed.fragment), html.ids, url)
            self.assertIn("Expanded API for agents.", (root / "site/llms-full.txt").read_text())
            self.assertFalse((root / "docs/llms.txt").exists())


if __name__ == "__main__":
    unittest.main()
