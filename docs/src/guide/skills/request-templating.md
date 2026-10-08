```yaml
name: request-templating
description: Stored parameterised JSON requests — the {target, variables, body} wrapper, $var / {{}} / $when substitution, the nine variable types, directory discovery, hot reload, and the PULSE_TEMPLATE_* family. Not expr-lang.
kind: design
type: guide
applies_to: process, compose, sample, facet, predict
covers: [templates, variables, substitution, PULSE_TEMPLATES_DIR]
requires: [capability:templates]
```

# Request templating

Stored JSON that renders into a **validated typed request** — shape fixed once, caller supplies only variables. Rendering is execution-free.

**Not expr-lang.** `$var` / `{{}}` / `$when` are *request-authoring* parameters substituted before decode; the formula attribute and expression filter run expr-lang over *row fields* at execution time ([`expression-language`](expression-language.md)). No interop: a formula string in a body is inert text to the renderer.

## File shape

`{"name"?, "description"?, "target", "variables"[], "body"}`; unknown top-level keys rejected. `target` ∈ `request | composed | chain | facet | sample` (lowercase) picks the strict-decode request root — required, never inferred. `name` derives from the file path; a disagreeing `name` key is rejected. `body` is a non-empty object, deliberately **not** runnable as-is.

## Substitution — three forms

| Form | Rule |
|---|---|
| `{"$var":"bucket"}` | Marker **iff** `$var` is the only key and its value a non-empty string; replaced whole, **type-preserving** — `{"interval":{"$var":"bucket"}}` → `{"interval":10}`, never `"10"`. `{"$var":"x","other":1}` is literal data. |
| `"{{name}}"` | Natural text, inside a **string value** only; `{{ name }}` works, `{{{{` escapes a literal `{{`. `list`/`period` have no text form → `_VAR_TYPE`. `{{` in an object **key** is rejected. |
| `{"$when":"segs",…}` | Survives iff the variable resolved — **presence, not truthiness**: drops only when unsupplied AND undefaulted; supplied `""`/`[]`/`0`/`false` KEEPS it. Key stripped either way. Guards run **before** descent, so markers in a dropped block never raise. Array drops compact the slice; root-level `$when` errors. |

## Variable types (nine)

`string`, `number`, `integer` (`1.0` yes, `1.5` no), `boolean`, `field` (a string), `enum` (+`values`, case-sensitive), `list` (+`items`, scalar only — no nesting), `date`, `period`.

- **`date`**: only `YYYY-MM-DD` (`03/04/2024` is ambiguous).
- **`period`**: exactly one of `ranges` or `table`, mirroring the labeled date-range operators' `Params` shape; unknown keys in it or in a `{label,start,end}` range are rejected.

## Worked example

`<root>/finance/revenue.json` → `finance/revenue`:

```jsonc
{"target":"request",
"variables":[{"name":"metric","type":"field","required":true},
 {"name":"bucket","type":"integer","default":10},
 {"name":"segs","type":"list","items":"string"}],
"body":{"label":"total {{metric}}","cohort":{"filename":"sales.pulse"},
 "aggregations":[{"type":"<aggregator>","field":{"$var":"metric"}}],
 "groups":[{"type":"<binning grouper>","field":{"$var":"metric"},"interval":{"$var":"bucket"}},
  {"$when":"segs","type":"<category grouper>","field":"region","include":{"$var":"segs"}}]}}
```

`<…>` = any operator the manifest lists for the slot; render never inspects `type`.

`RenderTemplateRequest("finance/revenue", map[string]any{"metric":"revenue"})` →

```json
{"label":"total revenue","cohort":{"filename":"sales.pulse"},
"aggregations":[{"type":"<aggregator>","field":"revenue"}],
"groups":[{"type":"<binning grouper>","field":"revenue","interval":10}]}
```

`bucket` defaulted, splicing as the **number** `10`; the `segs`-guarded grouper dropped and the array compacted.

## Discovery + lifecycle

`Options.TemplateDirs []string`, else `PULSE_TEMPLATES_DIR` split on `os.PathListSeparator` (`:` Unix, `;` Windows). Name = path relative to its root minus `.json`, forward-slash separated. Roots are ordered precedence — **first root wins**; losers land on the winner's `Summary.Shadows`, never an error. **Filesystem faults are `DATA_FILE`**, outside the `PULSE_TEMPLATE_*` family.

| Malformed file | Behaviour |
|---|---|
| at `pulse.New()` | **hard-fails startup**, path named — a boot break is a deploy error |
| after startup, parsed before | **serves last-good parse**; `ListTemplates` marks `Broken` + `Error` |
| after startup, never parsed | listed to be seen, not fetchable; `GetTemplate` → `_INVALID` |

Lookups rescan after 1s (unchanged size+mtime is not re-parsed). `ReloadTemplates()` forces the walk — for a deploy step that writes then renders; it returns **nil** for one broken file, only whole-walk faults return.

Facade: `ListTemplates` (non-nil `[]Summary`), `GetTemplate`, `RenderTemplate` (all five targets), `RenderTemplateRequest` (95% path), `ReloadTemplates`. `Rendered.Target` picks the populated pointer — compare it to `pulse.TemplateTarget*`, a variable's type to `pulse.TemplateVar*`, never `.String()`; echo `Rendered.JSON` — re-marshaling drops explicit zeros from `omitempty` structs.

## Errors — nine codes, by provenance

A bad **declared default** is an author error → `PULSE_TEMPLATE_INVALID` (checked semantically at declaration: enum membership, date parse, period XOR); a bad **caller value** → `_VAR_TYPE` / `_VAR_ENUM`. Same split on names: `$var`/`{{}}`/`$when` naming an **undeclared** variable is an author error caught at validation (`_INVALID`); a **declared but unresolved** reference is render-time `_UNRESOLVED`.

Also `_NOT_FOUND`, `_TARGET_UNKNOWN` (absent/unknown target, or `RenderTemplateRequest` on a non-`request` template), `_VAR_MISSING` (required, nothing resolved), `_VAR_UNKNOWN` (supplied but undeclared — never ignored), `_RENDER_INVALID` (strict decode failed — including a top-level slot this instance does not offer). Details carry `template` + `variable`; `pulse errors lookup CODE` is authoritative.

## What render does NOT do

Render **never opens a cohort**: it checks the request *shape* only; field existence, types, operator applicability and streamability stay `Predict`'s job. Strict decode is harsher than elsewhere in Pulse: a body pasted from an `internal/examples/` file with its `_meta` block attached fails `_RENDER_INVALID`.

## See

[`request-envelope`](request-envelope.md) (body slot map) · [`expression-language`](expression-language.md) (the other axis: expr-lang over row fields).
