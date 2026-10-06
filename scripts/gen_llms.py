"""Generate complete agent references from canonical reader Markdown at site build time."""
import argparse
from pathlib import Path, PurePosixPath
import re
from urllib.parse import quote, urlsplit, urlunsplit

# Keep this list aligned with findReaderPages in docs_snippets_test.go.
CONTRIBUTOR_REFERENCES = {"ci.md", "grammar.md", "requirements.md", "roadmap.md",
                          "std-go.md", "syntax-changes.md"}
TOKEN = re.compile(r'(?P<code>(?<!`)(?P<ticks>`+)(?!`)(?s:.*?)(?<!`)(?P=ticks)(?!`))|\[(?P<label>[^\]\n]+)\]\(\s*<?(?P<url>[^)\s>]+)>?(?:\s+"[^"]*")?\s*\)')
FENCE = re.compile(r"^ {0,3}(`{3,}|~{3,})")


def reader_pages(root):
    """Include new root, language and standard-library pages automatically."""
    docs = root / "docs"
    return [root / "README.md", *sorted(p for p in docs.glob("*.md")
                                      if p.name not in CONTRIBUTOR_REFERENCES),
            *sorted((docs / "language").glob("*.md")),
            *sorted((docs / "std").glob("*.md"))]


def published_url(path, site_url):
    """Map a docs-relative source path to this site's directory URL."""
    path = PurePosixPath(path)
    if path.suffix == ".md":
        path = path.parent if path.name in {"README.md", "index.md"} else path.with_suffix("")
        suffix = "/" if str(path) != "." else ""
    else:
        suffix = ""
    relative = "" if str(path) == "." else quote(str(path), safe="/")
    return site_url.rstrip("/") + "/" + relative + suffix


def resolve_link(url, page, root, site_url, repo_url, revision):
    parsed = urlsplit(url)
    if parsed.scheme or parsed.netloc or parsed.path.startswith("/"):
        return url
    target = (page.parent / parsed.path).resolve() if parsed.path else page.resolve()
    try:
        relative = target.relative_to(root.resolve())
    except ValueError:
        raise ValueError(f"link escapes repository: {page}: {url}") from None
    if not target.exists():
        raise ValueError(f"missing link target: {page}: {url}")
    if relative.parts and relative.parts[0] == "docs":
        destination = published_url(relative.relative_to("docs"), site_url)
        if target.is_dir():
            destination = destination.rstrip("/") + "/"
    else:
        kind = "tree" if target.is_dir() else "blob"
        suffix = "/" + quote(relative.as_posix(), safe="/") if relative.parts else ""
        destination = f"{repo_url.rstrip('/')}/{kind}/{quote(revision, safe='')}{suffix}"
    parts = urlsplit(destination)
    return urlunsplit((parts.scheme, parts.netloc, parts.path, parsed.query, parsed.fragment))


def rewrite_links(markdown, page, root, site_url, repo_url, revision):
    """Resolve prose links, preserving inline code, fences and their contents."""
    fence = None
    result, prose = [], []

    def replace(match):
        if match.group("code"):
            return match.group(0)
        url = match.group("url")
        destination = resolve_link(url, page, root, site_url, repo_url, revision)
        start, end = match.span("url")
        return match.group(0)[:start - match.start()] + destination + match.group(0)[end - match.start():]

    def flush():
        result.append(TOKEN.sub(replace, "".join(prose)))
        prose.clear()

    for line in markdown.splitlines(keepends=True):
        marker = FENCE.match(line)
        if marker:
            flush()
            token = marker.group(1)
            if fence is None:
                fence = token
            elif token[0] == fence[0] and len(token) >= len(fence) and not line[marker.end():].strip():
                fence = None
            result.append(line)
        elif fence:
            result.append(line)
        else:
            prose.append(line)
            if not line.strip():
                flush()
    flush()
    return "".join(result)


def generate(root, site_url, repo_url, revision):
    """Return the index and untruncated full reference, in stable source order."""
    index = ["# bork\n\n> A programming language for backend services.\n\n",
             f"[Complete reader reference]({site_url.rstrip('/')}/llms-full.txt)\n\n## Reader pages\n\n"]
    full = ["# bork complete reader reference\n\n",
            "Generated from the canonical reader documentation. Each source section is included in full.\n\n"]
    for page in reader_pages(root):
        source = page.read_text(encoding="utf-8")
        title = next((line[2:].strip() for line in source.splitlines() if line.startswith("# ")), page.stem)
        relative = page.relative_to(root).as_posix()
        url = resolve_link(page.name, page, root, site_url, repo_url, revision)
        index.append(f"- [{title}]({url})\n")
        full.append(f"---\n\n## Source: {relative}\n\n[{title}]({url})\n\n")
        full.append(rewrite_links(source, page, root, site_url, repo_url, revision))
        full.append("\n\n")
    return {"llms.txt": "".join(index), "llms-full.txt": "".join(full)}


def write_outputs(output, root, site_url, repo_url, revision):
    artifacts = generate(root, site_url, repo_url, revision)
    output.mkdir(parents=True, exist_ok=True)
    for name, content in artifacts.items():
        (output / name).write_text(content, encoding="utf-8", newline="\n")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--site-url", required=True)
    parser.add_argument("--repo-url", default="https://github.com/GiGurra/bork")
    parser.add_argument("--revision", default="main")
    args = parser.parse_args()
    write_outputs(args.output, Path(__file__).resolve().parent.parent,
                  args.site_url, args.repo_url, args.revision)
