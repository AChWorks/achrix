# Media Module

Media supplies an authorized private attachment library inside the Foundation Go dependency. It owns generic asset identity, metadata and storage lifecycle. Products own permission grants, upload ingress, content relationships, retention policy and deployment/recovery profiles. Media has no Identity, Audit, Admin, public URL or static-file dependency. The published v0.2 line includes the default/common attachment profile; explicit SVG, clean public-image preparation and schema version 3 belong to the next v0 minor successor. The published v0.1.0 dependency has no Media implementation.

The default upload profile remains PNG/JPEG. Products explicitly opt into the finite common profile with `Config{StorageRoot: path, AllowedMIMEs: CommonMIMEs()}`, or supply a nonempty subset of the owning canonical MIME values. `CommonMIMEs()` remains the same 36-type selection and excludes SVG. Select private SVG separately with `AllowedMIMEs: []string{"image/svg+xml"}` or append that value to the common selection. An empty non-nil selection, duplicates and unknown MIME values fail configuration. Configuration slices are copied; `Service.Formats()`, `SupportedFormats()` and `CommonMIMEs()` return independent copies. The effective service inventory supplies upload controls. The supported inventory remains available for reading retained files after a product narrows new-upload admission.

Private attachment admission/download, preview, extraction and document/video conversion are distinct capabilities. Private attachment admission retains original bytes and starts no automatic processor or background worker. Explicit public-image preparation is a separately authorized call described below. A disabled opaque extension fails after the eight-byte historical image sniff, before allocating the recognition prefix or calling its detector. Opaque common recognition acquires no image decoder slot. Dependency presence does not promise per-format code unloading or independent dependency versions.

## Finite common profile

`SupportedFormats()` owns these canonical MIME values and lowercase extension aliases. Only PNG/JPEG are fully decoded; every other listed format retains width and height zero. SVG appears in the supported catalog for separate explicit selection, with complete bounded XML admission below; it is outside `CommonMIMEs()`. SVGZ, HTML/JavaScript/executables and explicit macro-enabled Office extensions remain unsupported.

| MIME | Extensions |
| --- | --- |
| `image/png` | `png` |
| `image/jpeg` | `jpg`, `jpeg`, `jpe` |
| `image/gif` | `gif` |
| `image/webp` | `webp` |
| `image/avif` | `avif` |
| `image/bmp` | `bmp` |
| `image/tiff` | `tif`, `tiff` |
| `image/x-icon` | `ico` |
| `application/pdf` | `pdf` |
| `application/msword` | `doc` |
| `application/vnd.openxmlformats-officedocument.wordprocessingml.document` | `docx` |
| `application/vnd.ms-excel` | `xls` |
| `application/vnd.openxmlformats-officedocument.spreadsheetml.sheet` | `xlsx` |
| `application/vnd.ms-powerpoint` | `ppt` |
| `application/vnd.openxmlformats-officedocument.presentationml.presentation` | `pptx` |
| `application/vnd.oasis.opendocument.text` | `odt` |
| `application/vnd.oasis.opendocument.spreadsheet` | `ods` |
| `application/vnd.oasis.opendocument.presentation` | `odp` |
| `text/plain` | `txt` |
| `text/csv` | `csv` |
| `application/zip` | `zip` |
| `application/vnd.rar` | `rar` |
| `application/x-7z-compressed` | `7z` |
| `video/mp4` | `mp4`, `m4v` |
| `video/quicktime` | `mov` |
| `video/webm` | `webm` |
| `video/matroska` | `mkv` |
| `video/x-msvideo` | `avi` |
| `video/mpeg` | `mpeg`, `mpg` |
| `video/ogg` | `ogv` |
| `audio/mpeg` | `mp3` |
| `audio/mp4` | `m4a` |
| `audio/ogg` | `ogg`, `oga` |
| `audio/wav` | `wav` |
| `audio/flac` | `flac` |
| `audio/aac` | `aac` |
| `image/svg+xml` (explicit only) | `svg` |

The Module descriptor uses `achrix.Version()` for the containing Foundation dependency's source identity; capability ABI revisions are separate. Public Go declarations own the exact API.

## Compose and authorize

