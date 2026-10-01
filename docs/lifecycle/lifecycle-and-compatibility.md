# Lifecycle and Compatibility

Install, update, upgrade, migration, rollback, recovery, and compatibility are first-class product concerns.

## Lifecycle contract first

Do not begin by building one universal updater.

First define the lifecycle behavior every published distribution/module must make explicit.

## Version identity

Published Foundation artifacts, consumer-facing Foundation versions, and independently distributed Modules must have an immutable version/build identity tied to source.

Use semantic versioning where it accurately communicates compatibility.

Do not use version numbers as a substitute for a documented compatibility boundary.

## Foundation consumer lifecycle

A product consuming the Foundation must be able to identify the Foundation version it uses and the supported compatibility/update path.

A Foundation upgrade is not just a source refresh. Where applicable it must account for:

- consumer runtime/framework compatibility;
- enabled Foundation Module compatibility;
- configuration/default changes;
- schema/data migrations;
- published Application/API/event contract changes;
- required preflight/postflight behavior;
- recovery/rollback or roll-forward constraints.

Do not make routine Foundation maintenance depend on manually copying shared runtime source into each product.

The product remains owner of its deployment/release operation; the Foundation owns the compatibility information and migration behavior of Foundation code it publishes.

## Installation

Installation should be:

- deterministic enough to reproduce;
- explicit about runtime/database/extensions;
- explicit about writable paths and secrets;
- fail-closed on unsupported environments;
- safe to re-enter where practical;
- verifiable through post-install health/readiness evidence.

## Update vs upgrade

A lifecycle operation must know whether it is:

- compatible update;
- feature/minor evolution;
- breaking/major upgrade;
- data/schema migration;
- module enable/disable/install/removal;
- runtime/dependency upgrade.

Do not treat all of these as "replace files and hope".

## Preflight

Before a stateful upgrade, evaluate only relevant checks such as:

- current version;
- target compatibility;
- runtime/extensions;
- database family/version/capabilities;
- free disk/storage where material;
- enabled module compatibility;
- migration prerequisites;
- required secrets/keys;
- maintenance/quiescence requirements;
- recoverability/backup state where failure can damage durable data.

## Migrations

Migrations are production behavior, not setup scripts.

Rules:

- preserve domain invariants;
- consider old/new code overlap where deployment can create it;
- avoid irreversible destructive migration in the same step when safer expand/migrate/contract is warranted;
- handle interrupted/re-entry behavior where material;
- make failure state diagnosable;
- define rollback or roll-forward behavior;
- never infer a successful migration only from process exit if postflight invariants matter.

Database-specific migrations are allowed when the module deliberately requires that capability.

## Compatibility

Published module/API/event/message contracts should evolve additively where possible.

Breaking changes require:

- explicit version boundary;
- migration path;
- compatibility window/retirement rule appropriate to the consumer;
- tests proving supported combinations.

Persisted async payloads must remain readable for their required lifetime.

## Release activation

Where deployment risk justifies it, prefer versioned release artifacts plus an atomic/reversible activation mechanism rather than mutating a live code tree in place.

The exact mechanism is environment-specific and should not be universalized without evidence.

## Rollback vs roll-forward

Rollback is not always safe after a data migration or external side effect.

Every material upgrade should know which recovery strategy applies:

- rollback;
- roll-forward;
- restore;
- compensating operation;
- manual reconciliation.

Do not claim rollback support when only code files can be reverted but data cannot.

## Backup and recovery

Backup existence is not proof of recoverability.

When state is material, recovery planning includes:

- database/data;
- uploaded/object storage data;
- encryption/signing material required to interpret data;
- runtime configuration needed to restore;
- version identity;
- a credible restore path.

## Modules

Module lifecycle must eventually account for:

- compatibility with Foundation/runtime versions;
- dependency/capability requirements;
- migrations;
- enable/disable semantics;
- removal/data retention semantics.

Do not build dynamic plugin lifecycle until real products need runtime install/enable/disable behavior.

## Official release and ecosystem evidence

For future publishable official artifacts, the release/tooling path should bind immutable artifact identity/digest to source/version, include checksums, an SBOM and build provenance, and provide cryptographic authenticity verification. Select the smallest standard formats/tooling when artifacts exist; no signing service or new Core dependency is required by this policy.

The verification boundary must support an explicit authorized signer/issuer, trusted-key or certificate policy, rotation/revocation and offline/pinned verification where appropriate. A checksum alone proves neither official origin nor trust. Keep private signing material in an approved secret system, never in module metadata or Git.

Official Modules may publish the same evidence. Preserve separate compatibility-test/certification evidence and security advisory/affected-version information. Authenticity does not establish safety, authorization or compatibility.

Keep future official registry, advisory feed and update-channel locations in release/discovery tooling. Do not make one URL/provider a business invariant or require a company network call to boot/use Core. Define failure, stale metadata and revocation behavior when an actual updater exists; preserve the existing no-unsafe-downgrade and recovery rules.

A registry listing or self-declared “official” metadata is not proof of origin. Independently maintained/unsigned third-party artifacts remain usable under an explicit consumer trust policy and their licenses; they cannot acquire official identity or bypass Application authorization.

Forks may reuse licensed public release tooling and documentation but must represent their own publisher, trust/signing identity, supported compatibility and security maintenance. Do not intentionally break protocols to impose that separation.

## Updater extraction trigger

A shared updater/installer implementation becomes justified only after multiple real distributions converge on the same mechanics and centralization lowers total risk/maintenance cost.
