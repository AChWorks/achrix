# SEO and Semantic Web

Semantic web/SEO sections apply to distributions exposing public web content. Internationalization and directionality also apply to other user-facing product surfaces, including administration and native clients; HTML/CSS guidance applies to their web renderers.

## Default delivery

Prefer crawlable server-rendered or pre-rendered semantic HTML for content-oriented public surfaces.

Client-side JavaScript may enhance interaction but should not be required merely to reveal primary indexable content.

## User experience

User-facing quality is part of correctness, not a cosmetic follow-up.

- Prefer predictable interaction and progressive enhancement. A simple server-rendered flow is preferable to client complexity that adds no material usability value.
- Every real asynchronous/remote flow provides the states users need to understand it: initial/loading, success, empty/no-result and actionable failure. Long-running operations expose progress/identity when the user may need to leave and return.
- Consequential/destructive actions distinguish ordinary confirmation from genuinely irreversible/high-impact decisions and do not train users to click through constant modal noise.
- Forms keep validation close to the affected field/action while preserving safe server-side validation as the authority. Errors explain what the user can do next without exposing internal diagnostics.
- Support keyboard operation, sensible focus, semantic labels, visible status/error feedback and responsive layouts for the product's actual supported viewport/input classes.
- Do not disable browser/platform accessibility, selection, zoom, password-manager/autofill or standard navigation behavior merely for visual control.
- Keep user-perceived performance in scope: avoid unnecessary blocking JavaScript, layout shifts, oversized assets and avoidable round trips; measure real product paths before introducing complex client/cache state.
- Authentication/session expiry, update/install progress and recovery failures should return users to a clear safe state rather than losing work silently.
- Validate representative real workflows, including permission denial/error states, rather than proving only ideal screenshots.

## Semantic output

Use meaningful elements and hierarchy:

- one clear page purpose;
- semantic headings;
- `main`, `article`, `section`, `nav`, `header`, `footer` where appropriate;
- meaningful links;
- structured media metadata;
- accessible labels/alternative text.

This helps users, assistive technology, search engines, crawlers, and AI extraction.

## Public content and AI retrieval

Public product output should be understandable to people, search engines and AI retrieval systems, in addition to the development and Application contracts being recoverable by AI. Products and rendering Modules own this output; Core does not become a content model, SEO engine or crawler service.

- Keep primary public information in readable semantic HTML. Identify the page's subject, relevant author/publisher, real publication/modification dates, language and stable canonical URL when those facts apply. Preserve useful source references, units and context instead of relying on images or decorative layout to convey meaning.
- Derive supported structured data, such as Article/BlogPosting or BreadcrumbList, from the same authoritative content as the visible page. A future commerce owner supplies Product/Offer facts only when that real workflow exists. Never invent prices, availability, reviews, ratings or authors to fill a schema. Use maintained vocabulary appropriate to the actual page and normal safe output serialization.
- Publishing, corrections, withdrawal and route changes keep rendered content, metadata, structured data, sitemap and served artifacts consistent under the product's declared freshness/publication contract. A theme redesign must not lose the authoritative page meaning.
- Product operators own crawling, indexing and snippet policy. Search discovery and model-training controls can differ by provider; do not collapse them into a universal AI switch. A crawler user-agent is not an authenticated principal. Authentication and resource authorization protect private content; robots/noindex directives do not replace access control or make published data private.
- Prefer existing web standards and supported provider guidance. Do not promise that compliant output will be indexed, cited, ranked, recommended or interpreted correctly by any search/AI service. Google's AI Search guidance requires no separate AI-specific schema or file, but that statement is not a guarantee or a universal contract for other providers.

Validate the actual rendered product path when implemented: primary content without client JavaScript, correct canonical/language/author/date facts, structured data matching visible content, and no private/draft/cross-site leakage through HTML, metadata, sitemaps or public assets. Add only page-type/provider checks relevant to real supported flows; this policy does not require a new broad CI suite or absent-product tests.

