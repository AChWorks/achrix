# Contributing

## Workflow

- Start from current `main`.
- Work in a focused branch when implementation work begins.
- Keep one meaningful outcome reviewable together; do not split work merely by implementation layer.
- Use PRs for substantive code once the executable foundation exists.
- Link PRs to the Issue/outcome they implement when a durable work item exists.

## Before implementation

1. Read the relevant part of `MASTER-SPEC.md`.
2. Follow the Project Map to the narrowest relevant architecture/engineering document.
3. Check the current Issue/PR state.
4. Inspect existing code/capabilities before creating a parallel mechanism.
5. Confirm whether the need belongs in Core, an internal Module, an adapter, a package, or a separate service.

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
