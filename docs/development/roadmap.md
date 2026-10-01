# Development Roadmap

This is the outcome sequence. GitHub Issues own current scope, decisions, execution gates and progress; documentation publication alone does not authorize implementation.

1. **Executable Foundation:** minimal Core, a necessary extension, real PostgreSQL and an isolated pinned consumer. Prove one Application capability/authorization path with a necessary HTTP/API adapter, then meaningful tests/CI and supported version evidence. [Issue #1](https://github.com/AChWorks/achrix/issues/1) owns acceptance; [ADR-0005](../decisions/ADR-0005-initial-go-consumption.md) owns the starting consumption shape.
2. **First real product:** a lightweight content/CMS consumer that uses the dependency rather than recreating Foundation code. Implement only its actual product requirements and keep them product-owned.
3. **Stateful lifecycle:** prove simple in-product Core/Module installation/update, compatible prepared composition, migration, activation and recovery on that product. Routine users should need no server commands; do not extract an updater until repeated mechanics justify it. [Issue #19](https://github.com/AChWorks/achrix/issues/19) tracks AChrix compatibility evidence and product handoff.
4. **Different real consumer:** compare reusable semantics, data ownership, provider variation, compatibility and delivery cost against a materially different product.
5. **Earned shared assets:** promote Modules or extract independent packages/services only when those comparisons justify shared ownership/distribution. Additional database/provider support needs actual compatibility evidence.

Registry/marketplace infrastructure, every delivery adapter and speculative optional Modules are not prerequisites. Stop planning when the next bounded task is discoverable and verifiable; implement and measure to resolve remaining uncertainty.
