# AChrix — Master Specification

Status: Canonical project specification
Repository: `AChWorks/achrix`
Scope: AChrix, the reusable application base for suitable present and future AChWorks products

## 1. Mission

AChrix is an executable, reusable, versioned AChWorks Foundation specialized for web-connected applications. It shortens the path from an idea to a maintainable production application without forcing every suitable new product to redesign or reimplement the same engineering foundations.

`AChrix` is the stable project/technology name. **Foundation** is the generic AChWorks architectural category that AChrix fulfills; it is not an alternate product name. The official pronunciation is `ATCH-riks` (Persian: `اَچ‌ریکس`).

The Foundation should make common application concerns predictable, reusable, AI-readable, secure, lifecycle-safe, and evolvable while leaving product/domain logic free to differ.

The intended product range is deliberately broad: content sites, SaaS products, marketplaces, commerce systems, payment/financial products, media platforms, APIs, automation products, and future web-connected applications.

Within the AChWorks ecosystem, **Koinon** (`AChWorks/koinon`) owns cross-project governance, discovery, intake, and generic contracts. This repository owns AChrix's executable implementation, Core/Module boundaries, packaging/versioning, releases, and application-specific specialization. Koinon is not a runtime dependency of AChrix.

## 2. Desired outcome

A suitable new product should be able to consume a small, well-understood Foundation baseline, upgrade that shared baseline deliberately over time, and focus primarily on its differentiating domain.

A competent human or AI agent should be able to discover:

- what the Foundation guarantees;
- which modules/capabilities exist;
- which contracts are stable;
- how modules compose;
- what is optional;
- how authorization applies;
- how data and side effects are owned;
- how the product consumes and upgrades the Foundation without unmanaged source-copy divergence;
- how install/update/upgrade/migration/recovery work;
- how to extend the system without bypassing boundaries;
- when to reuse, adapt, extract, replace, or build.

## 3. Non-goals

The Foundation is not:

- a documentation-only reference architecture or starter that products are expected to permanently copy/fork;
- a replacement framework built merely to avoid using mature frameworks;
- a mandatory single database, cache, queue, search engine, AI provider, storage provider, frontend framework, or deployment platform;
- a microservice platform by default;
- a plugin marketplace or third-party extension ecosystem in its initial form;
- a generic entity/value database that turns every domain into the same model;
- a reason to wrap every framework class behind a custom interface;
- a reason to extract every internal module into a package or service;
- a promise to support every possible technology from version 1;
- an excuse to build speculative abstractions before real variation exists.

SQLite is intentionally outside the primary persistence path unless a future explicit project decision changes that boundary.

## 4. Governing engineering principles

### 4.1 Reuse before build

Preferred order:

`reuse -> configure -> extend -> adapt -> extract -> replace/build only when justified`

Before building a non-differentiating capability, inspect maintained native/framework functionality, existing AChWorks capabilities, and suitable external/open-source/commercial solutions.

### 4.2 Minimal stable core

Core contains only responsibilities that nearly every suitable consumer requires and that truly belong at the Foundation/application composition level.

A capability does not enter Core merely because it is useful.

### 4.3 Boundaries follow credible variation

Create explicit ports/contracts where variation, replacement, security, ownership, or composition is credible and materially valuable.

Do not create interfaces, packages, providers, services, or plugin systems solely for hypothetical future flexibility.

### 4.4 A boundary does not imply a deployment unit

An internal module may remain in the same repository, dependency module/package, process, deployment, and relational database for its entire life.

Architectural separation does not by itself justify a package, repository, plugin, process, or service.

### 4.5 Evidence before extraction

The first implementation may remain local.

Extract shared code only after real consumers demonstrate converged semantics/mechanics and the benefit exceeds coupling, migration, maintenance, and regression cost.

Duplication can be cheaper than a bad abstraction.

### 4.6 Stable semantics, replaceable technology

Business semantics and published contracts should outlive infrastructure choices where doing so has material value.

Technology-specific implementation remains free to use the strengths of its technology behind the appropriate boundary.

### 4.7 Reversible decisions early

Prefer low-cost reversible decisions while requirements are uncertain.

Delay high-coupling or hard-to-reverse commitments until evidence justifies them.

### 4.8 Measure before scaling infrastructure

Do not add Redis, queues, brokers, distributed caches, OpenSearch, microservices, containers, orchestration platforms, or specialized runtimes merely for anticipated scale.

