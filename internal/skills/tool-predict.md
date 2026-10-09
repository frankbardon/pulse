---
name: tool-predict
kind: tool
description: Validate a request against a cohort schema without executing.
type: reference
applies_to: predict, process, mcp
---

## When to use

CALL BEFORE STORING OR EXECUTING A HAND-AUTHORED OR GENERATED REQUEST.
Cheap — reads only the cohort header + schema, never records. Returns the shape errors execution would emit, plus normalization metadata.

## Input

A bare `types.Request` at the root, as execution takes it; or ONE alternative root alone (any key beside it is `SERVICE_VALIDATION`):

<!-- feature: capability:compose -->
- `composed` — a Compose batch: each slot over its cohort, then the batch overlays.
<!-- /feature -->
<!-- feature: capability:facet -->
- `facet` — a rich facet request.
<!-- /feature -->
<!-- feature: capability:process_chain -->
- `chain` — a chain: each stage over the schema the one before produces.
<!-- /feature -->

## Output

An alternative root answers under its own key (`valid`, the echoed `request`, its per-shape verdict) beside `errors` / `warnings`. A bare request answers with `PredictResult` at the root: `Streamable` (bool — matches runtime via `processing.CanStreamRequest`), `CrosstabFusable` + `CrosstabFusionReasons` (crosstab requests only: will the grid build on the fused one-pass, `O(cells + margins)` arm — and why not), `LimitFindings` (`limit_findings`, omitted when none: each instance resource limit a predicted figure exceeds — `{limit, configured, estimated, grade}`; `certain` = exact, refused with `PULSE_LIMIT_EXCEEDED` and `valid: false`; `possible` = an upper bound, a warning only), `DefaultsApplied` (slot-level inference summary), `Normalized` (the engine-canonical request after defaults)<!-- feature: capability:weighting -->, and `suggested_weight` — inspect's SPSS suggestion, echoed as data (never a warning or applied) while no weight resolves<!-- /feature -->, and `advisories` (omitted when none) — coded `{code, message, details}` notes that the analysis may not fit the data; never warnings, never escalated by `--strict` (`predict-advisories`).

## Gotchas

- Predict is no-execute: `internal/descriptor/predict.go` MUST NOT import `internal/service/` or `internal/processing/` (enforced by `TestPredictNoExecutionImports`). Header + schema only.
- `Streamable` reflects per-operator `Streamable()` plus schema gates (decimal128 forces buffered).
- `CrosstabFusable` is absent (nil) without a crosstab; it runs the engine's own fusion rule on the defaulted request and honours `Options.DisableCrosstabFusion` (false + reason). Output is identical either way — only memory differs. Predict cannot see a grouper factory rejecting its params: it may say fusable where the runtime refuses the request anyway.
- `DefaultsApplied` is always computed — disabling defaults at request time via `--no-defaults` does NOT suppress the report.
- Unknown top-level keys are rejected with `PULSE_REQUEST_UNKNOWN_FIELD` and a "did you mean" suggestion.

## See

- `request-envelope` — slot map and smart-default inference rules.
- `tool-process` — runtime sibling; identical input shape.
- `streaming-and-watching` — streamability semantics.
- `predict-advisories` — the advisory codes and suppression.
