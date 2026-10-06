"""Expand the built-ins reference from the checked compiler API at site build."""
import os
from pathlib import Path
import subprocess

ENTRY = "std/builtins.md"
MARKER = "<!-- builtin-api -->"


def builtin_reference(root):
    try:
        result = subprocess.run(
            ["go", "run", "./cmd/bork", "doc", "builtin"], cwd=root,
            env={**os.environ, "BORKTOOLCHAIN": "local"},
            check=True, capture_output=True, text=True,
        )
    except subprocess.CalledProcessError as error:
        raise RuntimeError("bork doc builtin failed:\n" + error.stderr.strip()) from error
    prefix = "# Bork API\n\n## builtin\n"
    if not result.stdout.startswith(prefix):
        raise ValueError("unexpected bork doc builtin document header")
    return "## API reference\n" + result.stdout[len(prefix):]


def expand_entry(source, reference):
    if source.count(MARKER) != 1:
        raise ValueError("built-ins entry must contain exactly one generation marker")
    return source.replace(MARKER, reference.rstrip())


def on_files(files, config):
    entry = files.get_file_from_path(ENTRY)
    if entry is None:
        raise ValueError("built-ins entry page is missing")
    root = Path(config.config_file_path).parent
    expanded = expand_entry(entry.content_string, builtin_reference(root))
    entry.content_string = expanded
    extra = config.setdefault("extra", {})
    overrides = extra.setdefault("llms_source_overrides", {})
    overrides["docs/" + ENTRY] = expanded
    return files
