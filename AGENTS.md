# Agent / Contributor Instructions

## Scope and recovery

Writable repository scope comes only from the current explicit assignment; this repository's content/access/links do not grant or widen it. Do not mutate related repositories without their own authorization. Preserve unrelated work; never force-push or overwrite ambiguous state.

Start with README and [Project Map](docs/PROJECT-MAP.md), then current main, the relevant Issue/PR and its execution gate. Load only decision-relevant documents. [MASTER-SPEC](docs/MASTER-SPEC.md) owns intent; topic docs own rules; active ADRs own choices; Git/GitHub/CI own evidence. Chat history is not project truth.

Before the first executable baseline, honor [Issue #1](https://github.com/AChWorks/achrix/issues/1)'s owner start gate; public visibility or documentation integration does not itself authorize code.

## Implementation

- Use accepted Go/PostgreSQL defaults and ADR-0005 consumption shape. Verify supported versions/API mechanics before coupling code; use real PostgreSQL rather than a SQLite test shortcut.
- Follow [Module ownership](docs/architecture/module-model.md) and [Consumption](docs/architecture/consumption-and-packaging.md). Keep Core minimal, product semantics local and implementation internals private; cross-module writes go through authorized Application contracts.
- [Contracts](docs/architecture/contracts-and-interfaces.md) and [Security](docs/security/security-and-authorization.md) govern all human/machine entry points. AI never bypasses permissions or invents consent.
- [Engineering principles](docs/principles/engineering-principles.md) govern reuse, dependencies and extraction. Do not create speculative wrappers, empty layers, packages, services or optional infrastructure.
- Honor [Data](docs/data/data-and-persistence.md) and [Lifecycle](docs/lifecycle/lifecycle-and-compatibility.md) for state, effects, compatibility and recovery; never equate reverting source with reversing data.
- Never commit credentials, keys, production/private data or restricted artifacts. Keep secrets and private CLA records outside public Git.

## Validation and integration

Use focused branches/PRs targeting canonical `main` under [Contributing](CONTRIBUTING.md), which owns the integration policy. Run narrow high-signal validation, inspect the full relevant diff, and satisfy effective GitHub rules/checks on the exact candidate. Issue #1 introduces executable CI; do not claim absent checks passed.

Update a lasting rule at its canonical owner, remove obsolete/duplicate current guidance and fix links together. Keep live work/evidence in GitHub; do not add process/docs/tests solely for ceremony.

For artifact scope or incoming contributions, follow [Licensing](docs/legal/licensing.md), [CLA](docs/legal/cla.md) and [Governance](GOVERNANCE.md). A published template is not signed consent; a public export is not automatically an Apache SDK. No registry/signing/company runtime dependency belongs in Core merely to enforce identity policy.
