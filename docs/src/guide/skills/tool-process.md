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

`request` (string): JSON-encoded `types.Request`. Top-level keys: `cohort`, `aggregations`, `groups`, `attributes`, `filterers`, `windows`, `features`, `regressions`, `crosstab`, `joins`, `overlays`, `post_tests`.

## Output

`descriptor.Envelope` wrapping `pulse.Response`. `data` carries `Data` (rows or matrix), `Metadata` (cohort filename, partial-cohort reasons), and `Components` (universal floor + per-operator state — see [`response-components`](response-components.md)). `errors` / `warnings` use coded entries.

## Gotchas

- Slot keys differ from manifest catalog names: groupings go under `"groups"` (NOT `"groupers"`); aggregations under `"aggregations"` (NOT `"aggregators"`). Unknown top-level keys → `PULSE_REQUEST_UNKNOWN_FIELD` with a "did you mean" suggestion.
- DO NOT author bodies from external docs or source code — they may be out of date. Always seed from `pulse_examples_search` + `pulse_examples_get` or `pulse_manifest`.
- Smart defaults: omitting `Type` on a slot that names a field auto-infers from schema type. Disable via `Options.DisableDefaults` or CLI `--no-defaults`.

## See

- [`request-envelope`](request-envelope.md) — slot map, smart defaults, normalization.
- [`response-components`](response-components.md) — universal floor + per-operator components emitted in `Response.Components`.
- [`aggregation-design`](aggregation-design.md), [`grouper-design`](grouper-design.md), [`attribute-composition`](attribute-composition.md) — operator catalogs.
