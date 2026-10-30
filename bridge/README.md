# Bridge

`github.com/AChWorks/achrix/bridge` is an optional independently versioned
application adapter for authenticated MCP Streamable HTTP. It uses the official
Go SDK's protocol, typed tool schemas and validation. Importing Bridge is not
required by Core, Identity or ordinary HTTP applications.

This first profile implements a **resource server integration boundary**, not a
production OAuth authorization server. Products supply real credential
verification, user/client binding, current admission and their ordinary typed
application services. It does not implement login, consent, refresh, grant
storage, account linking or actual ChatGPT/Claude onboarding. The Notes consumer
proves the integration with fixture credentials; it is not a production issuer.

## Composition and authority

Construct one `Endpoint` with `New(config, register)`. Inside the registration
callback, use `AddTool[Input, Output](registrar, tool, handler)` to bind deliberate
typed operations. Registration closes when the constructor returns. The SDK
infers schemas when no explicit schema is provided; explicit SDK schemas and
effect annotations remain supported. Do not auto-export internal methods,
database tables or arbitrary URLs. Catalog visibility and annotations never
grant permission.

`Config.Verify` receives only the bearer token. Its product implementation must
verify the issuer, intended resource/audience, expiry and current revocation,
then return a trusted `Credential` with `Caller.Principal`, `ClientID` and an
optional nonsecret `ConnectionID`. It returns `ErrInvalidCredential` for an
invalid, expired, revoked or wrong-resource credential. Other errors mean
verification is unavailable. Bridge independently requires a future expiry,
nonempty bounded user/client identities and all configured scopes; it does not
invent a verifier or freeze business rights in scopes.

`Config.Admit` is required and runs on every authenticated request. The product
checks its current administrator enablement and user/client connection status.
`achrix.ErrDenied` is an explicit denial; an evaluation error is unavailable.
An optional `Gate(ctx, caller, registeredName)` may further restrict the MCP
channel before invoking the service. Every service must still run its normal
application authorization using the current product policy. UI, HTTP and MCP
reuse that same service; there is no parallel AI role database. Revocation and
permission changes affect subsequent requests, not completed effects.

The handler obtains trusted attribution with `RequestFromContext(ctx)`. Client
JSON, MCP `clientInfo`, inbound request IDs, forwarded headers and claimed actor
fields do not establish identity. A Gateway must present a product-bound grant
for the correct user. Its own login token or a matching email is not that proof.

Configure `ResourceURL` and `AuthorizationServers` explicitly. HTTPS is required
except for loopback development. Mount the endpoint at the configured resource
path and `MetadataHandler()` at its RFC 9728 path, for example:

```go
mux.Handle("/mcp", endpoint)
mux.Handle("/.well-known/oauth-protected-resource/mcp", endpoint.MetadataHandler())
```

The canonical Host must reach the handler unchanged. A reverse proxy must be
configured accordingly; Bridge never derives canonical resource or identity
from forwarded headers. Allowed browser origins are explicit, exact origins;
the canonical resource origin is allowed. Resource metadata is public and uses
the SDK's discovery CORS behavior. Credential challenges refer to configured
metadata, not caller-controlled hosts.

`AllowedOrigins` admits an Origin; it does not implement browser CORS preflight.
The endpoint rejects OPTIONS. A real cross-origin browser product must compose
its required CORS route deliberately; that onboarding profile is not proved by
the server-side SDK or same-origin tests.

## Request-only profile and limits

The supported, tested protocol profiles are `2026-07-28` and `2025-11-25`.
Transport responses use JSON. POST requests must still advertise both JSON and
event-stream in `Accept` as required by the SDK's Streamable HTTP contract.
Current-profile `_meta` and HTTP control headers must agree; the SDK performs
that consistency checking. Legacy initialization negotiates supported versions
using the SDK's ordinary handshake. When `Mcp-Protocol-Version` is absent,
Bridge uses the supported legacy `2025-11-25` profile on a cloned request;
headerless legacy initialization remains supported. Each POST contains one
JSON-RPC object. The SDK rejects batch arrays before any operation is invoked,
so a request's outcome, reference and diagnostics belong to one invocation.

No retained session, subscription/listen, resource subscription, keepalive,
event store, sampling, roots, background task or automatic replay is enabled.
GET and session identifiers are rejected. The SDK compatibility switch
`MCPGODEBUG=allowsessionsinstateless=1` is incompatible and makes construction
fail. Creating an endpoint or registering tools creates no dedicated idle
connection worker, timer, poller, socket or external request. Active requests
use temporary SDK sessions that are closed when the HTTP request finishes.

| Bound | Default | Accepted configuration |
| --- | --- | --- |
| Request body | 64 KiB | 256 B–16 MiB; includes chunked input |
| Encoded HTTP response | 1 MiB | 4 KiB–16 MiB; includes failure responses |
| Concurrent requests | 16 | 1–1024; fail fast when full |
| Application request deadline | 15 seconds | 1 millisecond–5 minutes |
| Network response delivery | Request deadline plus 1 second | Fixed finite allowance for a truthful timeout response |

