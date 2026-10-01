# Lifecycle and Compatibility

## Ownership and versions

Published Foundation dependencies, independently distributed Modules and product releases have immutable version/build identity tied to source and documented compatibility. Use semantic versions where accurate; a version number alone is not the contract.

Products own activation/deployment. Foundation/Modules provide their compatibility information, migrations and recovery constraints. A dependency update may affect runtime, enabled Modules, configuration/defaults, schema/data and Application/API/event contracts. Do not update products by manually recopying runtime source or build a universal updater before multiple real distributions share the mechanics.

## Install and upgrade

Installation is reproducible, explicit about runtime/database/extensions, writable paths and secrets, safe to re-enter where practical, and verified by local health/readiness. Reject unsupported environments. The supported install path should have one obvious documented entry, perform bounded preflight/compatibility checks before mutation, distinguish generated/default configuration from secrets, and fail with actionable recovery guidance rather than partial hidden state. Routine operators should not need to understand AChrix internals to determine whether setup succeeded.

Distinguish compatible updates, feature evolution, breaking upgrades, schema migrations, module lifecycle and runtime/dependency changes. Before a stateful change, check only relevant target compatibility, environment/extension versions, enabled capabilities, migration prerequisites, storage, keys, maintenance needs and recoverability. Module enable/disable/removal has explicit dependency, migration and data-retention semantics; dynamic plugin lifecycle waits for a real need.

Migrations are production behavior: preserve domain invariants, consider overlapping old/new code, use expand/migrate/contract where safer, handle interruptions/re-entry, expose failure state and verify postflight invariants. Database-specific migrations are allowed for deliberately required capabilities.

Modules own immutable published migrations with unambiguous identity; product composition selects their compatible set/order. Serialize execution for the database scope as an explicit install/deploy step, rather than racing requests/replicas. Prove the ledger/locking mechanism; no new migration service is required.

## Compatibility and recovery

Prefer additive public contract change. Breaking versions define migration and an appropriate compatibility/retirement window with supported-combination tests. Durable async payloads stay readable for their required lifetime.

Choose activation/rollback mechanics for the actual deployment risk; versioned artifacts and reversible activation may help but are not universal. Identify rollback, roll-forward, restore, compensation or manual reconciliation. Reverting files alone does not make data or external effects reversible.

## Backup and recovery

