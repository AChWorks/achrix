#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Verify cold normal-module source pins and independently built identities."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

from modules import inventory, owns_path

EXTENSIONS = {".go", ".sql", ".js", ".css", ".html"}


def runtime_sources(root, module, modules):
    """Map the complete owned runtime file set, including unexpected files."""
    result = {}
    for source in sorted(root.rglob("*")):
        relative = source.relative_to(root)
        repository_relative = relative if module["directory"] == "." else Path(module["directory"]) / relative
        if (source.suffix not in EXTENSIONS or source.name.endswith("_test.go")
                or not owns_path(module, repository_relative.as_posix(), modules)):
            continue
        if source.is_symlink():
            raise ValueError("source boundary contains symlink")
        if source.is_file():
            result[relative.as_posix()] = source.read_bytes()
    return result


def verify_sources(repository, destination):
    modules = inventory(repository)
    resolved = {}
    digest = hashlib.sha256()
    cache = Path(os.environ["GOMODCACHE"]).resolve()
    for module in modules:
        value = json.loads(subprocess.check_output(
            ["go", "list", "-m", "-json", module["path"]], text=True))
        if (value["Path"] != module["path"] or not value.get("Version", "").startswith("v0.")
                or "Replace" in value or not value.get("Sum") or not value.get("GoModSum")):
            raise ValueError("normal independently pinned module required: " + module["path"])
        downloaded_root = Path(value["Dir"]).resolve()
        if not downloaded_root.is_relative_to(cache):
            raise ValueError("dependency is outside the cold module cache")
        source_root = repository / module["directory"]
        local_files = runtime_sources(source_root, module, modules)
        downloaded_files = runtime_sources(downloaded_root, module, modules)
        if not local_files:
            raise ValueError("module has no runtime source: " + module["name"])
        if local_files.keys() != downloaded_files.keys():
            raise ValueError("normal consumer runtime file set drift: " + module["name"])
        for name, local in sorted(local_files.items()):
            if local != downloaded_files[name]:
                raise ValueError("normal consumer source drift: " + module["name"] + "/" + name)
            relative = name if module["directory"] == "." else module["directory"] + "/" + name
            digest.update(relative.encode() + b"\0" + local)
        resolved[module["path"]] = {key: value[key] for key in ("Path", "Version", "Sum", "GoModSum")}
    destination.write_text(json.dumps(resolved, sort_keys=True) + "\n")
    print("Verified independently pinned module source SHA-256:", digest.hexdigest())
    for path, module in sorted(resolved.items()):
        print("Verified isolated pinned module:", path, module["Version"])


def verify_components(resolved_file, identity_file):
    modules = json.loads(resolved_file.read_text())
    build = json.loads(identity_file.read_text())
    expected = {path: value["Version"] for path, value in modules.items()}
    if build.get("versions") != expected or build.get("foundation") != expected["github.com/AChWorks/achrix"]:
        raise ValueError("compiled independent component identity drift")
    components = {descriptor["ID"]: descriptor for descriptor in build["components"]}
    if len(components) != len(build["components"]):
        raise ValueError("duplicate compiled component descriptor")
    if set(components) != {"achrix." + name for name in ("identity", "audit", "media", "multisite")}:
        raise ValueError("unexpected compiled component inventory")
    for name in ("identity", "audit", "media", "multisite"):
        descriptor = components["achrix." + name]
        if descriptor["Version"] != expected["github.com/AChWorks/achrix/" + name]:
            raise ValueError("descriptor source version drift: " + name)
        if any(capability["Version"] != 1 for capability in descriptor["Provides"]):
            raise ValueError("unexpected capability ABI: " + name)
        if any(capability["Version"] != (2 if capability["ID"] == "achrix.authorization" else 1)
               for capability in descriptor["Requires"]):
            raise ValueError("unexpected required ABI: " + name)
    print("Verified packaged independent component versions:", json.dumps(build, sort_keys=True))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    actions = parser.add_subparsers(dest="action", required=True)
    source = actions.add_parser("source")
    source.add_argument("repository", type=Path)
    source.add_argument("output", type=Path)
    compiled = actions.add_parser("components")
    compiled.add_argument("resolved", type=Path)
    compiled.add_argument("identity", type=Path)
    args = parser.parse_args()
    if args.action == "source":
        verify_sources(args.repository, args.output)
    else:
        verify_components(args.resolved, args.identity)


if __name__ == "__main__":
    main()
