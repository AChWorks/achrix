# Data and Persistence

## Ownership and database support

Each durable domain dataset has one owning Module. Shared database access is not shared table ownership; other Modules use an intentional Application/query contract, projection or justified event-driven copy.

Ordinary PostgreSQL is the first relational implementation/integration-test target. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns driver/tool/extension defaults. SQLite is outside this baseline. Another engine/profile is supported only with actual migrations, concurrency/constraint behavior and compatibility tests.

Use useful vendor capabilities behind the owning Infrastructure boundary without making them business identity or leaking private schemas/driver types into public contracts. Declare real requirements such as transactions, foreign keys, row/advisory locks, JSON, indexes or full-text search; portability does not mean a lowest common denominator. Keep migration/export formats usable rather than unnecessarily trapping data.

## Schema and consistency

Use domain-specific schemas and database constraints for durable invariants. Do not replace unrelated domain models with a universal entity/field/value or settings table.

Keep strong invariants in explicit transaction boundaries where practical. Cross-module transactions in the monolith still call authorized owning Application boundaries. Eventual consistency/projections are acceptable only when the product can explain the lag/failure semantics. Re-evaluate consistency when a Module becomes a service; do not assume its old local transaction remains. Distributed transactions/sagas/outboxes require a real topology/failure need.

## Durable values

- Use stable opaque public IDs when exposing database keys would create unwanted coupling; do not add duplicate IDs to every table without a boundary.
- Persist instants with unambiguous UTC semantics; locale/timezone preferences are separate and presentation converts them. Server-local time is not implicit business meaning.
- Never use binary floating point for monetary state/calculation. Amount/currency and domain-appropriate fixed precision/minor units are explicit; financial Modules own stricter ledger rules.
- The domain decides deletion semantics: delete, archive, cancel, revoke, reverse, redact or immutable retention. Generic CRUD is not a universal rule.

## Privacy, migration and recovery

Collect/retain only justified data; define domain-sensitive classification, retention, archival, anonymization, deletion/export and preservation requirements where applicable. Consider derived indexes, caches, exports, analytics and backups when those obligations require it. Avoid personal/sensitive values in public IDs, URLs, logs, events or capability metadata without a concrete need.

Do not build a universal compliance subsystem. Owned data boundaries allow product-specific obligations. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) governs migrations and restore; backups include the keys/configuration/version material necessary to interpret critical state.
