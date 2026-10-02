# SPDX-License-Identifier: MPL-2.0
"""Regression checks for omission-sensitive scope selection, using real Git."""
import pathlib
import subprocess
import tempfile
import unittest

from validation_scope import select


class ValidationScopeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="achrix-scope-")
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.git("init", "-q")
        self.git("config", "user.name", "Validation fixture")
        self.git("config", "user.email", "validation@example.invalid")
        for name in ("README.md", "achrix.go", "achrix_test.go"):
            self.write(name, "baseline\n")
        self.commit()
        self.base = self.git("rev-parse", "HEAD").strip()

    def git(self, *args):
        return subprocess.check_output(
            ["git", "-C", str(self.root), *args], text=True,
            stderr=subprocess.PIPE,
        )

    def write(self, name, text="changed\n"):
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def commit(self):
        self.git("add", "-A")
        self.git("commit", "-qm", "Fixture change")

    def scope(self):
        return select(self.base, self.root)

    def test_documentation_needs_no_runtime_setup(self):
        self.write("README.md")
        self.write("docs/architecture/new.md")
        self.commit()
        self.assertEqual(self.scope(), ("docs", False))

    def test_existing_core_test_and_docs_need_only_core(self):
        self.write("achrix_test.go")
        self.write("README.md")
        self.commit()
        self.assertEqual(self.scope(), ("core", False))

    def test_benchmark_addition_and_modification_need_only_core(self):
        self.write("achrix_bench_test.go")
        self.commit()
        self.assertEqual(self.scope(), ("core", False))
        base = self.git("rev-parse", "HEAD").strip()
        self.write("achrix_bench_test.go", "updated benchmark\n")
        self.commit()
        self.assertEqual(select(base, self.root), ("core", False))

    def test_benchmark_deletion_type_change_and_unknown_tests_stay_full(self):
        self.write("achrix_bench_test.go")
        self.commit()
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "achrix_bench_test.go").unlink()
        (self.root / "achrix_bench_test.go").symlink_to("README.md")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False))
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "achrix_bench_test.go").unlink()
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False))
        base = self.git("rev-parse", "HEAD").strip()
        self.write("other_bench_test.go")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False))

    def test_runtime_mixed_dependencies_and_unknown_paths_stay_full(self):
        for index, path in enumerate((
            "achrix.go", "go.mod", "fixtures/notes/go.sum",
            "fixtures/notes/internal/infrastructure/migrations/new.sql",
            "new-input",
        )):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write("README.md", f"docs {index}\n")
                self.write(path, f"runtime {index}\n")
                self.commit()
                self.assertEqual(select(base, self.root), ("full", False))

    def test_routing_changes_validate_the_router(self):
        for path in (".github/workflows/baseline.yml", "scripts/setup-quality-tools.sh",
                     "scripts/check-gofmt.sh", "scripts/test_go_quality.py"):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write(path)
                self.commit()
                self.assertEqual(select(base, self.root), ("full", True))

    def test_deleted_or_renamed_runtime_and_core_tests_cannot_hide(self):
        (self.root / "achrix_test.go").unlink()
        self.commit()
        self.assertEqual(self.scope(), ("full", False))
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "achrix.go").rename(self.root / "renamed.md")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False))

    def test_type_changed_core_test_is_not_a_narrow_exception(self):
        (self.root / "achrix_test.go").unlink()
        (self.root / "achrix_test.go").symlink_to("README.md")
        self.commit()
        self.assertEqual(self.scope(), ("full", False))

    def test_missing_invalid_and_empty_diff_never_select_narrow(self):
        for base in ("", "0" * 40, "f" * 40, "HEAD", self.base):
            with self.subTest(base=base):
                self.assertEqual(select(base, self.root), ("full", True))

    def test_nul_delimited_paths_and_whitespace_failure(self):
        self.write("docs/guide.md\nachrix.go")
        self.commit()
        self.assertEqual(self.scope(), ("full", False))
        self.write("README.md", "trailing whitespace \n")
        self.commit()
        with self.assertRaises(subprocess.CalledProcessError):
            self.scope()


if __name__ == "__main__":
    unittest.main()
