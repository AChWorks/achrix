# Optional OAuth authorization — Issue #125 candidate

**Status: experimental / not approved for release or production.** This Go module
is a **non-production design and interoperability candidate**; it does not
establish an accepted permanent protocol engine, a stable API, a deployable
identity provider or a supported public OAuth endpoint. Its author must not
independently approve the security review. The owning decision remains
[ADR-0007](../docs/decisions/ADR-0007-optional-oauth-authorization-profile.md)
and [Issue #125](https://github.com/AChWorks/achrix/issues/125).

The optional AChrix component owns authorization mechanics and its separate
PostgreSQL oauth schema. It does **not** own product users, their passwords,
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
  redirect URI.
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

There is **no** OIDC, ID token, JWT/JWKS, userinfo, CIMD/DCR, dynamic client
registration, automatic consent, central login or mandatory hosted IdP. Local
product authentication remains independent. This implementation deliberately
does not claim production protocol compliance or full real-world AI-client
onboarding from compilation, metadata or a fixture.

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
- The consumer implements Bridge Verify through
  issuer.VerifyAccess(ctx, exactResourceURL, bearer) and maps an invalid
  grant to bridge.ErrInvalidCredential. It retains Bridge Admit and
  operation Gate, and invokes the normal typed Application service with the
  verified user. A token's client_id is not the product principal.
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

Use an isolated **PostgreSQL 18.6** database named achrix_oauth_test,
configured as ACHRIX_OAUTH_TEST_DSN. Set ACHRIX_OAUTH_TEST_ROOT to the
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
AS ceremony logout, user-facing UI and real client onboarding still require
independent evidence and **author-separated HIGH_ASSURANCE** review.

## Unfinished acceptance boundaries

A proposal is **not done** until (1) independent review closes all
security/correctness findings, (2) supported PostgreSQL 18 migration and
recovery, real browser/authenticated client onboarding and genuine MCP/HTTP
consumer behavior are verified, (3) the public protocol contract and selected engine adapter receive
separate acceptance (the concrete v3.51.17 library selection is already
Owner-approved), and (4) release/deployment gates are
separately authorized. Do not turn an experimental in-process issuer into
a public authorization endpoint simply because tests pass.
