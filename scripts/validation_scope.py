# SPDX-License-Identifier: MPL-2.0
"""Select only proven non-runtime exceptions; unknown Git evidence runs full."""
import re
import subprocess
import sys

DOCUMENTS = {
    "README.md", "AGENTS.md", "CONTRIBUTING.md", "GOVERNANCE.md",
    "SECURITY.md", "TRADEMARKS.md", "fixtures/notes/README.md",
    "fixtures/notes/THIRD_PARTY_NOTICES.md",
}
ROUTING = {
    ".github/workflows/baseline.yml", "scripts/validation_scope.py",
    "scripts/test_validation_scope.py", "scripts/validate.sh",
    "scripts/setup-validation-postgres.sh",
}


def classify(changes):
    if not changes:
        return "full", True
    docs = lambda path: path in DOCUMENTS or (
        path.startswith("docs/") and path.endswith(".md")
    )
    routing = any(path in ROUTING for _, path in changes)
    if all(docs(path) for _, path in changes):
        return "docs", routing
    if all(docs(path) or (status == "M" and path == "achrix_test.go")
           for status, path in changes):
        return "core", routing
    return "full", routing


def select(base, directory=None):
    if not re.fullmatch(r"[0-9a-f]{40}", base) or base == "0" * 40:
        return "full", True
    git = ["git", "-C", str(directory)] if directory else ["git"]
    try:
        diff = subprocess.check_output(
            git + ["diff", "--no-ext-diff", "--no-textconv", "--no-renames",
                   "--name-status", "-z", base, "HEAD"], stderr=subprocess.PIPE
        )
    except subprocess.CalledProcessError:
        return "full", True
    fields = diff.split(b"\0")
    if not diff or fields[-1] != b"" or len(fields[:-1]) % 2:
        return "full", True
    changes = [(status.decode("ascii"), path.decode("utf-8", "surrogateescape"))
               for status, path in zip(fields[:-1:2], fields[1:-1:2])]
    # Fail an actual whitespace defect; never turn failed lint into a narrow pass.
    subprocess.run(
        git + ["diff", "--no-ext-diff", "--no-textconv", "--check", base, "HEAD"],
        check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    return classify(changes)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("Usage: python3 scripts/validation_scope.py <base SHA>")
    try:
        scope, routing = select(sys.argv[1])
    except subprocess.CalledProcessError as error:
        sys.stderr.buffer.write(error.stdout or error.stderr or b"Diff check failed\n")
        sys.exit(error.returncode)
    print(f"scope={scope}")
    print(f"routing_tests={str(routing).lower()}")
    print(f"Validation scope: {scope}", file=sys.stderr)
