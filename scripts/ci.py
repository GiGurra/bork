#!/usr/bin/env python3
"""Discover, balance, run and measure complete CI test shards."""
import argparse
import json
import math
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time

DRIVER = "github.com/GiGurra/bork/internal/driver"
LSP = "github.com/GiGurra/bork/internal/lsp"
LSP_SHARDS = ("lsp-0", "lsp-1", "lsp-2")
PACKAGE_SHARDS = {"cli": "github.com/GiGurra/bork/cmd/bork"}
DEDICATED = {"driver-examples": "TestExamples"}
CASE_SHARDS = 3
INTEGRATION_SHARDS = 6
SHARDS = ("core", *PACKAGE_SHARDS, *LSP_SHARDS, *(f"driver-cases-{i}" for i in range(CASE_SHARDS)), *DEDICATED,
          *(f"driver-integration-{i}" for i in range(INTEGRATION_SHARDS)))
TIMINGS = Path(__file__).with_name("ci-timings.json")
WARN_SECONDS = 120
FAIL_SECONDS = 180


def go_flags(mode):
    return ["-race"] if mode == "race" else []


def execute(command, deadline=None, capture=False):
    process = subprocess.Popen(command, start_new_session=True, text=True,
                               stdout=subprocess.PIPE if capture else None)
    try:
        remaining = None if deadline is None else max(0, deadline - time.monotonic())
        stdout, _ = process.communicate(timeout=remaining)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            process.communicate(timeout=5)
        except subprocess.TimeoutExpired:
            pass
        # The leader may exit before a descendant that ignores SIGTERM.
        # Always escalate the group, even if communicate already returned.
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.communicate()
        raise
    if process.returncode:
        if stdout:
            print(stdout, file=sys.stderr)
        raise subprocess.CalledProcessError(process.returncode, command)
    return stdout


def output(command, deadline=None):
    return execute(command, deadline, capture=True).splitlines()


def case_tests(root=Path("testdata/cases")):
    # Match TestCases' os.ReadDir selection, including ignoring symlinks and
    # hidden entries. Each directory is exactly one immediate t.Run child.
    names = [f"TestCases/{entry.name}" for entry in os.scandir(root)
             if entry.is_dir(follow_symlinks=False) and not entry.name.startswith(".")]
    if not names:
        raise RuntimeError("golden case discovery was empty")
    if any(not re.fullmatch(r"TestCases/[A-Za-z0-9_][A-Za-z0-9_.-]*", name) for name in names):
        # testing.T.Run sanitizes whitespace/control characters and can then
        # deduplicate collisions with #NN suffixes. Reject such names instead
        # of generating a pattern that might silently skip a fixture.
        raise RuntimeError("golden fixture names must use ASCII letters, digits, underscores, dots or hyphens")
    return sorted(names)


def discover(mode, deadline=None):
    flags = go_flags(mode)
    packages = output(["go", "list", *flags, "./..."], deadline)
    if DRIVER not in packages or len(packages) != len(set(packages)):
        raise RuntimeError("package discovery was empty or ambiguous")
    names = [line for line in output(["go", "test", *flags, "./internal/driver", "-list", "^(Test|Example|Fuzz)"], deadline)
             if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)]
    if not names or len(names) != len(set(names)):
        raise RuntimeError("driver test discovery was empty or ambiguous")
    if not {*DEDICATED.values(), "TestCases"}.issubset(names):
        raise RuntimeError("dedicated driver test parents are missing")
    return packages, [name for name in names if name != "TestCases"] + case_tests()


def discover_lsp(mode, deadline=None):
    names = [line for line in output(["go", "test", *go_flags(mode), "./internal/lsp", "-list", "^(Test|Example|Fuzz)"], deadline)
             if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)]
    if not names or len(names) != len(set(names)):
        raise RuntimeError("LSP test discovery was empty or ambiguous")
    return names


