# ADR-0003 — PostgreSQL and Optional Infrastructure Defaults

Status: Accepted
Date: 2026-10-01
Decision authority: owner direction to use PostgreSQL as the default and finalize technical infrastructure choices when they offer a development advantage.

## Context

AChrix should accelerate suitable products of different sizes while allowing independent product operation, release, and data ownership. The repository remains pre-executable: selecting defaults neither deploys infrastructure nor establishes tested support.

A common supported path can reduce repeated integration choices. Making every product run the same auxiliary services would also create resource, failure, upgrade, and support costs before there is product value. No AChrix workload benchmark or measured cost saving supports that commitment.

## Decision

### Relational persistence

Use ordinary PostgreSQL as the first primary relational implementation and integration-test target, replacing the MariaDB expectation retained by ADR-0001 and ADR-0002.

Use pgx for the PostgreSQL adapter. Prefer explicit SQL, with sqlc for query code generation where it removes meaningful manual mapping. Use golang-migrate v4 as the default application SQL migration mechanism. Pin and verify exact compatible versions before executable coupling; these selections do not establish a universal public persistence API.

Module-owned infrastructure contains queries, database-specific types, migrations, and optimizations. Public/application contracts should not leak driver types or private schemas where that impairs reuse. Do not create a universal ORM or lowest-common-denominator SQL abstraction.

Additional engines require real migrations and evidence for supported behavior, concurrency, constraints, and consumer compatibility. SQLite remains excluded from the primary implementation/test path.

TimescaleDB is optional for a concrete time-series workload. Keep ordinary transactional tables ordinary. Its hypertable constraints, PostgreSQL/extension compatibility, license/features, migrations, backup/restore, and exit path need explicit evaluation. Another PostgreSQL extension is also a module/profile requirement, not automatically a Core dependency.

### HTTP and diagnostics

Use Go net/http and explicit composition as the initial HTTP path; use log/slog for structured diagnostic logs. Keep Application authorization/behavior shared across HTTP, UI, CLI, MCP, and jobs.

When metrics/traces are justified, prefer OpenTelemetry instrumentation/export conventions. Select and pin mature signal-specific components; do not assume all Go SDK signals have the same stability. Collectors, dashboards, telemetry vendors, and a central monitoring service are optional operational choices.

### Shared cache

Valkey is the preferred optional shared-cache target for newly implemented cache workloads. Its BSD-3-Clause license and Redis OSS lineage fit a potentially public/self-hosted Foundation without requiring a Redis server in every consumer.

Compatibility must be verified for the commands, client, scripts, topology, and versions actually used. This does not promise compatibility with every Redis release, module, or feature.

A cache stores disposable derived data unless the owning capability explicitly defines another durability role. Define TTL, invalidation, memory/eviction bounds, and failure behavior for the real use case. Do not silently treat authorization, sessions, quotas, locks, or job durability as best-effort cache semantics. Do not implement an adapter before a consumer needs it.

### Jobs and events

For a product that needs durable background work, begin with a maintained PostgreSQL-backed job implementation, evaluated against transactional enqueueing, bounded concurrency/retries, cancellation, recovery, retention, and compatible job arguments. River's PostgreSQL path is the first candidate to validate; it is not selected as an implemented dependency or a mandatory worker.

At-least-once execution requires idempotent or reconcilable effects. A committed job, a transaction, or a broker does not by itself make an external payment/email/provider mutation happen exactly once. Define ambiguous-outcome reconciliation rather than blindly retrying an unknown mutation.

Use direct Application calls for immediate in-process results. Across products, use explicit authenticated HTTPS APIs/webhooks with bounded calls and attributable authorization. Add a transactional outbox or broker only when the actual delivery/consistency model requires it.

Kafka is not an initial runtime requirement or a pre-built Foundation adapter. Revisit it for durable replayable event streams, independent consumers, CDC/stream processing, or concrete throughput/retention requirements that justify broker operation. An ordinary background job does not alone select Kafka.

### Search

Start with product/module-owned PostgreSQL search for needs it actually satisfies. Evaluate indexing and normalization on representative Persian/multilingual queries; pg_trgm is an optional extension when similarity/indexed matching is useful, not a complete search relevance solution.

Do not select or build a universal dedicated search adapter without the first real search contract. Compare maintained engines such as OpenSearch and Meilisearch when required relevance, typo tolerance, filtering/facets, language behavior, volume, or latency warrants one.

