#!/usr/bin/env python3
"""Measure first output and total upgrade time, preserving both transcripts."""
import os
from pathlib import Path
import subprocess
import sys
import time


def measure(label, executable, version):
    root = Path("benchmark-results").resolve()
    root.mkdir(exist_ok=True)
    environment = dict(os.environ, BORKBIN=str(root / label / "bin"),
                       BORKUPDATECHECK="off", CI="true",
                       GOCACHE=str(root / label / "build-cache"),
                       GOMODCACHE=str(root / label / "module-cache"))
    start = time.monotonic()
    first = None
    with (root / f"{label}.log").open("w") as transcript:
        process = subprocess.Popen([executable, "upgrade", version], env=environment,
                                   stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                   text=True, bufsize=1)
        for line in process.stdout:
            elapsed = time.monotonic() - start
            if first is None:
                first = elapsed
            message = f"[{elapsed:.3f}s] {line}"
            transcript.write(message)
            print(message, end="", flush=True)
        status = process.wait()
    total = time.monotonic() - start
    first_text = "no output" if first is None else f"{first:.3f}s"
    result = f"| {label} | {first_text} | {total:.3f}s | {status} |\n"
    (root / f"{label}.timing.md").write_text(result)
    return result, status


def main():
    rows = []
    statuses = []
    for label, executable in zip(("source-before", "prebuilt-after"), sys.argv[1:3]):
        row, status = measure(label, executable, sys.argv[3])
        rows.append(row)
        statuses.append(status)
    summary = ("| Installer | First output | Total | Exit status |\n"
               "| --- | --- | --- | --- |\n" + "".join(rows))
    Path("benchmark-results/summary.md").write_text(summary)
    if path := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(path, "a") as output:
            output.write(summary)
    return int(any(statuses))


if __name__ == "__main__":
    sys.exit(main())
