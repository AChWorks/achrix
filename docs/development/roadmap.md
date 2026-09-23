# Development Roadmap

This roadmap describes the intended outcome sequence. GitHub Issues own live work/status.

No dates are implied.

## Outcome 0 — Durable foundation baseline

Goal: make project intent, architecture, lifecycle, AI/web/data/security principles, and recovery path authoritative and discoverable.

Completion evidence:

- canonical Master Spec;
- Project Map;
- architecture/principle/lifecycle docs;
- contributor/agent instructions;
- initial ADR;
- current next implementation outcome represented in GitHub.

## Outcome 1 — Minimal executable reusable Foundation

Goal: prove the smallest useful executable/versioned Foundation Core and a real consumer boundary without building speculative modules.

Expected work:

- confirm implementation/consumption ADR from current official compatibility evidence;
- bootstrap the chosen framework/runtime as reusable Foundation implementation rather than a reference-only application;
- prove a separate minimal consumer can use a versioned Foundation path without permanent shared-source copying;
- minimal module registration/composition;
- capability identity/metadata model;
- application boundary conventions;
- authorization primitives only as needed by the proving product;
- relational persistence baseline;
- root `achworks.yaml` Foundation descriptor;
- health/config/test/release skeleton;
- architecture fitness checks only for important proven boundaries.

Non-goals:

- plugin marketplace;
- microservices;
- universal provider framework;
- every database;
- every module.

## Outcome 2 — First real distribution: lightweight content/CMS

Goal: validate that a real application consumes and benefits from the Foundation rather than redefining/copying it.

Expected capabilities:

- identity/admin only to the extent required;
- content/pages/posts;
- media;
- SEO/semantic rendering;
- theme/presentation separation;
- structured content where useful;
- AI/machine-readable capability surface;
- safe draft/publish lifecycle;
- simple deployment.

Use real needs from this distribution to refine Module/Core boundaries. Keep CMS/product-specific behavior in the distribution unless repeated consumers prove it belongs in the Foundation.

## Outcome 3 — Lifecycle hardening

Goal: prove install/update/upgrade/migration/recovery on a stateful real distribution.

Expected evidence:

- version identity;
- compatibility checks;
- preflight;
- migration behavior;
- postflight/health;
- recovery/rollback or roll-forward semantics;
- module compatibility boundaries.

Do not extract a shared updater until mechanics repeat across real distributions.

## Outcome 4 — Second materially different consumer

Goal: test whether Foundation concepts generalize beyond content/CMS.

Choose a real product, not a synthetic benchmark.

Compare:

- which capabilities truly repeat and should be promoted from product-local to Foundation ownership;
- which remain product-specific;
- which adapters/providers vary;
- whether any module/package extraction is now justified.

## Outcome 5 — Evidence-driven reusable assets

Only after multiple consumers converge, consider:

- independently versioned reusable packages only where internal Module boundaries have earned that distribution boundary;
- starter/distribution tooling;
- shared lifecycle helpers;
- additional database/provider support;
- dedicated services;
- richer machine-readable module descriptors.

Every extracted asset must reduce total delivery/maintenance risk/cost.

## Continuous rule

At every outcome:

```text
discover -> reuse -> implement smallest useful slice -> measure -> extract only from evidence
```
