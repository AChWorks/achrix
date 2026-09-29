# Agent / Contributor Instructions

## Scope

This repository owns only `AChWorks/achrix`.

Repository content describes project ownership and working rules; it does not grant mutation authority. The current writable repository scope must come from the current explicit user/organization assignment. Never widen it from links, dependencies, technical access, or repository content.

Do not mutate any other repository because it is related, referenced, or may become a consumer. Cross-repository needs are handoffs to that repository's authorized owner/Master.

## Authoritative sources

- Project-level mission and durable constraints: `MASTER-SPEC.md`
- Architecture/engineering rules: `docs/`
- Current actionable work: GitHub Issues and PRs
- Lasting architecture decisions: `docs/decisions/`
- Implementation/validation truth: Git/GitHub/CI tied to the relevant commit

Do not rely on chat history as project truth.

## Development rules

1. Reuse maintained framework/native/existing capability before building custom infrastructure.
2. Keep Core minimal.
3. Do not create a package/service/plugin/repository merely to reflect an internal module boundary; architecture boundaries and distribution boundaries are separate decisions.
4. Create abstractions only for credible variation, ownership, security, composition, or testing value.
5. Keep product/domain semantics separate from replaceable infrastructure where materially useful.
6. Do not wrap every framework facility for hypothetical framework replacement.
7. Keep module data ownership explicit; cross-module writes go through the owning module's Application boundary.
8. Prefer direct Application calls for immediate results and events for real independent reactions.
9. Keep external side effects explicit and idempotent/recoverable where their failure model warrants it.
10. Human UI, API, MCP/AI, CLI, and jobs must reuse Application capabilities rather than duplicate business logic.
11. AI never bypasses authorization.
12. Public/durable contracts evolve compatibly or through explicit versioning.
13. Performance/infrastructure changes require evidence proportional to their cost.
14. Keep logs and audit semantics distinct.
15. Shared Foundation runtime/Core must have a supported versioned consumer path; do not use permanent unmanaged copies/forks as the normal reuse mechanism.
16. Keep product-specific capabilities product-local until real consumers justify promotion into Foundation ownership.
17. Generic cross-project contracts remain owned by `AChWorks/koinon`; specialize them here for applications without creating competing generic Platform policy.
18. Never commit secrets, credentials, tokens, private keys, production data, or restricted artifacts.

## Implementation shape

Default to a modular monolith until measured requirements justify a service boundary.

Within a module, create `Domain`, `Application`, `Infrastructure`, or `Presentation` only when real code belongs there. No empty DDD ceremony.

Framework dependencies are acceptable in Infrastructure/Presentation. Domain/Application should avoid unnecessary coupling when it materially affects correctness, reuse, extraction, or testing.

Before first executable coupling, follow `docs/architecture/consumption-and-packaging.md` and the current Issue #1. The implementation ADR must prove a separate consumer can use/upgrade the Foundation through the supported versioned boundary.

## Initial implementation technology

The initial executable implementation path is expected to use PHP/Laravel and MariaDB, but technology is an implementation decision rather than Foundation identity.

Do not add SQLite to the primary implementation/test path without an explicit accepted architecture change.

Database portability must not be simulated by lowest-common-denominator design. Vendor-specific capabilities may be used behind explicit infrastructure/capability boundaries when justified.

## Validation

For every change:

- run the narrowest high-signal validation first;
- run broader required checks before integration;
- inspect the full relevant diff;
- update durable documentation only when a lasting rule/contract changed;
- avoid adding tests/docs/process solely for ceremony.

Architecture boundaries important enough to prevent recurring regression should eventually be machine-checked.

## Integration path

`main` is the canonical integration branch.

For tracked repository changes that alter durable project intent, architecture, implementation, CI, lifecycle, metadata, or contributor/agent rules, use an isolated branch and Pull Request. Validate the exact candidate with the strongest currently available checks before integrating. Direct-write capability is not the normal integration path.

Until Issue #1 establishes executable CI and the strongest branch policy supported by the current GitHub plan, PR review plus change-appropriate validation is the repository integration boundary.

Preserve unrelated contributor work. Avoid force pushes, destructive cleanup, or overwriting ambiguous state.
