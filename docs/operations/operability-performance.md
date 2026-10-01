# Operations, Performance and Cost

## Configuration and readiness

Simple applications run without mandatory auxiliary services. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns infrastructure defaults; introduce them for actual requirements.

Separate business configuration, runtime/deployment configuration, secrets and domain data. Configuration has an owner, meaningful defaults, validation and explicit environment behavior. Shutdown is graceful; readiness probes only dependencies necessary for local readiness, not every external service synchronously.

## Supported environment

This document owns the runtime/database support matrix; manifests own pins and Git/CI own proof. Begin with one upstream-supported Go line and PostgreSQL major, using maintained patches. Verify exact versions before implementation; expand only for real consumers and tested compatibility. No executable matrix exists yet.

Releases declare maintained lines and upgrade/retirement paths. Older lines need explicit support; there is no permanent LTS promise. Validate dependency/security updates before consumer activation under [Lifecycle](../lifecycle/lifecycle-and-compatibility.md).

## Diagnosis and audit

Core, Modules, adapters and product behavior use a common structured diagnostic path under the application's configuration. Attach time, severity, component identity and useful request/job/operation correlation; add release/version and trace context where they improve diagnosis. Propagate relevant context across boundaries instead of creating unrelated per-Module logs. Prefer the existing standard-library logging default in ADR-0003 over another logging framework.

Keep useful root-cause evidence in restricted diagnostics; public responses expose safe actionable errors and correlation under [Contracts](../architecture/contracts-and-interfaces.md#errors-concurrency-and-effects). Never log credentials, keys, tokens or unnecessary sensitive payloads. Model/provider telemetry must not retain sensitive prompts/responses by default.

Normal diagnosis works locally through supported process/file output without a required central collector. Keep log files/support exports outside public serving, authorize diagnostic access, and redact exported evidence. Control rotation/retention, volume and storage; a telemetry outage must not stop normal application operation.

Where runtime debug control is needed, make it authorized, scoped to the failing component/operation and temporary, with production-safe defaults and a clear reset/expiry. Do not expose stack traces, SQL or verbose debug pages publicly or leave unbounded query/payload capture enabled.

Use metrics/traces/alerts for demonstrated detection or cross-component diagnosis needs, following the OpenTelemetry default when appropriate. Avoid unbounded metric labels and pointless telemetry; record the failing component and safe operation identity so humans/AI can reproduce a failure without production secrets.

Logs explain operational failure. Durable audit answers who/what/when/target/outcome/authority for accountable actions; [Security](../security/security-and-authorization.md#audit) owns those requirements. Debug logs are not the audit record and sampling/disposable log storage must not silently become audit policy.

## Bounds, scale and cost

Apply relevant pagination, query/payload/upload/memory/concurrency bounds, external-call timeouts, retry budgets and queue/backlog limits. Use representative workload evidence before optimization or service/infrastructure extraction; scale the demonstrated bottleneck.

Total cost includes integration/maintenance and operations, CPU/RAM, storage/I/O/connections, network/egress, telemetry/retention, managed services, backups, upgrades and restore.

Backup scope, consistency and proportional restore evidence are owned by [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#backup-and-recovery). Fast benchmark throughput alone does not establish safe or cheap operation.
