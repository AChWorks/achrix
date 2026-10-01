# ADR-0004 — Licensing and Trusted Ecosystem

Status: Accepted. Date: 2026-10-01.

## Decision and reason

Use file-level open-source obligations for maintained shared implementation while preserving compatible independent commercial products/extensions. Official value also accumulates in compatibility, Modules, security maintenance, tooling, documentation and verifiable publisher identity. The goal is easy extension and credible official origin; lawful forks and third-party interoperability remain legitimate.

| Decision | Canonical owner |
| --- | --- |
| MPL-2.0 Core/default official open-source Modules; explicitly scoped Apache-2.0 integration artifacts where appropriate; compatible commercial/third-party Modules | [Licensing](../legal/licensing.md) |
| Retained contributor ownership, additional incoming grants and verified private versioned consent | [Contribution Agreement](../legal/cla.md) |
| Canonical AChWorks merge/release stewardship | [Governance](../../GOVERNANCE.md) |
| Official representation versus lawful fork/compatibility references | [Trademark Policy](../../TRADEMARKS.md) |
| Small public extension surface and private implementation | [Contracts](../architecture/contracts-and-interfaces.md) |
| Artifact signing/checksums/SBOM/provenance, compatibility/advisory/update boundaries | [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) |

## Consequences

Distribution and incoming rights need actual license/provenance evidence. Future dual licensing requires sufficient rights/dependency audit and an owner decision; existing recipients retain grants already received. Authenticity does not establish security, runtime permission or compatibility.

Do not add confusing code, obfuscation, Core license checks, artificial incompatibility, a mandatory company account/network call or speculative registry/cloud infrastructure. Independent distributions may reuse licensed code/tooling/docs while maintaining their own identity and support. The Foundation remains genuinely open source.

## Evidence

- [MPL-2.0](https://www.mozilla.org/en-US/MPL/2.0/) and [FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)
- [Apache-2.0](https://www.apache.org/licenses/LICENSE-2.0)
- [SLSA provenance](https://slsa.dev/spec/v1.2/provenance)