Introduce infrastructure in response to measured workload, failure, latency, throughput, availability, isolation, security, or operational requirements.

### 4.9 Optional paved roads and explicit escape hatches

Default paths and distributions are paved roads, not closed boundaries.

A product may diverge when its workload, domain, security, compatibility, or operational constraints justify a different path. Divergence should be explicit, localized, testable, and should preserve the applicable security, data-ownership, compatibility, and lifecycle contracts rather than bypassing them.

The Foundation should make the common path easy without making the uncommon-but-valid path impossible.

Preserve credible low-cost evolution options in today's ownership, public contracts, data formats and deployment boundaries. Judge external reuse and future extraction by total delivery, operations, upgrade and exit cost. This is not a guarantee that every future technology is supported or interchangeable.

## 5. Initial executable implementation strategy

The Foundation's identity is not a language or framework.

Go is the accepted primary implementation language for AChrix and the default path for suitable new products consuming its shared implementation; see [ADR-0002](docs/decisions/ADR-0002-go-primary-implementation.md). This supersedes the earlier PHP/Laravel expectation without changing the Foundation mission or Core/Module/product boundaries.

Prefer Go's standard library and maintained ecosystem components for commodity capabilities. AChrix is a reusable application Foundation, not a custom replacement for general-purpose infrastructure.

Domain/application semantics should avoid unnecessary infrastructure coupling where that coupling would materially hinder testing, reuse, extraction, or future evolution. Use fit standard-library/ecosystem capabilities directly where appropriate; do not wrap every facility for hypothetical portability.

PostgreSQL is the primary relational implementation target. Use ordinary PostgreSQL without mandatory TimescaleDB or other specialized extensions. MariaDB or another engine may be added when a real consumer or capability warrants it. Official support requires real migration, semantic, and CI/compatibility evidence; architectural portability does not equal support for every database. Defaults are recorded in [ADR-0003](docs/decisions/ADR-0003-postgresql-and-optional-infrastructure.md); optional infrastructure stays outside Core and is introduced only for a concrete workload or correctness/operational requirement.

## 6. Architecture model

The default deployment model is a modular monolith.

Conceptual layers:

```text
Human UI / Public Web / REST / MCP / CLI / Jobs
                    |
                    v
             Application Layer
          Commands / Queries / Policies
                    |
                    v
               Domain Rules
                    |
                    v
          Infrastructure Adapters
 DB / Cache / Queue / Search / Storage / Providers
```

A later service boundary is extracted only when independent scaling, failure isolation, security, runtime, deployment, ownership, or multiple-consumer economics justify it.

## 7. Core responsibilities

The minimal Core may own:

- module registration/composition conventions;
- capability identity/metadata and discovery contracts;
- application boundary conventions;
- extension/composition hooks that have proven necessary;
- authorization primitives/contracts needed for capability enforcement;
- lifecycle/compatibility metadata required to compose and upgrade the application safely.

Core should not automatically own:

- identity/user management;
- admin UI;
- content;
- media;
- SEO;
- notifications;
- commerce;
- payments;
- search;
- AI models/providers;
- queues;
- Redis;
- a specific database.

These are modules or infrastructure concerns unless evidence proves otherwise.

## 8. Module model

A module owns a coherent business or application capability.

A module may contain, only when needed:

```text
Domain/
Application/
Infrastructure/
Presentation/
```

Rules:

- no empty DDD ceremony;
- Domain must not depend on another module's Infrastructure;
- cross-module mutation goes through the owning module's Application boundary;
- each durable domain dataset has one clear owning module;
- optional integrations must not become mandatory dependencies without a domain reason;
- modules expose stable provided capabilities and may declare required/optional capabilities;
- module internals remain private unless intentionally published;
- modules may use framework facilities inside Infrastructure/Presentation;
- internal modules stay internal until extraction earns its cost.

### 8.1 Consumption and packaging boundary

The Foundation is intended to be consumed as shared, versioned implementation rather than permanently copied into each product.

The default product relationship is:

```text
thin product/application shell
  + versioned Foundation Core
  + selected reusable Foundation modules
  + product-local modules/domain behavior
```

An unmanaged long-lived copy or fork of shared Foundation runtime code is not the default reuse mechanism because it causes fixes, security changes, and lifecycle improvements to diverge across products.

