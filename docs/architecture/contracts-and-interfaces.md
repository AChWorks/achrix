# Contracts and Interfaces

## Application and public contracts

Commands express a mutation intent with an authoritative result; queries provide authoritative reads; events describe completed facts for independent reactions. Events do not replace ordinary calls. UI/API/MCP/CLI/jobs reuse one Application capability and authorization boundary; there is no separate AI-only business path.

AI-first means explicit contracts and common permissions, not a dependency on any AI provider/model or a universal agent orchestration framework. Important machine-usable capabilities should expose stable semantic identity, structured inputs/outputs/errors and effect/permission metadata when a real client needs them, while humans and AI still traverse the same Application behavior. Keep repository/module structure, examples and contract naming predictable enough that automation can discover and reason about supported paths without importing internals or relying on chat history; do not create an AI-only bypass or opaque generated control plane.

Only intentionally published interfaces are compatibility contracts: APIs/MCP, webhooks, durable messages, import/export formats, extension contracts and automation-stable CLI behavior. Internal methods are not automatically public.

## Identity, metadata and extension surface

Use stable semantic capability IDs, for example `notifications.send` when behavior is provider-independent. Provider-specific semantics may have distinct IDs. Separate protocol/module identity from mutable display branding; qualify by an owning namespace when collisions matter. Third parties may publish their own identities and truthfully implement shared contracts. Do not hardcode branding into business rules or build a namespace registry before it is needed.

Publish only the small versioned surface needed by an independent Module/Provider/Adapter. Avoid mega-interfaces, driver/provider types and private schema leaks. Go unexported symbols and appropriate `internal` packages hide implementation dependencies; they are not an execution sandbox. Prove extensions through the supported public boundary rather than expose internals for convenience. [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) owns the starting package-consumption choice.

Where clients/composition need it, metadata exposes identity/version, description, provided/required/optional capabilities, input/output schemas, permissions, effect classification and provider variation. Prefer portable JSON Schema/OpenAPI or a fit standard. Discoverability never grants authority; [Security](../security/security-and-authorization.md) owns machine/AI permissions.

## Errors, concurrency and effects

Give machine clients stable actionable failure semantics when relevant: validation, authentication/permission, missing resource, conflict/precondition, quota/limit, transient/upstream failure and definitive business rejection. Map transports to those semantics; do not leak SQL, provider/framework exceptions, stack traces or secrets. Machine error codes, field names and capability IDs do not change with locale; products may translate user-facing explanations while preserving those contracts. [Internationalization](../web/seo-and-semantic-web.md#internationalization-and-directionality) owns language and presentation guidance. Specify retryability and request/operation correlation when callers need them.

Use expected versions/ETags or equivalent where concurrent human/AI/API edits could lose meaningful work. Use durable idempotency/operation identity for financially important, provisioning or replay-prone effects. These mechanisms do not replace domain invariants and need not decorate every trivial write.

For correctness-sensitive external mutations, persist intent/state, perform a bounded call, then record a definitive, retryable or uncertain outcome. A timeout is not proof of failure: reconcile unknown outcomes instead of blindly retrying. Use bounded retries, durable jobs, transactional enqueue/outbox or compensation only when the real topology/failure model requires them. A transaction or broker does not guarantee exactly-once external mutation.

Across products, use explicit authenticated APIs/webhooks with bounded calls and attributable authorization; shared Foundation code does not grant cross-product access.

## Evolution

Prefer additive change. Breaking public semantics/IDs require explicit versioning, migration guidance and an appropriate retirement path. Persisted events/messages/jobs must remain interpretable for their required lifetime; do not silently reinterpret payloads. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns activation and recovery; [Licensing](../legal/licensing.md) and [Trademark Policy](../../TRADEMARKS.md) own artifact terms and official representation.

## Initial Go public surface

The only shared import is `github.com/AChWorks/achrix`. [Package source](../../achrix.go) and Go documentation own exact signatures; unexported state/helpers are private. This pre-v1 surface is deliberately small:

| Contract | Intended consumer use |
| --- | --- |
| `Capability`, `Descriptor`, `Module` | Stable ID and positive capability ABI revision; implementation version; provided/required composition; owned `Start`, `Ready`, `Stop` |
| `Config`, `New`, `Application` | Explicit instance-owned composition; validate missing/duplicate/incompatible/cyclic dependencies before resources/traffic |
| `Principal`, `Policy`, `PolicyFunc`, `Application.Authorize` | Product-authenticated principal; consumer policy; fail-closed checks inside typed Application operations before state access |
| `Application.Start`, `Ready`, `Shutdown` | One-shot startup, caller-bounded local readiness, reverse bounded cleanup including the partially started failing component |
| `Application.Components`, `Version` | Defensive component metadata and actual source-backed Foundation dependency version; local/replacement builds identify themselves as development |
| `ErrComposition`, `ErrDenied`, `ErrNotReady` | Inspectable stable Core error categories; raw lifecycle extension errors remain restricted operator-level results |

`achrix.authorization` ABI 1 is Core-owned. The separate consumer provides `example.notes.create`/`example.notes.read` ABI 1. It owns its typed Service, Domain, PostgreSQL schema, migration and HTTP mapping; none is a shared Content/Identity Module or additional Foundation export. Required capabilities match exact ABI revisions; optional dependencies are omitted because this proof needs none. Descriptor registration/configuration never mutates a process-global registry. In-process Module/Policy methods must honor context; callers supply bounded operation deadlines and stop ingress/owned work before shutdown. `Ready` and `Authorize` check already-canceled contexts before admission and return `ErrNotReady` without waiting or invoking Module/Policy callbacks when lifecycle exclusion prevents admission.

The [Notes HTTP contract](../../fixtures/notes/README.md#http-contract) calls the same Service/authorization as direct invocation. Driver types/private SQL do not enter Foundation contracts. The fixture's `internal` packages are product-local, never Foundation internal imports. There is no generic dispatcher, dynamic loader, mandatory central service or AI-specific privilege path.
