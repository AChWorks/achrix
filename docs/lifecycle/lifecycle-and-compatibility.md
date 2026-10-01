# Lifecycle and Compatibility

## Ownership and versions

Published Foundation dependencies, independently distributed Modules and product releases have immutable version/build identity tied to source and documented compatibility. Use semantic versions where accurate; a version number alone is not the contract.

Products own activation/deployment. Foundation/Modules provide their compatibility information, migrations and recovery constraints. A dependency update may affect runtime, enabled Modules, configuration/defaults, schema/data and Application/API/event contracts. Do not update products by manually recopying runtime source or build a universal updater before multiple real distributions share the mechanics.

## Install and upgrade

Installation is reproducible, explicit about runtime/database/extensions, writable paths and secrets, safe to re-enter where practical, and verified by local health/readiness. Reject unsupported environments.

Distinguish compatible updates, feature evolution, breaking upgrades, schema migrations, module lifecycle and runtime/dependency changes. Before a stateful change, check only relevant target compatibility, environment/extension versions, enabled capabilities, migration prerequisites, storage, keys, maintenance needs and recoverability. Module enable/disable/removal has explicit dependency, migration and data-retention semantics; dynamic plugin lifecycle waits for a real need.

Migrations are production behavior: preserve domain invariants, consider overlapping old/new code, use expand/migrate/contract where safer, handle interruptions/re-entry, expose failure state and verify postflight invariants. Database-specific migrations are allowed for deliberately required capabilities.

Modules own immutable published migrations with unambiguous identity; product composition selects their compatible set/order. Serialize execution for the database scope as an explicit install/deploy step, rather than racing requests/replicas. Prove the ledger/locking mechanism; no new migration service is required.

## Compatibility and recovery

Prefer additive public contract change. Breaking versions define migration and an appropriate compatibility/retirement window with supported-combination tests. Durable async payloads stay readable for their required lifetime.

Choose activation/rollback mechanics for the actual deployment risk; versioned artifacts and reversible activation may help but are not universal. Identify rollback, roll-forward, restore, compensation or manual reconciliation. Reverting files alone does not make data or external effects reversible.

Backup existence is not restore evidence. Material recovery includes database/files/object data, encryption/signing keys, runtime configuration, version identity and a credible restore procedure.

## Product update experience

For products with an administration UI, routine compatible Core/Module installation/update is a simple authorized action without user-run server commands or compilation. [Consumption](../architecture/consumption-and-packaging.md#module-installation) owns prepared composition; choosing independently available latest versions is insufficient.

Check compatibility/impact; authorize the exact candidate; verify/stage it and establish recovery; serialize migration/activation; verify active identity/health. Preserve configuration, secrets and data. Show durable progress and actionable failures; retries/resume must not duplicate effects. Breaking, downtime or destructive changes require the relevant explicit decision.

The deployment profile supplies bounded activation that survives application replacement/restart, using an appropriate platform API or narrowly authorized local mechanism. Core and Gateway Bridge gain no generic shell/root/OS authority. Initial provisioning may be operator-owned; routine updates are product-driven only for validated profiles.

Reject activation on delivery/trust failure. Verify publisher/artifact identity, component compatibility and metadata safety under the artifact rules below, including stale metadata/older vulnerable candidates. Automatic rollback requires compatibility with current data; otherwise retain evidence and use declared recovery. Source rollback is not data rollback.

Prove this on the first real product before sharing updater tooling. Only this contract exists now. Evaluate maintained mechanisms against the [TUF threat model](https://theupdateframework.io/docs/security/) when implementing update trust; no TUF service, registry, helper or UI is built today.

## Official artifacts and future tooling

When publishable official artifacts exist, bind artifact digest and source/version, checksums, SBOM and build provenance to cryptographic authenticity evidence. Select standard tooling then; this policy adds no signing service or Core runtime dependency.

Verification defines authorized signer/issuer, trust anchors, rotation/revocation and pinned/offline verification where appropriate. Private signing material stays in an approved secret system. Checksums alone prove no official origin; signatures do not establish safety, compatibility or authorization.

Official Modules can publish equivalent evidence plus compatibility/certification results and affected-version security advisories. Future registry/advisory/update locations belong to release/discovery tooling, not hardcoded business URLs or mandatory online boot checks. An actual updater must handle unavailable/stale metadata and revocation safely.

Third-party/unsigned artifacts remain usable under explicit consumer trust and licenses; registry claims cannot create official status or bypass Application permissions. Forks may reuse licensed tooling/docs while maintaining their own publisher/trust identity, compatibility and security support. Do not deliberately break interoperability to impose separation.
