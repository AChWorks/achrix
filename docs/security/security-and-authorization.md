# Security and Authorization

## Default stance

- default deny;
- least privilege;
- explicit trust boundaries;
- server-side authorization;
- validation at ingress;
- secret minimization;
- safe failure.

## Authentication vs authorization

Authentication answers who/what the principal is.

Authorization answers what that principal may do to which resource.

Do not encode business authorization only as UI visibility or role-name conditionals.

Roles may be convenient permission bundles; policies/capabilities own enforcement semantics.

## Secure implementation discipline

Security is enforced by implementation and review; scanners/WAFs/headers are supporting controls, not substitutes for correct code.

- Validate and canonicalize untrusted input at the owning ingress while preserving domain invariants at deeper boundaries. Bound body, field, collection, recursion, decompression and upload work before expensive processing.
- Use parameterized database APIs and owned query code. Never construct SQL or authorization/resource identifiers by concatenating untrusted values.
- Encode/escape output for its actual sink (HTML/attribute/URL/JSON/etc.); do not treat input sanitization as universal XSS prevention. Keep templates/renderers responsible for safe presentation.
- Treat file paths/object keys, redirects and outbound URLs as security boundaries. Generate storage identities independently from user filenames, prevent traversal, and apply explicit SSRF/scheme/host/redirect policy to server-side fetches.
- Browser state-changing operations use explicit CSRF protection appropriate to the session model; SameSite is defense in depth rather than the sole CSRF control.
- Never invent cryptographic/password/token protocols. Use maintained libraries and current recognized guidance, preserve algorithm/version parameters with stored credentials where needed, and design migration/rehash paths before parameters become stale.
- Expected bad input/authentication/authorization failures remain safe and bounded; they must not panic, leak internal details or create unbounded CPU/memory/log/audit work.
- Security-sensitive concurrency/state transitions (session rotation/revocation, permission changes, password changes, recovery, lifecycle updates) define atomicity and stale/replay behavior explicitly.
- HIGH/CRITICAL changes to credentials, sessions, authorization, sensitive data, untrusted file/network boundaries or update trust require independent security/correctness review and discriminating tests. A green static/vulnerability scanner never waives code review.

