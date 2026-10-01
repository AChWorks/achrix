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

## Error and failure contracts

Published machine-facing interfaces should expose stable failure semantics where callers need to act on them.

When relevant, distinguish categories such as:

- validation failure;
- unauthenticated/unauthorized;
- not found;
- conflict/concurrency precondition failure;
- rate/quota/resource limit;
- transient/upstream failure;
- definitive business rejection.

Transport-specific details such as HTTP status codes may map onto these semantics but should not be the only machine-readable contract when clients need stable behavior.

Do not leak framework exceptions, SQL details, provider internals, stack traces, or secrets as the public error model.

Where retry behavior matters, make retryability and correlation/operation identity explicit rather than forcing callers or AI agents to guess.

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

## Public extension boundary and identity

Publish only the small surface needed by a real external consumer/Module/Provider/Adapter. Prefer additive contracts and capability negotiation where actual implementations vary; avoid mega-interfaces and provider/driver/private-schema types that would unnecessarily couple consumers.

Implementation-only APIs use unexported Go symbols and appropriately placed internal packages. Do not expose internals merely to make an extension work: refine the necessary public contract and prove it with a separate consumer. Exact package layout remains the implementation ADR's responsibility.

Keep stable module/capability identity separate from mutable display branding. Qualify published identifiers by an explicit owning namespace where collision prevention is needed; reserve official publisher identity to AChWorks without preventing third parties from declaring their own identities or truthfully implementing shared contracts. The existing semantic capability examples remain valid local names.

Choose the smallest naming/version convention needed by Issue #1; do not build a namespace registry now. Published IDs are durable contracts: avoid deriving them from transient display/repository names, and version or migrate any later identity change deliberately.

Refer to [Licensing](../legal/licensing.md) for SDK/contract artifact scope and [Trademark Policy](../../TRADEMARKS.md) for official representation. Do not embed marketing brands in business rules or confuse retained protocol/import identifiers with an endorsement.