def partition(packages, names, weights, lsp_names=()):
    groups = {"core": [package for package in packages if package not in (DRIVER, LSP) and package not in PACKAGE_SHARDS.values()]}
    groups.update({group: [package] for group, package in PACKAGE_SHARDS.items() if package in packages})
    covered_packages = [package for group, entries in groups.items() for package in entries]
    if len(packages) != len(set(packages)) or len(covered_packages) != len(set(covered_packages)) or set(covered_packages) != set(packages) - {DRIVER, LSP}:
        raise RuntimeError("package partition is not disjoint and complete")
    if LSP in packages:
        if not lsp_names or len(lsp_names) != len(set(lsp_names)):
            raise RuntimeError("LSP partition requires unique discovered tests")
        groups.update({group: [] for group in LSP_SHARDS})
        totals = dict.fromkeys(LSP_SHARDS, 0.0)
        for name in sorted(lsp_names, key=lambda name: (-max(0.01, weights.get("lsp:" + name, 5.0)), name)):
            group = min(LSP_SHARDS, key=lambda group: (totals[group], group))
            groups[group].append(name)
            totals[group] += max(0.01, weights.get("lsp:" + name, 5.0))
        covered = [name for group in LSP_SHARDS for name in groups[group]]
        if len(covered) != len(set(covered)) or set(covered) != set(lsp_names):
            raise RuntimeError("LSP partition is not disjoint and complete")
    groups.update({group: [name] for group, name in DEDICATED.items()})
    integration = [group for group in SHARDS if group.startswith("driver-integration-")]
    cases = [group for group in SHARDS if group.startswith("driver-cases-")]
    groups.update({group: [] for group in [*integration, *cases]})
    totals = dict.fromkeys([*integration, *cases], 0.0)
    # Unknown names still run, with a conservative cost, and spread across
    # shards until the next measurement records their real duration.
    def cost(name):
        return max(0.01, weights.get(name, 5.0))
    remaining = set(names) - set(DEDICATED.values())
    for name in sorted(remaining, key=lambda name: (-cost(name), name)):
        targets = cases if name.startswith("TestCases/") else integration
        group = min(targets, key=lambda group: (totals[group], group))
        groups[group].append(name)
        totals[group] += cost(name)
    covered = [name for group, entries in groups.items() if group != "core" and group not in PACKAGE_SHARDS and group not in LSP_SHARDS for name in entries]
    if len(covered) != len(set(covered)) or set(covered) != set(names):
        raise RuntimeError("driver partition is not disjoint and complete")
    return {group: sorted(entries) for group, entries in groups.items()}


def read_weights(mode, path):
    data = json.loads(path.read_text())
    if data.get("version") != 1:
        raise RuntimeError("unsupported CI timing file version")
    weights = data[mode]["driver"] | data[mode]["cases"] | {"lsp:" + name: cost for name, cost in data[mode].get("lsp", {}).items()}
    if any(not isinstance(cost, (int, float)) or not math.isfinite(cost) or cost < 0 for cost in weights.values()):
        raise RuntimeError("CI timing weights must be finite nonnegative seconds")
    return weights


def command(mode, shard, entries):
    result = ["go", "test", *go_flags(mode), "-count=1"]
    if shard == "core" or shard in PACKAGE_SHARDS:
        return [*result, *entries]
    if shard.startswith("driver-cases-"):
        expression = "^TestCases$/^(" + "|".join(re.escape(name.split("/", 1)[1]) for name in entries) + ")$"
        return [*result, "./internal/driver", "-run", expression]
    expression = "^(" + "|".join(re.escape(name) for name in entries) + ")$"
    return [*result, "./internal/lsp" if shard in LSP_SHARDS else "./internal/driver", "-run", expression]


def budget_result(elapsed, status):
    print(f"Shard wall time: {elapsed:.2f}s", flush=True)
    if elapsed > FAIL_SECONDS:
        print(f"::error::Shard exceeded {FAIL_SECONDS}s; refresh timings and add shards.", flush=True)
        return status or 1
    if elapsed > WARN_SECONDS:
        print(f"::warning::Shard exceeded {WARN_SECONDS}s; refresh timings or add shards.", flush=True)
    return status


