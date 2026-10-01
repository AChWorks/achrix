# ADR-0004 — Licensing and Trusted Ecosystem Boundaries

Status: Accepted
Date: 2026-10-01
Decision authority: explicit owner selection of MPL-2.0, conditional Apache-2.0 integration artifacts, commercial extension compatibility and AChWorks canonical stewardship.

## Context

AChrix is a reusable versioned Go Foundation for independently owned products. The owner wants long-term evolution and affordable external reuse without speculative infrastructure, while value accumulates in compatibility, official Modules, security maintenance, tooling, documentation and a trusted project identity.

“Easy to extend, but expensive to impersonate” is a stewardship/trust goal. It is not permission to make code confusing, inflate migration/fork costs or restrict rights granted by an open-source license.

## Decision and canonical owners

- [Licensing policy](../legal/licensing.md) owns artifact scope: MPL-2.0 Core and default official open-source Modules; explicitly scoped Apache-2.0 extension/integration artifacts where appropriate; compatible independently licensed commercial/third-party Modules.
- [Contribution Agreement](../legal/cla.md) owns additional incoming contribution rights. Version 1.0 identifies the individual recipient and manual consent process. Adoption of the template supplies no signed consent; covered external contributions still need verified acceptance.
- [Trademark Policy](../../TRADEMARKS.md) separates code permissions from representation as official AChrix/AChWorks.
- [Governance](../../GOVERNANCE.md) reserves canonical merge/release stewardship to AChWorks without denying lawful forks.
- Existing [contracts](../architecture/contracts-and-interfaces.md), [module model](../architecture/module-model.md), [packaging](../architecture/consumption-and-packaging.md), [reuse principles](../principles/reuse-and-evolution.md) and [lifecycle](../lifecycle/lifecycle-and-compatibility.md) own the corresponding engineering constraints.

Keep public extension surfaces small, deliberate and versioned. Protect implementation privacy with unexported APIs and appropriately placed Go internal packages; these are dependency controls, not security sandboxes or copy protection. A separate consumer/extension must use the supported contracts without importing Core internals.

Allow external maintained code/services through fit Application/module/provider boundaries. Preserve their licensing, data ownership, auth, failure and lifecycle constraints. Choose adaptation, direct use, custom residual code or later extraction by total lifetime cost; do not build a universal importer or wrap every library.

Keep future module/release identity, signing, checksums, SBOM, provenance, compatibility evidence, advisory and update-channel boundaries feasible in release/tooling metadata. No registry, marketplace, certification service, cloud runtime, signing dependency or central online Core check is built by this decision.

## Alternatives and consequences

GPL-style combined-work copyleft was considered. MPL's file-level obligations better preserve independent proprietary consumer files/modules while keeping distributed covered code and modifications available. Permissive licensing for all Core was not selected; targeted Apache artifacts can reduce integration friction without silently relicensing Core.

The ecosystem cannot make a lawful fork technically impossible or guarantee that impersonation is expensive. A serious independent distribution needs credible maintenance, release identity, compatibility/support and its own representation; it may legally reuse public code, tooling and documentation. Differentiation comes from maintained trust and service quality, not artificial incompatibility.

Signatures prove origin/integrity under a defined trust policy, not safety, compatibility or authorization. Those remain separate evidence. Forks cannot use official private keys or claim official approval, but third-party interoperability remains open.

No thirty-year compatibility or zero-cost technology replacement is promised. Preserve credible cheap options now; version and test actual published contracts, export/migrate owned data where material, and refine support only from real consumers.

## Implementation boundary

Issue #1 remains the minimal executable/versioned Foundation proof. Add only the public extension/consumer and artifact-license evidence it needs; do not absorb the full future ecosystem.

Legal-recipient/CLA adoption and its validation evidence are recorded in [Issue #15](https://github.com/AChWorks/achrix/issues/15). Release verification is implemented when publishable artifacts exist; registries/certification/marketplace services need real consumer economics.

## Evidence

- [MPL-2.0](https://www.mozilla.org/en-US/MPL/2.0/) and [Mozilla FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)
- [Apache-2.0](https://www.apache.org/licenses/LICENSE-2.0) and [ASF ICLA example](https://www.apache.org/licenses/icla.pdf)
- [Go module organization/internal packages](https://go.dev/doc/modules/layout)
- [Go compatibility policy](https://go.dev/doc/go1compat)
- [SLSA provenance](https://slsa.dev/spec/v1.2/provenance)

These establish legal/technical mechanisms. Project-fit, lifecycle economics and future trust advantages are engineering judgments, not measured AChrix results.
