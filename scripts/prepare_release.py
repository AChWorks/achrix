#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Gate and export one official source release; never publish or run runtime tests."""

import argparse
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile

REPOSITORY = "AChWorks/achrix"
WORKFLOW = ".github/workflows/release.yml"
BASELINE = ".github/workflows/baseline.yml"
VERSION = re.compile(r"v0\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")


def command(*args):
    return subprocess.check_output(args, text=True).strip()


def api(endpoint):
    return json.loads(command("gh", "api", endpoint))


def release_gate(version):
    if not VERSION.fullmatch(version):
        raise ValueError("version must be an unpublished v0.MINOR.PATCH")
    source = os.environ["GITHUB_SHA"]
    if (
        os.environ["GITHUB_REPOSITORY"] != REPOSITORY
        or os.environ["GITHUB_REF"] != "refs/heads/main"
        or os.environ["GITHUB_EVENT_NAME"] != "workflow_dispatch"
        or os.environ["RUNNER_ENVIRONMENT"] != "github-hosted"
        or os.environ["GITHUB_WORKFLOW_REF"] != f"{REPOSITORY}/{WORKFLOW}@refs/heads/main"
        or os.environ["GITHUB_WORKFLOW_SHA"] != source
        or not re.fullmatch(r"[0-9a-f]{40}", source)
        or command("git", "rev-parse", "HEAD") != source
    ):
        raise ValueError("source must be the exact hosted canonical-main dispatch")
    if api(f"repos/{REPOSITORY}/git/ref/heads/main")["object"]["sha"] != source:
        raise ValueError("main moved; reconcile and prepare its reviewed source")
    refs = api(f"repos/{REPOSITORY}/git/matching-refs/tags/{version}")
    releases = command(
        "gh", "api", "--paginate", f"repos/{REPOSITORY}/releases", "--jq", ".[].tag_name"
    ).splitlines()
    if any(ref["ref"] == f"refs/tags/{version}" for ref in refs) or version in releases:
        raise ValueError("version already has a tag or release; never overwrite it")
    runs = api(
        f"repos/{REPOSITORY}/actions/workflows/baseline.yml/runs"
        f"?head_sha={source}&event=push&status=success&per_page=100"
    )["workflow_runs"]
    checks = api(f"repos/{REPOSITORY}/commits/{source}/check-runs?per_page=100")["check_runs"]
    for run in runs:
        if (
            run["head_sha"] == source
            and run["head_branch"] == "main"
            and run["head_repository"]["full_name"] == REPOSITORY
            and run["path"] == BASELINE
            and run["event"] == "push"
            and run["status"] == "completed"
            and run["conclusion"] == "success"
            and any(
                check["name"] == "baseline"
                and check["app"]["id"] == 15368
                and check["status"] == "completed"
                and check["conclusion"] == "success"
                and check["details_url"].startswith(f"{run['html_url']}/job/")
                for check in checks
            )
        ):
            return source, run
    raise ValueError("no successful GitHub Actions baseline for this exact main source")


def export_source(source, version, output):
    # A new runner-temporary destination prevents workspace/generated files from
    # entering either the archive or the scanned source tree.
    output.mkdir(parents=True, exist_ok=False)
    assets = output / "assets"
    assets.mkdir()
    source_dir = output / "source"
    source_dir.mkdir()
    prefix = f"achrix-{version}/"
    archive = subprocess.check_output(
        ["git", "archive", "--format=tar", f"--prefix={prefix}", source]
    )
    tracked = {}
    for entry in subprocess.check_output(["git", "ls-tree", "-r", "-z", source]).split(b"\0"):
        if entry:
            identity, name = entry.split(b"\t", 1)
            mode, kind, digest = identity.decode().split()
            if kind != "blob" or mode not in ("100644", "100755"):
                raise ValueError("source release supports ordinary tracked files only")
            tracked[name.decode()] = digest
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:") as tree:
        exported = {}
        for member in tree.getmembers():
            if member.isdir():
                continue
            if not member.isfile() or not member.name.startswith(prefix):
                raise ValueError("unexpected source archive member")
            name = member.name[len(prefix):]
            if name not in tracked or name in exported or ".." in Path(name).parts:
                raise ValueError("archive differs from the tracked source tree")
            data = tree.extractfile(member).read()
            exported[name] = hashlib.sha1(b"blob " + str(len(data)).encode() + b"\0" + data).hexdigest()
        if exported != tracked:
            raise ValueError("archive bytes differ from tracked source (including export attributes)")
        required = {"LICENSE", "fixtures/notes/LICENSE", "fixtures/notes/THIRD_PARTY_NOTICES.md"}
        if not required <= exported.keys():
            raise ValueError("source license or dependency notices are missing")
        tree.extractall(source_dir, filter="data")
    archive_path = assets / f"achrix-{version}-source.tar.gz"
    with archive_path.open("xb") as destination:
        subprocess.run(["gzip", "-n"], input=archive, stdout=destination, check=True)
    return assets


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version")
    parser.add_argument("output", nargs="?", type=Path)
    parser.add_argument("--check-only", action="store_true")
    args = parser.parse_args()
    source, baseline = release_gate(args.version)
    if args.check_only:
        print(f"Release gates passed for {args.version} at {source}")
        return
    if args.output is None or not args.output.resolve().is_relative_to(Path(os.environ["RUNNER_TEMP"]).resolve()):
        parser.error("output must be a new directory within RUNNER_TEMP")
    assets = export_source(source, args.version, args.output)
    metadata = {
        "version": args.version,
        "repository": REPOSITORY,
        "source_sha": source,
        "source_tree": command("git", "rev-parse", f"{source}^{{tree}}"),
        "preparation_run": {
            "id": int(os.environ["GITHUB_RUN_ID"]),
            "attempt": int(os.environ["GITHUB_RUN_ATTEMPT"]),
            "url": f"https://github.com/{REPOSITORY}/actions/runs/{os.environ['GITHUB_RUN_ID']}",
            "workflow": WORKFLOW,
        },
        "baseline_run": {
            "id": baseline["id"],
            "attempt": baseline["run_attempt"],
            "url": baseline["html_url"],
            "workflow": BASELINE,
            "source_sha": baseline["head_sha"],
        },
        "inventory_scope": "Syft v1.52.0 SPDX source/manifest inventory of the exact exported tracked tree; not a compiled-runtime SBOM",
    }
    (assets / "release.json").write_text(json.dumps(metadata, indent=2) + "\n")
    print(f"Prepared {args.version} source at {source}; baseline {baseline['id']}")


if __name__ == "__main__":
    main()
