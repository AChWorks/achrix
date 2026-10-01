# Operations, Performance and Cost

## Configuration and readiness

Simple applications run without mandatory auxiliary services. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns infrastructure defaults; introduce them for actual requirements.

Separate business configuration, runtime/deployment configuration, secrets and domain data. Configuration has an owner, meaningful defaults, validation and explicit environment behavior. Shutdown is graceful; readiness probes only dependencies necessary for local readiness, not every external service synchronously.

## Diagnosis and audit

Diagnostic logs use meaningful severity/context and request/job/operation correlation where useful. Never log credentials, keys, tokens or unnecessary sensitive payloads. Model/provider telemetry must not retain sensitive prompts/responses by default.

Logs explain operational failure. Durable audit answers who/what/when/target/outcome/authority for accountable actions; [Security](../security/security-and-authorization.md) owns those requirements. Debug logs are not the audit record.

## Bounds, scale and cost

Apply relevant pagination, query/payload/upload/memory/concurrency bounds, external-call timeouts, retry budgets and queue/backlog limits. Use representative workload evidence before optimization or service/infrastructure extraction; scale the demonstrated bottleneck.

Total cost includes integration/maintenance and operations, CPU/RAM, storage/I/O/connections, network/egress, telemetry/retention, managed services, backups, upgrades and restore. Observability needs useful signals and bounded resources; it must not turn telemetry outages into application outages.

Backups require proportional restore evidence under [Lifecycle](../lifecycle/lifecycle-and-compatibility.md). Fast benchmark throughput alone does not establish safe or cheap operation.
