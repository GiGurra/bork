"""MkDocs hooks: derive navigation from the docs index and resolve source links."""
import os
from pathlib import Path, PurePosixPath
import posixpath
import re
from urllib.parse import quote, urlsplit, urlunsplit

from gen_llms import FENCE, TOKEN, write_outputs

# Reader pages use inline links, enforced by TestDocLinks. Protect inline code
# and fenced blocks so examples of Markdown are never changed by the site build.


def source_url(url, page, root, repo_url, revision):
    parsed = urlsplit(url)
    if parsed.scheme or parsed.netloc or not parsed.path or parsed.path.startswith("/"):
        return url
    target = posixpath.normpath(posixpath.join("docs", str(PurePosixPath(page).parent), parsed.path))
    if target == "docs" or target.startswith("docs/"):
        return url
    if target == ".." or target.startswith("../"):
        raise ValueError(f"link escapes repository: {page}: {url}")
    kind = "tree" if (root / target).is_dir() else "blob"
    path = f"{repo_url.rstrip('/')}/{kind}/{quote(revision, safe='')}/{quote(target, safe='/')}"
    return urlunsplit((*urlsplit(path)[:3], parsed.query, parsed.fragment))


def rewrite_links(markdown, page, root, repo_url, revision):
    fence = None
    result = []
    for line in markdown.splitlines(keepends=True):
        marker = FENCE.match(line)
        if marker:
            token = marker.group(1)
            if fence is None:
                fence = token
                # The snippet checker understands these suffixes; Markdown
                # renderers need only the language to recognize fenced code.
                line = re.sub(r"(`{3,}bork) (?:fragment|fails)\s*$", r"\1", line.rstrip("\r\n")) + ("\n" if line.endswith("\n") else "")
            elif token[0] == fence[0] and len(token) >= len(fence) and not line[marker.end():].strip():
                fence = None
            result.append(line)
            continue
        if fence:
            result.append(line)
            continue

        def replace(match):
            if match.group("code"):
                return match.group(0)
            url = match.group("url")
            rewritten = source_url(url, page, root, repo_url, revision)
            start, end = match.span("url")
            return match.group(0)[:start - match.start()] + rewritten + match.group(0)[end - match.start():]

        result.append(TOKEN.sub(replace, line))
    return "".join(result)


def index_links(markdown):
    for line in markdown.splitlines():
        for match in TOKEN.finditer(line):
            if match.group("url"):
                yield match.group("label"), match.group("url")


def navigation(root, repo_url, revision):
    docs = root / "docs"
    nav = [{"Home": "README.md"}]
    section = []
    for line in (docs / "README.md").read_text().splitlines():
        if line.startswith("## "):
            section = []
            nav.append({line[3:].strip(): section})
        for label, url in index_links(line):
            target = source_url(url, "README.md", root, repo_url, revision)
            if url == "std/README.md":
                packages = [{"Overview": url}]
                for package, package_url in index_links((docs / url).read_text()):
                    # The table and cookbook links are the API index. Contributor links and bork/build's
                    # alias into the language guide remain links in the overview.
                    if package_url.endswith(".md") and not package_url.startswith("../"):
                        packages.append({package: "std/" + package_url})
                section.append({label: packages})
            else:
                section.append({label: target})
    return nav


def on_config(config):
    root = Path(config.config_file_path).parent
    config["nav"] = navigation(root, config["repo_url"], os.environ.get("BORK_DOCS_REVISION", "main"))
    return config


def on_page_markdown(markdown, page, config, files):
    return rewrite_links(markdown, page.file.src_uri, Path(config.config_file_path).parent,
                         config["repo_url"], os.environ.get("BORK_DOCS_REVISION", "main"))


def on_post_build(config):
    write_outputs(Path(config["site_dir"]), Path(config.config_file_path).parent,
                  config["site_url"], config["repo_url"],
                  os.environ.get("BORK_DOCS_REVISION", "main"),
                  config.get("extra", {}).get("llms_source_overrides"))
