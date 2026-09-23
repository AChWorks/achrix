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

## Module boundaries

A module must not gain another module's authority by importing its Infrastructure internals or writing its tables.

Cross-module operations use an authorized Application boundary.

## Machine/AI access

AI/MCP/API access is never more privileged merely because it is automated.

Every machine action must execute under an explicit principal/context and permission boundary.

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
