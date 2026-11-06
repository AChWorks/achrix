# Optional OAuth authorization — Issue #125 candidate

**Status: experimental / not approved for release or production.** This Go module
is a **non-production design and interoperability candidate**; it does not
establish an accepted permanent protocol engine, a stable API, a deployable
identity provider or a supported public OAuth endpoint. Its author must not
independently approve the security review. The owning decision remains
[ADR-0007](../docs/decisions/ADR-0007-optional-oauth-authorization-profile.md)
and [Issue #125](https://github.com/AChWorks/achrix/issues/125).

The optional AChrix component owns authorization mechanics and its separate
PostgreSQL oauth schema. Multiple explicitly configured issuers may share this
schema: pending sessions are unique per (issuer, resource, session binding),
and Disconnect, token revocation and cleanup are issuer/resource-scoped.
The schema migration ledger and native backup/restore cover **all** issuer rows;
operators must protect and restore the shared state as one consistent unit.
There is no automatic discovery, routing or token fallback between issuers. It does **not** own product users, their passwords,
interactive sessions, business policy, resource or site authorization, or the
deployment. It does not import Identity, Bridge or Core into production code, and never
starts a hosted identity service. Its private adapter imports pinned
`github.com/zitadel/oidc/v3 v3.51.17` **only for supported OAuth protocol
validation**; it does not enable that library's full OIDC Provider, ID-token
issuance, default routes or permissive CORS policy. The consumer proof uses
these products **only in its Go test imports**, together with the maintained
golang.org/x/oauth2 client to exercise a real code exchange.

## Currently implemented and tested first-profile paths

- RFC 8414 issuer metadata; pre-enrolled public or client_secret_basic
  confidential clients; single HTTPS issuer and sole configured RFC 8707
  resource; Authorization Code plus mandatory PKCE S256 and exact registered
  redirect URI. A **pre-enrolled public URL client ID** (e.g. the operator-approved
  `https://chatgpt.com/oauth/client.json`) may be used directly without a
  staging alias, but only if the product has already verified and enrolled its
  canonical metadata document and precise callback. This is the
  *pre-registration* mode of the CIMD Internet-Draft, not dynamic discovery.
- Product-attested active Identity session and one-use, expiry-bounded consent
  transaction with independent server-side CSRF and strict POST Origin checking.
  Product-provided RenderConsent displays the consent UI in its own language,
  design and accessibility system. AChrix never renders its own HTML form.
  A new transaction for the same authenticated session replaces its prior
  pending transaction, bounding consent state per browser session.
  An authorization result includes the RFC 9207 iss and echoes the client's
  distinct state without treating it as server-side CSRF.
- PostgreSQL atomic one-use code redemption; opaque SHA-256-at-rest access and
  refresh tokens; client, user, issuer, resource and consent-scope ceilings.
  Access expires after ten minutes; refresh has an absolute fourteen-day
  expiry. A reused refresh token revokes its entire grant family.
- Current product identity, client enablement and per-request rights via the
  caller's mandatory Permit callback, not a snapshot of account permissions.
  Explicit Disconnect and authenticated RFC 7009 token revocation operate
  separately from product login/session logout. Logout must **never**
  implicitly revoke already issued grants or tokens; a logged-out/rotated
  product session cannot continue an unfinished consent transaction.
- Explicit migration with immutable checksum ledger, manual expiry cleanup
  and 30-day orphan-grant reconciliation retention,
  limited concurrent HTTP operations and body sizes. Construction starts no
  goroutine, network listener or database worker. Each HTTP request has a random non-secret X-Request-ID reflected through
  oauth.RequestID(ctx) into protected diagnostics. Operators must explicitly schedule
  `Prune`; no background maintenance is auto-started. All long-lived diagnostics,
  database backups, privilege controls, TLS ingress and secrets belong to
  the product/operator.

There is **no** OIDC, ID token, JWT/JWKS, userinfo, **automatic/unregistered**
CIMD client discovery, DCR, dynamic client registration, `private_key_jwt`,
automatic consent, central login or mandatory hosted IdP. Only explicitly
pre-enrolled URL identifiers are supported; the issuer **never fetches a URL
supplied by an OAuth request**. Local product authentication remains independent.
This implementation deliberately does not claim production protocol compliance
or full real-world AI-client onboarding from compilation, metadata or a fixture.


## Concrete protocol/engine responsibility map (candidate, not a public contract)

| Operation | ZITADEL v3.51.17 is authoritative for | AChrix/product remains responsible for |
| --- | --- | --- |
| Authorization request | `op.ValidateAuthRequestClient`: registered callback, response type, client scope eligibility, code challenge method | Exact RFC 8707 resource, mandatory S256 (SDK also permits `plain`), ignore unrecognized authorization parameters (RFC 6749 §3.1), reject duplicate supported parameters and unsupported OIDC scopes, current Identity session/Permit, product consent UI and one-use CSRF |
| Client authentication | SDK request parsing and Code/Refresh validators, RFC 6749 Basic decoding and `AuthorizeClientIDSecret` with live `ClientLookup` and AChrix-supplied constant-time digest check | Restrict methods to the *advertised* Basic-or-none profile: reject credential-in-form, public Basic, confidential missing Basic and mixed identity; apply immutable registration ceiling |
| Code exchange | `op.ParseAccessTokenRequest` + `op.ValidateAccessTokenRequest`: client, stored code, redirect and S256 verification **once** | Short-lived opaque code minting at consent; inside a PostgreSQL row lock, compare the already validated code snapshot against the durable record, recheck live Permit and atomically consume/issue |
| Refresh | `op.ParseRefreshTokenRequest` + `op.ValidateRefreshTokenRequest`: registered client and scopes | Original issuer/resource/grant/user binding; PostgreSQL row locks, replay-family revocation, absolute lifetime, live rights and hashed opaque token rotation |
| Revocation | `op.ParseTokenRevocationRequest`: authentication and form decoding | RFC 7009 undisclosing response, exact issuer/resource/client grant update and explicit user-owned Disconnect |
| Discovery, transport, tokens | No full provider routes or ID token/JWKS/CORS behavior enabled | Exact RFC 8414/9207/8707/9728 contract; own opaque access-token verification and ten-minute expiry, HTTP bounded ingress and protected diagnostics |

ZITADEL's `op.CreateTokenResponse` unconditionally enters OIDC ID-token
creation, so neither its full OIDC provider nor token-response helper is safe
for this explicitly OAuth-only, opaque-token profile. The SDK validators
currently require `op.Exchanger`/`op.Storage`; the private adapter implements
the interface and makes all unsupported signing/provider methods fail closed.
They are not product APIs or a second runtime-selectable engine.

This is deliberately one protocol validation authority and one AChrix-owned
durable grant/token state machine, **not** two competing client/PKCE engines.
Whether the resulting bounded library integration sufficiently meets the
Owner's Option B architecture must still be determined by the independent
security/architecture review. It does not imply approval of a validator-only
deviation, public OAuth compatibility or production deployment.

The adapter is pinned to one upstream version. A future SDK upgrade must
re-evaluate `Parse*Request`, `Validate*Request`, client-method restrictions,
OAuth Basic decoding and the required interface stubs against the executable
contract suite. Persistent issuer/resource/grant semantics are AChrix-owned;
a different engine may require deliberate token expiry, re-consent and client
re-enrollment, not seamless token migration. Do not create a generic engine
registry without evidence of an actual second supported implementation.

## Private proof composition

A product can create an explicitly owned connection pool and apply the
schema with oauth.Migrate(ctx, pool), then compose oauth.New(oauth.Config{...}).
The config must provide an exact canonical HTTPS issuer and resource, bounded
pre-enrolled clients, a mandatory live ClientLookup callback, a trusted product
Session callback, a current Permit callback, a product-owned RenderConsent
callback and a protected Diagnostic sink. The caller owns pool.Close() and schema backup.

- Session must authenticate **one current** product Identity cookie/token
  using the actual Identity Service (Authenticate); its binding is the hash
  of the **current opaque session credential**, not the account ID, client
  state or OAuth token. Never trust a browser-supplied principal or Host.
- Permit must check live user enablement, connection scope, site/resource and
  product Application authorization on **every** authorization, exchange,
  refresh and access verification. Explicit policy denial should return
  oauth.ErrInvalidGrant; unavailable dependencies should return
  oauth.ErrUnavailable and fail closed. The initially enrolled client set
  is immutable: the live ClientLookup callback can only reduce active
  privileges and cannot silently add new clients, callback URLs or scopes. Changing the initial registration
  ceiling requires creating a new configured Server; runtime disable/scope
  reductions are checked on every request. It must not return sensitive causes
  into an HTTP response. Product access rights remain narrower than OAuth
  grants even when the token is cryptographically valid.
- **Pre-enrolled CIMD URL clients** must use a canonical HTTPS `Client.ID`
  naming a metadata document on a publicly named DNS host, without userinfo,
  query, fragment, port or encoded/dot-segment path. Only public-client `none`
  authentication is implemented for this subset. The trusted product/operator
  must obtain the document over validated HTTPS **before enrollment** and
  verify its exact `client_id`, declared `redirect_uris`, public authentication
  method (`none`), response/grant compatibility and current owner approval;
  configure only the exact checked callback(s) and allowed scope ceiling.
  It must periodically revalidate this external metadata out of band and
  disable/re-enroll the client on incompatible changes. The issuer will not
  dynamically fetch metadata, follow redirects, crawl arbitrary URLs, trust
  metadata from the OAuth request, or grant privileges beyond the immutable
  enrollment ceiling. When at least one such client is enrolled, issuer
  metadata advertises `client_id_metadata_document_supported: true`; it
  means **trusted pre-registration only**, not open CIMD enrollment. This
  limited mode intentionally avoids request-driven SSRF and DNS rebinding.
  See [CIMD draft §9.2](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-client-id-metadata-document-03).
- The consumer implements Bridge Verify through
  issuer.VerifyAccess(ctx, exactResourceURL, bearer) and maps an invalid
  grant to bridge.ErrInvalidCredential. It retains Bridge Admit and
  operation Gate, and invokes the normal typed Application service with the
  verified user. A token's client_id is not the product principal.
- The Authorization GET response sets `Referrer-Policy: origin` for the
  same-origin HTML consent form: modern browsers otherwise set the POST
  `Origin` header to `null` under `no-referrer`, causing the strict CSRF
  Origin check to reject genuine consent. `origin` discloses no request
  path/query, PKCE state, or code; Token and other endpoints retain
  `no-referrer`. Product renderers must not override this policy to
  `no-referrer` unless they implement a separately reviewed, same-origin
  fetch-based consent submission with a valid Origin. The consent GET also
  scopes CSP `form-action` to `'self'` plus **only the current client's exact,
  pre-registered HTTPS callback origin**. Chromium checks the full form POST
  redirect chain, including the necessary OAuth 303 to that callback; retaining
  `'self'` alone would prevent user consent from reaching the client. The
  product must preserve this constrained CSP or supply a reviewed equivalent;
  do not open arbitrary form destinations, remove CSRF/Origin checks or use 307.
- RenderConsent receives a bounded ConsentView (the authenticated user,
  client, resource, scope, exact redirect, form action, CSRF and short-lived
  transaction). It must safely HTML-escape every untrusted display value,
  localize/accessibly render Approve/Deny without automatic grant, POST
  transaction/csrf/decision to FormAction on the same HTTPS issuer origin,
  and avoid caching/logging the opaque CSRF values. Rendering failures
  must fail closed. The default issuer deliberately has **no built-in
  HTML, login or branding**; the HTML fixture lives only in _test.go.
- The issuer http.Handler must be explicitly mounted on a trusted HTTPS
  origin. There is no reverse-proxy trust of arbitrary Forwarded or
  X-Forwarded-* headers. The product-owned `http.Server` or ingress must also
  bound header reading (`ReadHeaderTimeout`, `MaxHeaderBytes`) and connection
  resources; the handler cannot impose those limits before it is invoked.
  For POST bodies, the handler sets a five-second network read deadline through
  `http.ResponseController.SetReadDeadline`; middleware wrapping its
  `ResponseWriter` must expose `Unwrap()` or the same deadline method. If the
  deadline cannot be enforced, POST requests fail closed with HTTP 503 rather
  than consuming an unbounded slow body. The PostgreSQL role needs restricted
  access to the owned schema. Network and database availability are mandatory
  for protected operations.

The end-to-end regression in identity_bridge_test.go exercises **two real
PostgreSQL-backed Identity users and the real Audit and Bridge modules**. A
maintained Go OAuth2 client obtains a PKCE code over private HTTPS, exchanges
it for opaque tokens and invokes the existing Identity.Service.Account
operation through Bridge. It tests current permissions, logout isolation,
explicit revocation and account separation. This is a protocol/consumer
integration proof, **not** a working ChatGPT/Claude/browser-login product
onboarding implementation. A product still needs an independently reviewed
interactive login/continuation and consent UI, including cross-site navigation
and SameSite cookie behavior.

## Development verification

Production migration accepts UTF8 PostgreSQL **18.6 or later patch releases
within major 18**; newer majors require separate qualification, and older 18.x
releases are rejected. Production does not lock an exact minor patch number.
CI remains reproducibly pinned to 18.6. Use an isolated **PostgreSQL 18.6**
database named achrix_oauth_test, configured as ACHRIX_OAUTH_TEST_DSN. Set ACHRIX_OAUTH_TEST_ROOT to the
absolute task-owned private (mode 0700) cluster directory containing its
mode 0700 socket subdirectory; only this root's Unix socket and the exact
test database name/version may be used for destructive fixtures. Running
without ACHRIX_OAUTH_TEST_DSN skips database tests: a green empty run **does not** prove OAuth behavior. The
full repository validation supplies this database and independently tests
the same module, schema migration, controlled native dump/restore and
the existing pinned Notes consumer.

Run the focused package tests with:

~~~sh
go test -C oauth -race -count=1 -timeout=90s ./...
~~~

modules.json enumerates this proposed optional unit; no release tags
or production activation are permitted by this candidate. The final protocol
contract, retained-state retention/privacy policy, client lifecycle,
AS ceremony logout, user-facing UI and **first-class URL-client onboarding**
still require independent evidence and **author-separated HIGH_ASSURANCE**
review. Actual ChatGPT and Claude OAuth/MCP tests on a disposable QA endpoint
used an explicitly pinned test-only CIMD alias before the first-class
pre-enrolled URL client candidate existed; those tests **do not yet prove**
native ChatGPT onboarding with the first-class URL client registration.

## Unfinished acceptance boundaries

A proposal is **not done** until (1) independent review closes all
security/correctness findings, (2) supported PostgreSQL 18 migration and
recovery, real browser/authenticated client onboarding and genuine MCP/HTTP
consumer behavior are verified, (3) the public protocol contract and selected engine adapter receive
separate acceptance (the concrete v3.51.17 library selection is already
Owner-approved), and (4) release/deployment gates are
separately authorized. Do not turn an experimental in-process issuer into
a public authorization endpoint simply because tests pass.
