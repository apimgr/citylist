# Translating Citylist

Citylist includes embedded JSON catalogs for English (`en`), Spanish (`es`), French (`fr`), Arabic (`ar`), German (`de`), and Italian (`it`). English is the source catalog. Catalogs are loaded by `src/common/i18n`. The server and client use the same catalogs for HTML templates, API errors, OpenAPI and GraphQL descriptions, plain-text endpoints, CLI help, prompts, errors, messages, and shell completion labels. The server also serves the embedded catalogs at `/locales/{lang}.json`.

The six catalogs are embedded in both binaries and loaded through one registry. A valid `?lang=` query selects the language for the current request. It saves the one-year `lang` cookie only when the visitor has enabled preference cookies; otherwise detection uses an allowed preference cookie, the best supported `Accept-Language` match, then the configured default. `/server/preferences` also saves or clears the language selection, subject to preference consent. `server.i18n` configures the default and available languages; the cookie name (`lang`) and one-year lifetime are fixed. API errors and status messages, OpenAPI descriptions, GraphQL schema descriptions, plain-text API labels, server-rendered pages, and client help and output use the same translation registry. Standard protocol field names and codes remain fixed, and deployment-provided branding, policy, contact, and other configured content stays in the language supplied by the operator. The server-rendered consent banner is shown until a choice is recorded and supports accept, decline, and granular preference/analytics choices. Preference save, conditional CCPA opt-out, reset, and cross-device sync are implemented.

## Catalog format

Locale files are `src/common/i18n/locales/{language}.json`. They are UTF-8 JSON with two-space indentation and a `meta` object followed by nested `messages`:

```json
{
  "meta": {
    "language": "en",
    "name": "English",
    "native_name": "English",
    "direction": "ltr",
    "version": "1.0.0"
  },
  "messages": {
    "common": {
      "search": "Search"
    }
  }
}
```

Keep message keys unchanged and in parity with `en.json`. Translate values only. Keep each value a complete phrase or sentence; do not create sentence fragments that must be joined by code. Preserve placeholders exactly, including their braces and names, such as `{cities}`, `{countries}`, and `{count}`. Their position may change to match the target language.

Plural messages use CLDR categories. Supply the categories required by that language and always include `other`:

| Language | Categories |
|---|---|
| English, Spanish, French, German | `one`, `other` |
| Arabic | `zero`, `one`, `two`, `few`, `many`, `other` |
| Italian | `one`, `many`, `other` |

The Go tests compare catalog keys, placeholders, metadata, and plural categories. Whenever English keys or values change, update every locale catalog in the same change with best-effort translations. Run the project test gate after a catalog change:

```sh
make test
```

Go tests run in the configured Docker toolchain; do not run Go tests directly on the host.

## Translation guidance

- Use one consistent, neutral register for each language. Prefer clear, concise wording over literal word-for-word translation.
- Keep product names, protocol names, code, URLs, filenames, configuration keys, and literal configuration values such as `dark` and `auto` unchanged.
- Follow the language's normal punctuation, quotation, and spacing rules.
- Expect translated text to be longer than English. Review pages at narrow and wide viewport sizes and ensure labels, buttons, tables, and code examples remain usable.
- Arabic uses right-to-left direction. Review mixed Arabic/Latin text, URLs, numbers, and code so their order remains understandable.
- Do not claim translation coverage for strings that are still hard-coded in pages or binaries. Update the coverage status in this guide when those user-facing surfaces are localized.

## Current glossary

| English term | Translation guidance |
|---|---|
| Citylist | Keep as the product name. |
| API | Keep the acronym. |
| GraphQL | Keep the technology name. |
| MCP | Keep the protocol acronym. |
| ZIP code | Translate naturally as the local term for a United States postal code; keep `ZIP` in route examples. |
| GeoNames | Keep as the dataset/source name. |
| Latitude / longitude | Use the language's standard geographic terms. |

## Contributing a translation

Translation contributions may update one locale file at a time. A new language requires a complete translated catalog, metadata, registry entry, and `server.i18n.available_languages` entry; it is then embedded by both binaries. Never ship copied English text as a translation. The required language count and names in project documentation must match the embedded locale files.

No translator credits are currently recorded. Add a name to the locale metadata and this guide only with the translator's consent.
