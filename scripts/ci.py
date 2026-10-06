#!/usr/bin/env python3
"""Discover, balance, run and measure complete CI test shards."""
import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time

MODULE = "github.com/GiGurra/bork"
DRIVER = MODULE + "/internal/driver"
LSP = MODULE + "/internal/lsp"
CLI = MODULE + "/cmd/bork"
# Packages too slow to run whole in one shard are split by top-level test.
SPLIT = (DRIVER, LSP, CLI)
# Parents whose immediate children are one fixture directory each. They are
# split per fixture, discovered with the parent's own directory rules.
FIXTURES = {DRIVER: {"TestCases": Path("testdata/cases"), "TestExamples": Path("examples")}}
SHARDS = 10
# Estimated cost of starting a split package's test binary in another shard,
# so small tests cluster instead of every shard paying every package's setup.
PACKAGE_OVERHEAD = 5.0
DEFAULT_SECONDS = 5.0
TIMINGS = Path(__file__).with_name("ci-timings.json")
WARN_SECONDS = 240
FAIL_SECONDS = 300
GO_TEST = ["go", "test", "-race", "-count=1"]


def stop(process):
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass


def kill(process):
    # The leader may exit before a descendant that ignores SIGTERM.
    # Always escalate the group, even if the leader already returned.
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def execute(command, deadline=None, capture=False):
    process = subprocess.Popen(command, start_new_session=True, text=True,
                               stdout=subprocess.PIPE if capture else None)
    try:
        remaining = None if deadline is None else max(0, deadline - time.monotonic())
        stdout, _ = process.communicate(timeout=remaining)
    except subprocess.TimeoutExpired:
        stop(process)
        try:
            process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            pass
        kill(process)
        raise
    if process.returncode:
        if stdout:
            print(stdout, file=sys.stderr)
        raise subprocess.CalledProcessError(process.returncode, command)
    return stdout


def execute_all(commands, outputs, deadline):
    """Run commands concurrently, each writing stdout to its output file.

    Returns the exit codes, or raises TimeoutExpired after terminating every
    process group once the shared deadline passes."""
    processes = []
    try:
        for command, path in zip(commands, outputs):
            with open(path, "w") as stdout:
                processes.append(subprocess.Popen(command, start_new_session=True, stdout=stdout))
        for process in processes:
            process.wait(timeout=max(0, deadline - time.monotonic()))
    except subprocess.TimeoutExpired:
        for process in processes:
            stop(process)
        grace = time.monotonic() + 5
        for process in processes:
            try:
                process.wait(timeout=max(0, grace - time.monotonic()))
            except subprocess.TimeoutExpired:
                pass
        raise
    finally:
        for process in processes:
            kill(process)
    return [process.returncode for process in processes]


def output(command, deadline=None):
    return execute(command, deadline, capture=True).splitlines()


def fixture_tests(parent, root):
    # Match the parent's os.ReadDir selection, including ignoring symlinks and
    # hidden entries. Each directory is exactly one immediate t.Run child.
    names = [f"{parent}/{entry.name}" for entry in os.scandir(root)
             if entry.is_dir(follow_symlinks=False) and not entry.name.startswith(".")]
    if not names:
        raise RuntimeError(f"{parent} fixture discovery was empty")
    if any(not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", name.split("/", 1)[1]) for name in names):
        # testing.T.Run sanitizes whitespace/control characters and can then
        # deduplicate collisions with #NN suffixes. Reject such names instead
        # of generating a pattern that might silently skip a fixture.
        raise RuntimeError("fixture names must use ASCII letters, digits, underscores, dots or hyphens")
    return sorted(names)


def discover(deadline=None):
    """Return the whole packages and the {package: [test]} split tests."""
    packages = output(["go", "list", "-race", "./..."], deadline)
    if DRIVER not in packages or len(packages) != len(set(packages)):
        raise RuntimeError("package discovery was empty or ambiguous")
    split = [package for package in SPLIT if package in packages]
    # Each listing links a race test binary; link them concurrently.
    with ThreadPoolExecutor(len(split)) as pool:
        listings = pool.map(lambda package: output(["go", "test", "-race", package, "-list", "^(Test|Example|Fuzz)"], deadline), split)
    tests = {}
    for package, listing in zip(split, listings):
        names = [line for line in listing if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)]
        if not names or len(names) != len(set(names)):
            raise RuntimeError(f"{package} test discovery was empty or ambiguous")
        fixtures = FIXTURES.get(package, {})
        if not set(fixtures).issubset(names):
            raise RuntimeError(f"{package} fixture parents are missing")
        tests[package] = [name for name in names if name not in fixtures]
        for parent, root in fixtures.items():
            tests[package] += fixture_tests(parent, root)
    return [package for package in packages if package not in SPLIT], tests


