# ADR-0005 — Initial Go Consumption Shape

Status: **Superseded for packaging** by [ADR-0006](ADR-0006-independent-components-and-lifecycle.md). Retained only because the older published dependency shape still matters for consumer migrations.
Date: 2026-10-01.

## Historical choice and reason

AChrix initially used one repository-aligned Go module, `github.com/AChWorks/achrix`, containing Core and the initial shared packages. That let the first executable Foundation and its independent Notes consumer prove ordinary versioned Go imports, a small public composition/authorization contract and real PostgreSQL behavior without premature coordination of several release units, separate repositories or a vanity module domain.

The proving consumer had its own `go.mod`, selected a source-backed pinned Foundation dependency without a permanent local `replace`/workspace override, and composed the published code rather than copying runtime sources or importing internals. Compiled composition avoided the portability and toolchain constraints of dynamic Go plugins. [Issue #1](https://github.com/AChWorks/achrix/issues/1) owns the original executable acceptance/evidence; historical publication remains in the immutable tags/releases.

## What superseded it

[ADR-0006](ADR-0006-independent-components-and-lifecycle.md) adopted independently versioned Core and component Go modules **within the same source repository**, preserving the established import paths. Old monolithic roots can still contain the child packages; combining one with separately published child modules may create ambiguous imports.

Consumers moving from the old line must follow the [monolithic-to-independent transition](../lifecycle/lifecycle-and-compatibility.md#monolithic-to-independent-module-packaging), choose a coherent pinned dependency set, inspect compatibility and migrations, then rebuild and verify their product. Do not read this historical ADR as an instruction to start a new single-module release or to rewrite old tags.

Current package selection and extension are governed by [Consumption](../architecture/consumption-and-packaging.md), exact public APIs by [Contracts](../architecture/contracts-and-interfaces.md), supported environment by [Operations](../operations/operability-performance.md#supported-environment), and published release identities by Git/Go manifests and releases.
