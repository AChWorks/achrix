# ADR-0003 — PostgreSQL Baseline and Optional Infrastructure

Status: Accepted. Date: 2026-10-01. This decision defines the first baseline, not a promise of support for unimplemented providers.

## Context

The first reusable Foundation consumer needs real transactional data, explicit migration and recovery behavior, and a supportable test environment. A simultaneous multi-database abstraction or mandatory auxiliary service would add compatibility, operational and failure costs before a consumer demonstrates a need.

## Decision and trade-offs

- Use ordinary **PostgreSQL** for the initial relational implementation and integration proof. PostgreSQL-backed components and the Notes consumer use `pgx/v5` and explicit SQL behind their Infrastructure boundaries; dependency manifests, not this ADR, own exact versions. SQLite is not an equivalent test substitute. Any alternative engine requires real schema, isolation/concurrency, migration and failure/restore evidence.
- Keep Module-owned schema and migrations explicit. The original Notes fixture's bounded native PostgreSQL transaction/advisory-lock mechanism does not constitute a generic migration framework; broader tooling is chosen only when a real schema evolution requires it.
- Keep caching, search engines, background execution, brokers, observability services, container runtimes and orchestration **optional**. A product may use a maintained implementation that fits its workload, support profile and cost, but adopting AChrix never requires those services or a central application database/service.
- Decide a new provider when an owning product/Module has a real consumer, correctness/authorization/failure requirements and measured operational need. A list of possible technologies is not an accepted dependency, a supported profile, or a reason to build unused adapters.

PostgreSQL gives the baseline an ordinary relational transaction/constraint model and a reproducible retained-data proof, at the cost of requiring a real PostgreSQL environment for that supported path. Optional infrastructure preserves low starting complexity; specialized workloads may justify additional services after explicit validation.

## Owners and revisit criteria

[Data](../data/data-and-persistence.md) owns domain/schema invariants and database semantics; [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns migration, backup and recovery; [Operations](../operations/operability-performance.md#supported-environment) owns tested versions and profiles. [Module model](../architecture/module-model.md) owns Search and background-work placement; [Contracts](../architecture/contracts-and-interfaces.md) owns safe effects. Actual module manifests and GitHub validation/release evidence establish which providers and versions are implemented.

Revisit the baseline or add an auxiliary service when a real consumer supplies sufficient portability, isolation, correctness, performance or operating-cost evidence; record that specific decision at its owner rather than expanding this ADR into a speculative technology catalog.