The enclosing `http.Server` owns header, idle-connection and ingress limits.
Read/write network deadlines are applied when its response writer supports
them. Trusted in-process verifier, admission, gate, service and diagnostic
callbacks must honor context and be concurrency-safe; a context cannot forcibly
terminate arbitrary Go code. Product services must bound their output objects
and query sizes: the delivery byte limit does not prevent a handler from first
allocating an excessively large object. There are no automatic retries.

The 4 KiB response minimum reserves space for a useful failure with the complete
256-byte recovery reference even at maximum JSON escaping. Every HTTP problem
is checked after encoding. If its full projection cannot fit the configured
capacity, Bridge returns a bounded HTTP 500 `response_too_large` problem with
the exact known outcome, complete reference and trusted request ID, plus
reconciliation guidance. The original failure and response-limit cause remain
in protected diagnostics. Ordinary field details remain present when they fit;
references and correlation are never truncated to make the response smaller.

## Useful failures and truthful results

Use `Fault` for product-approved safe machine information: `Code`, `Message`,
`Action`, `Fields`, `Outcome` and an authorized `Reference`. A private `Cause`
is sent only to the protected diagnostic callback. Product fields must already
respect caller authorization and anti-enumeration rules. Reference is a maximum
256-byte nonsecret reconciliation identifier, not a credential. Safe messages
and actions are capped at 512 bytes, field maps at 16 entries, field names at
64 bytes and values at 256 bytes. Do not mutate returned data concurrently.

| Outcome | Meaning |
| --- | --- |
| `not_executed` | Bridge rejected before invocation, or the owning service explicitly proves no effect execution occurred |
| `not_committed` | The owning service proves an attempted mutation did not commit |
| `succeeded` | The service confirmed success, independently of whether its response could be delivered |
| `unknown` | The operation was invoked and there is insufficient evidence of success or noncommit |

An ordinary error after dispatch defaults to `unknown`. Timeout or HTTP 503
does not prove non-execution. A successful typed return records `succeeded`
before SDK output validation. Call `Confirm(ctx, authorizedReference)` after
confirmed success when the resource ID must survive later serialization or
delivery failure. A later error cannot erase that confirmed success/reference.
An uncertain product write uses `Fault{Outcome: Unknown, Reference: id, ...}`.
Bridge never infers an ID by reflecting over an output or repeats an operation.

MCP tool failures have `isError: true` and one text content block containing:

```json
{
  "code": "unknown_write",
  "message": "Write acknowledgement was lost.",
  "action": "Read the authorized reference to reconcile before repeating this write.",
  "outcome": "unknown",
  "reference": "server-generated-resource-id",
  "request_id": "bridge-generated-request-id"
}
```

Optional `fields` give safe correction details. Error content deliberately
does not populate the tool's success `structuredContent` schema. Successful
typed output remains ordinary SDK structured content. HTTP boundary failures
use Problem Details with the same safe extensions and `X-Request-ID`.

SDK input rejection retains useful field/constraint hints: missing `text`,
wrong `text` type and an unrecognized `actor` field produce different guidance.
SDK v1.8.0 formats its input validator error with `%v`, losing its unwrap chain;
the same pinned `jsonschema-go` validator is therefore used **only after a
rejection** to recover bounded schema-path/constraint information. Normal
successful inputs are not validated twice. Raw validation messages can include
input values and remain protected. Complex unrecognized paths retain a safe
schema-level hint rather than exposing arbitrary error text.

`Config.Diagnostic` is a required product composition boundary. It receives
original causes before public projection and terminal operation evidence,
including success with `Cause == nil`. `Dispatched` separately records whether
the typed application handler ran; it does not imply a mutation committed.
Phases distinguish authentication, admission, gate, input, invocation, output,
body read and delivery. Request/user/client/channel/registered-operation
correlation and authorized references remain useful even when a later delivery
fails. The SDK's ordinary logger is disabled; Bridge does not log bearer tokens,
payloads or private provider errors. Products may record selected safe fields
in ordinary logs while protecting `Cause` through appropriate access, storage,
export and retention. The callback owns bounded repetition handling without
silently merging distinct important failures. Business Audit records and their
transaction boundary stay with the application service.

A delivery error may occur after a confirmed commit. If an entire response is
lost, the client may also lose its generated reference. This adapter supplies
no durable client operation ledger; it cannot promise complete lost-response
recovery, and a later not-found read does not by itself prove a write never ran.

## Validation and versioning

From this module directory, run `go test ./...`, `go test -race ./...` and
`go vet ./...`. Repository-root tests do not test nested modules. The external
contract suite uses real HTTP plus the official SDK client for both profiles;
Notes owns the PostgreSQL-backed shared-policy and acknowledgement-loss proof.
These tests are not a claim of production OAuth or live ChatGPT/Claude support.

`Version()` reports this component's packaged source identity using Core's
dependency metadata. Bridge uses its own normal immutable module pin. It owns
no Core or Identity schema change and no global service. Removing the handler
stops new admission; it does not reverse completed application effects.

First-party code is MPL-2.0, as recorded in [LICENSE](LICENSE). Dependencies
retain their own terms. The official Go SDK v1.8.0 license records an
MIT-to-Apache-2.0 transition with remaining MIT contributions; do not relabel
the complete dependency as exclusively one license. Repository third-party
notices and the exact resolved dependency license files govern distribution.