The first supported development proof profile is Go 1.27.1, PostgreSQL 18 UTF-8 and Linux, with one product-owned private local filesystem directory. Storage must support exclusive hard links, directory/file `fsync` and Linux `flock`, and honor their durability semantics. Network filesystems, object storage and a production/off-host recovery profile are not established by this implementation. Remote PostgreSQL connections require verified TLS, including every parsed fallback host; private loopback/Unix-socket development connections may omit it. PostgreSQL `fsync` and `full_page_writes` must be enabled. Module connections force `synchronous_commit=on`; migration/startup check the supported settings.

Create an existing absolute-path directory with mode `0700`, outside every webroot. Keep it exclusively owned by this product's Media composition; never edit its files independently or expose it through a web server. `NewPostgres(dsn, Config{StorageRoot: path}, logger)` parses configuration without I/O. Explicitly run `Migrate(ctx, dsn)` before startup, then compose the Module with `achrix.New` and the product Policy. Construct `NewService(app, module)` for all domain calls. Migration and lifecycle contexts require deadlines. Core orders Media after its required `achrix.authorization` ABI 2 provider. No optional capability or role is inferred.

| Capability | Target | Meaning |
| --- | --- | --- |
| `Create` | `LibraryTarget` (`library`) | Create a private supported attachment. |
| `List` | `LibraryTarget` | Discover bounded collection metadata, including filenames. |
| `Read` | Exact generated asset ID | Read durable status or original attachment bytes. |
| `Delete` | Exact generated asset ID | Conditionally delete that asset at a supplied revision. |
| `Reconcile` | `LibraryTarget` | Abort interrupted pending uploads and finish deletion, in bounded batches. |
| `PreparePublicImage` | Exact generated asset ID | Prepare one known ready revision into a trusted private destination; grants no publication or original Read. |

The product Policy decides grants. Uploader identity is not ownership, collection metadata permission does not grant byte reads/deletion, and opaque IDs are not bearer authorization. Cheap filename/ID/cursor/limit validation precedes policy evaluation without reading sensitive state; denial precedes database/storage effects. Authentication happens at the product ingress, followed by separate Application authorization for each operation.

`List` uses generated-ID keysets with limits 1–100 and an opaque cursor. It returns ready metadata only and is not a multi-page snapshot. `Status` separately authorizes the exact ID and can return pending/deleting/deleted state. Deleted records retain only ID, time, state and revision; identifying filename, hash/type/dimensions/size are cleared. No listing-by-uploader, content relationship, retention scheduler or destructive tombstone-removal API is supplied.

## Input, transport and resource limits

