# ADR-0005 — Initial Go Consumption Shape

Status: accepted starting packaging decision; supported versions/public APIs and executable proof remain pending Issue #1.
Date: 2026-10-01.

## Context

AChrix must be a versioned reusable dependency with a small extension surface, rather than a copied starter or mandatory central service. Deciding the distribution shape now reduces avoidable coupling without requiring a framework, SDK repository or runtime infrastructure before implementation.

## Decision

- Begin with one Go module for the shared Foundation, using the repository-aligned module path `github.com/AChWorks/achrix`. Preserve its spelling in imports and metadata. No `go.mod` or package is created by this documentation decision.
- Core and necessary internal Modules may share that module. Publish only packages/APIs that the first independent consumer actually needs; implementation details use appropriate Go `internal` packages and unexported identifiers. An architecture Module does not automatically become a separately distributed Go module.
- The initial first-party artifact follows the existing MPL-2.0 scope. Do not extract an Apache-2.0 SDK merely to satisfy a directory pattern; designate one only when an actual integration artifact and rights boundary warrant it.
- The proving consumer has its own `go.mod` and composes a necessary minimal extension using intentionally public contracts. It must install/build/use a pinned Foundation version outside the Foundation module, without copying runtime source or importing its internals.
- A local `replace` or workspace can help development, but it is not the only consumption proof. Verify the published dependency path in an isolated consumer without a local replacement/workspace override, using a source-backed version or Go pseudo-version before the first suitable tag. Do not disable checksum/security mechanisms merely to make the proof pass.
- Start published development releases in `v0.x`, with explicit compatibility/change notes; this is not a promise of a stable `v1` API. Choose `v1` only when the tested contract deserves it. Future breaking major versions follow Go's module-path rules, including `/v2` and later suffixes when applicable.
- Record exact supported Go/PostgreSQL/dependency versions and the necessary public package/API choices with the implementation evidence in Issue #1 before coupling executable code. A preferred path is not verified compatibility or performance evidence.

## Consequences

One repository-aligned module avoids premature multi-package release coordination and a vanity-domain ownership commitment. It provides a cheap normal dependency path; changing that path later requires an explicit compatibility/migration decision.

Supported external contracts and deliberate upgrades preserve future options; this does not promise immutable imports or third-party compatibility for thirty years.

## Canonical owners and proof

[Consumption and packaging](../architecture/consumption-and-packaging.md) owns the durable constraints. [Module model](../architecture/module-model.md) owns capability placement. [Licensing](../legal/licensing.md) owns artifact terms. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns compatibility/release evidence. [Issue #1](https://github.com/AChWorks/achrix/issues/1) owns executable acceptance and its current start gate.

Do not infer that a public repository is a runnable module, that `internal` is a sandbox, or that this ADR validates an unimplemented extension API.

## Official evidence

- [Go Modules Reference](https://go.dev/ref/mod): module/package paths, versioning, pseudo-versions, major-version suffixes and replacements.
- [Go module layout](https://go.dev/doc/modules/layout): separating supported external imports from implementation details.
- [Go compatibility policy](https://go.dev/doc/go1compat): supported language/library compatibility boundaries, not a guarantee for untested third-party contracts.