The exact Go module/package layout and supported implementation versions/dependencies must be captured in an implementation ADR before executable coupling. It must provide a deliberate version/update path and must be proven with a separate minimal consumer.

An internal module boundary does **not** require an independent package. Keep Core and Modules together while that is cheaper and clearer. Extract a module into an independently versioned package/repository only when real consumers, lifecycle cadence, ownership, or compatibility needs make that boundary valuable.

Product-specific capabilities should begin in the product when they are not yet proven Foundation concerns. Design a clean local boundary when future reuse is credible and cheap; promote only after real consumer convergence.

See [Consumption and packaging](docs/architecture/consumption-and-packaging.md).

## 9. Composition and integration

Use direct Application commands/queries when the caller needs an immediate authoritative result.

Use domain/application events when something has happened and zero or more independent consumers may react.

Do not use events merely to avoid an ordinary function/application-service call.

Optional integrations should prefer events or optional capability bindings so the producer does not need to know every consumer.

Example:

```text
OrderPaid
  -> Notifications (if installed)
  -> Analytics (if installed)
  -> Loyalty (if installed)
```

## 10. Provider/channel variation

Where provider variation is natural, separate stable business semantics from channel/provider mechanics.

Example:

```text
Notification
  -> Telegram channel
  -> Email channel
  -> WhatsApp channel

Email channel
  -> SMTP provider
  -> SES provider
  -> Postmark provider
```

Do not force all providers into a lowest-common-denominator mega-interface.

Optional provider capabilities such as attachments, templates, rich text, actions, delivery receipts, or streaming should be explicit/discoverable.

Only implementations required by real products are built.

## 11. Human and machine parity

Important business operations must not exist only inside UI controllers.

Admin UI, public/API clients, MCP/AI, CLI, jobs, and automation should reuse the same Application capabilities and authorization boundaries.

Machine access never grants more authority than the acting principal possesses.

## 12. AI-first and machine-readable design

The Foundation is AI-first but not AI-dependent.

Where practical, modules should make available machine-readable descriptions of:

- installed modules;
- capabilities;
- commands and queries;
- inputs/outputs;
- versions;
- permissions;
- optional provider capabilities.

Schemas should use portable formats such as JSON Schema/OpenAPI where appropriate.

AI operations must be attributable to an authenticated/authorized principal and auditable where the domain requires durable accountability.

No arbitrary SQL, shell, HTTP proxy, or unrestricted code execution is introduced merely to make AI integration easier.

## 13. Data and persistence

Data rules:

- modules own their durable domain state;
- cross-module writes use the owning module's boundary;
- relational persistence is the initial general-purpose system of record;
- do not create a universal key/value or entity/field/value domain model to avoid real schemas;
- public/external identity should be stable and opaque where database identity would become an unwanted contract;
- database-specific optimizations are allowed behind infrastructure boundaries;
- a module may declare required database capabilities where portability would otherwise weaken correctness;
- data migration/export must not be made unnecessarily impossible by proprietary internal formats.

Time rules:

- persisted instants use unambiguous UTC semantics;
- user locale/timezone preferences are separate data;
- presentation performs locale/timezone conversion.

Money rules:

- monetary values must never use binary floating-point arithmetic;
- amount and currency semantics must be explicit;
- domain-specific fixed precision/integer-minor-unit rules belong to the owning financial module.

## 14. External side effects and asynchronous work

External effects such as payment capture, provider mutation, email, Telegram/WhatsApp delivery, webhooks, provisioning, or remote resource creation must have an explicit owner and failure model.

For correctness-sensitive effects, prefer:

```text
persist intent/state
-> perform bounded external effect
-> persist definitive/retryable/uncertain outcome
```

Use idempotency, durable operation identity, retries/backoff, transactional outbox, queueing, or authoritative reconciliation only where the actual failure model warrants them.

Never automatically retry an unknown mutation merely because a call timed out.

## 15. Security and authorization

Security defaults:

- default deny;
- least privilege;
- explicit server-side authorization;
- validation at trust boundaries;
- secrets outside source control and ordinary business data;
- sensitive values excluded/redacted from logs and machine output;
- provider credentials isolated to the integration that owns them;
- public interfaces cannot bypass domain/application authorization;
- AI is an actor/interface, not a privileged backdoor.

Identity and authorization are separate concerns. Role names may be convenience bundles; business enforcement should prefer explicit permissions/policies/capabilities.

