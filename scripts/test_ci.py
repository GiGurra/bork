"""Coverage and timing regressions for the CI runner (Go only for the -run check)."""
import json
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import ci

NO_WEIGHTS = {"packages": {}, "tests": {}}


def covered(groups):
    units = [(None, name) for group in groups for name in group["packages"]]
    return units + [(package, name) for group in groups for package, names in group["tests"].items() for name in names]


class PartitionTests(unittest.TestCase):
    def test_exact_partition_including_new_packages_tests_and_fixtures(self):
        packages = ["example/core", "example/new-package"]
        tests = {ci.DRIVER: ["TestCases/future-case", "TestExamples/new", "TestFuture", "ExampleFuture", "FuzzFuture",
                             *(f"TestIntegration{i}" for i in range(30))],
                 ci.LSP: ["TestHeavy", "TestOther"], ci.CLI: ["TestCLI"]}
        weights = {"packages": {"example/core": 40}, "tests": {ci.DRIVER: {"TestIntegration0": 90}}}
        groups = ci.partition(packages, tests, weights)
        self.assertEqual(len(groups), ci.SHARDS)
        units = covered(groups)
        self.assertCountEqual(units, [(None, name) for name in packages] +
                              [(package, name) for package, names in tests.items() for name in names])
        self.assertEqual(len(units), len(set(units)))
        reordered = {package: list(reversed(names)) for package, names in reversed(list(tests.items()))}
        self.assertEqual(groups, ci.partition(list(reversed(packages)), reordered, weights))

    def test_ambiguous_units_fail_closed(self):
        with self.assertRaises(RuntimeError):
            ci.partition(["example/core"], {ci.DRIVER: ["TestOne", "TestOne"]}, NO_WEIGHTS)
        with self.assertRaises(RuntimeError):
            ci.partition([ci.DRIVER], {ci.DRIVER: ["TestOne"]}, NO_WEIGHTS)

    def test_longest_units_land_in_different_shards(self):
        heavy = [f"TestHeavy{i}" for i in range(ci.SHARDS)]
        groups = ci.partition([], {ci.DRIVER: heavy}, {"packages": {}, "tests": {ci.DRIVER: dict.fromkeys(heavy, 60)}})
        self.assertTrue(all(len(group["tests"][ci.DRIVER]) == 1 for group in groups))

    def test_starting_a_split_package_costs_overhead_so_small_tests_cluster(self):
        weights = {"packages": {"example/heavy": 3}, "tests": {ci.LSP: {"TestA": 1, "TestB": 1}}}
        groups = ci.partition(["example/heavy"], {ci.LSP: ["TestA", "TestB"]}, weights, shards=2)
        self.assertEqual(groups[0]["packages"], ["example/heavy"])
        self.assertEqual(groups[1]["tests"], {ci.LSP: ["TestA", "TestB"]})
        self.assertEqual(groups[1]["weight"], 2 + ci.PACKAGE_OVERHEAD)

    def test_run_pattern_keeps_plain_subtests_and_selects_fixture_children(self):
        pattern = ci.run_pattern(["TestOne", "ExampleTwo", "TestCases/a.b", "TestCases/new-case", "TestExamples/x"])
        self.assertEqual(pattern, r"^(TestOne|ExampleTwo)$|^TestCases$/^(a\.b|new\-case)$|^TestExamples$/^(x)$")
        commands = ci.commands({"packages": ["example/core"], "tests": {ci.DRIVER: ["TestOne"]}})
        self.assertEqual(commands[0], [*ci.GO_TEST, "-json", "example/core"])
        self.assertEqual(commands[1][-3:], [ci.DRIVER, "-run", "^(TestOne)$"])
        self.assertTrue(all("-race" in command for command in commands))

    @unittest.skipUnless(shutil.which("go"), "needs the go command")
    def test_run_pattern_selects_exactly_the_units_under_go_test(self):
        source = """package p

import "testing"

func TestA(t *testing.T) { t.Run("y/z", func(t *testing.T) {}) }
func TestB(t *testing.T) {}
func TestCases(t *testing.T) {
	for _, name := range []string{"a.b", "new-case", "other"} {
		t.Run(name, func(t *testing.T) { t.Run("nested", func(t *testing.T) {}) })
	}
}
func Example() {
	// Output:
}
"""
        with tempfile.TemporaryDirectory() as directory:
            (Path(directory) / "go.mod").write_text("module p\n\ngo 1.21\n")
            (Path(directory) / "p_test.go").write_text(source)
            pattern = ci.run_pattern(["TestA", "Example", "TestCases/a.b", "TestCases/new-case"])
            result = subprocess.run(["go", "test", "-json", "-count=1", "-run", pattern, "."], cwd=directory,
                                    capture_output=True, text=True, check=True, env={**__import__("os").environ, "GOFLAGS": "", "GOWORK": "off"})
        ran = {json.loads(line).get("Test") for line in result.stdout.splitlines()
               if json.loads(line)["Action"] == "pass"} - {None}
        self.assertEqual(ran, {"TestA", "TestA/y/z", "Example", "TestCases", "TestCases/a.b", "TestCases/a.b/nested",
                               "TestCases/new-case", "TestCases/new-case/nested"})

    def test_workflow_matrix_matches_shard_count(self):
        workflow = (Path(__file__).parent.parent / ".github" / "workflows" / "ci.yml").read_text()
        shards = re.search(r"^\s+shard: \[([0-9, ]+)\]$", workflow, re.MULTILINE)
        self.assertIsNotNone(shards)
        self.assertEqual([int(shard) for shard in shards.group(1).split(",")], list(range(ci.SHARDS)))


