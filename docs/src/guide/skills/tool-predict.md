```yaml
name: tool-predict
kind: tool
description: Validate a request against a cohort schema without executing.
type: reference
applies_to: predict, process, mcp
```

## When to use

Before storing or executing a hand-authored or generated request. Cheap — reads only the cohort header + schema, never record data. Returns the same shape errors execution would emit on validation failure, plus normalization metadata.

## Input

`request` (string): JSON-encoded `types.Request`. The same request body execution takes.

## Output

`descriptor.Envelope` wrapping `PredictResult`: `Errors`, `Warnings`, `Streamable` (bool — matches runtime via `processing.CanStreamRequest`), `CrosstabFusable` + `CrosstabFusionReasons` (crosstab requests only: will the grid build on the fused one-pass, `O(cells + margins)` arm — and why not), `LimitFindings` (`limit_findings`, omitted when none: each instance resource limit a predicted figure exceeds — `{limit, configured, estimated, grade}`; `certain` = exact, refused with `PULSE_LIMIT_EXCEEDED` and `valid: false`; `possible` = an upper bound, a warning only), `DefaultsApplied` (slot-level inference summary), `Normalized` (the engine-canonical request after defaults), and `suggested_weight` — inspect's SPSS suggestion, echoed as data (never a warning or applied) while no weight resolves.

## Gotchas

- Predict is no-execute: `internal/descriptor/predict.go` MUST NOT import `internal/service/` or `internal/processing/` (enforced by `TestPredictNoExecutionImports`). Header + schema only.
- `Streamable` reflects per-operator `Streamable()` plus schema gates (decimal128 forces buffered).
- `CrosstabFusable` is absent (nil) without a crosstab; it runs the engine's own fusion rule on the defaulted request and honours `Options.DisableCrosstabFusion` (false + reason). Output is identical either way — only memory differs. Predict cannot see a grouper factory rejecting its params: it may say fusable where the runtime refuses the request anyway.
- `DefaultsApplied` is always computed — disabling defaults at request time via `--no-defaults` does NOT suppress the report.
- Unknown top-level keys are rejected with `PULSE_REQUEST_UNKNOWN_FIELD` and a "did you mean" suggestion.

## See

- [`request-envelope`](request-envelope.md) — slot map and smart-default inference rules.
- [`tool-process`](tool-process.md) — runtime sibling; identical input shape.
- [`streaming-and-watching`](streaming-and-watching.md) — streamability semantics.
