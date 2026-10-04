#!/usr/bin/env python3
"""Run one disjoint CI race group, discovering integration tests at runtime."""
import argparse
import hashlib
import re
import subprocess

DRIVER = "github.com/GiGurra/bork/internal/driver"
DEDICATED = {"driver-cases": "TestCases", "driver-examples": "TestExamples"}
GROUPS = ("core", *DEDICATED, "driver-integration-0", "driver-integration-1")


def output(*command):
    return subprocess.check_output(command, text=True).splitlines()


def driver_tests():
    names = [line for line in output("go", "test", "-race", "./internal/driver", "-list", "^(Test|Example|Fuzz)")
             if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line)]
    if not names or len(names) != len(set(names)):
        raise RuntimeError("driver test discovery was empty or ambiguous")
    if not set(DEDICATED.values()).issubset(names):
        raise RuntimeError("dedicated driver test parents are missing")
    return names


def selected(group, names):
    if group in DEDICATED:
        return [DEDICATED[group]]
    shard = int(group.rsplit("-", 1)[1])
    return [name for name in names if name not in DEDICATED.values()
            and hashlib.sha256(name.encode()).digest()[0] % 2 == shard]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("group", choices=GROUPS)
    parser.add_argument("--list", action="store_true", help="show coverage without running tests")
    args = parser.parse_args()
    if args.group == "core":
        packages = [name for name in output("go", "list", "-race", "./...") if name != DRIVER]
        if not packages:
            raise RuntimeError("core package discovery was empty")
        command = ["go", "test", "-race", "-count=1", *packages]
        coverage = packages
    else:
        names = selected(args.group, driver_tests())
        if not names:
            raise RuntimeError("selected driver shard was empty")
        expression = "^(" + "|".join(re.escape(name) for name in names) + ")$"
        command = ["go", "test", "-race", "./internal/driver", "-count=1", "-run", expression]
        coverage = names
    print(f"{args.group}: {len(coverage)} selected", flush=True)
    if args.list:
        print("\n".join(coverage))
        return
    subprocess.run(command, check=True)


if __name__ == "__main__":
    main()
