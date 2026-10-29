# Consumption and Packaging

## Supported reuse

A suitable product consumes versioned Core and selected reusable component dependencies, while its thin composition shell and product/domain behavior remain independently owned. [ADR-0006](../decisions/ADR-0006-independent-components-and-lifecycle.md) owns the independent version units; [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) records the original Go consumption decision. Exact supported versions and APIs need executable evidence.

The consumer must identify/pin its Core and selected component versions, deliberately review updates and check the resolved compatible set and migrations before activation. Shared security and lifecycle fixes must be obtainable through this path. Products rebuild, validate and redeploy their binaries to receive dependency updates; changing the Foundation source does not update a deployed product automatically.

Generated bootstrap files may transfer to the product. Shared runtime is not an unmanaged permanent source copy. A justified fork has explicit independent ownership/maintenance and follows [Trademark Policy](../../TRADEMARKS.md); ordinary dependency consumption remains the default shared path.

## Start a product

Choose an exact reviewed Core release from [GitHub Releases](https://github.com/AChWorks/achrix/releases), then keep its exact dependency identity and the compatible versions of selected components in the consumer's module files and build evidence. [ADR-0006](../decisions/ADR-0006-independent-components-and-lifecycle.md) owns independent version units; [the packaging transition](../lifecycle/lifecycle-and-compatibility.md#monolithic-to-independent-module-packaging) governs adoption from monolithic releases. AChrix remains pre-v1 until explicitly changed. Existing consumers must apply every relevant step in the [lifecycle and migration guide](../lifecycle/lifecycle-and-compatibility.md) before adopting a newer release. Use the toolchain and database/storage profile from the current [supported environment](../operations/operability-performance.md#supported-environment) and the owning Module guides linked below.

Create your own Go module and pin the exact version:

```bash
mkdir my-product
cd my-product
go mod init example.com/my-product
ACHRIX_VERSION="<reviewed-release-tag>"
go get "github.com/AChWorks/achrix@$ACHRIX_VERSION"
```

Save this complete minimal composition as `main.go`:

```go
package main

import (
    "context"
    "errors"
    "fmt"
    "log"
    "time"

    "github.com/AChWorks/achrix"
)

func run() error {
    // The product owns policy. This example denies every domain operation.
    policy := achrix.PolicyFunc(func(context.Context, achrix.Principal, string, string) error {
        return achrix.ErrDenied
    })
    app, err := achrix.New(achrix.Config{
        StartupTimeout:  5 * time.Second,
        ShutdownTimeout: 5 * time.Second,
    }, policy)
    if err != nil {
        return err
    }
    if err := app.Start(context.Background()); err != nil {
        return err
    }

    readyCtx, cancelReady := context.WithTimeout(context.Background(), time.Second)
    readyErr := app.Ready(readyCtx)
    cancelReady()

    // A server product closes ingress and drains its domain work before this.
    stopCtx, cancelStop := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancelStop()
    return errors.Join(readyErr, app.Shutdown(stopCtx))
}

func main() {
    if err := run(); err != nil {
        log.Fatal(err)
    }
    fmt.Println(achrix.Version())
}
```

Then run:

```bash
go mod tidy
go mod verify
go run .
```

Expected output is the exact selected Foundation version. This Core-only program opens no database, file store or HTTP listener. It proves the dependency and bounded composition entry point; it supplies no authentication or product domain operation. Commit the product's `go.mod`/`go.sum`. Use ordinary module resolution, without `replace`, `go.work` overrides or copied Foundation runtime source.

### Add only the Modules the product needs

Core and official components below have independent Go-module versions. Select a reviewed compatible set rather than unrelated “latest” versions. [The inventory](../../modules.json) owns module paths/directories; each module manifest owns its dependency requirements. Import, construct and pass only needed runtime Modules to `achrix.New`. An unconstructed Module opens no pool or worker. Related packages within one owning component still share its version; packages outside the imported graph need not be compiled into that executable.

| Need | Public entry and owning guide | Required composition / product obligation |
| --- | --- | --- |
| Accounts, passwords and sessions | [Identity](../../identity/README.md): `identity.NewPostgres`, `identity.NewService`, `identity.NewWeb` | Identity requires Audit and Core authorization ABI 2. Product owns profiles, explicit grants and trusted HTTPS ingress. |
| Accountable events and bounded query/export | [Audit](../../audit/README.md): `audit.NewPostgres`, `audit.NewService` | Identity/Audit use the same supported PostgreSQL consistency boundary. Atomic append uses the caller's native transaction; it is not distributed atomicity. |
| Private original attachments and clean image preparation | [Media](../../media/README.md): `media.NewPostgres`, `media.NewService` | Core authorization ABI 2; private Linux storage and explicit product permissions. Default PNG/JPEG; explicit SVG opt-in is private-only. `PreparePublicImage` is a separate exact-asset capability and grants no publication or public URL. |
| Optional site resolution and management planning | [Multi-Site](../../multisite/README.md): `multisite.New` or `multisite.NewManaged`, `multisite.NewService` | Core authorization ABI 2. Resolver-only use remains valid; managed mode adds safe inventory and per-site selection/activation plans. Product owns persistence, typed construction, trusted ingress and actual activation/isolation; single-site products may omit it. |
| Account/Media administration | [Admin](../../admin/README.md): `admin.New`, `Handler()`, `ConfigureServer`; [Media surface](../../media/admin/README.md) | Compose Identity/Web authentication plus explicit owning surfaces and product policy. The presentation shell is mounted separately, not an `achrix.Module`; owning database Modules still participate in Core lifecycle. |
| Installation assessment and re-entry planning | [Installation](../../installation/README.md): `Assess`, immutable plan and `Recheck` | Standalone tooling before a Core runtime/database/admin exists. Products declare requirements and provide verified observations/adapter authority/progress; product/adapters own actual effects and postflight. |

Add only needed module paths with exact reviewed versions. For example, an Identity/Audit composition pins its chosen set together:

```bash
ACHRIX_CORE_VERSION="<reviewed-core-version>"
ACHRIX_AUDIT_VERSION="<compatible-audit-version>"
ACHRIX_IDENTITY_VERSION="<compatible-identity-version>"
go get "github.com/AChWorks/achrix@$ACHRIX_CORE_VERSION" "github.com/AChWorks/achrix/audit@$ACHRIX_AUDIT_VERSION" "github.com/AChWorks/achrix/identity@$ACHRIX_IDENTITY_VERSION"
go mod tidy
go mod verify
go list -m all
```

Inspect the resulting direct/transitive versions; an owning component may require other components, including its optional presentation packages. Go dependency selection and successful compilation do not prove runtime ABI, schema or deployment compatibility. Record a tested compatible set in product manifests/build evidence.

For a stateful product, follow the owning public signatures/examples:

1. Supply product-owned configuration/secrets, supported PostgreSQL credentials and, if needed, an exclusive mode-`0700` private Media directory outside webroots.
2. Run only participating Modules' public `Migrate(ctx, dsn)` operations explicitly with deadlines, serialized by the product's install/deploy path. Preserve published SQL and ledgers; requests/startup do not install schema.
3. Construct typed Modules, compose the Application with a fail-closed product Policy, then construct the owning Services. Construct Audit Service before Identity Service; Identity and Audit must use the same supported database profile.
4. Start and check readiness before opening ingress. Authentication supplies a principal; each domain operation separately authorizes its capability/target. Public authentication admission, navigation, collection metadata, object bytes and mutation grants are distinct.
5. On shutdown, stop ingress and drain product-owned requests/transactions before shutting down the Application. Deadlines bound cooperative work; in-process Modules are trusted code.

[Notes](../../fixtures/notes/README.md) and its [Media](../../fixtures/notes/README.md#private-media-proving-composition)/[Admin browser](../../fixtures/notes/README.md#admin-browser-proving-composition) compositions demonstrate public API wiring, product-owned policy and normal pinned consumption. They are fixtures, not a production starter distribution; copy only product-owned example/bootstrap material you deliberately maintain, never shared runtime or private implementations.

### Extend and upgrade without losing product code

Keep product/domain packages, schemas, roles, content/media relationships, configuration and deployment code in the product. Implement supported public interfaces or use typed public collaborators; request a Foundation contract extension when no public seam fits rather than patching dependency-cache files or importing private implementation. A deliberate fork has its own maintenance/merge obligations.

For an upgrade, choose an exact reviewed component version, inspect its release/migration notes and public/dependency/schema diff against the whole affected dependency set, then:

```bash
# Select the component path and exact reviewed version before running.
ACHRIX_MODULE_PATH="github.com/AChWorks/achrix/media"
ACHRIX_MODULE_VERSION="<reviewed-compatible-module-version>"
go get "$ACHRIX_MODULE_PATH@$ACHRIX_MODULE_VERSION"
go mod tidy
go mod verify
go test ./...
go build ./...
```

Validate affected product behavior, stage explicit migrations and a coherent rebuilt product artifact, then activate through its supported deployment/recovery profile. Retain Core `achrix.Version()`, each participating component's `Version()`/`Descriptor.Version`, resolved module versions, product build and migration identities. Updating a dependency preserves separately owned product source; a breaking API or schema change can still require deliberate adaptation. Code rollback does not reverse data or external effects.

A real product must identify its minimum workflow and enabled Modules, policy, data/storage and deployment profile. CMS/content is one candidate; AChrix owns no CMS content model. Aggregate capacity, product update UX and production backup/recovery remain product/deployment evidence and require their own authorized implementation rather than following automatically from Foundation composition.

## Develop and extend a component

Keep one coherent owner for behavior, public contracts, state/migrations and related presentation packages. Use the existing component when the change belongs there; a helper or optional subsystem does not automatically earn another module/version. A new independent component must have a real consumer, clear ownership, public seam and inventory/release/validation coverage.

- Implement intentionally public typed interfaces and ordinary explicit constructor wiring. Keep private data/provider details behind the owning boundary; avoid internal imports or another component's SQL.
- Document required/optional capability ABI revisions, supported component dependencies, configuration/defaults, permissions/targets, bounded failures, state/migrations and disable/re-enable/removal/recovery obligations. A Go dependency version and a capability ABI are different contracts.
- Keep constructors and descriptors deterministic and side-effect-free where the owning contract requires it. Acquire only the resources of active instances, verify readiness before ingress and drain owned work before resource closure.
- Preserve immutable published migrations and map breaking API/schema changes to the owning component's version and consumer migration guidance. Compatible independent releases still need an actually tested product combination.
- Run focused checks inside the owning module with `GOWORK=off`. Root `go test ./...` covers Core only. Full validation enumerates [modules.json](../../modules.json) and proves normal cold-cache consumption from the independent Notes module; temporary authoring workspaces never replace that evidence.
- Release under the component's own tag namespace and exact source identity through [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#source-release-preparation-and-verification). Update consumers deliberately; publishing source does not rebuild or activate an installed product.

## Module installation

Compose trusted Modules at build time through public contracts into a pinned, tested product executable/image. Core and official component units have independent dependency versions; related packages inside one component share its owning version. One built product has one selected version for each Go module. Sites on a shared product installation can activate different supported capabilities without mixing different versions of that same dependency. Their `Descriptor.Version` reports that actual source-backed dependency identity under the [public version contract](contracts-and-interfaces.md#capability-abi-and-composition); development/replacement labels are explicit and never an invented release.

Installing/updating code selects or builds a compatible product artifact. An already packaged optional Module may be enabled/disabled through explicit product configuration under dependencies, authorization and data-lifecycle rules. **Code/artifact presence, advertised capability availability, runtime activation/resource acquisition, authorization and durable state/data removal are distinct.** Enabling a path never grants permission by itself; disabling one never implies uninstalling code, reversing migrations or deleting retained data. A change that alters the composed Module/capability graph normally takes effect through construction, validation and activation of a new Application instance/product artifact under the product lifecycle; it does not imply mutating the running graph, hot-unloading Go code or a dynamic plugin runtime.

The same distinction applies inside a Module when a real optional subsystem is justified. Product composition may omit that subsystem, or include its code while keeping its runtime capability inactive, according to the owning Module's explicit supported contract. If omitting a subsystem from the build materially avoids dependencies or artifact/resource cost, use an explicit package/composition boundary where useful; this does not by itself create a new Module, Go module, repository or service.

For per-site selection, use the optional [Multi-Site management contract](../../multisite/README.md). Its immutable replacement plan does not start/stop Modules or persist configuration. Products reauthorize, compare the expected configuration revision, persist consistently, stop/drain affected work, construct only selected typed instances, validate dependencies/readiness and activate through a supported lifecycle path. A plan fingerprint is a precondition, not permission. Retain inactive data and reject a composition whose required dependencies are absent.

Standard distributions offer prepared compatible combinations; custom compositions use a trusted publisher/build pipeline. A UI may identify a Core/Module change while applying a coherent product artifact. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#product-update-experience) owns the simple user-facing update flow and activation/recovery. External integration remains available under Module model; Core needs no source/archive loader, marketplace or build service.

## Module ownership versus distribution

The [Module model](module-model.md) decides whether a capability belongs in Core, a reusable AChrix Module or the product. That ownership decision is deliberately separate from packaging/distribution.

Official executable component units live in the existing repository as independent Go modules under [ADR-0006](../decisions/ADR-0006-independent-components-and-lifecycle.md). [modules.json](../../modules.json) is the machine-readable inventory; each owning `go.mod` declares its path and dependencies. Core uses the root tag namespace, while a child uses `<module-directory>/vMAJOR.MINOR.PATCH`; the version supplied to `go get` is the semantic version, not the repository tag prefix. Future breaking majors follow Go's module-path rules.

A **Go package** organizes imports/source. A **Go module** owns dependency requirements and release/version identity. A **repository** and a **service/process** are separate source-ownership and runtime/deployment decisions. Independent component versions can remain in one process, deployment and relational database indefinitely. Internal subsystems do not need separate Go modules, and a new repository/service still needs additional ownership/security/operational or lifetime-cost evidence.

The monolithic-to-split transition must move the root and all selected official child dependencies coherently. Old root releases contain those child package paths; combining them with split child modules can create ambiguous imports. Follow [the transition contract](../lifecycle/lifecycle-and-compatibility.md#monolithic-to-independent-module-packaging) rather than adding a child to an old root by trial and error.

A Distribution is an optional curated product-class composition, such as content/CMS or API/headless. It consumes the Foundation and keeps product delivery/domain ownership; it is not a permanent source fork or a constraint on other products.

## Public and license boundaries

An independent consumer/extension must compose through intentionally public contracts, without runtime source copying or internal imports. Keep implementation details private and artifact/license scope clear. [Licensing](../legal/licensing.md) owns notices, covered-source obligations and any separately designated Apache-2.0 SDK; an exported symbol alone does not create that SDK or require another repository.

Pinned/local/third-party dependencies must remain usable without a registry account or central runtime service. Registry discovery, signing and update delivery belong to optional release/tooling boundaries in [Lifecycle](../lifecycle/lifecycle-and-compatibility.md). A listing, license, certification and runtime permission are separate evidence.

## Executable consumption proof

[fixtures/notes](../../fixtures/notes/README.md) is a separately composed consumer with its own Go module. It pins source-backed Core/component versions in its manifests; no `replace`, `go.work`, Foundation source copying or Foundation internal import is used. `scripts/validate.sh` copies only consumer-owned source into an isolated workspace and downloads the selected components into a new module cache with normal checksums, then compares runtime/embedded source with the candidate and uses the public contracts. Every built component identity must match its own resolved pin.

The fixture is evidence for this boundary, not a real product or authorization to create a product repository. Update its affected Core/component requirements explicitly through ordinary `go get <module-path>@<reviewed-commit-or-version>`, `go mod tidy`, validation and a coherent consumer rebuild. A Foundation source update alone never updates an existing consumer executable.
