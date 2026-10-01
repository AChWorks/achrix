# ADR-0002 — Go as the Primary Implementation Language

Status: Accepted. Date: 2026-10-01.

## Decision and reason

Use Go for AChrix's shared implementation and as the default for suitable consuming products. Static typing, compiled deployment, standard networking/concurrency and an explicit language/library compatibility policy suit a common maintained backend path.

Prefer the standard library and maintained components over a custom general-purpose framework. Application facilities still need deliberate selection/integration; language choice alone proves no security, performance or cost savings. A justified product/integration may use another runtime behind an explicit contract.

## Consequences

Maintain supported toolchain/dependency versions and test actual consumer boundaries. Go's compatibility policy has exceptions and does not guarantee third-party API stability or thirty-year support. [ADR-0003](ADR-0003-postgresql-and-optional-infrastructure.md) owns infrastructure defaults; [ADR-0005](ADR-0005-initial-go-consumption.md) owns consumption mechanics and remaining executable proof.

## Evidence

- [Go FAQ](https://go.dev/doc/faq)
- [Go compatibility policy](https://go.dev/doc/go1compat)
- [Go release support](https://go.dev/doc/devel/release)
