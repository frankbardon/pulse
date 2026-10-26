---
name: tool-predict
kind: tool
description: Validate a request against a cohort schema without executing.
type: reference
applies_to: predict, process, mcp
---

## When to use

CALL BEFORE STORING OR EXECUTING A HAND-AUTHORED OR GENERATED REQUEST.
Cheap. Returns the shape errors execution would emit, plus normalization metadata.

## Input

A bare `types.Request` at the root, as execution takes it; or ONE alternative root alone (any key beside it is `SERVICE_VALIDATION`):

<!-- feature: capability:compose -->
- `composed` — a Compose batch: each slot over its cohort, then the batch overlays. The verdict adds `slots[]` (each slot's `streamable`, `p_values`, correction `families`).
<!-- /feature -->
<!-- feature: capability:facet -->
- `facet` — a rich facet request.
<!-- /feature -->
<!-- feature: capability:process_chain -->
- `chain` — a chain: each stage over the schema the one before produces.
<!-- /feature -->

## Output

Every answer carries coded `errors` / `warnings` (`{code, message, details}`, omitted when empty) beside the verdict — read them for why `valid` is false. An alternative root answers under its own key (`valid`, the echoed `request`, its verdict); under strict mode warnings come back as errors. A bare request answers with `PredictResult` at the root:

- `Streamable` — matches the runtime (`decimal128` buffers).
- `CrosstabFusable` + `CrosstabFusionReasons` — crosstab only: will the grid build on the fused one-pass arm, and why not.
- `limit_findings` — each resource limit a predicted figure exceeds, `{limit, configured, estimated, grade}`: `certain` refuses `PULSE_LIMIT_EXCEEDED` (`valid: false`), `possible` only warns.
- `DefaultsApplied` (always computed, even under `--no-defaults`) and `Normalized` (the canonical request after defaults).
<!-- feature: capability:weighting -->
- `suggested_weight` — inspect's SPSS suggestion, echoed as data while no weight resolves; never applied.
<!-- /feature -->
- `post_window` — with windows: `windows_first`, the attributes run after them (by `index`); set on refusals.
- `advisories` — coded notes that the analysis may not fit the data; never warnings, never escalated (`predict-advisories`).

## Gotchas

- Header + schema only, never records — but every formula, expression filter and `total()` argument is type-compiled: `PULSE_FORMULA_COMPILE_ERROR`, byte-identical to the runtime's.
- `CrosstabFusable` honours `Options.DisableCrosstabFusion`; output is identical, only memory differs. It cannot see a grouper factory rejecting its params.
- An unknown key at any depth (an undeclared `params` key included) is `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path` and a "did you mean"; process refuses the same body alike.

## See

- `request-envelope` (slots, smart defaults), `tool-process` (same input shape), `streaming-and-watching`, `predict-advisories`.
