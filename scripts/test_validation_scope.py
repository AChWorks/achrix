# SPDX-License-Identifier: MPL-2.0
"""Regression checks for omission-sensitive scope selection, using real Git."""
import pathlib
import subprocess
import tempfile
import unittest

from validation_scope import classify, select


class ValidationScopeTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="achrix-scope-")
        self.addCleanup(self.temporary.cleanup)
        self.root = pathlib.Path(self.temporary.name)
        self.git("init", "-q")
        self.git("config", "user.name", "Validation fixture")
        self.git("config", "user.email", "validation@example.invalid")
        for name in ("README.md", "achrix.go", "achrix_test.go",
                     "identity/README.md", "audit/README.md", "media/README.md",
                     "admin/README.md", "media/admin/README.md", "bridge/README.md"):
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
        self.assertEqual(self.scope(), ("docs", False, False))

    def test_known_module_readme_edits_need_no_runtime_setup(self):
        for path in ("identity/README.md", "audit/README.md", "media/README.md",
                     "admin/README.md", "media/admin/README.md", "bridge/README.md"):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write(path)
                self.commit()
                self.assertEqual(select(base, self.root), ("docs", False, False))

    def test_module_readme_and_core_test_need_only_core(self):
        self.write("media/README.md")
        self.write("achrix_test.go")
        self.commit()
        self.assertEqual(self.scope(), ("core", False, False))

    def test_module_docs_cannot_hide_runtime_or_unknown_markdown(self):
        for index, path in enumerate((
            "media/media.go", "bridge/bridge.go", "bridge/go.mod", "identity/migrations/new.sql",
            "media/admin/assets/app.js", "go.mod", "new-module/README.md",
        )):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write("media/README.md", f"docs {index}\n")
                self.write(path, f"input {index}\n")
                self.commit()
                self.assertEqual(select(base, self.root), ("full", False, True))

    def test_module_readme_type_change_and_rename_stay_full(self):
        path = self.root / "media/README.md"
        path.unlink()
        path.symlink_to("../README.md")
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))
        base = self.git("rev-parse", "HEAD").strip()
        path.rename(self.root / "media/unknown.md")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))

    def test_runtime_renamed_to_known_module_readme_stays_full(self):
        (self.root / "identity/README.md").unlink()
        (self.root / "achrix.go").rename(self.root / "identity/README.md")
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))

    def test_existing_core_test_and_docs_need_only_core(self):
        self.write("achrix_test.go")
        self.write("README.md")
        self.commit()
        self.assertEqual(self.scope(), ("core", False, False))

    def test_benchmark_addition_and_modification_need_only_core(self):
        self.write("achrix_bench_test.go")
        self.commit()
        self.assertEqual(self.scope(), ("core", False, False))
        base = self.git("rev-parse", "HEAD").strip()
        self.write("achrix_bench_test.go", "updated benchmark\n")
        self.commit()
        self.assertEqual(select(base, self.root), ("core", False, False))

    def test_added_benchmark_symlink_is_not_a_narrow_exception(self):
        (self.root / "achrix_bench_test.go").symlink_to("README.md")
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))

    def test_benchmark_deletion_type_change_and_unknown_tests_stay_full(self):
        self.write("achrix_bench_test.go")
        self.commit()
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "achrix_bench_test.go").unlink()
        (self.root / "achrix_bench_test.go").symlink_to("README.md")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "achrix_bench_test.go").unlink()
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))
        base = self.git("rev-parse", "HEAD").strip()
        self.write("other_bench_test.go")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))

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
                self.assertEqual(select(base, self.root), ("full", False, True))

    def test_routing_changes_validate_the_router(self):
        for path in (".github/workflows/baseline.yml", "scripts/validation_scope.py",
                     "scripts/test_validation_scope.py", "scripts/setup-quality-tools.sh",
                     "scripts/check-gofmt.sh", "scripts/test_go_quality.py",
                     "scripts/modules.py", "scripts/verify_consumer.py",
                     "scripts/test_module_packaging.py", "modules.json"):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write(path)
                self.commit()
                self.assertEqual(select(base, self.root), ("full", True, True))

    def test_deleted_or_renamed_runtime_and_core_tests_cannot_hide(self):
        (self.root / "achrix_test.go").unlink()
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "achrix.go").rename(self.root / "renamed.md")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))

    def test_type_changed_core_test_is_not_a_narrow_exception(self):
        (self.root / "achrix_test.go").unlink()
        (self.root / "achrix_test.go").symlink_to("README.md")
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))

    def test_missing_invalid_and_empty_diff_never_select_narrow(self):
        for base in ("", "0" * 40, "f" * 40, "HEAD", self.base):
            with self.subTest(base=base):
                self.assertEqual(select(base, self.root), ("full", True, True))

    def test_nul_delimited_paths_and_whitespace_failure(self):
        self.write("docs/guide.md\nachrix.go")
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))
        self.write("README.md", "trailing whitespace \n")
        self.commit()
        with self.assertRaises(subprocess.CalledProcessError):
            self.scope()

    def test_browser_runtime_assets_and_shared_fixture_inputs_select_browser(self):
        paths = (
            "admin/assets/admin.js", "admin/assets/admin.css", "admin/templates/page.html",
            "admin/localize/catalog.go", "admin/catalogs/fa.json", "admin/admin.go",
            "identity/http.go", "identity/admin/admin.go", "media/admin/admin.go",
            "media/media.go", "audit/postgres.go", "achrix.go",
            "fixtures/notes/admin_test.go", "fixtures/notes/media_test.go",
            "fixtures/notes/admin_localization_test.go", "fixtures/notes/go.mod",
        )
        for index, path in enumerate(paths):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write(path, f"browser input {index}\n")
                self.commit()
                self.assertEqual(select(base, self.root), ("full", False, True))

    def test_existing_unrelated_backend_changes_keep_full_without_browser(self):
        paths = ("installation/installation.go", "multisite/management.go",
                 "fixtures/notes/internal/domain/note.go")
        for path in paths:
            self.write(path, "existing backend\n")
        self.commit()
        for path in paths:
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write(path)
                self.write("README.md", path + "\n")
                self.commit()
                self.assertEqual(select(base, self.root), ("full", False, False))
        base = self.git("rev-parse", "HEAD").strip()
        self.write("multisite/new.go")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))

    def test_complete_diff_keeps_earlier_browser_change_and_mixed_backend(self):
        self.write("admin/assets/admin.js")
        self.commit()
        self.write("README.md")
        self.write("installation/installation.go")
        self.commit()
        self.assertEqual(self.scope(), ("full", False, True))

    def test_browser_deletion_rename_and_added_document_symlink_do_not_skip(self):
        self.write("admin/assets/admin.js")
        self.commit()
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "admin/assets/admin.js").rename(self.root / "docs-guide.md")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))
        base = self.git("rev-parse", "HEAD").strip()
        (self.root / "docs").mkdir()
        (self.root / "docs/guide.md").symlink_to("../achrix.go")
        self.commit()
        self.assertEqual(select(base, self.root), ("full", False, True))

    def test_browser_tool_inputs_select_browser_and_routing_checks(self):
        for path in ("scripts/admin_browser.cjs", "scripts/validate-admin-browser.sh",
                     "fixtures/browser-tools/package.json", "fixtures/browser-tools/package-lock.json"):
            with self.subTest(path=path):
                base = self.git("rev-parse", "HEAD").strip()
                self.write(path)
                self.commit()
                self.assertEqual(select(base, self.root), ("full", True, True))

    def test_unsupported_status_cannot_select_a_known_exception(self):
        for status in ("U", "R100", "X", ""):
            with self.subTest(status=status):
                self.assertEqual(classify([(status, "README.md")]), ("full", True, True))


if __name__ == "__main__":
    unittest.main()
