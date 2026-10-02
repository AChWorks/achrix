# Development Media Module

Media supplies an authorized private PNG/JPEG asset library inside the Foundation Go dependency. It owns generic asset identity, metadata and storage lifecycle. Products own permission grants, upload ingress, content relationships, retention policy and deployment/recovery profiles. Media has no Identity, Audit, Admin, public URL or static-file dependency. This is next-minor development source; the published v0.1.0 dependency has no Media implementation.

The Module descriptor uses `achrix.Version()` for the containing Foundation dependency's source identity; capability ABI revisions are separate. Public Go declarations own the exact API.

## Compose and authorize

The first supported development proof profile is Go 1.27.1, PostgreSQL 18 UTF-8 and Linux, with one product-owned private local filesystem directory. Storage must support exclusive hard links, directory/file `fsync` and Linux `flock`, and honor their durability semantics. Network filesystems, object storage and a production/off-host recovery profile are not established by this implementation. Remote PostgreSQL connections require verified TLS, including every parsed fallback host; private loopback/Unix-socket development connections may omit it. PostgreSQL `fsync` and `full_page_writes` must be enabled. Module connections force `synchronous_commit=on`; migration/startup check the supported settings.

Create an existing absolute-path directory with mode `0700`, outside every webroot. Keep it exclusively owned by this product's Media composition; never edit its files independently or expose it through a web server. `NewPostgres(dsn, Config{StorageRoot: path}, logger)` parses configuration without I/O. Explicitly run `Migrate(ctx, dsn)` before startup, then compose the Module with `achrix.New` and the product Policy. Construct `NewService(app, module)` for all domain calls. Migration and lifecycle contexts require deadlines. Core orders Media after its required `achrix.authorization` ABI 2 provider. No optional capability or role is inferred.

| Capability | Target | Meaning |
| --- | --- | --- |
| `Create` | `LibraryTarget` (`library`) | Create a private image asset. |
| `List` | `LibraryTarget` | Discover bounded collection metadata, including filenames. |
| `Read` | Exact generated asset ID | Read durable status or original image bytes. |
| `Delete` | Exact generated asset ID | Conditionally delete that asset at a supplied revision. |
| `Reconcile` | `LibraryTarget` | Abort interrupted pending uploads and finish deletion, in bounded batches. |

The product Policy decides grants. Uploader identity is not ownership, collection metadata permission does not grant byte reads/deletion, and opaque IDs are not bearer authorization. Cheap filename/ID/cursor/limit validation precedes policy evaluation without reading sensitive state; denial precedes database/storage effects. Authentication happens at the product ingress, followed by separate Application authorization for each operation.

`List` uses generated-ID keysets with limits 1–100 and an opaque cursor. It returns ready metadata only and is not a multi-page snapshot. `Status` separately authorizes the exact ID and can return pending/deleting/deleted state. Deleted records retain only ID, time, state and revision; identifying filename, hash/type/dimensions/size are cleared. No listing-by-uploader, content relationship, retention scheduler or destructive tombstone-removal API is supplied.

## Input, transport and resource limits

