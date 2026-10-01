# Module Model

## Ownership

A Module owns one coherent capability: its business semantics, Application operations, intentionally public contracts, durable domain data/assets, mutations, provider adapters, compatibility/migrations and [recovery dependencies](../lifecycle/lifecycle-and-compatibility.md#backup-and-recovery). Other Modules do not write its tables or import its Infrastructure implementation; they use authorized Application contracts.

Create Domain, Application, Infrastructure and Presentation only when actual behavior belongs there. A Module boundary is not automatically a dependency package, plugin, repository, process, service or database.

## Composition and variation

- **Required dependency:** needed to fulfill the Module's accepted purpose.
- **Optional dependency:** integration works when present; its absence does not invalidate the capability.
- **Provider/channel:** a replaceable implementation of stable semantics, such as notification delivery through email or Telegram, with SMTP or another email provider behind the email channel.

Do not make producers know every optional integration. Use direct Application calls for an immediate authoritative result and events for independent reactions, such as notification or analytics reacting to a completed order.

Keep provider-specific features discoverable when implementations vary; do not force attachments, templates, receipts or streaming into one mandatory mega-interface. Build only needed adapters. Start simple registration/composition in code; metadata becomes richer when actual consumers need it, not through a speculative manifest engine.

Before serving traffic, reject missing/incompatible required capabilities, ambiguous identities and unsatisfiable startup dependencies. Optional absence has explicit behavior. If an optional provider is present, compatibility must still be deterministic rather than silently binding to an incompatible ABI. The published v0.1 descriptor does not yet expose executable optional-dependency metadata; [Issue #44](https://github.com/AChWorks/achrix/issues/44) owns that pre-shared-Module alignment. Until then, do not invent private/global discovery to work around the gap.

Resources and mutable registration belong to the application instance, not process-global state. Startup and bounded shutdown/cleanup handle partial failure for actual components.

## Placement and extraction

Choose ownership before choosing packaging:

1. **Core** is reserved for broadly required Foundation primitives whose optionalization would distort normal consumers: composition, capability/application/authorization boundaries and lifecycle primitives.
2. **Reusable AChrix Module from the start** is appropriate when current product knowledge makes use across multiple product classes more likely than product-specific use, the capability has coherent shared semantics/data/lifecycle ownership, products can consume it without importing one product's business policy, and shared ownership is expected to reduce duplicate implementation/maintenance. The owner's rough 50–60% reuse threshold is a directional “more likely than not” heuristic, not a statistical gate.
3. **Product-local behavior** remains correct when semantics are differentiating/product-specific, reuse is genuinely uncertain, or a shared contract would require speculative generic behavior.
4. **Shared infrastructure/tooling** is appropriate when products repeatedly need an execution mechanism but the business meaning/state still belongs to the calling Module. A shared job runner, transport or diagnostic adapter does not automatically become a business Module or Core primitive.

An accepted reusable placement does not require immediate implementation. Implement the real behavior when a current/near-term consumer needs it; do not create empty packages/interfaces merely to reserve a future Module. A second materially different consumer validates and may refine or reverse a placement, but is not a mandatory prerequisite when strong current evidence already supports shared ownership.

Packaging is a separate decision in [Consumption](consumption-and-packaging.md). Reusable Modules default to the existing AChrix repository/Go module/process unless independent versioning, ownership/security isolation, deployment/runtime needs or lower total lifetime cost justify extraction.

## Initial accepted reusable Modules

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

Identity, Admin, Media, Audit and Notifications are the current accepted reusable placements. Content/taxonomy, commerce, payments, search, AI/provider behavior, Multi-Site and Gateway Bridge remain case-by-case until their own evidence/decision establishes placement; a familiar feature name alone is not reuse evidence.

## Shared infrastructure candidates

Durable background work is expected to recur, but its **business meaning belongs to the owning Module**. When the first real durable job is needed, prefer a reusable execution boundary (the PostgreSQL/River-first candidate in ADR-0003) with bounded workers/retries/cancellation/retention and domain-owned idempotency/effect semantics. Do not make a broker/queue/job runner a Core dependency or let a generic Jobs Module own product state.

Likewise, do not create a universal Settings Module/table. Products own configuration source/precedence; each Module owns its typed business settings/data and may expose them through Admin. Search, feature flags, backup engines and other familiar infrastructure remain demand/evidence driven.

## External functionality and trust

An external maintained library/application/service can be used directly or through a suitable Module/Provider/Adapter without becoming AChrix-owned. The owning integration preserves authorization, data semantics, licensing and effect/recovery rules. A foreign plugin is not automatically compatible with AChrix.

In-process extensions share process authority and are trusted code. Signatures and Go `internal` are not sandboxes; genuinely untrusted code may require process/service isolation. Compatibility or an official badge never supplies another Module's permissions.

Where composition needs metadata, identify Module version and provided/required/optional capabilities; [Contracts](contracts-and-interfaces.md) owns stable identity/public surface, and package/releases own source/license/publisher evidence. Third-party Modules may have compatible independent commercial licenses. Official identity, compatibility, licensing and runtime trust are distinct.

Do not build a dynamic installer, arbitrary loader, third-party sandbox or marketplace until real installation/lifecycle requirements justify its security and maintenance cost.
