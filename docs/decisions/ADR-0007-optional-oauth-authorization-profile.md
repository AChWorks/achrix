# ADR-0007 — Optional OAuth authorization: interoperable profile and security gate

Status: **proposed for independent review**. No runtime authorization server, provider dependency, official release or production activation is approved.
Date: 2026-11-04.
Outcome: [OAuth/OIDC #125](https://github.com/AChWorks/achrix/issues/125), prerequisite of [Bridge #5](https://github.com/AChWorks/achrix/issues/5).

## Current ownership and evidence

[Identity](../../identity/README.md) owns product-local account and session authentication. The optional [Bridge](../../bridge/README.md) owns HTTP/MCP **resource-server** admission: its existing RFC 9728 protected-resource metadata locates configured authorization issuers, while a product-provided token verifier and current application policy decide actual access. Bridge does not run an authorization server, consent flow or refresh-token store. A token must not substitute for the product's current rights.

AChrix is accepted as owner of **reusable optional authorization mechanics**, not product users, product permissions, a central AChWorks account service, or hosted client/onboarding UX. Future AChrix authorization needs its own optional versioned component boundary, using existing Identity and Bridge public contracts without importing their infrastructure or putting OAuth in Core. Products still own sessions, UI, roles, site/resource relationships, runtime/release and credential custody.

## Proposed smallest interoperable profile

For the **first executable issuer slice**, select OAuth **authorization-code with mandatory PKCE S256**, for explicitly enrolled public or confidential clients; implicit and resource-owner-password grants are out. Require canonical HTTPS issuer and one configured resource/audience. Publish RFC 8414 Authorization Server Metadata and RFC 9207 issuer identification in authorization responses, including errors. Bind the code and token exchange to the same exact client, redirect URI, user, issuer and resource. Require the RFC 8707 resource parameter at both authorization and token endpoints; reject token passthrough to another API. Advertise only endpoints and methods that actually work.

**Start with pre-enrolled clients**, each with exact redirect URIs, bounded scopes and token endpoint authentication method. This is a supported configuration path described by [ChatGPT MCP plugin authentication](https://developers.openai.com/plugins/build/auth) and [Claude custom connector setup](https://support.claude.com/en/articles/11175166-get-started-with-custom-connectors-using-remote-mcp); actual client interoperability is **not proved** by those documents. User-supplied client identifiers must not cause arbitrary outbound metadata fetches in the initial profile.

**CIMD, not DCR, is the intended future direction:** [MCP specification 2026-07-28](https://blog.modelcontextprotocol.io/posts/2026-07-28/) prioritizes Client ID Metadata Documents and deprecates DCR for future profiles. The currently evolving [CIMD draft-ietf-oauth-client-id-metadata-document-03](https://datatracker.ietf.org/doc/html/draft-ietf-oauth-client-id-metadata-document-03) is **not an RFC**. Before admitting URL-based clients, separately define SSRF and DNS-rebinding resistance, redirect/domain constraints, issuer-scoped stable identity, bounded fetch size/deadlines/redirect rules, cache revalidation and metadata changes. Implement DCR only for an evidenced legacy client requirement with limits, retention and authorization—not for compatibility speculation.

OAuth does not automatically imply **OIDC identity-provider** support. Do not advertise OIDC Discovery, ID tokens, userinfo or JWKS until a real consumer requires them and signed claims, key storage and rotation are proved. These are still in Issue #125's full outcome, not silently dropped.

## Security invariants and required tests

| Boundary | Requirement and discriminating failure |
| --- | --- |
| User and consent | The real product session identifies the authorizing user. Consent is explicit and binds user, verified client, audience, product/site and scope. Matching email, claimed actor or Gateway session cannot establish an account link. Two users cannot exchange grants. |
| Redirect and anti-CSRF | Registered redirect URIs compare **exactly**; reject wildcard, userinfo, fragments, unregistered callbacks, open-redirect or query confusion. Bind each user-agent transaction and one-use state to its issuer and user. Reject mix-up using RFC 9207 issuer identity. |
| Authorization code | Code is short-lived, unpredictable, hashed at rest and redeemed **once atomically**, with its recorded client/redirect/issuer/resource and S256 challenge. Reject downgrade, missing or wrong verifier, simultaneous redemption, reused code and replay. |
| Audience and scope | Client resource parameter must be exact in both phases; returned token is bound to its sole intended resource. A valid connection scope only narrows product Application authority; wrong audience, site, client or product account is denied. |
| Token profile | Prefer opaque high-entropy short-lived access tokens with durable verifier material and no public JWT dependency initially; validate issuer/resource/expiry/current grant and account status on **every request**. No token content/secret in logs or stored plaintext. |
| Refresh | Support only explicit consented durable grants. Specify refresh lifetime, **atomic rotation**, single-use/replay detection, family invalidation, concurrent refresh behavior, revocation, retained-state recovery and cleanup. Refresh cannot widen scope. |
| Revocation | User or administrator disconnect, user disabled, grant revoked, client disabled or credentials changed must prevent later protected operations. Do not claim completed business effects were undone. A failed revocation dependency must fail closed, never grant stale authority. |
| State | Normal PostgreSQL transactions must own code, grant, refresh and revocation consistency. Specify versioned migrations and restore of retained revocations; code rollback is not data rollback. Do not share Identity's internal tables across component boundaries. |
| Security keys | Secrets are provided by product-controlled secure runtime sources. If asymmetric JWT/ID token support is later selected, first prove key rotation with JWKS/kid overlap and revocation; no hand-written cryptographic primitives. |
| Bounded execution | Limit metadata, request/response bytes, client count, active work, state TTL, consent attempts, token refresh and expensive crypto. Constructed-but-idle capability starts no network poller or per-client worker. |
| Diagnostic boundary | Public denials identify a safe error, correlation and corrective action; protected logs preserve phase and provider/DB cause with targeted redaction. Tokens, codes, verifier, private fields and signing keys never enter public diagnostics. |
| Availability | Issuer/DB/key/consent service unavailable means unavailable; not anonymous access, fallback issuer, implicit approval or a successful empty grant. |

Canonical references: [OAuth 2.0 Security BCP RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html), [RFC 8707](https://www.rfc-editor.org/rfc/rfc8707.html), [RFC 9207](https://www.rfc-editor.org/rfc/rfc9207.html), [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728.html), [RFC 8414](https://www.rfc-editor.org/rfc/rfc8414.html), [Browser-based Apps RFC 10017](https://www.rfc-editor.org/rfc/rfc10017.html). Standards describe required behaviors, not evidence that any chosen SDK implements them.

## Engine/dependency comparison — no adoption yet

| Candidate | Benefit | Cost / proof gap | Recommendation |
| --- | --- | --- | --- |
| Small in-house protocol server with maintained crypto/JOSE primitives | Tight control, small runtime graph | We would own authorization-code, PKCE, refresh, issuer, redirect and registration security indefinitely, including future RFC changes | **Do not write a new security protocol engine merely for speed** |
| [ZITADEL OIDC Go v3](https://github.com/zitadel/oidc) | Apache-2.0, maintained in 2026, in-process provider and explicit storage interfaces; can support OIDC if later needed | Nontrivial dependency graph, concrete PostgreSQL adapter and product session adapter still needed; check resource binding, CIMD and CORS against actual version | **Preferred proof candidate only**; no dependency committed |
| [ORY Fosite](https://github.com/ory/fosite) | Apache-2.0, extensive OAuth/OIDC framework and storage extension | Larger transitive graph and older latest published version; no demonstrated 2026 CIMD/profile fit | Fallback to compare if first candidate fails |
| [ORY Hydra](https://github.com/ory/hydra) or hosted IdP | Maintained separate authorization-service pattern | Separate runtime and provisioning, key custody, deployment/vendor lifecycle, and product consent integration | Optional real-product deployment choice, **not** mandatory Foundation infrastructure |
| Small generic OAuth servers | Potentially fewer packages | No justified OIDC/registration/security completeness from the available evidence | Not selected |

An isolated **API-only Go 1.27.3 spike** resolved ZITADEL OIDC v3.51.17 using ordinary Go modules (41 modules including the spike after tidy) and successfully compiled a test against its OpenIDProvider HTTP handler and authorization storage methods for code issue/exchange, access/refresh tokens, revocation and session termination. This proves that a suitable API seam exists, **not** that its behavior, security, resource semantics, CORS defaults, persistence or client interoperability are acceptable. No dependency was added to AChrix by the disposable spike.

A library is an **engine**, not a product-independent persisted schema or permission source. Keep AChrix grant semantics and identities independent from its internal storage, subject to real integration evidence. Do not import any candidate into Core, Bridge or the existing Notes consumer simply to reserve a framework. Defaults from sample login/storage/CORS implementations are not production decisions.

## Next executable implementation gate

1. In an isolated non-production consumer, prove the chosen in-process library supports the exact code+S256/resource/issuer profile and integrates with the existing product Identity login and Bridge verification callbacks. Inspect exact module versions, dependency/supply-chain exposure, licensing and replacement cost; choose an engine **after** this proof.
2. Implement a dedicated optional authorization component and bounded PostgreSQL storage/secret adapter. Verify atomic code exchange and refresh rotation/replay, multi-user/client/resource/site isolation, valid and invalid redirects, grant revocation and account/permission freshness. Do not implement new product business roles.
3. Prove ordinary UI and MCP/HTTP invoke one owning Application operation with two users and current rights. Where actual clients require it, add CIMD/OIDC/DCR via separately reviewed profiles; do not advertise unimplemented capability or claim real-client support based only on a fixture.
4. Perform supported versioned consumer, migration/restore, resource/latency, local HTTPS/browser and genuine client onboarding acceptance. Require fresh author-separated **HIGH_ASSURANCE** security/correctness review plus Owner authorization for public security/compatibility commitments and any production rollout.

## This package's precise delivery boundary

The accompanying Bridge test proves only that the **existing** protected-resource metadata and anonymous Bearer challenge contain the trusted resource and issuer, even when an HTTP request supplies hostile Host/forwarded headers. It does **not** test OAuth authorization, PKCE, consent, tokens, refresh, a product editor, a real client, or deployment.

This is the first **protocol/dependency decision gate** under Issue #125. The Issue remains open: no issuer, new Module, migrations, new vendor dependency, release or cross-repository mutation is delivered here. The profile remains a proposed security contract until independently reviewed and the consequential decisions are accepted.
