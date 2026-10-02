# SPDX-License-Identifier: MPL-2.0
"""Fail-closed acquisition and formatting checks, without downloading tools."""
import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPTS = pathlib.Path(__file__).resolve().parent


class GoQualityTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="achrix-quality-")
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.log = self.root / "go-calls"
        stub = self.bin / "go"
        stub.write_text("""#!/usr/bin/env bash
set -eu
printf '%s\\n' "$*" >> "$FAKE_GO_LOG"
case "$*" in
  'env GOVERSION') echo "$FAKE_GOVERSION";;
  'env GOSUMDB') echo "$FAKE_GOSUMDB";;
  'env GOROOT') echo "$FAKE_GO_ROOT";;
  'mod download -json '*)
    echo '{"Path":"honnef.co/go/tools","Version":"v0.8.1","Sum":"wrong","GoModSum":"wrong"}';;
  *) echo 'Unexpected tool acquisition' >&2; exit 99;;
esac
""")
        stub.chmod(0o755)
        self.environment = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                                FAKE_GO_LOG=str(self.log), FAKE_GO_ROOT=str(self.root),
                                FAKE_GOVERSION="go1.27.1", FAKE_GOSUMDB="sum.golang.org")

    def setup_tools(self, destination, **environment):
        return subprocess.run(
            ["bash", str(SCRIPTS / "setup-quality-tools.sh"), str(destination)],
            env=dict(self.environment, **environment), text=True, capture_output=True,
        )

    def test_wrong_go_and_disabled_checksums_cannot_acquire_tools(self):
        for override, message in (({"FAKE_GOVERSION": "go1.26.0"}, "Go 1.27.1"),
                                  ({"FAKE_GOSUMDB": "off"}, "Checksum verification")):
            with self.subTest(override=override):
                destination = self.root / "tools"
                result = self.setup_tools(destination, **override)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)
                self.assertFalse(destination.exists())
                self.assertNotIn("mod download", self.log.read_text())

    def test_existing_or_relative_target_is_never_overwritten(self):
        existing = self.root / "existing"
        existing.mkdir()
        marker = existing / "keep"
        marker.write_text("preserve")
        for destination in (existing, "relative-tools"):
            with self.subTest(destination=destination):
                result = self.setup_tools(destination)
                self.assertNotEqual(result.returncode, 0)
        self.assertEqual(marker.read_text(), "preserve")
        self.assertFalse(self.log.exists())

    def test_checksum_mismatch_cannot_install(self):
        result = self.setup_tools(self.root / "tools")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("identity/checksum mismatch", result.stderr)
        self.assertNotIn("install", self.log.read_text())

    def test_formatter_detects_defect_and_accepts_formatted_space_named_source(self):
        subprocess.run(["git", "init", "-q", str(self.root)], check=True)
        source = self.root / "source with spaces.go"
        source.write_text("package quality\nfunc Hello( ){}\n")
        subprocess.run(["git", "-C", str(self.root), "add", "."], check=True)
        command = ["bash", str(SCRIPTS / "check-gofmt.sh")]
        result = subprocess.run(command, cwd=self.root, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(source.name, result.stderr)
        subprocess.run(["gofmt", "-w", str(source)], check=True)
        result = subprocess.run(command, cwd=self.root, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        source.write_text("package quality\nfunc broken(\n")
        result = subprocess.run(command, cwd=self.root, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
