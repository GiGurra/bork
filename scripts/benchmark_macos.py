#!/usr/bin/env python3
"""Measure build/run latency and the first execution of a new binary."""

import argparse
import json
import os
from pathlib import Path
import shutil
import statistics
import subprocess
import tempfile
import time


def measure(command, env):
    start = time.perf_counter()
    subprocess.run(command, env=env, check=True, stdout=subprocess.DEVNULL)
    return (time.perf_counter() - start) * 1000


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bork", required=True)
    parser.add_argument("--samples", type=int, default=5)
    args = parser.parse_args()
    bork = str(Path(args.bork).resolve())
    results = {}
    with tempfile.TemporaryDirectory(prefix="bork-macos-") as work:
        root = Path(work)
        env = dict(os.environ, BORKCACHE=str(root / "cache"), BORKTOOLCHAIN="local")
        for program in ("hello", "http_server"):
            source = "examples/" + program
            output = root / program
            operations = {"build": [bork, "build", source, "-o", str(output)]}
            if program == "hello":
                operations["run"] = [bork, "run", source]
            for operation, command in operations.items():
                shutil.rmtree(root / "cache", ignore_errors=True)
                output.unlink(missing_ok=True)
                cold = measure(command, env)
                # Give detached receipt publication a chance to finish before
                # measuring the steady state; these warmups are not timed.
                for _ in range(3):
                    measure(command, env)
                warm = [measure(command, env) for _ in range(args.samples)]
                results[program + "/" + operation] = {
                    "cold_ms": cold,
                    "warm_ms": warm,
                    "warm_median_ms": statistics.median(warm),
                }
            if program == "hello":
                measure(operations["build"], env)
                first, repeat = [], []
                for index in range(args.samples):
                    fresh = root / ("fresh-" + str(index))
                    shutil.copyfile(output, fresh)
                    fresh.chmod(0o755)
                    first.append(measure([str(fresh)], env))
                    repeat.append(measure([str(fresh)], env))
                results["hello/exec"] = {
                    "first_ms": first,
                    "repeat_ms": repeat,
                    "first_median_ms": statistics.median(first),
                    "repeat_median_ms": statistics.median(repeat),
                }
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