## 16. Web, semantic content, SEO, and accessibility

When a distribution exposes public web content, the default path is server-rendered or otherwise crawlable semantic HTML with minimal unnecessary client-side JavaScript.

Content semantics should be separable from presentation/theme where doing so improves reuse, AI readability, SEO, accessibility, or redesignability.

User-facing web foundations should preserve Unicode/UTF-8 content, locale-aware presentation, and RTL/LTR directionality where applicable. Domain/content models should not hardcode one language or direction when a small local design choice can keep later internationalization feasible.

Relevant web modules should support, when applicable:

- semantic document structure and headings;
- canonical URLs;
- redirect history;
- robots/noindex;
- XML sitemaps;
- structured data/JSON-LD;
- Open Graph/social metadata;
- meaningful publication/modified timestamps;
- breadcrumbs/internal linking;
- image alt/media metadata;
- responsive media;
- accessible interaction;
- structured content representations;
- derived Markdown/machine representations when useful.

Conventions such as `llms.txt` may be implemented as adapters if useful, but no evolving convention becomes a Core architectural dependency without stronger evidence.

## 17. Lifecycle: install, update, upgrade, migration, recovery

Lifecycle safety is a first-class architectural concern.

Published Foundation artifacts/modules/distributions and consumer-facing Foundation dependencies should have explicit version and compatibility semantics.

Lifecycle changes must consider:

- preflight compatibility;
- environment/runtime requirements;
- dependency compatibility;
- schema/data migration;
- module compatibility;
- maintenance/downtime requirements;
- backup/recovery requirements when state risk is material;
- health/postflight verification;
- rollback or roll-forward strategy;
- secret/key material required to recover encrypted state;
- release artifact identity and provenance.

Do not build one universal updater merely because lifecycle concerns are shared. Standardize the contract first; extract common updater/installer implementation only after multiple real consumers converge.

Breaking public/module contracts require explicit version evolution. Persisted asynchronous messages/events must remain interpretable for their required lifetime.

A product upgrading its Foundation version is a lifecycle operation: compatibility, configuration, module versions, migrations, and recovery implications must be knowable before activation. Foundation updates must not depend on manually re-copying shared source into every product.

## 18. Compatibility policy

Separate internal implementation APIs from published/public contracts.

Published contracts include, when applicable:

- external REST/API contracts;
- MCP capabilities;
- webhooks;
- durable events/messages;
- plugin/module contracts;
- serialized/import/export formats;
- CLI contracts relied on by automation.

Prefer backward-compatible additive change.

Breaking changes require explicit versioning, migration guidance, and a bounded compatibility/retirement path appropriate to the contract.

## 19. Observability and audit

Operational logs and audit records are different concerns.

Logs exist for diagnosis and operation.

Audit exists for durable accountability such as who/what/when/under which authority performed a sensitive action.

Use correlation/request/operation identities when they materially improve diagnosis across boundaries.

Do not add metrics/tracing/dashboards merely because they are available. Add observability where it materially improves detection, diagnosis, capacity management, or release safety.

## 20. Performance, capacity, and cost

Prefer bounded behavior over speculative infrastructure.

Where relevant:

- list/search endpoints paginate;
- query counts and memory growth remain bounded;
- payload sizes are bounded;
- external calls have timeouts;
- queue/backlog growth has explicit limits when queues exist;
- resource-intensive features have representative measurements;
- infrastructure cost/complexity is part of architectural evaluation.

Optimize using evidence, not intuition.

## 21. Configuration and secrets

Separate:

- application/business configuration;
- deployment/runtime configuration;
- secrets/credentials;
- business data.

Do not collapse unrelated domain state into a universal `settings` key/value store.

Configuration should have explicit ownership, defaults, validation, and environment behavior where material.

## 22. Multi-tenancy and feature flags

Do not build multi-tenancy or a feature-flag platform until real requirements exist.

Also avoid scattering irreversible single-tenant or all-users-at-once assumptions through business code when a small local choice can keep evolution feasible.

This is an evolution guard, not a request to implement tenants/organizations/cohorts now.

## 23. Dependency policy

A new dependency must justify its ownership cost.

Review, proportional to risk:

- maintenance activity;
- security posture;
- license;
- compatibility;
- transitive dependency impact;
- upgrade path;
- operational burden;
- whether it removes enough custom code/complexity to be worthwhile.