Core supplies only the necessary lifecycle/composition identity and readiness contracts. Modules identify actual durable assets and recovery dependencies; the product owns backup scope, consistency, policy, authorization and restore coordination. Maintained [database](https://www.postgresql.org/docs/current/backup.html), storage and deployment tools perform capture and restoration. A shared Backup Module/tool needs real reusable mechanics before promotion; no backup engine or host authority is required in Core.

Support whole-product and database-only/files-or-object-data-only backups when the declared profile can restore them safely. Record coverage/exclusions, source product/Core/Module/schema identities, capture point/consistency method, integrity evidence and required configuration, database roles/extensions and protected key references. A data export is not automatically a complete disaster-recovery backup. Cache/search projections may be rebuilt only when their authoritative source survives.

Separate component schedules/formats are valid, but combining captures taken near the same time does not prove consistency. Use a demonstrated snapshot/write-quiescence/versioning or reconciliation strategy for cross-store references. Reject unsupported partial/Module restore unless its owned restore contract preserves relationships and domain invariants; shared tables do not imply independently restorable Modules.

Choose logical, physical, continuous/WAL or managed-provider mechanisms against product recovery objectives: acceptable data loss (RPO), restore time (RTO), scale, compatible tooling and total cost. Do not copy a running database's data directory as a generic file backup. No universal tool, schedule or recovery-time promise is selected here.

Protect backup access and transport/storage, keep recoverable key material separately controlled, and retain usable copies beyond the production host's failure scope. Define retention, resource bounds and actionable failure status; backups are not publicly served files. Verify trusted input, integrity, coverage and target compatibility before destructive restore.

Rehearse restoration into an isolated supported target, verifying schema, critical data/file relationships, application identity and readiness; record achieved recovery limits. Restore operations obey normal authorization/audit and serialize with conflicting lifecycle operations. Reconcile credentials/revocations and external effects before resuming jobs; restoring old local state must not blindly replay completed payments/messages or erase retained audit evidence.

Backup existence is not restore evidence. Prove the needed profile on real persisted behavior, then on the first product, before claiming supported recovery.

## Initial fixture lifecycle and database scope

The proving consumer installs its one immutable SQL migration through an explicit `notes -mode migrate` operation before serving traffic. Its private installer uses native PostgreSQL transactional DDL, a transaction advisory lock and an ID/SHA-256 ledger. Schema and ledger commit together; failure/interruption rolls back both, concurrent installers serialize, changed/unknown published identities fail, and repeat execution is harmless. Runtime startup checks the exact ledger; it does not execute migrations from requests or replicas. No down migration, universal migration engine or shared Backup Module is published. The native one-file mechanism is a bounded baseline use of maintained database functionality; reconsider a broader migration tool when real schema evolution needs it.

`scripts/validate.sh` demonstrates trusted `pg_dump`/`pg_restore` into another empty database in a task-owned PostgreSQL 18.6 cluster. It checks dump integrity, exact persisted row equality, migration identity, reconstructed composition, local readiness and authorized Application reads. Scope is this fixture's ordinary relational schema/ledger/notes only. Source Foundation pin, consumer build, Module version and migration digest are identified in the validation output. Runtime configuration/tokens, files/object data, roles/ACLs, external effects, whole-product consistency, off-host retention and recovery-time guarantees are excluded. The dump is generated within the test, never accepted from an untrusted party or restored over user data. Issue #19 retains the real-product/deployment/recovery-profile gate.

## Product update experience

For products with an administration UI, routine compatible Core/Module installation/update is a simple authorized action without user-run server commands or compilation. [Consumption](../architecture/consumption-and-packaging.md#module-installation) owns prepared composition; choosing independently available latest versions is insufficient.

Check compatibility/impact; authorize the exact candidate; verify/stage it and establish recovery; serialize migration/activation; verify active identity/health. Preserve configuration, secrets and data. Show durable progress, current component/version identity and actionable failures; retries/resume must not duplicate effects. A user returning after interruption must be able to distinguish active/succeeded/failed/recovery-required state instead of guessing from a spinner or process exit. Breaking, downtime or destructive changes require the relevant explicit decision.

The deployment profile supplies bounded activation that survives application replacement/restart, using an appropriate platform API or narrowly authorized local mechanism. Core and Gateway Bridge gain no generic shell/root/OS authority. Initial provisioning may be operator-owned; routine updates are product-driven only for validated profiles.

Reject activation on delivery/trust failure. Verify publisher/artifact identity, component compatibility and metadata safety under the artifact rules below, including stale metadata/older vulnerable candidates. Automatic rollback requires compatibility with current data; otherwise retain evidence and use declared recovery. Source rollback is not data rollback.

Prove this on the first real product before sharing updater tooling. Only this contract exists now. Evaluate maintained mechanisms against the [TUF threat model](https://theupdateframework.io/docs/security/) when implementing update trust; no TUF service, registry, helper or UI is built today.

## Initial development release line

The initial maintained development line is `v0.1.x`, beginning with `v0.1.0`; GitHub Releases own actual publication state. It is a source Foundation dependency for developing a real consumer/extension, not a stable-v1 API or a product deployment distribution. [Contracts](../architecture/contracts-and-interfaces.md#initial-go-public-surface) owns the supported public surface and [Operations](../operations/operability-performance.md#supported-environment) owns the tested environment. PostgreSQL/pgx persistence in Notes is consumer-owned; Core has only standard-library dependencies.

Keep patches within `v0.1.x` compatible with the published public surface/capability semantics. A required breaking development change uses a new `v0` minor line with explicit compatibility/migration notes; capability ABI revision is a distinct contract identity. Maintain the latest patch of this initial line during first-consumer development, and announce a successor or retirement in release notes rather than implying permanent LTS/backports. Never overwrite a published version; correct defects through a new reviewed version.

Consumers pin a reviewed version, inspect the dependency/contract diff, validate affected behavior and deliberately rebuild/deploy. A source release does not activate an installed product. Whole-product recovery, remote deployment, administrator update UI, Multi-Site and Gateway Bridge remain unproven; Issue #19 and their owning product/module decisions retain those obligations. Unicode storage is proven by Notes, while localized UI/RTL rendering follows [Web](../web/seo-and-semantic-web.md#internationalization-and-directionality) when a real renderer exists.

## Source release preparation and verification

`.github/workflows/release.yml` is a manually dispatched preparer on canonical trusted `main`. It requires successful existing `baseline` evidence for that exact source, exports its tracked tree, and produces a source archive, SPDX source inventory, source/run identity metadata, checksums and signed build/SBOM bundles. The archive includes repository documentation, fixtures and tooling; it ships no product executable or vendored dependency implementation. The SBOM catalogs that exported source/declared manifest scope, not a deployed runtime or tested compatibility for every listed dependency.

Preparation has no tag/release publication permission, runs no PostgreSQL or repeated runtime suite and gives Core no signing dependency. A maintainer separately reviews and publishes the exact verified assets/source identity. PR/main checks remain required when preparation inputs change. Official verification expects this repository's hosted `release.yml` on `refs/heads/main`, the independently approved source SHA and GitHub's OIDC issuer `https://token.actions.githubusercontent.com`; a matching checksum alone is insufficient. A changed publisher/workflow trust boundary needs the normal owner decision. Retain downloaded bundles for verification; trust-root/issuer revocation and offline verification follow the maintained GitHub/Sigstore verifier rather than a custom key service.

From a maintainer environment with a GitHub CLI supporting artifact-attestation verification:

```bash
ACHRIX_RELEASE_VERSION=v0.1.0
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
