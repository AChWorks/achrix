# SPDX-License-Identifier: MPL-2.0
"""Independent module topology, release selection and archive safety contracts."""

import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock

import modules
import prepare_release
import verify_consumer


class InventoryTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="achrix-module-policy-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.data = {"schemaVersion": 1, "modules": [
            {"name": "core", "directory": ".", "path": modules.MODULE_PATH},
            {"name": "media", "directory": "media", "path": modules.MODULE_PATH + "/media"},
        ]}
        self.write()

    def write(self):
        (self.root / "modules.json").write_text(json.dumps(self.data))

    def test_independent_tag_namespace_and_version(self):
        core, tag, artifact = modules.release_identity("core", "v0.4.0", self.root)
        self.assertEqual((tag, artifact), ("v0.4.0", "achrix-core-v0.4.0"))
        child, tag, artifact = modules.release_identity("media", "v0.1.2", self.root)
        self.assertEqual((tag, artifact), ("media/v0.1.2", "achrix-media-v0.1.2"))
        for name, version in (("../media", "v0.1.2"), ("media", "media/v0.1.2"),
                              ("media", "v0.01.2"), ("media", "v1.0.0")):
            with self.subTest(name=name, version=version), self.assertRaises(ValueError):
                modules.release_identity(name, version, self.root)

    def test_duplicate_identity_and_unsafe_directories_fail(self):
        for entry in (self.data["modules"][1],
                      {"name": "escape", "directory": "../escape", "path": modules.MODULE_PATH + "/escape"},
                      {"name": "foreign", "directory": "foreign", "path": "example.com/foreign"}):
            with self.subTest(entry=entry):
                original = self.data["modules"][:]
                self.data["modules"].append(entry)
                self.write()
                with self.assertRaises(ValueError):
                    modules.inventory(self.root)
                self.data["modules"] = original

    def test_source_ownership_excludes_other_modules_and_consumer(self):
        inventory = modules.inventory(self.root)
        core, media = inventory
        self.assertTrue(modules.owns_path(core, "achrix.go", inventory))
        self.assertFalse(modules.owns_path(core, "media/media.go", inventory))
        self.assertFalse(modules.owns_path(core, "fixtures/notes/go.mod", inventory))
        self.assertTrue(modules.owns_path(media, "media/admin/admin.go", inventory))
        self.assertFalse(modules.owns_path(media, "media/../achrix.go", inventory))
        self.assertFalse(modules.owns_path(media, "/media/media.go", inventory))


class ReleaseArchiveTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="achrix-module-export-")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.repo.mkdir()
        subprocess.run(["git", "init", "-q", str(self.repo)], check=True)
        subprocess.run(["git", "-C", str(self.repo), "config", "user.name", "Fixture"], check=True)
        subprocess.run(["git", "-C", str(self.repo), "config", "user.email", "fixture@example.invalid"], check=True)
        data = {"schemaVersion": 1, "modules": [
            {"name": "core", "directory": ".", "path": modules.MODULE_PATH},
            {"name": "media", "directory": "media", "path": modules.MODULE_PATH + "/media"},
        ]}
        (self.repo / "modules.json").write_text(json.dumps(data))
        (self.repo / "LICENSE").write_text("Core source license\n")
        (self.repo / "go.mod").write_text("module " + modules.MODULE_PATH + "\n\ngo 1.27.3\n")
        (self.repo / "achrix.go").write_text("package achrix\n")
        (self.repo / "media").mkdir()
        (self.repo / "media/LICENSE").write_text("Media source license\n")
        (self.repo / "media/go.mod").write_text("module " + modules.MODULE_PATH + "/media\n\ngo 1.27.3\n")
        (self.repo / "media/media.go").write_text("package media\n")
        (self.repo / "fixtures/notes").mkdir(parents=True)
        (self.repo / "fixtures/notes/private.go").write_text("consumer only\n")
        self.commit()
        self.original_cwd = Path.cwd()
        os.chdir(self.repo)
        self.addCleanup(os.chdir, self.original_cwd)
        # Production policy remains the versioned inventory of the checked-out
        # exact source. Test with the isolated source fixture, never host data.
        self.inventory_patch = mock.patch.object(prepare_release, "inventory", lambda: modules.inventory(self.repo))
        self.identity_patch = mock.patch.object(prepare_release, "release_identity", lambda name, version: modules.release_identity(name, version, self.repo))
        self.inventory_patch.start()
        self.identity_patch.start()
        self.addCleanup(self.inventory_patch.stop)
        self.addCleanup(self.identity_patch.stop)

    def commit(self):
        subprocess.run(["git", "-C", str(self.repo), "add", "."], check=True)
        subprocess.run(["git", "-C", str(self.repo), "commit", "-qm", "Fixture source"], check=True)
        self.sha = subprocess.check_output(["git", "-C", str(self.repo), "rev-parse", "HEAD"], text=True).strip()

    def archive(self, name):
        assets = prepare_release.export_source(self.sha, "v0.1.0", self.root / (name + "-export"), name)
        archive = next(assets.glob("*.tar.gz"))
        with tarfile.open(archive, "r:gz") as tree:
            return {entry.name: tree.extractfile(entry).read() for entry in tree if entry.isfile()}

    def test_component_export_contains_own_exact_source_and_license(self):
        exported = self.archive("media")
        self.assertEqual(set(exported), {"achrix-media-v0.1.0/" + name for name in ("LICENSE", "go.mod", "media.go")})
        self.assertEqual(exported["achrix-media-v0.1.0/media.go"], b"package media\n")
        exported = self.archive("core")
        self.assertIn("achrix-core-v0.1.0/achrix.go", exported)
        self.assertFalse(any("/media/" in path or "/fixtures/" in path for path in exported))

    def test_missing_license_and_export_ignored_source_fail_closed(self):
        (self.repo / "media/LICENSE").unlink()
        self.commit()
        with self.assertRaises(ValueError):
            self.archive("media")
        (self.repo / "media/LICENSE").write_text("license\n")
        (self.repo / ".gitattributes").write_text("media/media.go export-ignore\n")
        self.commit()
        with self.assertRaises(ValueError):
            self.archive("media")

    def test_changed_inventory_and_manifest_cannot_relabel_source(self):
        (self.repo / "modules.json").write_text("{}")
        with self.assertRaises((ValueError, KeyError)):
            self.archive("media")
        subprocess.run(["git", "checkout", "--", "modules.json"], cwd=self.repo, check=True)
        (self.repo / "media/go.mod").write_text("module example.com/foreign\n")
        with self.assertRaises(ValueError):
            self.archive("media")


class ReleaseGateTests(unittest.TestCase):
    def test_child_tag_collision_preserves_immutable_release(self):
        source = "a" * 40
        environment = {
            "SYFT_VERSION": "v1.52.0", "GITHUB_SHA": source,
            "GITHUB_REPOSITORY": prepare_release.REPOSITORY,
            "GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_NAME": "workflow_dispatch",
            "RUNNER_ENVIRONMENT": "github-hosted",
            "GITHUB_WORKFLOW_REF": prepare_release.REPOSITORY + "/" + prepare_release.WORKFLOW + "@refs/heads/main",
            "GITHUB_WORKFLOW_SHA": source,
        }
        def command(*args):
            if args == ("git", "rev-parse", "HEAD"):
                return source
            return "media/v0.1.0"
        def api(endpoint):
            if endpoint.endswith("git/ref/heads/main"):
                return {"object": {"sha": source}}
            self.assertTrue(endpoint.endswith("git/matching-refs/tags/media/v0.1.0"))
            return [{"ref": "refs/tags/media/v0.1.0"}]
        with mock.patch.dict(os.environ, environment), mock.patch.object(prepare_release, "command", command), mock.patch.object(prepare_release, "api", api):
            with self.assertRaisesRegex(ValueError, "never overwrite"):
                prepare_release.release_gate("v0.1.0", "media")
        with mock.patch.dict(os.environ, dict(environment, GITHUB_REF="refs/heads/work/unsafe")):
            with self.assertRaisesRegex(ValueError, "exact hosted canonical-main"):
                prepare_release.release_gate("v0.1.0", "media")


