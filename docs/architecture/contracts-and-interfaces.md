# Contracts and Interfaces

## Application and public contracts

Commands express a mutation intent with an authoritative result; queries provide authoritative reads; events describe completed facts for independent reactions. Events do not replace ordinary calls. UI/API/MCP/CLI/jobs reuse one Application capability and authorization boundary; there is no separate AI-only business path.

AI-first means explicit contracts and common permissions, not a dependency on any AI provider/model or a universal agent orchestration framework. Important machine-usable capabilities should expose stable semantic identity, structured inputs/outputs/errors and effect/permission metadata when a real client needs them, while humans and AI still traverse the same Application behavior. Keep repository/module structure, examples and contract naming predictable enough that automation can discover and reason about supported paths without importing internals or relying on chat history; do not create an AI-only bypass or opaque generated control plane.

Only intentionally published interfaces are compatibility contracts: APIs/MCP, webhooks, durable messages, import/export formats, extension contracts and automation-stable CLI behavior. Internal methods are not automatically public.

## Identity, metadata and extension surface

Use stable semantic capability IDs, for example `notifications.send` when behavior is provider-independent. Provider-specific semantics may have distinct IDs. Separate protocol/module identity from mutable display branding; qualify by an owning namespace when collisions matter. Third parties may publish their own identities and truthfully implement shared contracts. Do not hardcode branding into business rules or build a namespace registry before it is needed.

Publish only the small versioned surface needed by an independent Module/Provider/Adapter. Avoid mega-interfaces, driver/provider types and private schema leaks. Go unexported symbols and appropriate `internal` packages hide implementation dependencies; they are not an execution sandbox. Prove extensions through the supported public boundary rather than expose internals for convenience. [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) owns the starting package-consumption choice.

Where clients/composition need it, metadata exposes identity/version, description, provided/required/optional capabilities, input/output schemas, permissions, effect classification and provider variation. Prefer portable JSON Schema/OpenAPI or a fit standard. Discoverability never grants authority; [Security](../security/security-and-authorization.md) owns machine/AI permissions.

## Authorization scope

The Core authorization `resource` value is a product-owned stable opaque **authorization scope/resource reference**, not a generic business object serializer or trusted client-claim bag.

- Existing resources may use an opaque stable reference that the product policy can resolve authoritatively.
- Create/list actions may authorize against the owning parent/scope (for example a site or collection) rather than invent a not-yet-created child ID.
- Multi-Site later resolves deterministic site context before authorization; Core does not infer tenancy/site from URLs, database rows or arbitrary strings.
- Do not encode untrusted JSON/claims into the resource field and then treat that encoding as authorization evidence.
- Empty/global scope is valid only when the capability semantics explicitly allow it.
- The owning Application cheaply bounds and normalizes attacker-influenced reference syntax/size before expensive policy evaluation. This preflight performs no authorization-sensitive resource lookup and reveals no resource existence. Notes rejects anything outside its 26–64 byte opaque ID syntax before policy; create uses its explicitly global fixture scope.

If a real consumer cannot express safe policy with this minimal reference without awkward duplicate lookups or hidden context, that evidence may justify a typed authorization request in a later pre-v1 contract revision. Do not build a generic ABAC/tenant framework in anticipation.

## Errors, concurrency and effects

