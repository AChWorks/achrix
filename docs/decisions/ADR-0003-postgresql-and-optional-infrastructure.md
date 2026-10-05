# ADR-0003 — PostgreSQL and Infrastructure Defaults

Status: Accepted. Date: 2026-10-01. Defaults select an integration path; support exists only after versioned compatibility/failure evidence.

## Decision

Use ordinary PostgreSQL for the first implementation and integration tests. Auxiliary services stay optional so small products avoid unnecessary resource, failure and operational cost. Choose maintained components; do not build a universal ORM/provider framework.

| Need | Starting choice | Adoption boundary |
| --- | --- | --- |
| Relational store | [PostgreSQL](https://www.postgresql.org/support/versioning/) | Baseline; another engine needs real migrations/semantic tests; SQLite is excluded |
| PostgreSQL access | [pgx](https://github.com/jackc/pgx), explicit SQL; [sqlc](https://docs.sqlc.dev/en/latest/reference/language-support.html) when useful | Pin compatibility; keep driver/schema details in owned Infrastructure |
| SQL migrations | [golang-migrate v4](https://github.com/golang-migrate/migrate) for broader migration needs; native PostgreSQL transaction/lock for the bounded one-file fixture | Owned schema; baseline checksum/atomicity proof is private consumer install behavior under [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#initial-fixture-lifecycle-and-database-scope), not a new shared migration framework |
| Database backup/recovery | Maintained PostgreSQL logical backup first where suitable; physical/base backup plus WAL/PITR or managed equivalents when the supported profile requires them | [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#backup-and-recovery) and Recovery #49 own profile/proof; no custom backup format or Core host authority |
| HTTP and diagnostics | Go `net/http`, `log/slog` | Explicit composition and shared Application authorization |
| Simple search | PostgreSQL FTS; optional [pg_trgm](https://www.postgresql.org/docs/current/pgtrgm.html) | Reusable Search #48 starts here; prove normalization/relevance on representative Persian/multilingual queries plus authorization/freshness bounds |
| Advanced search | [OpenSearch](https://docs.opensearch.org/latest/analyzers/language-analyzers/index/); compare a simpler engine where fit warrants it | Adopt only for measured relevance/filtering/volume/latency/operations need; keep the Search provider contract narrow and do not make a cluster mandatory |
| Shared cache | [Valkey](https://valkey.io/topics/migration/) | Real cache workload; verify commands/client/topology/version, not all Redis features |
| Durable jobs | [River with PostgreSQL](https://riverqueue.com/docs) as first candidate | Transactional enqueue, bounded workers/retries, cancellation/recovery/retention and compatible arguments |
| Long durable workflows | [Temporal](https://docs.temporal.io/workflows) | Durable progress/waits/recovery justify service, replay and versioning costs |
| Durable messaging | [NATS JetStream](https://docs.nats.io/concepts/jetstream) | Independent consumers/replay/delivery needs justify a broker |
| Observability | [OpenTelemetry](https://opentelemetry.io/docs/languages/go/); [SigNoz](https://signoz.io/docs/install/docker/) as initial backend candidate | Select mature signals; collection/ClickHouse/storage/retention fit the operational budget |
| Delivery | Optional OCI/container packaging; Go binary/systemd also valid | Products own deployment/secrets; a Foundation dependency does not require Docker |
| Orchestration | [Kubernetes](https://kubernetes.io/docs/setup/production-environment/) later | Concrete multi-node scheduling, isolation, rollout/availability and operator requirements |
| Interactive frontend | TypeScript | Product selects framework and any Node production need |
| AI/data runtime | Python where ecosystem value warrants it | Calling a model API from Go alone requires no Python service |
| Time-series profile | [TimescaleDB](https://github.com/timescale/timescaledb) when justified | Check extension/PG compatibility, hypertable constraints, [license](https://github.com/timescale/timescaledb/blob/HEAD/LICENSE), migrations/restore and exit path |

## Correctness and operating constraints

Keep vendor optimizations and required database capabilities explicit under [Data](../data/data-and-persistence.md); ordinary transactional tables need no specialized extension.

Caches hold disposable derived state unless the capability explicitly chooses another durability role. Define TTL, invalidation, eviction/memory bounds and failures; sessions, permission decisions, quotas, locks or job state must not silently adopt best-effort semantics. [Valkey's license](https://github.com/valkey-io/valkey/blob/unstable/COPYING) is part of its fit; verify actual dependency terms when adopting it.

At-least-once jobs/workflows require idempotent or reconcilable effects under [Contracts](../architecture/contracts-and-interfaces.md). Do not run the same owned workflow in River and Temporal. Messaging defines acknowledgments, persistence, retention/replication and any necessary PostgreSQL-to-broker outbox. A broker is not a workflow engine or cross-system exactly-once guarantee. Kafka can be evaluated for concrete event-stream/CDC/replay/throughput needs, not ordinary jobs.

Search is a rebuildable projection: preserve Application authorization, deletion/permission propagation and acceptable freshness; result counts/snippets/facets must not become an authorization side channel. Product/domain owners define searchable fields and relevance semantics. Provider-native document/field security may add defense in depth but does not replace the AChrix authorization boundary. Measure representative multilingual relevance, query/update latency, storage and rebuild cost before changing provider; search remains non-authoritative business state.

Backup/recovery uses maintained database/storage/deployment mechanisms under the product's supported profile. Logical dump, physical/base backup, WAL/PITR, snapshots or managed tools have different consistency/RPO/RTO/operational tradeoffs; select and rehearse the real profile rather than declaring one universal mechanism. The reusable Backup & Recovery boundary coordinates identity/integrity/coverage/recovery state and narrow adapters, not a new database backup format or generic shell/root service.

Telemetry has bounded access/retention/resources and its outage must not stop normal application operation.

Keep configuration external, shutdown graceful, resources bounded and schema activation deliberate. Binary/systemd or Compose alone does not provide high availability. Shared infrastructure may host isolated product databases; shared Foundation code does not require shared account/data ownership or a central router. [Security](../security/security-and-authorization.md#product-accounts-and-optional-sso) owns independent accounts/optional SSO.

## Proof and revisit trigger

Pin current supported Go/PostgreSQL/dependency versions in the executable work, not from chat assumptions. Verify the smallest mechanism against its real correctness/failure workload and total delivery/operating/upgrade/recovery cost. Optional interfaces, metadata or this table do not establish implementation or official support. Do not expand Issue #1 to install these optional services just because their paths are named.