Every file retains the 10 MiB cap. PNG/JPEG are selected from bytes independently of filename/extension, with maximum dimension 4096 and maximum 8,388,608 pixels. Header checks precede full decoding with the maintained Go standard [PNG](https://pkg.go.dev/image/png) and [JPEG](https://pkg.go.dev/image/jpeg) decoders. The process-global image registry cannot expand these explicit decoders. Full decoding rejects malformed/truncated image streams.

Opaque common formats require both an enabled supported extension and compatible byte recognition by pinned [mimetype v1.4.15](https://github.com/gabriel-vasile/mimetype/tree/v1.4.15). Media passes at most the first 4096 bytes and never calls `SetLimit`/`Extend` or promotes a generic ZIP/OLE container to an Office subtype based on its claimed extension. UTF-8 TXT/CSV additionally scan the whole already size-bounded file with a fixed buffer, rejecting malformed UTF-8 and controls other than tab/CR/LF. Caller Content-Type has no admission authority.

Recognition is a bounded file/container classification, not complete validity, malware scanning or safe-to-open attestation. Legitimate encodings/layouts whose identifying metadata falls beyond the prefix may fail closed: this includes ordinary LibreOffice-generated DOC/XLS/PPT with late CFB directories and Office ZIP packages whose subtype cannot be seen within the prefix. Encrypted Office containers whose exact subtype is not recognized fail closed. Opaque archive encryption/contents are not inspected, and recognized encrypted archives are not promised to be rejected. Newly admitted GIF/WebP/AVIF/BMP/TIFF/ICO also remain opaque: their dimensions/pixels are not decoded or bounded. No opening, extraction, conversion, transcoding, preview, metadata stripping or sanitization is supplied.

Explicit SVG requires `.svg` and a complete UTF-8 XML 1.0 document through EOF, with exactly one root whose expanded name is `{http://www.w3.org/2000/svg}svg`. An optional UTF-8 BOM and one leading XML declaration are accepted; comments and literal XML whitespace may surround the root. The standard `encoding/xml` tokenizer is supplemented by whole-document XML 1.0 character checks and raw start-tag attribute-separator checks. Media rejects malformed/truncated XML, extra roots, non-whitespace outside the root, duplicate attributes, all directives/DTDs and all other processing instructions. No custom entity/charset resolver or network/rendering path is installed. Standard predefined/numeric XML character references remain valid. SVG input is at most **1 MiB**, with depth at most **128**, at most **100000** elements and at most **128** attributes on each element (including namespace declarations). Parsing holds one of the same two nonqueued expensive-validation slots used by image decoding and checks context cancellation between tokens.

This is structural private admission, not full SVG/schema/namespace conformance, sanitization or safe-to-open attestation. Scripts and resource references inside the document remain opaque original bytes; untrusted width/height/viewBox attributes never become stored pixel dimensions. MIME is canonical `image/svg+xml`, dimensions are zero, and exact original bytes/size/hash are retained. Products serve those bytes only through the separately authorized private attachment path; no inline/public SVG, HTML/object preview or direct StorageRoot serving is supplied. Future public SVG requires a separately reviewed publication flow and passive output/transport/resource profile.

Filenames are bounded UTF-8 metadata, not storage paths: at most 256 bytes/120 code points, no separators, leading/trailing whitespace, control characters or Unicode bidi controls. Ordinary Persian text, ZWNJ and ZWJ are preserved. Generated 128-bit opaque IDs exclusively name stored files. The local adapter checks that both generated names are unused before creating durable intent, uses descriptor-contained `os.Root` operations, rejects symlink/nonregular/wrong-mode/hard-linked read targets, and publishes with an exclusive hard link rather than replacing an existing destination.

Each Module instance uses `Config.MaxOperations` for active owned leases and `Config.MaxConns` for pool connections; each zero defaults to four and each explicit minimum is one. The separate two nonqueued expensive-validation slots remain fixed, shared by PNG/JPEG decoding and SVG parsing. Saturation returns `ErrLimited` immediately. [Operations](../docs/operations/operability-performance.md#module-resource-configuration) owns validation, aggregate budgets and native pool creation/reuse/reclamation; raised maxima do not eagerly allocate resources. Copies use fixed 32 KiB buffers without invoking caller `ReaderFrom`/`WriterTo` fast paths. Read verifies the complete stored size/SHA-256 before delivering any byte, then streams a second pass; missing/corrupt assets produce safe unavailability before the destination is written. Products must serve original bytes as an authenticated attachment with the derived exact MIME, `nosniff` and private/no-store policy, without static paths/public links.

Service operations add finite deadlines (create/read 15 seconds, list/status one second, delete five seconds, reconcile ten seconds). Synchronous `io.Reader`/`io.Writer`, filesystem calls and image decoders cannot be forcibly interrupted by a context. Trusted caller I/O must return on its transport/caller deadline; a network ingress must set actual socket/body read/write deadlines, bound multipart/body parsing, requests and header/idle budgets. A context alone does not bound arbitrary `Read` or `Write`. Cancellation is checked between buffer reads/writes and around decoding and between XML tokens. These bounds do not claim a process-wide RSS/CPU limit; a product must measure aggregate demand across composed Modules and choose its admission/deployment profile.

Module shutdown closes its admission, cancels owned work and waits for it before closing the pool/storage descriptors. Products first stop ingress and drain their domain work, as required by Core's lifecycle contract. A noncompliant blocked caller can exhaust the stop deadline; Core retains that failed cleanup result and operator/process recovery may be needed. Shutdown does not promise a forced interruption or an automatic retry of a completed failed Module stop.

## Clean public-image preparation

`Service.PreparePublicImage(ctx, actor, PreparePublicImageRequest{AssetID: id, ExpectedRevision: revision}, privateWriter)` produces a complete `PublicImage` result only after success. `PreparePublicImage` identifies `achrix.media.prepare-public-image` ABI 1; this exact-asset grant alone authorizes the operation. `Read`, `List` and `Create` never imply it. Product Policy may require additional rights. This API belongs to the next v0 minor successor; published v0.2.0 has no such method.

Use a trusted **private** destination, discard all partial output on every error and never stream it directly to a public response. Media does not close the writer, persist a derivative or activate delivery. The result contains only `SourceAssetID`, `SourceRevision`, `SourceSHA256`, `Profile`, `ColorBasis`, `MIME`, `Size`, `SHA256`, `Width` and `Height`. It binds one verified snapshot, not future freshness or permission. Products own derivative storage, source relationships/freshness checks, separately authorized publication, atomic activation, public URLs/cache, withdrawal and retention. Original asset bytes/hash/revision and private upload/Read remain unchanged. Rixa's [publication contract](https://github.com/AChWorks/rixa/issues/5) owns its product workflow; this Module has no CMS dependency.

Profile `achrix-public-image-v1` is a finite SDR application profile. It makes no color-managed, cross-viewer or perceptual-equivalence claim. Ordinary untagged web images use an explicit sRGB assumption. Inputs outside this preparation profile can remain privately uploadable/readable under the original attachment policy.

| Input | Accepted interpretation / fresh output |
| --- | --- |
| PNG | Static gray/indexed 1/2/4/8-bit or RGB/gray-alpha/RGBA 8-bit; Adam7 supported. Complete signature/length/CRC/order/EOF validation and native decode. Reject 16-bit, APNG, ICC, cICP/HDR declarations and unknown chunks. |
| PNG color | `sRGB` intent 0–3; optional `gAMA=45455` and exact canonical sRGB/D65 `cHRM`. Preserve accepted declaration presence; reject other gamma/chromaticities. Without sRGB, legacy gamma/chromaticity remain `legacy-canonical-png`, including alongside compatible Exif. Only Exif ColorSpace=1 with no PNG color chunks synthesizes sRGB intent 0. Untagged remains untagged. |
| PNG auxiliary | Bound/validate then discard sBIT, bKGD, hIST, sPLT, tIME and ordinary text; compressed text is not decompressed. Reject identified XMP/raw-Exif text containers. pHYs requires positive equal X/Y and known unit. eXIf accepts one bounded TIFF. Product supplies background. |
| JPEG | 8-bit Huffman SOF0/SOF1/SOF2; gray 1x1, RGB 444 or YCbCr 444/440/422/420/411/410. Full marker/segment/scan/EOI/EOF and native decode, including nonzero 8-bit quantization, defined tables, valid Huffman prefixes and progressive DC/band/refinement history. Legal omitted AC bands remain zero; full precision is not required. Reject CMYK/YCCK, other coding processes, unknown apps and trailers. |
| JPEG metadata | Bounded JFIF1.00–1.02 with equal positive densities, recognized JFXX thumbnails, one Exif APP1 and exact Adobe APP14 version100/101 with nonessential flags and compatible RGB/YCbCr transform. Discard thumbnails/COM; reject XMP, ICC/MPF, JPEG Systems/HDR, Photoshop and unknown APP containers. |
| PNG output | Straight NRGBA samples; preserve RGB at alpha>0 and all alpha, zero RGB at alpha0 to remove recoverable invisible color. This is not general pixel redaction. Fresh accepted PNG color declarations only. |
| JPEG output | Fixed quality90 baseline; gray stays gray, color uses native420. This deliberately loses progressive/source-subsampling structure and is lossy. Fresh fixed JFIF1.02/unit0/density1x1/no thumbnail; a small fresh Exif ColorSpace=1 only for declared sRGB. No raw metadata copy or full Exif3.1/JFIF combined-conformance claim. |

`ColorBasis` is exactly `declared-srgb`, `legacy-canonical-png` or `assumed-untagged`. A declaration does not prove correct source production; JFIF/Adobe channel conventions do not prove sRGB, and gamma45455 is a legacy power law rather than piecewise-sRGB proof.

Finite Exif inspection supports checked II/MM TIFF spans/types/counts/duplicates, at most eight directories/depth four/4096 entries and no cycles. Interpret IFD0/Exif/Interop; structurally bound and discard private GPS/MakerNote/thumbnail contents. Normalize Orientation1–8 (5–8 swap dimensions), accept only ColorSpace1, supported versions0200/0210/0220/0221/0230/0231/0232/0300 and InteropR98/0100. Raw pixel dimensions/component declarations must agree; optional chromaticities/coefficient fields must be canonical sRGB/D65/YCbCr. Equal positive physical resolutions are discarded; co-sited YCbCr is accepted only444. Reject gamma/transfer/reference-black-white, unknown primary interpretations and primary TIFF encoding/offset fields. Known bounded capture/ownership/descriptive fields are discarded. No original metadata values are returned/logged. Current Exif3.1 still uses the0300 identifier; this finite support does not imply full Exif conformance.

Source preparation takes one of the same two nonqueued expensive slots **before allocating a snapshot** and retains it through verification, decode, orientation, encoding and private writes. It verifies one complete size/hash snapshot under existing database/file locks, rejects unavailable/nonready/stale/raw-dimension conflicts and never rereads a separately mutable file. The 15-second context is cooperative; native codecs and arbitrary synchronous I/O cannot be forcibly interrupted. Caller I/O needs actual deadlines. Output is streamed with limit/count/hash wrappers and at most33 bytes of prefix buffering, without a full output buffer. [Operations](../docs/operations/operability-performance.md#public-image-resource-profile) owns bounds, allocation accounting and opt-in cold observations; [Lifecycle](../docs/lifecycle/lifecycle-and-compatibility.md#public-image-successor-boundary) owns version/recovery implications.

Normative interpretation follows [PNG3](https://www.w3.org/TR/png-3/), [JPEG T.81](https://www.w3.org/Graphics/JPEG/itu-t81.pdf), [JFIF T.871](https://www.itu.int/rec/T-REC-T.871-201105-I/en), [Exif3.1](https://www.cipa.jp/std/documents/download_e.html?CIPA_DC-008-2026-E), [DCF2.0](https://www.jeita.or.jp/cgi-bin/standard_e/pdf.cgi?jk_n=51&jk_pdf_file=CP) and [Adobe5116](https://pdfa.org/wp-content/uploads/2020/07/5116.DCT_Filter.pdf), restricted to this named profile. New interpretation/format support requires a reviewed profile change and evidence, rather than silently accepting metadata that native codecs ignore.

## Durable lifecycle and unknown outcomes

The immutable `001_media.sql` and its original checksum are retained. `002_common_formats.sql` adds the finite MIME/opaque-dimension constraints without rewriting retained metadata or touching asset bytes. `003_private_svg.sql` adds only the explicitly selected SVG MIME to the stored attachment constraint; dimensions/read/lifecycle rules and retained metadata/original bytes stay unchanged. Explicit `Migrate` installs all three on a fresh database or advances an exact retained v1/v2 ledger under one transaction and transaction advisory lock. The ordered ledger must be a nonempty exact prefix for an existing schema; missing, unknown, reordered or changed entries fail before migration effects. DDL and ledger insertion roll back together on failure. Concurrent fresh installation/upgrade and repeated exact installation are serialized and idempotent.

Startup checks the existing environment, complete current ledger and required columns; a v1/v2-only database is unavailable to this source until explicitly upgraded. Startup never migrates and does not continuously detect privileged database/storage tampering. Published v0.2 source expects the exact two-entry ledger and rejects schema version 3 even if no SVG is stored; older v1-only source also rejects it. This is a next-v0-minor schema boundary, not a compatible v0.2 patch. Stop all old writers/instances before explicit upgrade; overlapping v0.2/current source is unsupported. Reverting source is not data rollback: retain coherent metadata plus asset recovery coverage and use an explicit reviewed restore/forward path.

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


The opt-in common-profile admission benchmark on the same Go/Linux/EPYC development profile uses complete owned PDF/ZIP/DOCX/WebM/MP3 files and a warm local file. Disabled types read only eight bytes: about 2.1–2.2 µs, 40 B and two allocations per admission, with no MIME detection or 4096-byte prefix allocation. Enabled recognition reads up to 4096 bytes: about 5.1–7.7 µs, 4,216 B and five allocations. These observations include file seeks/reads and exact MIME parsing; they are not pure detector or network throughput.

TXT/CSV require an additional whole-file UTF-8 scan. Maximum 10 MiB synthetic ASCII files measured about 87–94 ms and 37 KiB allocated per admission (seven to eleven allocations), using a fixed reader buffer. This distinct cost is not represented by the prefix-only opaque-format measurements. Reproduce with `GOMAXPROCS=2 go test ./media -run '^$' -bench 'Benchmark(CommonRecognition|WholeUTF8Admission)$' -benchmem -benchtime=300ms -count=1`. The full real-PostgreSQL Media race suite completed in 6.9 seconds within the 90-second package timeout; one warm Go/test invocation observed approximately 207 MiB maximum child RSS, including test/toolchain allocations rather than a production resource limit.
