# Contributing

## Work and review

Start from the repository's current canonical integration branch, the relevant Issue/PR and [Project Map](docs/PROJECT-MAP.md). Read only the specification/rules relevant to the change, inspect existing capabilities, and resolve ownership before creating parallel functionality.

For hands-on setup, follow [Develop and extend a component](docs/architecture/consumption-and-packaging.md#develop-and-extend-a-component) and the [Notes setup and validation guide](fixtures/notes/README.md#setup-and-validation); [Operations](docs/operations/operability-performance.md#supported-environment) owns the supported tool prerequisites.

Use a focused branch and Pull Request for tracked changes to intent, architecture, implementation, CI, lifecycle, metadata or working rules. Link the existing outcome when applicable and keep one meaningful change reviewable together. Do not overwrite unrelated work or force through protected state.

Validate the exact candidate with change-appropriate checks and review the full diff. Live GitHub rulesets own enforced integration branch, merge method, approval/check/thread requirements, branch protection and bypass policy; inspect them before integration rather than duplicating their current settings here. Repository rules do not waive risk-required independent review or owner decisions. Never describe self-review as independent.

The live repository rules own which checks are required against the current base before integration; do not hard-code provider/integration identifiers in durable documentation. When the repository-defined `baseline` check is required, it reports the selected validation scope. Non-runtime documentation changes need no runtime proof; changes only to the existing Core test file or known Core benchmark source use Core checks. Runtime, dependencies, migrations, CI and unknown scope retain the full Foundation, isolated consumer and PostgreSQL proof. [Operations](docs/operations/operability-performance.md#validation-commands) owns commands and the conservative selector. [Go quality and benchmarks](docs/development/go-quality.md) owns pinned analysis tools, vulnerability triage and controlled performance comparison. The same required check conditionally runs the existing HTTPS browser proof for relevant or ambiguous changes, using the complete diff; known unrelated backend changes can still require full backend checks without browser setup. Recorded review remains necessary. Inspect effective GitHub rules before integration rather than relying on this text alone.

Done means the accepted behavior is implemented, required checks pass, compatibility/state/recovery implications are handled and affected canonical docs are current. Integration and production delivery are separate facts. Current documents contain rules still in force; obsolete decisions remain in Git history.

README is the user entry point: explain the project, available capabilities, requirements and how to start. Keep detailed architecture, API/ABI contracts, engineering process and validation evidence in their owning documentation or GitHub records; link them from README instead of duplicating them.

Durable product/architecture/usage documentation should describe stable contracts and point to the authoritative owner of mutable truth rather than duplicate the current release tag, commit SHA, provider ID or other rotating evidence. Exact dependency/release identities belong in manifests/lockfiles, release and migration records, active Issues/PRs/CI evidence, or other artifacts whose purpose is exact reproducibility. Versioned migration history, supported-environment matrices and measured benchmark/tool evidence may retain exact versions because those documents explicitly own that version-sensitive truth. Security/provenance/trust pins may also remain exact in the machine-enforced configuration or verifier that owns that trust boundary; do not weaken them merely to remove a hard-coded value. Stable Issue/ADR identifiers may remain as navigation or decision provenance; do not copy their changing state into durable rules. Executable validation/security surfaces may intentionally repeat exact compatibility or trust pins when each is an independent fail-closed check whose change deserves explicit review; centralize only a value that truly has one owner and would otherwise be duplicated synchronization state.

## Validation selection

Choose checks from the behavior, dependencies, invariants and failure modes that the change can actually affect. Explain a non-obvious selection briefly in the PR; no test-plan artifact is needed. A mechanically provable non-behavioral edit may need only inspection/syntax/diff checks. Confidence alone is not proof when interfaces, authorization, persistence, concurrency, dependencies or environment assumptions changed.

Batch coherent edits before one meaningful validation pass. After a localized correction, rerun the discriminating test and directly affected dependants; reuse earlier passing evidence for unchanged inputs and assumptions. A new SHA alone does not invalidate all evidence or require another full local suite. Respect the currently applicable CI/integration gate, report exactly what ran and expand validation only for new failures, unresolved interactions or a required delivery proof. Never label reused evidence as a fresh execution.

Use native targeted commands during development; the default full command remains available for cross-boundary validation. A build/runtime input must never enter the documentation exception; update routing in the same change if a file gains that role. Shard a still-required slow suite only after timing shows test execution is the bottleneck and isolation/completeness can be preserved; do not duplicate setup or create a matrix for short tests.

## Issue classification

Use `type:idea` for unactivated feature/placement proposals awaiting evidence or a decision, matching Koinon's meaning. Activation reconciles scope, evidence and labels; a label never overrides execution gates. Use existing `enhancement` for accepted feature/outcome work and `documentation` for document/policy work. Preserve unrelated labels and update existing work before creating another Issue.

## Architecture and dependencies

Follow [Engineering principles](docs/principles/engineering-principles.md) and the owning topic document. Review the actual control flow/data/resource boundary, not only whether a preferred tool or pattern is present. Performance-sensitive changes use representative benchmark/profile/query evidence when material; security-sensitive changes use focused threat-aware tests/review and current dependency/protocol guidance. A lasting consequential choice may need an ADR; small implementation choices do not. Dependencies need maintenance/security/license/compatibility/upgrade/cost justification, not a wrapper by default.

When changing failure paths, prove useful cause/phase/field/outcome evidence as well as outward data protection. Capture causes before public canonicalization, preserve confirmed and uncertain mutation results, and keep suppression counts observable after traffic stops; [Operations](docs/operations/operability-performance.md#diagnosis-and-audit) owns the diagnostic contract.

New or materially changed application capabilities must address [internal and external programmatic coverage](docs/architecture/contracts-and-interfaces.md#internal-and-external-programmatic-coverage): name the relevant consumers and HTTP/MCP paths, complete applicable adapters and tests with the feature, or state a justified exception or explicitly owned unfinished scope. Keep the owning contract/README and examples aligned; do not claim coverage from Go exports or schema generation alone. Reuse the same service in-process without a mandatory network hop.

Programmatic-access changes must prove the ordinary application policy still owns authority, current connection/channel restrictions only narrow it, and idle configuration does not start per-connection work. Include a real consumer and a discriminating post-dispatch failure check when mutations are exposed. A resource-server fixture or existing browser regression pass is not evidence of OAuth onboarding or a real AI client's interoperability; record those separately under the [Bridge contract](bridge/README.md).

## Rights and sensitive data

[Licensing](docs/legal/licensing.md) owns file/artifact scope; preserve third-party notices and use actual-license SPDX headers for new source. Templates/generated files need unambiguous scope. [CLA](docs/legal/cla.md) requires verified signed versioned consent and sufficient contributor/employer rights before covered external code merges; a PR checkbox or agent assertion is insufficient.

Keep signatures, private contributor records, credentials, keys, customer data and restricted payloads out of public Git. [Governance](GOVERNANCE.md) owns merge/release authority. Extensions use published contracts; an independently licensed compatible Module does not become official by claiming compatibility.
