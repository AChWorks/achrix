# Module Model

## Ownership

A Module owns one coherent reusable behavior domain and is the ownership/lifecycle boundary for that domain: its business semantics, Application operations, intentionally public contracts, durable domain data/assets, mutations, provider adapters, compatibility/migrations and [recovery dependencies](../lifecycle/lifecycle-and-compatibility.md#backup-and-recovery). A Module may expose a bounded set of related public Capabilities inside that domain. A Capability is an intentionally public behavior/authorization/composition contract, not a synonym for the whole Module, every method, screen, package or internal component. Other Modules do not write its tables or import its Infrastructure implementation; they use authorized Application contracts.

Create Domain, Application, Infrastructure and Presentation only when actual behavior belongs there. A Module boundary is not automatically a dependency package, plugin, repository, process, service or database.

## Composition and variation

- **Required dependency:** needed to fulfill the Module's accepted purpose.
- **Optional dependency:** integration works when present; its absence does not invalidate the capability.
- **Provider/channel:** a replaceable implementation of stable semantics, such as notification delivery through email or Telegram, with SMTP or another email provider behind the email channel.

Do not make producers know every optional integration. Use direct Application calls for an immediate authoritative result and events for independent reactions, such as notification or analytics reacting to a completed order.

Keep provider-specific features discoverable when implementations vary; do not force attachments, templates, receipts or streaming into one mandatory mega-interface. Build only needed adapters. Start simple registration/composition in code; metadata becomes richer when actual consumers need it, not through a speculative manifest engine.

Before serving traffic, reject missing/incompatible required capabilities, ambiguous identities and unsatisfiable startup dependencies. In the v0.2 Core, `Descriptor.Optional` permits absence but a present provider must match the exact ABI and precede its consumer. Present optional edges participate in cycle checks; duplicate/invalid declarations and required/optional overlap are rejected. This is compiled metadata, never private/global service discovery or injection. [Contracts](contracts-and-interfaces.md#capability-abi-and-composition) owns ABI revision/publication rules and the one-provider/one-revision-per-ID constraint; [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#next-development-minor-migration) owns migration from published v0.1.

Descriptors are deterministic, side-effect-free and cheap. Do not read environment/secrets, acquire resources, call networks/databases or mutate registration in `Descriptor()`. Actual startup/resource acquisition belongs to `Start`. Composition snapshots descriptor slices once per instance; product code explicitly wires typed collaborators.

Resources and mutable registration belong to the application instance, not process-global state. Startup and bounded shutdown/cleanup handle partial failure for actual components.

## Capability granularity and selective composition

Keep ownership, public behavior and implementation granularity distinct:

- A **Module** is the coherent ownership/lifecycle boundary. Related operations that share semantics, state and lifecycle may remain one Module even when they need separate public permissions/contracts.
- A **Capability** exists when an independent consumer, authorization decision or composition dependency needs a stable public behavior identity. Do not create a Capability for every exported method, page, helper or internal package.
- An **internal component/package** is an implementation boundary. Splitting code for maintainability does not create a public Capability or a new Module.
- A **Provider/Adapter** supplies a replaceable implementation or external integration behind the owning semantics; provider variation does not transfer ownership.

Treat these states as independent: **code/artifact presence**, **capability availability in the composed Application**, **runtime activation/resource acquisition**, **authorization**, and **durable state/data retention or removal**. Code presence grants no permission; an authorization denial does not mean a Capability is absent; disabling or omitting an active path does not implicitly uninstall code, reverse migrations or delete retained state.

A Module instance may be constructed from explicit immutable composition/configuration so its Descriptor advertises the capability set actually supported by that instance. Once composed, that advertised set is fixed. Descriptor calculation remains deterministic, side-effect-free and cheap: no environment/database/filesystem/network discovery, package-init registration, global mutable registry or service-locator lookup. Product composition explicitly supplies typed collaborators.

`Descriptor.Optional` is only an **optional dependency edge**: the consumer remains valid when that provider is absent, while a present provider must match the declared contract. It is not a generic feature flag, optional-subsystem registry or permission switch. When a real Module has a meaningful optional subsystem, first prefer explicit constructor/package composition inside the existing ownership boundary; add a new Core abstraction only if a real consumer proves that the current contracts cannot express the requirement safely.

Prefer fail-fast startup for active capabilities so configuration/dependency failures are found before traffic. Lazy/deferred initialization is exceptional: use it only when measured startup/resource value justifies later failure timing and the owning Module can define bounded concurrent initialization, readiness, shutdown and recovery semantics.

Do not use hidden blank-import/`init()` registration, Go runtime plugins/hot loading, or build tags as the normal AChrix product-feature mechanism. Build constraints remain appropriate for genuine platform/build variation. If excluding an optional subsystem from a product meaningfully avoids dependencies or artifact/resource cost, a separate Go package may be useful without changing Module ownership.

Split a subsystem only when a meaningful dependency, resource, lifecycle, security, durable-state or ownership boundary earns the extra public/versioning/testing complexity. If a subsystem later needs independently meaningful migrations, backup/recovery, compatibility cadence, release ownership or process isolation, re-evaluate whether it should become a separate Module/package/service then; do not pre-split for hypothetical flexibility.

## Placement and extraction

Choose ownership before choosing packaging:

1. **Core** is reserved for broadly required Foundation primitives whose optionalization would distort normal consumers: composition, capability/application/authorization boundaries and lifecycle primitives.
2. **Reusable AChrix Module from the start** is appropriate when current product knowledge makes use across multiple product classes more likely than product-specific use, the capability has coherent shared semantics/data/lifecycle ownership, products can consume it without importing one product's business policy, and shared ownership is expected to reduce duplicate implementation/maintenance. The owner's rough 50–60% reuse threshold is a directional “more likely than not” heuristic, not a statistical gate.
3. **Product-local behavior** remains correct when semantics are differentiating/product-specific, reuse is genuinely uncertain, or a shared contract would require speculative generic behavior.
4. **Shared infrastructure/tooling** is appropriate when products repeatedly need an execution mechanism but the business meaning/state still belongs to the calling Module. A shared job runner, transport or diagnostic adapter does not automatically become a business Module or Core primitive.

An accepted reusable placement does not require immediate implementation. Implement the real behavior when a current/near-term consumer needs it; do not create empty packages/interfaces merely to reserve a future Module. A second materially different consumer validates and may refine or reverse a placement, but is not a mandatory prerequisite when strong current evidence already supports shared ownership.

Packaging is a separate decision in [Consumption](consumption-and-packaging.md). Reusable Modules default to the existing AChrix repository/Go module/process unless independent versioning, ownership/security isolation, deployment/runtime needs or lower total lifetime cost justify extraction.

## Accepted reusable Modules

These are accepted **ownership boundaries**, not claims that every listed behavior is already implemented or that final package/capability identifiers are fixed. GitHub Issues own implementation scope and evidence.

### Identity

Identity owns reusable authentication/account/session mechanics inside each consuming product:

- local account identity/status and authentication lifecycle;
- credential/password authentication using maintained security libraries rather than custom cryptography;
- session lifecycle and the authentication principal supplied to normal Application authorization;
- external-identity mapping and OIDC/SSO adapters when required.

Products remain independently operable and own their account namespace/data, product profile/business meanings and permission grants. Domain roles such as author, customer, staff, seller or member belong to the owning product/domain unless separately proven generic. Successful login/SSO establishes identity only; Core's/product policy authorization still decides allowed actions. Identity does not imply a shared central AChWorks account service or cross-product user table.

### Admin

Admin owns a reusable administration presentation shell and Module-facing admin-surface composition boundary, including the common layout/navigation/routing integration and user-facing conventions needed across products.

Admin interactions must enter normal Application contracts and authorization; the shell is not a privileged business path. It should support applicable accessibility, localization and RTL/LTR behavior under [Web](../web/seo-and-semantic-web.md). Product/Module-specific admin screens, forms, validation and business semantics remain owned by their capability. Admin does not own direct database access, generic server/root control or another Module's state.

### Media

Media owns reusable asset/upload/storage lifecycle mechanics:

- asset identity and generic media metadata;
- bounded upload/input validation and access through supported Application contracts;
- storage-provider integration;
- derivative/variant lifecycle when real consumers require it;
- delete/retention semantics and declared recovery dependencies.

Products/other Modules own the business relationship to a Media asset, such as featured image, product gallery, avatar or content-specific meaning. Provider-specific capabilities stay explicit rather than forcing one storage/processing mega-interface.

### Audit

Audit owns reusable accountability-record mechanics for products that require durable audit evidence:

- append-only/immutable record identity and storage semantics;
- actor/action/target/outcome/authority/time metadata needed for accountable operations;
- bounded query/export/retention and integrity evidence where required;
- compatibility/recovery behavior for the audit dataset.

The owning domain decides **which** actions require audit and owns the business meaning/classification of the payload. Audit is not a copy of operational/debug/security-event logs and must not become a sink for arbitrary request bodies or secrets. Before implementation, each critical action defines whether audit persistence must commit atomically with domain state or how any non-atomic outcome is durably reconciled; “we logged it” is not proof of accountable atomicity.

### Notifications

Notifications owns reusable delivery mechanics when products need outbound human/system notifications:

- channel/provider abstraction and bounded delivery attempts;
- reusable delivery state/retry/reconciliation mechanics when required;
- generic template/rendering mechanics only where multiple products actually share them.

The owning product/domain decides why/when/to-whom a notification is sent and owns consent/preferences/business meaning. Marketing consent, domain recipient policy and provider-specific semantics do not become generic Core rules. No Notification implementation is required until a real product flow needs it.

### Search

Search owns reusable **derived search projection/query mechanics** when a product needs search:

- search-document/projection identity, update/delete propagation and rebuild/reconciliation lifecycle;
- bounded query/result/continuation contracts;
- provider adapters and provider-specific indexing/query behavior behind a narrow capability boundary;
- generic freshness/health/diagnostic state needed to operate the search projection.

Source Modules/products remain authoritative for their records, invariants, which fields/documents are searchable, visibility/authorization semantics, business relevance/boosting and domain meaning. Search is never authoritative inventory/payment/security state.

Start with PostgreSQL full-text search and optional `pg_trgm` for ordinary workloads; a dedicated engine such as OpenSearch is optional and must earn its extra service/operations cost from representative relevance, filtering, volume or latency evidence. Permission changes/deletes must propagate without leaking documents, counts, snippets or facets. Provider-native document/field security may add defense in depth, but it does not replace normal Application authorization. Implementation waits for a real product search flow under [Issue #48](https://github.com/AChWorks/achrix/issues/48).

### Settings

Settings owns only **application-level, operator-editable, durable settings** whose semantics belong to the product/application shell rather than a feature Module.

Each accepted setting has typed/versioned semantics, defaults/value-presence rules, validation, authorization, migration/compatibility and relevant concurrency/audit behavior. Settings is not a universal EAV/JSON bag, process-global configuration registry or secret store. Deployment/runtime configuration and secrets remain product/composition-owned; each feature Module continues to own its domain/business settings and may expose them through Admin.

Exact fields are introduced only from a real product need; examples never freeze a generic schema. Admin may render Settings through normal Application contracts. [Issue #50](https://github.com/AChWorks/achrix/issues/50) owns first implementation when concrete application-level fields exist.

### Backup & Recovery

Backup & Recovery is an accepted reusable **operational Module/tooling boundary**, not a Core primitive and not a generic host/root agent.

It may own reusable backup/restore operation identity/progress, manifests binding product/Core/Module/schema identities and integrity evidence, recovery-plan coordination and narrow adapters to maintained database/storage/deployment tools. Products own recovery policy, supported profiles, schedule/destination, RPO/RTO/retention and protected credentials/keys. Modules declare their durable assets, cross-store relationships and recovery constraints.

Use maintained native/provider mechanisms rather than inventing a backup format. Restore/preflight/reconciliation evidence is part of support; backup existence alone is not. The first real-product proof in #19 shapes the minimum reusable mechanics before [Issue #49](https://github.com/AChWorks/achrix/issues/49) implements a general boundary. Core gains no shell/root/filesystem/database-proxy authority from this placement.

Identity, Admin, Media, Audit, Notifications, Search, Settings and Backup & Recovery are the current accepted reusable placements. Content/taxonomy, commerce, payments, AI/provider behavior, Multi-Site and Gateway Bridge remain case-by-case until their own evidence/decision establishes placement; a familiar feature name alone is not reuse evidence.

## Shared infrastructure candidates

Durable background work is expected to recur, but its **business meaning belongs to the owning Module**. When the first real durable job is needed, prefer a reusable execution boundary (the PostgreSQL/River-first candidate in ADR-0003) with bounded workers/retries/cancellation/retention and domain-owned idempotency/effect semantics. Do not make a broker/queue/job runner a Core dependency or let a generic Jobs Module own product state.

Feature flags and other familiar infrastructure remain demand/evidence driven. Search/Settings/Backup & Recovery have accepted ownership boundaries above, but acceptance does not authorize empty implementations, mandatory auxiliary services or provider mega-interfaces.

## External functionality and trust

An external maintained library/application/service can be used directly or through a suitable Module/Provider/Adapter without becoming AChrix-owned. The owning integration preserves authorization, data semantics, licensing and effect/recovery rules. A foreign plugin is not automatically compatible with AChrix.

In-process extensions share process authority and are trusted code. Signatures and Go `internal` are not sandboxes; genuinely untrusted code may require process/service isolation. Compatibility or an official badge never supplies another Module's permissions.

Where composition needs metadata, identify Module version and provided/required/optional capabilities; [Contracts](contracts-and-interfaces.md) owns stable identity/public surface, and package/releases own source/license/publisher evidence. Third-party Modules may have compatible independent commercial licenses. Official identity, compatibility, licensing and runtime trust are distinct.

Do not build a dynamic installer, arbitrary loader, third-party sandbox or marketplace until real installation/lifecycle requirements justify its security and maintenance cost.
