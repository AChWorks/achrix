# Data and Persistence

## Ownership

Every durable domain dataset has one clear owning module.

Other modules read or mutate it only through an intentional contract, query projection, or event-driven copy where justified.

Shared database access does not make tables shared ownership.

## Reference persistence

The initial general-purpose persistence model is relational.

MariaDB is the intended primary reference engine for the first executable implementation.

The Foundation must not encode MariaDB as business identity.

Official support for another engine requires real migrations/tests/compatibility evidence.

SQLite is not part of the reference baseline.

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

## Migration and recovery

Schema/data migration follows the lifecycle contract.

For critical state, backup must include all material required to interpret/recover the data, including encryption/signing material when applicable.
