"""Focused tests for repository links and the docs site's derived navigation."""
from pathlib import Path
import tempfile
import unittest

import docs_site


class SiteTests(unittest.TestCase):
    def test_links(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            (root / "examples" / "hello").mkdir(parents=True)
            cases = {
                "../examples/hello": "https://github.com/GiGurra/bork/tree/main/examples/hello",
                "../internal/prelude/strings.bork#L4": "https://github.com/GiGurra/bork/blob/main/internal/prelude/strings.bork#L4",
                "../README.md?plain=1#install": "https://github.com/GiGurra/bork/blob/main/README.md?plain=1#install",
                "language/basics.md#functions": "language/basics.md#functions",
                "std/README.md": "std/README.md",
                "#local": "#local",
                "https://go.dev/dl/": "https://go.dev/dl/",
                "//example.com/x": "//example.com/x",
            }
            for url, want in cases.items():
                self.assertEqual(docs_site.source_url(url, "README.md", root, "https://github.com/GiGurra/bork", "main"), want)
            self.assertEqual(docs_site.source_url("../../internal/a.bork", "std/http.md", root,
                             "https://github.com/GiGurra/bork", "abc123"),
                             "https://github.com/GiGurra/bork/blob/abc123/internal/a.bork")
            with self.assertRaises(ValueError):
                docs_site.source_url("../../escape", "README.md", root, "https://github.com/GiGurra/bork", "main")

    def test_preserves_examples_and_link_titles(self):
        root = Path(__file__).resolve().parent.parent
        source = '[source](../README.md "title")\n`[example](../README.md)`\n```bork\n[source](../README.md)\n```\n'
        want = '[source](https://github.com/GiGurra/bork/blob/main/README.md "title")\n`[example](../README.md)`\n```bork\n[source](../README.md)\n```\n'
        self.assertEqual(docs_site.rewrite_links(source, "README.md", root, "https://github.com/GiGurra/bork", "main"), want)

    def test_snippet_modes_render_as_code(self):
        root = Path(__file__).resolve().parent.parent
        for mode in ("fragment", "fails"):
            source = f'```bork {mode}\nassemble[Server](s, Services)\n```\n'
            self.assertEqual(docs_site.rewrite_links(source, "language/packages.md", root,
                             "https://github.com/GiGurra/bork", "main"),
                             '```bork\nassemble[Server](s, Services)\n```\n')

    def test_navigation_follows_index(self):
        root = Path(__file__).resolve().parent.parent
        nav = docs_site.navigation(root, "https://github.com/GiGurra/bork", "main")
        self.assertEqual([next(iter(item)) for item in nav],
                         ["Home", "Start", "The language", "Reference", "For contributors"])
        self.assertEqual(nav[1]["Start"][0], {"A tour of bork": "tour.md"})
        reference = nav[3]["Reference"]
        packages = reference[1]["Standard packages"]
        self.assertIn({"bork/http": "std/http.md"}, packages)
        self.assertTrue(reference[2]["Editor support and VS Code"].endswith("/blob/main/editors/vscode/README.md"))


if __name__ == "__main__":
    unittest.main()
