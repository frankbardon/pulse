---
name: debugging-with-predict
description: Iterating on a request using api predict
type: guide
applies_to: predict
---

# Debugging With Predict

Predict reads only the cohort header and schema; it never executes the request. Use it to validate cheaply before paying for a full `process` run.

## Workflow

1. Build `req.json` with cohort, aggregations, filters, groups, and attributes.
2. Run `pulse api predict --request req.json --json`.
3. Resolve every entry in `errors` and review `warnings`.
4. Optionally re-run with `--strict` to promote warnings to errors (CI gate).
5. Once clean, run `pulse api process --request req.json --json`.

## What predict checks

- Field names exist in the cohort schema.
- Field types are compatible with each component.
- Required params are present (e.g., `AGG_PERCENTILE` p, `GROUP_RANGE` bounds).
- Numeric ops on categorical fields emit `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`.
- Description quality emits `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`.

## Common scenarios

| Scenario | Symptom | Fix |
|---|---|---|
| Field name typo | `SERVICE_VALIDATION` error citing unknown field | Run `pulse api inspect` and copy the exact name. |
| Numeric agg on categorical | `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning | Switch to `AGG_FREQUENCY` or `AGG_COUNT`. |
| Missing aggregator param | `PROCESSING_CONFIG` error (e.g., percentile without `p`) | Add the required parameter to the aggregator config. |
| Wrong filterer key | `SERVICE_VALIDATION` error on filter shape | Match the filterer's expected keys (`values`, `min`/`max`, `expression`). |
| Low-quality description | `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warning | Rewrite the field description as a concrete sentence ≥10 chars. |

## Predict cannot detect

- Runtime numeric overflow during aggregation.
- Empty groups produced after filtering.
- Post-import dictionary growth on categorical fields.

For those, run `process` and inspect output.
