# Multi-Site composition and management

The optional `github.com/AChWorks/achrix/multisite` module provides an authorized immutable exact-authority resolver and optional site inventory/Module selection planning. Its independent Go-module version uses the same repository and stable import path; the owning packaging/lifecycle contracts define consumption and migration from the historical shared release. It changes no mandatory Core concept and acquires no database, storage, network connection or worker. A single-site product may omit it entirely.

Source and Go documentation own exact signatures. The [runnable example](example_test.go) composes two declared sites through the public Core and Service boundary. It uses a product-owned principal and narrowly scoped policy; products explicitly wire the Service to the Application that composes its Module. A Module instance belongs to one Application.

| Public contract | Behavior |
| --- | --- |
| `Config{Sites: []Site}` / `New` | Trusted finite inventory, validated and copied before effects. No fixed site-count ceiling. Each site has at least one authority; duplicate IDs and duplicate authority bindings, including repeated aliases for one site, fail with `ErrConfiguration`. |
| `Site{ID, Authorities, Disabled}` | IDs match ASCII `[A-Za-z0-9][A-Za-z0-9_-]{0,63}` and retain exact case. `Disabled` defaults to false; disabled mappings remain reserved in the inventory. |
| `Module.Descriptor` | Cheap side-effect-free metadata: `achrix.multisite`, its packaged implementation version, `achrix.multisite.resolve` ABI 1 and Core authorization ABI 2. Managed construction adds the two optional management capabilities below. Each call returns fresh metadata slices. |
| `Module.Start` / `Ready` / `Stop` | One-shot context-bounded startup, local readiness and atomic terminal admission closure. Stop also closes admission when its context is canceled; there are no acquired resources to clean. |
| `NewService` / `Service.Resolve` | Typed operation authorizes the exact canonical authority, then checks owning lifecycle and returns only the stable site ID. Resolver-only construction exposes no inventory or binding data; managed construction adds explicit authorized metadata below. |

Authorities are exact lower-case ASCII DNS hostname syntax (labels of 1–63 letters/digits/hyphens with alphanumeric ends, name at most 253 bytes, a letter in the final label), canonical dotted IPv4, or bracketed canonical IPv6. IPv6 uses Go `net/netip.Addr.String()` spelling, including the canonical dotted tail for IPv4-mapped IPv6. ASCII `xn--` labels are accepted as bounded DNS syntax; the resolver performs no IDNA conversion or punycode attestation. Products own the correctness of their configured internationalized DNS names.

Each host may have an explicit decimal port from 1 to 65535 with no leading zeros. Absent port, `:443` and `:8443` are distinct inventory keys; one never substitutes for another. Schemes, paths, userinfo, query/fragment, whitespace, wildcards, trailing dots, Unicode, zones, upper-case/expanded IPv6 and legacy numeric/hexadecimal IPv4 spellings are rejected. There is no normalization, default-port inference, suffix matching, redirect, DNS query or discovery. Syntax validation is bounded by a maximum 259-byte authority before policy; construction uses linear maps and lookup work is independent of configured inventory size.

`Resolve` derives a context capped at one second, preserving an earlier caller deadline or cancellation. Malformed input returns `ErrInput` before policy and reveals no mapping existence. Valid input is authorized before binding lookup: policy denial remains Core `ErrDenied`, evaluation failure is safe `ErrAuthorizationUnavailable`, and a non-ready Application returns Core `ErrNotReady`. Unknown and disabled authorities share `ErrNotFound`; unavailable owning Module state returns `ErrUnavailable`. Errors return an empty site ID. Actual context cancellation/deadline is preserved. Policies must honor cancellation and support concurrent calls; a synchronous in-process callback cannot be forcibly terminated by this resolver.

The final lifecycle check and lookup serialize with Stop. Calls cannot resolve against a stopped Module, including a Stop during policy evaluation. Core shutdown closes/cancels/drains its policy admission before stopping Modules; a direct Module Stop closes local admission without waiting for an external callback. Products still stop ingress and drain their whole domain operations: resolution linearizes before stop and does not grant permission to continue a domain operation after shutdown. Configuration changes require a new validated composition; mutating the original Config changes nothing in a constructed Module.

