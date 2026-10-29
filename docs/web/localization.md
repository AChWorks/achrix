# File-based interface localization

AChrix supplies optional, data-only localization through `github.com/AChWorks/achrix/admin/localize`. The package is part of the independently versioned Admin component, but imports neither the Admin shell nor Core. Public product pages can use it without constructing an administration shell, database, runtime Module or global registry. Pin a reviewed compatible Admin version through ordinary Go module resolution; adding locale data does not update that dependency or a deployed product binary.

Each presentation owner supplies its own English/Persian resources and stable message domain. Products compose those resources with their own catalogs and own supported-language selection, preference persistence and reload policy. UI language is separate from original content language, dates, calendars, currency and authorization. Catalogs do not translate stored content or alter machine IDs/API codes. Rixa's operator CLI and terminal menu retain their English policy independently of web locale selection.

## Add a product domain

Keep production Go, JavaScript and templates free of embedded Persian UI translations. Put complete messages in JSON resources; use stable keys and named parameters rather than sentence fragments. A product-owned `locales/notes.en.json` can contain:

```json
{
  "schema": 1,
  "locale": "en",
  "name": "English",
  "direction": "ltr",
  "domain": "notes",
  "messages": {
    "title": {"other": "Notes"},
    "greeting": {"other": "Hello {Name}"},
    "count": {"one": "{Count} note", "other": "{Count} notes"}
  }
}
```

Its Persian file uses the same domain/keys and parameter names, `locale: "fa"`, `name: "فارسی"` and `direction: "rtl"`. It can reorder the complete message, for example `"other": "{Name}، خوش آمدید"`. English is the required baseline and fallback. A missing whole translated message uses its English source locale for plural selection; a missing plural category within a translated message uses that message's `other` form. Cardinal plural rules come from maintained Unicode/CLDR data, not language-specific product conditionals. The initial plural API accepts integral `int64` counts; it does not perform locale number/date formatting.

Files use UTF-8 and one domain/locale per document. File names are descriptive only: metadata owns identity. `Load` discovers `.json` files recursively under each explicitly supplied `fs.FS` root. Every occurrence of the same locale must agree on name and direction. Adding keys to a translated file requires corresponding English keys, and every form must preserve the baseline's named parameters. There is no ordered override precedence: duplicate domain/locale/key identities are errors. Products can extend their own domains without replacing Module catalogs.

## Use the support without Admin

Expose only the intended resource directory, for example with `fs.Sub` over an embedded `locales` directory, then load it once at startup:

```go
catalog, err := localize.Load(productLocales, installedLocales)
if err != nil {
    return err
}
view, err := catalog.For(selectedLanguage) // empty selects English
if err != nil {
    return err
}
message, err := view.Text("notes", "greeting", map[string]string{"Name": name})
if err != nil {
    return err
}
countMessage, err := view.Plural("notes", "count", count, nil)
if err != nil {
    return err
}
```

Here `productLocales` and `installedLocales` are product-selected `fs.FS` values. `Catalog.Locales()` returns a defensive available-language list; `Localizer.Locale()` supplies the selected tag/name/direction. Unsupported locale selection is an error, rather than silently inventing a language. These strings are plain data: pass them through normal `html/template` output escaping or DOM text nodes. Never convert a translated string to `template.HTML`, JavaScript source or executable template code. `{Name}` substitutes a named value; `{{` and `}}` represent literal braces. There are no catalog functions, arbitrary expressions, template execution or translated HTML.

## Compose the shared administration screens

An owning `admin.Surface` supplies its `Catalogs` filesystem and a title reference such as `admin.Text{Domain: "notes", Key: "title"}`. The shell combines its own resources, each registered Surface's resources and additional `Config.Catalogs` sources. Identity Admin and Media Admin supply their catalogs automatically through their normal constructors. Register each catalog identity once: when several product surfaces share one domain, put the shared resources in `Config.Catalogs` once rather than repeating them on every Surface.

