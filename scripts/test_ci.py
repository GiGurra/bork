"""Coverage and timing regressions for the CI runner (no Go subprocesses)."""
import json
from pathlib import Path
import tempfile
import time
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import ci


class PartitionTests(unittest.TestCase):
    def test_exact_partition_including_new_test_example_and_fuzz_parents(self):
        names = [*ci.DEDICATED.values(), "TestCases/future-case", "TestFuture", "ExampleFuture", "FuzzFuture",
                 *(f"TestIntegration{i}" for i in range(12))]
        packages = [ci.DRIVER, "example/core", "example/new-package"]
        groups = ci.partition(packages, names, {"TestIntegration0": 90})
        covered = [name for group, entries in groups.items() if group != "core" and group not in ci.PACKAGE_SHARDS and group not in ci.LSP_SHARDS for name in entries]
        self.assertCountEqual(covered, names)
        self.assertEqual(len(covered), len(set(covered)))
        self.assertEqual(groups["core"], ["example/core", "example/new-package"])
        self.assertEqual(groups, ci.partition(list(reversed(packages)), list(reversed(names)), {"TestIntegration0": 90}))

    def test_package_shards_take_their_package_out_of_core(self):
        cli = ci.PACKAGE_SHARDS["cli"]
        groups = ci.partition([ci.DRIVER, cli, "example/core"], list(ci.DEDICATED.values()), {})
        self.assertEqual(groups["cli"], [cli])
        self.assertEqual(groups["core"], ["example/core"])
        self.assertLessEqual(set(ci.SHARDS) - set(ci.LSP_SHARDS), set(groups))

    def test_lsp_tests_have_exact_balanced_coverage_in_both_modes(self):
        packages = [ci.DRIVER, ci.LSP, "example/core"]
        names = ["TestHeavy", "TestOther", "ExampleNew", "FuzzNew", "TestFuture"]
        weights = {"lsp:TestHeavy": 60, "lsp:TestOther": 55}
        groups = ci.partition(packages, list(ci.DEDICATED.values()), weights, names)
        covered = [name for group in ci.LSP_SHARDS for name in groups[group]]
        self.assertCountEqual(covered, names)
        self.assertEqual(len(covered), len(set(covered)))
        self.assertEqual(groups["core"], ["example/core"])
        self.assertEqual(groups, ci.partition(list(reversed(packages)), list(ci.DEDICATED.values()), weights, list(reversed(names))))
        self.assertNotEqual(next(g for g in ci.LSP_SHARDS if "TestHeavy" in groups[g]), next(g for g in ci.LSP_SHARDS if "TestOther" in groups[g]))
        for mode in ("normal", "race"):
            for shard in ci.LSP_SHARDS:
                command = ci.command(mode, shard, ["TestHeavy", "FuzzNew"])
                self.assertEqual(command[-3:], ["./internal/lsp", "-run", "^(TestHeavy|FuzzNew)$"])
                self.assertEqual("-race" in command, mode == "race")
        for tests in ([], ["TestDuplicate", "TestDuplicate"]):
            with self.assertRaises(RuntimeError):
                ci.partition(packages, list(ci.DEDICATED.values()), {}, tests)
        with self.assertRaises(RuntimeError):
            ci.partition([*packages, ci.LSP], list(ci.DEDICATED.values()), {}, names)

    def test_lsp_discovery_rejects_empty_and_duplicate_lists(self):
        with patch.object(ci, "output", return_value=["TestOne", "ExampleNew", "FuzzNew", "ok package"]) as output:
            self.assertEqual(ci.discover_lsp("race"), ["TestOne", "ExampleNew", "FuzzNew"])
            self.assertIn("-race", output.call_args.args[0])
        for names in ([], ["TestOne", "TestOne"]):
            with patch.object(ci, "output", return_value=names), self.assertRaises(RuntimeError):
                ci.discover_lsp("normal")

    def test_longest_tests_land_in_different_shards(self):
        heavy = [f"TestHeavy{i}" for i in range(ci.INTEGRATION_SHARDS)]
        groups = ci.partition([ci.DRIVER], [*ci.DEDICATED.values(), *heavy], dict.fromkeys(heavy, 60))
        for group, entries in groups.items():
            if group.startswith("driver-integration-"):
                self.assertEqual(len(entries), 1)

    def test_anchored_parent_pattern_keeps_subtests(self):
        command = ci.command("race", "driver-integration-0", ["TestOne", "ExampleTwo"])
        self.assertIn("-race", command)
        self.assertEqual(command[-1], "^(TestOne|ExampleTwo)$")
        self.assertNotIn("-race", ci.command("normal", "core", ["example/core"]))

    def test_race_discovery_and_invalid_discovery_fail_closed(self):
        with patch.object(ci, "case_tests", return_value=["TestCases/new-case"]), \
             patch.object(ci, "output", side_effect=[[ci.DRIVER], [*ci.DEDICATED.values(), "TestCases", "FuzzNew", "ok package"]]) as output:
            _, names = ci.discover("race")
            self.assertIn("FuzzNew", names)
            self.assertIn("TestCases/new-case", names)
            self.assertNotIn("TestCases", names)
            self.assertTrue(all("-race" in call.args[0] for call in output.call_args_list))
        with patch.object(ci, "output", side_effect=[[ci.DRIVER], ["TestUnrelated"]]):
            with self.assertRaises(RuntimeError):
                ci.discover("normal")

    def test_fixture_discovery_matches_golden_directory_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "new-case").mkdir()
            (root / ".hidden").mkdir()
            (root / "file").touch()
            (root / "link").symlink_to(root / "new-case", target_is_directory=True)
            self.assertEqual(ci.case_tests(root), ["TestCases/new-case"])
            (root / "new case").mkdir()
            with self.assertRaises(RuntimeError):
                ci.case_tests(root)
        command = ci.command("normal", "driver-cases-0", ["TestCases/a.b", "TestCases/new-case"])
        self.assertEqual(command[-1], r"^TestCases$/^(a\.b|new\-case)$")


