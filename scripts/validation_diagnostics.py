#!/usr/bin/env python3
# SPDX-License-Identifier: MPL-2.0
"""Retain bounded private failure evidence before a task-owned cluster is removed.

The support summary contains stage/exit/severity facts only. Original server/test
messages remain private, mode 0600, and MUST be reviewed/redacted before export.
This is not a universal text sanitizer or a production log-retention service.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import sys
import tempfile

LIMIT = 65536
LOGS = ("postgres.log", "fixture.log")
FIXTURE_FAILURES = 32


def fixture_failures(root):
    """Read only the fixture-owned safe channel, never records inside raw logs."""
    source = root / "browser-failures.jsonl"
    if not source.exists():
        return [], False, 0
    fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, "rb") as stream:
        info = os.fstat(stream.fileno())
        if not stat.S_ISREG(info.st_mode):
            raise ValueError("regular fixture failure projection required")
        data = stream.read(LIMIT)
    records, rejected = [], 0
    truncated = info.st_size > LIMIT or (bool(data) and not data.endswith(b"\n"))
    for line in data.splitlines():
        if len(records) >= FIXTURE_FAILURES:
            truncated = True
            break
        try:
            value = json.loads(line)
            if not isinstance(value, dict) or set(value) - {
                "test", "file", "line", "reason", "error_type", "sqlstate", "errno", "context"
            }:
                raise ValueError("unexpected fixture field")
            if not re.fullmatch(r"Test[A-Za-z0-9_/-]{1,127}", value.get("test", "")):
                raise ValueError("invalid fixture test")
            if not re.fullmatch(r"[a-z0-9_]{1,100}_test\.go", value.get("file", "")):
                raise ValueError("invalid fixture source")
            if type(value.get("line")) is not int or not 1 <= value["line"] <= 1000000:
                raise ValueError("invalid fixture line")
            reason = value.get("reason")
            if not isinstance(reason, str) or not 1 <= len(reason) <= 256 or any(ord(c) < 32 for c in reason):
                raise ValueError("invalid source-owned reason")
            if "error_type" in value and not re.fullmatch(r"[A-Za-z0-9_.*\[\]/]{1,128}", value["error_type"]):
                raise ValueError("invalid fixture error type")
            if "sqlstate" in value and not re.fullmatch(r"[A-Z0-9]{5}", value["sqlstate"]):
                raise ValueError("invalid fixture SQLSTATE")
            if "errno" in value and (type(value["errno"]) is not int or not 1 <= value["errno"] <= 4095):
                raise ValueError("invalid fixture errno")
            if "context" in value and value["context"] not in {"deadline_exceeded", "cancelled"}:
                raise ValueError("invalid fixture context result")
            records.append(value)
        except (ValueError, TypeError):
            rejected += 1
    return records, truncated, rejected


def capture(root, stage, exit_code, line, destination=None):
    root = Path(root)
    if root.is_symlink() or not root.is_dir() or stat.S_IMODE(root.stat().st_mode) & 0o077:
        raise ValueError("private task-owned evidence source required")
    if not re.fullmatch(r"[a-z0-9_-]{1,64}", stage):
        stage = "validation"
    target = Path(tempfile.mkdtemp(prefix="achrix-validation-evidence-")) if destination is None else Path(destination)
    if destination is not None:
        target.mkdir(mode=0o700)
    os.chmod(target, 0o700)
    report = {"stage": stage, "exit_code": int(exit_code), "line": int(line), "logs": []}
    failures, truncated, rejected = fixture_failures(root)
    if failures or truncated or rejected:
        report.update(fixture_failures=failures, fixture_failures_truncated=truncated,
                      fixture_failure_records_rejected=rejected)
    for name in LOGS:
        source = root / name
        if not source.exists():
            continue
        fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW)
        with os.fdopen(fd, "rb") as stream:
            info = os.fstat(stream.fileno())
            if not stat.S_ISREG(info.st_mode):
                raise ValueError("regular task log required")
            stream.seek(max(0, info.st_size - LIMIT))
            data = stream.read(LIMIT)
        private = target / (name + ".private")
        with os.fdopen(os.open(private, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as output:
            output.write(data)
        text = data.decode("utf-8", "replace")
        severity = {level: len(re.findall(r"\b" + level + r":", text)) for level in ("ERROR", "FATAL", "PANIC", "WARNING")}
        report["logs"].append({"file": private.name, "bytes_retained": len(data), "tail_only": info.st_size > LIMIT,
                               "sha256": hashlib.sha256(data).hexdigest(), "severity_counts": severity})
    with os.fdopen(os.open(target / "support.json", os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as output:
        json.dump(report, output, sort_keys=True)
        output.write("\n")
    return target, report


def main():
    if len(sys.argv) != 5:
        raise SystemExit("usage: validation_diagnostics.py PRIVATE_ROOT STAGE EXIT LINE")
    target, report = capture(Path(sys.argv[1]), *sys.argv[2:])
    print("Validation failed: stage={stage} line={line} exit={exit_code}".format(**report))
    # Keep useful bounded metadata in CI logs after an ephemeral runner exits.
    # This report was constructed above; never serialize arbitrary log bodies.
    print("Support summary: " + json.dumps(report, sort_keys=True))
    print("Bounded private evidence: " + str(target) + " (review/redact private logs before export; remove after diagnosis)")


if __name__ == "__main__":
    main()
