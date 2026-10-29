#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Official independently versioned module inventory and source boundary."""

import argparse
import json
from pathlib import Path, PurePosixPath
import re
import subprocess

ROOT = Path(__file__).resolve().parents[1]
MODULE_PATH = "github.com/AChWorks/achrix"


def inventory(root=ROOT):
    data = json.loads((root / "modules.json").read_text())
    if data.get("schemaVersion") != 1 or set(data) != {"schemaVersion", "modules"}:
        raise ValueError("unsupported module inventory")
    modules = data["modules"]
    if not isinstance(modules, list) or not modules:
        raise ValueError("module inventory is empty")
    names, directories, paths = set(), set(), set()
    for module in modules:
        if set(module) != {"name", "directory", "path"}:
            raise ValueError("invalid module entry")
        name, directory, path = (module[k] for k in ("name", "directory", "path"))
        if not isinstance(name, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", name):
            raise ValueError("invalid component name")
        if directory != "." and (not isinstance(directory, str) or not re.fullmatch(r"[a-z][a-z0-9-]*", directory)):
            raise ValueError("module directories must be root or one explicit child")
        expected = MODULE_PATH if directory == "." else MODULE_PATH + "/" + directory
        if path != expected or name in names or directory in directories or path in paths:
            raise ValueError("duplicate or mismatched module identity")
        names.add(name)
        directories.add(directory)
        paths.add(path)
    if modules[0] != {"name": "core", "directory": ".", "path": MODULE_PATH}:
        raise ValueError("first module must be the Core")
    return modules


def component(name, root=ROOT):
    for module in inventory(root):
        if module["name"] == name:
            return module
    raise ValueError("unknown release component")


def release_identity(name, version, root=ROOT):
    if not re.fullmatch(r"v0\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("version must be an unpublished v0.MINOR.PATCH")
    module = component(name, root)
    prefix = "" if module["directory"] == "." else module["directory"] + "/"
    return module, prefix + version, "achrix-" + name + "-" + version


def validate(root=ROOT):
    modules = inventory(root)
    expected = {module["directory"] for module in modules}
    manifests = {"." if path.parent == root else path.parent.as_posix()[len(root.as_posix()) + 1:]
                 for path in root.rglob("go.mod") if ".git" not in path.parts}
    if manifests != expected | {"fixtures/notes"}:
        raise ValueError("missing or uninventoried Go module manifest")
    for module in modules:
        directory = root / module["directory"]
        data = json.loads(subprocess.check_output(
            ["go", "mod", "edit", "-json"], cwd=directory, text=True))
        if data["Module"]["Path"] != module["path"] or data.get("Replace"):
            raise ValueError("module path drift or local replacement: " + module["name"])
        if not (directory / "LICENSE").is_file():
            raise ValueError("module license missing: " + module["name"])
    consumer = json.loads(subprocess.check_output(
        ["go", "mod", "edit", "-json"], cwd=root / "fixtures/notes", text=True))
    if consumer.get("Replace"):
        raise ValueError("consumer local replacements are prohibited")
    return modules


def owns_path(module, path, modules):
    """Whether a repository-relative tracked file belongs to this Go module."""
    relative = PurePosixPath(path)
    if relative.is_absolute() or ".." in relative.parts:
        return False
    directory = module["directory"]
    if directory != ".":
        return bool(relative.parts) and relative.parts[0] == directory
    excluded = {m["directory"] for m in modules if m["directory"] != "."}
    return bool(relative.parts) and relative.parts[0] not in excluded | {"fixtures"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("directories", "paths", "json", "check"))
    args = parser.parse_args()
    modules = validate() if args.command == "check" else inventory()
    if args.command == "json":
        print(json.dumps(modules))
    elif args.command == "check":
        print("Independent module inventory/manifests verified")
    else:
        for module in modules:
            print(module["directory" if args.command == "directories" else "path"])


if __name__ == "__main__":
    main()
