# Notes proving consumer

This separately composed Go module proves consumption of AChrix's public boundary. It is a benign local fixture, not a production content product, CMS, shared Content Module or real product repository. It owns immutable notes, its schema/migration, authentication policy and HTTP adapter. AChrix supplies shared composition/authorization/lifecycle contracts and explicitly selected reusable components.

Its `go.mod` pins source-backed Core and independently versioned component dependencies plus pgx; `go.sum` retains normal checksums. [Consumption](../../docs/architecture/consumption-and-packaging.md) owns compatible selection and the coherent transition from monolithic releases; [modules.json](../../modules.json) owns the component inventory. There is no `replace`, workspace override, source copy or Foundation internal import. `internal/domain` owns text/ID invariants; `internal/application` authorizes typed create/read operations; `internal/infrastructure` owns SQL/resources; `internal/presentation` maps HTTP to the same Service.

## Setup and validation

Use the exact supported tools from [Operations](../../docs/operations/operability-performance.md#supported-environment). From the repository root:

```bash
scripts/validate.sh
```

The command provisions only a task-owned local Unix-socket PostgreSQL cluster, runs each inventoried component and the isolated consumer tests with `-race`, checks normal pinned dependency download/build/source identities, and restores its trusted logical dump into a separate empty database. It checks data, immutable schema identity, composition/readiness and an authorized read; then removes only its owned test workspace/cluster. It requires a non-root user and never uses a host database service. Missing PostgreSQL is a failure, not a skipped persistence check. There are no mandatory containers or auxiliary services.

Root `go test ./...` covers Core only. Focused checks run inside their owning Go module with ordinary dependencies and `GOWORK=off`:

| Scope | Working directory | Command |
| --- | --- | --- |
| Core | repository root | `GOWORK=off go test -race ./...` |
| Installation planner | `installation` | `GOWORK=off go test -race ./...` |
| Multi-Site module | `multisite` | `GOWORK=off go test -race ./...` |
| Pure consumer domain/configuration | `fixtures/notes` | `GOWORK=off go test -race ./internal/domain ./internal/config` |

Persisted consumer tests require their dedicated empty disposable databases and explicit test DSNs; use `scripts/validate.sh full` to supply those safely. Module-local planner/resolver tests do not replace the real PostgreSQL consumer or retained-state checks. These commands describe validation; exact passing candidate evidence belongs to Git/CI and the active work item.

If PostgreSQL tools are absent and the listed build prerequisites are installed:

```bash
scripts/setup-validation-postgres.sh /absolute/new/owned/pg-validation
ACHRIX_PG_BIN=/absolute/new/owned/pg-validation/install/bin scripts/validate.sh
```

The installer builds official checksum-pinned PostgreSQL source in that new directory only. It does not install system packages or configure host services. See [Lifecycle](../../docs/lifecycle/lifecycle-and-compatibility.md#initial-fixture-lifecycle-and-database-scope) for the tested database-only scope and exclusions.

For interactive local operation, use a separately owned empty database in the current [supported PostgreSQL profile](../../docs/operations/operability-performance.md#supported-environment) and set `NOTES_DATABASE_URL` through your local runtime configuration/secret source. Only a local socket/loopback DB is accepted. Do not use production data or put a real DSN in Git. Set distinct random `NOTES_WRITER_TOKEN` and `NOTES_READER_TOKEN` (32–128 characters); for a disposable local demonstration, generate each independently with `openssl rand -hex 32`. Do not reuse credentials from another service. `NOTES_LISTEN` defaults to `127.0.0.1:8080` and accepts loopback addresses only.

Then, from this consumer directory, with those environment values supplied:

```bash
go mod download
go run ./cmd/notes -mode migrate
go run ./cmd/notes -mode serve
```

Migration is explicit before startup, never performed by HTTP requests. It can be repeated safely. Failed/interrupted installation rolls back schema/ledger; checksum/unknown-identity failures require reconciliation, not forced ledger edits. `SIGINT`/`SIGTERM` closes HTTP ingress, drains bounded requests and shuts down the owned pool. Startup is one-shot; reconstruct composition to restart.

## HTTP contract

| Endpoint | Authentication / behavior |
| --- | --- |
| `GET /live` | Cheap process liveness; no DB call |
| `GET /ready` | One bounded check of the local required DB and active composition |
| `POST /notes` | Bearer writer token; JSON `{"text":"..."}`; 201 with immutable note |
| `GET /notes/{id}` | Writer or reader token; 200 with the same persisted note |

The fixture maps the two runtime tokens to `writer`/`reader`; direct callers pass an explicitly authenticated `achrix.Principal` to that same Service. `writer` may create/read; `reader` may only read; empty/unknown principals fail closed. HTTP authentication does not grant a separate path around Application authorization. In-process callers/extensions are trusted code; they may not claim another identity on behalf of an untrusted request.

Bodies are capped at 1 KiB; text is valid nonblank Unicode with at most 200 characters and no NUL. Unknown fields/trailing JSON are rejected. Public codes include `unauthenticated` (401), `permission_denied` (403), `invalid_input` (400, with safe field/correction detail), `body_timeout` (408), `not_found` (404), and `authorization_unavailable`/`unavailable`/`busy`/`unknown_outcome` (503). `X-Request-ID` correlates safe structured stderr records across the actual boundaries. There is no automatic payload/DSN/token/SQL capture, debug endpoint or central telemetry requirement. Notes have no update/delete/list endpoint or external effect.

An explicit `outcome` distinguishes proven `not_executed`, a PostgreSQL-rejected `not_committed` statement, and `unknown`. An uncertain create returns the generated `id` so the authorized caller can reconcile that note and request with an operator before deciding whether to retry. Temporary absence does not settle an unresolved write; HTTP 503 or a timeout alone proves neither rollback nor non-execution. Create performs no automatic replay. A response-write failure after successful persistence stays `committed` in operator evidence.

Read cheaply preflights the opaque reference's 26–64 byte ASCII/base32 syntax before policy or storage; malformed references reveal no existence. The fixture's create capability deliberately permits the empty global scope. Service operations supply deadlines, explicitly denied policies map to 403 and safe Core evaluation-unavailable maps to 503. Its descriptor requires Core authorization ABI 2 under the [v0.1 to v0.2 migration](../../docs/lifecycle/lifecycle-and-compatibility.md#v01-to-v02-development-minor-migration); the schema/migration and Notes capability ABI remain unchanged.

For a local request, with the writer token already supplied as a runtime variable:

```bash
curl --fail-with-body -H "Authorization: Bearer $NOTES_WRITER_TOKEN" \
  -H 'Content-Type: application/json' --data '{"text":"Hello from the consumer"}' \
  http://127.0.0.1:8080/notes
```

Retain the returned opaque ID to read it. Authentication tokens are not used as principal IDs, logged, stored in domain records or included in discovery metadata. This loopback-only fixture does not establish a remote/TLS/authentication deployment profile.

## Identity and deliberate updates

```bash
GOWORK=off go run ./cmd/notes -mode identity
GOWORK=off go run ./cmd/componentidentity
# Set the reviewed owning module path and compatible version before updating.
go get "$ACHRIX_MODULE_PATH@$ACHRIX_MODULE_VERSION"
go mod tidy
go mod verify
go list -m all
```

Before the update command, set `ACHRIX_MODULE_PATH` to the owning Core/component module path and `ACHRIX_MODULE_VERSION` to its exact reviewed compatible commit/version. Inspect all resulting transitive changes; independently available latest versions are not a compatibility guarantee. The Notes identity command reports Core, fixture Module version, consumer build revision and migration checksum. Isolated validation injects the exact consumer Git revision when building outside its Git worktree. `cmd/componentidentity` reports each imported component's own `Version()` and composed runtime descriptors; full validation compares them with their separate normally resolved manifest pins. It uses a real executable because Go test binaries may omit dependency build metadata. Local/replaced builds have explicit development labels; deprecated source-line constants are not build identity. No v1 stability, automatic updater or binary release is claimed.

Review the update/manifest diff, run `scripts/validate.sh` from the root, then rebuild your consumer. Deployment remains a separate owner action; changing Foundation source does not update an existing executable. Never edit a published migration in place or equate reverting source with reverting data. Generic product update/recovery remains separate real-product work rather than a capability implied by this fixture.

## Private Media proving composition

The test-only `media_test.go` composes public Core, Identity, Audit and Media contracts from normally resolved Core/component dependencies. The product fixture owns explicit create/list/exact-object-read/delete/reconcile grants, trusted same-origin HTTPS ingress and synthetic accounts. Uploading or authenticating grants no object rights; collection discovery and byte access remain distinct. The fixture explicitly selects PNG/JPEG, PDF, ZIP and private SVG, retaining all five originals. SVG is a separate explicit selection and does not expand `CommonMIMEs()`. Persian filenames including ZWNJ, bounded keyset listing, exact private reads/status, conditional delete and safe rejected input exercise the real private file library. This fixture does not define content relationships, public serving, roles or a production storage profile.

Full validation uses separate private Media test/consumer/restore databases and mode-0700 storage roots. It stops ingress and all participating Modules before native PostgreSQL capture and a manifested private asset archive, verifies paths/types/checksums before extraction, compares the whole participating metadata/ledger datasets and reads all five retained files through the restored public services. A missing or corrupt asset must fail before destination bytes are written. The tested coherent quiesced local capture has no live snapshot, off-host delivery or RPO/RTO guarantee. See [Media](../../media/README.md) for lifecycle, resource and recovery obligations; generic product lifecycle/recovery remains separately gated by real deployment evidence.

## Optional Multi-Site resolver composition

The test-only [multisite_test.go](multisite_test.go) consumes the normally downloaded public `multisite` package. It composes two stable site IDs on the same hostname with absent versus explicit `:443` ports, authorizes each exact authority, rejects an ungranted principal and proves that unknown `:8443` never falls back. An actual product-local Core participant declares the resolver optional; when omitted, the fixture selects its explicit single-site identity without a resolver provider.

The compatible Core/component set pinned by this fixture's `go.mod`/`go.sum` supplies private SVG, the optional resolver, operator resource configuration, clean public-image preparation and safe dependency-error boundaries through normal module resolution. Full validation compares participating runtime/embedded-asset source with each normally downloaded owning dependency before running the consumer. Resolver selection grants no domain authority. The resolver-only composition remains distinct from the retained-state management test below; real product account/store/TLS isolation and shared-installation update evidence remain product obligations.

## Installation re-entry composition

[management_installation_test.go](management_installation_test.go), `TestPublicInstallationReentry`, exercises the public [Installation](../../installation/README.md), Identity and Audit contracts against a dedicated disposable PostgreSQL database. The fixture declares required schema/initial-account outcomes while missing root is optional context. Its own progress table retains `Attempted` before effects and `Verified` after reconciliation; the shared planner opens no database and executes nothing.

The test repeats public migrations, then creates one synthetic initial account with atomic Audit history. It simulates a lost successful account-creation acknowledgement by reloading retained progress and providing unknown evidence. Recheck must return reconciliation-required with no replay action. Authorized exact-login lookup reconciles the committed account; the final checks require one account and one Audit event, a ready plan after verified outcomes, and rejection of a changed candidate binding.

This is a controlled single-caller fixture with retained database progress, not a process-crash, concurrent installer, host provisioning or privileged administrator-role implementation. Real products/adapters must verify actual outcomes, authenticate the candidate, authorize exact targets, serialize/fence execution, durably record intent, and reconcile uncertainty before repeating effects. Schema migration and one-time account bootstrap retain their distinct domain semantics.

## Per-site activation with retained PostgreSQL state

In the same file, `TestPublicManagedSitesRetainPostgresState` uses public [Multi-Site](../../multisite/README.md) management and Core lifecycle contracts with two dedicated Notes databases. A trusted finite descriptor catalog and product-owned typed factory select Notes for site A while site B omits it. Constructor counts and absent-capability authorization checks ensure inactive features acquire no Notes instance; site A's persisted Unicode note stays out of site B's database.

An authorized immutable plan proposes removing Notes from A without mutating its live Application. The fixture applies the pure replacement input, stops the old compositions and constructs new ones. While Notes is inactive, its stored row remains and its capability is absent. Re-enabling constructs the feature again and reads the same note through its public Service; another site's principal is denied. Advancing selection generations rejects reuse of the old plan after disable/re-enable.

The fixture keeps selection configuration in caller-owned memory; it does not prove durable selection persistence, concurrent compare-and-swap, live ingress draining or a product restart/update. A real product must reauthorize the plan, persist the expected-revision change consistently, drain affected work, construct only selected typed instances, validate/start/check readiness and activate through its supported lifecycle. This test changes activation within one pinned product dependency set; it neither mixes module versions per site nor proves code-upgrade compatibility, real product tenancy or a CMS/Search implementation. Disabling a feature retains data; removal and destructive cleanup are separate authorized operations.

## Runtime language-file proving consumer

`cmd/localizationproof` is a bounded test-only consumer of the public Admin/localize APIs, with its own `notes` message domain and synthetic presentation-only principal. It is not a deployment authentication example. `TestLocalizationConsumerAddsFileAfterBuild` builds that executable once using normal pinned dependencies, runs the English default and selected Persian, then installs an external third test-only locale file and runs the unchanged executable again. It verifies public language discovery, generic RTL direction, reordered escaped user text, plural fallback to English and unchanged binary identity. It needs no database or browser; it does not replace actual Identity/Media session/screen verification below.

From this consumer directory, after selecting the reviewed compatible component pins:

```bash
GOWORK=off go test -race -count=1 -run '^TestLocalizationConsumerAddsFileAfterBuild$' .
```

The full isolated consumer suite also includes this test. Product-owned locale resources and the external directory are composed through supported public APIs; there is no SDK source copying, replacement directive or permanent workspace override. [File-based localization](../../docs/web/localization.md) explains the product integration and data-only language-pack workflow.

## Admin browser proving composition

The opt-in `admin_test.go` composes the real Admin shell with Identity-owned account forms and Media-owned library forms using only normally downloaded Core/component dependencies. It owns synthetic administrator/viewer/denied accounts, explicit product policy, two trusted local HTTPS browser origins and a private database/assets/control directory. Before the browser flow it creates a third test-only RTL locale at runtime and verifies actual login/Identity/Media pages plus safe browser-message snapshots through a separate temporary HTTPS session. The viewer's collection metadata grant does not permit file bytes/deletion or exact-login discovery. Durable state snapshots compare real denied/stale/malformed calls independently of displayed buttons.

Run from the repository root after installing the exact test-only Node/Playwright/Chromium profile required by `scripts/validate-admin-browser.sh` into an explicitly owned directory:

```bash
ACHRIX_BROWSER_NODE=/absolute/owned/browser-tools/node/bin/node \
ACHRIX_PLAYWRIGHT_MODULE=/absolute/owned/browser-tools/node_modules/playwright \
PLAYWRIGHT_BROWSERS_PATH=/absolute/owned/browser-tools/browsers \
ACHRIX_PG_BIN=/absolute/owned/pg-validation/install/bin \
scripts/validate-admin-browser.sh
```

The runner requires supported Go/PostgreSQL tools, normal checksums and cold component download with recursive runtime/embedded-asset source equality. It verifies certificates using only the fixture's public certificate/SPKI pins, constrains browser requests to its two local HTTPS origins and removes its owned temporary cluster/cache afterward. Product runtime has no Node/Playwright dependency. The browser suite exercises actual login/cookie/CSRF, create and exact-login recovery after a committed response is lost, finite waits without replay, conditional account management, PNG/JPEG plus PDF/ZIP/WebP/WebM original upload/library/private download/confirmed deletion, permissions, keyboard focus, responsive English/Persian RTL and preserved mixed text. PDF/ZIP bytes are complete consumer-owned test files; the pinned browser encodes complete WebP/WebM fixtures with its native test-only APIs. This introduces no product encoder or conversion dependency. It reports actual UI payload and request counts; these local observations establish no production network latency or capacity. `scripts/validate.sh full` separately runs the complete package trees and real exact-login PostgreSQL proof; the browser command is explicit and must pass for the Admin candidate's acceptance.

## Licensing

First-party files, including this fixture and its public Foundation contracts, are MPL-2.0. No Apache SDK exists. Preserve [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md) with distributed executables and provide matching covered source for the identified build and Core/component dependency set. Dependencies retain their own compatible terms. Normal dependency use neither creates an official badge nor changes product data ownership.

## Resource capacity observation

The normal public Media consumer/restore composition explicitly uses Identity `MaxConns: 1, MaxOperations: 2`, Audit `MaxConns: 6, MaxOperations: 24` and Media `MaxConns: 6, MaxOperations: 8`. Existing public create/login, authorized private reads (including retained SVG), conditional mutation and coherent restore checks run through these copied keyed Config values and the ordinary pinned dependency. Different pool/admission settings remain compatible with the same Identity/Audit database profile; they are not database identity. Module tests own the invalid/default/minimum matrix.

`cmd/resourceprofile` is an opt-in bounded raised-profile observation derived from the [recorded default fixture](https://github.com/AChWorks/achrix/issues/76#issuecomment-5976870760); ordinary tests/full CI compile it without repeating its two-minute idle observation. It composes control Identity+Audit and two independent Identity+Audit+Media sites, with six maximum connections for all eight pools, 24 leases for each Identity/Audit and eight for each Media: maxima 48 owned connections and 160 owned leases before caller/migration/admin/replica/reserve budgets. Constructor/start/bounded public demand, same-session reuse, native idle reclamation and shutdown are observed through PostgreSQL, without changing idle/health scheduling. Four simultaneous public reads per pool deliberately demand less than the configured maximum; the second eight-read pass verifies that the session PID set is reused.

Reproduce once on a disposable non-root PostgreSQL 18.6 UTF-8 cluster in a task-owned mode-0700 absolute root. Start a Unix socket only at `ROOT/socket` on port 5432 with no TCP listener; create empty template0 databases `achrix76_control`, `achrix76_site_a`, `achrix76_site_b` and private 0700 directories `ROOT/achrix76_site_a-media`, `ROOT/achrix76_site_b-media`. Set `ACHRIX76_ROOT` to that root and `ACHRIX76_PGUSER` to its actual owning PostgreSQL user, then from this normal pinned consumer run `GOWORK=off go run ./cmd/resourceprofile`. Keep any module cache outside the consumer tree and checksum verification enabled. Stop and verify the exact task-owned cluster afterward. The program has a 145-second context; native idle/health checks are not an exact expiry SLA. [Operations](../../docs/operations/operability-performance.md#module-resource-configuration) owns the resource contract. This observation establishes no product/fleet fairness, HTTP/TLS/site-isolation, throughput or RSS guarantee; Rixa owns actual product aggregate proof.

On 2026-10-04, the supported Go 1.27.1/Linux amd64/PostgreSQL 18.6 profile consumed runtime commit `a31688390798e2e50f596e768ee7ca95ce4107a2` through normal pin `v0.2.1-0.20261004141858-a31688390798`. Observed owned connections were constructor 0, startup 8, the 32-read burst 32, the subsequent eight-read pass 32 with the same session PID set, native reclamation 0 at the 120001 ms idle sample, and shutdown 0. The configured maximum was 48, rather than eagerly allocated demand. The private non-root Unix-socket cluster stopped successfully; `pg_ctl status` reported no server, `pg_isready` no response and no postmaster PID remained. These are observation results, not an expiry SLA.

Reproducible source SHA-256: `da455df2e7b31d1ba25a3060d46673b0ffea92471b8cf38cd75ee20694637dd1`; complete JSONL result SHA-256: `952a515ffdab0a2dcb282533f6898a58b847bf41b6c11e2929d6fc4d12221f1f`. The normal Foundation module checksum was `h1:HadrIT1gHoGZKxXKx+Maa7voj7ngJznSz0sWRScZhS4=`. All 28 validation inventory files (19 runtime Go files plus nine owning embedded assets) matched the downloaded dependency byte-for-byte, aggregate source SHA-256 `8bbebad9f7895ea1e4c3938b77d084761f3ae2c73903a1c89ce6ba960cf867f1`. Public Identity/Audit, Media and optional Multi-Site consumer race checks passed with real PostgreSQL and no database skips. Full required baseline and exact-candidate independent review remain integration gates, owned by the PR.

## Public-image consumer proof

The normal pinned Media consumer calls the distinct `PreparePublicImage` ABI1 on its already-retained PNG/JPEG, with product-owned exact-asset grants and a private in-memory destination. It verifies original Read is separately denied when only preparation is granted, complete output/source hashes and dimensions, and unchanged database/private original state. The same public call is verified after coherent restore. No extra asset or persisted derivative changes the existing five-original PNG/JPEG/PDF/ZIP/SVG capture, and no public publication route is introduced. [Media](../../media/README.md#clean-public-image-preparation) owns the profile/API; product publication owns staging/freshness/activation. Exact dependency identity and release compatibility are owned by this fixture's manifests and the lifecycle guide, not by prose in this proof section.

## Diagnosing failures locally

The CLI emits structured JSON to stderr. Configuration errors name the runtime setting; bind, migration, startup, output and shutdown failures name their stage and produce a nonzero exit. Library components preserve stable outward categories while retaining the original cause at their optional protected `Diagnostic` hook. The fixture's `NewWithDiagnostics` / `MigrateWithDiagnostics` show the same ownership at its private PostgreSQL boundary.

Correlate the response's server-generated `X-Request-ID` with component, operation, phase and cause fields. Notes preserves an existing trusted context ID across nested adapters and ignores an incoming header as authority. A normal pinned-consumer PostgreSQL fault test demonstrates one response identity reaching the original `PgError` in a protected callback and a deliberately typed safe support projection. A product adapter must choose its provider-aware fields and redact credentials/payloads before storage/export; never serialize an arbitrary error wholesale.

Distinct failures remain visible within a fixed runtime budget; timed suppression summaries arrive even if requests stop, and orderly CLI exit flushes pending summaries. A response timeout or 503 never establishes that a sent write did not happen. Preserve the operation/resource identity and use the owning authorized reconciliation flow; Notes does not automatically retry writes.

Failed validation retains bounded private PostgreSQL/fixture logs before deleting the disposable cluster. The printed evidence directory has restricted permissions; `support.json` is a payload-free metadata projection. Private log files may contain statements or fixture data: inspect/redact before sharing and remove them after diagnosis. [Operations](../../docs/operations/operability-performance.md#diagnosis-and-audit) owns the common policy and retention guidance.
