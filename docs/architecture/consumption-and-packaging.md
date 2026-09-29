# AChrix Consumption and Packaging

## Purpose

AChrix is shared executable software and an AChWorks Foundation for web-connected applications, not only architecture guidance or a starter repository.

A suitable product should be able to consume Foundation behavior through an explicit versioned boundary, upgrade that boundary deliberately, and keep product/domain-specific behavior outside the Foundation.

This document owns the durable **consumption and packaging constraints**. Exact PHP/Laravel package layout remains an implementation ADR decision until executable work begins.

## Target consumer shape

```text
Product repository
|
+-- thin product/application shell
|   +-- product configuration/composition
|   +-- delivery/deployment ownership
|
+-- versioned AChrix Core
|
+-- selected reusable AChrix Modules
|
+-- product-local Modules / domain behavior
```

The product remains independently owned. It keeps its own Issues, PRs, CI, releases, runtime/deployment truth, secrets, and domain-specific architecture.

**Koinon** (`AChWorks/koinon`) is an ecosystem governance/contract/discovery source and is not a product runtime dependency.

## Consumption guarantees

The initial executable Foundation must prove a path with these properties:

- the consumer can identify the Foundation version it uses;
- dependency/update intent is explicit and reviewable;
- upgrading Foundation does not require manually copying shared runtime source into every product;
- compatibility and migration requirements can be checked before activation;
- Foundation code fixes can reach consumers through the supported version/update mechanism;
- a product can keep product-specific code without editing Foundation internals;
- product divergence, when necessary, stays explicit and localized rather than becoming an invisible long-lived fork.

The first implementation may use a minimal workbench/integration consumer rather than a full product, but the proof must exercise the same supported consumption boundary intended for real products.

## Copying and forking

A generated application shell or scaffold may copy bootstrap files whose ownership transfers to the product.

Shared Foundation runtime/Core code is different: a permanent unmanaged copy/fork is not the default reuse model because consumers would silently diverge and shared security/lifecycle fixes would need repeated manual ports.

A fork may still be justified by an intentional ownership split, but then it is no longer ordinary shared-Foundation consumption and must be treated as its own maintained implementation.

## Internal module versus package

Architecture boundary and distribution boundary are separate decisions.

An internal Foundation Module may remain:

- in the same repository;
- in the same dependency/package;
- in the same process;
- in the same deployment;
- in the same relational database;

for its entire useful life.

Do not create one package/repository per Module merely because the architecture is modular.

Extract an independently versioned package/repository only when evidence such as these makes it cheaper/safer:

- multiple consumers need the same module independently;
- release/compatibility cadence differs materially from Foundation Core;
- ownership/security boundary is independent;
- consumers need selective dependency/versioning that the current packaging cannot provide cleanly;
- independent distribution reduces total maintenance or migration cost.

## Product-local to Foundation promotion

A product-specific capability begins in the product unless it is already a proven Foundation concern.

When future reuse is credible and a clean local boundary is cheap, design it so later extraction is possible without pre-building the extraction.

Promotion flow:

```text
product-local capability
        |
        | second real consumer appears
        v
compare semantics / lifecycle / ownership
        |
        +-- different enough -> stay product-local
        |
        `-- converged enough -> Foundation Module/capability
                                  |
                                  | independent packaging justified?
                                  v
                         optional package/repository extraction
```

A second consumer triggers comparison, not automatic promotion. A third converged consumer is stronger evidence, not a mandatory threshold.

## Distribution relationship

A Distribution is a maintained composition for a product class, such as CMS, SaaS, Commerce, or API/headless.

A Distribution may select Foundation Core plus reusable Modules and provide product-class defaults, but it should use the supported Foundation consumption boundary rather than become a permanent copy of shared Foundation runtime code.

A real Distribution is also evidence: repeated needs may refine Core/Module boundaries, while product-specific behavior stays outside the Foundation.

## Upgrade and compatibility

A Foundation version change is a lifecycle operation.

Where relevant, consumer upgrade must account for:

- Foundation version compatibility;
- runtime/framework compatibility;
- enabled Module compatibility;
- configuration changes;
- schema/data migrations;
- published contract changes;
- required secrets/keys;
- preflight/postflight health;
- rollback, roll-forward, or recovery.

Do not claim safe downgrade/rollback when data or public contracts make it impossible.

## First implementation boundary

Before executable code is coupled to PHP/Laravel, Issue #1 requires an implementation ADR that selects the smallest supported packaging/consumption shape from current official compatibility evidence.

That decision must:

- support a versioned Foundation dependency/update path;
- avoid permanent unmanaged copying of shared Foundation runtime code;
- prove the relationship with a separate minimal consumer;
- avoid package-per-layer or package-per-Module ceremony;
- preserve the ability to extract independently versioned Modules later when real consumers justify it.

The implementation ADR may refine mechanics without changing these durable constraints.