Give machine clients stable actionable failure semantics when relevant: validation, authentication/permission, missing resource, conflict/precondition, quota/limit, transient/upstream failure and definitive business rejection. Map transports to those semantics; do not leak SQL, provider/framework exceptions, stack traces or secrets. Machine error codes, field names and capability IDs do not change with locale; products may translate user-facing explanations while preserving those contracts. [Internationalization](../web/seo-and-semantic-web.md#internationalization-and-directionality) owns language and presentation guidance. Specify retryability and request/operation correlation when callers need them.

For JSON HTTP APIs that need a shared error representation, prefer [RFC 9457 Problem Details](https://www.rfc-editor.org/rfc/rfc9457.html) rather than inventing a new incompatible envelope per product/Module. Keep a stable machine error code/type separate from localized human text, include safe correlation/instance identity when useful, and expose only fields a caller is allowed to know. HTML/Admin flows may render their own user-facing view from the same underlying error semantics; RFC 9457 is not a requirement for HTML pages or every internal handler.

Use expected versions/ETags or equivalent where concurrent human/AI/API edits could lose meaningful work. Use durable idempotency/operation identity for financially important, provisioning or replay-prone effects. These mechanisms do not replace domain invariants and need not decorate every trivial write.

For correctness-sensitive external mutations, persist intent/state, perform a bounded call, then record a definitive, retryable or uncertain outcome. A timeout is not proof of failure: reconcile unknown outcomes instead of blindly retrying. Use bounded retries, durable jobs, transactional enqueue/outbox or compensation only when the real topology/failure model requires them. A transaction or broker does not guarantee exactly-once external mutation.

Across products, use explicit authenticated APIs/webhooks with bounded calls and attributable authorization; shared Foundation code does not grant cross-product access.

## Collection contracts and pagination

Unbounded collections are bounded at the contract boundary.

- Prefer cursor/keyset pagination for mutable or potentially large machine-facing collections. Ordering must be deterministic and include a stable tie-breaker so inserts/deletes do not silently reshuffle an in-flight traversal.
- Cursors are opaque transport state, not business identifiers or authorization evidence. Treat them as untrusted input, version/sign/protect them when their content would otherwise expose or permit tampering with internal state, and cap page size.
- Offset/page-number pagination is acceptable for demonstrably small/bounded administrative lists or UX where exact page numbers are the product requirement; do not use it by default for large mutable feeds merely because it is easy to implement.
- Filters/sorts are explicit, bounded and index/query-plan aware. Do not expose arbitrary field/SQL ordering as a generic API.
- Do not standardize one universal list response type before a real public list endpoint needs it; preserve the semantics above across whatever transport shape the product selects.

## Evolution

Prefer additive change. Breaking public semantics/IDs require explicit versioning, migration guidance and an appropriate retirement path. Persisted events/messages/jobs must remain interpretable for their required lifetime; do not silently reinterpret payloads. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns activation and recovery; [Licensing](../legal/licensing.md) and [Trademark Policy](../../TRADEMARKS.md) own artifact terms and official representation.

## Initial Go public surface

The only shared import is `github.com/AChWorks/achrix`. [Package source](../../achrix.go) and Go documentation own exact signatures; unexported state/helpers are private. This pre-v1 surface is deliberately small:

| Contract | Intended consumer use |
| --- | --- |
| `Capability`, `Descriptor`, `Module` | Stable ID and positive capability ABI revision; implementation version; provided/required/optional compiled composition; owned `Start`, `Ready`, `Stop` |
| `Config`, `New`, `Application` | Explicit instance-owned composition; validate missing/duplicate/incompatible/cyclic dependencies before resources/traffic |
| `Principal`, `Policy`, `PolicyFunc`, `Application.Authorize` | Product-authenticated principal; consumer policy; fail-closed checks inside typed Application operations before state access |
| `Application.Start`, `Ready`, `Shutdown` | One-shot startup, caller-bounded local readiness, reverse bounded cleanup including the partially started failing component |
| `Application.Components`, `Version` | Defensive component metadata and actual source-backed Foundation dependency version; local/replacement builds identify themselves as development |
| `ErrComposition`, `ErrDenied`, `ErrAuthorizationUnavailable`, `ErrNotReady` | Inspectable Core categories; explicit denial is distinct from safe evaluation failure. Caller context errors are preserved; raw lifecycle extension errors remain restricted operator-level results |

Current source targets the next development minor; the published `v0.1.x` surface remains immutable. [Migration](../lifecycle/lifecycle-and-compatibility.md#next-development-minor-migration) owns that distinction. `achrix.authorization` ABI 2 is Core-owned for the new deadline/error/admission semantics; ABI 1 belongs to the published `v0.1` line. The separate consumer still provides `example.notes.create`/`example.notes.read` ABI 1. It owns its typed Service, Domain, PostgreSQL schema, migration and HTTP mapping; none is a shared Content/Identity Module or additional Foundation export.

### Capability ABI and composition

A stable capability ID names one semantic contract. Its positive `Capability.Version` is an ABI revision, independent of the Module's implementation release: additive/backward-compatible changes retain that revision; genuinely incompatible published semantics increment it. Required and present optional capabilities match **exact** revisions. There are no version ranges, implicit ABI upgrades or runtime negotiation. `New` admits one provider and one ABI revision per capability ID, including within a single provider. A real consumer needing old/new contracts simultaneously must explicitly compose a compatibility adapter or deliberately versioned contract strategy; advertising ABI 2 never satisfies an ABI 1 requirement.

`Descriptor.Optional` may name an absent capability without invalidating composition. A present provider must be compatible and starts before the consumer; reverse shutdown follows that order. Invalid/duplicate dependencies, required/optional overlap, duplicate provision and cycles including present optional edges fail before startup. `Descriptor()` is called once per composition and its slices are snapshotted. It must be deterministic, side-effect-free and cheap: no environment/secret access, resource acquisition, network/database calls or mutable registration. Trusted in-process implementations own that obligation; Core does not sandbox a descriptor. Optional metadata performs no service discovery/injection; product code still supplies typed collaborators explicitly.

### Authorization and shutdown

`Policy.Authorize` returns nil to allow, `ErrDenied` (possibly wrapped) to explicitly deny, and another error when evaluation/dependencies fail. Core returns only safe `ErrAuthorizationUnavailable` for that last category, without wrapping the provider error. A dependency's own context failure is unavailable unless the admitted operation itself is canceled/expired. Actual caller cancellation/deadline takes precedence over policy outcomes. Products may map denial to 403 and evaluation-unavailable to 503; this distinction never grants access or makes a domain mutation automatically retryable.

In-process Module/Policy methods must honor context cancellation and return promptly; Policies support concurrent calls. `Ready` and `Authorize` require caller deadlines. A missing authorization deadline returns an inspectable `ErrAuthorizationUnavailable` without invoking Policy. Already-canceled callers retain their context error; non-ready instances reject admission with `ErrNotReady` without waiting behind lifecycle callbacks. Callbacks run outside the short state lock.

Shutdown atomically closes admission, cancels admitted readiness/policy contexts, waits for their callbacks to return, then stops Modules. One shutdown budget covers lifecycle serialization, drain and reverse Stop. A concurrent startup is canceled and owns its bounded partial-start cleanup. If waiting/drain expires, Shutdown returns the context result, admission stays closed and no Stop races a callback; a subsequent Shutdown may finish after it drains. Completed cleanup is not repeated and retains its result. Core cannot forcibly terminate arbitrary in-process code; no worker pool/scheduler or goroutine wrapper is used to pretend otherwise. Products stop ingress and drain their own Application/domain work before shutdown: Core tracks only its own readiness/policy callbacks, not a whole domain operation after authorization.

The [Notes HTTP contract](../../fixtures/notes/README.md#http-contract) calls the same Service/authorization as direct invocation. Driver types/private SQL do not enter Foundation contracts. The fixture's `internal` packages are product-local, never Foundation internal imports. Its simple JSON error body is evidence for the original bounded proof, not the required future public API envelope. There is no generic dispatcher, dynamic loader, mandatory central service or AI-specific privilege path.
