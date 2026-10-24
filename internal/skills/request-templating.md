---
name: request-templating
description: Stored parameterised JSON requests — the {target, variables, body} wrapper, $var / {{}} / $when substitution, the nine variable types, directory discovery, hot reload, and the PULSE_TEMPLATE_* family. Not expr-lang.
kind: design
type: guide
applies_to: process, compose, sample, facet, predict
covers: [templates, variables, substitution, PULSE_TEMPLATES_DIR]
requires: [capability:templates]
---

# Request templating

Stored JSON that renders into a **validated typed request** — shape fixed once, caller supplies only variables. Execution-free.

**Not expr-lang.** `$var` / `{{}}` / `$when` substitute before decode; expressions run on *row fields* at execution (`expression-language`). `{{}}` in an expression string (`expression`, `holdout`, overlay `params.formula`, feature `params.fit_on`) is `_EXPR_SPLICE` at load, axis tokens too; whole `$var`, `{{{{` are legal.
<!-- feature: capability:params -->
Use a param: `"params":{"x":{"$var":"x"}}` + `params.x` (`request-params`).
<!-- /feature -->

## File shape

`{"name"?, "description"?, "target", "variables"[], "body"}`; unknown top-level keys rejected. `target` ∈ `request | composed | chain | facet | sample` (lowercase) picks the strict-decode root — required, never inferred. `name` derives from the file path; a disagreeing `name` key is rejected. `body` is a non-empty object, deliberately **not** runnable as-is.

## Substitution — three forms

| Form | Rule |
|---|---|
| `{"$var":"bucket"}` | Marker **iff** `$var` is the only key and its value a non-empty string; replaced whole, **type-preserving** — `{"interval":{"$var":"bucket"}}` → `{"interval":10}`, never `"10"`. `{"$var":"x","other":1}` is literal data. |
| `"{{name}}"` | Natural text, inside a **string value** only; `{{ name }}` works, `{{{{` escapes a literal `{{`. `list`/`period` have no text form → `_VAR_TYPE`. `{{` in an object **key** is rejected. |
| `{"$when":"segs",…}` | Survives iff the variable resolved — **presence, not truthiness**: drops only when unsupplied AND undefaulted; supplied `""`/`[]`/`0`/`false` KEEPS it. Key stripped either way. Guards run **before** descent, so markers in a dropped block never raise. Array drops compact the slice; root-level `$when` errors. |

<!-- feature: capability:for_each -->
A `for_each` item's axis placeholders pass through to its expansion (`item-fan-out`).
<!-- /feature -->
<!-- feature: capability:compose_sweep -->
A `sweep`'s axis placeholders pass through in its `request`, `overlays`, `label` only (`compose-sweeps`).
<!-- /feature -->

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

`<…>` = any operator the manifest lists; render never inspects `type`.

`RenderTemplateRequest("finance/revenue", map[string]any{"metric":"revenue"})` →

```json
{"label":"total revenue","cohort":{"filename":"sales.pulse"},
"aggregations":[{"type":"<aggregator>","field":"revenue"}],
"groups":[{"type":"<binning grouper>","field":"revenue","interval":10}]}
```

`bucket` defaulted to the **number** `10`; the `segs`-guarded grouper dropped, the array compacted.

## Discovery + lifecycle

`Options.TemplateDirs []string`, else `PULSE_TEMPLATES_DIR` split on `os.PathListSeparator`. Name = root-relative path minus `.json`, `/`-separated. Roots are ordered precedence — **first root wins**; losers land on the winner's `Summary.Shadows`, never an error. **Filesystem faults are `DATA_FILE`**, outside the `PULSE_TEMPLATE_*` family.

| Malformed file | Behaviour |
|---|---|
| at `pulse.New()` | **hard-fails startup**, path named |
| after startup, parsed before | **serves last-good parse**; `ListTemplates` marks `Broken` + `Error` |
| after startup, never parsed | listed to be seen, not fetchable; `GetTemplate` → `_INVALID` |

Lookups rescan after 1s (unchanged size+mtime: no re-parse). `ReloadTemplates()` forces it; **nil** for one broken file, only whole-walk faults return.

Facade: `ListTemplates` (non-nil `[]Summary`), `GetTemplate`, `RenderTemplate` (all five targets), `RenderTemplateRequest` (95% path), `ReloadTemplates`. `Rendered.Target` picks the set pointer; compare it to `pulse.TemplateTarget*`, a variable's type to `pulse.TemplateVar*`, never `.String()`; echo `Rendered.JSON` — re-marshaling drops explicit zeros from `omitempty` structs.

## Errors — eleven codes, by provenance

A bad **declared default** is an author error → `PULSE_TEMPLATE_INVALID` (checked semantically at declaration: enum membership, date parse, period XOR); a bad **caller value** → `_VAR_TYPE` / `_VAR_ENUM`. Same split on names: `$var`/`{{}}`/`$when` naming an **undeclared** variable is an author error caught at validation (`_INVALID`); a **declared but unresolved** reference is render-time `_UNRESOLVED`.

Also `_NOT_FOUND`, `_TARGET_UNKNOWN` (absent/unknown target, or `RenderTemplateRequest` on a non-`request` template), `_VAR_MISSING` (required, nothing resolved), `_VAR_UNKNOWN` (supplied, undeclared — never ignored), `_RENDER_INVALID` (strict decode failed, incl. a top-level slot this instance does not offer), `_AXIS_COLLISION` (a variable shares an item or sweep axis's name), `_EXPR_SPLICE`. Details carry `template` + `variable`; `pulse errors lookup CODE` is authoritative.

## What render does NOT do

Render **never opens a cohort**: shape only; fields, types, operators and streamability are `Predict`'s job. Strict decode: an unknown key (examples `_meta` too) fails `_RENDER_INVALID` (`details.path`, `suggestion`).

## See

`request-envelope` (body slot map) · `expression-language` (expr-lang over row fields).
