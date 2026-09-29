# AChrix

**AChrix** (pronounced `ATCH-riks`; Persian: `اَچ‌ریکس`) is the AChWorks Application Foundation: an executable, reusable, versioned, AI-first base for building evolvable web-connected products with a minimal Core and composable Modules.

`AChrix` is the project and technology name; **Application Foundation** remains its architectural role.

AChrix is designed to reduce repeated engineering decisions while keeping product logic and future technology choices open.

It is intentionally **not** a new framework, mandatory monorepo, mandatory database, plugin marketplace, or microservice platform.

## Project Map

### Canonical intent

- [MASTER-SPEC.md](MASTER-SPEC.md) — project mission, durable principles, boundaries, non-goals, lifecycle expectations, and success criteria.
- [docs/PROJECT-MAP.md](docs/PROJECT-MAP.md) — where each kind of durable project truth lives.
- [achworks.yaml](achworks.yaml) — machine-readable Foundation identity/capability metadata for AChWorks discovery; not a live backlog/runtime-state store.

### Architecture

- [Architecture overview](docs/architecture/overview.md)
- [Module model](docs/architecture/module-model.md)
- [Contracts and interfaces](docs/architecture/contracts-and-interfaces.md)
- [Consumption and packaging](docs/architecture/consumption-and-packaging.md) — how products consume/upgrade shared Foundation code without permanent copy divergence.

### Engineering principles

- [Engineering principles](docs/principles/engineering-principles.md)
- [Reuse and evolutionary architecture](docs/principles/reuse-and-evolution.md)
- [Technology and infrastructure selection](docs/principles/technology-and-infrastructure.md)

### Lifecycle

- [Install, update, upgrade, migration, and recovery](docs/lifecycle/lifecycle-and-compatibility.md)

### AI / Web / Data / Security / Operations

- [AI-first and machine readability](docs/ai/ai-first-and-machine-readability.md)
- [SEO and semantic web](docs/web/seo-and-semantic-web.md)
- [Data and persistence](docs/data/data-and-persistence.md)
- [Security and authorization](docs/security/security-and-authorization.md)
- [Operability, performance, and cost](docs/operations/operability-performance.md)

### Development

- [Development roadmap](docs/development/roadmap.md)
- [ADR-0001 — Foundation shape](docs/decisions/ADR-0001-foundation-shape.md)
- [CONTRIBUTING.md](CONTRIBUTING.md)
- [AGENTS.md](AGENTS.md)

## Current state

The repository currently owns the durable Application Foundation intent, architecture, and development rules. The next accepted implementation outcome introduces the first executable/versioned Foundation Core and proves a real consumer boundary rather than creating a reference-only application or starter copy.

GitHub Issues own current actionable work. Documentation must not mirror live task status.

## Core philosophy

```text
reuse -> configure -> extend -> adapt -> extract -> replace/build when justified
```

Build the smallest correct thing today while preserving credible paths for tomorrow.

## AChWorks ecosystem relationship

**Koinon** (`AChWorks/koinon`) is the ecosystem-level governance/discovery/intake layer. This repository is AChrix, the executable Application Foundation owned independently from Koinon.

Suitable products may consume the versioned Application Foundation Core and reusable Modules, while keeping their own product/domain behavior, Issues, PRs, CI, releases, deployment truth, and runtime state.

Koinon generic contracts remain authoritative at the cross-project level; this Foundation specializes them for application products. Koinon is not a runtime dependency of the Foundation.
