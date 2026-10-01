# Consumption and Packaging

## Supported reuse

A suitable product consumes a versioned Foundation dependency plus selected reusable Modules, while its thin composition shell and product/domain behavior remain independently owned. [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) selects the starting Go module/consumer mechanics; exact supported versions and APIs need executable evidence.

The consumer must identify/pin its Foundation version, deliberately review updates and check compatibility/migrations before activation. Shared security and lifecycle fixes must be obtainable through this path. Products rebuild, validate and redeploy their binaries to receive dependency updates; changing the Foundation source does not update a deployed product automatically.

Generated bootstrap files may transfer to the product. Shared runtime is not an unmanaged permanent source copy. A justified fork has explicit independent ownership/maintenance and follows [Trademark Policy](../../TRADEMARKS.md); ordinary dependency consumption remains the default shared path.

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
