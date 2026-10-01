# Contracts and Interfaces

## Application and public contracts

Commands express a mutation intent with an authoritative result; queries provide authoritative reads; events describe completed facts for independent reactions. Events do not replace ordinary calls. UI/API/MCP/CLI/jobs reuse one Application capability and authorization boundary; there is no separate AI-only business path.

AI-first means explicit contracts and common permissions, not a dependency on any AI provider/model or a universal agent orchestration framework.

Only intentionally published interfaces are compatibility contracts: APIs/MCP, webhooks, durable messages, import/export formats, extension contracts and automation-stable CLI behavior. Internal methods are not automatically public.

## Identity, metadata and extension surface

Use stable semantic capability IDs, for example `notifications.send` when behavior is provider-independent. Provider-specific semantics may have distinct IDs. Separate protocol/module identity from mutable display branding; qualify by an owning namespace when collisions matter. Third parties may publish their own identities and truthfully implement shared contracts. Do not hardcode branding into business rules or build a namespace registry before it is needed.

Publish only the small versioned surface needed by an independent Module/Provider/Adapter. Avoid mega-interfaces, driver/provider types and private schema leaks. Go unexported symbols and appropriate `internal` packages hide implementation dependencies; they are not an execution sandbox. Prove extensions through the supported public boundary rather than expose internals for convenience. [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) owns the starting package-consumption choice.

Where clients/composition need it, metadata exposes identity/version, description, provided/required/optional capabilities, input/output schemas, permissions, effect classification and provider variation. Prefer portable JSON Schema/OpenAPI or a fit standard. Discoverability never grants authority; [Security](../security/security-and-authorization.md) owns machine/AI permissions.

## Errors, concurrency and effects

Give machine clients stable actionable failure semantics when relevant: validation, authentication/permission, missing resource, conflict/precondition, quota/limit, transient/upstream failure and definitive business rejection. Map transports to those semantics; do not leak SQL, provider/framework exceptions, stack traces or secrets. Specify retryability and request/operation correlation when callers need them.

Use expected versions/ETags or equivalent where concurrent human/AI/API edits could lose meaningful work. Use durable idempotency/operation identity for financially important, provisioning or replay-prone effects. These mechanisms do not replace domain invariants and need not decorate every trivial write.

For correctness-sensitive external mutations, persist intent/state, perform a bounded call, then record a definitive, retryable or uncertain outcome. A timeout is not proof of failure: reconcile unknown outcomes instead of blindly retrying. Use bounded retries, durable jobs, transactional enqueue/outbox or compensation only when the real topology/failure model requires them. A transaction or broker does not guarantee exactly-once external mutation.

Across products, use explicit authenticated APIs/webhooks with bounded calls and attributable authorization; shared Foundation code does not grant cross-product access.

## Evolution

Prefer additive change. Breaking public semantics/IDs require explicit versioning, migration guidance and an appropriate retirement path. Persisted events/messages/jobs must remain interpretable for their required lifetime; do not silently reinterpret payloads. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns activation and recovery; [Licensing](../legal/licensing.md) and [Trademark Policy](../../TRADEMARKS.md) own artifact terms and official representation.
