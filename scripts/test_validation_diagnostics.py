# SPDX-License-Identifier: MPL-2.0
import json
import os
from pathlib import Path
import re
import shlex
import stat
import subprocess
import sys
import tempfile
import unittest

from validation_diagnostics import LIMIT, capture


class FailureEvidenceTests(unittest.TestCase):
    def test_private_cause_and_safe_support_summary_survive_cleanup(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            source = parent / "source"
            source.mkdir(mode=0o700)
            (source / "postgres.log").write_text("ERROR: failed DDL permission check PRIVATE_PASSWORD\nSTATEMENT: PRIVATE_SQL\n")
            target, result = capture(source, "component_tests", 7, 123, parent / "evidence")
            (source / "postgres.log").unlink()
            source.rmdir()
            self.assertIn("failed DDL permission check", (target / "postgres.log.private").read_text())
            self.assertEqual(stat.S_IMODE(target.stat().st_mode), 0o700)
            self.assertEqual(stat.S_IMODE((target / "postgres.log.private").stat().st_mode), 0o600)
            summary = (target / "support.json").read_text()
            self.assertNotIn("PRIVATE_", summary)
            self.assertEqual(json.loads(summary)["logs"][0]["severity_counts"]["ERROR"], 1)
            self.assertEqual((result["stage"], result["exit_code"], result["line"]), ("component_tests", 7, 123))

    def test_capture_is_bounded_and_rejects_log_symlink(self):
        with tempfile.TemporaryDirectory() as temporary:
            parent = Path(temporary)
            source = parent / "source"
            source.mkdir(mode=0o700)
            (source / "postgres.log").write_bytes(b"x" * (LIMIT * 2))
            target, report = capture(source, "postgres_start", 1, 5, parent / "evidence")
            self.assertEqual((target / "postgres.log.private").stat().st_size, LIMIT)
            self.assertTrue(report["logs"][0]["tail_only"])
            (source / "postgres.log").unlink()
            (source / "postgres.log").symlink_to(target / "postgres.log.private")
            with self.assertRaises(OSError):
                capture(source, "postgres_start", 1, 5, parent / "other")


class ValidationCleanupTests(unittest.TestCase):
    """Execute each real shell lifecycle with deterministic external failures.

    Analysis/consumer phases are irrelevant to cleanup. Load the actual cleanup
    function and startup block, replacing only PostgreSQL and removal commands;
    no server, network access, or host directory is needed by these fault tests.
    """

    scripts = ("validate.sh", "validate-admin-browser.sh")

    def lifecycle(self, parent, name, *, startup=False, primary=0,
                  stop=0, status=3, leave_pid=False, removal=0):
        repository = Path(__file__).resolve().parent.parent
        source = (repository / "scripts" / name).read_text()
        cleanup = source[source.index("cleanup() {"):source.index("\ntrap cleanup EXIT")]
        startup_block = re.search(r"\nstage=postgres_start\n(.*?)\n(?:export PGHOST=|PGHOST=)",
                                  source, re.DOTALL).group(1)
        root = parent / "owned"
        root.mkdir(mode=0o700)
        (root / "postgres.log").write_text("ERROR: PRIVATE_STARTUP_CAUSE\n")
        binaries = parent / "bin"
        binaries.mkdir()
        trace = parent / "trace.jsonl"
        stub = "#!" + sys.executable + "\n" + '''
import json, os, pathlib, shutil, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["CLEANUP_TEST_TRACE"], "a") as output:
    output.write(json.dumps([name, args]) + "\\n")
root = pathlib.Path(os.environ["CLEANUP_TEST_ROOT"])
if name == "initdb":
    assert args[args.index("-D") + 1] == str(root / "database")
    (root / "database").mkdir()
elif name == "pg_ctl":
    assert args[args.index("-D") + 1] == str(root / "database")
    pid = root / "database/postmaster.pid"
    if args[-1] == "start":
        pid.write_text("simulated starting server\\n")
        sys.exit(7)
    if args[-1] == "stop":
        if os.environ["CLEANUP_TEST_LEAVE_PID"] == "0":
            pid.unlink(missing_ok=True)
        sys.exit(int(os.environ["CLEANUP_TEST_STOP"]))
    assert args[-1] == "status"
    sys.exit(int(os.environ["CLEANUP_TEST_STATUS"]))
elif name == "rm":
    assert args == ["-rf", "--", str(root)]
    result = int(os.environ["CLEANUP_TEST_REMOVAL"])
    if result:
        sys.exit(result)
    shutil.rmtree(root)
'''
        for command in ("initdb", "pg_ctl", "rm"):
            path = binaries / command
            path.write_text(stub)
            path.chmod(0o700)
        env = dict(os.environ, PATH=str(binaries) + os.pathsep + os.environ["PATH"],
                   TMPDIR=str(parent), CLEANUP_TEST_TRACE=str(trace), CLEANUP_TEST_ROOT=str(root),
                   CLEANUP_TEST_STOP=str(stop), CLEANUP_TEST_STATUS=str(status),
                   CLEANUP_TEST_LEAVE_PID=str(int(leave_pid)), CLEANUP_TEST_REMOVAL=str(removal))
        # Set both names so this harness also reproduces the former post-success
        # 'started' guard; the real startup block must record the attempted start.
        program = ("set -Eeuo pipefail\numask 077\n" +
                   "repository=" + shlex.quote(str(repository)) + "\n" +
                   "validation_root=" + shlex.quote(str(root)) + "\n" +
                   "start_attempted=0\nstarted=0\nfixture_pid=''\nstage=consumer_tests\nfailure_line=0\n" +
                   cleanup + "\ntrap cleanup EXIT\n")
        if startup:
            program += "stage=postgres_start\n" + startup_block + "\n"
        program += "exit " + str(primary) + "\n"
        result = subprocess.run(["bash", "-c", program], env=env, text=True,
                                capture_output=True, timeout=10)
        calls = [json.loads(line) for line in trace.read_text().splitlines()] if trace.exists() else []
        evidence = sorted(parent.glob("achrix-validation-evidence-*/support.json"))
        return result, root, calls, evidence

    def test_failed_start_wait_reconciles_and_retains_unverified_cluster(self):
        for name in self.scripts:
            # Running, unknown status, and a starting PID despite status=3 all
            # require retention. A failed wait cannot authorize datadir removal.
            for status, leave_pid in ((0, True), (4, False), (3, True)):
                with self.subTest(script=name, status=status, pid=leave_pid), tempfile.TemporaryDirectory() as tmp:
                    result, root, calls, evidence = self.lifecycle(Path(tmp), name, startup=True,
                                                                  stop=8, status=status, leave_pid=leave_pid)
                    self.assertEqual(result.returncode, 7, result.stderr)
                    self.assertEqual([args[-1] for command, args in calls if command == "pg_ctl"],
                                     ["start", "stop", "status"])
                    self.assertTrue(root.is_dir())
                    self.assertFalse(any(command == "rm" for command, _ in calls))
                    self.assertIn("stop is unverified", result.stderr)
                    self.assertEqual(len(evidence), 1)
                    summary = json.loads(evidence[0].read_text())
                    self.assertEqual((summary["stage"], summary["exit_code"]), ("postgres_start", 7))
                    self.assertIn("PRIVATE_STARTUP_CAUSE", (evidence[0].parent / "postgres.log.private").read_text())
                    self.assertNotIn("PRIVATE_", result.stderr + evidence[0].read_text())

    def test_failed_start_wait_removes_only_confirmed_stopped_cluster(self):
        for name in self.scripts:
            with self.subTest(script=name), tempfile.TemporaryDirectory() as tmp:
                result, root, calls, evidence = self.lifecycle(Path(tmp), name, startup=True)
                self.assertEqual(result.returncode, 7, result.stderr)
                self.assertEqual([args[-1] for command, args in calls if command == "pg_ctl"],
                                 ["start", "stop", "status"])
                self.assertFalse(root.exists())
                self.assertEqual(sum(command == "rm" for command, _ in calls), 1)
                self.assertEqual(len(evidence), 1)

    def test_removal_failure_preserves_primary_status_and_additional_evidence(self):
        for name in self.scripts:
            for primary, expected in ((7, 7), (0, 9)):
                with self.subTest(script=name, primary=primary), tempfile.TemporaryDirectory() as tmp:
                    result, root, calls, evidence = self.lifecycle(Path(tmp), name, primary=primary, removal=9)
                    self.assertEqual(result.returncode, expected, result.stderr)
                    self.assertTrue(root.is_dir())
                    self.assertIn("removal exited 9; primary exit " + str(primary), result.stderr)
                    self.assertEqual(sum(command == "rm" for command, _ in calls), 1)
                    self.assertTrue(evidence)
                    for summary_path in evidence:
                        report = json.loads(summary_path.read_text())
                        self.assertEqual(report["exit_code"], expected)
                        self.assertEqual(report["stage"], "workspace_cleanup" if primary == 0 else "consumer_tests")
                        self.assertEqual(stat.S_IMODE(summary_path.stat().st_mode), 0o600)
                        self.assertEqual(stat.S_IMODE(summary_path.parent.stat().st_mode), 0o700)
                    self.assertNotIn("PRIVATE_", result.stderr)
