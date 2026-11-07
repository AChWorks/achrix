# Private Media administration

`media/admin.New(service)` composes a real private file-library surface through the shared Admin shell. It imports only the public Media and Admin APIs. The product still supplies Identity/Web authentication and its Application policy; Media domain/storage implementation remains independent of Admin, Identity and Audit.

Navigation and the first 25 ready metadata records require `media.List` on `media.LibraryTarget`. Refresh/next page keep the same bounded keyset contract; pages are not a shared snapshot. Seeing metadata grants neither `Read` nor `Delete`. Each download/status read separately calls the owning public exact-ID operations; each deletion supplies the exact decimal revision and owning Service evaluates its distinct capability. Revision values stay decimal strings in JSON and DOM, preserving the full int64 precondition without JavaScript numeric conversion. The UI requires explicit confirmation before deletion. This surface does not expose `Reconcile` or invent a product maintenance grant.

Upload is one multipart `file` part only, fully parsed before `Service.Create` can create durable intent. Additional files/fields/part headers, hidden transfer encoding, path-bearing filenames, malformed framing and over-limit files fail before domain creation. Parsing admits at most two operations without a queue and allocates at most two fixed 10 MiB+1 file buffers. Multipart header/framing allocations are additionally bounded by the 12 MiB request body and standard parser; these figures exclude image decoder/runtime/other Module overhead and are not a process RSS ceiling. No multipart temporary file or disk spill is created. Actual transport read deadlines apply before parsing; domain byte-derived validation remains authoritative. `Service.Formats()` supplies upload hints from the immutable effective selection: nil configuration keeps PNG/JPEG; products may explicitly select `media.CommonMIMEs()` or a supported subset, including separately selected `image/svg+xml`/`.svg`. SVG is absent from both nil defaults and `CommonMIMEs()`. Common opaque attachments require matching extension and bounded byte recognition, have no decoded dimensions and consume no image decoding slots. Explicit private SVG instead requires complete bounded XML admission and shares the two image parser/decoder slots; SVG has zero stored dimensions and no preview or public-rendering path. The screen omits unknown dimensions; selection narrows new admission without revoking reads of previously retained supported files. Filename metadata preserves accepted Unicode without rewriting it to a storage path.

Private byte responses first call public `Status`, then `Read`. Attachment headers are delayed until Media has verified the file and supplies bytes. Responses use the derived exact MIME, RFC-compatible Unicode attachment filename, `nosniff`, `private, no-store`, known length and an actual write deadline no later than the request context. A pre-byte error remains safe JSON; a transfer failure after writing aborts the HTTP response rather than appending an error document. There is no static directory or public asset URL.

The shared request-local CSRF/client controller supports these forms with native FormData and delegated handlers for bounded refreshed rows; no frontend framework is needed. A 15 second client abort never proves rollback and never replays a mutation. Returned error `asset_id` evidence can populate the separately authorized known-ID status form. A wholly lost HTTP upload response may contain no known ID: inspect the permitted library and use the product's authorized operator recovery path. Filenames may repeat, so a matching name does not uniquely identify that upload; this UI neither promises that identity nor silently uploads again. Failed upload selection clears to avoid accidental replay.

The surface owns its English/Persian catalogs in the `media` domain and registers them with `Surface.Catalogs`. The shared Admin shell loads and validates the composed catalog data once; templates and browser updates use the same request-selected locale. Upload guidance and status results are complete messages with named parameters, so translations can reorder values. Technical file extensions use Unicode LTR isolation in the plain upload-guidance text; status IDs/revisions keep their `bdi` nodes when browser messages reorder them. Catalog values and filenames remain escaped text, never translated HTML. Products can add a locale's `media` messages through `Config.Catalogs`; follow the [localization and migration guide](../../docs/web/localization.md). UI selection changes no stored content, file metadata, permissions or machine status codes.

Module-owned focused tests cover framing before Service access, admission/transport bounds, exact target dispatch, denied operations, decimal int64 CAS, empty/localized rendering, known unknown-outcome identity and delayed private attachment/error semantics. Normal pinned consumer and actual TLS/browser evidence cover the enclosing Admin contract and the common attachment extension in #67. Upload limits remain Media-owned: this slice retains 10 MiB; a future authorized settings surface may expose a typed limit only within its reviewed resource profile.

## Optional composable picker

Within the existing Media Admin surface, `GET /admin/media/picker.js` serves an optional vanilla custom element, `<achrix-media-picker>`, that other trusted Admin surfaces can include without a new framework or Module. The product must compose the Media surface and its catalogs with the same Admin shell and authenticate the browsing actor. Example markup in a product-owned Admin template:

```html
<script src="/admin/media/picker.js" defer></script>
<achrix-media-picker accept="image/png,image/jpeg"></achrix-media-picker>
```

**Per-instance endpoint (optional):** The `endpoint` attribute selects a fixed
same-origin Picker HTTP root for this one element. If omitted, the root remains
`/admin/media/picker`, preserving existing consumers. For a trusted product
Control origin with explicit selected-site routing, an example is:

```html
<achrix-media-picker
  endpoint="/admin/sites/site/site-a/media/picker"
  accept="image/png,image/jpeg"></achrix-media-picker>
```

The supplied endpoint must be a **literal path**, beginning with one `/`,
containing only nonempty ASCII `[A-Za-z0-9_-]` segments separated by one `/`,
and at most 256 characters. Absolute URLs, protocol-relative paths, empty
attributes, trailing/doubled slashes, dot segments, percent escapes, fragments,
queries, backslashes and ambiguous Unicode are invalid and **fail closed**:
there is no fallback to the default route and no fetch to the rejected target.
Products should HTML-attribute-escape trusted, already validated server-side
routes; the element does not infer an active site from cookies, Referer or tabs.

