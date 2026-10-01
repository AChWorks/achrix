# Licensing Policy

Status: Accepted by the owner on 2026-10-01.
Standard license texts govern their respective files; this policy explains scope and does not modify those licenses.

## Artifact scope

| Artifact | Default / selection rule |
| --- | --- |
| AChrix Core | MPL-2.0 |
| Official reusable open-source Modules | MPL-2.0 unless an explicitly reviewed artifact decision states otherwise |
| Separately scoped extension SDK, public extension contracts and integration-facing artifacts | Apache-2.0 when appropriate and explicitly designated before publication |
| Third-party Modules | Their own compatible license, including proprietary/commercial |
| AChWorks commercial Modules and services | Independent commercial/closed terms where dependency obligations permit |

The root [LICENSE](../../LICENSE) applies to first-party repository source, documentation and configuration unless a file/directory explicitly declares a different scope. Preserve any existing third-party terms. Policy documents describe contribution, governance and trademark rules; their text licensing does not waive those rules or grant trademarks.

No Apache-licensed SDK exists merely because a public symbol or interface is exported. Until explicitly designated, first-party files follow the root license. A future Apache artifact needs clear file/directory/package scope, the standard license text, SPDX identifiers and any required notices in its actual distribution. Avoid ambiguous mixed-license packaging; do not copy MPL implementation into an Apache-designated file and assume its obligations disappear.

The initial packaging ADR must identify the actual public/runtime boundary and license scope without forcing a separate repository or one Go module per application Module.

## MPL obligations and commercial use

MPL allows commercial use, modification and forks. On distribution, make covered source and modifications available as required, preserve notices, and tell recipients how to obtain the source corresponding to covered code in an executable.

Independent files containing no MPL-covered code may form part of a larger proprietary work. New files containing MPL-covered code and modifications of covered files remain subject to MPL's requirements. A proprietary label or a Module boundary cannot override that rule.

Internal use and server-only operation do not generally constitute distribution to users; client-delivered software is different. This does not require a private SaaS service to publish all backend code, nor force downstream contributors to submit their changes upstream.

Do not add an Exhibit B incompatible-with-secondary-licenses notice by default. Retain the standard MPL options; analyze the real dependency combination before using them.

## Dependencies and redistributions

Before adopting or publishing an artifact, verify direct/transitive dependency licenses, copied/generated/vendored code, build/frontend assets, commercial terms and the actual release version.

Record required notices and covered-source delivery with the artifact that ships them. A registry entry or an SPDX field alone is not compliance evidence. Generated integration artifacts need a known license scope for their templates and included code.

The executable baseline introduces no Apache SDK. Foundation has only Go standard-library dependencies; the separate Notes consumer adds pgx and its pinned MIT/BSD runtime dependencies. Its [LICENSE](../../fixtures/notes/LICENSE) and [third-party notices](../../fixtures/notes/THIRD_PARTY_NOTICES.md) travel with any distributed consumer artifact. Preserve corresponding covered-source access for the actual Foundation pin/consumer build reported by its identity command, rather than merely linking a changing main branch. Go module checksums and the exact manifests identify dependency source; no dependency implementation is copied/vendored into first-party files. Owner-authorized work introduces no covered external contribution or invented CLA signature.

Strong copyleft, network copyleft, source-available or paid-provider terms can change the viable product/distribution model. Evaluate the actual combination; never claim that MPL overrides another dependency's obligations. Prefer a compatible maintained option when fit and total cost are favorable.

## Incoming contributions and rights

[GOVERNANCE.md](../../GOVERNANCE.md) owns canonical authority; [CONTRIBUTING.md](../../CONTRIBUTING.md) owns the PR workflow; [Contribution Agreement](cla.md) owns additional contribution rights.

A CLA can grant rights only in contributions the signer is authorized to license. It does not relicense unrelated third-party dependencies or remove obligations from previously distributed copies. Future relicensing/dual licensing remains a separate AChWorks decision with an actual rights/dependency audit; it is not performed by this policy.

Trademarks are governed separately by [TRADEMARKS.md](../../TRADEMARKS.md). MPL code rights are not conditional on using an official registry, network service, commercial account or license-check endpoint.

## Evidence

- [Mozilla MPL-2.0 text](https://www.mozilla.org/en-US/MPL/2.0/)
- [Mozilla MPL FAQ](https://www.mozilla.org/en-US/MPL/2.0/FAQ/)
- [Apache-2.0 text](https://www.apache.org/licenses/LICENSE-2.0)