class ConsumerSourceTests(unittest.TestCase):
    def test_removed_runtime_source_is_not_silently_accepted(self):
        with tempfile.TemporaryDirectory(prefix="achrix-source-proof-") as temporary:
            root = Path(temporary)
            repo, cache = root / "repo", root / "cache"
            repo.mkdir()
            downloaded = cache / "component"
            downloaded.mkdir(parents=True)
            module = {"name": "core", "directory": ".", "path": modules.MODULE_PATH}
            (repo / "achrix.go").write_text("package achrix\n")
            (downloaded / "achrix.go").write_text("package achrix\n")
            (downloaded / "removed.go").write_text("package achrix\n")
            value = {"Path": modules.MODULE_PATH, "Version": "v0.4.0",
                     "Sum": "h1:fixture", "GoModSum": "h1:fixture", "Dir": str(downloaded)}
            with mock.patch.dict(os.environ, {"GOMODCACHE": str(cache)}), \
                    mock.patch.object(verify_consumer, "inventory", return_value=[module]), \
                    mock.patch.object(verify_consumer.subprocess, "check_output", return_value=json.dumps(value)):
                with self.assertRaisesRegex(ValueError, "runtime file set drift"):
                    verify_consumer.verify_sources(repo, root / "resolved.json")
                (downloaded / "removed.go").unlink()
                verify_consumer.verify_sources(repo, root / "resolved.json")


    def test_catalog_bytes_and_file_sets_are_runtime_inputs(self):
        with tempfile.TemporaryDirectory(prefix="achrix-catalog-source-") as temporary:
            root = Path(temporary)
            module = {"name": "admin", "directory": "admin",
                      "path": modules.MODULE_PATH + "/admin"}
            (root / "locales").mkdir()
            catalog = root / "locales/en.json"
            catalog.write_text('{"messages":{"title":{"other":"Administration"}}}')
            actual = verify_consumer.runtime_sources(root, module, [module])
            self.assertEqual(actual, {"locales/en.json": catalog.read_bytes()})
            catalog.write_text('{"messages":{"title":{"other":"Changed"}}}')
            changed = verify_consumer.runtime_sources(root, module, [module])
            self.assertNotEqual(actual, changed)
            catalog.unlink()
            self.assertNotEqual(actual.keys(), verify_consumer.runtime_sources(root, module, [module]).keys())


class CompiledIdentityTests(unittest.TestCase):
    def test_independent_versions_and_descriptor_binding(self):
        with tempfile.TemporaryDirectory(prefix="achrix-version-proof-") as temporary:
            root = Path(temporary)
            versions = {modules.MODULE_PATH: "v0.4.0"}
            versions.update({modules.MODULE_PATH + "/" + name: "v0.1." + str(i)
                             for i, name in enumerate(("audit", "identity", "media", "multisite", "admin", "installation"))})
            resolved = root / "resolved.json"
            resolved.write_text(json.dumps({path: {"Version": version} for path, version in versions.items()}))
            descriptors = [{"ID": "achrix." + name, "Version": versions[modules.MODULE_PATH + "/" + name],
                            "Provides": [{"ID": "example." + name, "Version": 1}],
                            "Requires": [{"ID": "achrix.authorization", "Version": 2}]}
                           for name in ("audit", "identity", "media", "multisite")]
            build = {"foundation": versions[modules.MODULE_PATH], "versions": versions, "components": descriptors}
            identity = root / "identity.json"
            identity.write_text(json.dumps(build))
            verify_consumer.verify_components(resolved, identity)
            build["components"][0]["Version"] = versions[modules.MODULE_PATH]
            identity.write_text(json.dumps(build))
            with self.assertRaises(ValueError):
                verify_consumer.verify_components(resolved, identity)


if __name__ == "__main__":
    unittest.main()