A search index is a rebuildable projection. Explicitly preserve resource/tenant authorization, deletion propagation, and acceptable freshness; search results must not become the authoritative payment/inventory/security state.

### Deployment and central services

The initial Linux reference path should allow a Go application binary supervised by systemd. Products own deployment and secrets. Docker Compose/OCI packaging may provide reproducible development or a product deployment profile, but containers remain optional for Foundation consumption.

Keep configuration external, shutdown graceful, resource use bounded, readiness local to necessary dependencies, and schema activation deliberate. These properties ease later orchestration without implementing Kubernetes APIs or charts now.

Kubernetes is not an initial deployment dependency. Revisit it for concrete multi-node scheduling, rollout, isolation, availability, or operating-team requirements; the availability of managed/lightweight Kubernetes can change the comparison. Binary/systemd or Compose alone does not provide high availability.

Sharing a Foundation dependency does not require one running AChrix service or one database for every product. Products retain separately owned data and release boundaries; several product databases may share infrastructure where isolation and resource budgets allow it.

Extract or consume a central service only for a demonstrated capability and explicit owner, failure, authorization, lifecycle, and operational boundary. Do not create a central router/service through which every product operation must pass.

## Owner decisions still open

The owner accepted independently owned product accounts with optional SSO on 2026-10-01; see [Security and Authorization](../security/security-and-authorization.md#product-accounts-and-optional-sso). This policy does not select an identity provider or require a central identity service.

The AChrix public license and distribution policy remain open. The owner's request to explain a WordPress-like license is a review direction, not acceptance of a specific license grant.

Multi-Site and Gateway Bridge placement gates remain unchanged. No consumer or other repository is migrated by this decision.

## Implementation and support

Issue #1 still proves the smallest versioned Foundation plus an independent consumer on real PostgreSQL, supported toolchain/dependency pins, authorization behavior, and CI. Do not expand it to implement cache, queue, search, TimescaleDB, identity, or orchestration modules solely because paths are named here.

For each later optional profile, support requires real compatibility and failure/recovery evidence. Configuration metadata or an interface alone is not proof.

Before adding infrastructure, identify the required behavior and representative resource/cost constraints. Test the smallest mechanism that satisfies them; revisit a default when evidence changes the result. Total cost includes integration, operations, RAM/CPU, disk/I/O, connections, network, telemetry, retention, backup, upgrades, and recovery.

## Evidence

- [PostgreSQL support and upgrade policy](https://www.postgresql.org/support/versioning/)
- [PostgreSQL transaction isolation](https://www.postgresql.org/docs/current/transaction-iso.html)
- [PostgreSQL pg_trgm](https://www.postgresql.org/docs/current/pgtrgm.html)
- [pgx driver and standard database/sql adapter](https://github.com/jackc/pgx)
- [sqlc database/language support](https://docs.sqlc.dev/en/latest/reference/language-support.html)
- [golang-migrate drivers and versioned migrations](https://github.com/golang-migrate/migrate)
- [TimescaleDB extension and analytics capabilities](https://github.com/timescale/timescaledb)
- [TimescaleDB license boundaries](https://github.com/timescale/timescaledb/blob/main/LICENSE)
- [Valkey license](https://github.com/valkey-io/valkey/blob/unstable/COPYING)
- [Valkey migration and Redis OSS compatibility scope](https://valkey.io/topics/migration/)
- [Current Redis license options](https://redis.io/legal/licenses/)
- [River PostgreSQL integration](https://riverqueue.com/docs)
- [River worker execution and idempotency](https://riverqueue.com/docs/reliable-workers)
- [River transactional enqueueing](https://riverqueue.com/docs/transactional-enqueueing)
- [Kafka event-streaming model](https://kafka.apache.org/intro/)
- [Kubernetes production considerations](https://kubernetes.io/docs/setup/production-environment/)
- [OpenSearch language analyzers](https://docs.opensearch.org/latest/analyzers/language-analyzers/index/)
- [Meilisearch Persian support update](https://www.meilisearch.com/blog/september-2025-updates)
- [OpenTelemetry Go signal status](https://opentelemetry.io/docs/languages/go/)

These sources establish capabilities and constraints. Expected development benefits are recommendations from those facts, not measured AChrix speed, capacity, availability, or cost results.
