# Technology and Infrastructure Selection

## Foundation identity

Technology is an implementation choice, not Foundation identity.

The initial executable implementation may use PHP/Laravel today and another component/runtime may be introduced tomorrow without changing the Foundation mission.

## Initial implementation path

Initial intended executable path:

- PHP/Laravel;
- relational database, with MariaDB as the primary implementation target;
- server-rendered web where applicable;
- minimal JavaScript by default;
- no mandatory Redis, queue worker, Node production runtime, container orchestration, or search cluster.

SQLite is not part of the primary implementation/test path unless an explicit future architecture decision changes that.

## Database support

Architect for reasonable relational portability but do not claim support without tests.

A database-specific feature is allowed when it materially improves correctness/performance and is contained behind the owning Infrastructure boundary.

Where useful, modules may express required capabilities such as:

- transactions;
- foreign keys;
- row locking;
- JSON;
- full-text search;
- advisory locking.

Do not reduce every database to a lowest common denominator.

## Specialized runtimes

Use a specialized runtime only for a real workload.

Examples:

- Go for high-concurrency network/service workloads;
- Python for AI/data/ML;
- Rust for low-level/CPU/security-sensitive processing;
- Node/TypeScript for specific realtime/frontend/server workloads.

Do not move business capabilities out of the modular monolith merely because another runtime benchmarks faster in isolation.

## Infrastructure escalation

Default to the smallest adequate mechanism.

Examples:

```text
Cache: file/database -> Redis when needed
Queue: none/sync -> database -> Redis/SQS/etc when needed
Search: relational -> dedicated search engine when needed
Storage: local -> object storage when needed
```

Each escalation must have a measurable or operational reason.

## Dependency selection

Prefer maintained, boring, standard technology for commodity concerns.

A dependency must earn its maintenance/security/license/upgrade cost.
