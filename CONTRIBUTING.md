# Contributing

## Work and review

Start from current main, the relevant Issue/PR and [Project Map](docs/PROJECT-MAP.md). Read only the specification/rules relevant to the change, inspect existing capabilities, and resolve ownership before creating parallel functionality.

Use a focused branch and Pull Request for tracked changes to intent, architecture, implementation, CI, lifecycle, metadata or working rules. Link the existing outcome when applicable; keep one meaningful change reviewable together. Canonical integration targets main. Do not overwrite unrelated work or force-push.

Validate the exact candidate with change-appropriate checks and review the full diff. Issue #1 introduces executable checks and the strongest suitable enforceable policy supported by the current plan; until then PR review plus relevant validation is the boundary.

Done means the accepted behavior is implemented, required checks pass, compatibility/state/recovery implications are handled and affected canonical docs are current. Integration and production delivery are separate facts. Current documents contain rules still in force; obsolete decisions remain in Git history.

## Architecture and dependencies

Follow [Engineering principles](docs/principles/engineering-principles.md) and the owning topic document. A lasting consequential choice may need an ADR; small implementation choices do not. Dependencies need maintenance/security/license/compatibility/upgrade/cost justification, not a wrapper by default.

## Rights and sensitive data

[Licensing](docs/legal/licensing.md) owns file/artifact scope; preserve third-party notices and use actual-license SPDX headers for new source. Templates/generated files need unambiguous scope. [CLA](docs/legal/cla.md) requires verified signed versioned consent and sufficient contributor/employer rights before covered external code merges; a PR checkbox or agent assertion is insufficient.

Keep signatures, private contributor records, credentials, keys, customer data and restricted payloads out of public Git. [Governance](GOVERNANCE.md) owns merge/release authority. Extensions use published contracts; an independently licensed compatible Module does not become official by claiming compatibility.
