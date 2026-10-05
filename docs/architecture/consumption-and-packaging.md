# Consumption and Packaging

## Supported reuse

A suitable product consumes a versioned Foundation dependency plus selected reusable Modules, while its thin composition shell and product/domain behavior remain independently owned. [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) selects the starting Go module/consumer mechanics; exact supported versions and APIs need executable evidence.

The consumer must identify/pin its Foundation version, deliberately review updates and check compatibility/migrations before activation. Shared security and lifecycle fixes must be obtainable through this path. Products rebuild, validate and redeploy their binaries to receive dependency updates; changing the Foundation source does not update a deployed product automatically.

Generated bootstrap files may transfer to the product. Shared runtime is not an unmanaged permanent source copy. A justified fork has explicit independent ownership/maintenance and follows [Trademark Policy](../../TRADEMARKS.md); ordinary dependency consumption remains the default shared path.

## Start a product

Choose an exact reviewed release from [GitHub Releases](https://github.com/AChWorks/achrix/releases), then keep that exact dependency identity in the consumer's module files and build evidence. AChrix remains pre-v1 until explicitly changed. Existing consumers must apply every relevant step in the [lifecycle and migration guide](../lifecycle/lifecycle-and-compatibility.md) before adopting a newer release. Use the toolchain and database/storage profile from the current [supported environment](../operations/operability-performance.md#supported-environment) and the owning Module guides linked below.

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

All official packages below share the same Foundation version; do not select unrelated per-package “latest” versions. Import, construct and pass only needed runtime Modules to `achrix.New`. An unconstructed Module opens no pool or worker. Packages outside the imported package graph need not be compiled into that executable; shared Go module requirements/versioning are still shared.

| Need | Public entry and owning guide | Required composition / product obligation |
| --- | --- | --- |
| Accounts, passwords and sessions | [Identity](../../identity/README.md): `identity.NewPostgres`, `identity.NewService`, `identity.NewWeb` | Identity requires Audit and Core authorization ABI 2. Product owns profiles, explicit grants and trusted HTTPS ingress. |
| Accountable events and bounded query/export | [Audit](../../audit/README.md): `audit.NewPostgres`, `audit.NewService` | Identity/Audit use the same supported PostgreSQL consistency boundary. Atomic append uses the caller's native transaction; it is not distributed atomicity. |
| Private original attachments and clean image preparation | [Media](../../media/README.md): `media.NewPostgres`, `media.NewService` | Core authorization ABI 2; private Linux storage and explicit product permissions. Default PNG/JPEG; explicit SVG opt-in is private-only. `PreparePublicImage` is a separate exact-asset capability and grants no publication or public URL. |
| Optional exact authority-to-site resolution | [Multi-Site](../../multisite/README.md): `multisite.New`, `multisite.NewService` | Optional capability using Core authorization ABI 2. Product owns trusted ingress and all site-bound accounts/data/storage/configuration; single-site products may omit it entirely. |
| Account/Media administration | [Admin](../../admin/README.md): `admin.New`, `Handler()`, `ConfigureServer`; [Media surface](../../media/admin/README.md) | Compose Identity/Web authentication plus explicit owning surfaces and product policy. The presentation shell is mounted separately, not an `achrix.Module`; owning database Modules still participate in Core lifecycle. |

For a stateful product, follow the owning public signatures/examples:

1. Supply product-owned configuration/secrets, supported PostgreSQL credentials and, if needed, an exclusive mode-`0700` private Media directory outside webroots.
2. Run only participating Modules' public `Migrate(ctx, dsn)` operations explicitly with deadlines, serialized by the product's install/deploy path. Preserve published SQL and ledgers; requests/startup do not install schema.
3. Construct typed Modules, compose the Application with a fail-closed product Policy, then construct the owning Services. Construct Audit Service before Identity Service; Identity and Audit must use the same supported database profile.
4. Start and check readiness before opening ingress. Authentication supplies a principal; each domain operation separately authorizes its capability/target. Public authentication admission, navigation, collection metadata, object bytes and mutation grants are distinct.
5. On shutdown, stop ingress and drain product-owned requests/transactions before shutting down the Application. Deadlines bound cooperative work; in-process Modules are trusted code.

[Notes](../../fixtures/notes/README.md) and its [Media](../../fixtures/notes/README.md#private-media-proving-composition)/[Admin browser](../../fixtures/notes/README.md#admin-browser-proving-composition) compositions demonstrate public API wiring, product-owned policy and normal pinned consumption. They are fixtures, not a production starter distribution; copy only product-owned example/bootstrap material you deliberately maintain, never shared runtime or private implementations.

### Extend and upgrade without losing product code

Keep product/domain packages, schemas, roles, content/media relationships, configuration and deployment code in the product. Implement supported public interfaces or use typed public collaborators; request a Foundation contract extension when no public seam fits rather than patching dependency-cache files or importing private implementation. A deliberate fork has its own maintenance/merge obligations.

For an upgrade, choose an exact reviewed version, inspect its release/migration notes and public/dependency/schema diff, then:

```bash
# Set this to the exact reviewed target version before running.
go get "github.com/AChWorks/achrix@$ACHRIX_VERSION"
go mod tidy
go mod verify
go test ./...
go build ./...
```

Validate affected product behavior, stage explicit migrations and a coherent rebuilt product artifact, then activate through its supported deployment/recovery profile. Retain `achrix.Version()`, participating `Descriptor.Version` values, product build and migration identities. Updating a dependency preserves separately owned product source; a breaking API or schema change can still require deliberate adaptation. Code rollback does not reverse data or external effects.

The first real product must identify its minimum real workflow and enabled Modules, policy, data/storage and deployment profile. CMS/content is one candidate; AChrix owns no CMS content model. Aggregate capacity, a product update UI and production backup/recovery remain product evidence; [#19](https://github.com/AChWorks/achrix/issues/19) retains its separate execution gate.

## Module installation

Compose trusted Modules at build time through public contracts into a pinned, tested product executable/image. Core and internal Modules in one Go module share its dependency version; separately packaged Modules have their own versions. Their `Descriptor.Version` reports that actual source-backed dependency identity under the [public version contract](contracts-and-interfaces.md#capability-abi-and-composition); development/replacement labels are explicit and never an invented release.

Installing/updating code selects or builds a compatible product artifact. An already packaged optional Module may be enabled/disabled through explicit product configuration under dependencies, authorization and data-lifecycle rules. **Code/artifact presence, advertised capability availability, runtime activation/resource acquisition, authorization and durable state/data removal are distinct.** Enabling a path never grants permission by itself; disabling one never implies uninstalling code, reversing migrations or deleting retained data. A change that alters the composed Module/capability graph normally takes effect through construction, validation and activation of a new Application instance/product artifact under the product lifecycle; it does not imply mutating the running graph, hot-unloading Go code or a dynamic plugin runtime.

The same distinction applies inside a Module when a real optional subsystem is justified. Product composition may omit that subsystem, or include its code while keeping its runtime capability inactive, according to the owning Module's explicit supported contract. If omitting a subsystem from the build materially avoids dependencies or artifact/resource cost, use an explicit package/composition boundary where useful; this does not by itself create a new Module, Go module, repository or service.

Standard distributions offer prepared compatible combinations; custom compositions use a trusted publisher/build pipeline. A UI may identify a Core/Module change while applying a coherent product artifact. [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#product-update-experience) owns the simple user-facing update flow and activation/recovery. External integration remains available under Module model; Core needs no source/archive loader, marketplace or build service.

## Module ownership versus distribution

The [Module model](module-model.md) decides whether a capability belongs in Core, a reusable AChrix Module or the product. That ownership decision is deliberately separate from packaging/distribution.

A reusable Module normally begins in the existing AChrix repository and Foundation Go module, and may share process, deployment and relational database with Core/other Modules indefinitely. This captures reuse before the first product duplicates the behavior without prematurely creating repository/module/service overhead.

Keep the distribution boundaries distinct. A **Go package** is primarily an import/code-organization boundary; an optional package that no consumer path imports need not enter that compiled package graph. Packages inside the same **Go module** still share that module's dependency requirements and version/release identity. A separate Go module creates an independent dependency/version boundary and may still live in the same repository, but it also adds its own compatibility, tagging, upgrade and CI/release burden. A **repository** and a **service/process** are separate ownership/source and runtime/deployment decisions again. Package splitting alone therefore does not prove independent versioning, while dependency weight/conflicts, materially different release cadence or independently consumed compatibility may eventually justify a separate Go module.

Independent packaging/versioning needs additional value, such as:

- consumers needing selective independent versions/dependencies, including material optional dependency weight/conflict that should not burden unrelated consumers;
- materially different release/compatibility cadence;
- independent ownership/security boundary;
- deployment/runtime isolation that is worth the distributed-system cost;
- lower total migration, coupling and maintenance cost.

A Distribution is an optional curated product-class composition, such as content/CMS or API/headless. It consumes the Foundation and keeps product delivery/domain ownership; it is not a permanent source fork or a constraint on other products.

## Public and license boundaries

An independent consumer/extension must compose through intentionally public contracts, without runtime source copying or internal imports. Keep implementation details private and artifact/license scope clear. [Licensing](../legal/licensing.md) owns notices, covered-source obligations and any separately designated Apache-2.0 SDK; an exported symbol alone does not create that SDK or require another repository.

Pinned/local/third-party dependencies must remain usable without a registry account or central runtime service. Registry discovery, signing and update delivery belong to optional release/tooling boundaries in [Lifecycle](../lifecycle/lifecycle-and-compatibility.md). A listing, license, certification and runtime permission are separate evidence.

## Executable consumption proof

[fixtures/notes](../../fixtures/notes/README.md) is a separately composed consumer with its own Go module. It pins a source-backed Foundation pseudo-version in its manifest; no `replace`, `go.work`, Foundation source copying or Foundation internal import is used. `scripts/validate.sh` copies only consumer-owned source into an isolated workspace and downloads Foundation into a new module cache with normal checksums, then builds and uses its public contracts. The build's identity must match the resolved pin.

The fixture is evidence for this boundary, not a real product or authorization to create a product repository. Update its Foundation explicitly with `go get github.com/AChWorks/achrix@<reviewed-commit-or-version>`, `go mod tidy`, validation and a coherent consumer rebuild. A Foundation source update alone never updates an existing consumer executable.
