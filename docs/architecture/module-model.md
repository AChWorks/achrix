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

Before serving traffic, reject missing/incompatible required capabilities, ambiguous identities and unsatisfiable startup dependencies. Optional absence has explicit behavior. Resources and mutable registration belong to the application instance, not process-global state. Startup and bounded shutdown/cleanup handle partial failure for actual components.

## Placement and extraction

Choose ownership before choosing packaging:

1. **Core** is reserved for broadly required Foundation primitives whose optionalization would distort normal consumers: composition, capability/application/authorization boundaries and lifecycle primitives.
2. **Reusable AChrix Module from the start** is appropriate when current product knowledge makes use across multiple product classes more likely than product-specific use, the capability has coherent shared semantics/data/lifecycle ownership, products can consume it without importing one product's business policy, and shared ownership is expected to reduce duplicate implementation/maintenance. The owner's rough 50–60% reuse threshold is a directional “more likely than not” heuristic, not a statistical gate.
3. **Product-local behavior** remains correct when semantics are differentiating/product-specific, reuse is genuinely uncertain, or a shared contract would require speculative generic behavior.

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

Identity, Admin and Media are the current initial accepted reusable placements. Content/taxonomy, notifications, commerce, payments, search, AI/provider behavior, Multi-Site and Gateway Bridge remain case-by-case until their own evidence/decision establishes placement; a familiar feature name alone is not reuse evidence.

## External functionality and trust

An external maintained library/application/service can be used directly or through a suitable Module/Provider/Adapter without becoming AChrix-owned. The owning integration preserves authorization, data semantics, licensing and effect/recovery rules. A foreign plugin is not automatically compatible with AChrix.

In-process extensions share process authority and are trusted code. Signatures and Go `internal` are not sandboxes; genuinely untrusted code may require process/service isolation. Compatibility or an official badge never supplies another Module's permissions.

Where composition needs metadata, identify Module version and provided/required/optional capabilities; [Contracts](contracts-and-interfaces.md) owns stable identity/public surface, and package/releases own source/license/publisher evidence. Third-party Modules may have compatible independent commercial licenses. Official identity, compatibility, licensing and runtime trust are distinct.

Do not build a dynamic installer, arbitrary loader, third-party sandbox or marketplace until real installation/lifecycle requirements justify its security and maintenance cost.
