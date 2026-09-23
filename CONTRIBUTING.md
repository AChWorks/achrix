# Contributing

## Workflow

- Start from current `main`.
- Work in a focused branch for tracked changes that alter durable project intent, architecture, implementation, CI, lifecycle, metadata, or repository working rules.
- Keep one meaningful outcome reviewable together; do not split work merely by implementation layer.
- Use Pull Requests as the normal integration path for those tracked changes; validation depth should match the changed surface. Issue #1 will add executable CI and the strongest enforceable repository policy supported by the current GitHub plan.
- Link PRs to the Issue/outcome they implement when a durable work item exists.

## Before implementation

1. Read the relevant part of `MASTER-SPEC.md`.
2. Follow the Project Map to the narrowest relevant architecture/engineering document.
3. Check the current Issue/PR state.
4. Inspect existing code/capabilities before creating a parallel mechanism.
5. Confirm whether the need belongs in Foundation Core, a reusable internal Foundation Module, a product-local Module, an adapter, an independently versioned package, or a separate service.
6. For changes that affect how products consume shared Foundation code, read `docs/architecture/consumption-and-packaging.md` and preserve the supported version/update boundary.

## Definition of done

A change is done when the accepted behavior is implemented, required validation passes, relevant compatibility/lifecycle implications are handled, the diff has been reviewed, and durable documentation is updated only where a lasting rule or contract changed.

Integration and production delivery are separate facts.

## Architecture changes

Create an ADR only for lasting decisions whose rationale/constraints future maintainers will need.

Do not create ADRs for small reversible implementation choices.

## Dependencies

Before adding a dependency, compare the cost of owning equivalent custom code with the dependency's maintenance, security, license, upgrade, compatibility, and operational costs.

## Security

Never commit credentials, tokens, private keys, session material, production customer data, or restricted provider payloads.