The Control origin must serve the **unchanged official** `picker.js` asset from the
selected Media component version under its existing authenticated script
route (the site-origin Media Admin script route does not automatically exist
on Control). The product must mount a **same-origin, authenticated and
explicitly site-bound** handler at that root, exposing `GET {endpoint}/messages`,
`GET {endpoint}/page[/{cursor}]` and `GET {endpoint}/asset/{id}`
with the published Picker response shapes. It must resolve the site from that
request's fixed path, verify the current Control principal's authorization to
that site and the site's `media.List` permission **before any collection
metadata**, and delegate exact choice to that same site's `media.Select`
under current policy. Do not forward site cookies, borrow the Site Admin
session, create an ambient last-selected-site route, or grant `List` to ordinary
scoped editors merely to make the picker work. These BFF/site policy concerns
are **product owned**, not new Foundation permissions or a global Media API.

Changing `endpoint` on a connected element invalidates in-flight catalog,
page and selection requests, clears old results and events, and loads the new
root's localized catalog before browsing. Disconnect/reconnect likewise
invalidates prior work. Independently mounted elements do not share endpoint,
controller, cursor, results or event state. A caller must treat each
`achrix:media-selected` result as a proposal only and reauthorize/save against
its own explicitly selected site and current revision/operation preconditions.

**Cross-surface localization:** the shared Admin shell deliberately puts only the active surface's domain (and `admin`) in the browser page. The picker must not assume `media` is there: on connect it loads its bounded static labels from authenticated `GET {endpoint}/messages` (default `GET /admin/media/picker/messages`) using the existing Media `Surface.Catalogs`, current request-selected locale and ordinary `media.List` gate. The exact selection response returns its human confirmation already formatted through that same request-local Media catalog. There is no duplicated English/Persian dictionary or separate client-side formatter. A failed/denied catalog request or language mismatch displays the current page's shared Admin localized error and **does not initialize the picker**; a reconnect or page refresh can safely retry. Products using `Config.SelectLanguage` must keep the selected locale consistent across the page and authenticated same-origin Media requests (for example, via their existing session preference), otherwise the picker fails closed instead of silently switching language. No extra media permission is implied.

The product's external script listens for the bubbling `achrix:media-selected` event. Its immutable `detail` contains `{id, filename, mime, size, width, height, revision}`, with `revision` as an exact decimal **string**. This is a proposed reference, not an executed product edit or permission token; submit it through the product's ordinary authorized Application operation, which must recheck its own business/site scope, permitted MIME and current Media state. The optional `accept` attribute filters on canonical MIME strings for user convenience; it is never a server access control.

Picker browse is bounded to 25 ready metadata records per underlying `Service.List` page (`GET /admin/media/picker/page[/{cursor}]`), then checks **`media.Select` on each exact asset** to omit currently denied choices. Each selection (`GET /admin/media/picker/asset/{id}`) goes through `Service.Select` again, verifying the current exact permission and ready state before returning typed metadata; it does not require `Read`, copy bytes, reveal content fingerprints, emit storage paths or grant publication/deletion. A permission-evaluation error fails the entire page closed with an actionable error; stale/deleted assets cannot be selected. Cursors retain Media's non-snapshot keyset semantics; an empty displayed page does not imply later pages lack selectable items. Keyboard users can use native summary/buttons and Escape returns focus to the trigger. Announcements, escaped filenames, Persian/English text and document RTL direction reuse the shell.

**Visibility boundary:** the shared Admin surface's navigation and paging still require whole-library `media.List`, which grants filename/metadata visibility regardless of individual `Select` rights. Only use this initial picker for product principals that already legitimately hold that broad collection permission. Do **not** grant global List to tenant-scoped editors merely to enable browsing: a separately justified scoped discovery contract is required before supporting them. Product-side integration in another repository remains separately authorized.

## Failure and completion semantics

JSON and multipart read deadlines/timeouts are reported as `request_timeout` with `outcome: not_executed` only before calling the owning Service. Other underlying body-read failures return safe `unavailable` with the same proven non-execution outcome; the protected diagnosis retains the original reader error and the JSON value/trailing or multipart part/body/trailing phase. Deadline-support errors also preserve their pre-dispatch non-execution evidence. Malformed input/framing remains separate and size limits retain HTTP 413. Safe Media `InputError` values expose machine `field` and `reason`; neither body values nor arbitrary exception text are returned. `permission_denied` is a genuine denial; `authorization_unavailable` is a policy evaluation failure, not a claim that the actor lacks permission.

The surface uses `Request.CheckAllowed` for presentation. If permission hints fail after a successful upload, the response remains successful with the confirmed asset identity and `warning: permissions_unavailable`; dependent actions are disabled and the localized screen explains the temporary check failure. A hint failure never falsifies a completed write. Collection/status responses preserve the same distinction. The older `Allowed` callback remains a compatibility fallback for standalone compositions.

Transport/deadline, localization, presentation and response-delivery failures call the shell's protected `Request.Diagnose` boundary with the same request context before outward mapping. Ordinary component diagnostics retain safe phase/type evidence; the product's configured hook owns provider-specific redaction and authorized retention/export. A partial HTML, JSON or attachment delivery never acquires an appended second error document. HTTP status alone never establishes that a domain write did not execute; unknown uploads retain the existing known-ID inspection/reconciliation flow.