Products own actual TLS, Host and trusted-proxy validation; authentication of the principal; identity-to-Application/store bindings; account/grant checks; domain draining; and data, Media, cache, jobs and recovery isolation. Resolving an address supplies neither trusted domain ownership nor account/resource access. A site ID includes no DSN, path, secret, credential or grant. Reusing this Module creates no shared account or store.

The package tests prove immutable construction, exact mapping, authorized failure, independent instances, optional Core composition, cancellation and lifecycle/concurrency behavior. The management tests additionally prove selection/dependency validation, target-specific authorization, stale/ABA preconditions and resource omission under reconstructed Applications. They do **not** establish real account/database/storage/TLS isolation or preservation of site-owned state under a shared Foundation update. Real-consumer proof remains necessary; the independent Notes composition and actual products own their persisted-state proof.

## Optional management and per-site composition

The resolver's current data and lifecycle are also the coherent owner of bounded
site inventory and selection **planning**. The product remains the owner of
durable desired-state persistence, site-to-Application/store bindings, account
grants, domain behavior and actual activation. This adds no CMS, tenant framework,
site-state engine or host administrator.

Existing `New(Config)`, `Site`, `Config` and resolver ABI 1 behavior remain valid.
Their exported struct shapes are unchanged. Choose management explicitly:

```go
module, err := multisite.NewManaged(
    multisite.Config{Sites: []multisite.Site{
        {ID: "site-a", Authorities: []string{"a.example"}},
        {ID: "site-b", Authorities: []string{"b.example"}},
    }},
    multisite.ManagementConfig{
        Available: []achrix.Descriptor{storeDescriptor, optionalDescriptor},
        Sites: []multisite.SiteSelection{
            {ID: "site-a", Modules: []string{"example.store", "example.optional"}},
            {ID: "site-b", Modules: []string{"example.store"}},
        },
    },
)
```

Descriptors above are the trusted public metadata of product-wired, compiled
Module factories. Supplying metadata neither constructs those Modules nor grants
a caller a factory/service/store handle. Products must build the selected Modules
using the same actual descriptors, validate the resulting Core composition and
acquire resources only for active Modules. Catalogue entries share the product's
compatible installed dependency set; selection does not choose a different
Foundation version for each site.

`ManagementConfig.Available` admits at most 256 descriptors. Each identity is a
bounded (1–128 byte) Core-style semantic name, implementation version is nonempty
and at most 256 bytes, and each capability/dependency list has at most 256 entries.
Descriptors are copied, valid public metadata and cannot replace Core
authorization. Per-site selection admits at most 256 unique known Module IDs.
Selected descriptors are validated with Core's exact capability ABI, required/
present-optional dependency, duplicate-provider and cycle rules, without starting
any Module. A missing required Module is rejected instead of silently enabled.

The site's zero `Generation` is valid initial desired state. The product persists
this monotonic uint64 alongside the selected IDs; `Replacement` advances only the
target's generation. Preserve it when disabling/re-enabling, transferring or
restoring the supported configuration. Never reset it to make an old plan fit.
Overflow fails with `ErrConflict`. The public opaque `Revision` binds generation,
site ID, resolver `Disabled`, selection and public catalogue metadata. Thus
A → B → A does not revive an old proposal. It is a configuration precondition,
never an authorization or a migration/data-compatibility attestation. It does not
replace the product's own CAS for other configuration or durable state.

### Public operations and permissions

| Contract | Required permission / behavior |
| --- | --- |
| `Service.Site(ctx, actor, siteID) (SiteInfo, error)` | `InventoryRead` against the exact SiteID **before** existence lookup. Returns only ID, routing-disabled flag, selected Module IDs and opaque Revision. |
| `Service.Inventory(ctx, actor, cursor, limit) (InventoryPage, error)` | `InventoryRead` against `InventoryTarget` (the explicitly empty collection target), then against each exact SiteID. A collection grant alone reveals no site. |
| `Service.PlanActivation(ctx, actor, ActivationRequest{SiteID, ExpectedRevision, Modules}) (ActivationPlan, error)` | `PlanActivation` against the exact SiteID before configuration lookup. Disabled/unknown targets fail; exact current Revision and a valid selected graph are required. |
| `ActivationPlan.Site/BeforeRevision/AfterRevision/Selection/Descriptors` | Defensive target-only metadata. Selection includes the next Generation. No other site's inventory/authorities or product secrets are returned. |
| `ActivationPlan.Replacement(current Config, input ManagementConfig) (ManagementConfig, error)` | Pure trusted-caller helper over the caller's **own supplied inputs**; validates current target/catalogue Revision and returns a defensive updated copy. No authorization, persistence, resource or live activation occurs. |

