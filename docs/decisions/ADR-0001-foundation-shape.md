# ADR-0001 — AChrix Foundation Shape

Status: Accepted. Date: 2026-10-01.

## Decision and reason

AChrix is an executable, reusable, versioned application Foundation with minimal Core and optional Modules, consumed by independently owned products. Use a modular monolith until an actual extraction boundary warrants distributed-system cost.

This shares maintained engineering capabilities without making all products one domain, deployment or central service. Product-specific behavior stays local until real consumer convergence justifies promotion; internal Module and independent package/service boundaries are separate decisions. Koinon owns ecosystem governance/contracts, not application runtime.

## Consequences and owners

The first independent consumer must prove versioned reuse, Application authorization, real persistence and lifecycle rather than copied starter behavior. [Architecture overview](../architecture/overview.md), [Module model](../architecture/module-model.md) and [Consumption](../architecture/consumption-and-packaging.md) own detailed rules. Technology choices are owned by ADR-0002/0003/0005; this ADR owns only the Foundation shape.
