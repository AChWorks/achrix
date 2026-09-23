# Contracts and Interfaces

## Contract categories

### Application commands

Use for an intent that may change state and whose caller needs an authoritative result.

Examples:

- `content.publish`
- `order.cancel`
- `payment.refund`

### Application queries

Use for authoritative reads.

Examples:

- `content.read`
- `orders.search`

### Events

Use for facts that have happened and may have zero or more independent consumers.

Examples:

- `OrderPaid`
- `ContentPublished`

Events are not a replacement for ordinary calls.

### Published external contracts

Examples:

- REST/OpenAPI;
- MCP capabilities;
- webhooks;
- durable async messages;
- import/export formats;
- automation-stable CLI.

These carry compatibility obligations that internal classes do not.

## Capability identity

Capability names should describe stable semantics, not infrastructure.

Prefer:

`notifications.send`

over:

`telegram.sendMessage`

when the business operation is provider-independent.

Provider-specific functionality may expose its own explicit capability when the semantics truly are provider-specific.

## Machine readability

Where useful, published operations should expose:

- stable identity;
- description;
- input schema;
- output schema;
- required permission/capability;
- version;
- side-effect classification;
- optional/provider capabilities.

JSON Schema/OpenAPI or similarly portable formats are preferred where they fit.

## Compatibility

Prefer additive change.

A breaking semantic change requires a new contract version or an explicit migration/retirement path.

Do not silently reinterpret already persisted async messages/events/jobs.

## Concurrency and optimistic mutation

Where concurrent human/AI/API edits can overwrite meaningful work, mutations should support a version/precondition model such as an expected version, ETag, or equivalent.

Do not add optimistic concurrency to every trivial write; apply it where lost updates are materially possible.

## Idempotency

Operations with external, financial, provisioning, or AI-replayed effects should support durable idempotency/operation identity where duplicate execution is materially harmful.

Idempotency keys do not replace domain invariants.

## Interface exposure

Internal methods are not automatically public APIs.

Only intentionally published contracts become compatibility boundaries.
