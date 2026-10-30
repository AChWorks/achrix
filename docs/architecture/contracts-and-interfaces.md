# Contracts and Interfaces

## Application and public contracts

Commands express a mutation intent with an authoritative result; queries provide authoritative reads; events describe completed facts for independent reactions. Events do not replace ordinary calls. UI/API/MCP/CLI/jobs reuse one Application capability and authorization boundary; there is no separate AI-only business path.

AI-first means explicit contracts and common permissions, not a dependency on any AI provider/model or a universal agent orchestration framework. Important machine-usable capabilities should expose stable semantic identity, structured inputs/outputs/errors and effect/permission metadata when a real client needs them, while humans and AI still traverse the same Application behavior. Keep repository/module structure, examples and contract naming predictable enough that automation can discover and reason about supported paths without importing internals or relying on chat history; do not create an AI-only bypass or opaque generated control plane.

The optional [Bridge](../../bridge/README.md) binds deliberate typed operations to MCP using maintained protocol/schema machinery and to explicitly routed bounded JSON HTTP or private file transfer. These profiles reuse the resource-server admission, trusted request attribution and effect/error boundary. It is a presentation/resource-server component, not an operation registry in Core or an OAuth issuer. The product maps a verified user/client to trusted request context, admits the connection/channel and calls the ordinary owning service; that service evaluates current product authorization. Additional channel restrictions intersect this authority and never replace it. [Security](../security/security-and-authorization.md#user-delegation-and-channel-restrictions) owns credential and account-linking requirements.

Only intentionally published interfaces are compatibility contracts: APIs/MCP, webhooks, durable messages, import/export formats, extension contracts and automation-stable CLI behavior. Internal methods are not automatically public.

## Internal and external programmatic coverage

A capability intended for a credible internal or external programmatic consumer should have supported HTTP API and MCP access alongside its ordinary application behavior. Assess operations and user outcomes, not exported Go symbols, screens or tables. Existing partial coverage remains explicit work; a generated schema, Go method or Admin form alone does not establish network support.

| Consumer | Supported route |
| --- | --- |
| Code in the same process, including jobs or another Module | Direct typed owning service with its normal principal, authorization and invariants |
| Another internal service, script or ordinary external application | Explicit authenticated HTTP API, or MCP when its discovery/invocation model fits |
| Direct AI client, Gateway-mediated AI or a Gateway human operation | Deliberate MCP/HTTP binding to the same owning operation; Gateway/model use is optional |
| Product browser/UI | The product's presentation route to that service, including HTTP when useful |

An internal network is not an authority boundary. Protected cross-process calls still need verified identity, resource/site binding and current policy. Public reads are public only under a deliberate product data/publication contract. [Security](../security/security-and-authorization.md#machineai-access) distinguishes user delegation from a genuine unattended workload.

### Coverage and ownership

For each new or materially changed eligible capability, the owning component contract/README and PR identify the intended consumer, typed operation, applicable HTTP method/path and MCP tool/resource mapping, and evidence for the supported paths. Use the existing documentation owner; a small operation table is enough when several paths need comparison. Do not add a global runtime registry or a second catalog of mutable implementation status.

Applicable adapters normally ship with the capability. A channel may be deliberately inapplicable for a documented reason, such as a secure login ceremony, a local composition primitive or a maintenance action lacking an authorized remote operator profile. A bounded accepted delivery may defer a channel only with a concrete reason, owning follow-up and honest incomplete coverage. Historical gaps do not force every unrelated PR to expand scope. Demand-gated Modules still require their real consumer/fields/workflow before implementation.

Core's composition, policy invocation and lifecycle APIs are local contracts for trusted product code. Remote consumers call the owning authorized application operation; Core has no remote lifecycle or authorization dispatcher. The [Admin shell](../../admin/README.md) owns presentation and routes actions to those services. [Installation](../../installation/README.md) supplies a pure planner over declared requirements and observations before a runtime or account exists; its public Go API grants no host access or execution authority. These deliberate boundaries do not imply a missing generic HTTP/MCP control plane. Credential-bearing Identity ceremonies retain their dedicated secure routes; additional remote administration requires a specific consumer and contract.

Reusable component operations and their adapters remain AChrix-owned; product-specific commands and policy stay product-owned. Keep the semantic owner distinct from packaging: choose optional adapter packages or an earned companion distribution so an MCP SDK does not become mandatory in Core or unrelated domain consumers. Do not create a Module per route/tool or a universal dispatcher to avoid a few explicit bindings.

### Equivalent behavior, deliberate transport

HTTP and MCP are normally sibling adapters calling the same typed service. A remote connector may adapt an existing HTTP service when the deployment boundary warrants it; an in-process MCP handler need not call its own HTTP server. Do not require UI calls to traverse MCP.

Share useful component-owned input/result types, stable field meanings and compatible schema fragments. Publish an OpenAPI description for a supported programmatic HTTP surface and use the maintained MCP SDK's schemas/metadata for MCP. Select a schema/specification version supported by the actual validators and consumers. Generation is useful only when it reduces demonstrated drift; it does not replace explicit exposure, domain validation or authorization.

Coverage means equivalent supported outcomes, not one tool per endpoint or identical wire payloads. Keep HTTP methods/statuses/headers, binary or multipart transfer and cache behavior in the HTTP adapter; keep MCP tool descriptions, effect hints and supported result forms in its adapter. For Media, a bounded upload/download path and an authorized reference may be more appropriate than large base64 tool payloads. References have an explicit access/lifetime contract and do not make private assets public. Do not claim MCP resources, subscriptions or transfer profiles that the composed adapter does not implement.

Discovery reveals only intentionally publishable metadata and must preserve resource/user/site confidentiality. Recheck execution authority regardless of earlier discovery. Tool annotations and schemas are descriptive, never permission or replay guarantees. Business pagination and filtering remain the owning service's bounded contract, distinct from protocol catalog pagination.

### Completion evidence

Prove applicable paths use the same ordinary operation and current policy, including allowed/denied users and resources, changed permissions, narrower channel admission, equivalent invalid input and stale preconditions. Fix a missing or unsafe domain operation at its owning service rather than recreating it in a transport.

Mutation coverage also proves known non-execution, confirmed effects, uncertainty after dispatch and authorized reconciliation where supported; preserve useful errors and protected causes through serialization/delivery failure. Keep trusted actor/client/channel/request/operation attribution available under [Operations](../operations/operability-performance.md#diagnosis-and-audit), and preserve it in required durable Audit rather than treating diagnostic logs as accountability evidence.

Check schema/example consistency, bounded collection/input/output/concurrency behavior and idle composition cost. Record only executed profiles: unit/resource-server tests, an ordinary browser pass and real OAuth/client interoperability prove different things. Existing [errors and effects](#errors-concurrency-and-effects), [pagination](#collection-contracts-and-pagination) and [evolution](#evolution) rules apply to every supported channel.

## Identity, metadata and extension surface

Use stable semantic capability IDs, for example `notifications.send` when behavior is provider-independent. Provider-specific semantics may have distinct IDs. Separate protocol/module identity from mutable display branding; qualify by an owning namespace when collisions matter. Third parties may publish their own identities and truthfully implement shared contracts. Do not hardcode branding into business rules or build a namespace registry before it is needed.

Publish only the small versioned surface needed by an independent Module/Provider/Adapter. Avoid mega-interfaces, driver/provider types and private schema leaks. Go unexported symbols and appropriate `internal` packages hide implementation dependencies; they are not an execution sandbox. Prove extensions through the supported public boundary rather than expose internals for convenience. [ADR-0006](../decisions/ADR-0006-independent-components-and-lifecycle.md) owns the independent component distribution choice; ADR-0005 records its initial single-module predecessor.

Where clients/composition need it, metadata exposes identity/version, description, provided/required/optional capabilities, input/output schemas, permissions, effect classification and provider variation. Prefer portable JSON Schema/OpenAPI or a fit standard. Discoverability never grants authority; [Security](../security/security-and-authorization.md) owns machine/AI permissions.

## Authorization scope

The Core authorization `resource` value is a product-owned stable opaque **authorization scope/resource reference**, not a generic business object serializer or trusted client-claim bag.

- Existing resources may use an opaque stable reference that the product policy can resolve authoritatively.
- Create/list actions may authorize against the owning parent/scope (for example a site or collection) rather than invent a not-yet-created child ID.
- Multi-Site resolves deterministic site context without granting domain authority; each owning operation still authorizes its exact scope. Core does not infer tenancy/site from URLs, database rows or arbitrary strings.
- Do not encode untrusted JSON/claims into the resource field and then treat that encoding as authorization evidence.
- Empty/global scope is valid only when the capability semantics explicitly allow it.
- The owning Application cheaply bounds and normalizes attacker-influenced reference syntax/size before expensive policy evaluation. This preflight performs no authorization-sensitive resource lookup and reveals no resource existence. Notes rejects anything outside its 26–64 byte opaque ID syntax before policy; create uses its explicitly global fixture scope.

If a real consumer cannot express safe policy with this minimal reference without awkward duplicate lookups or hidden context, that evidence may justify a typed authorization request in a later pre-v1 contract revision. Do not build a generic ABAC/tenant framework in anticipation.

## Errors, concurrency and effects

Give machine clients stable actionable failure semantics when relevant: validation, authentication/permission, missing resource, conflict/precondition, quota/limit, transient/upstream failure and definitive business rejection. Map transports to those semantics; do not leak SQL, provider/framework exceptions, stack traces or secrets. Machine error codes, field names and capability IDs do not change with locale; products may translate user-facing explanations while preserving those contracts. [Internationalization](../web/seo-and-semantic-web.md#internationalization-and-directionality) owns language and presentation guidance. Specify retryability and request/operation correlation when callers need them.

Preserve supported `errors.Is` categories while adding safe explanations or component-owned field details. Capture the original cause and phase at the owning protected diagnostic boundary **before** canonicalizing a public dependency error. Public unwrap chains must not carry private provider objects. [Operations](../operations/operability-performance.md#diagnosis-and-audit) owns the protected hook, useful structured evidence and bounded suppression.

Distinguish body timeout before dispatch, explicit denial, failed policy evaluation, wrong credentials and expired sessions. A transport may infer `outcome: not_executed` only when no domain invocation occurred. After dispatch, an explicit owning-operation result may still prove that no effect execution happened or that attempted persistence did not commit; retain that evidence as `not_executed` or `not_committed` respectively. Dispatch and effect certainty are separate facts. Timeout/503 alone proves neither and is insufficient evidence for replay. A confirmed mutation followed by unavailable permission hints, output serialization or cleanup remains successful, with its resource/reconciliation identity and a separate diagnostic. Partial response delivery can be uncertain even when the server knows the mutation committed.

Preserve a known outcome and authorized resource identity before a protocol SDK validates or serializes output. Error conversion must not discard a partial result that carries the only reconciliation reference. Failed MCP tools use the owning adapter's documented safe machine-error representation without placing an incompatible error object under the tool's success output schema. An entirely lost response may also lose a server-generated resource ID; if the product has no client operation key or authorized status contract, state that recovery limit and do not invent automatic replay.

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

[Audit Query and Export](../../audit/remote/README.md) each return one exact-target page, defaulting to 20 records with a maximum of 100. Their permissions are separate and rechecked for every page; a cursor is bound to its original target. Authorized empty results are empty arrays and do not establish another domain object's existence. The retained sequence orders visible committed records, but pages are not a consistent snapshot: concurrent insert transactions can become visible out of sequence, including behind a previously returned cursor. Traversal alone cannot establish a complete export. A product needing that guarantee must use its supported write-quiescence or database snapshot procedure.

## Evolution

Prefer additive change. Breaking public semantics/IDs require explicit versioning, migration guidance and an appropriate retirement path. Persisted events/messages/jobs must remain interpretable for their required lifetime; do not silently reinterpret payloads. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns activation and recovery; [Licensing](../legal/licensing.md) and [Trademark Policy](../../TRADEMARKS.md) own artifact terms and official representation.

## Initial Go public surface

The Core import is `github.com/AChWorks/achrix`. [Package source](../../achrix.go) and Go documentation own exact signatures; unexported state/helpers are private. This pre-v1 Core surface is deliberately small:

| Contract | Intended consumer use |
| --- | --- |
| `Capability`, `Descriptor`, `Module` | Stable ID and positive capability ABI revision; implementation version; provided/required/optional compiled composition; owned `Start`, `Ready`, `Stop` |
| `Config`, `New`, `Application` | Explicit instance-owned composition; validate missing/duplicate/incompatible/cyclic dependencies before resources/traffic |
| `Principal`, `Policy`, `PolicyFunc`, `Application.Authorize` | Product-authenticated principal; consumer policy; fail-closed checks inside typed Application operations before state access |
| `Application.Start`, `Ready`, `Shutdown` | One-shot startup, caller-bounded local readiness, reverse bounded cleanup including the partially started failing component |
| `Application.Components`, `Version`, `DependencyVersion` | Defensive component metadata, actual Core version and source-backed identity for an explicit component module path; local/replacement builds identify themselves as development |
| `ErrComposition`, `ErrDenied`, `ErrAuthorizationUnavailable`, `ErrNotReady` | Inspectable Core categories; explicit denial is distinct from safe evaluation failure. Caller context errors are preserved; raw lifecycle extension errors remain restricted operator-level results |

Published release-specific compatibility history belongs to the [lifecycle and migration guide](../lifecycle/lifecycle-and-compatibility.md); published tags remain immutable. The current Core authorization contract is `achrix.authorization` ABI 2 with bounded deadline/error/admission semantics. The separate Notes consumer provides its own `example.notes.create`/`example.notes.read` ABI 1 contracts and owns their typed Service, Domain, PostgreSQL schema, migration and HTTP mapping; none is a shared Content/Identity Module or additional Foundation export.

### Capability ABI and composition

`Descriptor.Version` identifies the Module implementation's actual packaged source dependency, rather than a generic development-line label or the Core version. Official components have independent module paths/versions; their `Version()` values and descriptors report the resolved owning dependency. Core-backed runtime Modules use `achrix.DependencyVersion(modulePath)`; standalone tooling may read its own Go build metadata without depending on a running Core. Core `achrix.Version()` identifies Core only. A normal tagged/pseudo-version dependency reports that version; local unversioned or replaced builds report explicit development labels. These labels do not identify an immutable artifact, so products retain exact source/build identity as well. `Capability.Version` remains the separate semantic ABI revision. The existing Identity/Audit `ModuleVersion` constants remain deprecated historical source-line labels for source compatibility; do not use them for component/update/recovery identity.

A stable capability ID names one semantic contract. Its positive `Capability.Version` is an ABI revision, independent of the Module's implementation release: additive/backward-compatible changes retain that revision; genuinely incompatible published semantics increment it. Required and present optional capabilities match **exact** revisions. There are no version ranges, implicit ABI upgrades or runtime negotiation. `New` admits one provider and one ABI revision per capability ID, including within a single provider. A real consumer needing old/new contracts simultaneously must explicitly compose a compatibility adapter or deliberately versioned contract strategy; advertising ABI 2 never satisfies an ABI 1 requirement.

`Descriptor.Optional` may name an absent capability without invalidating composition. A present provider must be compatible and starts before the consumer; reverse shutdown follows that order. Invalid/duplicate dependencies, required/optional overlap, duplicate provision and cycles including present optional edges fail before startup. `Descriptor()` is called once per composition and its slices are snapshotted. It must be deterministic, side-effect-free and cheap: no environment/secret access, resource acquisition, network/database calls or mutable registration. Trusted in-process implementations own that obligation; Core does not sandbox a descriptor. Optional metadata performs no service discovery/injection; product code still supplies typed collaborators explicitly.

### Authorization and shutdown

`Policy.Authorize` returns nil to allow, `ErrDenied` (possibly wrapped) to explicitly deny, and another error when evaluation/dependencies fail. Core returns only safe `ErrAuthorizationUnavailable` for that last category, without wrapping the provider error. A dependency's own context failure is unavailable unless the admitted operation itself is canceled/expired. Actual caller cancellation/deadline takes precedence over policy outcomes. Products may map denial to 403 and evaluation-unavailable to 503; this distinction never grants access or makes a domain mutation automatically retryable.

In-process Module/Policy methods must honor context cancellation and return promptly; Policies support concurrent calls. `Ready` and `Authorize` require caller deadlines. A missing authorization deadline returns an inspectable `ErrAuthorizationUnavailable` without invoking Policy. Already-canceled callers retain their context error; non-ready instances reject admission with `ErrNotReady` without waiting behind lifecycle callbacks. Callbacks run outside the short state lock.

Shutdown atomically closes admission, cancels admitted readiness/policy contexts, waits for their callbacks to return, then stops Modules. One shutdown budget covers lifecycle serialization, drain and reverse Stop. A concurrent startup is canceled and owns its bounded partial-start cleanup. If waiting/drain expires, Shutdown returns the context result, admission stays closed and no Stop races a callback; a subsequent Shutdown may finish after it drains. Completed cleanup is not repeated and retains its result. Core cannot forcibly terminate arbitrary in-process code; no worker pool/scheduler or goroutine wrapper is used to pretend otherwise. Products stop ingress and drain their own Application/domain work before shutdown: Core tracks only its own readiness/policy callbacks, not a whole domain operation after authorization.

The [Notes HTTP contract](../../fixtures/notes/README.md#http-contract) calls the same Service/authorization as direct invocation. Driver types/private SQL do not enter Core contracts. The fixture's `internal` packages are product-local, never Foundation internal imports. Its simple JSON error body is evidence for the original bounded proof, not the required future public API envelope. There is no generic dispatcher, dynamic loader, mandatory central service or AI-specific privilege path.

### Development Identity and Audit imports

The optional `github.com/AChWorks/achrix/identity` and `/audit` components have independent Go modules and implementation versions. Their [Identity](../../identity/README.md) and [Audit](../../audit/README.md) contracts require Core authorization ABI 2; Identity also requires Audit append ABI 1. Explicit product composition supplies their typed collaborators and database profile. Their capabilities use ABI 1 and their descriptors identify their own packaged source dependency. Independent versioning does not separate the supported same-database atomic Identity/Audit boundary. Release-specific introduction and compatibility history belongs to the [lifecycle guide](../lifecycle/lifecycle-and-compatibility.md).

Their optional [Identity remote](../../identity/remote/README.md) and [Audit remote](../../audit/remote/README.md) packages bind typed `Account`/`SetEnabled` and `Query`/`Export` operations to HTTP and MCP. Direct services remain available. The owning guides define routes, tools, DTOs and limits; these adapters add no account listing, credential arguments or remote Audit append. Products supply independently audience-bound verification, current connection admission and any narrower channel restrictions before ordinary service authorization.

Identity also supplies separately authorized exact-login metadata lookup for unknown account-creation acknowledgement. `AccountLookup` targets the validated exact login and grants neither listing nor credentials/session access. The [Admin shell](../../admin/README.md) composes finite trusted surfaces, with real forms owned by [Identity](../../identity/admin/) and [Media](../../media/admin/README.md). Navigation permission is only an entry gate; owning Services separately authorize every operation. Presentation transports int64 revision preconditions as exact decimal strings. Release-specific compatibility history belongs to the lifecycle guide.

Identity's permission to attempt authentication uses the fixed `PublicPrincipal` and `Authentication` capability. Successful credentials return an account principal, then each product operation separately authorizes it. Management and self-password change have distinct permissions and Audit append authorization. Account reads expose a revision for subsequent conditional mutations; profiles/roles/permission grants remain product-owned.

The selected account workflow preserves the int64 revision as an exact positive decimal string on HTTP/MCP. `SetEnabled` requires a revision and an explicit boolean, authorizes the exact account and Audit append, and performs no implicit Account read. Every successful call increments the revision, revokes all account sessions and commits one Audit event with the account change, even when the enabled value already matches. It is not idempotent. Preserve the original precondition and known outcome; a timeout, unavailable response or lost response does not authorize automatic replay or replacing the revision with a newer one. Authorized Account/Audit reads can inform reconciliation, but this profile supplies no operation receipt or proof of which concurrent request caused a change.

For the actual atomic Identity events, Audit exposes one deliberate PostgreSQL integration seam: `Prepare` authorizes and derives immutable metadata, then `AppendInTx` receives the owning transaction's native `pgx.Tx` and executes only Audit-owned SQL. Identity owns commit/rollback and must abort on append failure. This narrow provider-specific seam proves one commit boundary without a generic transaction facade, cross-module private SQL, distributed transaction, or portable-provider claim. A prepared operation is bounded by its original deadline/cancellation, Module and single use; it is not a durable permission grant. Source rollback cannot undo either owned migration or a committed account/Audit action.

Trusted ingress may attach `audit.WithAttribution(ctx, actor, value)` before preparation. It binds a copied client/optional-connection/channel/request/operation value to the authenticated actor; `Prepare` rejects a different actor and snapshots that value for the same atomic append. The Identity adapter obtains it only from trusted Bridge context. `Record.Actor` remains the subject, and optional `Record.Attribution` stays absent for historical/direct records that supplied none. Attribution grants no authority and is distinct from protected operational diagnostics. Adoption requires the explicit [Audit schema upgrade](../lifecycle/lifecycle-and-compatibility.md#audit-attribution-schema-boundary).