References: [Google AI features](https://developers.google.com/search/docs/appearance/ai-features), [structured-data policies](https://developers.google.com/search/docs/appearance/structured-data/sd-policies), [OpenAI crawler controls](https://developers.openai.com/api/docs/bots).

## Internationalization and directionality

Preserve Unicode/UTF-8 through accepted text, storage, APIs and export. Do not restrict human text to ASCII or rewrite it merely for display direction. Stable protocol/capability IDs remain separate from translated display text under [Contracts](../architecture/contracts-and-interfaces.md).

Products own supported languages, locale selection/fallback, translation resources and presentation. Distinguish interface language, content language/translated content and locale formatting; a user's interface choice must not silently change stored content. A Module owns translations or localized content semantics only when its actual capability needs them. Core needs no global locale registry, translation engine or mandatory locale field.

Keep language/locale and base direction separate: a language declaration does not itself set direction. Web renderers emit meaningful `lang` language tags and `dir="rtl"` or `dir="ltr"` for the document and genuine content-language/direction changes. Use semantic direction markup rather than alignment alone. Prefer CSS logical properties such as `margin-inline-start`, `padding-inline-end` and `text-align: start` where layout should follow direction. Mirror only direction-dependent controls; code, URLs, identifiers and media do not all reverse with the interface.

Isolate inserted mixed-direction text appropriately, for example with `bdi` for inline user text and `dir="auto"` where content direction is unknown. Preserve the stored text and apply normal output escaping; direction metadata does not make HTML safe. Date/number/currency formatting belongs at presentation boundaries and must preserve the [canonical data semantics](../data/data-and-persistence.md#durable-values).

When implementing an actual localized surface, validate a representative supported LTR/RTL path, mixed Persian/Latin text and the affected form/navigation/error behavior. Reuse unchanged evidence and target a corrected component; do not create a locale matrix or visual tests for absent UI. The initial Notes proof covers persisted Persian/Unicode text, not rendered RTL layout or translated UI.

Follow [W3C language declarations](https://www.w3.org/International/questions/qa-html-language-declarations), [HTML directionality](https://www.w3.org/International/questions/qa-html-dir) and [CSS logical properties](https://www.w3.org/TR/css-logical-1/) at renderer boundaries. Introduce translation tooling for real product needs, while keeping reusable contracts free of hardcoded language/direction assumptions.

## SEO capabilities

A content/SEO distribution should be able to manage, as applicable:

- title/meta description;
- canonical URL;
- index/noindex/follow policy;
- Open Graph/social metadata;
- structured data/JSON-LD;
- sitemap generation;
- robots policy;
- breadcrumbs;
- publication/modified timestamps;
- redirect history when slugs/routes change;
- 404/410 semantics;
- image alt/caption/dimensions/responsive variants;
- internal linking metadata;
- locale/hreflang when multilingual support exists.

Do not add fields merely because SEO tools expose them; support what the product can correctly render.

## URL stability

Public URLs are external contracts.

Changing a slug/route should preserve intentional redirect behavior when the old URL has meaningful external value.

## Draft and preview

Non-public content must not accidentally become indexable.

Preview/draft behavior should default to non-indexable and protected where appropriate.

## Theme separation

Presentation/theme should not own the only copy of page meaning.

Keep one structured semantic content source; themes/renderers derive presentation from it.

This supports redesign, AI readability, accessibility, and alternate representations.

## Machine-oriented representations

One semantic source may derive HTML, JSON, Markdown, JSON-LD or API/MCP resources where useful; do not keep independently editable copies merely for different interfaces. A custom HTML escape hatch may serve exceptional designs without becoming the default content model.

Conventions such as `llms.txt` are optional adapters, not Core architectural requirements.

## Performance

SEO-sensitive public pages should keep:

- server response work bounded;
- asset payloads proportionate;
- images optimized;
- unnecessary JavaScript low;
- caching available where useful.

Do not add a complex rendering/cache architecture before measurement.