class DiscoveryTests(unittest.TestCase):
    def test_discovery_splits_packages_and_fixture_parents(self):
        listing = {ci.DRIVER: ["TestCases", "TestExamples", "FuzzNew", "ok package"], ci.LSP: ["TestLSP"], ci.CLI: ["TestCLI"]}

        def output(command, deadline=None):
            if command[:2] == ["go", "list"]:
                return [ci.DRIVER, ci.LSP, ci.CLI, "example/core"]
            return listing[command[3]]
        with patch.object(ci, "output", side_effect=output) as calls, \
             patch.object(ci, "fixture_tests", side_effect=lambda parent, root: [f"{parent}/new"]):
            packages, tests = ci.discover()
        self.assertEqual(packages, ["example/core"])
        self.assertEqual(tests[ci.DRIVER], ["FuzzNew", "TestCases/new", "TestExamples/new"])
        self.assertEqual(tests[ci.LSP], ["TestLSP"])
        self.assertTrue(all("-race" in call.args[0] for call in calls.call_args_list))
        listing[ci.DRIVER] = ["TestUnrelated"]
        with patch.object(ci, "output", side_effect=output), self.assertRaises(RuntimeError):
            ci.discover()

    def test_fixture_discovery_matches_parent_directory_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "new-case").mkdir()
            (root / ".hidden").mkdir()
            (root / "file").touch()
            (root / "link").symlink_to(root / "new-case", target_is_directory=True)
            self.assertEqual(ci.fixture_tests("TestCases", root), ["TestCases/new-case"])
            (root / "new case").mkdir()
            with self.assertRaises(RuntimeError):
                ci.fixture_tests("TestCases", root)


class MeasurementTests(unittest.TestCase):
    def measure(self, *files):
        with tempfile.TemporaryDirectory() as directory:
            paths = []
            for index, events in enumerate(files):
                path = Path(directory) / f"{index}.jsonl"
                path.write_text("\n".join(json.dumps({"Package": ci.DRIVER, **event}) for event in events))
                paths.append(path)
            return ci.measurements(paths)

    def test_parallel_children_and_nested_serial_children(self):
        events = [{"Action": "pass", "Test": name, "Elapsed": seconds} for name, seconds in
                  [("TestParallel/a", 4), ("TestParallel/b/nested", 3), ("TestParallel/b", 3.5),
                   ("TestParallel", 0), ("TestSerial/a", 2), ("TestSerial", 5)]]
        result = self.measure([*events, {"Action": "pass", "Elapsed": 10}])
        self.assertEqual(result["tests"][ci.DRIVER], {"TestParallel": 7.5, "TestSerial": 5})
        self.assertEqual(result["packages"], {})

    def test_shard_files_merge_with_fixture_children_and_whole_packages(self):
        first = [{"Action": "pass", "Test": "TestCases/a", "Elapsed": 2}, {"Action": "pass", "Test": "TestCases", "Elapsed": 2},
                 {"Package": "example/core", "Action": "pass", "Elapsed": 3}]
        second = [{"Action": "pass", "Test": "TestCases/b/sub", "Elapsed": 1}, {"Action": "pass", "Test": "TestCases/b", "Elapsed": 4},
                  {"Action": "pass", "Test": "TestCases", "Elapsed": 4}, {"Action": "pass", "Elapsed": 4}]
        result = self.measure(first, second)
        self.assertEqual(result["tests"][ci.DRIVER], {"TestCases/a": 2, "TestCases/b": 4})
        self.assertEqual(result["packages"], {"example/core": 3})
        with self.assertRaises(RuntimeError):
            self.measure(first, first)

    def test_failed_truncated_or_repeated_measurements_are_rejected(self):
        for events in [
            [{"Action": "fail", "Test": "TestBad"}],
            [{"Action": "build-fail"}],
            [{"Action": "run", "Test": "TestIncomplete"}],
            [{"Action": "pass", "Test": "TestRepeated"}] * 2,
        ]:
            with self.subTest(events=events), self.assertRaises(RuntimeError):
                self.measure(events)