Prefer established implementations for commodity cryptography, protocols, transport, storage drivers, parsers, OAuth/OIDC primitives, and similar specialized foundations unless a concrete reason justifies custom ownership.

## 24. Testing and architecture fitness

Testing follows risk and contracts rather than file count.

Use, where relevant:

- unit/domain tests for business invariants;
- application tests for use cases;
- adapter/integration tests for real technology boundaries;
- contract tests for multiple implementations of one port;
- compatibility tests for supported databases/providers;
- composition tests for supported module combinations;
- architecture/fitness checks for boundaries that are important enough to enforce mechanically.

Do not attempt combinatorial testing of every hypothetical module combination.

## 25. Distribution model

A Distribution is a curated consumer composition of the versioned Foundation plus selected modules for a product class.

Examples may eventually include:

- Content/CMS;
- SaaS;
- Commerce;
- API/headless;
- future specialized products.

Distributions are optional paved roads, not constraints on products, and should consume the Foundation rather than become permanent source forks of it.

The first real proving distribution is expected to be a lightweight content/CMS use case because it exercises modules, theme/content separation, SEO, AI readability, admin, media, lifecycle, and deployment without requiring speculative enterprise infrastructure.

## 26. Development/evolution rule

For every proposed capability:

1. confirm the product outcome;
2. discover reusable native/existing solutions;
3. choose the smallest implementation that satisfies current needs;
4. identify only credible future variation that materially affects today's boundary;
5. implement and verify at the smallest correct ownership level;
6. when behavior starts product-local, observe a real additional consumer before promoting it into the Foundation;
7. observe real second/third consumers before independent package/service extraction;
8. promote only when shared ownership/versioning reduces total delivery/maintenance risk and cost.

### 26.1 Licensing and trusted ecosystem

AChrix is genuine open-source software with a minimal, maintainable Core. The accepted artifact licensing and contribution-rights policy is owned by [Licensing](docs/legal/licensing.md) and [ADR-0004](docs/decisions/ADR-0004-licensing-and-trusted-ecosystem.md).

The project should be easy to extend while official identity remains verifiable. Long-term value also accumulates in supported compatibility, official Modules, security lifecycle, tooling, documentation and ecosystem trust. [Governance](GOVERNANCE.md) and [Trademark Policy](TRADEMARKS.md) own canonical authority and representation.

Do not pursue this goal with obfuscation, confusing code, license checks in Core, mandatory company services, deliberate incompatibility or restrictions that contradict published open-source rights. Future registries, signing, certification and update channels must earn their implementation cost.

## 27. Source-of-truth model

| Truth | Owner |
| --- | --- |
| project mission, durable principles, non-goals | `MASTER-SPEC.md` |
| generic cross-project AChWorks contracts/governance | `AChWorks/koinon` |
| architecture and engineering rules | `docs/` |
| current work/priority/dependencies | GitHub Issues/Milestones/Projects when used |
| lasting architecture decisions | `docs/decisions/` ADRs |
| implementation identity | Git commits/branches/PRs |
| validation | current local/CI evidence tied to the relevant commit |
| release/deployment state | release/deployment system |
| runtime health | runtime/monitoring/control plane |
| secrets | approved secret/runtime mechanism |

Do not duplicate live work state into docs.

## 28. Success criteria

The Foundation is successful when:

- a new product can start quickly without redesigning common application architecture;
- a product can consume and deliberately upgrade the shared Foundation without unmanaged permanent source-copy divergence;
- unused modules/infrastructure remain absent;
- modules compose without knowledge of unrelated modules and can remain internal until independent packaging is economically justified;
- AI/human interfaces reuse the same application capabilities;
- a technology/provider/database can evolve at a credible boundary without rewriting unrelated domain logic;
- lifecycle/update/upgrade remains safe and recoverable;
- SEO/AI-readable public content can be implemented without fighting the architecture;
- product logic remains product-specific;
- reuse reduces net delivery/maintenance cost rather than creating central coupling;
- a replacement human/AI can recover project intent and current work without chat history.

## 29. Change policy

This file owns durable project-level intent and constraints.

Update it only when the mission, non-goals, core architectural principles, stable boundaries, initial implementation strategy, source-of-truth model, or success criteria materially change.

Do not use this file as a task log, roadmap status report, or release note.
