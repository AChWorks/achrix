# SPDX-License-Identifier: MPL-2.0
import json
from pathlib import Path
import stat
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
