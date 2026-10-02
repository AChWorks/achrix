# Notes proving consumer

This separately composed Go module proves consumption of AChrix's public boundary. It is a benign local fixture, not a production content product, CMS, shared Content Module or real product repository. It owns immutable notes, its schema/migration, authentication policy and HTTP adapter. Foundation owns only shared composition/authorization/lifecycle contracts.

Its `go.mod` pins a source-backed Foundation version and pgx; `go.sum` retains normal checksums. There is no `replace`, workspace override, source copy or Foundation internal import. `internal/domain` owns text/ID invariants; `internal/application` authorizes typed create/read operations; `internal/infrastructure` owns SQL/resources; `internal/presentation` maps HTTP to the same Service.

## Setup and validation

Use the exact supported tools from [Operations](../../docs/operations/operability-performance.md#supported-environment). From the repository root:

```bash
scripts/validate.sh
```

The command provisions only a task-owned local Unix-socket PostgreSQL cluster, runs Foundation and isolated consumer tests with `-race`, proves normal pinned dependency download/build, and restores its trusted logical dump into a separate empty database. It checks data, immutable schema identity, composition/readiness and an authorized read; then removes only its owned test workspace/cluster. It requires a non-root user and never uses a host database service. Missing PostgreSQL is a failure, not a skipped persistence check. There are no mandatory containers or auxiliary services.

If PostgreSQL tools are absent and the listed build prerequisites are installed:

```bash
scripts/setup-validation-postgres.sh /absolute/new/owned/pg-validation
ACHRIX_PG_BIN=/absolute/new/owned/pg-validation/install/bin scripts/validate.sh
```

The installer builds official checksum-pinned PostgreSQL source in that new directory only. It does not install system packages or configure host services. See [Lifecycle](../../docs/lifecycle/lifecycle-and-compatibility.md#initial-fixture-lifecycle-and-database-scope) for the tested database-only scope and exclusions.

For interactive local operation, use a separately owned empty PostgreSQL 18 database and set `NOTES_DATABASE_URL` through your local runtime configuration/secret source. Only a local socket/loopback DB is accepted. Do not use production data or put a real DSN in Git. Set distinct random `NOTES_WRITER_TOKEN` and `NOTES_READER_TOKEN` (32–128 characters); for a disposable local demonstration, generate each independently with `openssl rand -hex 32`. Do not reuse credentials from another service. `NOTES_LISTEN` defaults to `127.0.0.1:8080` and accepts loopback addresses only.

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

Bodies are capped at 1 KiB; text is valid nonblank Unicode with at most 200 characters and no NUL. Unknown fields/trailing JSON are rejected. Public codes are `unauthenticated` (401), `permission_denied` (403), `invalid_input` (400), `not_found` (404), `unavailable`/`busy` (503). `X-Request-ID` correlates safe structured stderr records across the actual boundaries. There is no payload/DSN/token/SQL capture, debug endpoint or central telemetry requirement. Notes have no update/delete/list endpoint or external effect. Create has no automatic retry/replay guarantee; a network timeout alone does not prove that a write was absent.

Read cheaply preflights the opaque reference's 26–64 byte ASCII/base32 syntax before policy or storage; malformed references reveal no existence. The fixture's create capability deliberately permits the empty global scope. Service operations supply deadlines, explicitly denied policies map to 403 and safe Core evaluation-unavailable maps to 503. Its descriptor requires Core authorization ABI 2 under the [next-minor migration](../../docs/lifecycle/lifecycle-and-compatibility.md#next-development-minor-migration); the schema/migration and Notes capability ABI remain unchanged.

For a local request, with the writer token already supplied as a runtime variable:

```bash
curl --fail-with-body -H "Authorization: Bearer $NOTES_WRITER_TOKEN" \
  -H 'Content-Type: application/json' --data '{"text":"Hello from the consumer"}' \
  http://127.0.0.1:8080/notes
```

Retain the returned opaque ID to read it. Authentication tokens are not used as principal IDs, logged, stored in domain records or included in discovery metadata. This loopback-only fixture does not establish a remote/TLS/authentication deployment profile.

## Identity and deliberate updates

```bash
go run ./cmd/notes -mode identity
go get "github.com/AChWorks/achrix@$ACHRIX_VERSION"
go mod tidy
```

Before the update command, set `ACHRIX_VERSION` to the exact reviewed commit/version you intend to consume. The identity command reports the actual Foundation dependency, fixture Module version, consumer build revision and migration checksum. Isolated validation injects the exact consumer Git revision when building outside its Git worktree and checks that the Foundation runtime identity matches its manifest pin. Unversioned local builds identify themselves as development. Full validation also builds `cmd/componentidentity` and compares the composed Identity/Audit `Descriptor.Version` values with the normally resolved Foundation pin. This uses a real executable because Go test binaries may omit dependency build metadata; the deprecated source-line constants are not build identity. No v1 stability, automatic updater or binary release is claimed.

Review the update/manifest diff, run `scripts/validate.sh` from the root, then rebuild your consumer. Deployment remains a separate owner action; changing Foundation source does not update an existing executable. Never edit a published migration in place or equate reverting source with reverting data. Later real-product update/recovery work remains gated by Issue #19.

## Private Media proving composition

The test-only `media_test.go` composes public Core, Identity, Audit and Media contracts from the normal Foundation dependency. The product fixture owns explicit create/list/exact-object-read/delete/reconcile grants, trusted same-origin HTTPS ingress and synthetic accounts. Uploading or authenticating grants no object rights; collection discovery and byte access remain distinct. PNG/JPEG originals, Persian filenames including ZWNJ, bounded keyset listing, conditional delete and safe rejected input exercise the real private image library. This fixture does not define content relationships, public serving, roles or a production storage profile.

Full validation uses separate private Media test/consumer/restore databases and mode-0700 storage roots. It stops ingress and all participating Modules before native PostgreSQL capture and a manifested private asset archive, verifies paths/types/checksums before extraction, compares the whole participating metadata/ledger datasets and reads retained images through the restored public services. A missing or corrupt asset must fail before destination bytes are written. The tested coherent quiesced local capture has no live snapshot, off-host delivery or RPO/RTO guarantee. See [Media](../../media/README.md) for lifecycle, resource and recovery obligations; #19 and #49 remain separately gated.

## Licensing

First-party files, including this fixture and its public Foundation contracts, are MPL-2.0. No Apache SDK exists. Preserve [LICENSE](LICENSE) and [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES.md) with distributed executables and provide matching covered source for the identified build/Foundation pin. Dependencies retain their own compatible terms. Normal dependency use neither creates an official badge nor changes product data ownership.