def run_shard(args):
    started = time.monotonic()
    deadline = started + FAIL_SECONDS
    entries = []
    status = 0
    try:
        packages, names = discover(args.mode, deadline)
        groups = partition(packages, names, read_weights(args.mode, args.timings), discover_lsp(args.mode, deadline))
        entries = groups[args.shard]
        if not entries:
            raise RuntimeError("selected shard is empty")
        print(f"{args.mode}/{args.shard}: {len(entries)} selected", flush=True)
        execute(command(args.mode, args.shard, entries), deadline)
    except subprocess.TimeoutExpired:
        print(f"::error::Shard reached {FAIL_SECONDS}s wall-time limit.", flush=True)
        status = 1
    except (RuntimeError, subprocess.CalledProcessError, OSError, ValueError, KeyError) as error:
        print(f"::error::{error}", file=sys.stderr)
        status = 1
    elapsed = time.monotonic() - started
    result = budget_result(elapsed, status)
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps({"mode": args.mode, "shard": args.shard, "seconds": round(elapsed, 2),
                                          "result": result, "selected": entries}, indent=2) + "\n")
    return result


def measurements(path):
    tests = {}
    lsp_tests = {}
    packages = {}
    for line in path.read_text().splitlines():
        event = json.loads(line)
        if event["Action"] == "fail":
            raise RuntimeError("refusing timings from a failed test run")
        if event["Action"] not in ("pass", "skip"):
            continue
        package = event["Package"]
        if "Test" in event:
            if package in (DRIVER, LSP):
                target = tests if package == DRIVER else lsp_tests
                name = event["Test"]
                if name in target:
                    raise RuntimeError("timings must come from one -count=1 run")
                target[name] = event.get("Elapsed", 0.0)
        else:
            if package in packages:
                raise RuntimeError("timings contain duplicate package results")
            packages[package] = event.get("Elapsed", 0.0)
    if DRIVER not in packages or not tests:
        raise RuntimeError("timing input has no completed driver package")
    # A serial parent's Elapsed contains child work, while a parallel parent
    # can return before its children execute. max(parent, sum(children))
    # retains that work without counting nested serial time twice.
    def parent_costs(tests):
        costs = dict(tests)
        for name in sorted(tests, key=lambda name: (-name.count("/"), name)):
            children = [child for child in tests if child.rpartition("/")[0] == name]
            costs[name] = max(tests[name], sum(costs[child] for child in children))
        return costs
    costs = parent_costs(tests)
    lsp_costs = parent_costs(lsp_tests)
    return {"packages": dict(sorted(packages.items())),
            "lsp": {name: round(lsp_costs[name], 2) for name in sorted(lsp_costs) if "/" not in name},
            "cases": {name: round(costs[name], 2) for name in sorted(costs)
                      if name.startswith("TestCases/") and name.count("/") == 1},
            "driver": {name: round(costs[name], 2) for name in sorted(costs) if "/" not in name}}


def refresh(args):
    measurement = measurements(args.input)
    packages, names = discover(args.mode)
    measured = (set(measurement["driver"]) - {"TestCases"}) | set(measurement["cases"])
    if set(measurement["packages"]) != set(packages) or measured != set(names) or set(measurement["lsp"]) != set(discover_lsp(args.mode)):
        raise RuntimeError("timing input must cover every current package and driver parent")
    data = json.loads(args.timings.read_text()) if args.timings.exists() else {"version": 1}
    data[args.mode] = measurement
    args.timings.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--timings", type=Path, default=TIMINGS)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("matrix", help="print the shared workflow shard list")
    for name in ("run", "plan", "refresh"):
        sub = commands.add_parser(name)
        sub.add_argument("--mode", choices=("normal", "race"), required=True)
        if name == "run":
            sub.add_argument("shard", choices=SHARDS)
            sub.add_argument("--report", type=Path)
        if name == "refresh":
            sub.add_argument("--input", type=Path, required=True)
    args = parser.parse_args()
    if args.command == "matrix":
        print(json.dumps(SHARDS))
        return 0
    if args.command == "run":
        return run_shard(args)
    if args.command == "refresh":
        refresh(args)
        return 0
    packages, names = discover(args.mode)
    print(json.dumps(partition(packages, names, read_weights(args.mode, args.timings), discover_lsp(args.mode)), indent=2))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (RuntimeError, subprocess.CalledProcessError) as error:
        print(f"::error::{error}", file=sys.stderr)
        sys.exit(1)
