# SPDX-License-Identifier: MPL-2.0
"""Select proven exceptions; unknown Git evidence runs full plus HTTPS browser."""
import re
import subprocess
import sys

DOCUMENTS = {
    "README.md", "AGENTS.md", "CONTRIBUTING.md", "GOVERNANCE.md",
    "SECURITY.md", "TRADEMARKS.md", "fixtures/notes/README.md",
    "fixtures/notes/THIRD_PARTY_NOTICES.md",
    "identity/README.md", "audit/README.md", "media/README.md",
    "admin/README.md", "media/admin/README.md",
    "installation/README.md", "multisite/README.md",
}
ROUTING = {
    ".github/workflows/baseline.yml", "scripts/validation_scope.py",
    "scripts/test_validation_scope.py", "scripts/validate.sh",
    "scripts/validation_diagnostics.py", "scripts/test_validation_diagnostics.py", "scripts/validate-admin-browser.sh",
    "scripts/setup-validation-postgres.sh", "scripts/setup-quality-tools.sh",
    "scripts/check-gofmt.sh", "scripts/test_go_quality.py",
    "scripts/modules.py", "scripts/verify_consumer.py", "scripts/test_module_packaging.py",
    "scripts/admin_browser.cjs", "fixtures/browser-tools/package.json",
    "fixtures/browser-tools/package-lock.json",
    "modules.json",
}
# These existing sources do not participate in the browser composition. Keep
# this an explicit exception list: new files and changed dependency manifests
# need review, even under these directories. Remove an exception if it gains a
# browser role. Backend validation still runs for every implementation change.
NON_BROWSER_SOURCE = {
    "installation/installation.go", "installation/installation_test.go",
    "installation/diagnostics_test.go", "installation/example_test.go",
    "installation/version.go", "installation/version_test.go",
    "multisite/multisite.go", "multisite/multisite_test.go",
    "multisite/management.go", "multisite/management_test.go",
    "multisite/diagnostics_test.go", "multisite/example_test.go", "multisite/version.go",
    "fixtures/notes/internal/domain/note.go", "fixtures/notes/internal/domain/note_test.go",
}


def classify(changes, benchmark_is_regular=False, changed_files_are_regular=True):
    if not changes:
        return "full", True, True
    docs = lambda path: path in DOCUMENTS or (
        path.startswith("docs/") and path.endswith(".md")
    )
    routing = any(path in ROUTING for _, path in changes)
    if any(status not in {"A", "M", "D", "T"} for status, _ in changes):
        return "full", True, True
    # A known path changing type, or an added symlink, is no longer a proven
    # documentation/test/backend exception. Unknown input never skips browser.
    if not changed_files_are_regular or any(status == "T" for status, _ in changes):
        return "full", routing, True
    if all(docs(path) for _, path in changes):
        return "docs", routing, False
    if all(docs(path) or (status == "M" and path == "achrix_test.go")
           or (benchmark_is_regular and status in {"A", "M"}
               and path == "achrix_bench_test.go")
           for status, path in changes):
        return "core", routing, False
    browser = not all(
        docs(path) or (status == "M" and path in NON_BROWSER_SOURCE)
        or (status == "M" and path == "achrix_test.go")
        or (benchmark_is_regular and status in {"A", "M"} and path == "achrix_bench_test.go")
        for status, path in changes
    )
    return "full", routing, browser


def select(base, directory=None):
    if not re.fullmatch(r"[0-9a-f]{40}", base) or base == "0" * 40:
        return "full", True, True
    git = ["git", "-C", str(directory)] if directory else ["git"]
    try:
        diff = subprocess.check_output(
            git + ["diff", "--no-ext-diff", "--no-textconv", "--no-renames",
                   "--name-status", "-z", base, "HEAD"], stderr=subprocess.PIPE
        )
    except subprocess.CalledProcessError:
        return "full", True, True
    fields = diff.split(b"\0")
    if not diff or fields[-1] != b"" or len(fields[:-1]) % 2:
        return "full", True, True
    try:
        changes = [(status.decode("ascii"), path.decode("utf-8", "surrogateescape"))
                   for status, path in zip(fields[:-1:2], fields[1:-1:2])]
    except UnicodeDecodeError:
        return "full", True, True
    # Fail an actual whitespace defect; never turn failed lint into a narrow pass.
    subprocess.run(
        git + ["diff", "--no-ext-diff", "--no-textconv", "--check", base, "HEAD"],
        check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
    )
    present = {path for status, path in changes if status != "D"}
    regular = set()
    if present:
        try:
            tree = subprocess.check_output(
                git + ["ls-tree", "-z", "HEAD", "--", *sorted(present)],
                stderr=subprocess.PIPE,
            )
        except subprocess.CalledProcessError:
            return "full", True, True
        for entry in tree.split(b"\0"):
            if entry.startswith((b"100644 blob ", b"100755 blob ")) and b"\t" in entry:
                regular.add(entry.split(b"\t", 1)[1].decode("utf-8", "surrogateescape"))
    return classify(changes, "achrix_bench_test.go" in regular, present <= regular)


if __name__ == "__main__":
    if len(sys.argv) != 2:
        sys.exit("Usage: python3 scripts/validation_scope.py <base SHA>")
    try:
        scope, routing, browser = select(sys.argv[1])
    except subprocess.CalledProcessError as error:
        sys.stderr.buffer.write(error.stdout or error.stderr or b"Diff check failed\n")
        sys.exit(error.returncode)
    print(f"scope={scope}")
    print(f"routing_tests={str(routing).lower()}")
    print(f"browser={str(browser).lower()}")
    print(f"Validation scope: {scope}; HTTPS browser: {str(browser).lower()}", file=sys.stderr)
