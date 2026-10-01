# Architecture Overview

## Application shape

Use a modular monolith by default: one application deployment, ordinary PostgreSQL as the first system of record, and optional infrastructure only when needed. This keeps development/debugging and local transactions simple while Modules keep domain ownership explicit.

Entry points call Application behavior; Domain owns business invariants; Infrastructure implements the technology/provider details needed by those behaviors. Create these layers only where real code exists. Do not make Domain depend on another Module's Infrastructure or on unnecessary framework details.

## Core, Modules and products

Core owns broadly required composition/capability/application/authorization/lifecycle contracts. Identity management, admin, content, media, notifications, commerce, payments, search and AI/provider implementations are optional Modules or product/infrastructure concerns unless real evidence establishes a Core responsibility.

A product composes the versioned Core, selected reusable Modules and its own local behavior. It owns its data, release, deployment and secrets. Sharing code does not require one central service, shared account table or database for all products. Koinon provides ecosystem governance/discovery, not a runtime dependency.

Use maintained Go/native/ecosystem functionality rather than build a general-purpose framework. [ADR-0003](../decisions/ADR-0003-postgresql-and-optional-infrastructure.md) owns concrete infrastructure choices; [Module model](module-model.md) owns composition; [Consumption](consumption-and-packaging.md) owns the product dependency boundary.

## Extraction

An in-process Module can remain so indefinitely. Extract a service only when independent scaling, failure/security isolation, runtime, deployment cadence, ownership or multiple-consumer economics beats the distributed-system cost. Re-evaluate transactions, authorization, failure/recovery and public contract compatibility at that boundary; splitting deployment does not preserve in-process guarantees automatically.
