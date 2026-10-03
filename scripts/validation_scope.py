# SPDX-License-Identifier: MPL-2.0
"""Select only proven non-runtime exceptions; unknown Git evidence runs full."""
import re
import subprocess
import sys

DOCUMENTS = {
    "README.md", "AGENTS.md", "CONTRIBUTING.md", "GOVERNANCE.md",
    "SECURITY.md", "TRADEMARKS.md", "fixtures/notes/README.md",
    "fixtures/notes/THIRD_PARTY_NOTICES.md",
    "identity/README.md", "audit/README.md", "media/README.md",
    "admin/README.md", "media/admin/README.md",
}
ROUTING = {
    ".github/workflows/baseline.yml", "scripts/validation_scope.py",
    "scripts/test_validation_scope.py", "scripts/validate.sh",
    "scripts/setup-validation-postgres.sh", "scripts/setup-quality-tools.sh",
    "scripts/check-gofmt.sh", "scripts/test_go_quality.py",
}


def classify(changes, benchmark_is_regular=False):
    if not changes:
        return "full", True
    docs = lambda path: path in DOCUMENTS or (
        path.startswith("docs/") and path.endswith(".md")
    )
    routing = any(path in ROUTING for _, path in changes)
    # A known path changing file type is no longer proven documentation/test input.
    if any(status == "T" for status, _ in changes):
        return "full", routing
    if all(docs(path) for _, path in changes):
        return "docs", routing
    if all(docs(path) or (status == "M" and path == "achrix_test.go")
           or (benchmark_is_regular and status in {"A", "M"}
               and path == "achrix_bench_test.go")
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
    benchmark_is_regular = False
    if any(path == "achrix_bench_test.go" for _, path in changes):
        try:
            tree = subprocess.check_output(
                git + ["ls-tree", "-z", "HEAD", "--", "achrix_bench_test.go"],
                stderr=subprocess.PIPE,
            )
        except subprocess.CalledProcessError:
            return "full", True
        benchmark_is_regular = (
            tree.startswith((b"100644 blob ", b"100755 blob "))
            and tree.endswith(b"\tachrix_bench_test.go\0")
            and tree.count(b"\0") == 1
        )
    return classify(changes, benchmark_is_regular)


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
