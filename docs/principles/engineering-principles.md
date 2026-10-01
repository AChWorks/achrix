# Engineering Principles

Use these rules when an implementation or ownership choice needs judgment. Detailed data, security, contract and lifecycle rules remain with their owners in the [Project Map](../PROJECT-MAP.md).

## Reuse and ownership

Inspect maintained native/standard capabilities, existing AChWorks capabilities and suitable external open-source/commercial solutions before custom construction. Configure, extend or adapt when sufficient; direct use is preferable when a wrapper has no ownership, compatibility, security or testing value.

Compare fit, integration effort, authorization/data ownership, failure behavior, support/upgrade burden, license/commercial limits, transitive dependencies, resources and a credible export/replacement path. Internal discovery does not outrank a better external solution. Own custom behavior when differentiation, control or total cost warrants it.

## Boundaries and evolution

- Keep Core minimal. Put coherent cross-product mechanics in reusable Modules when current evidence makes reuse more likely than product-specific use; keep differentiating/product-specific semantics local. [Module model](../architecture/module-model.md) owns the placement rule and accepted initial reusable Modules.
- The owner's rough 50–60% reuse threshold is a directional decision heuristic, not a measured probability requirement. Treat it as “more likely than not across anticipated product classes,” then require coherent shared semantics/ownership and lower expected lifetime cost before choosing reusable Module ownership.
- Reuse-first **placement** does not authorize speculative **implementation**. Establish the Module boundary early when justified, but implement only behavior needed by current/near-term consumers; do not create empty packages, interfaces or generic feature catalogs merely to reserve future reuse.
- A second materially different consumer is valuable validation of shared semantics and may expose a placement mistake, but it is not a mandatory prerequisite when strong current evidence already supports a reusable Module. Conversely, a second consumer does not automatically justify sharing when semantics/ownership diverge.
- Create ports/contracts only for credible variation, ownership, security, composition or testing value. Do not hide every standard library/framework facility.
- Separate business semantics from infrastructure coupling where it materially helps testing, reuse or future change. Let infrastructure use useful vendor capabilities.
- A Module boundary does not itself require a package, repository, process, service or database. Choose distribution separately using [Consumption](../architecture/consumption-and-packaging.md).
- Prefer reversible choices under uncertainty. A clean cheap local boundary can preserve a likely future option without implementing it now; avoid scattered hardcoded single-tenant, all-users-at-once or one-language assumptions where a small choice keeps evolution feasible. Do not build tenancy/feature-flag/translation systems without a product need.
- Duplication can still be cheaper than a premature shared abstraction when reuse/semantics are genuinely uncertain. Do not use the reuse-first rule to force product-specific behavior into a generic Module.
- Introduce infrastructure or service extraction for actual correctness, failure isolation, security, resource, ownership or operating needs. Benchmarks of an isolated runtime are insufficient.
- Replace maintained functionality when recurring limitations, security/compatibility, maintenance cost or measured bottlenecks outweigh bounded adaptation.

## Dependencies, proof and documentation

Prefer established libraries for cryptography, protocols, transport, parsers, storage drivers and OAuth/OIDC. A dependency must save enough custom ownership to earn its maintenance, security, compatibility, licensing, upgrade and operational burden.

Test meaningful contracts and risk: domain invariants, Application behavior/authorization, real adapters, versioned consumer composition and supported compatibility. Add architecture checks when recurring boundary regressions justify them; do not test every hypothetical module combination or write tests that merely repeat the implementation.

Keep each durable rule at one canonical owner. Add an ADR for an accepted consequential choice whose rationale will affect later work; add documentation/process only when it changes how someone develops, operates, recovers or extends the system. Current documents explain the current system, not abandoned paths or a speculative future catalog.
