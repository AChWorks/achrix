# AChrix

**AChrix** (pronounced `ATCH-riks`; Persian: `اَچ‌ریکس`) is an AChWorks Foundation being built as a reusable, versioned application base for web-connected products. A minimal Core and optional Modules share useful engineering capabilities while products own their business behavior.

The accepted starting stack is Go and ordinary PostgreSQL. Optional infrastructure is introduced for real workloads; see [ADR-0002](docs/decisions/ADR-0002-go-primary-implementation.md) and [ADR-0003](docs/decisions/ADR-0003-postgresql-and-optional-infrastructure.md).

## Start here

- [Master Specification](docs/MASTER-SPEC.md): mission, durable constraints and success criteria.
- [Project Map](docs/PROJECT-MAP.md): the authoritative document and work navigation.
- [Contributing](CONTRIBUTING.md) and [Agent instructions](AGENTS.md): how to work in this repository.
- [GitHub Issues](https://github.com/AChWorks/achrix/issues) and [Pull Requests](https://github.com/AChWorks/achrix/pulls): current work and review evidence.
- [achworks.yaml](achworks.yaml): machine-readable Foundation identity and discovery metadata.

## Implementation status

The initial executable baseline supplies minimal instance-owned Go composition, authorization and lifecycle contracts. The separately composed [Notes consumer](fixtures/notes/README.md) proves a pinned dependency, consumer-owned PostgreSQL persistence/migrations, direct invocation and one HTTP adapter through the same Application policy. [Operations](docs/operations/operability-performance.md#supported-environment) owns the supported matrix and the shared local/CI validation command. [Issue #1](https://github.com/AChWorks/achrix/issues/1) owns full executable acceptance and its exact evidence; this fixture does not establish a production product/deployment profile.

Suitable products will consume a versioned AChrix dependency rather than permanently copy its shared source. Product-specific behavior stays with the product. [Koinon](https://github.com/AChWorks/koinon) owns ecosystem governance/discovery and is not an AChrix runtime dependency.

## Consume and extend

From your own Go module, pin the initial development release explicitly:

```bash
go get github.com/AChWorks/achrix@v0.1.0
```

[GitHub Releases](https://github.com/AChWorks/achrix/releases) owns availability and exact source/artifact evidence. The [initial release line](docs/lifecycle/lifecycle-and-compatibility.md#initial-development-release-line) defines compatibility/support limits; use the [public contracts](docs/architecture/contracts-and-interfaces.md#initial-go-public-surface) to compose trusted Modules. Products own their account/profile meaning, permission policy, domain/business semantics and data; accepted reusable Modules such as Identity may supply shared authentication/session mechanics when their real implementation is delivered. [Internationalization](docs/web/seo-and-semantic-web.md#internationalization-and-directionality) covers multilingual and RTL/LTR evolution; the current v0.1.0 Foundation release includes no reusable UI/Identity/Search/Settings/Recovery implementation.

Current source develops the next **v0.2** contract, including optional capability metadata and bounded authorization/shutdown with Core authorization ABI 2. It is not a v0.1-compatible patch or a published v0.2 release; read the [migration notes](docs/lifecycle/lifecycle-and-compatibility.md#next-development-minor-migration) before adopting that source.

That development source includes product-local [Identity](identity/README.md) accounts, maintained Argon2id passwords and revocable server-side sessions with a same-origin HTTPS cookie/CSRF adapter. [Audit](audit/README.md) records the five accountable account/credential/status/revocation operations in the same PostgreSQL transaction, with authorized bounded query/export. The independent Notes test composition proves authentication followed by separate product authorization and retained-dataset restore; it establishes no production deployment, CMS, central identity service or permission roles.

## Licensing and participation

First-party repository content defaults to [MPL-2.0](LICENSE). Compatible independent commercial/third-party Modules are possible; any Apache-2.0 SDK designation requires an explicit artifact scope. [Licensing](docs/legal/licensing.md) owns the details.

[Governance](GOVERNANCE.md), [Trademark Policy](TRADEMARKS.md) and the [Contribution Agreement](docs/legal/cla.md) cover canonical authority, honest representation and incoming rights. Publishing the agreement or opening a PR does not constitute signed consent.
