# ADR-0001 — AChrix Foundation Shape

Status: Accepted

Technology note: the initial PHP/Laravel expectation below is historical and superseded by [ADR-0002](ADR-0002-go-primary-implementation.md). The initial MariaDB target below is superseded by [ADR-0003](ADR-0003-postgresql-and-optional-infrastructure.md). The Foundation shape and other constraints in this decision still apply.

## Context

AChWorks needs a reusable base that can accelerate many future web-connected products without requiring each idea to repeat common architecture decisions.

The base must remain lightweight for small products while leaving credible paths to larger commerce, financial, media, SaaS, API, and AI-enabled systems.

Two failure modes must be avoided:

1. building a new framework/platform ecosystem before product needs justify it;
2. coupling every product so tightly to today's framework/database/provider choices that future evolution requires unnecessary rewrites.

## Decision

Create `AChWorks/achrix`, branded **AChrix**, as an executable, reusable, versioned application implementation Foundation that suitable products can consume while keeping product/domain-specific behavior in their own repositories.

`AChrix` is the project/technology name; `Foundation` is the generic architectural category AChrix fulfills for web-connected applications.

**Koinon** (`AChWorks/koinon`) remains the ecosystem governance/discovery/contract layer and is not a runtime dependency of this Foundation.

Use these boundaries:

- modular monolith by default;
- minimal Core;
- coherent internal Modules;
- Ports/Adapters only where credible variation/ownership/security/composition warrants them;
- stable published contracts and module-owned data;
- AI/human interfaces over shared Application capabilities;
- lifecycle/update/upgrade/recovery as first-class concerns;
- evidence-driven infrastructure and extraction;
- a supported versioned consumer boundary so shared Foundation runtime code is not permanently copied/forked into each product.

The initial executable implementation path is expected to use PHP/Laravel with MariaDB as the primary relational implementation engine, subject to implementation-time verification.

These technologies are initial implementation choices, not Foundation identity.

SQLite is excluded from the primary persistence path.

A module boundary does not imply a separate package, plugin, repository, service, deployment, or database.

A separate product should consume shared Foundation behavior through a deliberate version/update boundary. An unmanaged long-lived copy or fork of shared Foundation runtime code is not the default reuse model. Exact initial packaging mechanics are decided in the implementation ADR required by Issue #1.

## Consequences

Positive:

- fast idea-to-product path;
- strong reuse of mature ecosystem capabilities;
- lower initial operational cost;
- future provider/database/service evolution remains possible at meaningful boundaries;
- AI and human tooling can share stable application capabilities;
- modules can later be promoted/extracted if real consumer evidence justifies shared ownership and independent versioning;
- Foundation fixes can flow through a supported version/update path instead of repeated manual source copying.

Costs:

- architecture requires discipline to prevent cross-module shortcuts;
- portability is intentional rather than automatic;
- some future engine/service changes may still require migration work;
- avoiding premature abstraction requires repeated evidence-based judgment.

## Rejected alternatives

### Build a custom framework immediately

Rejected because it would recreate mature framework capabilities without evidence that ownership cost is justified.

### Start with microservices

Rejected because distribution/failure/operations complexity is not justified for the initial product scope.

### Make every technology interchangeable from day one

Rejected because lowest-common-denominator abstractions and broad compatibility testing would slow the project without real consumers.

### Keep no module boundaries until later

Rejected because ownership/coupling mistakes become expensive to unwind, especially for AI, lifecycle, data, and future service extraction.

### Treat the Foundation as a reference/starter copy

Rejected because permanent copies/forks of shared runtime code would diverge across products, making security fixes, compatibility changes, and lifecycle improvements repeated manual work instead of shared maintenance.

## Revisit triggers

Revisit only with concrete evidence such as:

- recurring framework limitations across real products;
- a second/third database requirement with meaningful shared compatibility needs;
- module extraction justified by independent scaling/security/runtime/ownership;
- repeated lifecycle mechanics justifying shared updater/installer implementation;
- real plugin ecosystem requirements;
- measured operational/performance constraints.
