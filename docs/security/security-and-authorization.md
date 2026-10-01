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

## Product accounts and optional SSO

Products keep independently owned accounts and authorization; single sign-on (SSO) is optional.

Each product owns its account/profile lifecycle, domain data, permission grants, and release boundary. Reusing Foundation identity capabilities does not imply one shared user table or one central AChrix runtime.

Where a product actually needs SSO, integrate a maintained identity provider through a standard such as OpenID Connect. Keep the provider identity and its explicit mapping to the local product account separate from product authorization. Do not merge accounts merely because email addresses match, or treat successful SSO as permission to access another product's resources.

Cross-product data sharing requires explicit authorization and a defined data boundary. Define session, account-linking, deactivation, and provider-unavailability behavior for the real integration.

A public/self-hosted consumer must remain independently operable without an AChWorks identity service. This policy neither selects an identity provider nor requires an SSO adapter or central identity service in Issue #1.

## Module boundaries

A module must not gain another module's authority by importing its Infrastructure internals or writing its tables.

Cross-module operations use an authorized Application boundary.

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

Sensitive/admin/financial/security actions may require a durable audit trail.

Audit should capture enough to answer who/what/when/target/outcome/authority without storing secrets or excessive sensitive payloads.

Operational debug logs are not the audit source of truth.

## Dependencies

Security-sensitive protocol/crypto/auth implementations should normally use established maintained libraries rather than custom cryptography/protocol code.

## Vulnerability reporting and response

Use [GitHub private vulnerability reporting](https://github.com/AChWorks/achrix/security/advisories/new) for suspected AChrix vulnerabilities. Include affected version/commit, impact and minimal safe reproduction; omit live credentials, customer data and unrelated private material. Do not disclose exploitable details in public Issues/PRs. If the private form is unavailable, request a private contact using public-safe details only.

AChWorks maintainers own triage, affected-version assessment, fix review and coordinated advisory/release publication. Enabling reporting is not a response-time SLA, a security certification or authorization to test someone else's systems. [Operations](../operations/operability-performance.md#supported-environment) owns supported environments; [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns release/update evidence. Product operators own deployment and credential/incident actions in their own systems.
