# Project Map

The document index, not a status report. Start from README/AGENTS and the relevant Issue; load only the sources needed for that decision.

| Question | Canonical source |
| --- | --- |
| Mission, durable boundaries and success | [MASTER-SPEC](MASTER-SPEC.md) |
| Development/review rules | [CONTRIBUTING](../CONTRIBUTING.md), [AGENTS](../AGENTS.md) |
| Active choices and rationale | [ADRs](decisions/) |
| Reuse, ownership judgment and dependencies | [Engineering principles](principles/engineering-principles.md) |
| Core/application shape | [Architecture](architecture/overview.md) |
| Module ownership, composition, provider variation and trust | [Module model](architecture/module-model.md) |
| Versioned product consumption and packaging | [Consumption](architecture/consumption-and-packaging.md) |
| Application/public extension contracts, AI schemas and safe mutations | [Contracts](architecture/contracts-and-interfaces.md) |
| Data, consistency, time/money and privacy | [Data](data/data-and-persistence.md) |
| Authorization, product accounts/SSO, AI access, secrets, abuse and vulnerability reporting | [Security](security/security-and-authorization.md); GitHub entry: [SECURITY](../SECURITY.md) |
| Install/upgrade, backup/restore, compatibility and official artifact evidence | [Lifecycle](lifecycle/lifecycle-and-compatibility.md) |
| Configuration, diagnosis, bounded resources and cost | [Operations](operations/operability-performance.md) |
| Semantic web, SEO, localization and machine content representations | [Web](web/seo-and-semantic-web.md) |
| Outcome sequence | [Roadmap](development/roadmap.md) |
| Artifact licenses | [LICENSE](../LICENSE), [Licensing policy](legal/licensing.md) |
| Incoming rights and private consent procedure | [CLA](legal/cla.md) |
| Canonical authority and official representation | [Governance](../GOVERNANCE.md), [Trademarks](../TRADEMARKS.md) |
| Stable machine-readable Foundation identity | [achworks.yaml](../achworks.yaml) |
| Live work, review and exact validation | [Issues](https://github.com/AChWorks/achrix/issues), [PRs](https://github.com/AChWorks/achrix/pulls), Git/CI evidence |

[Koinon](https://github.com/AChWorks/koinon) owns generic AChWorks cross-project governance/contracts; it is not a runtime or public-consumer documentation dependency. Repository scope still comes from explicit assignment, not this map.

Use the [documentation authority rule](MASTER-SPEC.md#documentation-authority) when sources conflict. Do not use a removed choice, historical chat, document presence or technical access as current decision/implementation evidence.
