---
name: tool-predict
kind: tool
description: Validate a request against a cohort schema without executing.
type: reference
applies_to: predict, process, mcp
---

## When to use

Before storing or executing a hand-authored / programmatically generated request. Cheap — reads only the cohort header + schema, never record data. Returns the same shape errors `pulse_process` would emit on validation failure, plus normalization metadata.

## Input

`request` (string): JSON-encoded `types.Request`. Same shape as `pulse_process` input.

## Output

`descriptor.Envelope` wrapping `PredictResult`: `Errors`, `Warnings`, `Streamable` (bool — matches runtime via `processing.CanStreamRequest`), `DefaultsApplied` (slot-level inference summary), and `Normalized` (the engine-canonical request after defaults).

## Gotchas

- Predict is no-execute: `descriptor/predict.go` MUST NOT import `service/` or `processing/` (enforced by `TestPredictNoExecutionImports`). Header + schema only.
- `Streamable` reflects per-operator `Streamable()` plus schema gates (decimal128 forces buffered).
- `DefaultsApplied` is always computed — disabling defaults at request time via `--no-defaults` does NOT suppress the report.
- Unknown top-level keys are rejected with `PULSE_REQUEST_UNKNOWN_FIELD` and a "did you mean" suggestion.

## See

- `request-envelope` — slot map and smart-default inference rules.
- `tool-process` — runtime sibling; identical input shape.
- `streaming-and-watching` — streamability semantics.
