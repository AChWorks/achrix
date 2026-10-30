# Multi-Site HTTP and MCP reads

`github.com/AChWorks/achrix/multisite/remote` binds the existing Multi-Site
`Service.Inventory` and `Service.Site` to optional authenticated HTTP and MCP
entry points. Both adapters call the same product-composed Service and its
current Core authorization policy. Same-process consumers continue to call that
Service directly.

This package shares the Multi-Site component's version. Importing only
`github.com/AChWorks/achrix/multisite` does not compile or start Bridge or the MCP
SDK. The component's Go module can still select their dependency versions and
download dependency metadata; optional composition is not a claim of an empty
module graph. Core has no dependency on these adapters.

| Owning operation | HTTP below `Config.ResourceURL` | MCP tool | Current authority |
| --- | --- | --- | --- |
| `Service.Inventory` | `GET /sites?cursor=...&limit=20` | `achrix.multisite.inventory.read` | `multisite.InventoryRead` against empty `InventoryTarget`, then each examined exact site |
| `Service.Site` | `GET /sites/{site_id}` | `achrix.multisite.site.read` | `multisite.InventoryRead` against the exact SiteID before existence lookup |
| `Service.Resolve` | Not exposed | Not exposed | Requires an actual host-lookup consumer before adding a remote profile |
| `Service.PlanActivation` | Not exposed | Not exposed | Pure planning remains separate from actual activation and provisioning |

## Composition and trusted identity

Use `NewHTTP(service, configuration)` and `NewMCP(service, configuration)` for
separate endpoints. `Register(registrar, service)` can instead add these two
tools inside an existing `bridge.New` registration callback. Registration is
fixed at construction. None of these constructors starts a listener, background
worker, socket, poller, timer or per-connection session.

Each `bridge.Config` supplies a canonical resource URL, authorization-server
metadata, a resource-specific credential verifier, current connection admission
and protected diagnostics. Configure different HTTP and MCP resource audiences;
the respective verifier must enforce its intended audience, issuer, expiry,
revocation and trusted account/client mapping. Merely choosing different URL
paths does not authenticate the audience. Scopes and operation `Gate` callbacks
may further restrict a connection; they never replace the Service's current
user permissions. The fixed operation names in the table also identify HTTP
gate checks and diagnostics.

For example, a product may mount the HTTP resource at
`https://product.example/api/multisite` and the MCP resource at
`https://product.example/mcp/multisite`. Mount each returned endpoint without
rewriting its canonical path or Host. Mount its `MetadataHandler()` at its
resource-specific RFC 9728 path, such as
`/.well-known/oauth-protected-resource/api/multisite`. Use the owning Bridge
contract for trusted origins, limits and supported protocol versions.

Caller identity comes exclusively from the verifier-bound Bridge request.
There are no actor, principal, grant or client fields in the operation inputs.
Identity-like request headers do not establish authority. A `site_id` chooses
the exact target that the Service must authorize; it does not prove membership
or produce an authorized reference before successful service completion.

