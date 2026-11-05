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
deployment. It does not import Identity, Bridge, Core, an OIDC framework or an
external hosted identity service into production code. The consumer proof uses
these products **only in its Go test imports**, together with the maintained
golang.org/x/oauth2 client to exercise a real code exchange.

## Currently implemented and tested first-profile paths

- RFC 8414 issuer metadata; pre-enrolled public or client_secret_basic
  confidential clients; single HTTPS issuer and sole configured RFC 8707
  resource; Authorization Code plus mandatory PKCE S256 and exact registered
  redirect URI.
- Product-attested active Identity session and one-use, expiry-bounded consent
  transaction with independent server-side CSRF and strict POST Origin checking.\n  A new transaction for the same authenticated session replaces its prior\n  pending transaction, bounding consent state per browser session.
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
- Explicit migration with immutable checksum ledger, explicit expiry cleanup,
  limited concurrent HTTP operations and body sizes. Construction starts no
  goroutine, network listener or database worker. Each HTTP request has a random non-secret X-Request-ID reflected through\n  oauth.RequestID(ctx) into protected diagnostics. All long-lived diagnostics,
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
The config must provide an exact canonical HTTPS issuer and resource, enrolled
clients, a trusted product Session callback, a current Permit callback and a
protected Diagnostic sink. The caller owns pool.Close() and schema backup.

- Session must authenticate **one current** product Identity cookie/token
  using the actual Identity Service (Authenticate); its binding is the hash
  of the **current opaque session credential**, not the account ID, client
  state or OAuth token. Never trust a browser-supplied principal or Host.
- Permit must check live user enablement, connection scope, site/resource and
  product Application authorization on **every** authorization, exchange,
  refresh and access verification. Explicit policy denial should return
  oauth.ErrInvalidGrant; unavailable dependencies should return
  oauth.ErrUnavailable and fail closed. A pre-enrolled client registry
  passed to oauth.New is an immutable process snapshot: products requiring
  immediate client disablement must check their **live** client registry
  in Permit and explicitly revoke old grants as necessary, rather than
  assuming that editing application config updates an existing Server. It must not return sensitive causes
  into an HTTP response. Product access rights remain narrower than OAuth
  grants even when the token is cryptographically valid.
- The consumer implements Bridge Verify through
  issuer.VerifyAccess(ctx, exactResourceURL, bearer) and maps an invalid
  grant to bridge.ErrInvalidCredential. It retains Bridge Admit and
  operation Gate, and invokes the normal typed Application service with the
  verified user. A token's client_id is not the product principal.
- The issuer http.Handler must be explicitly mounted on a trusted HTTPS
  origin. There is no reverse-proxy trust of arbitrary Forwarded or
  X-Forwarded-* headers. The PostgreSQL role needs restricted access to the
  owned schema. Network and database availability are mandatory for protected
  operations.

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
contract, retained-state retention/privacy policy, client registry lifecycle,
AS ceremony logout, user-facing UI and client onboarding require the Owner's
decision followed by an **author-separated HIGH_ASSURANCE** review.

## Unfinished acceptance boundaries

A proposal is **not done** until (1) independent review closes all
security/correctness findings, (2) supported PostgreSQL 18 migration and
recovery, real browser/authenticated client onboarding and genuine MCP/HTTP
consumer behavior are verified, (3) the engine/dependency and public protocol
contract receive the Owner decision, and (4) release/deployment gates are
separately authorized. Do not turn an experimental in-process issuer into
a public authorization endpoint simply because tests pass.
