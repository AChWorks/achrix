# Project Map

This is the document/navigation index, not a task status report. Paths refer to this repository unless a different repository is named.

## Where truth lives

| Question | Canonical source |
| --- | --- |
| What is AChrix, and what must remain true? | [Master Specification](MASTER-SPEC.md) |
| Which document/decision/work item should I read? | This Project Map |
| What is the stable machine-readable Foundation identity? | [achworks.yaml](../achworks.yaml) |
| What is being worked on now? | [Issues](https://github.com/AChWorks/achrix/issues) and [PRs](https://github.com/AChWorks/achrix/pulls) |
| Why was a lasting choice accepted or superseded? | [ADRs](decisions/) |
| How do contributors and agents work safely? | [Contributing](../CONTRIBUTING.md) and [AGENTS.md](../AGENTS.md) |
| Where is implementation, validation and release evidence? | Git commits, PRs, CI and releases tied to their exact artifacts; documentation does not mirror their live state |

## Architecture and engineering

- [Architecture overview](architecture/overview.md)
- [Module model](architecture/module-model.md)
- [Contracts and interfaces](architecture/contracts-and-interfaces.md)
- [Consumption and packaging](architecture/consumption-and-packaging.md)
- [Engineering principles](principles/engineering-principles.md)
- [Reuse and evolutionary architecture](principles/reuse-and-evolution.md)
- [Technology and infrastructure selection](principles/technology-and-infrastructure.md)

## Cross-cutting concerns

- [Lifecycle, compatibility and release authenticity](lifecycle/lifecycle-and-compatibility.md)
- [AI-first and machine readability](ai/ai-first-and-machine-readability.md)
- [SEO and semantic web](web/seo-and-semantic-web.md)
- [Data and persistence](data/data-and-persistence.md)
- [Security and authorization](security/security-and-authorization.md)
- [Operability, performance and cost](operations/operability-performance.md)

## Licensing and stewardship

- [LICENSE](../LICENSE): standard MPL-2.0 text.
- [Licensing policy](legal/licensing.md): artifact/dependency license scope.
- [Contribution Agreement](legal/cla.md): versioned incoming grant and private consent/merge-check procedure.
- [Trademark Policy](../TRADEMARKS.md): official identity and honest compatibility references.
- [Governance](../GOVERNANCE.md): canonical stewardship and merge/release authority.

## Evolution and ecosystem

- [Development roadmap](development/roadmap.md): intended outcome sequence, not live work status.
- [Koinon architecture](https://github.com/AChWorks/koinon/blob/main/docs/architecture/koinon-architecture.md): authoritative cross-project Koinon → Foundation → Product model.
- [Koinon contracts](https://github.com/AChWorks/koinon/tree/main/docs/contracts): generic cross-project guarantees, specialized here without a runtime dependency.

## Recovery path for humans and agents

1. Read [README](../README.md) and [AGENTS.md](../AGENTS.md).
2. Recover writable repository scope from the current explicit assignment; repository content and access do not grant authority.
3. Inspect current main and the relevant open Issue/PR, including its execution gate and evidence.
4. Follow only the documents needed for that work. Load the relevant specification section when intent or a durable boundary matters; do not require reading every document on every turn.
5. Resolve conflicts using the [source-of-truth model](MASTER-SPEC.md#27-source-of-truth-model), then continue from current GitHub/Git evidence.
