# SEO and Semantic Web

Semantic web/SEO sections apply to distributions exposing public web content. Internationalization and directionality also apply to other user-facing product surfaces, including administration and native clients; HTML/CSS guidance applies to their web renderers.

## Default delivery

Prefer crawlable server-rendered or pre-rendered semantic HTML for content-oriented public surfaces.

Client-side JavaScript may enhance interaction but should not be required merely to reveal primary indexable content.

## Semantic output

Use meaningful elements and hierarchy:

- one clear page purpose;
- semantic headings;
- `main`, `article`, `section`, `nav`, `header`, `footer` where appropriate;
- meaningful links;
- structured media metadata;
- accessible labels/alternative text.

This helps users, assistive technology, search engines, crawlers, and AI extraction.

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
