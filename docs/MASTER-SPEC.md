# AChrix — Master Specification

This is the **canonical reference for AChrix's purpose, identity, enduring principles, boundaries and success**. It is not a detailed architecture guide, implementation plan, component catalog or documentation index.

## Mission

**AChrix** is AChWorks' coherent, reusable and versioned **product-building Foundation** for suitable web-connected applications. It makes products easier to build, extend, operate and maintain by sharing valuable capabilities without absorbing what differentiates each product. It is neither a one-time source-copy starter nor merely a collection of libraries, and it does not seek to become a universal framework.

A suitable product can start with AChrix and **continue using its maintained Core and selected Modules throughout the product's lifetime** while retaining its own business behavior, domain data, policy, releases and deployment. AChrix is the preferred shared path when it reduces total lifetime cost, not a mandatory dependency for every AChWorks project.

**Koinon** owns cross-project governance and ecosystem coordination. **AChrix** owns its Foundation-specific architecture, public contracts, shared behavior, compatibility and releases. That distinction does not impose AChrix's runtime or technical choices on unrelated products.

## Durable boundaries

1. **Small, composable Foundation.** Core retains only the essential shared application, authorization, composition and lifecycle primitives. Modules own coherent reusable behavior; module ownership need not imply separate services, databases or repositories. A modular monolith is the default until evidence justifies another deployment boundary.

2. **Independent products and justified reuse.** Products own their differentiating behavior, domain state, permission grants and deployment. Common mechanics belong to the appropriate AChrix Module when reuse across product classes is credible and genuinely reduces lifetime maintenance; uncertain or product-specific functionality stays local. Accepted ownership is not permission to build features without a real need.

3. **Consistent behavior and authority.** Human interfaces, APIs, automation and AI-accessible channels use the same authoritative Application behavior and current permissions. A new presentation or transport channel does not acquire business authority or bypass security rules.

4. **Long-lived, versioned consumption.** Products compose reviewed compatible Core/Module versions and extend through public contracts rather than copied Foundation source, private imports or routine per-product forks. Shared changes are maintained upstream and deliberately adopted by products. Compatible evolution, state migrations and recovery must be designed and proven; independent versions do not promise live replacement or automatic deployment.

5. **Evidence-based engineering.** Choose internal ownership versus maintained external implementations by security, control, interoperability, portability, licensing, compatibility, performance and lifetime cost—not delivery speed alone. Go is the primary shared implementation; ordinary PostgreSQL is the first relational baseline where required. Other runtimes, databases and infrastructure remain optional unless a supported consumer and tested profile justify them.

6. **Safe, usable and trustworthy systems.** Protect authorization, privacy, data integrity, operational diagnosability, bounded resource use, supported lifecycle changes and recovery. Human-facing and public experiences should be accessible, internationalized and semantically meaningful. Keep the shared Foundation genuinely open source and distinguish verifiable official origin from lawful independent forks and extensions; never rely on obfuscation, artificial incompatibility or obligatory central services for that distinction.

## Non-goals

AChrix does not own every product domain, force every product into one runtime or process, provide a universal entity model, wrap every external library, or prebuild every possible Module, provider or service. It is not a mandatory microservice platform, marketplace, universal updater or automatic deployment system. Architectural intent does not establish implementation, cross-platform compatibility, production readiness, effortless rollback or indefinite support.

## Proof and success

AChrix succeeds when independently owned products can **start, grow and stay maintainable on selected Foundation releases**: reuse shared capabilities without copying their implementation, preserve product-owned behavior and data, receive shared improvements through reviewed dependency updates, and make necessary state or deployment changes safely. A product may intentionally leave AChrix, but then owns its replacement, migration and future maintenance path.

Success is established by real consumers and verified capability, authorization, compatibility, persistence and recovery evidence relevant to each supported profile—not by documentation alone. Product developers and routine operators should have clear, practical paths to build, extend, diagnose and upgrade supported products without managing Foundation internals. Different real product classes should refine reusable boundaries rather than trigger speculative abstraction.

## Documentation authority

**This document states the enduring direction; other documents implement and explain it.** Technical topics own their detailed rules and should remain consistent with this reference without copying it into parallel specifications. Accepted consequential decisions may retain short ADRs; implementation truth belongs to source, manifests and tests; current work belongs to Issues/PRs; delivered identity and validation belong to Git/CI/releases. Revise this reference only when the durable direction changes, not whenever code, work status or a provider changes.

The [Project Map](PROJECT-MAP.md) is the separate, concise navigation index for important technical documents.
