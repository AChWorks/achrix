# Contributing

## Work and review

Start from current main, the relevant Issue/PR and [Project Map](docs/PROJECT-MAP.md). Read only the specification/rules relevant to the change, inspect existing capabilities, and resolve ownership before creating parallel functionality.

Use a focused branch and Pull Request for tracked changes to intent, architecture, implementation, CI, lifecycle, metadata or working rules. Link the existing outcome when applicable; keep one meaningful change reviewable together. Canonical integration targets main. Do not overwrite unrelated work or force-push.

Validate the exact candidate with change-appropriate checks and review the full diff. The active `main` ruleset requires PRs, squash merges and resolved review threads, blocks force pushes/deletion, and has no bypass actors. It requires zero approval votes so a sole maintainer can integrate reviewed low/medium-risk work; this does not waive risk-required independent review or owner decisions. Never describe self-review as independent.

The existing ruleset binds the actual `baseline` check to GitHub Actions (integration ID `15368`) and requires it to pass against the current base before integration. This check always reports the selected validation scope. Non-runtime documentation changes need no runtime proof; changes only to the existing Core test file or known Core benchmark source use Core checks. Runtime, dependencies, migrations, CI and unknown scope retain the full Foundation, isolated consumer and PostgreSQL proof. [Operations](docs/operations/operability-performance.md#validation-commands) owns commands and the conservative selector. [Go quality and benchmarks](docs/development/go-quality.md) owns pinned analysis tools, vulnerability triage and controlled performance comparison. Recorded review remains necessary. Inspect effective GitHub rules before integration rather than relying on this text alone.

Done means the accepted behavior is implemented, required checks pass, compatibility/state/recovery implications are handled and affected canonical docs are current. Integration and production delivery are separate facts. Current documents contain rules still in force; obsolete decisions remain in Git history.

## Validation selection

Choose checks from the behavior, dependencies, invariants and failure modes that the change can actually affect. Explain a non-obvious selection briefly in the PR; no test-plan artifact is needed. A mechanically provable non-behavioral edit may need only inspection/syntax/diff checks. Confidence alone is not proof when interfaces, authorization, persistence, concurrency, dependencies or environment assumptions changed.

Batch coherent edits before one meaningful validation pass. After a localized correction, rerun the discriminating test and directly affected dependants; reuse earlier passing evidence for unchanged inputs and assumptions. A new SHA alone does not invalidate all evidence or require another full local suite. Respect the currently applicable CI/integration gate, report exactly what ran and expand validation only for new failures, unresolved interactions or a required delivery proof. Never label reused evidence as a fresh execution.

Use native targeted commands during development; the default full command remains available for cross-boundary validation. A build/runtime input must never enter the documentation exception; update routing in the same change if a file gains that role. Shard a still-required slow suite only after timing shows test execution is the bottleneck and isolation/completeness can be preserved; do not duplicate setup or create a matrix for short tests.

## Issue classification

Use `type:idea` for unactivated feature/placement proposals awaiting evidence or a decision, matching Koinon's meaning. Activation reconciles scope, evidence and labels; a label never overrides execution gates. Use existing `enhancement` for accepted feature/outcome work and `documentation` for document/policy work. Preserve unrelated labels and update existing work before creating another Issue.

## Architecture and dependencies

Follow [Engineering principles](docs/principles/engineering-principles.md) and the owning topic document. Review the actual control flow/data/resource boundary, not only whether a preferred tool or pattern is present. Performance-sensitive changes use representative benchmark/profile/query evidence when material; security-sensitive changes use focused threat-aware tests/review and current dependency/protocol guidance. A lasting consequential choice may need an ADR; small implementation choices do not. Dependencies need maintenance/security/license/compatibility/upgrade/cost justification, not a wrapper by default.

## Rights and sensitive data

[Licensing](docs/legal/licensing.md) owns file/artifact scope; preserve third-party notices and use actual-license SPDX headers for new source. Templates/generated files need unambiguous scope. [CLA](docs/legal/cla.md) requires verified signed versioned consent and sufficient contributor/employer rights before covered external code merges; a PR checkbox or agent assertion is insufficient.

Keep signatures, private contributor records, credentials, keys, customer data and restricted payloads out of public Git. [Governance](GOVERNANCE.md) owns merge/release authority. Extensions use published contracts; an independently licensed compatible Module does not become official by claiming compatibility.
