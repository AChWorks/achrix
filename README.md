# AChWorks Application Foundation

Reusable, AI-first application foundation for building evolvable web-connected products with a minimal core and composable modules.

The Foundation is designed to reduce repeated engineering decisions while keeping product logic and future technology choices open.

It is intentionally **not** a new framework, mandatory monorepo, mandatory database, plugin marketplace, or microservice platform.

## Project Map

### Canonical intent

- [MASTER-SPEC.md](MASTER-SPEC.md) — project mission, durable principles, boundaries, non-goals, lifecycle expectations, and success criteria.
- [docs/PROJECT-MAP.md](docs/PROJECT-MAP.md) — where each kind of durable project truth lives.

### Architecture

- [Architecture overview](docs/architecture/overview.md)
- [Module model](docs/architecture/module-model.md)
- [Contracts and interfaces](docs/architecture/contracts-and-interfaces.md)

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

The repository currently owns the durable project foundation and development rules. Executable runtime code should be introduced only through the next accepted implementation outcome rather than being invented speculatively during bootstrap.

GitHub Issues own current actionable work. Documentation must not mirror live task status.

## Core philosophy

```text
reuse -> configure -> extend -> adapt -> extract -> replace/build when justified
```

Build the smallest correct thing today while preserving credible paths for tomorrow.