Uploads accept PNG or JPEG based on their bytes, with a maximum of 10 MiB, maximum dimension 4096 and maximum 8,388,608 pixels. Extension and caller MIME claims do not select the decoder. Header checks precede full decoding with the maintained Go standard [PNG](https://pkg.go.dev/image/png) and [JPEG](https://pkg.go.dev/image/jpeg) decoders. The process-global image registry cannot expand the accepted formats. Full decoding rejects malformed/truncated image streams. SVG, GIF, PDF, video, transformation and derivatives are excluded. Original accepted bytes are retained; metadata stripping or malware scanning is not claimed.

Filenames are bounded UTF-8 metadata, not storage paths: at most 256 bytes/120 code points, no separators, leading/trailing whitespace, control characters or Unicode bidi controls. Ordinary Persian text, ZWNJ and ZWJ are preserved. Generated 128-bit opaque IDs exclusively name stored files. The local adapter checks that both generated names are unused before creating durable intent, uses descriptor-contained `os.Root` operations, rejects symlink/nonregular/wrong-mode/hard-linked read targets, and publishes with an exclusive hard link rather than replacing an existing destination.

Each Module instance admits at most four owned operations/four PostgreSQL connections and two nonqueued decoder computations. Saturation returns `ErrLimited` immediately. Copies use fixed 32 KiB buffers without invoking caller `ReaderFrom`/`WriterTo` fast paths. Read verifies the complete stored size/SHA-256 before delivering any byte, then streams a second pass; missing/corrupt assets produce safe unavailability before the destination is written. Products must serve original bytes as an authenticated attachment with the derived exact MIME, `nosniff` and private/no-store policy, without static paths/public links.

Service operations add finite deadlines (create/read 15 seconds, list/status one second, delete five seconds, reconcile ten seconds). Synchronous `io.Reader`/`io.Writer`, filesystem calls and image decoders cannot be forcibly interrupted by a context. Trusted caller I/O must return on its transport/caller deadline; a network ingress must set actual socket/body read/write deadlines, bound multipart/body parsing, requests and header/idle budgets. A context alone does not bound arbitrary `Read` or `Write`. Cancellation is checked between buffer reads/writes and around decoding. These bounds do not claim a process-wide RSS/CPU limit; a product must measure aggregate demand across composed Modules and choose its admission/deployment profile.

Module shutdown closes its admission, cancels owned work and waits for it before closing the pool/storage descriptors. Products first stop ingress and drain their domain work, as required by Core's lifecycle contract. A noncompliant blocked caller can exhaust the stop deadline; Core retains that failed cleanup result and operator/process recovery may be needed. Shutdown does not promise a forced interruption or an automatic retry of a completed failed Module stop.

## Durable lifecycle and unknown outcomes

The immutable PostgreSQL migration owns `media.assets` and its migration ledger. Installation serializes through a transaction advisory lock. Repeated identical installation is harmless; unknown/changed ledger identities fail. Startup checks the existing environment, ledger and required columns; it does not migrate or continuously detect privileged database/storage tampering. Reverting source does not reverse this state.

1. Create generates its ID, acquires a per-asset PostgreSQL session lock and acknowledges a durable `pending` intent at revision 1 before touching storage. No database transaction spans streaming/decoding.
2. Storage exclusively creates `ID.upload` with mode `0600`, streams/hashes/validates it, synchronizes the file, exclusively links `ID`, removes the temporary name and synchronizes the directory.
3. Only then does PostgreSQL conditionally publish `ready` at revision 2 with derived metadata. A crash before publication leaves an explicitly reconcilable pending intent, never an incomplete readable asset.
4. Delete requires the exact ready revision, acknowledges a `deleting` revision before unlinking either name, synchronizes the directory, then publishes a cleared `deleted` tombstone at another revision. Revision overflow is rejected before changing state or unlinking files. Interrupted deletion resumes idempotently.

`Create` returns its generated ID on errors after intent insertion begins. `ErrUnknownOutcome` means an acknowledgement may have been lost: do not blindly replay creation/deletion or infer rollback. Read `Status` at the known ID. Authorized `Reconcile` takes limits 1–40, aborts pending uploads and finishes deleting states; it never publishes an interrupted upload. Repeating a completed reconciliation is harmless. If an entire HTTP creation response is lost, the client may not know the ID; the product must expose honest inspection through granted collection metadata and separately authorized reconciliation, without silently retrying the upload.

Session advisory locks span filesystem effects, and unknown acquisition/unlock acknowledgements discard the physical connection. Independent directory `flock` descriptors provide another safety boundary if a PostgreSQL session disappears during a transfer: creates/reads share the directory lock; delete/reconcile need an immediate exclusive lock. Thus a cleanup can return `ErrConflict` while **any** transfer is active, including another asset or independently composed instance using the same root. This conservative local-provider policy favors safe cleanup over concurrent deletion. After exclusive directory admission, busy individual asset locks are counted by `ReconcileResult.Busy` and untouched. Callers retry only reconciliation after inspecting its safe result; they do not replay an unknown domain mutation.

Expected input/absence/conflict/capacity and ordinary cancellation errors are quiet. Operational failures return safe categories, increment `FailureCount` and emit at most one fixed-field diagnostic per Module per second. Unknown-outcome errors remain inspectable even when joined with cancellation and have their own safe diagnostic reason. Raw driver errors, paths, filenames and file bytes are not logged. The product owns logger handler/resource policy and request correlation.

## Capture and restore boundary

A coherent capture covers the product PostgreSQL database (including Media metadata and immutable migration ledger), the original ready files, and protected source/configuration/profile identity needed to interpret them. Relationships owned by other Modules/products must survive in the same coordinated profile. A SQL-only capture cannot restore ready bytes; a files-only copy cannot restore authorization, asset identity or lifecycle metadata.

For this private development proof, stop ingress and drain domain operations across all instances; while the Application is still available, explicitly reconcile unfinished Media work until no processed/busy result remains; then stop Modules. Capture native `pg_dump` plus a manifested private file archive at that quiescent point. Record IDs, sizes/SHA-256, database/migration/source identity and private permissions. Restore only into a separate empty compatible database/root after checking coverage, integrity, safe entry names/types and target compatibility. Preserve root `0700` and files `0600`, reject symlink/hardlink/path traversal entries, and verify each ready asset's size/hash and retained relationships before exposing ingress. Reconstruct the normal pinned composition, verify readiness and authorized `List`/`Status`/`Read` on retained data. Readiness alone does not verify every file or establish recovery correctness.

The independent Notes composition must prove this bounded quiesced native database-plus-assets restore in an isolated target before integration. It establishes no live product, off-host copy, production RPO/RTO/retention promise, partial Module restore or implementation of paused #19/downstream #49. Product recovery must also handle credentials, restored sessions, roles/keys/configuration and external effects according to their owning contracts.

## Focused validation and measurements

Set `ACHRIX_MEDIA_TEST_DSN` to the disposable task-owned PostgreSQL test database (its name must start with `achrix_media_`), then run `go test -race -count=1 ./media`. The tests reset only the Media schema in that explicitly selected database and use private temporary storage. Without that variable, real PostgreSQL tests report a skip rather than successful database proof. Repository full validation owns the normal pinned independent consumer, TLS boundary, retained restore and exact CI gate.

On 2026-10-02, Go 1.27.1/Linux amd64 on AMD EPYC 7763 development hardware with `GOMAXPROCS=2` measured three runs of three operations:

- A maximum 4096×2048 RGBA64 PNG full decode: 150–160 ms and approximately 67.24 million bytes (64.1 MiB) allocated per operation, 24 allocations. Two concurrently admitted decodes can therefore allocate roughly 128 MiB of pixel buffers plus decoder/runtime/other Module overhead; this is not product total RSS or a hard heap ceiling.
- A synthetic 10 MiB zero-reader to discard transfer: 0.13–0.19 ms, 33,128 allocated bytes, eight allocations. This isolates the fixed-buffer mechanism and is not disk/network throughput.
- A warm local 10 MiB file hash pass followed by a second discard-copy pass: 10.31–10.40 ms (approximately 1.01 GB/s of logical delivered bytes), 66,112 allocated bytes, 13 allocations. Cache, host/storage, TLS/network and concurrent product load affect deployment performance.

Reproduce with `GOMAXPROCS=2 go test -run '^$' -bench 'Benchmark(ValidatePNG16|Copy10MiB|StorageHashCopy10MiB)$' -benchmem -benchtime=3x -count=3 ./media`. Real PostgreSQL `EXPLAIN (ANALYZE, BUFFERS)` with 6,000 synthetic metadata rows verifies the ready keyset partial index, exact-ID primary key and unfinished-state partial index without planner overrides. These are representative bounded access-path observations, not a production scale/SLO claim.
