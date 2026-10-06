"""Focused tests for repository links and the docs site's derived navigation."""
from html.parser import HTMLParser
from pathlib import Path
import re
import tempfile
import unittest

import markdown
import docs_site
import gen_llms


class Tables(HTMLParser):
    def __init__(self):
        super().__init__()
        self.tables = []
        self.links = []
        self.in_table = False

    def handle_starttag(self, tag, attrs):
        if tag == "table":
            self.in_table = True
            self.tables.append([])
        elif tag == "tr" and self.in_table:
            self.tables[-1].append(0)
        elif tag in {"td", "th"} and self.in_table:
            self.tables[-1][-1] += 1
        elif tag == "a" and self.in_table:
            self.links.extend(value for name, value in attrs if name == "href")

    def handle_endtag(self, tag):
        if tag == "table":
            self.in_table = False


def table_cells(line):
    # Portable tables need escaped pipes even inside code spans. Count only
    # structural pipes, honoring escapes (including escaped backslashes).
    cells, cell = [], []
    i = 0
    while i < len(line):
        char = line[i]
        if char == "\\" and i + 1 < len(line):
            cell.extend(line[i:i + 2])
            i += 2
            continue
        if char == "|":
            cells.append("".join(cell).strip())
            cell = []
        else:
            cell.append(char)
        i += 1
    cells.append("".join(cell).strip())
    if cells[0] == "":
        cells.pop(0)
    if cells and cells[-1] == "":
        cells.pop()
    return cells


def checked_tables(source):
    # Every reader table uses leading pipes. Reject detached rows and malformed
    # delimiters before comparing the rendered header/data cell counts.
    lines = source.splitlines()
    expected, in_code = [], False
    i = 0
    while i < len(lines):
        line = lines[i]
        if docs_site.FENCE.match(line):
            in_code = not in_code
        if in_code or not line.startswith("|"):
            i += 1
            continue
        header = table_cells(line)
        delimiter = table_cells(lines[i + 1]) if i + 1 < len(lines) else []
        if len(header) != len(delimiter) or not delimiter or not all(re.fullmatch(r":?-+:?", cell) for cell in delimiter):
            raise ValueError(f"line {i + 1}: malformed table header or detached row")
        rows = [len(header)]
        i += 2
        while i < len(lines) and lines[i].startswith("|"):
            count = len(table_cells(lines[i]))
            if count != len(header):
                raise ValueError(f"line {i + 1}: table row has {count} cells, expected {len(header)}; escape literal pipes")
            rows.append(count)
            i += 1
        expected.append(rows)
    rendered = Tables()
    # Reader-specific bork fence annotations are stripped by the site hook.
    source = re.sub(r"^```bork (?:fails|fragment)$", "```bork", source, flags=re.MULTILINE)
    rendered.feed(markdown.markdown(source, extensions=["tables", "fenced_code"]))
    if rendered.tables != expected:
        raise ValueError(f"rendered tables {rendered.tables} differ from source {expected}")
    return rendered


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

    def test_source_checker_link_spellings(self):
        root = Path(__file__).resolve().parent.parent
        for source, want in [
            ('[source](<../README.md>)', '[source](<https://github.com/GiGurra/bork/blob/main/README.md>)'),
            ('[source]( ../README.md )', '[source]( https://github.com/GiGurra/bork/blob/main/README.md )'),
            ('[source]( <../README.md> "title" )', '[source]( <https://github.com/GiGurra/bork/blob/main/README.md> "title" )'),
        ]:
            self.assertEqual(docs_site.rewrite_links(source, "README.md", root,
                             "https://github.com/GiGurra/bork", "main"), want)

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
        sections = {name: value for item in nav for name, value in item.items()}
        self.assertEqual(list(sections),
                         ["Home", "Start", "The language", "Quick references", "Reference", "For contributors"])
        self.assertEqual(sections["Start"][:3], [{"Installing bork": "install.md"}, {"A tour of bork": "tour.md"},
                                               {"Build a service": "tour-service.md"}])
        packages = sections["Reference"][1]["Standard packages"]
        self.assertIn({"bork/http": "std/http.md"}, packages)
        self.assertIn({"bork/shape": "std/shape.md"}, packages)
        self.assertIn({"CLI application cookbook": "std/cli-cookbook.md"}, packages)
        self.assertEqual(sections["Reference"][2], {"Editors": "editors.md"})

        def targets(items):
            if isinstance(items, str):
                yield items
            elif isinstance(items, dict):
                for value in items.values():
                    yield from targets(value)
            else:
                for value in items:
                    yield from targets(value)

        linked = set(targets(nav))
        for page in gen_llms.reader_pages(root):
            if page == root / "README.md":
                continue  # The repository overview is outside the site's docs dir.
            self.assertIn(page.relative_to(root / "docs").as_posix(), linked)

    def test_language_footer_chain_follows_index(self):
        root = Path(__file__).resolve().parent.parent
        index = (root / "docs/README.md").read_text()
        chain = [(label, url) for label, url in docs_site.index_links(index) if url.startswith("language/")]
        self.assertEqual({url for _, url in chain},
                         {p.relative_to(root / "docs").as_posix() for p in (root / "docs/language").glob("*.md")})
        self.assertEqual(len(chain), len({url for _, url in chain}))
        for i, (_, url) in enumerate(chain):
            source = (root / "docs" / url).read_text()
            footers = [line for line in source.splitlines() if line.startswith(("Previous:", "Next:"))]
            self.assertEqual(len(footers), 1, url)
            footer = footers[0]
            self.assertEqual(source.strip().splitlines()[-1], footer, url)
            if i:
                label, previous = chain[i - 1]
                self.assertIn(f"Previous: [{label}]({Path(previous).name})", footer, url)
            else:
                self.assertNotIn("Previous:", footer, url)
            if i + 1 < len(chain):
                label, following = chain[i + 1]
                self.assertIn(f"Next: [{label}]({Path(following).name})", footer, url)
            else:
                self.assertNotIn("Next:", footer, url)
            self.assertIn("[All pages](../README.md#the-language)", footer, url)

    def test_reader_tables_and_public_examples_render(self):
        root = Path(__file__).resolve().parent.parent
        for page in gen_llms.reader_pages(root):
            with self.subTest(page=page.relative_to(root)):
                source = page.read_text()
                tables = checked_tables(source)
                if page == root / "docs/examples.md":
                    linked = {match[1] for url in tables.links
                              if (match := re.search(r"^\.\./examples/([^/#]+)(?:/|$)", url))}
                    public = {entry.name for entry in (root / "examples").iterdir()
                              if entry.is_dir() and not entry.name.startswith(".") and (entry / "main.bork").is_file()}
                    self.assertTrue(public <= linked, f"examples missing from rendered table rows: {public - linked}")

    def test_table_regressions(self):
        valid = '| API | Result |\n| --- | --- |\n| `call()` | `Int \\| Error` |\n'
        self.assertEqual(checked_tables(valid).tables, [[2, 2]])
        for invalid in [
            '| A | B |\n| --- | --- | --- |\n| one | two | three |\n',
            valid + '\n| detached | row |\n',
            '| API | Result |\n| --- | --- |\n| `call()` | `Int | Error` |\n',
        ]:
            with self.assertRaises(ValueError):
                checked_tables(invalid)
        self.assertEqual(checked_tables('```text\n| not | a | table |\n```\n').tables, [])


if __name__ == "__main__":
    unittest.main()