Use OWASP guidance such as [Secure Code Review](https://cheatsheetseries.owasp.org/cheatsheets/Secure_Code_Review_Cheat_Sheet.html), [Session Management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html), [CSRF Prevention](https://cheatsheetseries.owasp.org/cheatsheets/Cross-Site_Request_Forgery_Prevention_Cheat_Sheet.html), [Password Storage](https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html), [HTTP Security Headers](https://cheatsheetseries.owasp.org/cheatsheets/HTTP_Headers_Cheat_Sheet.html) and [Logging](https://cheatsheetseries.owasp.org/cheatsheets/Logging_Cheat_Sheet.html) as current implementation references where applicable; verify current recommendations when coupling security-sensitive code.

## Product accounts and optional SSO

Products remain independent owners/operators of their account namespace/data and authorization; single sign-on (SSO) is optional. The reusable [Identity Module](../architecture/module-model.md#identity) may implement generic account/authentication/session mechanics inside each product without creating shared central account ownership.

Identity may own generic local account status, credential/session lifecycle and external-identity mapping where composed. Each product/domain still owns product-specific profile semantics, domain data, permission grants and release boundary. Reusing Foundation identity capabilities does not imply one shared user table or one central AChrix runtime.

For first-party browser administration, the starting authentication direction is an **opaque server-side session**, not a long-lived bearer/JWT credential stored in browser Web Storage. Generate high-entropy session identifiers, keep server-side revocation/expiry, rotate at authentication/privilege-sensitive transitions as required, and exchange the session only through HTTPS cookies with explicit `Secure`, `HttpOnly` and deliberate `SameSite`/host/path scope. Prefer host-only `__Host-` cookie semantics when the deployment does not need deliberate cross-subdomain sharing. Do not accept session identifiers through URLs.

For this stateful session model, start with a synchronizer-token CSRF design bound to the session and add Origin/Referer validation where appropriate as defense in depth. SameSite is useful but is not the sole CSRF control. Exact schema, hash-at-rest strategy, expiry/concurrency policy and cookie semantics are owned and tested by the Identity implementation.

For new local password storage, Argon2id is the preferred starting algorithm unless a documented deployment/compliance constraint requires another maintained option. Choose and record current parameters from maintained guidance at implementation time, keep per-credential algorithm/parameter identity so stronger settings can migrate, and rehash deliberately after successful verification when policy advances. Never use fast general-purpose hashes as password storage.

Where a product actually needs SSO, integrate a maintained identity provider through a standard such as OpenID Connect. Keep the provider identity and its explicit mapping to the local product account separate from product authorization. Do not merge accounts merely because email addresses match, or treat successful SSO as permission to access another product's resources.

Cross-product data sharing requires explicit authorization and a defined data boundary. Define session, account-linking, deactivation, and provider-unavailability behavior for the real integration.

A public/self-hosted consumer must remain independently operable without an AChWorks identity service. Reusable Identity/OIDC support does not require a company identity service or central authentication dependency.

## Module boundaries

A module must not gain another module's authority by importing its Infrastructure internals or writing its tables.

Cross-module operations use an authorized Application boundary.

Derived/search/cache projections do not acquire authority by copying data. Permission/deletion changes must propagate with an explicit freshness/reconciliation contract, and query results/counts/snippets/facets must not reveal state the principal could not read through the authoritative Application boundary. Provider-native search/cache security may be defense in depth, not a substitute for product authorization.

## Machine/AI access

AI/MCP/API access is never more privileged merely because it is automated.

Every machine action must execute under an explicit principal/context and permission boundary. Discovery is not permission. Use product-required preview/approval boundaries for sensitive mutations; [Contracts](../architecture/contracts-and-interfaces.md) owns concurrency and replay safety.

Do not expose:

- arbitrary SQL;
- arbitrary shell;
- unrestricted HTTP proxying;
- unrestricted filesystem access;
- secrets;

as generic AI capabilities merely for convenience.

## Secrets

Secrets must not live in:

- Git;
- public logs;
- normal business records;
- AI capability metadata;
- exception output;
- catalog/README docs.

Use the approved runtime/secret mechanism appropriate to the deployment.

## Browser/web boundary

First-party Admin/browser surfaces are same-origin by default. Do not enable broad CORS merely because an API exists; introduce explicit trusted origins/methods/credentials only for a real browser cross-origin consumer.

Render untrusted content with context-appropriate escaping and keep the browser defense-in-depth policy appropriate to the actual surface:

- correct `Content-Type` and `X-Content-Type-Options: nosniff`;
- Content Security Policy, including framing control (for example `frame-ancestors`) for interactive Admin pages;
- an explicit referrer policy;
- `Cache-Control: no-store` or another deliberate private/sensitive policy for authenticated sensitive responses;
- no obsolete `X-XSS-Protection` dependence or technology-disclosure headers as a security strategy.

HSTS belongs to the supported HTTPS/deployment profile because an incorrect long-lived domain policy can lock out legitimate clients. Enable it deliberately where TLS/domain/certificate operations support it rather than hardcoding a universal preload policy in Core.

## External integrations

Provider credentials belong to the narrowest integration boundary.

Outbound integrations should consider, as relevant:

- TLS verification;
- SSRF/redirect policy;
- timeouts;
- payload bounds;
- signature/authentication verification;
- replay protection;
- idempotency;
- secret-safe logging.

## Abuse and resource protection

Authorization answers whether an actor may perform an action; it does not by itself protect the system from abusive or accidental resource consumption.

For public or untrusted boundaries, apply only the controls justified by the threat/workload, such as:

- request/payload/upload size bounds;
- rate limits or quotas;
- concurrency limits;
- anti-automation/anti-spam controls;
- webhook/replay protections;
- expensive-operation budgets;
- provider/API quota protection.

Place these controls at the most effective boundary (edge, application, module, provider adapter, or infrastructure) rather than forcing one universal limiter into Core.

Do not add abuse infrastructure to private/low-risk paths without evidence, but do not leave a public expensive or security-sensitive operation unbounded merely because authentication exists.

## Audit

Sensitive/admin/financial/security actions may require a durable audit trail. The reusable [Audit Module](../architecture/module-model.md#audit) owns generic accountability-record mechanics when composed; each product/domain still decides which actions require audit and the business classification of their payload.

Audit should capture enough to answer who/what/when/target/outcome/authority without storing secrets or excessive sensitive payloads. Critical actions define whether the audit record must share commit semantics with domain state or how non-atomic outcomes are durably reconciled.

Operational debug logs are not the audit source of truth. Repetitive invalid/denied traffic is not automatically an audit event; retain only the accountable/security evidence the product actually needs and bound attacker-controlled event volume.

## Dependencies

Security-sensitive protocol/crypto/auth implementations should normally use established maintained libraries rather than custom cryptography/protocol code.

Prefer a small, reviewed dependency graph. Verify source/license/checksums and use low-noise vulnerability analysis that understands reachable Go code (for example maintained `govulncheck`) where it improves signal. Static/vulnerability tools complement focused tests and review; do not stack overlapping scanners merely to increase tool count.

## Vulnerability reporting and response

Use [GitHub private vulnerability reporting](https://github.com/AChWorks/achrix/security/advisories/new) for suspected AChrix vulnerabilities. Include affected version/commit, impact and minimal safe reproduction; omit live credentials, customer data and unrelated private material. Do not disclose exploitable details in public Issues/PRs. If the private form is unavailable, request a private contact using public-safe details only.

AChWorks maintainers own triage, affected-version assessment, fix review and coordinated advisory/release publication. Enabling reporting is not a response-time SLA, a security certification or authorization to test someone else's systems. [Operations](../operations/operability-performance.md#supported-environment) owns supported environments; [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns release/update evidence. Product operators own deployment and credential/incident actions in their own systems.
