# Operability, Performance, and Cost

## Operational simplicity

The primary Foundation implementation path should remain deployable without auxiliary infrastructure until evidence requires it.

Do not make Redis, queue supervisors, brokers, search clusters, container orchestration, or Node production runtime mandatory for a simple application.

## Health

Health/readiness checks should prove only dependencies relevant to local readiness.

Do not turn a cheap health endpoint into a synchronous probe of every external dependency.

## Logging

Use useful severity and context.

Where it materially improves diagnosis, include stable correlation/request/job/operation identifiers.

Never log secrets, tokens, passwords, private keys, authorization codes, or unnecessary sensitive data.

## Audit vs logs

Logs answer "why did the system fail?"

Audit answers "who/what performed the accountable action?"

Keep them separate.

## Performance budgets

Prefer architectural bounds such as:

- bounded query count;
- pagination;
- bounded payload size;
- bounded memory growth;
- external-call timeout;
- bounded retries;
- queue/backlog limits when queues exist.

Use representative measurements before adding architecture solely for performance.

## Capacity evolution

Scale the demonstrated bottleneck.

Examples:

```text
relational query -> index/query design -> cache -> dedicated search/read model if justified
sync effect -> durable operation -> queue if justified
local file -> object storage/CDN if justified
single process -> more workers -> specialized service if justified
```

Do not jump directly to the final distributed shape.

## Cost

Infrastructure and observability have ongoing cost:

- CPU/RAM;
- database connections/storage;
- network/egress;
- logs/traces/metrics;
- managed services;
- human operational complexity.

Architecture decisions should consider total cost of ownership, not benchmark throughput alone.

## Recoverability

For stateful systems, distinguish backup from restore.

Recovery evidence should be proportional to data/business criticality.
