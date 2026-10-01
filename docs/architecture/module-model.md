# Module Model

## Purpose

Modules separate coherent capabilities so different products can compose what they need without dragging unrelated features into the application.

A Module boundary does not imply a package, plugin, repository, process, service, or separate database.

## Internal shape

Use only the layers that contain real behavior:

```text
Modules/<Module>/
  Domain/
  Application/
  Infrastructure/
  Presentation/
```

No empty architecture ceremony.

## Ownership

A module owns:

- its business semantics;
- commands/queries/use cases;
- public module contracts it intentionally exposes;
- its durable domain data;
- mutations of that data;
- provider-specific adapters that belong to its capability;
- module-specific compatibility/migration rules.

Another module may not directly mutate its tables or import its Infrastructure implementation.

## Dependency types

### Required

The module cannot fulfill its accepted purpose without the dependency.

### Optional

The module can work without the dependency and integrates when the capability is present.

### Provider/channel

A stable module capability is fulfilled by one or more replaceable implementations.

## Composition example

```text
Commerce
  provides:
    order.create
    order.read
    order.capture

  required:
    identity

  optional:
    notifications
    analytics
    loyalty
```

Do not build a complex manifest engine during early versions. These semantics may initially live in registration code/tests and become machine-readable when the module set makes that worthwhile.

## Notification example

Notification is a credible variation point because multiple delivery channels/providers are common and semantically related.

```text
Notification
  -> Telegram channel
  -> Email channel
  -> WhatsApp channel
  -> Push channel

Email channel
  -> SMTP
  -> SES
  -> Postmark
```

The initial product may implement only Telegram.

Do not pre-build the other adapters.

Do not create one giant interface requiring every channel to support every feature.

Capabilities such as:

- attachments;
- rich text;
- actions/buttons;
- provider templates;
- delivery receipts;

should be optional/discoverable where needed.

## Optional integrations

Avoid conditionals spread throughout producer modules.

Prefer a composition boundary or event reaction where appropriate.

```text
OrderPaid
  -> Notification listener (if installed)
  -> Analytics listener (if installed)
```

## Product-local promotion and package extraction

A capability that first appears in one product should remain product-local unless it is already a proven Foundation concern.

If a future reuse path is credible and cheap, keep its ownership/Application boundary clean without creating a premature package.

When another real consumer needs similar behavior:

1. compare business semantics, lifecycle, data ownership, compatibility, and provider variation;
2. keep separate implementations when the needs differ materially;
3. promote into a reusable Foundation Module only when shared ownership lowers total cost/risk;
4. extract that Module into an independently versioned package/repository only when independent distribution/versioning/ownership adds further value.

A second consumer triggers comparison, not automatic promotion or package extraction.

An internal Foundation Module may remain in the same repository and package indefinitely.

## External capabilities and extension trust

A maintained external library, application or service need not become AChrix-owned to be useful. Integrate through the smallest suitable Module/Provider/Adapter or standard API; use it directly when an extra wrapper has no ownership/security/compatibility value.

The owning Module preserves authorization, data semantics, external failure/recovery and licensing obligations. A foreign plugin is not automatically compatible; verify the actual contract rather than promise universal importing.

Third-party Modules may be independently licensed, including proprietary/commercial, subject to [artifact/dependency licensing](../legal/licensing.md). Official status, license, compatibility and runtime trust are distinct properties.

In-process extensions share process authority and must be treated as trusted code. A signature or Go internal package is not an execution sandbox; untrusted code may need a real process/service isolation boundary when justified. An extension must not obtain another Module's permissions by using an official badge.

Where metadata is actually needed for composition, keep module identity/version, provided/required capabilities and compatibility requirements explicit. Source/license/publisher/artifact evidence belongs with the package/release; exact schema and optional fields follow real consumers rather than a speculative manifest engine.

## Plugin system

Install/enable/disable lifecycle may eventually justify a formal plugin/module runtime.

Do not build a marketplace, ZIP installer, arbitrary class loader, or third-party extension sandbox until real product requirements justify those capabilities and their security/compatibility cost.
