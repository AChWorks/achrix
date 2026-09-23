# AI-First and Machine Readability

## Principle

The Foundation should be easy for AI systems to understand and operate without creating a privileged bypass path.

AI-first means:

- important capabilities are explicit;
- inputs/outputs are structured;
- permissions are discoverable;
- durable state is machine-readable;
- semantic content is preferred over presentation-only blobs;
- AI and humans use the same Application behavior.

It does **not** mean the system depends on a particular AI provider or model.

## One business surface

```text
Admin UI
REST/API
MCP/AI
CLI
Jobs
   \ | /
 Application
    |
  Domain
```

Do not create separate AI-only business logic when the underlying capability is the same.

## Capability discovery

Where useful, expose machine-readable metadata for:

- module identity/version;
- provided capabilities;
- commands/queries;
- input/output schemas;
- required authorization;
- optional provider/channel features;
- compatibility/version information.

Use portable schema formats when practical.

## AI authorization

AI is an actor/interface subject to the same authorization model as any other principal.

Examples:

- AI may draft but not publish;
- AI may update content but not change payment policy;
- AI may read settings but not secrets;
- AI may execute only explicitly granted capabilities.

Never infer permission from the fact that a capability is discoverable.

## Safe mutations

For concurrent or replay-prone AI workflows, use:

- expected version/precondition where lost updates matter;
- idempotency where duplicate effects matter;
- explicit preview/draft/approval boundaries where product policy requires them;
- audit records for sensitive accountable actions.

## Semantic content

Content intended for public pages should, where useful, have a structured semantic representation independent from theme markup.

Example:

```text
Page
  title
  summary
  sections
    hero
    introduction
    features
    faq
    cta
```

Theme/rendering adapters transform that model into HTML.

A raw/custom HTML escape hatch may exist for exceptional designs but should not become the default content representation.

## Multiple representations

One source of truth may produce:

- HTML;
- JSON;
- Markdown;
- JSON-LD;
- MCP/API resources.

Avoid storing independently editable copies of the same content merely to serve different interfaces.

## AI provider independence

If AI-backed product features are introduced, isolate provider/model specifics behind a suitable boundary only when multiple providers or replacement are credible.

Do not build a universal AI orchestration framework before real workflows justify it.

## Auditability

Distinguish:

- operational AI logs;
- model/provider telemetry;
- durable business audit.

Never persist prompts/responses containing sensitive data by default merely for debugging.
