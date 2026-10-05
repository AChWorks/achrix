# Lifecycle and Compatibility

## Ownership and versions

Published Foundation dependencies, independently distributed Modules and product releases have immutable version/build identity tied to source and documented compatibility. Use semantic versions where accurate; a version number alone is not the contract. Component metadata follows the [source-backed implementation-version contract](../architecture/contracts-and-interfaces.md#capability-abi-and-composition); retain the exact product/artifact/source identity alongside any development or replacement label.

Products own activation/deployment. Foundation/Modules provide their compatibility information, migrations and recovery constraints. A dependency update may affect runtime, enabled Modules, configuration/defaults, schema/data and Application/API/event contracts. Do not update products by manually recopying runtime source or build a universal updater before multiple real distributions share the mechanics.

## Install and upgrade

Installation is reproducible, explicit about runtime/database/extensions, writable paths and secrets, safe to re-enter where practical, and verified by local health/readiness. Reject unsupported environments. The supported install path should have one obvious documented entry, perform bounded preflight/compatibility checks before mutation, distinguish generated/default configuration from secrets, and fail with actionable recovery guidance rather than partial hidden state. Routine operators should not need to understand AChrix internals to determine whether setup succeeded.

Distinguish compatible updates, feature evolution, breaking upgrades, schema migrations, Module/capability lifecycle and runtime/dependency changes. Before a stateful change, check only relevant target compatibility, environment/extension versions, enabled capabilities, migration prerequisites, storage, keys, maintenance needs and recoverability. Module or supported optional-subsystem enable/disable/removal has explicit dependency, migration and data-retention semantics. **Disable/re-enable is not removal:** disabling an active path does not implicitly reverse migrations or delete durable state, and re-enabling must validate compatibility with retained state. Destructive cleanup/removal is a separate authorized lifecycle action with its own dependency/recovery consequences. Dynamic plugin lifecycle waits for a real need.

Migrations are production behavior: preserve domain invariants, consider overlapping old/new code, use expand/migrate/contract where safer, handle interruptions/re-entry, expose failure state and verify postflight invariants. Database-specific migrations are allowed for deliberately required capabilities.

Modules own immutable published migrations with unambiguous identity; product composition selects their compatible set/order. Serialize execution for the database scope as an explicit install/deploy step, rather than racing requests/replicas. Prove the ledger/locking mechanism; no new migration service is required.

## Compatibility and recovery

Prefer additive public contract change. Breaking versions define migration and an appropriate compatibility/retirement window with supported-combination tests. Durable async payloads stay readable for their required lifetime.

Choose activation/rollback mechanics for the actual deployment risk; versioned artifacts and reversible activation may help but are not universal. Identify rollback, roll-forward, restore, compensation or manual reconciliation. Reverting files alone does not make data or external effects reversible.

## Backup and recovery

Core supplies only the necessary lifecycle/composition identity and readiness contracts. Modules identify actual durable assets and recovery dependencies; the product owns backup scope, consistency, policy, authorization, RPO/RTO/retention and the supported restore profile. Maintained [database](https://www.postgresql.org/docs/current/backup.html), storage and deployment tools perform capture and restoration.

Backup & Recovery is an accepted reusable operational Module/tooling boundary under the [Module model](../architecture/module-model.md#backup--recovery). It may coordinate reusable operation state, manifests/identity/integrity metadata, declared asset dependencies, preflight/verification and narrow adapters to maintained tools. It does **not** create a custom database backup format, universal schedule/destination policy, generic shell/root/filesystem/database-proxy authority or a Core backup engine. A real product/deployment recovery proof must establish reusable mechanics before a general implementation is justified; current execution state belongs to GitHub work items, not this lifecycle rule.

Support whole-product and database-only/files-or-object-data-only backups when the declared profile can restore them safely. Record coverage/exclusions, source product/Core/Module/schema identities, capture point/consistency method, integrity evidence and required configuration, database roles/extensions and protected key references. A data export is not automatically a complete disaster-recovery backup. Cache/search projections may be rebuilt only when their authoritative source survives.

Separate component schedules/formats are valid, but combining captures taken near the same time does not prove consistency. Use a demonstrated snapshot/write-quiescence/versioning or reconciliation strategy for cross-store references. Reject unsupported partial/Module restore unless its owned restore contract preserves relationships and domain invariants; shared tables do not imply independently restorable Modules.

Choose logical, physical/base, continuous/WAL/PITR, snapshot or managed-provider mechanisms against product recovery objectives: acceptable data loss (RPO), restore time (RTO), scale, compatible tooling and total cost. Do not copy a running database's data directory as a generic file backup. No universal tool, schedule or recovery-time promise is selected here; supported claims come from rehearsed restore evidence on the declared deployment profile.

Protect backup access and transport/storage, keep recoverable key material separately controlled, and retain usable copies beyond the production host's failure scope. Define retention, resource bounds and actionable failure status; backups are not publicly served files. Verify trusted input, integrity, coverage and target compatibility before destructive restore.

Rehearse restoration into an isolated supported target, verifying schema, critical data/file relationships, application identity and readiness; record achieved recovery limits. Restore operations obey normal authorization/audit and serialize with conflicting lifecycle operations. Reconcile credentials/revocations and external effects before resuming jobs; restoring old local state must not blindly replay completed payments/messages or erase retained audit evidence.

Backup existence is not restore evidence. Prove the needed profile on real persisted behavior, then on the first product, before claiming supported recovery.

## Initial fixture lifecycle and database scope

The v0.2 [Identity](../../identity/README.md) and [Audit](../../audit/README.md) Modules add separate owned immutable migration ledgers. Product installation migrates them explicitly before Core startup; startup verifies compatible existing state. Atomic Identity/Audit history belongs to one database consistency boundary and must be captured/restored together. Full validation proves a quiescent native PostgreSQL capture, exact retained rows/ledger identity and public contracts after restore. It does not establish or authorize a production recovery profile. Retain the source/configuration/migration identities and protect credential/session hashes; after real recovery, reconcile credentials and revoke restored sessions before ingress. No destructive retention API or independent partial-schema restore is supported by this first slice.

The proving consumer installs its one immutable SQL migration through an explicit `notes -mode migrate` operation before serving traffic. Its private installer uses native PostgreSQL transactional DDL, a transaction advisory lock and an ID/SHA-256 ledger. Schema and ledger commit together; failure/interruption rolls back both, concurrent installers serialize, changed/unknown published identities fail, and repeat execution is harmless. Runtime startup checks the exact ledger; it does not execute migrations from requests or replicas. No down migration, universal migration engine or Backup & Recovery implementation is published by this fixture. The reusable recovery ownership boundary remains evidence-driven until a real product/deployment proves mechanics worth sharing. The native one-file mechanism is a bounded baseline use of maintained database functionality; reconsider a broader migration tool when real schema evolution needs it.

The original Notes-only sub-proof in `scripts/validate.sh` demonstrates trusted `pg_dump`/`pg_restore` into another empty database in a task-owned PostgreSQL 18.6 cluster. It checks dump integrity, exact persisted row equality, migration identity, reconstructed composition, local readiness and authorized Application reads. Scope is this fixture's ordinary relational schema/ledger/notes only. Source Foundation pin, consumer build, Module version and migration digest are identified in the validation output. Runtime configuration/tokens, files/object data, roles/ACLs, external effects, whole-product consistency, off-host retention and recovery-time guarantees are excluded. The dump is generated within the test, never accepted from an untrusted party or restored over user data. Real-product deployment/recovery support requires separate product evidence and authorization.

The same full command also proves the current [Media consumer profile](../../media/README.md#capture-and-restore-boundary): ingress and participating Modules stop before a coherent capture of the participating Identity/Audit/Media database and the exact private asset tree. Trusted task-generated database/file archives and a file manifest have integrity checks; restoration into isolated empty targets verifies retained rows, immutable migration ledgers, private file identity/permissions/hashes and authorized public reads. This is a bounded quiesced fixture proof, not a live snapshot, independently supported partial-schema/files-only restore or real-product backup policy. Protected runtime configuration/keys, roles/ACLs, external-effect reconciliation, off-host retention and RPO/RTO remain outside this proof. Real deployment/recovery implementation remains separately gated by its product profile and active authorized work.

## Product update experience

For products with an administration UI, routine compatible Core/Module installation/update is a simple authorized action without user-run server commands or compilation. [Consumption](../architecture/consumption-and-packaging.md#module-installation) owns prepared composition; choosing independently available latest versions is insufficient.

Check compatibility/impact; authorize the exact candidate; verify/stage it and establish recovery; serialize migration/activation; verify active identity/health. Preserve configuration, secrets and data. Show durable progress, current component/version identity and actionable failures; retries/resume must not duplicate effects. A user returning after interruption must be able to distinguish active/succeeded/failed/recovery-required state instead of guessing from a spinner or process exit. Breaking, downtime or destructive changes require the relevant explicit decision.

The deployment profile supplies bounded activation that survives application replacement/restart, using an appropriate platform API or narrowly authorized local mechanism. Core and Gateway Bridge gain no generic shell/root/OS authority. Initial provisioning may be operator-owned; routine updates are product-driven only for validated profiles.

Reject activation on delivery/trust failure. Verify publisher/artifact identity, component compatibility and metadata safety under the artifact rules below, including stale metadata/older vulnerable candidates. Automatic rollback requires compatibility with current data; otherwise retain evidence and use declared recovery. Source rollback is not data rollback.

Prove this on the first real product before sharing updater tooling. Only this contract exists now. Evaluate maintained mechanisms against the [TUF threat model](https://theupdateframework.io/docs/security/) when implementing update trust; no TUF service, registry, helper or UI is built today.

## Initial development release line

The initial development line was `v0.1.x`, beginning with `v0.1.0`; GitHub Releases own its immutable publication evidence and any maintained-line or retirement declaration. It established the first source Foundation dependency for developing a real consumer/extension, not a stable-v1 API or a product deployment distribution. [Contracts](../architecture/contracts-and-interfaces.md#initial-go-public-surface) owns the supported public surface and [Operations](../operations/operability-performance.md#supported-environment) owns the tested environment. PostgreSQL/pgx persistence in Notes is consumer-owned; Core has only standard-library dependencies.

Published `v0.1.x` patches remain compatible with that line's public surface/capability semantics. A required breaking development change uses a new `v0` minor line with explicit compatibility/migration notes; capability ABI revision is a distinct contract identity. Successor, maintenance, and retirement declarations belong in release evidence rather than this historical section; no permanent LTS/backport promise is implied. Never overwrite a published version; correct defects through a new reviewed version.

Consumers pin a reviewed version, inspect the dependency/contract diff, validate affected behavior and deliberately rebuild/deploy. A source release does not activate an installed product. Whole-product recovery, remote deployment, administrator update UI, product-level Multi-Site isolation and Gateway integration require their own product/module evidence and authorized implementation; this lifecycle guide does not mirror live work-item status. Unicode storage is proven by Notes, while localized UI/RTL rendering follows [Web](../web/seo-and-semantic-web.md#internationalization-and-directionality) when a real renderer exists.

## v0.1 to v0.2 development minor migration

The Core correction in [Issue #44](https://github.com/AChWorks/achrix/issues/44) belongs to **v0.2**, not a compatible v0.1 patch. These migration steps apply when an existing v0.1 consumer adopts v0.2. [GitHub Releases](https://github.com/AChWorks/achrix/releases) owns actual publication and exact evidence. This historical migration introduced the v0.2 developer line; it did not retire v0.1 or alter that line's patch compatibility commitment. The published v0.1 tag/source is unchanged; no permanent LTS/backport promise is introduced.

Consumers updating to this source must:

- Replace positional `Descriptor` literals with keyed fields; its new `Optional []Capability` field changes struct shape. Nil/empty optional declarations keep required-only composition. A present optional provider participates in startup ordering and must match its exact ABI; absence is valid.
- Update requirements for Core's `achrix.authorization` from ABI 1 to **ABI 2** after adopting the new authorization contract. Revision 2 cannot silently satisfy revision 1. Unrelated compatible Module capabilities retain their ABI revisions; implementation release numbers do not imply a capability ABI bump.
- Return `ErrDenied` (or a wrapped form) for explicit Policy denial. A generic policy error now means safe, inspectable `ErrAuthorizationUnavailable`, not 403. Actual operation cancellation/deadline remains a context error. Map unavailable separately; do not automatically replay a domain mutation.
- Supply a deadline for every `Authorize` and `Ready` call, including direct callers. Missing authorization deadlines fail closed as unavailable without policy evaluation. Policy implementations must be concurrent-safe and promptly honor cancellation.
- Stop ingress and drain product-owned domain work before shutdown. Core cancels/drains its admitted readiness/policy callbacks before stopping Modules, within one shutdown waiting/drain/stop budget. A wait/drain timeout leaves admission closed; retry Shutdown only to complete cleanup after callbacks return. Startup cancellation uses fresh bounded partial-start cleanup. Completed cleanup's result is retained; repeated calls do not retry failed Module stops. Arbitrary noncompliant in-process code still requires operator/process recovery.
- Cheaply bound/normalize untrusted resource references at the owning Application before expensive policy work without reading authorization-sensitive state. The Notes fixture now preflights its opaque ID; create's explicit global scope remains valid.

The isolated Notes consumer pins the reviewed source revision through ordinary Go module resolution/checksums; its schema and immutable migration are unchanged. Validate the complete candidate with Core race tests and isolated PostgreSQL/consumer/restore proof. Release preparation uses the exact integrated source and separately verified Lifecycle evidence; #44 itself required integration only. Historical v0.2 installation remains recoverable from its immutable tag and release.

## v0.2 to v0.3 development minor migration

`v0.3.0` is the reviewed pre-v1 developer minor after published `v0.2.0`; [GitHub Releases](https://github.com/AChWorks/achrix/releases) owns actual publication and exact release evidence. It does not rewrite or retire v0.1/v0.2. A consumer adopting v0.3.0 must deliberately inspect/rebuild its product and apply every participating migration before ingress.

Compared with v0.2.0:

- Media schema version 3 adds explicitly selected private SVG support. Quiesce old writers, preserve a coherent database/private-original capture, run explicit Media migration with a deadline, then start only the new composition. Published v0.2 source rejects the newer ledger; source downgrade is not database rollback.
- Identity/Audit/Media exported `Config` structs add bounded resource fields. Replace positional literals with keyed fields before compiling against v0.3.0; zero retains documented defaults and never means unlimited.
- Media adds separately authorized `achrix.media.prepare-public-image` ABI 1 for clean PNG/JPEG preparation. It grants neither original `Read` nor publication, derivative storage or public URLs.
- The optional `achrix.multisite.resolve` ABI 1 resolver becomes available in the shared Foundation release. Single-site composition remains valid without it; ingress trust and all site-bound authorization/data/storage/configuration remain product-owned.
- Media and Identity/Audit dependency-error boundaries now return only documented safe categories/context identities instead of retaining private provider/destination wrapper text or objects. Consumers must not depend on driver-specific unwrap chains as public API. Existing mutation acknowledgement/reconciliation semantics and capability ABIs are unchanged.

The [product quick start](../architecture/consumption-and-packaging.md#start-a-product) owns ordinary versioned installation. Updating a Go dependency alone never migrates a database or replaces a deployed product artifact.

## Private SVG v0.3 schema boundary

The explicitly selected private SVG attachment slice in [Issue #78](https://github.com/AChWorks/achrix/issues/78) belongs to the **v0.3.0** source line after published v0.2.0. It does not change the published v0.1/v0.2 tags or their compatibility commitments, and source publication still does not activate any product. Nil Media configuration remains PNG/JPEG and `CommonMIMEs()` retains its 36 types; SVG requires explicit `image/svg+xml` selection.

Media schema version 3 adds immutable `003_private_svg.sql` after byte-identical 001/002. Before adopting this source, quiesce all participating product instances and preserve a coherent PostgreSQL metadata/ledger plus private-original capture. Explicitly migrate with a deadline, then start the new composition and verify retained reads; startup never upgrades. Fresh install, retained v1/v2 upgrade and rollback of a failed upgrade transaction are tested on the supported PostgreSQL profile. Overlapping old/new source is unsupported: published v0.2 source rejects the newer three-entry ledger, even when SVG has never been enabled.

Source rollback is **not database rollback**. Removing SVG from new-upload configuration preserves authorized reads of retained SVG. Reverting source alone cannot reverse the ledger or remove durable originals. Recover through the reviewed forward source or restore the pre-upgrade coherent database-plus-assets capture into an isolated compatible target, validating retained relationships and authorized reads before ingress. Such a restore loses changes made after its capture; no automatic down migration or removal of SVG state is supplied. [Media](../../media/README.md#capture-and-restore-boundary) owns exact bounds and coherent original-file recovery obligations.

## Resource configuration v0.3 boundary

The Identity/Audit/Media `Config.MaxConns int32` and `Config.MaxOperations int` additions in [Issue #76](https://github.com/AChWorks/achrix/issues/76) belong to the **v0.3.0** source line after published v0.2.0. Exported struct shape changes require replacing positional Config literals with keyed fields before adopting the source, for example `identity.Config{MaxConns: 6, MaxOperations: 24}` or `media.Config{StorageRoot: privatePath}`. Omitted/zero and explicit-default resource fields preserve the existing default behavior; zero never means unlimited. [Operations](../operations/operability-performance.md#module-resource-configuration) owns the exact default/minimum and lease semantics.

This configuration addition changes no schema, migration bytes/checksums or capability ABI, and is not part of a compatible v0.2 patch. The same source also carries the private SVG schema boundary above, whose explicit migration/recovery requirements still apply when upgrading from published v0.2.0. Published older tags remain unchanged; a source release never implies product or production activation. Pool/admission limits are instance runtime policy, not database identity or a fleet capacity promise.

## Public-image v0.3 boundary

The stateless [Media preparation API](../../media/README.md#clean-public-image-preparation) in [Issue #77](https://github.com/AChWorks/achrix/issues/77) belongs to the same **v0.3.0** source line. It adds exact-asset `achrix.media.prepare-public-image` ABI1 without changing original `Read` ABI1, private upload defaults, existing durable state or immutable migrations. Published tags remain immutable; no release or product activation is implied. Unsupported preparation inputs retain their existing private attachment semantics.

Preparation grants no publication and stores no derivative. Its complete result binds a source snapshot only; the product owns future source/revision/hash and permission checks, staged artifact activation, relationships, withdrawal/cache invalidation and retention. A product capture must cover its active representations plus those source relationships/manifests or a reproducible compatible regeneration path. Restoring originals alone does not establish correctness of an already published artifact. Partial private output is discarded on failure and never activated. New source still includes the separate SVG schema3 upgrade boundary above; reverting source is not data rollback or automatic withdrawal of product-owned public artifacts.

## Source release preparation and verification

`.github/workflows/release.yml` is a manually dispatched preparer on canonical trusted `main`. It requires successful existing `baseline` evidence for that exact source, exports its tracked tree, and produces a source archive, SPDX source inventory, source/run identity metadata, checksums and signed build/SBOM bundles. The archive includes repository documentation, fixtures and tooling; it ships no product executable or vendored dependency implementation. The SBOM catalogs that exported source/declared manifest scope, not a deployed runtime or tested compatibility for every listed dependency.

Preparation has no tag/release publication permission, runs no PostgreSQL or repeated runtime suite and gives Core no signing dependency. A maintainer separately reviews and publishes the exact verified assets/source identity. PR/main checks remain required when preparation inputs change. Official verification expects this repository's hosted `release.yml` on `refs/heads/main`, the independently approved source SHA and GitHub's OIDC issuer `https://token.actions.githubusercontent.com`; a matching checksum alone is insufficient. A changed publisher/workflow trust boundary needs the normal owner decision. Retain downloaded bundles for verification; trust-root/issuer revocation and offline verification follow the maintained GitHub/Sigstore verifier rather than a custom key service.

From a maintainer environment with a GitHub CLI supporting artifact-attestation verification:

```bash
ACHRIX_RELEASE_VERSION="<reviewed-unpublished-version>"
gh workflow run release.yml --repo AChWorks/achrix --ref main -f version="$ACHRIX_RELEASE_VERSION"
gh run list --repo AChWorks/achrix --workflow release.yml --branch main --limit 5 --json databaseId,headSha,status,conclusion
```

Identify the successful preparation run and independently reviewed source commit as `ACHRIX_RELEASE_RUN` and `ACHRIX_RELEASE_SHA`; do not trust downloaded metadata to choose its own authorized source. Download into an empty task-owned directory:

```bash
gh run download "$ACHRIX_RELEASE_RUN" --repo AChWorks/achrix --name "achrix-$ACHRIX_RELEASE_VERSION-release" --dir release-assets
cd release-assets
sha256sum -c SHA256SUMS
for asset in "achrix-$ACHRIX_RELEASE_VERSION-source.tar.gz" "achrix-$ACHRIX_RELEASE_VERSION-source.spdx.json" release.json; do
  gh attestation verify "$asset" --repo AChWorks/achrix --bundle provenance.sigstore.json \
    --signer-workflow AChWorks/achrix/.github/workflows/release.yml \
    --source-ref refs/heads/main --source-digest "$ACHRIX_RELEASE_SHA" --deny-self-hosted-runners
done
gh attestation verify "achrix-$ACHRIX_RELEASE_VERSION-source.tar.gz" --repo AChWorks/achrix \
  --bundle sbom.sigstore.json --predicate-type https://spdx.dev/Document/v2.3 \
  --signer-workflow AChWorks/achrix/.github/workflows/release.yml \
  --source-ref refs/heads/main --source-digest "$ACHRIX_RELEASE_SHA" --deny-self-hosted-runners
```

Verify metadata/source-tree identity against the approved commit and source archive against its Git export. Refresh tag/release absence and source/review/CI identities before publishing; do not overwrite an existing version or blindly repeat an ambiguous write. Create the exact-source tag and stage a draft Release with these assets, matching covered source and compatibility/evidence notes. Prove normal isolated `github.com/AChWorks/achrix@<version>` resolution/checksums and reported `Version()` without replacement/workspace/source copying before publishing the draft. Missing/failed delivery proof keeps the release work open; source publication is separate from any product deployment. After publication, re-download and verify the released assets and tag/source identity. [GitHub attestation verification](https://cli.github.com/manual/gh_attestation_verify) owns the maintained CLI contract.

## Official artifacts and future tooling

When publishable official artifacts exist, bind artifact digest and source/version, checksums, SBOM and build provenance to cryptographic authenticity evidence. Select standard tooling then; this policy adds no signing service or Core runtime dependency.

Verification defines authorized signer/issuer, trust anchors, rotation/revocation and pinned/offline verification where appropriate. Private signing material stays in an approved secret system. Checksums alone prove no official origin; signatures do not establish safety, compatibility or authorization.

Official Modules can publish equivalent evidence plus compatibility/certification results and affected-version security advisories. Future registry/advisory/update locations belong to release/discovery tooling, not hardcoded business URLs or mandatory online boot checks. An actual updater must handle unavailable/stale metadata and revocation safely.

Third-party/unsigned artifacts remain usable under explicit consumer trust and licenses; registry claims cannot create official status or bypass Application permissions. Forks may reuse licensed tooling/docs while maintaining their own publisher/trust identity, compatibility and security support. Do not deliberately break interoperability to impose separation.
