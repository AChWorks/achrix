# Private Media HTTP and MCP bindings

`github.com/AChWorks/achrix/media/remote` is an optional package in the existing
Media module. It binds a product-composed `*media.Service` to explicit HTTP routes
and four typed MCP tools. The Service remains the owner of authorization, format
validation, byte integrity, revision preconditions and durable file lifecycle.
Direct in-process Service use remains available.

`NewHTTP(service, httpConfig)` returns a `*bridge.Endpoint` using the bounded
streaming HTTP profile. `NewMCP(service, mcpConfig, httpResourceURL)` composes the
four tools; `Register(registrar, service, httpResourceURL)` is available inside a
product's fixed Bridge registration callback. Constructors start no listeners,
sessions, storage work or background reconciliation.

Configure distinct HTTP and MCP resources and verifier audiences. Each Bridge
configuration supplies credential verification, current connection admission,
protected diagnostics and optional operation gates. The trusted HTTP reference
resource must be a canonical absolute HTTPS URL; loopback HTTP is allowed for
development. It cannot contain credentials, queries, fragments or escaped path
aliases. The adapter does not discover or fetch this URL. Products mount the
endpoint and its separate resource metadata handler at their configured paths.

## Deliberate coverage

HTTP paths are relative to `httpConfig.ResourceURL`.

| HTTP route | MCP tool | Existing Service permission |
| --- | --- | --- |
| `GET /assets?cursor&limit` | `achrix.media.list` | List on `library` |
| `POST /assets` with raw original bytes | Companion HTTP upload | Create on `library` |
| `GET /assets/{asset_id}` | `achrix.media.status` | Read on the exact ID |
| `GET /assets/{asset_id}/content` | Authenticated HTTP reference from ready Status | Read on the exact ID, freshly checked |
| `POST /assets/{asset_id}/delete` | `achrix.media.delete` | Delete on the exact ID |
| `POST /reconcile` | `achrix.media.reconcile` | Reconcile on `library` |

Every request passes fresh verification and admission, and every actual operation
calls the ordinary Service. Actor, client, connection and request identities are
never accepted in arguments or inferred from forwarded identity headers. A tool
annotation or returned content reference grants no authority.

List preserves existing **whole-library metadata** semantics, including
filenames. It does not filter by per-asset Read permission or add content
references. The optional limit defaults to 20 and must be 1 through 100. Use the
unchanged cursor for the next page; traversal uses generated IDs, reauthorizes
each page and provides no multi-page snapshot. Empty `assets` is `[]`.

Status preserves `pending`, `ready`, `deleting` and `deleted`. A deleted asset is
a minimal tombstone containing only `id`, `state`, `revision` and `created_at`.
Cleared or zero metadata is omitted. Revisions are canonical positive signed
int64 **decimal strings**, including values above JavaScript's exact integer
range. Creation times are UTC RFC3339 strings at the owning Service's retained
precision.

Ready Status and successful Create return a structured `content` object:

```json
{
  "url": "https://product.example/api/media/assets/ABCDEFGHIJKLMNOPQRSTUVWXYZ/content",
  "resource": "https://product.example/api/media"
}
```

It contains no token. The URL is built only from the trusted configured resource
and a known generated asset ID. Fetching it requires an HTTP-resource credential,
current connection admission and exact Read permission. Create permission alone
does not grant a later Status or download. The download performs authorized
Status and then the Service's separately authorized, integrity-checked Read.

This profile does not add native MCP attachment upload, MCP resources/read,
public image preparation/export, arbitrary URL fetching, range delivery, or a
hosted-client interoperability claim. Original upload and download use the
companion HTTP leg; metadata and lifecycle tools use ordinary typed MCP.

## Bounded original transfers and JSON

Upload sends one raw body with `Content-Type: application/octet-stream` and one
`Content-Disposition: attachment` header containing exactly one `filename` or
UTF-8 RFC 5987 `filename*` parameter. Quoted semicolons are supported. Duplicate
parameters, fallback pairs, RFC 2231 continuations, extra metadata parameters,
Content-Encoding, trailers and range controls are rejected. The Service owns the
safe filename and enabled file-format rules; the transfer media type does not
override actual file detection or opt a product into additional formats.

Known empty uploads and known Content-Length above 10 MiB fail before dispatch.
Unknown-length bodies stay as streams and retain the owning Service's extra-byte
oversize probe. The adapter does not buffer a full upload. A late body or content
validation failure can occur after pending intent exists, including an empty
unknown-length stream; its returned asset ID and conservative outcome survive.

