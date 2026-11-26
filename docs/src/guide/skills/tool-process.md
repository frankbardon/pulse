```yaml
name: tool-process
kind: tool
description: Execute one pre-built processing request against a cohort.
type: reference
applies_to: process, compose, predict, mcp
```

## When to use

CALL TO RUN ONE VALIDATED REQUEST AGAINST A COHORT.
The body comes from `pulse_examples_get` (or is hand-authored and run through `pulse_predict`). For multi-question batches use `pulse_compose`. For stage-chained workflows use `pulse_process_chain`.

## Input

The request's keys at top level (no wrapper, no JSON string): `cohort` (`{filename}`, or `union`), then the slots (`aggregations`, `groups`, `filterers`, `tests`, `crosstab`, `return`, …). The tool schema lists every key; [`request-envelope`](request-envelope.md) maps them.

## Output

No envelope: `data`, `metadata` (`total_rows`, `filtered_rows`, `cohort_file`, partial-cohort reasons), each slot family's results (`tests`, `regressions`, `overlays`, …), coded `warnings`, and `returned {preset, digest}` when shaped. A refusal is a tool error `{code, message, details}`. Record rows (an ungrouped windowed run) carry `decimal128` as a decimal string, `date` as `YYYY-MM-DD` and `datetime` as RFC 3339 UTC (`…Z`): never epoch numbers, no zone applied.

## Gotchas

- **The MCP default `return` preset is `standard`** (the server's `--return` or feature profile may change it): `components` and some per-slot detail are absent. Ask for them with `"return": {"preset": "full"}` or an `include` list ([`response-shaping`](response-shaping.md)).
- Slot keys differ from manifest catalog names: groupings go under `"groups"` (NOT `"groupers"`); aggregations under `"aggregations"` (NOT `"aggregators"`). An unknown key at any depth → `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path` (`sort[0].direction`) and a "did you mean" suggestion.
- Seed bodies from `pulse_examples_search` + `pulse_examples_get` or `pulse_manifest`, never external docs or source code.
- Smart defaults: a slot naming a `field` without `type` gets one from the field type (`defaults_applied` in predict says which); the library's `Options.DisableDefaults` / CLI `--no-defaults` turn them off.
- `models: [{name, path}]` loads saved model files by name for scoring references; see [`compose-fits`](compose-fits.md) (Loading).

## See

- [`request-envelope`](request-envelope.md) — slot map, smart defaults, normalization.
- [`response-shaping`](response-shaping.md) — `return` presets, `include` / `exclude`.
- [`response-components`](response-components.md) — the counts behind each figure (`components`, `full` preset).
- [`aggregation-design`](aggregation-design.md), [`grouper-design`](grouper-design.md), [`attribute-composition`](attribute-composition.md) — operator catalogs.
