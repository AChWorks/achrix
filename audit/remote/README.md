# Optional Audit HTTP and MCP pages

`github.com/AChWorks/achrix/audit/remote` binds a product-composed `*audit.Service`
to two explicit reads. The Service owns exact-target permissions, validation,
cursor semantics and retained data. Direct Service calls remain available.

`NewHTTP(service, cfg)` and `NewMCP(service, cfg)` return a `*bridge.Endpoint`.
`Register(registrar, service)` adds the tools during an existing fixed Bridge
registration callback. Constructors start no listeners, sessions, subscriptions,
jobs or background work. Products supply distinct HTTP/MCP resource audiences,
verifiers, current connection admission and protected diagnostics, then mount
each endpoint and its resource metadata explicitly.

| HTTP route below ResourceURL | MCP tool | Owning permission |
| --- | --- | --- |
| `GET /records?target=...&cursor=...&limit=...` | `achrix.audit.query` | Query on the exact target |
| `GET /export?target=...&cursor=...&limit=...` | `achrix.audit.export` | Export on the exact target |

Each request makes exactly one corresponding Service call. Export does not reuse
Query permission. Credential verification and connection admission are current
on every request; the ordinary Service then authorizes the verified internal
principal. Actor, client, channel, request ID and attribution are never public
arguments, and forwarded identity headers do not establish authority.

## Page contract

`PageInput` contains a required exact `target`, optional `cursor` and optional
integer `limit`. The default limit is 20 and the maximum is 100; explicit zero and
null are invalid. Target references use Audit's existing 1–128-character opaque
reference grammar. There is no wildcard, target discovery or account listing.

`Page` contains `records`, always an array including when empty, and an optional
`next_cursor`. An authorized target without visible records returns HTTP 200 and
an empty array. That response makes no claim that another domain object exists.
Denied targets return 403 without an existence lookup.

Keep the cursor unchanged and use it only with its original target. The owning
Service validates its canonical 34-character representation and encoded
position. Every subsequent page reauthorizes; a cursor is not a permission.
The implementation reads only committed visible records. **Pages are not one
consistent snapshot**, and concurrent insert transactions can become visible
out of sequence. Export is one bounded JSON page, not an unbounded file, job,
subscription or completeness guarantee. Complete consistent exports require the
product's supported write-quiescence or database snapshot procedure.

`RecordInfo` exposes only `id`, `actor`, `action`, `target`, `authority`, `outcome`,
`occurred_at`, and optional `attribution`. IDs stay opaque strings; timestamps are
UTC RFC3339 at retained microsecond precision. The internal database sequence is
not exposed. The retained outcome currently supports only `succeeded`; failed
transactions and routine denials remain operational diagnostics.

Attribution is a separate explicit snake_case DTO with `client_id`, optional
`connection_id`, `channel`, `request_id` and `operation`. It represents provenance
that the owning domain atomically retained using Audit's actor-bound trusted
context API. It contains no credential and grants no access. Historical or direct
records with nil attribution omit the entire field; no client or transport is
guessed. Actor remains the subject already stored on the record.

The database's append guards and startup presence checks protect ordinary
application integrity. Privileged database operators can change schema and
guards; this is not cryptographic tamper evidence or continuous detection of
privileged changes.

## Input, failures and limits

HTTP accepts only the published GET routes and query names, each at most once,
with at most 512 encoded query bytes. It rejects bodies, unsupported methods,
unknown/repeated query fields and noncanonical paths without redirects. A 405
response advertises GET. MCP uses the maintained SDK's typed input validation;
this package adds no alternate JSON-RPC parser or independent duplicate-key
guarantee. Discovery and read-only/idempotent annotations never grant access.

The ordinary Bridge byte, duration and concurrency limits apply. There is no
streaming export or automatic accumulation/retry loop. Each page is also bounded
by the Service's record count and field lengths.

Failures use Bridge's safe HTTP Problem Details or MCP tool-error projection,
including code, message, correction action, outcome and generated request ID.
Owning InputError field/reason guidance does not echo submitted values. Denial is
403, invalid arguments 400, active-capacity failure 429, and readiness, policy
evaluation or dependency failures 503. An unsupported route is 404; missing
records for an authorized target are an empty successful page.

Owning Query/Export failures use `not_executed` to mean no state-changing effect was
attempted. The diagnostic `Dispatched` flag separately says whether the read was
actually invoked. This does not describe the outcome of any earlier mutation.
A later output/delivery failure preserves a successfully completed read's known
outcome; it does not imply that retained data changed.
Failures expose no partial page or requested target as a verified recovery
reference. Original causes remain protected; wire errors do not serialize them.
Products retain Audit/Core provider causes through their diagnostic callbacks,
correlating the trusted Bridge request context before public mapping. This is
operational evidence, not a new retained Audit event for reading Audit.

## Schema and integration

`OpenAPI()` returns a defensive copy of the embedded OpenAPI 3.1.1 JSON contract.
Replace its example server URL with the configured product HTTP resource when
publishing. Its explicit input/output fragments also supply the actual SDK tools.
The contract version names this transport surface, not the Go module release.

Focused tests exercise the real Service/Core validation and denial seams without
starting storage, strict public HTTP/SDK input, separate operation and audience
identity, safe faults, immutable projections, and actual SDK catalog/schema
equality. The normal Notes consumer supplies real PostgreSQL/TLS/SDK successful
pages, current permissions, attribution from Identity mutations, atomic failure
and restore evidence. Audit owns its genuine v1 upgrade and transaction tests.

This optional package shares Audit's module version. Its module graph contains
Bridge and the maintained SDK, while direct `audit` imports remain free of those
compiled presentation dependencies. No Core coupling, new Go module, append
tool, editing/deletion operation, arbitrary payload, secret ceremony or product
authorization policy is introduced.
