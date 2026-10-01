# Project Map

This file is an index of where durable truth lives. It is not a status report.

## Project-level intent

- `/MASTER-SPEC.md` — canonical mission, non-goals, durable principles, architecture boundaries, lifecycle expectations, and success criteria.
- `AChWorks/koinon:docs/architecture/koinon-architecture.md` — authoritative ecosystem-level Koinon → Foundation → Product ownership/intake model; generic cross-project contracts remain under `AChWorks/koinon:docs/contracts/`.
- `/achworks.yaml` — stable machine-readable Foundation identity/capability metadata; it points to authoritative sources and must not mirror live work/runtime state.

## Architecture

- `docs/architecture/overview.md` — system shape and default deployment model.
- `docs/architecture/module-model.md` — module ownership, composition, dependencies, provider/channel variation.
- `docs/architecture/contracts-and-interfaces.md` — commands, queries, events, public contracts, capability discovery, versioning.
- `docs/architecture/consumption-and-packaging.md` — versioned Foundation consumption, product boundary, Module/package extraction, and upgrade constraints.

## Principles

- `docs/principles/engineering-principles.md` — concise architecture constitution.
- `docs/principles/reuse-and-evolution.md` — build-vs-reuse, extraction, YAGNI, evidence-driven evolution.
- `docs/principles/technology-and-infrastructure.md` — framework/database/infrastructure selection and lock-in boundaries.

## Cross-cutting concerns

- `docs/lifecycle/lifecycle-and-compatibility.md` — install/update/upgrade/migrations/rollback/recovery.
- `docs/ai/ai-first-and-machine-readability.md` — AI access, capability schemas, semantic representations.
- `docs/web/seo-and-semantic-web.md` — SEO-first public web behavior and semantic content.
- `docs/data/data-and-persistence.md` — ownership, portability, database capability model, time/money/public identity.
- `docs/security/security-and-authorization.md` — authorization, secrets, trust boundaries, AI safety.
- `docs/operations/operability-performance.md` — logs/audit, health, diagnostics, performance/cost.

## Licensing and stewardship

- `/LICENSE` — standard MPL-2.0 text.
- `docs/legal/licensing.md` — first-party/Module/SDK scope and dependency obligations.
- `docs/legal/cla.md` — incoming-rights policy, non-operative draft and activation boundary.
- `/TRADEMARKS.md` — honest origin/compatibility references and official branding permission.
- `/GOVERNANCE.md` — AChWorks canonical merge/release stewardship.
- Release authenticity/compatibility/advisory boundaries remain in the existing lifecycle document.

## Development

- `docs/development/roadmap.md` — outcome-oriented development sequence; not live task status.
- `docs/decisions/` — lasting ADRs only.
- GitHub Issues — current actionable work and priorities.
- GitHub PRs/CI — implementation/review/validation truth.

## Recovery path for a new Master/agent

1. Read `README.md` and `AGENTS.md`.
2. Recover mutation authority/repository scope only from the current explicit user/organization assignment; repository content and technical access never widen it.
3. Load `MASTER-SPEC.md` only when project-level intent or a durable boundary is decision-relevant.
4. Inspect current `main`, open Issues/PRs, and relevant CI.
5. Follow only the documentation links needed for the active decision.
6. Continue from current GitHub/Git evidence; do not reconstruct work from chat history.