def partition(packages, tests, weights, shards=SHARDS):
    """Balance whole packages and split tests over shards, longest first."""
    units = [(None, package) for package in packages]
    units += [(package, name) for package in sorted(tests) for name in tests[package]]
    if len(units) != len(set(units)) or set(packages) & set(tests):
        raise RuntimeError("discovered packages or tests are ambiguous")

    # Unknown names still run, with a conservative cost, and spread across
    # shards until the next measurement records their real duration.
    def cost(unit):
        package, name = unit
        known = weights["packages"].get(name) if package is None else weights["tests"].get(package, {}).get(name)
        return max(0.01, DEFAULT_SECONDS if known is None else known)

    groups = [{"packages": [], "tests": {}} for _ in range(shards)]
    totals = [0.0] * shards
    for unit in sorted(units, key=lambda unit: (-cost(unit), unit[0] or "", unit[1])):
        package, name = unit

        def load(index):
            started = package is None or package in groups[index]["tests"]
            return totals[index] + cost(unit) + (0 if started else PACKAGE_OVERHEAD)
        index = min(range(shards), key=lambda index: (load(index), index))
        totals[index] = load(index)
        if package is None:
            groups[index]["packages"].append(name)
        else:
            groups[index]["tests"].setdefault(package, []).append(name)
    covered = [(None, name) for group in groups for name in group["packages"]]
    covered += [(package, name) for group in groups for package, names in group["tests"].items() for name in names]
    if len(covered) != len(set(covered)) or set(covered) != set(units):
        raise RuntimeError("partition is not disjoint and complete")
    for group, total in zip(groups, totals):
        group["packages"].sort()
        group["tests"] = {package: sorted(names) for package, names in sorted(group["tests"].items())}
        group["weight"] = round(total, 2)
    return groups


def run_pattern(names):
    """One -run expression for top-level tests and fixture children.

    Go splits -run at top-level '|' into alternatives. A plain alternative
    keeps every subtest of its parents, while a fixture alternative selects
    only the listed children."""
    plain = [name for name in names if "/" not in name]
    children = {}
    for name in names:
        if "/" in name:
            parent, child = name.split("/", 1)
            children.setdefault(parent, []).append(child)
    alternatives = []
    if plain:
        alternatives.append("^(" + "|".join(re.escape(name) for name in plain) + ")$")
    for parent, names in sorted(children.items()):
        alternatives.append(f"^{re.escape(parent)}$/^(" + "|".join(re.escape(name) for name in names) + ")$")
    return "|".join(alternatives)


def commands(group):
    result = []
    if group["packages"]:
        result.append([*GO_TEST, "-json", *group["packages"]])
    for package, names in group["tests"].items():
        result.append([*GO_TEST, "-json", package, "-run", run_pattern(names)])
    return result


def summarize(paths):
    """Print package results, failing test output, build output and any
    test still running, from go test -json event files. Returns the
    (package, test) keys that finished, with "" as a package's own test."""
    status = {}
    lines = {}
    running = set()
    for path in paths:
        try:
            text = Path(path).read_text()
        except OSError:
            continue
        for line in text.splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                print(line)
                continue
            action = event.get("Action")
            if action in ("build-output", "build-fail"):
                print(event.get("Output", ""), end="")
                continue
            key = (event.get("Package", ""), event.get("Test", ""))
            if action == "output":
                lines.setdefault(key, []).append(event.get("Output", ""))
            elif action == "run":
                running.add(key)
            elif action in ("pass", "fail", "skip"):
                running.discard(key)
                status[key] = action
                if not key[1]:
                    print(f"{'ok  ' if action == 'pass' else action.upper()} {key[0]} {event.get('Elapsed', 0):.2f}s")
    for key, action in sorted(status.items()):
        if action == "fail":
            print("".join(lines.get(key, [])), end="")
    for package, test in sorted(running):
        print(f"::error::still running at the deadline: {package} {test}")
    return set(status)


def unrun(group, finished):
    """Selected units without a pass, fail or skip event. Go exits 0 when a
    -run pattern matches nothing, so a fixture whose subtest name drifted
    from its directory would otherwise vanish silently."""
    units = [(package, "") for package in group["packages"]]
    units += [(package, name) for package, names in group["tests"].items() for name in names]
    return [unit for unit in units if unit not in finished]


def budget_result(elapsed, status):
    # Only the test phase's own deadline fails a shard. A cold build cache
    # (a new go.sum or Go version) can push discovery's compilation past
    # the budget once without anything being wrong.
    print(f"Shard wall time: {elapsed:.2f}s", flush=True)
    if elapsed > FAIL_SECONDS:
        print(f"::warning::Shard exceeded the {FAIL_SECONDS}s budget; check for a cold build cache, "
              "then refresh timings or add shards.", flush=True)
    elif elapsed > WARN_SECONDS:
        print(f"::warning::Shard exceeded {WARN_SECONDS}s; refresh timings or add shards.", flush=True)
    return status


