---
name: response-shaping
description: The request `return` block — choose which parts of a response come back (presets, include / exclude paths) and how many significant digits floats carry on the wire; what is projected, what stays exact, and how it composes with streaming and instance defaults.
kind: design
type: guide
applies_to: process, compose, predict, manifest
covers: [return, presets, include, exclude, precision, returned]
---

# Response shaping (`return`)

`return` trims a response to what the caller reads, saving tokens. Absent `return` (or `preset: full`) the response is byte-identical to before and hashes identically. Predict resolves and validates it before any record is read; `pulse_predict` echoes `data.return` (`{preset, include, exclude, keep, precision?, identity, digest}`) plus `sizes` — per top-level section `{section, full_bytes, shaped_bytes, basis}` (`exact` / `upper_bound` / `heuristic`; excluded = 0; omitted when the size depends on data) — and `unresolved_includes`.

```json
{"return": {"preset": "standard", "exclude": ["warnings"], "precision": 4}}
```

## Resolution

Order: **preset, then `include` adds, then `exclude` removes — exclude wins.** `include` with no preset starts from an EMPTY base (an allowlist). `exclude` / `precision` alone shape the full response. Top-level `warnings`, and every nested `warnings` whose parent is emitted, are kept unless excluded.

Presets (closed enum; the manifest `return_presets` lists each one's paths, already narrowed to what this instance offers):

- `full` — the identity.
- `standard` — data, warnings, metadata, whole crosstab / overlays, trimmed tests and regressions; no `components`.
- `minimal` — the PRIMARY RESULT only: `data`, `warnings`, crosstab matrix, matrix primaries, test statistic and p-value, regression coefficients. No `metadata`, no `components`.

## Path grammar

Paths use the Response JSON names at any depth: `.` between keys, `[*]` into every array element, a trailing `*` on a map-key segment as a prefix glob (`tests[*].details.effect_*`, `components.aggregations[*].groups`). `data[*].<column>` selects output columns; a column the request cannot produce is refused.

## Errors

- `PULSE_RETURN_INVALID` — unknown preset, precision outside 1–17, malformed path, naming `returned`, or a non-overlay path in a Compose-level block.
- `PULSE_RETURN_PATH_UNKNOWN` — a path this instance's Response does not carry (a hidden feature's path counts).
- `PULSE_RETURN_PATH_UNMATCHED` — WARNING, buffered runs only: an include through an open map matched nothing in the executed response. Never raised on streams; predict lists the includes that may go unmatched as `unresolved_includes`.

## Precision

`precision` is significant digits, wire-only: Go values stay full float64, JSON floats are written at N digits (Go `g` format, so tiny or huge values use exponent form). Untouched: integers, `decimal128` decimal strings, NaN (still `null`), and exact counts — count-aggregation columns, count crosstab cells and margins, `matrices[*].auxiliary.n`. Extension aggregators are never count-exempt.

## Absent, not null

An excluded part is ABSENT on the wire — never `null`, even a required key such as `metadata.total_rows` — and zero / nil on the Go value. A shaped response is a PROJECTION: it is not schema-valid against the full payload schema, so do not validate it against `pulse://schema` or re-decode it as a full Response. A response whose plan changes something carries `returned {preset, digest, precision?}` (`preset` is `custom` for include / exclude alone); the marker is unexcludable and its `digest` equals predict's.

## Defaults and precedence

Instance default: `Options.DefaultReturn`, else the feature profile's `return`, else `full`. A request's `return` REPLACES the default entirely (include-only over a `standard` default gives an empty base) — blocks never merge.

MCP tools default to `standard`: request `return` > host `pulse mcp --return` / `DefaultReturn` > instance default > `standard`. Send `"return": {"preset": "full"}` when you need components or everything.

`disable_components` is shorthand: alone it only skips computing components (byte-identical, no marker). Combined with any `return` layer it adds `exclude: ["components"]`. An engine that disabled components stays disabled under a request `return`: the components are not computed and the block gains `exclude: ["components"]`. Only an explicit request `disable_components: false` re-opens them.

## Excluded means not computed

An excluded `components` part (aggregations incl. `groups`, groupers, filterers, run) is never computed — the run does less work, not just the wire. An excluded `matrices` slot is never accumulated (unless `components.matrices` is kept), and an excluded `auxiliary`, `scalars` or `vectors` is never built. An excluded overlay layer, test or post-test is never computed and raises nothing, unless a multiplicity family claims it; excluded regressions are never fitted. A kept overlay that reads components keeps them computed, still pruned from the wire. Kept figures never change, and the execution path is chosen from the full request.

## Surfaces

- Process: shapes the one response.
- Compose: each slot's `return` (else the instance default) shapes `responses[i]`; `ComposedRequest.return` shapes top-level overlays only (`overlays…` paths), `responses` stays whole.
- Chain: each stage has its own `return`, applied AFTER the whole chain, so a stage excluding `data` still feeds the next stage; `final` follows the last stage.
- Streaming: rows carry only selected columns at the requested precision; `returned` rides the terminal chunk only; excluding `data` emits no rows. Details: `streaming-and-watching`.

## See

`request-envelope` (slot map) · `response-components` (what `components` holds, per-group `groups[]`) · `streaming-and-watching` · `pulse_manifest` `return_presets`.
