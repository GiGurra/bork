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


def diagnose(command, bork, root, env):
    probe = root / "go-commands.txt"
    phases = root / "phases.json"
    status = root / "cache-status.txt"
    cache = root / "profile-cache"
    profile_env = dict(
        env,
        BORKCACHE=str(cache),
        BORK_TEST_CACHE_PRODUCTION="1",
        BORK_TEST_CACHE_PUBLISH="on",
        BORK_TEST_GO_COMMAND_PROBE=str(probe),
        BORK_TEST_DISK_CACHE_TIMINGS=str(phases),
        BORK_TEST_DISK_CACHE_PROBE=str(status),
    )
    command = [str(Path(bork).resolve()), *command[1:]]
    output = None
    if "-o" in command:
        index = command.index("-o") + 1
        output = root / ("profile-" + Path(command[index]).name)
        command[index] = str(output)
    for _ in range(3):
        measure(command, profile_env)

    def binaries():
        paths = list(cache.glob("stage/v3/*/*/program"))
        if output is not None:
            paths.append(output)
        return {
            str(path): (path.stat().st_dev, path.stat().st_ino, path.stat().st_mtime_ns)
            for path in paths
        }

    before = binaries()
    probe.write_text("")
    status.write_text("")
    measure(command, profile_env)
    commands = probe.read_text().splitlines()
    return {
        "unchanged_inode_and_mtime": bool(before) and before == binaries(),
        "warm_go_commands": commands,
        "warm_go_command_count": len(commands),
        "compiler_cache_status": status.read_text().splitlines(),
        "warm_phases_ms": json.loads(phases.read_text()) if phases.exists() else {},
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--bork", required=True)
    parser.add_argument("--samples", type=int, default=5)
    parser.add_argument("--probe-bork", help="CLI built with the test cache gate")
    args = parser.parse_args()
    bork = str(Path(args.bork).resolve())
    results = {}
    with tempfile.TemporaryDirectory(prefix="bork-macos-") as work:
        root = Path(work)
        env = dict(os.environ, BORKCACHE=str(root / "cache"), BORKTOOLCHAIN="local")
        script = root / "hello.bork"
        script.write_text('#!/usr/bin/env -S bork script\nprintln("Hello, world!")\n')
        script_deps = root / "deps.bork"
        script_deps.write_text(
            '#!/usr/bin/env -S bork script\n'
            '// bork:require github.com/GiGurra/boa v1.0.31\n'
            '// bork:unsafe\n'
            'fn answer():Int unsafe go {\n'
            'import "github.com/GiGurra/boa/pkg/boa"\n'
            'var _ boa.NoParams\nreturn 42\n}\n'
            'println(answer())\n'
        )
        for name, path in (("hello", script), ("inline-deps", script_deps)):
            shutil.rmtree(root / "cache", ignore_errors=True)
            command = [bork, "script", str(path)]
            cold = measure(command, env)
            for _ in range(3):
                measure(command, env)
            warm = [measure(command, env) for _ in range(args.samples)]
            results[name + "/script"] = {
                "cold_ms": cold,
                "warm_ms": warm,
                "warm_median_ms": statistics.median(warm),
            }
            if args.probe_bork:
                results[name + "/script"]["diagnostics"] = diagnose(
                    command, args.probe_bork, root, env
                )
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
                if args.probe_bork:
                    results[program + "/" + operation]["diagnostics"] = diagnose(
                        command, args.probe_bork, root, env
                    )
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
