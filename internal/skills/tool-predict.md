---
name: tool-predict
kind: tool
description: Validate a request against a cohort schema without executing.
type: reference
applies_to: predict, process, mcp
---

## When to use

CALL BEFORE STORING OR EXECUTING A HAND-AUTHORED OR GENERATED REQUEST.
Cheap: the shape errors execution would emit, plus the defaults it applies.

## Input

A bare request at the root<!-- feature: capability:process -->, as `pulse_process` takes it<!-- /feature --> (`cohort` required); or ONE alternative root alone (any key beside it is `SERVICE_VALIDATION`):

<!-- feature: capability:compose -->
- `composed` — a Compose batch: each slot over its cohort, then the batch overlays. Adds `slots[]`, one verdict per slot.
<!-- /feature -->
<!-- feature: capability:facet -->
- `facet` — a rich facet request.
<!-- /feature -->
<!-- feature: capability:process_chain -->
- `chain` — a chain: each stage over the schema the one before produces.
<!-- /feature -->

## Output

No envelope. Coded `errors` / `warnings` (`{code, message, details}`, omitted when empty) sit beside the verdict — read them for why `valid` is false; under strict mode warnings come back as errors. An alternative root answers under its own key (`valid`, the echoed `request`, its verdict). A bare request answers at the root (`valid`, `request`, `schema_info`, `suggestions`, …):

- `streamable` + `streamable_reasons` — matches the runtime (`decimal128` buffers).
- `crosstab_fusable` + `crosstab_fusion_reasons` — crosstab only: fused one-pass arm or not, and why.
- `limit_findings` — `{limit, configured, estimated, grade}` per exceeded limit: `certain` refuses `PULSE_LIMIT_EXCEEDED`, `possible` only warns.
- `defaults_applied` (`path`, `field`, `type`, `reason`; computed even under `--no-defaults`) and `aggregations` / `groups` / `filterers` with each slot's resolved `type` and `component_schema`.
<!-- feature: capability:weighting -->
- `suggested_weight` — inspect's SPSS suggestion, never applied.
<!-- /feature -->
- `post_window` — with windows: `windows_first`, the attributes run after them (by `index`).
- `advisories` — coded notes that the analysis may not fit the data; never warnings (`predict-advisories`).

## Gotchas

- Header + schema only, never records — but every formula, expression filter and `total()` argument is type-compiled (`PULSE_FORMULA_COMPILE_ERROR`, as the runtime reports it).
- `record_count` is 0 for a single-file cohort (summed only over a shard archive's headers); use `pulse_inspect`.
- `crosstab_fusable` honours `Options.DisableCrosstabFusion` (memory differs, output does not); it cannot see a grouper rejecting its params.
- An unknown key at any depth is `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path` and a "did you mean", as process refuses it.

## See

- `request-envelope` (slots, smart defaults), `tool-process` (same input shape), `streaming-and-watching`, `predict-advisories`.