When the product leaves byte limits at zero, NewHTTP selects request bytes of
`media.MaxUploadBytes + 1` and response bytes of `media.MaxUploadBytes` (10 MiB).
Explicit lower product limits remain effective. JSON mutation bodies are
independently limited to 4096 bytes. A real HTTP server or middleware must support
actual read and write deadlines; Bridge fails before dispatch if it cannot
establish them. Its existing concurrency and duration limits still apply.

Downloads send complete private originals with exact stored MIME, an attachment
filename, exact Content-Length, `private, no-store` and `nosniff`. No attachment
headers or bytes are released before the Service has verified the source size
and digest. A failure before delivery becomes a bounded safe JSON problem. A
failure after headers aborts the transfer without appending JSON or treating a
truncated original as a complete success.

Delete uses one JSON object, for example
`{"expected_revision":"9007199254740993"}`. Use the unchanged revision from
authorized Status. Its HTTP precondition is in the body; a lifecycle or active
storage conflict is HTTP 409, not a blanket If-Match/412 interpretation. The MCP
input additionally includes `asset_id`.

Reconcile accepts `{"limit":10}` or `{}`. Its default is 10 and maximum is 40.
It performs one explicit bounded library-wide batch, aborting interrupted pending
uploads and completing deletion; it never publishes incomplete uploads. Busy
locked assets remain untouched and are counted separately.

HTTP JSON requires one object with only the published fields, no duplicate
members or trailing values. Unknown/repeated queries, unsupported methods,
noncanonical paths and nonempty GET bodies are rejected without redirects.
Relevant content/transfer headers cannot be repeated. MCP argument parsing and
validation belong to the maintained SDK and the published explicit schemas;
this package makes no independent duplicate-key guarantee for MCP JSON-RPC.
Successful JSON operations, including Create, use HTTP 200.

## Outcomes, recovery and protected diagnostics

HTTP failures use Bridge's Problem Details projection; MCP failures use its safe
tool-error projection. Both include a useful `code`, `message`, `action`, outcome
and server-generated request ID, with safe fields and a known asset reference
when available. The HTTP status is independent of an MCP tool result's `isError`.

Input guidance retains the owning `InputError` field and reason without echoing
submitted filenames, cursors or private provider messages. Authorization denial
is 403, authorized missing state is 404, conflicts are 409, bounded active-capacity
failures are 429, and readiness/evaluation/uncertain operation failures are 503.
Transport size and media-type failures use 413 and 415 respectively.

Read failures use `not_executed` to mean **no state-changing effect was attempted**;
the separate diagnostic `Dispatched` flag records whether the query actually
ran. This is not a transaction rollback claim. Dispatched mutation failures remain
`unknown` unless the owning evidence proves a pre-effect input, authorization or
readiness failure. `ErrUnknownOutcome` takes precedence over joined input and
cleanup errors for mutations. A failed Create that returns an ID retains that ID,
even when its safe error is `invalid_arguments`. Failed Reconcile retains decimal
`processed` and `busy` counts in its fault fields.

There is no automatic retry. Preserve the original precondition, inspect an
authorized Status and use explicitly permitted Reconcile when appropriate. A
lost response may also lose the newly generated ID; ordinary List contains only
ready metadata and cannot promise to rediscover that caller's pending upload.
The library-wide maintenance permission is an operator decision, not an implicit
caller-specific recovery grant.

Successful mutations call Bridge Confirm before output. A later serialization
or delivery failure cannot erase a known success and reference. Original request
body and destination I/O failures remain protected causes. Products also wire
Media and Core diagnostic callbacks to retain their original provider/policy
causes before public mapping, using Bridge's trusted request context for
correlation. These callbacks are protected operational evidence, not an atomic
business audit or permission to log raw credentials and private file content.

## Published contracts and consumption

`OpenAPI()` returns a defensive copy of the embedded OpenAPI 3.1.1 JSON document.
The product replaces the example server URL when publishing it. Input/output
schema fragments from that document are supplied directly to the four typed SDK
tools; catalog discovery is not a permission grant.

Focused package tests cover strict HTTP framing, unchanged streaming readers,
exact revisions, tombstones, safe outcome/cause mapping, canonical references,
actual SDK catalog/schema equality and early route rejection. The normal Notes
consumer supplies the real PostgreSQL/TLS/SDK workflow, exact large revisions,
current permission changes, original-byte transfer and a real publication-loss
reconciliation case. Bridge owns the generic socket-deadline and partial-delivery
proofs.

The optional package shares Media's module version. Its module graph includes
Bridge and the maintained MCP SDK; direct imports of `media` do not compile or
run those presentation dependencies. No new module, migration, Core dependency
on Bridge, or product-specific policy is introduced.
