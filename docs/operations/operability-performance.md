# Operations, Performance and Cost

## Configuration and readiness

Simple applications run without mandatory auxiliary services. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns infrastructure defaults; introduce them for actual requirements.

Separate runtime/deployment configuration, secrets, application-level operator settings and domain data. Configuration has an owner, meaningful defaults, validation and explicit environment behavior. The **product/composition boundary owns deployment/runtime configuration sources and precedence**; reusable Modules receive typed validated configuration and should not read environment variables, process-global config or secret stores directly.

When composed, the reusable [Settings Module](../architecture/module-model.md#settings) owns only concrete application-level, operator-editable, durable settings that genuinely belong to the product shell. It is not the source for DSNs, credentials, signing keys or deployment toggles, and it does not absorb another Module's business settings into a universal key/value/JSON table. Settings changes use normal Application authorization/concurrency/audit rules; exact fields require real reusable product needs rather than being predeclared here.

Shutdown is graceful. Readiness probes only dependencies necessary for local readiness, not every external service synchronously. A Module readiness check must be side-effect-free, bounded and cheap enough for repeated probes; shared infrastructure should not be pinged redundantly once per Module merely because several Modules use it. Deep diagnostics belong to explicit operator/debug actions rather than health-probe fan-out.

### Optional external connections

Configured external access must not start dedicated polling, sockets, session workers or timers for each idle connection. Store only the bounded metadata actually needed, acquire request resources on demand, and release them when work ends. Token refresh, discovery refresh or durable background work needs a real workload and explicit lifetime/budget; connection existence alone is not that workload.

The optional [Bridge profiles](../../bridge/README.md) use fixed MCP registration or explicit JSON HTTP routes, without subscriptions or session upkeep. JSON HTTP constructs no MCP server; both profiles share per-request authentication/admission and limits. It bounds admitted requests, bodies, responses and cooperative execution time; products still bound their returned objects and own listener/TLS/header/read/write/drain limits. A deadline does not forcibly terminate arbitrary trusted in-process code. Sum these limits with the real product's pools, replicas and other ingress rather than treating the component limit as a fleet capacity promise.

## Module resource configuration

Identity, Audit and Media receive copied instance-owned `Config` values. `NewPostgres` rejects invalid maxima before creating owned runtime resources; zero selects the existing default and never means unlimited. DSN pool options cannot override the typed effective maximum or fixed lifecycle bounds. Products own the configuration source and precedence; this is not hot reload or durable Settings.

| Config field | Identity | Audit | Media |
| --- | --- | --- | --- |
| `MaxConns int32` | Default 4, explicit minimum 1 | Default 4, explicit minimum 1 | Default 4, explicit minimum 1 |
| `MaxOperations int` | Default 16, explicit minimum 2 | Default 16, explicit minimum 2 | Default 4, explicit minimum 1 |

Negative values and positive values below the stated minimum are invalid. Positive values through each Go type's maximum are representable; those representation bounds are not supported hardware/deployment capacity. There is no required pool-to-lease ratio: a larger pool than admission can be underused without being invalid. Raising maxima changes available capacity, not actual demand, security floors or a throughput/RSS promise.

`MaxOperations` counts simultaneously active owned **leases**, including nested work, rather than public requests or database connections. Identity credential mutations/login can hold an outer work lease plus a nested database lease. Standalone Audit `Append` holds a transaction lease plus its `AppendInTx` lease; caller-owned `AppendInTx` connections add separately to the database budget. Minimum two preserves an isolated supported operation, without promising fairness or successful simultaneous nested mutations. Admission still fails fast with `ErrLimited`; native pool acquisition may wait within the existing operation context. Cancellation releases the owned waiting lease; a canceled acquisition may briefly leave native connection construction running within the one-second connect bound, and pool closure still drains native resources.

Native pgxpool keeps `MinConns=0` and `MinIdleConns=0`: construction opens no connections and startup acquires only what its checks need. Demand creates connections up to the maximum, later calls reuse idle connections, and native health checks reclaim them. Idle time and health period remain one minute, lifetime one hour, jitter zero, connect/ping one second. Health scheduling is not an exact idle-expiry SLA. Module operation deadlines, cancellation/drain/partial-start cleanup and safe error/diagnostic categories remain unchanged. Explicit migration pools and Audit database-profile checks retain their existing connection semantics; pool/admission limits are not database identity.

Budget each independently owned pool/Module, then sum the actual composition and replicas. Product-owned pools, caller transactions, migrations/observers/admin connections and PostgreSQL reserve add to the owned runtime maxima. The [default eight-pool fixture measurement](https://github.com/AChWorks/achrix/issues/76#issuecomment-5976870760) composed control Identity+Audit and two separate Identity+Audit+Media sites: defaults imply at most 32 owned connections and 104 owned leases per replica, including nesting. Actual observed constructor/start/burst/reclamation/shutdown counts were 0/8/32/0/0. This Foundation fixture is not the real product's aggregate/fairness or site-isolation proof; Rixa owns that evidence. The optional [raised-profile observation](../../fixtures/notes/README.md#resource-capacity-observation) uses ordinary public consumption to distinguish maxima from demand.

Identity hash concurrency/security/session/HTTP bounds and Media upload/image/SVG/parser/private-storage/durable-outcome invariants remain independent. Media expensive validation stays two even when general admission is raised. Products choose and measure their aggregate workload/deployment budget; no adaptive controller, shared fairness scheduler, security knob or Core tenancy is introduced.

## Public-image resource profile

[Media](../../media/README.md#clean-public-image-preparation) owns the finite interpretation/API profile. Source remains 10MiB, each dimension 4096 and total pixels 8388608. Clean output is capped 40MiB and streamed without a 40MiB buffer. Metadata is capped 256KiB: PNG counts encoded ancillary chunk bytes plus 12-byte framing; JPEG counts complete APP/COM segments. PNG has at most 4096 total chunks; JPEG at most 4096 SOI/EOI plus length-bearing segments and 64 SOS. Restarts/stuffing/fill are bounded by source bytes/geometry. Exif adds its directory/entry/depth bounds. These fixed bounds are independent of operator pool/general-lease maxima; two nonqueued shared expensive slots remain held from before snapshot allocation through private output.

Memory follows admitted shapes, not a 128MiB promise. Native progressive JPEG coefficient arrays require `256*ceil(W/(8Hmax))*ceil(H/(8Vmax))*sum(HiVi)` bytes: 4096x2048/444 alone requires 96MiB. Add source snapshot, decoded YCbCr/gray/RGB planes, any oriented pixel buffer and encoder/runtime/database overhead. RGB decoding also converts its temporary YCbCr representation; Adam7 decoding allocates individual pass images in addition to the combined image. Typed row/pixel access avoids per-pixel interface allocations; rotated opaque JPEG uses the native encoder's RGBA fast path. Aggregate active Modules/replicas and product-held outputs add separately. No hard heap/RSS, fleet, throughput, fairness or perceptual guarantee follows.

Cold observations are opt-in and not repeated by ordinary baseline CI. Set `ACHRIX_MEDIA_TEST_DSN` to a disposable supported PostgreSQL 18.6 database named `achrix_media_*` and run one fresh process per scenario: `(cd media && GOWORK=off ACHRIX77_RESOURCE_SCENARIO=adam7 go test -count=1 -v -run '^TestPublicImageResourceObservation$' -timeout=40s .)`; other scenarios are `progressive444`, `progressive-rgb`, `progressive-gray` and `two-concurrent`. The repository-owned fixtures generate complete synthetic codestreams at the maximum admitted pixel count and seed exact ready source metadata/private bytes without a preceding image decode. Every actual Service operation retains its 15-second cooperative deadline; observing two simultaneous private writers also verifies immediate third-operation rejection. Sampling reports actual Go allocation/heap and Linux process RSS, including runtime/setup overhead, and is observational rather than a peak guarantee. Ordinary tests verify smaller framing/color/alpha/lifecycle cases and full CI verifies normal dependency consumption and retained restore.


On 2026-10-04, Go 1.27.1/Linux amd64 on the dedicated development host observed the following fresh-process Service calls against real PostgreSQL 18.6. Each synthetic source is 4096x2048 with Orientation6. JPEGs have constant zero coefficients; source payloads are 33–247KB. These shapes exercise large native allocation planes, not arbitrary entropy, 10MiB snapshots, every dimension rounding or the complete product workload. Five-millisecond samples may miss shorter peaks; allocation totals include sampling/runtime activity.

| Shape | Concurrent calls | Elapsed ms | Sampled peak Go heap MiB | Allocated MiB | Sampled peak process RSS MiB |
| --- | --- | --- | --- | --- | --- |
| adam7 | 1 | 375 | 82.0 | 97.7 | 110.7 |
| progressive444 | 1 | 457 | 152.7 | 152.3 | 168.9 |
| progressive-rgb | 1 | 487 | 184.8 | 184.3 | 200.7 |
| progressive-gray | 1 | 173 | 48.6 | 48.1 | 64.3 |
| two-concurrent | 2 | 716 | 368.8 | 368.4 | 384.9 |

After explicit GC, observed live Go heap returned to approximately 0.46–0.53MiB; RSS reclamation was not asserted. The ordinary focused race proof also exercises upload/preparation shared saturation and cooperative stop, independently of these opt-in observations. Source-derived rounding remains authoritative for budgeting other shapes; the table is not a worst-case ceiling.

## Supported environment

This document owns the runtime/database support matrix; manifests own pins and Git/CI own proof. The first executable baseline supports one Go line and PostgreSQL major; expand only for real consumers and tested compatibility.

| Component | Baseline support and exact validation pin |
| --- | --- |
| Go | 1.27 line; validation and minimum 1.27.3, required by the inventoried component and consumer `go.mod` files |
| PostgreSQL | ordinary 18, UTF-8, no required extensions; server/client proof 18.6 |
| Driver / password library | pgx/v5 for owned Identity/Audit and Notes persistence; maintained alexedwards/argon2id over x/crypto/argon2; exact direct/transitive pins in owning component and `fixtures/notes` manifests |
| Platform | Linux amd64, local loopback/Unix-socket proving consumer; remote/production profiles remain unproven |
| Validation tools | Bash, Python 3, checksum-pinned [Go quality tools](../development/go-quality.md), native PostgreSQL 18.6 tools, C compiler for Go's race detector; optional source-tool setup requires gcc/make/bison/flex/m4/curl/bzip2 |

Official compatibility was checked before API coupling on 2026-10-01: [Go releases](https://go.dev/doc/devel/release), [Go downloads](https://go.dev/dl/), [pgx v5.11.0 manifest](https://github.com/jackc/pgx/blob/v5.11.0/go.mod)/[changelog](https://github.com/jackc/pgx/blob/v5.11.0/CHANGELOG.md), [PostgreSQL version policy](https://www.postgresql.org/support/versioning/), [pg_dump compatibility](https://www.postgresql.org/docs/18/app-pgdump.html). Driver support is wider than AChrix's tested matrix and does not expand it. The minimum toolchain includes the [Go 1.27.2 and 1.27.3 security fixes](https://go.dev/doc/devel/release#go1.27.3); retained historical measurements identify the toolchain actually used and are not a current security-support pin. Go toolchain/dependency checksums stay enabled; `scripts/validate.sh` rejects a disabled checksum database.

The 2026-10-02 Identity dependency review checked argon2id v1.0.0 (MIT, Go 1.12), pgx v5.11.0 (MIT, Go 1.25) and x/crypto v0.57.0 (BSD-3-Clause, Go 1.26) against the supported Go pin, official source and checksum identities. Full validation analyzes every inventoried component and the cold normal-module consumer. [Identity](../../identity/README.md) owns the measured hash policy and its explicit resource/web limits; [Audit](../../audit/README.md) owns retained data and query limits. The loopback/Unix test database is intentionally local; remote DSNs defensively require certificate-verified TLS on every host/fallback without establishing a remote production profile.

## Validation commands

[Contributing](../../CONTRIBUTING.md#validation-selection) owns test selection, batching and evidence reuse. Commands use the supported matrix above:

| Scope | Command / CI selection |
| --- | --- |
| Complete runtime proof | `scripts/validate.sh` (or `scripts/validate.sh full`) |
| Core only | `scripts/validate.sh core`; no consumer or PostgreSQL setup |
| One Core correction | From the root, `GOWORK=off go test -race -run '^TestName$' .`; select actual affected tests |
| One component correction | From its owning directory, `GOWORK=off go test -race ./...`; supply only the dedicated test DSNs required by persisted tests |
| Consumer domain/config correction | From `fixtures/notes`, use `go test -race ./internal/domain` or `./internal/config`; persisted interactions may still require full proof |
| Relevant browser behavior | `scripts/validate-admin-browser.sh`; automatically selected inside `baseline` as described below |
| Router correction | `python3 -m unittest discover -s scripts -p 'test_validation_scope.py'` |
| Known non-runtime documentation | Complete-tree diff/whitespace inspection; CI installs no Go/PostgreSQL and performs no runtime test |

The required `baseline` workflow remains present on pull-request and canonical-integration-branch events. `scripts/validation_scope.py` owns its small exception list and reads a NUL-delimited Git diff with renames disabled so both old and new paths remain visible. Only known documentation, a modification of the existing `achrix_test.go`, or an addition/modification of the known `achrix_bench_test.go` (plus optional docs) can select a narrow path; deleted tests, type-changed inputs, runtime/manifests/migrations/CI/unknown paths, empty diffs and unavailable/invalid base evidence select full. Routing/tool changes run focused router/tool failure regression cases. [Go quality](../development/go-quality.md) owns formatting, pinned Staticcheck/govulncheck, finding triage and benchmark commands. Logs identify the scope; a documentation pass does not claim a new runtime pass. No top-level path filter can leave the required check absent.

### Conditional HTTPS browser proof

The same complete-tree selector emits an independent `browser` decision. Admin templates/JavaScript/styles/catalogs/localization, shared Core/Audit/Identity/Media behavior, adapters, Notes browser/shared fixture inputs, dependency manifests and browser/validation tooling select the existing HTTPS proof. Known non-runtime documentation and the existing Core test/benchmark exceptions skip it. A small explicit list of existing independent Installation/Multi-Site and Notes domain sources also skips browser while retaining full backend validation. Unknown paths, new files outside a proven exception, type changes, mixed relevant changes and unavailable/empty diff evidence select browser conservatively. Added symlinks cannot masquerade as documentation. If an exempt file gains a browser role, update routing with that change.

The required `baseline` job runs the backend selection once, then the selected browser proof using the same native PostgreSQL tools. Test-only Node 24.21.0, locked Playwright 1.63.0 and its asserted Chromium 153.0.8010.12 profile remain outside product runtime dependencies. CI installs npm packages with lock integrity and lifecycle scripts disabled, and invokes only the installed Playwright CLI. Browser acquisition/execution has explicit step limits within the existing job limit. No browser/package/dependency-result cache is introduced; the fresh Go component cache and `verify_consumer.py` source/checksum equality remain required for each browser run. The existing HTTPS, permissions, no-replay, keyboard, installed-locale, English/Persian and payload assertions stay together in one run.

CI records the selected scope/result, checked-out commit/tree, PR source head and browser duration. A PR's effective merge checkout and its source head are different identities and are reported separately. A skipped browser step is not a browser pass. GitHub step timings expose acquisition cost separately; the active change's PR records the measured added cost.

Failure capture retains bounded private causes before disposable database/workspace removal and prints the safe stage, exit, source line, log sizes/hashes, truncation and severity counts into the CI log. Browser fixture assertions additionally emit a separate source-owned safe channel with test/file/line, specific reason and applicable typed SQLSTATE/errno/context facts. At most 32 records and 64 KiB are read, with malformed/truncated input counted; arbitrary raw log text cannot inject a trusted record. Those useful distinctions survive hosted-runner teardown in the support summary. Arbitrary fixture/database log bodies are not automatically exported. On hosted disposable runners those private files expire with the runner; direct command/assertion output and the useful safe failure projection remain in CI logs under repository retention. For protected raw-cause inspection, reproduce the exact recorded candidate with the [explicit persistent-host browser command](../../fixtures/notes/README.md#admin-browser-proving-composition), then review/redact and remove evidence under the diagnostic policy. This CI route promises no external private-log store.

Run `scripts/validate.sh` as a non-root user with the supported tools. Full CI uses this same command on a hosted disposable runner with read-only repository permission, no persisted checkout credential, no production secrets and no privileged `pull_request_target` execution. It enumerates [modules.json](../../modules.json), checks each module with `GOWORK=off`, downloads the pinned Core/components into an isolated consumer/cold module cache, verifies each component's source/version identity, tests real PostgreSQL and verifies trusted quiesced restore profiles: the original Notes database-only proof, coupled Identity/Audit state, and the Media composition’s participating database plus exact private asset tree. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#initial-fixture-lifecycle-and-database-scope) and [Media](../../media/README.md#capture-and-restore-boundary) own their scope and exclusions; this establishes no production, off-host, recovery-time or unsupported partial-restore guarantee. The script owns and removes only its temporary cluster/workspace; it does not touch a host PostgreSQL service. [Consumer setup](../../fixtures/notes/README.md) documents normal invocation and deliberate updates; exact candidate/target proof belongs to Git/CI and the active PR/work item.

CI may reuse only the native PostgreSQL tool installation through an exact runner/OS/architecture/setup-script cache identity without prefix fallbacks. The setup digest covers source checksum, version and build options. Only a successful full validation on the canonical integration branch may publish that cache; PRs restore or build cold and do not publish through this workflow. Cache misses rebuild from the same checksum-pinned source. Restore/prerequisite acquisition is time-bounded and failures remain visible. Database clusters, consumer source, Go dependency caches and validation results are never reused by this cache: every full run still checks exact tool versions and performs the complete cold-consumer/real-database/restore proof. The workflow owns exact runner label, branch/ref, action SHAs, cache key and timeout values; GitHub owns cache eviction, so a warm path is an optimization rather than a validation precondition.

If native PostgreSQL tools are absent, `scripts/setup-validation-postgres.sh /absolute/new/owned/directory` builds the checksum-pinned official 18.6 source without ICU/readline/zlib. Set `ACHRIX_PG_BIN` to that directory's `install/bin`. This is test tooling, not a product installer or a system package/service change. The dump proof uses uncompressed custom archives and needs no compression library.

Releases declare maintained lines and upgrade/retirement paths. Older lines need explicit support; there is no permanent LTS promise. Validate dependency/security updates before consumer activation under [Lifecycle](../lifecycle/lifecycle-and-compatibility.md).

## Diagnosis and audit

[Engineering Principles](../principles/engineering-principles.md#actionable-failures-and-retained-diagnostic-evidence) owns the requirement. Components capture meaningful causes at the point where they are still available, before rebuilding public-safe errors. Products compose a normal local diagnostic path using Go `log/slog`; no collector, process-global logger change or new logging framework is required.

### Two deliberate diagnostic boundaries

Ordinary structured records contain timestamp, component, operation, failing phase, safe reason and relevant typed facts: validated SQLSTATE, errno/network category, credential-format category, missing/incompatible ledger or guard facts, and outcome certainty where meaningful. Retain enough context to choose a useful next step. Do not stringify arbitrary provider/driver/template exceptions into ordinary logs, and do not classify all schema/operation/technical information as secret.

Core and the stateful/Admin components accept an optional `Diagnostic func(context.Context, string, string, error)` in their configuration. The strings identify operation and phase; the last argument is the original cause at an explicitly **protected product boundary**. It is separate from returned public errors and their unwrap chains. Each original operational failure reaches the callback before ordinary-log suppression; the receiving product owns any sampling or retention of that protected evidence. Callbacks run outside diagnostic admission and component state locks. The synchronous callback must honor the operation's bound, be concurrency-safe and avoid unbounded queues or blocking a normal operation on an unavailable collector. Nil retains ordinary structured diagnostics; products needing arbitrary provider-specific explanations must compose the protected hook.

The product owns provider-aware redaction: inspect typed errors, retain useful cause/phase/technical fields, and exclude credentials, tokens, keys, password/PHC material and private payload values before retention or export. A generic regex cannot safely recognize every provider's secrets. Do not pass the raw callback error directly to `slog.Any`, public JSON or a support bundle. In-process hooks are trusted code, not an access-control sandbox; granting remote access to diagnostics still requires authentication and authorization.

Bridge requires an explicit protected diagnostic callback at composition. Its terminal records include trusted request/user/client/channel/operation identity, actual dispatch, known outcome and an authorized reference when supplied; failures additionally retain the original cause and phase. Route safe fields into the product's ordinary logger and the cause into its protected diagnostic policy. The callback is synchronous, concurrency-safe and bounded by the request; it must not create an unbounded queue or make an unavailable collector a runtime dependency. Success attribution is not a substitute for an owning service's required durable/atomic Audit record. SDK output/delivery failures remain separate from a confirmed application result.

Standalone Identity, Audit and Media migrations retain `Migrate(ctx, dsn)` and add `MigrateWithDiagnostics(ctx, dsn, logger, diagnostic)` for explicit operator composition. Migration/startup phases identify configuration, environment, ledger/guard validation, lock, DDL and commit failures without changing immutable migration history. Default structured stderr diagnosis remains usable locally. See each component README for its exact fields and error contract.

### Severity, correlation and outcome

| Level | Meaning |
| --- | --- |
| DEBUG | Investigation detail and routine policy denial/readiness detail that normally stays below production output. |
| INFO | Useful expected milestones and bounded transport rejection context. |
| WARN | Abnormal degraded but recoverable conditions needing operator attention. |
| ERROR | Failed intended lifecycle/application/dependency operation requiring investigation, including failed policy evaluation. |

The product owns the handler, threshold and volume policy. Libraries return failures; `main` owns process exit. Do not invent `FATAL`/`TRACE` levels or mutate global logging thresholds. Temporary debug control, when needed, is authorized, scoped and expires/resettable.

Propagate the existing trusted context across Application, provider and transport boundaries. Generate a server-side request identity at the product edge; do not trust arbitrary `X-Request-ID` values or replace a trusted ID at every Module. Use durable operation/reconciliation identity for long-lived mutations. Diagnostic dimensions never use unbounded principal/IP/request/resource values as key names or metric labels.

A dependency deadline/cancellation with a live caller is an operational failure. A caller-aborted read is a different event and need not produce a dependency alarm. A write whose acknowledgment is uncertain still needs evidence even when the request context expires. Use `not_executed`, `not_committed`/`rolled_back`, `committed` and `unknown` only when the owning phase proves that fact; a transaction-local append is not a committed outer transaction. Keep successful mutation results and resource identities when permission presentation or cleanup later fails. Do not turn generic unavailable responses into retry instructions.

### Failure ownership and next action

| Owner / boundary | Caller result and protected evidence | Safe next action |
| --- | --- | --- |
| Core composition | `ErrComposition` with bounded module/index/capability and missing versus incompatible ABI details. | Fix the named descriptor/provider/cycle before startup. |
| Core policy | Explicit denial remains `ErrDenied`; failed evaluation remains `ErrAuthorizationUnavailable`. ERROR phase/cause evidence and the protected hook retain the original provider failure. | Correlate the request and repair the policy dependency; do not interpret unavailable as a permission grant or safe write replay. |
| Installation | `ErrInput` plus `InputError.Field/Problem`; stale binding/plan explains what needs reassessment. The planner is pure and emits no operational logs. | Correct the named declared input or reconcile durable attempts against a fresh plan. |
| Multi-Site | Safe syntax details before lookup; denied/unknown site privacy unchanged. Trusted selection planning retains the Core graph cause and identifies revision/generation preconditions. | Refresh an authorized selection or correct supplied composition; no automatic persistence/activation. |
| Identity/Audit startup and migration | Canonical public result plus distinct environment/ledger/guard/lock/DDL/commit evidence and original protected cause. | Verify the named environment, grants, migration identity and phase; reconcile uncertain commits before rerun. |
| Identity credentials/session | Wrong credentials retain non-enumerating authentication semantics; malformed stored credentials and dependency failures have distinct protected diagnostic categories without password/hash data. | Correct the credential-storage/dependency fault through authorized procedures; do not retry or disclose account existence based on a generic 401. |
| Audit append / transaction | Audit owns its actual provider cause; the outer transaction owns commit/rollback certainty. | Repair the named failure and reconcile the owning operation; never treat an append return as outer commit evidence. |
| Media storage/output | Preserve errno, short-write/integrity/format and failing preparation/destination phase before public canonicalization. Durable Media unknown outcomes retain their existing reconciliation identity. | Correct permissions/space/I/O or the supported input profile. Discard partial prepared output; reconcile uncertain mutations rather than replaying them. |
| Admin and module-owned adapters | Distinct transport/authentication/permission/locale/render/input codes and protected phases. `CheckAllowed` preserves evaluation failure; compatibility `Allowed` diagnoses it. | Correct input, sign in, or follow request correlation as the specific code requires. A confirmed action plus unavailable permission hints remains confirmed. |
| Browser | Machine code drives localized instructions; wrong credentials differ from expired session. Proven pre-dispatch timeout differs from a sent write with unknown acknowledgment. | Follow the stated correction or authorized reconciliation flow; no automatic write retries. |
| Notes CLI | Named configuration field, mode, bind/output/start/shutdown phase and meaningful nonzero exit. | Correct the indicated setting/tool/dependency and inspect correlated component evidence. |
| Notes write / HTTP | Preserve pre-send, rejected-statement and unknown outcomes across repository, Service and HTTP. An uncertain create retains its generated ID; failed response delivery after success remains committed. | Reconcile the returned ID and request with the operator before retrying. Temporary absence alone does not prove a still-unresolved write never committed. |
| Cleanup | Unexpected rollback/unlock/close failure is additional evidence; harmless already-closed cleanup stays quiet. | Preserve the primary result, then diagnose cleanup. Do not relabel confirmed success as unknown or retry a write to repair cleanup. |

### Bounded volume without silent evidence loss

Components and products bound repetition using fixed-capacity, source-owned operation/phase/reason/outcome classifications. Distinct operational faults get first-occurrence evidence; repeated identical faults aggregate. Capacity overflow must be explicit rather than pretending every cause was retained. Request IDs, file names, resource values and arbitrary provider text must not grow an unbounded map. Unknown mutation outcomes have independent visibility from readiness/denial noise.

A retained suppression count must have a bounded timed flush or explicit lifecycle/command flush; future traffic is not a precondition for understanding what happened. Component summary records carry `diagnostic_summary=true`, so the proving consumer does not suppress the only final component evidence again. A summary spanning requests does not borrow a single request ID. Protected sinks need their own bounded policy and appropriate retention.

Notes uses Info-level JSON stderr, up to 32 distinct signatures in each standard-severity window and a separate unknown-outcome window, with one-second summaries and `diagnosis.Flush` on orderly exit. Signatures contain bounded classification fields, not messages or request IDs. This is a small proving-product policy, not a universal per-second budget required of every product. Libraries never install a global limiter or change a product's threshold.

### Access, retention and validation failure evidence

Keep operational files and support exports outside public serving. Authorize access, use appropriate filesystem permissions/encryption for the deployment, rotate/bound storage and choose a useful retention period. Restricted diagnosis and public error privacy are separate obligations. A collector outage must not turn diagnostic transport into an application outage. Metrics/tracing and OpenTelemetry are optional responses to actual detection/cross-component needs.

The validation scripts identify the failing stage, shell line and exit status without secret-bearing shell tracing. Before removing their private cluster/workspace, failed runs retain at most 64 KiB from each relevant PostgreSQL/fixture log in a mode-0700 evidence directory with mode-0600 files. The printed locator and `support.json` contain stage/exit/severity/count/hash metadata; original exception, statement or fixture content stays in explicitly private files. Review those private files and apply targeted redaction before any export; do not attach them wholesale to public GitHub. Remove retained evidence after diagnosis under the operator's retention policy. Successful runs remove their temporary resources. Every PostgreSQL startup attempt is reconciled, including a failed startup wait: cleanup requests a bounded stop and requires stopped status plus absence of the owned PID file before removal. Unverified cluster shutdown retains its task directory for reconciliation instead of deleting live database files. Cleanup failures add their own status/evidence without replacing an already nonzero primary exit; cleanup failure after otherwise successful validation makes the run fail.

Tests assert both sides of the boundary: useful protected cause/phase/correlation/outcome evidence, and absence of secret markers from public results, public unwrap objects, ordinary logs and safe support summaries. Storage/timeout/commit distinctions need real PostgreSQL or discriminating fault evidence; a generic-error snapshot alone is insufficient.

Logs explain operational failure. Durable Audit answers who/what/when/target/outcome/authority under [Security](../security/security-and-authorization.md#audit). Repetitive validation/denial traffic is not automatically durable Audit, and log sampling/retention never silently changes the required audit record.

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

Apply relevant pagination, query/payload/upload/memory/concurrency bounds, external-call timeouts, retry budgets and queue/backlog limits. **Per-Module bounds are not a product-level budget:** the composed product must also account for the aggregate connections/pools, goroutines/workers, memory, file descriptors/storage handles, network/provider concurrency and telemetry/resource cost of all active Modules and adapters. Measure the real composition before changing ownership or infrastructure. If aggregate pressure becomes material, compare smaller per-Module limits, an explicit shared infrastructure collaborator, external pooling or another bounded mechanism; do not introduce a process-global pool/service locator merely because several Modules use the same technology.

Use representative workload evidence before optimization or service/infrastructure extraction; scale the demonstrated bottleneck.

Fixture bounds: 200 Unicode characters per immutable note, 1 KiB HTTP body, 8 KiB headers, 32 active adapter requests, four runtime DB connections, one-second Application/connect deadlines, 200 ms readiness, three-second startup and two-second HTTP/Core shutdown. Startup validates configuration/schema before traffic. No query lists, automatic retries, unbounded payload logging or background job queues exist. Trusted in-process extensions must honor context cancellation; Core does not pretend to forcibly sandbox or terminate arbitrary Go code. No throughput/capacity/RPO/RTO guarantee follows from these bounds.

Total cost includes integration/maintenance and operations, CPU/RAM, storage/I/O/connections, network/egress, telemetry/retention, managed services, backups, upgrades and restore.

Backup scope, consistency and proportional restore evidence are owned by [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#backup-and-recovery). Fast benchmark throughput alone does not establish safe or cheap operation.
