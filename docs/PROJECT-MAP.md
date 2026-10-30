# Project Map

The document index, not a status report. Start from README/AGENTS and the relevant Issue; load only the sources needed for that decision.

| Question | Canonical source |
| --- | --- |
| Mission, durable boundaries and success | [MASTER-SPEC](MASTER-SPEC.md) |
| Development/review rules | [CONTRIBUTING](../CONTRIBUTING.md), [AGENTS](../AGENTS.md) |
| Active choices and rationale | [ADRs](decisions/) |
| Reuse, ownership judgment and dependencies | [Engineering principles](principles/engineering-principles.md) |
| Core/application shape | [Architecture](architecture/overview.md) |
| Module ownership, composition, provider variation and trust, including Search/Settings/Backup & Recovery placement | [Module model](architecture/module-model.md) |
| Independent component consumption, extension and packaging | [Consumption](architecture/consumption-and-packaging.md), [module inventory](../modules.json), [ADR-0006](decisions/ADR-0006-independent-components-and-lifecycle.md) |
| Application/public extension contracts, internal/external HTTP API and MCP coverage, schemas and safe mutations | [Contracts](architecture/contracts-and-interfaces.md), [coverage and completion](architecture/contracts-and-interfaces.md#internal-and-external-programmatic-coverage); [Issue #120](https://github.com/AChWorks/achrix/issues/120) owns the component coverage rollout |
| Optional MCP resource-server integration, current user authority and supported transport profile | [Bridge](../bridge/README.md), [Notes consumer proof](../fixtures/notes/README.md#optional-mcp-bridge); [Issue #5](https://github.com/AChWorks/achrix/issues/5) owns remaining OAuth/client/product integration evidence |
| Data, consistency, time/money and privacy | [Data](data/data-and-persistence.md) |
| Authorization, product accounts/SSO, AI access, secrets, abuse and vulnerability reporting | [Security](security/security-and-authorization.md); GitHub entry: [SECURITY](../SECURITY.md) |
| Install/upgrade, backup/restore/recovery profile, compatibility and official artifact evidence | [Lifecycle](lifecycle/lifecycle-and-compatibility.md); [installation tooling](../installation/README.md) owns the first reusable assessment/re-entry API |
| Deployment/runtime configuration versus durable Settings, diagnosis, bounded resources and cost | [Operations](operations/operability-performance.md) |
| Semantic web, SEO, localization and machine content representations | [Web](web/seo-and-semantic-web.md), [file-based localization](web/localization.md) |
| Go analysis tools, vulnerability triage and Foundation benchmarks | [Go quality](development/go-quality.md) |
| Development Identity/Audit/Media contracts, web security, atomic events and retained-state/asset limits | [Identity](../identity/README.md), [Audit](../audit/README.md), [account HTTP/MCP workflow](../identity/remote/README.md), [Audit HTTP/MCP pages](../audit/remote/README.md), [Media](../media/README.md), [private Media HTTP/MCP workflow](../media/remote/README.md) |
| Durable actor-bound request attribution, Audit schema upgrade and retained consumer restore | [Security](security/security-and-authorization.md#audit), [Audit attribution schema boundary](lifecycle/lifecycle-and-compatibility.md#audit-attribution-schema-boundary), [consumer composition and restore](../fixtures/notes/README.md#optional-identity-and-audit-http-and-mcp-composition) |
| Optional exact-authority resolution, authorized HTTP/MCP site reads and per-site activation planning | [Multi-Site](../multisite/README.md), [remote contracts](../multisite/remote/README.md), [consumer composition proof](../fixtures/notes/README.md#optional-multi-site-resolver-composition); products own isolation, persistence and actual activation. [Issue #4](https://github.com/AChWorks/achrix/issues/4) owns broader consumer-proof evidence rather than API semantics |
| Development administration shell, Module-owned forms and browser proof | [Admin](../admin/README.md), [Identity Admin](../identity/admin/), [Media Admin](../media/admin/README.md), [consumer proof](../fixtures/notes/README.md#admin-browser-proving-composition) |
| Outcome sequence | [Roadmap](development/roadmap.md) |
| Artifact licenses | [LICENSE](../LICENSE), [Licensing policy](legal/licensing.md) |
| Incoming rights and private consent procedure | [CLA](legal/cla.md) |
| Canonical authority and official representation | [Governance](../GOVERNANCE.md), [Trademarks](../TRADEMARKS.md) |
| Stable machine-readable Foundation identity | [achworks.yaml](../achworks.yaml) |
| Live work, review and exact validation | [Issues](https://github.com/AChWorks/achrix/issues), [PRs](https://github.com/AChWorks/achrix/pulls), Git/CI evidence |

[Koinon](https://github.com/AChWorks/koinon) owns generic AChWorks cross-project governance/contracts; it is not a runtime or public-consumer documentation dependency. Repository scope still comes from explicit assignment, not this map.

Use the [documentation authority rule](MASTER-SPEC.md#documentation-authority) when sources conflict. Do not use a removed choice, historical chat, document presence or technical access as current decision/implementation evidence.
