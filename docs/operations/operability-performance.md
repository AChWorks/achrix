# Operations, Performance and Cost

## Configuration and readiness

Simple applications run without mandatory auxiliary services. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns infrastructure defaults; introduce them for actual requirements.

Separate runtime/deployment configuration, secrets, application-level operator settings and domain data. Configuration has an owner, meaningful defaults, validation and explicit environment behavior. The **product/composition boundary owns deployment/runtime configuration sources and precedence**; reusable Modules receive typed validated configuration and should not read environment variables, process-global config or secret stores directly.

When composed, the reusable [Settings Module](../architecture/module-model.md#settings) owns only concrete application-level, operator-editable, durable settings that genuinely belong to the product shell. It is not the source for DSNs, credentials, signing keys or deployment toggles, and it does not absorb another Module's business settings into a universal key/value/JSON table. Settings changes use normal Application authorization/concurrency/audit rules; exact fields wait for a real product under #50.

Shutdown is graceful. Readiness probes only dependencies necessary for local readiness, not every external service synchronously. A Module readiness check must be side-effect-free, bounded and cheap enough for repeated probes; shared infrastructure should not be pinged redundantly once per Module merely because several Modules use it. Deep diagnostics belong to explicit operator/debug actions rather than health-probe fan-out.

## Supported environment

This document owns the runtime/database support matrix; manifests own pins and Git/CI own proof. The first executable baseline supports one Go line and PostgreSQL major; expand only for real consumers and tested compatibility.

| Component | Baseline support and exact validation pin |
| --- | --- |
| Go | 1.27 line; validation 1.27.1, required by both `go.mod` files |
| PostgreSQL | ordinary 18, UTF-8, no required extensions; server/client proof 18.6 |
| Driver | consumer-owned pgx/v5; exact direct/transitive versions in `fixtures/notes/go.mod` and `go.sum` |
| Platform | Linux amd64, local loopback/Unix-socket proving consumer; remote/production profiles remain unproven |
| Validation tools | Bash, Python 3, checksum-pinned [Go quality tools](../development/go-quality.md), native PostgreSQL 18.6 tools, C compiler for Go's race detector; optional source-tool setup requires gcc/make/bison/flex/m4/curl/bzip2 |

Official compatibility was checked before API coupling on 2026-10-01: [Go releases](https://go.dev/doc/devel/release), [Go downloads](https://go.dev/dl/), [pgx v5.11.0 manifest](https://github.com/jackc/pgx/blob/v5.11.0/go.mod)/[changelog](https://github.com/jackc/pgx/blob/v5.11.0/CHANGELOG.md), [PostgreSQL version policy](https://www.postgresql.org/support/versioning/), [pg_dump compatibility](https://www.postgresql.org/docs/18/app-pgdump.html). Driver support is wider than AChrix's tested matrix and does not expand it. Go toolchain/dependency checksums stay enabled; `scripts/validate.sh` rejects a disabled checksum database.

## Validation commands

[Contributing](../../CONTRIBUTING.md#validation-selection) owns test selection, batching and evidence reuse. Commands use the supported matrix above:

| Scope | Command / CI selection |
| --- | --- |
| Complete runtime proof | `scripts/validate.sh` (or `scripts/validate.sh full`) |
| Core only | `scripts/validate.sh core`; no consumer or PostgreSQL setup |
| One Core correction | `go test -race -run '^TestName$' .`; select actual affected tests |
| Consumer domain/config correction | From `fixtures/notes`, use `go test -race ./internal/domain` or `./internal/config`; persisted interactions may still require full proof |
| Router correction | `python3 -m unittest discover -s scripts -p 'test_validation_scope.py'` |
| Known non-runtime documentation | Complete-tree diff/whitespace inspection; CI installs no Go/PostgreSQL and performs no runtime test |

The required `baseline` workflow remains present on every PR/main event. `scripts/validation_scope.py` owns its small exception list and reads a NUL-delimited Git diff with renames disabled so both old and new paths remain visible. Only known documentation, a modification of the existing `achrix_test.go`, or an addition/modification of the known `achrix_bench_test.go` (plus optional docs) can select a narrow path; deleted/type-changed tests, runtime/manifests/migrations/CI/unknown paths, empty diffs and unavailable/invalid base evidence select full. Routing/tool changes run focused router/tool failure regression cases. [Go quality](../development/go-quality.md) owns formatting, pinned Staticcheck/govulncheck, finding triage and benchmark commands. Logs identify the scope; a documentation pass does not claim a new runtime pass. No top-level path filter can leave the required check absent.

Run `scripts/validate.sh` as a non-root user with the supported tools. Full CI uses this same command on a hosted disposable runner with read-only repository permission, no persisted checkout credential, no production secrets and no privileged `pull_request_target` execution. It downloads the pinned Foundation into an isolated consumer/cold module cache, tests real PostgreSQL and verifies a trusted database-only restore. The script owns and removes only its temporary cluster/workspace; it does not touch a host PostgreSQL service. [Consumer setup](../../fixtures/notes/README.md) documents normal invocation and deliberate updates; exact candidate/main proof belongs to [Issue #1](https://github.com/AChWorks/achrix/issues/1) and CI.

CI reuses only the native PostgreSQL installation through an exact Ubuntu 24.04/OS/architecture/setup-script cache key, without prefix fallbacks. The setup digest covers the source checksum, version and build options. Only a successful full validation on a trusted push to main publishes it; PRs restore or build cold and do not publish through this workflow. Cache misses rebuild from the same checksum-pinned source. Restore/prerequisite acquisition is time-bounded and failures remain visible. Database clusters, consumer source, Go dependency caches and validation results are never reused by this cache: every full run still checks exact tool versions and performs the complete cold-consumer/real-database/restore proof. The workflow owns the action SHA and timeout values; GitHub owns cache eviction, so a warm path is an optimization rather than a validation precondition.

If native PostgreSQL tools are absent, `scripts/setup-validation-postgres.sh /absolute/new/owned/directory` builds the checksum-pinned official 18.6 source without ICU/readline/zlib. Set `ACHRIX_PG_BIN` to that directory's `install/bin`. This is test tooling, not a product installer or a system package/service change. The dump proof uses uncompressed custom archives and needs no compression library.

Releases declare maintained lines and upgrade/retirement paths. Older lines need explicit support; there is no permanent LTS promise. Validate dependency/security updates before consumer activation under [Lifecycle](../lifecycle/lifecycle-and-compatibility.md).

## Diagnosis and audit

Core, Modules, adapters and product behavior use a common structured diagnostic path under the application's configuration. Prefer Go `log/slog` and its standard levels rather than another logging framework or a custom severity taxonomy.

Use these meanings consistently:

| Level | Use |
| --- | --- |
| `DEBUG` | bounded diagnostic detail useful during investigation; normally filtered from production defaults |
| `INFO` | expected lifecycle/operational milestones worth retaining, such as a successful activation/readiness transition |
| `WARN` | abnormal/degraded but recoverable condition that deserves operator attention; not routine validation/permission-denied traffic |
| `ERROR` | an intended operation/lifecycle action failed and operator investigation may be required |

Do not add default `TRACE`/`FATAL` levels merely for convention. A library/Module returns failure; product/main owns process termination. When tracing is actually needed, use the trace facility rather than inventing trace-level log spam.

The product owns the `slog.Handler`, output and minimum threshold. The normal production default is Info or stricter; controlled runtime lowering may use a handler/LevelVar-style mechanism when the operating model needs it. Core/Modules do not mutate a process-global log level behind the product's back.

Attach time, component identity and useful request/job/operation correlation; add release/version and trace context where they improve diagnosis. Correlation identities are bounded and trusted: generate a server-side request identity by default, use durable operation/job identity for long work, and accept propagated trace context only through the selected standard/trust boundary. Do not use arbitrary user strings as unbounded field names or cardinality dimensions.

Propagate relevant context across boundaries instead of creating unrelated per-Module logs. Log the failure at the narrowest meaningful owner; another layer emits a second record only when it adds materially distinct transport/ownership evidence. Avoid the common “database error + service error + HTTP error” duplication when all three records say the same thing.

Keep useful root-cause evidence in restricted diagnostics; public responses expose safe actionable errors and correlation under [Contracts](../architecture/contracts-and-interfaces.md#errors-concurrency-and-effects). Never log credentials, keys, tokens or unnecessary sensitive payloads. Model/provider telemetry must not retain sensitive prompts/responses by default.

Normal diagnosis works locally through supported process/file output without a required central collector. Keep log files/support exports outside public serving, authorize diagnostic access, and redact exported evidence. Control rotation/retention, volume and storage; a telemetry outage must not stop normal application operation.

Diagnostic **volume is also a reliability/security concern**. Public invalid/denied requests, authentication failures and other attacker-controlled inputs must not create unbounded warning/audit/trace cost. Choose severity at the narrowest useful owner, aggregate/rate-limit/sample repetitive diagnostics where needed, and preserve a high-signal correlation path for investigation. Security audit events are retained according to their accountability need; ordinary repetitive denials are not automatically durable audit records.

Where runtime debug control is needed, make it authorized, scoped to the failing component/operation and temporary, with production-safe defaults and a clear reset/expiry. Do not expose stack traces, SQL or verbose debug pages publicly or leave unbounded query/payload capture enabled. Profiling follows the same rule: CPU/heap/mutex/block profiles may be captured in an authorized diagnostic environment when needed, but no unauthenticated/public pprof surface is a default product feature.

Use metrics/traces/alerts for demonstrated detection or cross-component diagnosis needs, following the OpenTelemetry default when appropriate. Avoid unbounded metric labels and pointless telemetry; record the failing component and safe operation identity so humans/AI can reproduce a failure without production secrets.

Logs explain operational failure. Durable audit answers who/what/when/target/outcome/authority for accountable actions; [Security](../security/security-and-authorization.md#audit) owns those requirements. Debug logs are not the audit record and sampling/disposable log storage must not silently become audit policy.

The proving consumer emits JSON `slog` to stderr. Its handler propagates a generated request ID into Core authorization, Notes Application, PostgreSQL and HTTP failure records. Database startup records identify `check_environment` or `check_schema`; migration failures identify `migrate`. Reasons distinguish unsupported environments, missing/incompatible/incomplete schema, invalid/changed/unknown migrations, SQLSTATE, deadlines, cancellation and connection failures. These consumer-owned diagnostics preserve inspectable causes internally and log safe categories, never driver exception messages, SQL, tokens, DSNs or note text. Public failures carry stable safe codes and `X-Request-ID`. There is no debug endpoint, collector, durable audit system or product log-retention claim. Operators own stderr access/rotation; the benign fixture creates no sensitive/admin/financial audit requirement.

## Code-level performance engineering

Performance starts with algorithm, data movement and resource ownership before cache/infrastructure selection.

- Know expected cardinality for loops/maps/graphs and avoid accidental unbounded, N+1 or quadratic work on request/data paths that can grow with users/content. A small bounded startup path may legitimately choose simpler code over a more complex asymptotic implementation.
- Treat memory allocation/copying, lock contention, goroutine growth, database round trips/query plans, serialization, storage/network I/O and telemetry volume as costs when they can affect the workload.
- Stream potentially large uploads/downloads/exports and use bounded buffers/backpressure rather than loading whole objects into memory where practical.
- Database performance is part of code quality: use bounded result sets, intentional indexes and transaction scopes; inspect `EXPLAIN (ANALYZE, BUFFERS)` or equivalent on material query regressions rather than guessing.
- Add Go benchmarks and allocation checks for real hot paths/regressions. Use CPU/heap/mutex/block profiles to locate actual cost before optimizing. Compare performance-sensitive changes in a controlled environment (for example with `benchstat`); shared hosted-runner wall-clock noise is not a trustworthy hard regression gate.
- Preserve clarity unless measurement demonstrates a meaningful benefit. Any lower-level optimization that obscures ownership/control flow needs evidence and focused tests.
- First real products establish representative latency/throughput/resource budgets for their workloads. Foundation benchmarks prove only the measured path/environment, never universal capacity.

## Bounds, scale and cost

Apply relevant pagination, query/payload/upload/memory/concurrency bounds, external-call timeouts, retry budgets and queue/backlog limits. Use representative workload evidence before optimization or service/infrastructure extraction; scale the demonstrated bottleneck.

Fixture bounds: 200 Unicode characters per immutable note, 1 KiB HTTP body, 8 KiB headers, 32 active adapter requests, four runtime DB connections, one-second Application/connect deadlines, 200 ms readiness, three-second startup and two-second HTTP/Core shutdown. Startup validates configuration/schema before traffic. No query lists, automatic retries, unbounded payload logging or background job queues exist. Trusted in-process extensions must honor context cancellation; Core does not pretend to forcibly sandbox or terminate arbitrary Go code. No throughput/capacity/RPO/RTO guarantee follows from these bounds.

Total cost includes integration/maintenance and operations, CPU/RAM, storage/I/O/connections, network/egress, telemetry/retention, managed services, backups, upgrades and restore.

Backup scope, consistency and proportional restore evidence are owned by [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#backup-and-recovery). Fast benchmark throughput alone does not establish safe or cheap operation.
