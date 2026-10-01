# Module Model

## Ownership

A Module owns one coherent capability: its business semantics, Application operations, intentionally public contracts, durable domain data, mutations, provider adapters and compatibility/migrations. Other Modules do not write its tables or import its Infrastructure implementation; they use authorized Application contracts.

Create Domain, Application, Infrastructure and Presentation only when actual behavior belongs there. A Module boundary is not automatically a dependency package, plugin, repository, process, service or database.

## Composition and variation

- **Required dependency:** needed to fulfill the Module's accepted purpose.
- **Optional dependency:** integration works when present; its absence does not invalidate the capability.
- **Provider/channel:** a replaceable implementation of stable semantics, such as notification delivery through email or Telegram, with SMTP or another email provider behind the email channel.

Do not make producers know every optional integration. Use direct Application calls for an immediate authoritative result and events for independent reactions, such as notification or analytics reacting to a completed order.

Keep provider-specific features discoverable when implementations vary; do not force attachments, templates, receipts or streaming into one mandatory mega-interface. Build only needed adapters. Start simple registration/composition in code; metadata becomes richer when actual consumers need it, not through a speculative manifest engine.

## Placement and extraction

Product-specific behavior starts product-local unless already a proven Foundation concern. Keep a cheap clean boundary when reuse is credible. A second real consumer triggers comparison of semantics, ownership, lifecycle, compatibility and total cost; promote only when shared ownership is better. Independent packaging is a separate decision in [Consumption](consumption-and-packaging.md).

## External functionality and trust

An external maintained library/application/service can be used directly or through a suitable Module/Provider/Adapter without becoming AChrix-owned. The owning integration preserves authorization, data semantics, licensing and effect/recovery rules. A foreign plugin is not automatically compatible with AChrix.

In-process extensions share process authority and are trusted code. Signatures and Go `internal` are not sandboxes; genuinely untrusted code may require process/service isolation. Compatibility or an official badge never supplies another Module's permissions.

Where composition needs metadata, identify Module version and provided/required/optional capabilities; [Contracts](contracts-and-interfaces.md) owns stable identity/public surface, and package/releases own source/license/publisher evidence. Third-party Modules may have compatible independent commercial licenses. Official identity, compatibility, licensing and runtime trust are distinct.

Do not build a dynamic installer, arbitrary loader, third-party sandbox or marketplace until real installation/lifecycle requirements justify its security and maintenance cost.