class MeasurementTests(unittest.TestCase):
    def measure(self, events):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "tests.json"
            path.write_text("\n".join(json.dumps({"Package": ci.DRIVER, **event}) for event in events))
            return ci.measurements(path)

    def test_parallel_children_and_nested_serial_children(self):
        events = [{"Action": "pass", "Test": name, "Elapsed": seconds} for name, seconds in
                  [("TestParallel/a", 4), ("TestParallel/b/nested", 3), ("TestParallel/b", 3.5),
                   ("TestParallel", 0), ("TestSerial/a", 2), ("TestSerial", 5)]]
        result = self.measure([*events, {"Action": "pass", "Elapsed": 10}])
        self.assertEqual(result["driver"], {"TestParallel": 7.5, "TestSerial": 5})

    def test_lsp_measurements_include_parallel_subtests_and_reject_duplicates(self):
        events = [{"Action": "pass", "Test": "TestDriver", "Elapsed": 1},
                  {"Action": "pass", "Elapsed": 1},
                  {"Package": ci.LSP, "Action": "pass", "Test": "TestParallel/a", "Elapsed": 3},
                  {"Package": ci.LSP, "Action": "pass", "Test": "TestParallel/b", "Elapsed": 4},
                  {"Package": ci.LSP, "Action": "pass", "Test": "TestParallel", "Elapsed": 0},
                  {"Package": ci.LSP, "Action": "pass", "Elapsed": 5}]
        self.assertEqual(self.measure(events)["lsp"], {"TestParallel": 7})
        with self.assertRaises(RuntimeError):
            self.measure([*events, events[-2]])

    def test_failed_truncated_or_repeated_measurements_are_rejected(self):
        for events in [
            [{"Action": "fail", "Test": "TestBad"}],
            [{"Action": "pass", "Test": "TestIncomplete"}],
            [{"Action": "pass", "Test": "TestRepeated"}] * 2,
        ]:
            with self.subTest(events=events), self.assertRaises(RuntimeError):
                self.measure(events)

    def test_wall_budget_and_original_test_failure(self):
        with patch("builtins.print") as output:
            self.assertEqual(ci.budget_result(100, 0), 0)
            self.assertEqual(ci.budget_result(121, 0), 0)
            self.assertEqual(ci.budget_result(181, 0), 1)
            self.assertEqual(ci.budget_result(100, 7), 7)
        messages = " ".join(str(call) for call in output.call_args_list)
        self.assertIn("::warning::", messages)
        self.assertIn("::error::", messages)

    def test_timeout_terminates_test_command_and_writes_failed_report(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            args = SimpleNamespace(mode="normal", shard="core", timings=Path("unused"), report=report)
            with patch.object(ci, "discover", return_value=([ci.DRIVER, "example/core"], list(ci.DEDICATED.values()))), \
                 patch.object(ci, "read_weights", return_value={}), \
                 patch.object(ci, "discover_lsp", return_value=["TestOne"]), \
                 patch.object(ci, "command", return_value=[sys.executable, "-c", "import time; time.sleep(10)"]), \
                 patch.object(ci, "FAIL_SECONDS", 0.1), patch("builtins.print"):
                self.assertEqual(ci.run_shard(args), 1)
            result = json.loads(report.read_text())
            self.assertEqual(result["result"], 1)
            self.assertLess(result["seconds"], 1)

    def test_discovery_timeout_shares_deadline_and_still_writes_report(self):
        import sys
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            args = SimpleNamespace(mode="race", shard="core", timings=Path("unused"), report=report)
            def slow_discovery(mode, deadline):
                ci.output([sys.executable, "-c", "import time; time.sleep(10)"], deadline)
            with patch.object(ci, "discover", side_effect=slow_discovery), \
                 patch.object(ci, "FAIL_SECONDS", 0.1), patch("builtins.print"):
                self.assertEqual(ci.run_shard(args), 1)
            result = json.loads(report.read_text())
            self.assertEqual(result["result"], 1)
            self.assertEqual(result["selected"], [])
            self.assertLess(result["seconds"], 1)

    @unittest.skipUnless(Path("/proc").is_dir(), "Linux process-state check")
    def test_timeout_kills_descendant_even_after_leader_exits(self):
        import os
        import signal
        import subprocess
        import sys
        with tempfile.TemporaryDirectory() as directory:
            pid_file = Path(directory) / "child.pid"
            child = ("import os,signal,time; from pathlib import Path; "
                     "signal.signal(signal.SIGTERM,signal.SIG_IGN); "
                     f"Path({str(pid_file)!r}).write_text(str(os.getpid())); time.sleep(30)")
            parent = (f"import subprocess,time; subprocess.Popen([{sys.executable!r}, '-c', {child!r}], "
                      "stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL); time.sleep(30)")
            try:
                with self.assertRaises(subprocess.TimeoutExpired):
                    ci.execute([sys.executable, "-c", parent], time.monotonic() + 1)
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
