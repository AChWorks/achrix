# AChrix — Master Specification

Canonical project intent for `AChWorks/achrix`. Detailed rules have one owner each, located through the [Project Map](PROJECT-MAP.md).

## Mission

AChrix is a reusable, versioned AChWorks Foundation for web-connected applications. Its purpose is to shorten the path from an idea to a maintainable product by sharing useful engineering capabilities while each product keeps its differentiating business behavior.

`AChrix` is the project/technology name; Foundation is its architectural category. Pronunciation: `ATCH-riks` (Persian: `اَچ‌ریکس`). Suitable consumers include content sites, SaaS, marketplaces, commerce, financial products, media, APIs and automation; none must adopt unrelated modules or infrastructure.

Koinon owns AChWorks cross-project governance/discovery/contracts. AChrix owns its implementation, application-specific contracts, Core/Modules, packaging and releases. Koinon is not a runtime dependency or a prerequisite for public consumers to understand the usage and extension rules published here.

## Durable boundaries

1. **Minimal Core.** Core contains only broadly required composition, capability identity, application/authorization and lifecycle contracts. Useful product features do not automatically belong in Core.
2. **Owned Modules and products.** A Module owns one coherent reusable behavior domain, any durable state/assets that belong to it, and may expose a bounded set of related Capabilities. Cross-module mutations use its authorized Application boundary. Reusable Modules may own generic mechanics used inside independently owned products; products still own their domain/business semantics, product account/profile meaning, permission grants, data, releases and deployment. Optional SSO does not create shared central account ownership.
3. **Versioned reuse.** Products consume shared Foundation implementation through an explicit dependency/update boundary, rather than unmanaged source copies. Internal Modules can remain in the same repository, dependency module, process and database. Architecture separation alone does not justify distribution or service extraction.
4. **One Application surface.** Human UI, HTTP/API, MCP/AI, CLI and jobs use the same behavior and authorization. Machine access never grants extra authority. Public contracts and permissions are explicit and machine-readable where useful.
5. **Deliberate extension.** Publish a small, versioned surface; keep implementation private. Stable capability/protocol identity is separate from branding. External libraries, modules and services may be used directly or through the smallest valuable adapter without becoming AChrix-owned.
6. **Safe state and effects.** Data invariants, security, compatibility, migrations, external side effects and recovery are owned and testable. Unknown mutation outcomes are reconciled rather than blindly retried. A source upgrade is not proof of a safe deployed/data upgrade.
7. **Reuse-first placement, evidence-based implementation/extraction.** When current product knowledge makes a coherent capability more likely than not to recur across product classes, establish the reusable Module ownership boundary before the first product duplicates it. Implement only real near-term behavior, and keep uncertain/product-specific semantics local. Independent packages, repositories, services or databases still require their own evidence and cost justification.
8. **Public meaning for humans and machines.** Products exposing public web content preserve semantic, accessible and truthful output for people, search and AI retrieval. [Web](web/seo-and-semantic-web.md#public-content-and-ai-retrieval) owns the detailed publication rules; Core gains no mandatory SEO, content or crawler subsystem.

The default is a modular monolith. Reuse maintained standard/native/ecosystem capabilities before owning commodity infrastructure. [Engineering principles](principles/engineering-principles.md) define the decision and evidence rules; the [Module model](architecture/module-model.md) owns detailed placement and the accepted initial reusable Module boundaries.

## Starting path

- Go is the primary shared implementation language: [ADR-0002](decisions/ADR-0002-go-primary-implementation.md).
- Ordinary PostgreSQL is the first relational and integration-test target. Drivers, tooling and optional infrastructure defaults are owned by [ADR-0003](decisions/ADR-0003-postgresql-and-optional-infrastructure.md).
- Begin with one Foundation Go module and an independent pinned consumer: [ADR-0005](decisions/ADR-0005-initial-go-consumption.md).
- Exact supported versions, public APIs and installation/upgrade behavior require executable proof. A named technology or interface is not tested support.

Products may use another justified runtime/provider/database through an explicit boundary. This does not force migration of other repositories or require a central AChrix service.

## Non-goals

- A copied starter, a new general-purpose framework, or mandatory microservices.
- Universal entity/field/value storage, wrappers around every library, or one package/service per Module.
- Every database, provider, delivery adapter, tenant/feature-flag engine or future product feature built in advance.
- Mandatory cache, broker, workflow engine, search cluster, company account, container runtime or orchestrator.
- A marketplace, registry, certification/cloud platform or dynamic plugin runtime before a concrete need.
- Thirty-year compatibility guarantees, zero-cost migrations, or claims of measured performance/security before evidence exists.

## Quality contracts

The following are product-relevant obligations, not a demand to implement every subsystem immediately:

Preserve explicit ownership/consistency, stable public identity, correct time/money semantics and privacy; default-deny authorization and secret-safe AI/public access; actionable contracts and recoverable effects; compatible stateful upgrades and verifiable releases; bounded resources and useful diagnosis; semantic accessible web, Unicode/RTL and content independent from presentation. The [Project Map](PROJECT-MAP.md) locates each detailed rule owner.

## Licensing and ecosystem

Core remains clean, maintainable and genuinely open source. [Licensing](legal/licensing.md) owns code/artifact terms; [Governance](../GOVERNANCE.md), [Contribution Agreement](legal/cla.md) and [Trademark Policy](../TRADEMARKS.md) own stewardship, incoming rights and representation.

The aim is **easy to extend, but expensive to impersonate** through trustworthy compatibility, official Modules, security maintenance, tooling, documentation and release identity. Do not achieve it through obfuscation, confusing code, Core license checks, mandatory online services, artificial incompatibility or restrictions contradicting open-source rights. Lawful forks remain possible. Future registry/signing/certification/update services must justify their own implementation cost.

## Proof and success

The first executable outcome proves minimal Core, a necessary extension, real PostgreSQL behavior and one shared Application/authorization path from an independent consumer and a necessary delivery adapter. Do not build all adapters or optional Modules to prove the concept. The first real product/consumer is chosen from actual product need and may be content/CMS, SaaS, marketplace/commerce, API/automation or another suitable application; no product class is an AChrix prerequisite. It should consume only the accepted reusable Modules it actually needs instead of reimplementing their shared mechanics, while its product/domain semantics remain independently owned. A later materially different consumer validates/refines those shared boundaries and reveals additional reuse; it is not a mandatory prerequisite for every clearly reusable Module decision. [Roadmap](development/roadmap.md) owns this sequence; GitHub Issues own scope and execution gates.

Success means products start faster, shared fixes reach deliberately upgraded consumers, optional infrastructure stays optional, product semantics remain independently owned, and important compatibility/security/data/recovery boundaries can be verified. Reuse must reduce total delivery and maintenance cost. Humans and AI must recover the intended behavior and current work without chat history.

For products with an administration UI, routine compatible Core/Module installation and upgrades should be simple product actions without user-run server commands. Products own prepared composition and safe activation/recovery; this does not make Core an updater or host administrator.

## Documentation authority

This specification owns mission, non-goals, durable boundaries and success criteria. Topic documents own detailed rules; active ADRs own accepted choice/rationale; Git/PR/CI/release evidence owns implementation and delivery facts; Issues own live work. README is the entry point and the Project Map is the index.

Change a rule at its owner and reconcile affected links in the same change. Keep current documents limited to decisions and guidance still in force; replaced choices remain recoverable in Git/PR history. Do not mirror task status or preserve obsolete alternatives in the current rulebook. Resolve material contradictions before affected work; repository text and chat history never grant mutation authority.
