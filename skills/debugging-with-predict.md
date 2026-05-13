---
name: debugging-with-predict
description: Validate a request against a .pulse schema with pulse_predict before executing — surfaces field-name typos, type mismatches, leakage warnings, streamability gates, and applied defaults. Use when a Process call errored or before running an expensive request.
type: guide
applies_to: predict
---

# Debugging With Predict

<skill_overview>
Predict reads only the cohort header and schema; it never executes the request. Invoke this skill when validating a request shape before paying for a full `pulse_process` run.
</skill_overview>

<workflow id="A" name="iterate-on-request">
## Workflow

1. Build a `Request` JSON for the cohort, aggregations, filters, groups, attributes, etc.
2. Call `pulse_predict` with the request:

   ```json
   {"request": "{\"cohort\":{\"filename\":\"data.pulse\"}, ... }"}
   ```

3. Resolve every entry in `errors` and review `warnings`. Read `data.suggestions[]` — structured machine-actionable fixups derived from each error code's metadata.
4. Check `data.streamable` and `data.streamable_reasons` — buffering gates surface here.
5. Once clean, call `pulse_process` with the same `request` payload.

For the one-shot path, call `pulse_ask` with `{"request": ..., "predict": true, "on_invalid": "suggest"}`. The response carries `predict` (the PredictResult) plus `suggestions` ready to act on; the engine skips execution.
</workflow>

<reference>
## What predict checks

- Field names exist in the cohort schema.
- Field types are compatible with each component.
- Required params are present (e.g., `AGG_PERCENTILE` `percentile`, `GROUP_RANGE` bounds, `WIN_LAG` `order_by`).
- Numeric ops on categorical fields emit `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`.
- Description quality emits `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`.
- Window contracts (`PULSE_WINDOW_INVALID`).
- Feature ordering (`PULSE_FEAT_TARGET_LEAKAGE_RISK`).
- Streamability — every gate that forces buffering is named in `streamable_reasons`.
- Defaults — operators inferred from field types appear in `defaults_applied`.

`data.suggestions[]` is the structured fixup channel. Entries look like:

```json
{
  "path": ["Aggregations", "0", "Field"],
  "reason": "field revenu is not in the schema; closest names by edit distance",
  "current": "revenu",
  "proposed": ["revenue"],
  "confidence": 0.9
}
```

Apply suggestions in confidence order; high-confidence (0.8–0.9) entries are single-candidate swaps.
</reference>

<reference>
## Common scenarios

| Scenario | Symptom | Fix |
|---|---|---|
| Field name typo | `SERVICE_VALIDATION` error citing unknown field | Read `data.suggestions` — likely candidates ranked by edit distance. Or call `pulse_inspect` and copy the exact name. |
| Numeric agg on categorical | `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` warning | Switch to `AGG_FREQUENCY` or `AGG_COUNT`. |
| Missing aggregator param | `PROCESSING_CONFIG` error (e.g., percentile without `percentile`) | Add the required parameter to the aggregator config. |
| Wrong filterer key | `SERVICE_VALIDATION` error on filter shape | Match the filterer's expected keys (`values`, `min`/`max`, `expression`). |
| Low-quality description | `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` warning | Rewrite the field description as a concrete sentence ≥10 chars. |
| Window without OrderBy | `PULSE_WINDOW_INVALID` | Add at least one `order_by` key referencing a numeric or date field. |
| Streamable=false | `streamable_reasons` populated | Either accept buffering, or swap the gating operator (`GROUP_QUANTILE` -> `GROUP_RANGE`, `AGG_MEDIAN` -> `AGG_AVERAGE`, etc.). |
</reference>

<rule severity="caveat" topic="predict-blind-spots">
## Predict cannot detect

- Runtime numeric overflow during aggregation.
- Empty groups produced after filtering.
- Post-import dictionary growth on categorical fields.

For those, run `pulse_process` and inspect the output envelope's `warnings`/`errors`.
</rule>