Managed construction additionally advertises `achrix.multisite.inventory.read`
and `achrix.multisite.activation.plan`, both ABI 1. It retains resolver ABI 1 and
Core authorization ABI 2. Plain `New` advertises only resolution and its Service
returns `ErrUnavailable` for management operations.

Inventory pages contain at most 100 authorized sites and have no total count.
The opaque cursor encodes only the last returned authorized SiteID, never a hidden
inventory index or denied site's identity. Unknown valid cursor identities are
ordering input, not access grants. Exact denials are omitted; policy-evaluation
failure aborts the whole page with no partial result. An empty result may simply
mean no permitted sites. After collection authorization, a binary search over the
immutable sorted IDs starts strictly after the cursor; exact-site authorization
continues through the remaining suffix until one additional permitted site or
exhaustion establishes whether another page exists. Denied sites never supply the
cursor. This avoids revisiting earlier IDs, not the policy checks needed for sparse
grants or a denied suffix. Each call has one cooperative maximum one-second
context, including all policy calls and scanning; products own total configured
inventory and policy cost. The suffix scan is linear in the remaining configured
inventory and is not a constant-time side-channel defense. Invalid
syntax/limit/cursor fails before policy, without sensitive lookup.

All management results recheck Module admission before returning, including Stop
during policy. Callers still drain complete domain work before Core shutdown.
Policy callbacks are trusted synchronous cancellation-cooperative code. Core
policy/error semantics, safe evaluation-unavailable results and actual caller
context errors are preserved. `ErrConflict` identifies a stale/overflowed
selection precondition; invalid request syntax uses `ErrInput`, invalid composition
uses `ErrConfiguration`, and an authorized missing/disabled plan target uses
`ErrNotFound`.

### Product activation sequence

1. Read the authorized target's current Revision and obtain a plan for selected
   Module IDs. This is only a proposal; showing it grants no durable permission.
2. At the actual change boundary, freshly authorize the exact site/action.
   Serialize conflicting operations and atomically compare-and-swap the product's
   persisted desired-state generation/revision. Reconcile a lost acknowledgement
   instead of replaying a mutation blindly.
3. Use the pure `Replacement` helper against the current trusted configuration,
   then persist the selected IDs and returned Generation through that protected
   product operation. Preserve unrelated sites, retained data and migrations.
4. Drain affected ingress/domain work, construct only selected typed Module
   factories, validate dependency/ABI compatibility, stage required explicit
   migrations and establish supported recovery, then start/check readiness and
   activate the new Application through the product's supported lifecycle.
5. Verify desired versus active state. A plan, saved configuration or successful
   process start alone is not proof of activation. After interruption the product
   must distinguish pending/active/failed state and reconcile before retry.

Persistence and activation are distinct boundaries and cannot be made atomic by
this in-memory planner. The product chooses its supported restart/maintenance
profile and durable progress/recovery evidence. The first API does not mutate a
running Core graph, load Go plugins, delete inactive Module data, grant domain
permissions or perform a cross-site transaction. Resolver `Disabled` keeps only its
existing routing meaning; public serving, administration and other lifecycle
states remain product-defined.

When a feature is removed from selection, its factory/start path must not run;
its data is retained for compatible re-enable. Owning Module dependencies still
apply (for example Identity requires Audit). Future actual domain Search or bulk
operations retain their own accepted work/evidence gates.

### Actionable planning failures

Trusted configuration/planning failures identify bounded field/index/precondition details while preserving `errors.Is` categories. Selected graph errors retain Core's `ErrComposition` cause, including missing versus incompatible capability ABI. No provider/database exception is involved in this pure planning boundary. Runtime syntax errors explain the caller's malformed field without echoing its value or discovering a site. Authorization still precedes site existence/selection lookup; unknown and disabled sites remain outwardly indistinguishable. Policy diagnostics belong to Core and propagate the existing context. A stale revision requires a fresh authorized read/plan, not automatic activation.
