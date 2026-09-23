# Architecture Overview

## Default shape

The default architecture is a modular monolith: one application deployment, one primary relational system of record, and optional infrastructure introduced only when required.

```text
                 Public Web / Admin
                        |
REST / MCP / CLI / Jobs |
          \             |             /
           ---- Application Layer ----
                    |
                 Domain
                    |
              Infrastructure
       /       /      |      \        \
 Relational  Cache   Queue   Search   Providers
```

## Why modular monolith first

It preserves:

- fast product development;
- simple deployment and debugging;
- local transactions where appropriate;
- explicit domain/module ownership;
- a path to later extraction when scale/security/runtime/ownership evidence requires it.

Microservices are not the maturity model. A module may remain in-process forever.

## Dependency direction

Entry points call Application behavior. Domain owns business invariants. Infrastructure implements technology/provider details.

```text
Presentation/Adapters
        -> Application
        -> Domain

Infrastructure -> implements ports required by Application/Domain
```

Do not invert this into Domain depending on framework/provider code.

## Framework relationship

The initial executable implementation may use Laravel as the runtime/application framework.

Laravel facilities should be reused where fit. Framework replacement is not a current goal.

The architecture protects business semantics and public contracts from unnecessary framework coupling, not every internal implementation detail.

## Infrastructure relationship

Infrastructure choices are defaults, not Foundation identity.

A distribution may start with only:

```text
PHP/Laravel
+ relational database
+ local filesystem/session/cache where adequate
```

and later add Redis, object storage, search engines, queues, specialized services, or other runtimes only from evidence.

## Consumer relationship

A suitable product should consume the shared Foundation through the supported versioned boundary rather than permanently copy Foundation runtime code.

```text
Product shell
  -> Foundation Core
  -> selected Foundation Modules
  -> product-local Modules
```

The product owns its domain behavior, delivery, and runtime truth. Foundation Core/Modules own only reusable application behavior that has earned shared ownership.

Architecture boundaries do not automatically create package boundaries; see [Consumption and packaging](consumption-and-packaging.md).

## Extraction rule

A module becomes a separate service only if one or more real conditions justify the added distributed-system cost:

- materially different scaling;
- independent failure isolation;
- security/trust boundary;
- different runtime;
- separate deployment cadence;
- distinct ownership/team boundary;
- multiple independent consumers;
- operational economics that beat in-process composition.

Service extraction must preserve published contracts or version them intentionally.
