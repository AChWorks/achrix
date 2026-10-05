# Architecture Overview

## Application shape

Use a modular monolith by default: one application deployment, ordinary PostgreSQL as the first system of record, and optional infrastructure only when needed. This keeps development/debugging and local transactions simple while Modules keep domain ownership explicit.

Entry points call Application behavior; Domain owns business invariants; Infrastructure implements the technology/provider details needed by those behaviors. Create these layers only where real code exists. Do not make Domain depend on another Module's Infrastructure or on unnecessary framework details.

## Core, Modules and products

Core owns broadly required composition/capability/application/authorization/lifecycle contracts. Identity, Admin, Media, Audit, Notifications, Search, Settings and Backup & Recovery are accepted reusable ownership boundaries under the [Module model](module-model.md); their implementation remains demand-driven rather than empty prebuilt framework surface. Search/Settings require real product flows/fields, and Backup & Recovery is an operational Module/tooling boundary shaped by real recovery evidence rather than a host daemon. Release-specific Core hardening history belongs to lifecycle/GitHub evidence, not this durable architecture overview.

Content/taxonomy, commerce, payments, AI/provider behavior and other capabilities remain reusable-Module or product/infrastructure concerns according to the same placement rule; a common feature name alone does not make it Core or automatically shared. Durable job execution is a reusable infrastructure candidate when a real background workload exists, while each Module keeps ownership of the job's business meaning/effects.

A product composes the versioned Core, selected reusable Modules and its own local behavior. It owns its domain data/semantics, release, deployment and secrets. Reusing Identity does not create one central user table/account service; reusing Admin does not create a privileged business path; reusing Media does not transfer product-specific asset relationships. Sharing code does not require one central service or database for all products. Koinon provides ecosystem governance/discovery, not a runtime dependency.

Use maintained Go/native/ecosystem functionality rather than build a general-purpose framework. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns concrete infrastructure choices; [Module model](module-model.md) owns composition/placement; [Consumption](consumption-and-packaging.md) owns the product dependency and packaging boundary.

## Extraction

An in-process Module can remain so indefinitely. Extract a package/repository/service only when independent versioning, scaling, failure/security isolation, runtime, deployment cadence, ownership or multiple-consumer economics beats the added distribution/system cost. Re-evaluate transactions, authorization, failure/recovery and public contract compatibility at that boundary; splitting deployment does not preserve in-process guarantees automatically.