`Config.Language` sets the instance default; the empty value means English. Optional `Config.SelectLanguage` lets the product choose from each request without making the shell own cookies, account preferences or a preferences database. The product must constrain that choice to its supported policy; the shell rejects an unsupported result before the requested operation. `Shell.Locales()` supplies available metadata for a product-owned selector. Language availability does not grant access to any screen or operation.

Every surface request receives a `Request.Localizer`. Keep it with the page data and call its methods from trusted compiled templates, for example `{{.Locale.Text "notes" "title" nil}}`. The normal `Request.Render` path retains bounded rendering and automatic escaping. The shared browser runtime consumes a safe catalog snapshot from the same selected localizer, so form messages and server-rendered labels share keys, parameters and fallback semantics. Only the shared `admin` domain and the active Surface title's domain are included in that page's browser snapshot. A product can use the Go helper independently, but the browser runtime's asset/initialization contract belongs to the composed shell; importing the helper alone does not mount HTTP assets. Browser plural counts must be safe JavaScript integers, whereas the server API accepts `int64`. Keep opaque IDs and revisions as their original strings rather than converting them into numbers. Mixed-direction values such as opaque IDs and URLs remain isolated with semantic `bdi`/`dir` or the corresponding safe DOM-parts API; translations never supply markup.

## Install another language without rebuilding

After the product has integrated this public API and been built once:

1. Configure a private product-owned catalog directory and pass `os.DirFS` for that directory through `Config.Catalogs` or `localize.Load`. Do not use a public upload directory or an entire application tree as the source root.
2. Put the new UTF-8 JSON catalog files in that directory. A language pack can contain translations for the product and shared Module domains. A partial pack is valid and uses English for missing messages.
3. Load the complete replacement catalog or construct a replacement shell. Validate it before replacing the serving instance; a failed load leaves the product's existing instance available for its normal recovery policy.
4. Select the new locale through the product's supported language preference mechanism. No application source edit or recompilation is needed for these data-only changes.

The initial implementation loads snapshots at construction. Restarting/reconstructing the shell is sufficient; there is no automatic file watcher or mutation of a catalog already in use. A product that needs live reload owns synchronized instance replacement and in-flight request handling. Old instances retain their immutable catalogs. Core does not provide a universal locale singleton, hot-plugin mechanism, translation editor or language marketplace.

## Bounds and diagnostics

All source reads and validation happen during `Load`, with no filesystem or database access per label. Catalog limits bound source/file/directory/message/locale counts, input sizes, parameters and formatted output; the public constants in [`localize`](../../admin/localize/catalog.go) own exact values. Filesystem sources and their roots remain trusted product configuration, while catalog contents are validated data. The loader rejects malformed/duplicate JSON fields, invalid UTF-8, unsupported metadata, missing English baselines, conflicting identities, incompatible placeholders and over-limit input. Errors identify the source and relevant message location; products should retain that diagnostic context in their authorized operator logs.

Unknown message/locale IDs and invalid parameters are errors. Do not hide a broken key behind an empty label. Avoid logging user-provided interpolation values merely to diagnose a catalog error. Language metadata does not make content safe or change the product's authorization, CSP or mutation-recovery rules.

## Consumer evidence and compatibility

The Notes proving consumer contains `cmd/localizationproof` and `TestLocalizationConsumerAddsFileAfterBuild`. The test builds one separate consumer executable using normal pinned dependencies, runs English/Persian, writes a third test-only locale file afterward, reruns the same binary, and verifies discovery, selection, direction, reordered escaped content, English fallback and unchanged executable identity. The third locale is test input, not a shipped default language. The actual Identity/Media browser fixture remains the real session/CSRF/Module-screen proof; it also creates third-language files at runtime and checks actual localized Module pages and their safe browser snapshots through a real HTTPS session. Passing results belong to the exact candidate's CI/Issue evidence; this guide is not a claim that every possible locale or accessibility path was tested.

The catalog-based Admin title/request/configuration migration is described in [Lifecycle](../lifecycle/lifecycle-and-compatibility.md#catalog-based-admin-presentation-migration). Rixa and other real products must adopt a reviewed published compatible dependency set under their own repository and deployment scope. This Foundation change does not update their source or running installations automatically.