def run_shard(args):
    started = time.monotonic()
    tested = None
    group = {}
    status = 0
    events = args.events or Path(tempfile.mkdtemp(prefix="bork-ci-"))
    paths = []
    try:
        events.mkdir(parents=True, exist_ok=True)
        # Listing tests links the split packages' race test binaries, so this
        # deadline also bounds most compilation.
        packages, tests = discover(started + FAIL_SECONDS)
        group = partition(packages, tests, read_weights(args.timings))[args.shard]
        selected = commands(group)
        if not selected:
            raise RuntimeError("selected shard is empty")
        tested = time.monotonic()
        print(f"shard {args.shard}: {len(group['packages'])} packages, "
              f"{sum(map(len, group['tests'].values()))} split tests, weight {group['weight']}s, "
              f"discovered in {tested - started:.2f}s", flush=True)
        paths = [events / f"shard-{args.shard}-{index}.jsonl" for index in range(len(selected))]
        if any(execute_all(selected, paths, tested + FAIL_SECONDS)):
            status = 1
    except subprocess.TimeoutExpired:
        phase = "Discovery" if tested is None else "Test run"
        print(f"::error::{phase} reached the {FAIL_SECONDS}s wall-time limit.", flush=True)
        status = 1
    except (RuntimeError, subprocess.CalledProcessError, OSError, ValueError, KeyError) as error:
        print(f"::error::{error}", file=sys.stderr)
        status = 1
    finished = summarize(paths)
    if not status and group:
        for package, name in unrun(group, finished):
            print(f"::error::selected but never ran: {package} {name}".rstrip(), flush=True)
            status = 1
    elapsed = time.monotonic() - started
    result = budget_result(elapsed, status)
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps({"shard": args.shard, "seconds": round(elapsed, 2),
                                          "test_seconds": None if tested is None else round(time.monotonic() - tested, 2),
                                          "result": result, "selected": group}, indent=2) + "\n")
    return result


def read_weights(path):
    data = json.loads(path.read_text())
    if data.get("version") != 2:
        raise RuntimeError("unsupported CI timing file version")
    costs = [*data["packages"].values(), *(cost for tests in data["tests"].values() for cost in tests.values())]
    if any(not isinstance(cost, (int, float)) or not math.isfinite(cost) or cost < 0 for cost in costs):
        raise RuntimeError("CI timing weights must be finite nonnegative seconds")
    return data


def measurements(paths):
    """Merge the go test -json files of one complete run, local or sharded."""
    tests = {}
    packages = {}
    for path in paths:
        for line in Path(path).read_text().splitlines():
            event = json.loads(line)
            if event["Action"] in ("fail", "build-fail"):
                raise RuntimeError("refusing timings from a failed test run")
            if event["Action"] not in ("pass", "skip") or "Package" not in event:
                continue
            package = event["Package"]
            if "Test" in event:
                # Fixture parents run in many shards; their children are the units.
                if package in SPLIT and event["Test"] not in FIXTURES.get(package, {}):
                    key = (package, event["Test"])
                    if key in tests:
                        raise RuntimeError("timings must come from one -count=1 run")
                    tests[key] = event.get("Elapsed", 0.0)
            elif package not in SPLIT:
                if package in packages:
                    raise RuntimeError("timings contain duplicate package results")
                packages[package] = event.get("Elapsed", 0.0)
    if not any(package == DRIVER for package, _ in tests):
        raise RuntimeError("timing input has no completed driver tests")
    # A serial parent's Elapsed contains child work, while a parallel parent
    # can return before its children execute. max(parent, sum(children))
    # retains that work without counting nested serial time twice.
    costs = dict(tests)
    for key in sorted(tests, key=lambda key: (-key[1].count("/"), key)):
        children = [child for child in tests if child[0] == key[0] and child[1].rpartition("/")[0] == key[1]]
        costs[key] = max(tests[key], sum(costs[child] for child in children))
    result = {}
    for (package, name), cost in sorted(costs.items()):
        parent = name.split("/", 1)[0]
        if "/" not in name or (parent in FIXTURES.get(package, {}) and name.count("/") == 1):
            result.setdefault(package, {})[name] = round(cost, 2)
    return {"version": 2, "packages": {name: round(cost, 2) for name, cost in sorted(packages.items())}, "tests": result}


def refresh(args):
    measurement = measurements(args.input)
    packages, tests = discover()
    measured = {(package, name) for package, names in measurement["tests"].items() for name in names}
    discovered = {(package, name) for package, names in tests.items() for name in names}
    if set(measurement["packages"]) != set(packages) or measured != discovered:
        raise RuntimeError("timing input must cover every current package and split test")
    args.timings.write_text(json.dumps(measurement, indent=2, sort_keys=True) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--timings", type=Path, default=TIMINGS)
    subcommands = parser.add_subparsers(dest="command", required=True)
    subcommands.add_parser("plan", help="print every shard's packages and tests")
    run = subcommands.add_parser("run", help="run one shard")
    run.add_argument("shard", type=int, choices=range(SHARDS))
    run.add_argument("--report", type=Path)
    run.add_argument("--events", type=Path, help="directory for the go test -json event files")
    update = subcommands.add_parser("refresh", help="rewrite timings from go test -json files")
    update.add_argument("--input", type=Path, nargs="+", required=True)
    args = parser.parse_args()
    if args.command == "run":
        return run_shard(args)
    if args.command == "refresh":
        refresh(args)
        return 0
    packages, tests = discover()
    print(json.dumps(partition(packages, tests, read_weights(args.timings)), indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (RuntimeError, subprocess.CalledProcessError) as error:
        print(f"::error::{error}", file=sys.stderr)
        sys.exit(1)
