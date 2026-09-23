# Project Map

This file is an index of where durable truth lives. It is not a status report.

## Project-level intent

- `/MASTER-SPEC.md` — canonical mission, non-goals, durable principles, architecture boundaries, lifecycle expectations, and success criteria.

## Architecture

- `docs/architecture/overview.md` — system shape and default deployment model.
- `docs/architecture/module-model.md` — module ownership, composition, dependencies, provider/channel variation.
- `docs/architecture/contracts-and-interfaces.md` — commands, queries, events, public contracts, capability discovery, versioning.

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

## Development

- `docs/development/roadmap.md` — outcome-oriented development sequence; not live task status.
- `docs/decisions/` — lasting ADRs only.
- GitHub Issues — current actionable work and priorities.
- GitHub PRs/CI — implementation/review/validation truth.

## Recovery path for a new Master/agent

1. Read `README.md` and `AGENTS.md`.
2. Load `MASTER-SPEC.md` only when project-level intent or a durable boundary is decision-relevant.
3. Inspect current `main`, open Issues/PRs, and relevant CI.
4. Follow only the documentation links needed for the active decision.
5. Continue from current GitHub/Git evidence; do not reconstruct work from chat history.
