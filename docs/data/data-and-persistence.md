# Data and Persistence

## Ownership

Every durable domain dataset has one clear owning module.

Other modules read or mutate it only through an intentional contract, query projection, or event-driven copy where justified.

Shared database access does not make tables shared ownership.

## Primary persistence path

The initial general-purpose persistence model is relational.

PostgreSQL is the primary relational target for the first executable implementation, as selected in [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md).

Ordinary PostgreSQL is the baseline. TimescaleDB is an optional extension profile for a module whose time-series workload and lifecycle justify it; it is not a prerequisite for ordinary transactional modules. Required extensions and their versions, migrations, constraints, and recovery must be verified before claiming support.

The Foundation must not encode PostgreSQL as business identity.

Official support for another engine requires real migrations/tests/compatibility evidence.

SQLite is not part of the primary implementation/test baseline.

## Portability

Reasonable portability means:

- business rules do not casually depend on vendor SQL;
- public contracts do not leak internal schema unnecessarily;
- migrations/data formats do not trap data without a reason;
- engine-specific features stay in the appropriate Infrastructure/module boundary.

It does **not** mean avoiding useful engine-specific capabilities.

## Database capability requirements

A module may require capabilities such as:

- transactions;
- foreign keys;
- row locks;
- partial/generated indexes;
- JSON;
- advisory locks;
- full-text search.

Declare/support the real requirement instead of pretending every engine is equivalent.

## Consistency and transaction boundaries

Consistency is a domain requirement, not an architectural fashion.

- keep invariants that require strong consistency inside an explicit transaction boundary where practical;
- choose eventual consistency only when the product can tolerate and explain the lag/failure semantics;
- shared relational infrastructure may support a cross-module use case transaction in the modular monolith, but participating modules must still be invoked through their Application boundaries rather than by direct foreign-table mutation;
- do not introduce distributed transactions, sagas, compensation workflows, or outbox mechanics until the actual topology/failure model requires them;
- if a module is later extracted into a service, re-evaluate the affected consistency contract explicitly rather than pretending the old in-process transaction boundary still exists;
- derived/search/cache/read-model copies may lag only when that staleness is acceptable to the consuming use case.

## Schema design

Use domain-specific schemas.

Do not collapse Posts, Products, Payments, Users, Ledger entries, and unrelated concepts into generic entity/field/value tables.

Use database constraints for durable invariants where appropriate.

## Public identity

When identity crosses a module/API/public boundary, prefer a stable opaque identifier if exposing the internal database key would create long-term coupling.

Do not add duplicate identifiers to every table without a real boundary.

## Time

Persist instants with unambiguous UTC semantics.

Keep timezone/locale preferences separately.

Do not use server-local time as implicit business meaning.

## Money

Never use binary floating point for monetary state/calculation.

Represent amount/currency explicitly using a domain-appropriate fixed precision or minor-unit strategy.

Financial modules own stricter precision/ledger semantics.

## Data deletion semantics

Do not force generic CRUD delete behavior on every domain.

Possible domain operations include:

- delete;
- archive;
- cancel;
- revoke;
- reverse;
- redact;
- retain immutably.

The owning module defines the correct semantics.

## Data lifecycle and privacy

The owning module is responsible for the lifecycle semantics of the data it creates.

Where applicable:

- collect and retain only data justified by product/domain needs;
- distinguish ordinary, sensitive, confidential, and regulated data when that distinction changes handling;
- make retention, archival, anonymization, deletion, export, and legal/business preservation rules explicit for the domain that needs them;
- ensure materially important derived copies such as search indexes, caches, exports, analytics feeds, or backups are considered when deletion/redaction/retention semantics require it;
- avoid placing personal/sensitive data in identifiers, URLs, logs, events, or capability metadata without a concrete need.

Do not build a universal compliance/privacy subsystem before a real product or jurisdiction requires it. Preserve the boundaries needed to implement product-specific privacy obligations correctly later.

## Migration and recovery

Schema/data migration follows the lifecycle contract.

For critical state, backup must include all material required to interpret/recover the data, including encryption/signing material when applicable.