The product still owns ingress TLS, credentials, user/grant state and actual
OAuth consent/account linking. Fixture credentials and resource-server tests
do not prove a production issuer or direct ChatGPT, Claude or Gateway onboarding.
Those acceptance gates remain under [#5](https://github.com/AChWorks/achrix/issues/5).

## Wire contract and bounds

The embedded [OpenAPI 3.1.1 document](openapi.json) is available as a defensive
copy from `OpenAPI()`. Replace its example server URL with the configured HTTP
resource. Its JSON Schema 2020-12 `InventoryInput`, `SiteInput`, `SiteInfo` and
`InventoryPage` definitions also supply the actual MCP input/output schemas.
Authenticated `tools/list` publishes only these two static tool descriptions,
schemas and read-only effect annotations. It contains no site inventory, host
bindings or product configuration. Discovery does not grant invocation rights.

HTTP inventory accepts only optional `cursor` and `limit` query parameters;
the site route accepts none. Unknown or repeated parameters, malformed query
encoding, more than 512 encoded query bytes, a nonempty GET body, unsupported
methods and noncanonical paths are rejected. The handler performs no path
cleaning redirect. A site ID retains exact case and the owning Service's bounded
ASCII syntax. MCP input schemas reject unknown fields, including actor claims.

The absent limit is **20** in either channel. An explicit limit must be an
integer in **1..100**; zero is not a request for the default. The Service bounds
traversal cooperatively to one second, even when sparse permission grants
require examining more configured sites than the requested result count.
Bridge additionally bounds input/output bytes, active requests and deadlines.
Its default 1 MiB output limit can reject an unusually large maximum-size page;
reduce `limit` or deliberately configure an appropriate bounded response budget.

Every page checks collection and exact-site permissions again. A cursor is an
opaque traversal position containing only the last **returned authorized** ID;
it does not retain a grant, encode denied IDs, expose a total, or establish a
snapshot. Missing or empty `cursor` starts traversal. Absent `next_cursor` means
the traversal is exhausted under the permissions evaluated for that request.
Newly granted sites that sort before the cursor require restarting traversal.
A permission-evaluation failure aborts the whole page; accumulated results are
not returned as a partial success.

HTTP example using a product-issued credential:

```sh
curl --fail-with-body \
  -H "Authorization: Bearer ${HTTP_ACCESS_TOKEN}" \
  'https://product.example/api/multisite/sites?limit=20'
```

Successful page shape:

```json
{
  "sites": [
    {
      "id": "site-a",
      "disabled": false,
      "modules": [],
      "revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
    }
  ],
  "next_cursor": "c2l0ZS1h"
}
```

This is a shape example, not an actual configured site or revision. `sites` and
`modules` always serialize as arrays, including empty arrays. `revision` is the
Service's unchanged opaque string; it is never converted to a JSON number.
The exact-site response is the same site object without the page envelope.
An authorized disabled site remains readable and truthfully reports
`disabled: true`; that flag does not define the product's entire lifecycle.

An MCP tool call uses the same inputs, for example
`achrix.multisite.inventory.read` with `{"limit":20}` or
`achrix.multisite.site.read` with `{"site_id":"site-a"}`. The maintained SDK
owns the MCP protocol envelope; applications use the supported Bridge profile
and its client examples rather than inventing another dispatcher.

## Failures and diagnostics

HTTP failures use Bridge's RFC 9457 `application/problem+json` representation.
MCP failures use its safe tool result. Both retain a stable code, useful action,
bounded field guidance when relevant, outcome and server-generated request ID.
Original causes are protected diagnostic values and are never serialized as
outward error strings.

| Component failure | HTTP | Meaning |
| --- | --- | --- |
| `invalid_arguments` | 400 | Correct the indicated field; invalid values and extra authority claims are not echoed |
| `denied` | 403 | Current user/channel authority does not permit the read; denied existing and missing sites have the same response |
| `site_not_found` | 404 | The Service authorized the exact target and then found no configured site |
| `method_not_allowed` | 405 | Use `GET`; the HTTP adapter advertises `Allow: GET` |
| `authorization_unavailable` | 503 | Policy could not be evaluated; no partial inventory is returned |
| `multisite_unavailable` | 503 | Check managed composition and application/module readiness |
| `deadline_exceeded` | 503 | The bounded read ended without a complete result |

Bridge also owns authentication, canonical resource, origin, admission,
concurrency, body/output and response-delivery failures. The OpenAPI failure
schema includes these safe transport responses.

These two owning Services attempt no state-changing effect. Their query
failures therefore carry `not_executed` as that evidence, while the diagnostic
`Dispatched` field separately records whether the read Service was invoked.
Successful Site reads confirm only the authorized returned ID as a reference.
A later output or delivery failure preserves the known success. These outcomes
do not classify an earlier write or grant permission to replay any mutation.

Configure both Core's protected diagnostic callback and `bridge.Config.Diagnostic`.
Core retains the original authorization-provider failure and returns only its
safe category to the adapter; Bridge cannot reconstruct a hidden provider cause.
Both callbacks can use the same trusted request context to correlate verified
subject/client/connection, actual `http` or `mcp` channel, fixed operation and
outcome. Diagnostic logging is not a substitute for required durable Audit.

## Validation ownership

The external-package contract tests use the real managed Multi-Site Service,
Core policy and both transports. They cover sparse traversal, current per-page
permission, anti-enumeration, disabled metadata, bounded/ambiguous input,
resource-specific fixture credentials, protected diagnostics and schema drift.
The independent Notes consumer owns the normal pinned-dependency and actual
HTTPS/SDK composition proof. The owning module's focused tests do not substitute
for the repository's required full candidate gates.
