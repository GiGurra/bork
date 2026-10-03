#!/usr/bin/env python3
"""Measure fresh-process bork latency and output sizes; emit JSON to stdout."""
import argparse
import json
import os
from pathlib import Path
import platform
import re
import statistics
import subprocess
import sys
import tempfile
import time


def run(command, cwd, env, timeout, expected_exit=0):
    start = time.perf_counter_ns()
    result = subprocess.run(command, cwd=cwd, env=env, capture_output=True,
                            timeout=timeout, check=False)
    elapsed = time.perf_counter_ns() - start
    if result.returncode != expected_exit:
        raise RuntimeError(f"{command}: exit {result.returncode}\n"
                           + result.stderr.decode(errors="replace")
                           + result.stdout.decode(errors="replace"))
    return elapsed, result.stdout


def synthetic(directory, count):
    directory.mkdir()
    source = "".join(f"fn value{i}(x: Int): Int {{\n  x + {i}\n}}\n\n"
                     for i in range(count))
    source += "fn main() {\n"
    source += "".join(f"  assert(value{i}(1) == {i + 1})\n" for i in range(count))
    source += '}\n\ntest "generated functions" {\n  assert(value0(1) == 1)\n}\n'
    (directory / "main.bork").write_text(source)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bork", type=Path, required=True, help="prebuilt compiler")
    parser.add_argument("--samples", type=int, default=3)
    parser.add_argument("--program", action="append", help="example name or synthetic100/1000")
    parser.add_argument("--cold", action="store_true", help="also use a fresh private GOCACHE per sample")
    parser.add_argument("--timeout", type=float, default=180)
    args = parser.parse_args()
    if args.samples < 1:
        parser.error("--samples must be positive")
    root = Path(__file__).resolve().parent.parent
    compiler = str(args.bork.resolve())
    rows = []
    with tempfile.TemporaryDirectory(prefix="bork-benchmark-") as temp:
        temp = Path(temp)
        workloads = {p.name: p for p in sorted((root / "examples").iterdir()) if p.is_dir()}
        for count in (100, 1000):
            name = f"synthetic{count}"
            synthetic(temp / name, count)
            workloads[name] = temp / name
        if args.program:
            missing = set(args.program) - workloads.keys()
            if missing:
                parser.error(f"unknown programs: {sorted(missing)}")
            workloads = {name: workloads[name] for name in args.program}
        for name, path in workloads.items():
            env = os.environ.copy()
            env.update(GOWORK="off", GOFLAGS="")
            _, source = run([compiler, "emit", str(path)], path, env, args.timeout)
            binary = temp / "program"
            run([compiler, "build", str(path), "-o", str(binary)], path, env, args.timeout)
            sizes = {"go_bytes": len(source), "binary_bytes": binary.stat().st_size,
                     "source_bytes": sum(p.stat().st_size for p in path.glob("*.bork"))}
            run_args = (path / "args.txt").read_text().split() if (path / "args.txt").exists() else []
            commands = {
                "check": [compiler, "check", str(path)],
                "build": [compiler, "build", str(path), "-o", str(binary)],
                "run": [compiler, "run", str(path), "--", *run_args],
            }
            if any(re.search(r'^test\s', p.read_text(), re.MULTILINE) for p in path.glob("*.bork")):
                commands["test"] = [compiler, "test", str(path)]
            for operation, command in commands.items():
                expected_exit = 0
                golden = root / "testdata" / "examples" / f"{name}.txt"
                if operation == "run" and golden.exists():
                    match = re.search(r"exit code (\d+)\n?$", golden.read_text())
                    if match:
                        expected_exit = int(match.group(1))
                # Prime the ambient Go cache for this specific command (test has different output).
                run(command, path, env, args.timeout, expected_exit)
                for cache in (["warm", "cold"] if args.cold else ["warm"]):
                    samples = []
                    for _ in range(args.samples):
                        with tempfile.TemporaryDirectory(dir=temp, prefix="go-cache-") as cache_dir:
                            sample_env = env.copy()
                            if cache == "cold":
                                sample_env["GOCACHE"] = cache_dir
                            elapsed, _ = run(command, path, sample_env, args.timeout, expected_exit)
                        samples.append(elapsed)
                    rows.append({"program": name, "operation": operation, "cache": cache,
                                 "expected_exit": expected_exit, "samples_ns": samples, "median_ns": statistics.median(samples), **sizes})
                    print(f"{name}/{operation}/{cache}: {statistics.median(samples)/1e6:.1f} ms", file=sys.stderr)
    print(json.dumps({"revision": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
                      "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
                      "platform": platform.platform(), "cpu_count": os.cpu_count(),
                      "results": rows}, indent=2))


if __name__ == "__main__":
    main()
