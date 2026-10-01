# SEO and Semantic Web

This document applies to distributions that expose public web content.

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

Use Unicode/UTF-8 throughout the public content path.

When a product needs localization, keep locale/language and presentation direction explicit enough to support both LTR and RTL output, and render semantic language/direction metadata where applicable.

Do not build a full translation subsystem before a product needs it, but avoid hardcoding one language or text direction into reusable content/domain contracts when a small local choice can preserve future support.

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