class RunTests(unittest.TestCase):
    def test_wall_budget_and_original_test_failure(self):
        with patch("builtins.print") as output:
            self.assertEqual(ci.budget_result(100, 0), 0)
            self.assertEqual(ci.budget_result(121, 0), 0)
            self.assertEqual(ci.budget_result(181, 0), 0)
            self.assertEqual(ci.budget_result(100, 7), 7)
        messages = " ".join(str(call) for call in output.call_args_list)
        self.assertIn("::warning::Shard exceeded the 180s budget", messages)

    def run_shard(self, commands, **patches):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            args = SimpleNamespace(shard=0, timings=Path("unused"), report=report, events=Path(directory) / "events")
            group = {"packages": ["example/core"], "tests": {}, "weight": 1}
            with patch.object(ci, "discover", return_value=(["example/core"], {})), \
                 patch.object(ci, "read_weights", return_value=NO_WEIGHTS), \
                 patch.object(ci, "partition", return_value=[group]), \
                 patch.object(ci, "commands", return_value=commands), \
                 patch("builtins.print") as output:
                for name, value in patches.items():
                    patch.object(ci, name, value).start()
                try:
                    status = ci.run_shard(args)
                finally:
                    patch.stopall()
            printed = "".join(str(call) for call in output.call_args_list)
            return status, json.loads(report.read_text()), printed

    def test_concurrent_commands_report_failures_and_events(self):
        events = [{"Action": "run", "Package": "p", "Test": "TestBad"},
                  {"Action": "output", "Package": "p", "Test": "TestBad", "Output": "boom\n"},
                  {"Action": "fail", "Package": "p", "Test": "TestBad"}, {"Action": "fail", "Package": "p", "Elapsed": 1}]
        script = "import json; [print(json.dumps(e)) for e in %r]; raise SystemExit(1)" % events
        status, report, printed = self.run_shard([[sys.executable, "-c", "print('{}')"], [sys.executable, "-c", script]])
        self.assertEqual(status, 1)
        self.assertEqual(report["result"], 1)
        self.assertIn("boom", printed)
        self.assertIn("FAIL p", printed)

    def test_selected_units_that_never_ran_fail_the_shard(self):
        events = [{"Action": "pass", "Package": "example/core", "Elapsed": 1}]
        script = "import json; [print(json.dumps(e)) for e in %r]" % events
        status, _, printed = self.run_shard([[sys.executable, "-c", script]])
        self.assertEqual(status, 0)
        group = {"packages": ["example/core"], "tests": {ci.DRIVER: ["TestCases/renamed"]}}
        self.assertEqual(ci.unrun(group, {("example/core", "")}), [(ci.DRIVER, "TestCases/renamed")])
        status, _, printed = self.run_shard([[sys.executable, "-c", "pass"]])
        self.assertEqual(status, 1)
        self.assertIn("selected but never ran: example/core", printed)

    def test_timeout_terminates_every_command_and_names_running_tests(self):
        hang = ("import json, time; print(json.dumps({'Action': 'run', 'Package': 'p', 'Test': 'TestHang'}), flush=True); "
                "time.sleep(10)")
        status, report, printed = self.run_shard([[sys.executable, "-c", hang]] * 2, FAIL_SECONDS=0.5)
        self.assertEqual(status, 1)
        self.assertLess(report["seconds"], 2)
        self.assertIn("still running at the deadline: p TestHang", printed)

    def test_discovery_timeout_shares_deadline_and_still_writes_report(self):
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            args = SimpleNamespace(shard=0, timings=Path("unused"), report=report, events=Path(directory))

            def slow_discovery(deadline):
                ci.output([sys.executable, "-c", "import time; time.sleep(10)"], deadline)
            with patch.object(ci, "discover", side_effect=slow_discovery), \
                 patch.object(ci, "FAIL_SECONDS", 0.1), patch("builtins.print"):
                self.assertEqual(ci.run_shard(args), 1)
            result = json.loads(report.read_text())
            self.assertEqual(result["result"], 1)
            self.assertEqual(result["selected"], {})
            self.assertLess(result["seconds"], 1)

    @unittest.skipUnless(Path("/proc").is_dir(), "Linux process-state check")
    def test_timeout_kills_descendant_even_after_leader_exits(self):
        import os
        import signal
        with tempfile.TemporaryDirectory() as directory:
            pid_file = Path(directory) / "child.pid"
            child = ("import os,signal,time; from pathlib import Path; "
                     "signal.signal(signal.SIGTERM,signal.SIG_IGN); "
                     f"Path({str(pid_file)!r}).write_text(str(os.getpid())); time.sleep(30)")
            parent = (f"import subprocess,time; subprocess.Popen([{sys.executable!r}, '-c', {child!r}], "
                      "stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL); time.sleep(30)")
            try:
                with self.assertRaises(subprocess.TimeoutExpired):
                    ci.execute_all([[sys.executable, "-c", parent]], [Path(directory) / "out"], time.monotonic() + 1)
                pid = int(pid_file.read_text())
                state = Path(f"/proc/{pid}/stat")
                deadline = time.monotonic() + 1
                while True:
                    try:
                        if state.read_text().split()[2] == "Z":
                            break
                    except FileNotFoundError:
                        break
                    self.assertLess(time.monotonic(), deadline, "descendant survived timeout")
                    time.sleep(0.01)
            finally:
                if pid_file.exists():
                    try:
                        os.kill(int(pid_file.read_text()), signal.SIGKILL)
                    except ProcessLookupError:
                        pass


if __name__ == "__main__":
    unittest.main()
