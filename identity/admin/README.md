# Identity administration

`identity/admin.New(service)` supplies the Identity-owned account forms and English/Persian catalogs to the shared Admin shell. The surface uses public Identity operations; its navigation, per-account permissions, exact decimal revision preconditions and unknown-create reconciliation retain their existing boundaries. Identity owns account and session behavior; selecting a UI language grants no permission and changes no account data.

The surface registers message references in the `identity` domain and its embedded catalog data through `Surface.Catalogs`. The shell composes those catalogs once at construction with its own and the product's installed catalogs. Templates use the request-local `Request.Localizer`; the browser uses the shared plain-data formatter. Translations remain ordinary escaped text. Products can supply another locale's `identity` messages through `Config.Catalogs` and select the installed locale without copying these forms or changing this package.

Follow the [localization and migration guide](../../docs/web/localization.md) for the data format, English fallback, explicit reload and product-owned locale policy. This package remains part of the independently versioned Identity component; its Admin dependency must belong to the reviewed compatible set. Product adoption, rebuild and deployment are separate actions.
