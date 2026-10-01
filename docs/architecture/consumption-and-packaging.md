# Consumption and Packaging

## Supported reuse

A suitable product consumes a versioned Foundation dependency plus selected reusable Modules, while its thin composition shell and product/domain behavior remain independently owned. [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) selects the starting Go module/consumer mechanics; exact supported versions and APIs need executable evidence.

The consumer must identify/pin its Foundation version, deliberately review updates and check compatibility/migrations before activation. Shared security and lifecycle fixes must be obtainable through this path. Products rebuild, validate and redeploy their binaries to receive dependency updates; changing the Foundation source does not update a deployed product automatically.

Generated bootstrap files may transfer to the product. Shared runtime is not an unmanaged permanent source copy. A justified fork has explicit independent ownership/maintenance and follows [Trademark Policy](../../TRADEMARKS.md); ordinary dependency consumption remains the default shared path.

## Module installation

Compose trusted Modules at build time through public contracts into a pinned, tested product executable/image. Core and internal Modules in one Go module share its dependency version; separately packaged Modules have their own versions.

Installing/updating code selects or builds a compatible product artifact. An already packaged optional Module may be enabled/disabled through configuration under dependencies, authorization and data-lifecycle rules. Code presence, activation, permission and data removal are distinct.

Standard distributions offer prepared compatible combinations; custom compositions use a trusted publisher/build pipeline. A UI may identify a Core/Module change while applying a coherent product artifact. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#product-update-experience) owns the simple user-facing update flow and activation/recovery. External integration remains available under Module model; Core needs no source/archive loader, marketplace or build service.

## Module versus distribution

Core and internal Modules can share repository, dependency module, process, deployment and relational database indefinitely. Promote product-local behavior only under the [Module model](module-model.md); independent packaging then needs additional value, such as:

- consumers needing selective independent versions/dependencies;
- materially different release/compatibility cadence;
- independent ownership/security boundary;
- lower total migration, coupling and maintenance cost.

A Distribution is an optional curated product-class composition, such as content/CMS or API/headless. It consumes the Foundation and keeps product delivery/domain ownership; it is not a permanent source fork or a constraint on other products.

## Public and license boundaries

An independent consumer/extension must compose through intentionally public contracts, without runtime source copying or internal imports. Keep implementation details private and artifact/license scope clear. [Licensing](../legal/licensing.md) owns notices, covered-source obligations and any separately designated Apache-2.0 SDK; an exported symbol alone does not create that SDK or require another repository.

Pinned/local/third-party dependencies must remain usable without a registry account or central runtime service. Registry discovery, signing and update delivery belong to optional release/tooling boundaries in [Lifecycle](../lifecycle/lifecycle-and-compatibility.md). A listing, license, certification and runtime permission are separate evidence.

## Executable consumption proof

[fixtures/notes](../../fixtures/notes/README.md) is a separately composed consumer with its own Go module. It pins a source-backed Foundation pseudo-version in its manifest; no `replace`, `go.work`, Foundation source copying or Foundation internal import is used. `scripts/validate.sh` copies only consumer-owned source into an isolated workspace and downloads Foundation into a new module cache with normal checksums, then builds and uses its public contracts. The build's identity must match the resolved pin.

The fixture is evidence for this boundary, not a real product or authorization to create a product repository. Update its Foundation explicitly with `go get github.com/AChWorks/achrix@<reviewed-commit-or-version>`, `go mod tidy`, validation and a coherent consumer rebuild. A Foundation source update alone never updates an existing consumer executable.
