# ADR-0006 — Independent Components and Reusable Lifecycle Tooling

Status: accepted ownership/distribution decision; each published component and consumer retains its own exact release and validation evidence.
Date: 2026-10-29.

## Context

AChrix must remain an independently maintained dependency that suitable products can deliberately upgrade without replacing their product code or retaining private Foundation forks. The initial single-Go-module shape in [ADR-0005](ADR-0005-initial-go-consumption.md) gave the first consumer a cheap coherent baseline, but every official package shared its version. The Owner now requires independent Core/Module version units and clear consumption, extension and lifecycle contracts.

Rixa's installation evidence also distinguishes actual usable database, private-storage, TLS and persistent-service outcomes from detected host tools. Migration re-entry, one-time administrator bootstrap and an uncertain mutation require different handling. The reusable contract must support product requirements and verified adapters without making one product or control panel part of Core.

## Decision

- Keep one source repository. Core and the official component units listed in [modules.json](../../modules.json) are independent Go modules, retaining their established import paths. This is version/distribution separation, with no requirement for separate processes, services or databases. Internal packages and related admin surfaces remain with their owning component.
- Preserve old published tags and their monolithic module contents. Transition to the split line as one compatible dependency-set change: a new root that excludes child modules plus explicit compatible child requirements. Do not combine a monolithic root containing a package with a child module that also provides it.
- Core tags retain the root namespace; child tags use their module-directory prefix. Each component's public implementation version identifies its actual resolved source dependency, while capability ABI revisions remain separate. The module inventory and release tooling own exact component paths and preparation mechanics.
- Products pin one compatible selected version per imported module in normal `go.mod`/`go.sum`, inspect resolved transitive changes, validate public contracts/schema compatibility, and rebuild one coherent product artifact. Independent updates respect dependencies; they do not promise arbitrary version combinations or replacement of running Go code.
- AChrix owns optional reusable installation/update tooling. Its first installation contract assesses product-declared requirements, typed observations, explicit adapter support and caller-owned operation progress into a deterministic, recheckable plan. Products and validated adapters own probing, artifact/configuration verification, serialized/fenced mutation, durable intent, migrations/bootstrap, activation and postflight. A plan grants no platform authority and performs no provisioning.
- Keep the contract extensible across direct servers, control panels and processor architectures. Add real adapters and support claims from current consumer evidence, with declared prerequisites and end-to-end validation for each supported profile. No blanket hosting or architecture support follows from a common interface.
- Keep future update and recovery paths compatible with these ownership boundaries, but implement only the presently accepted coherent slice. Core gains no generic host/root/shell/SSH authority, runtime plugin loader, registry or universal updater.

## Consequences

Each component can have its own compatible release cadence and dependency graph. The repository pays the necessary cost of module-local checks, normal isolated multi-module consumption, per-component source/version identity and release namespaces. A tested consumer dependency set supplies a known coherent example without requiring all components to share a version number.

Packaging independence alone changes no published migration bytes, data ownership or application permission. Later public API/schema changes still need versioned compatibility and migration guidance. Source rollback is not data rollback. Products retain their own update UX and deployment authority; prepared artifacts can hide build mechanics from routine users.

This replaces ADR-0005's single-module packaging default for the split line. It does not relabel old releases, authorize product/production rollout, or turn Module ownership into a rule that every helper/subsystem needs an independent distribution.

## Canonical owners

[Consumption](../architecture/consumption-and-packaging.md) owns use and extension, [Contracts](../architecture/contracts-and-interfaces.md) owns public identity/ABI rules, and [Lifecycle](../lifecycle/lifecycle-and-compatibility.md) owns transition, state and activation safety. Component READMEs/source own exact APIs. Git/PR/CI/releases and consumer manifests own actual availability, selected versions and proof.

## Official reference

[Go Modules Reference](https://go.dev/ref/mod) owns module-path, version, subdirectory-tag and dependency-selection mechanics. A Go module is the unit released and versioned together; a repository and a running service are different boundaries.
