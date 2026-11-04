# AChrix — Master Specification

This is the **canonical reference for AChrix's purpose, identity, enduring principles, boundaries and success**. It is not a detailed architecture guide, implementation plan, component catalog or documentation index.

## Mission

**AChrix** is AChWorks' coherent, reusable and versioned **product-building Foundation** for suitable web-connected applications. It makes products easier to build, extend, operate and maintain by sharing valuable capabilities without absorbing what differentiates each product. It is neither a one-time source-copy starter nor merely a collection of libraries, and it does not seek to become a universal framework. Suitable consumers may include content, SaaS, commerce, financial, media, API and automation products; no single product class is a prerequisite.

A suitable product can start with AChrix and **continue using its maintained Core and selected Modules throughout the product's lifetime** while retaining its own business behavior, domain data, policy, releases and deployment. AChrix is the preferred shared path when it reduces total lifetime cost, not a mandatory dependency for every AChWorks project. AChrix is the project and technology name; **Foundation** describes its architectural role, not a replacement product name.

**Koinon** owns cross-project governance and ecosystem coordination. **AChrix** owns its Foundation-specific architecture, public contracts, shared behavior, compatibility and releases. Koinon is not an AChrix runtime dependency, and neither project imposes AChrix's runtime or technical choices on unrelated products.

## Durable boundaries

1. **Small, composable Foundation.** Core retains only broadly necessary composition, capability identity, Application/authorization and lifecycle primitives. Modules own coherent reusable behavior and its state/assets; module ownership need not imply separate services, databases or repositories. A modular monolith is the default until a real boundary justifies extraction.

2. **Independent products and justified reuse.** Products own their differentiating behavior, profiles, domain state, permission grants, releases and deployment; optional SSO does not make a central Foundation account mandatory. When current evidence makes coherent shared behavior **more likely than not to recur across product classes**, establish its Module ownership before each product reimplements it, provided that shared ownership lowers lifetime cost. Keep uncertain/product-specific semantics local and implement only near-term needed behavior, not empty reserved Modules.

3. **One Application behavior and authorization boundary.** Human UI, HTTP/API, MCP/AI, CLI and background jobs use the same authoritative Application behavior and current permissions. For a capability with credible internal or external programmatic consumers, applicable **HTTP API and MCP coverage is part of capability completion**, with justified exceptions or honestly tracked remaining scope; an exported method alone is not remote support. Same-process callers may invoke the owning service directly. No channel grants extra authority.

4. **Long-lived, versioned consumption and extension.** Core and official Modules have **independent version units**; products pin a reviewed compatible set and extend through small public contracts, not copied Foundation source, private imports or routine per-product forks. Shared fixes belong upstream and reach consumers through deliberate upgrades. Stable semantic capability/protocol identity remains distinct from display branding. Compatibility, state migrations and recovery require evidence; separate versions do not promise live replacement, automatic deployment or arbitrary version combinations.

## Starting path

Go is the primary shared implementation language, and ordinary PostgreSQL is the first relational implementation and integration-test target. These are **supported starting choices**, not a mandate to migrate unrelated projects or a claim that every other language, database or provider works. Another justified runtime or storage profile needs an explicit integration boundary and real compatibility evidence. Auxiliary infrastructure remains optional unless a consumer's correctness or operating needs justify it.

Choose internal ownership versus maintained external implementations by security, control, standards, interoperability, portability, licensing, compatibility, performance, maintainability and lifetime cost—not delivery speed alone. External libraries and services may be used directly or through a worthwhile narrow adapter without becoming AChrix-owned. Reuse established primitives where appropriate without outsourcing enduring Foundation behavior and public semantics by accident.

## Non-goals

AChrix does not own every product domain, force every product into one runtime or process, provide a universal entity model, wrap every external library, or prebuild every possible Module, provider or service. It does not require a central account, cache, broker, search cluster, workflow service, container/orchestrator, marketplace, registry, certification platform or dynamic plugin loader. It is not a mandatory microservice platform, universal updater, host administrator or automatic deployment system. Architectural intent does not establish implementation, cross-platform compatibility, production readiness, effortless rollback, fixed-term support guarantees or measured security/performance.

## Quality contracts

Preserve default-deny authorization, secret-safe public/AI access, privacy, consistent owned state, correct time and money semantics, actionable failures for legitimate callers, useful protected diagnosis, bounded resources and recoverable effects. Uncertain external mutations require reconciliation; reverting source alone cannot restore incompatible data.

Human-facing experiences should be accessible, localized and Unicode/RTL-aware. Published content should be truthful and semantically useful for people, search and AI without coupling meaning to presentation or requiring an SEO/crawler subsystem in Core. These obligations apply when relevant; they do not authorize prebuilding every subsystem.

## Licensing and ecosystem

Keep AChrix genuinely open source and **easy to extend, but expensive to impersonate** through trustworthy official origin, compatibility, security maintenance, quality, documentation and verifiable release provenance—not obfuscation, artificial incompatibility, Core license checks, compulsory company accounts or mandatory online services. Lawful forks, compatible commercial products and independent extensions remain possible. Legal terms, incoming rights, trademarks and official release authority have their own dedicated owners.

## Proof and success

AChrix succeeds when independently owned products can **start, grow and stay maintainable on selected Foundation releases**: reuse shared capabilities without copying their implementation, preserve product-owned behavior and data, receive shared improvements through reviewed dependency updates, and make necessary state or deployment changes safely. A product may intentionally leave AChrix, but then owns its replacement, migration and future maintenance path.

Success is established by real independent consumers and verified capability, authorization, compatibility, persistence and recovery evidence relevant to each supported profile—not by documentation alone. The initial executable Foundation must be demonstrable through an independently pinned consumer, a necessary public extension, real PostgreSQL state and a shared Application/authorization path used by a real delivery adapter; this does not require every optional Module or channel. Another materially different consumer should refine reuse boundaries, but is not a prerequisite for every strongly justified Module. Humans and AI should be able to discover the intended behavior and supported development path without relying on chat history.

For products with an administration UI, routine compatible installation and upgrades should be **simple authorized product actions without user-run server commands**. AChrix owns reusable installation/update contracts and mechanics, while the product and supported adapters own real provisioning, prepared artifact activation and recovery. Each claimed server, control-panel or processor profile must be tested; no one platform becomes a Foundation-wide requirement.

## Documentation authority

**This document states the enduring direction; other documents implement and explain it.** Technical topics own their detailed rules and should remain consistent with this reference without copying it into parallel specifications. Accepted consequential decisions may retain short ADRs; implementation truth belongs to source, manifests and tests; current work belongs to Issues/PRs; delivered identity and validation belong to Git/CI/releases. Keep superseded alternatives in Git/Issue history unless their rationale still matters for future choices or supported migrations. Revise this reference only when the durable direction changes, not whenever code, work status or a provider changes.

The [Project Map](PROJECT-MAP.md) is the separate, concise navigation index for important technical documents.
