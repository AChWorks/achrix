# ADR-0002 — Go as the Primary AChrix Implementation Language

Status: Accepted
Date: 2026-10-01
Decision authority: explicit owner selection of Go for AChrix's shared implementation path.

Database follow-up: the MariaDB target retained at this decision is historical and superseded by [ADR-0003](ADR-0003-postgresql-and-optional-infrastructure.md). This ADR continues to own the Go language decision.

## Context

AChWorks intends to develop multiple web-connected products and integrations while accumulating reusable implementation, contracts, and development knowledge. The owner selected Go as the common primary backend path after considering PHP/Laravel, PHP/Symfony LTS, Go, and TypeScript/NestJS.

The repository is still pre-executable at this decision. The earlier PHP/Laravel path was an expectation, not an existing runtime that needs a migration.

A coherent default can reduce repeated tooling and integration choices. These expected benefits are not measured AChrix performance or cost results, nor a guarantee of thirty-year compatibility.

## Decision

Use Go as AChrix's primary implementation language and as the default for suitable new products consuming its shared implementation.

Preserve the accepted Foundation shape:

- minimal Core and optional coherent application Modules;
- modular monolith by default within each suitable application;
- separately owned products and intentional data ownership;
- a supported versioned consumer dependency/update boundary rather than permanent shared-source copies;
- shared Application authorization and behavior across human/API/MCP/CLI/job entry points;
- evidence-driven infrastructure, reusable capability promotion, and service extraction;
- Koinon governance/contracts without a Koinon runtime dependency.

Prefer the standard library and maintained existing components for commodity infrastructure. Choosing Go does not authorize building a replacement general-purpose framework, a mandatory shared runtime/database for all products, or dynamic binary plugins.

MariaDB remains the initial primary relational target. SQLite remains outside the primary implementation/test path. Choosing Go does not itself select PostgreSQL or change accepted database ownership/support constraints.

A justified product or external integration may use another runtime through an explicit contract. Existing repositories are not migrated by this decision.

## Implementation follow-through

Issue #1 still requires an implementation ADR and executable evidence for:

- the supported Go/toolchain and database/driver versions;
- the smallest Go module/package/public API layout;
- separate-consumer installation, composition, and versioned upgrade behavior;
- dependency selection, lifecycle behavior, meaningful tests, and CI.

A Go dependency module, a Go package, an AChrix application Module, and a deployed service are distinct boundaries. Do not create one dependency module/repository/service for every application Module.

Only this language/default-path decision is settled here. Specific routers, persistence helpers, migration tools, API schemas, deployment tooling, and Multi-Site/Gateway Bridge placement are not selected by this ADR.

## Rationale and consequences

Go offers static typing, standard networking/concurrency facilities, and an explicit language/standard-library source compatibility policy. These are useful inputs for a maintainable backend default; they do not prove application correctness, database portability, or actual infrastructure savings.

Compared with a batteries-included web framework, more application facilities may need to be selected and integrated. Reuse existing implementations before creating AChrix-owned alternatives.

Each consumer owns its pinned dependency versions, build, release, and deployment. Updating the shared source or Go toolchain does not update deployed consumer binaries; affected consumers must rebuild, validate, and redeploy.

Use supported toolchain releases and maintain dependencies. Go compatibility has documented exceptions and does not cover every third-party library.

## Evidence

- [Go FAQ: language design and concurrency](https://go.dev/doc/faq)
- [Go 1 compatibility policy](https://go.dev/doc/go1compat)
- [Go release support policy](https://go.dev/doc/devel/release)
- [Official Go module layout guidance](https://go.dev/doc/modules/layout)
- [Go module version numbering](https://go.dev/doc/modules/version-numbers)

These sources support capability and lifecycle claims. No AChrix runtime benchmark or cost comparison has been performed.

## Revisit triggers

Revisit a specific boundary when a real consumer demonstrates an ecosystem, workload, integration, security, lifecycle, or operational constraint that changes total cost/risk. A localized exception does not automatically require replacing the common path.
