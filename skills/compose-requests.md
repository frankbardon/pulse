---
name: compose-requests
description: Run several Process requests against one cohort in a single pulse_compose call — order-preserving slot-by-index, optional parallel execution. Use when comparing operator outputs, fan-out reporting, or batching multiple analyses for one MCP round-trip.
type: guide
applies_to: compose
---

# Compose Requests

<skill_overview>
A ComposedRequest bundles multiple Request objects into a single operation. Invoke this skill when running several analyses against the same cohort without reloading data.
</skill_overview>

<reference>
## When to Use ComposedRequest

Use ComposedRequest when:

1. **Multiple aggregations on the same cohort**: You need COUNT, AVERAGE, and FREQUENCY results but with different grouping or filtering.
2. **Comparison queries**: You want the same aggregation with different filters to compare subgroups.
3. **Dashboard population**: You need to fill multiple dashboard widgets from a single cohort in one call.

Do NOT use ComposedRequest when:

- You only have one query. Use a regular Request instead.
- Each query targets a different cohort. ComposedRequest is optimized for shared-cohort scenarios.
</reference>

<example name="composed-request">
## Structure

```json
{
  "requests": [
    {
      "cohort": {"filename": "students.pulse"},
      "aggregations": [{"type": "AGG_COUNT", "field": "score"}],
      "filterers": [{"type": "FILTER_INCLUDE", "field": "grade", "values": ["A"]}]
    },
    {
      "cohort": {"filename": "students.pulse"},
      "aggregations": [{"type": "AGG_AVERAGE", "field": "score"}],
      "groups": [{"type": "GROUP_CATEGORY", "field": "department"}]
    }
  ]
}
```
</example>

<rule severity="should" topic="shared-cohort">
## Shared Cohorts

When all requests reference the same cohort file, Pulse loads and decodes the file once and shares the record set across all requests. This provides significant performance benefit for large cohorts.

If requests reference different cohort files, each file is loaded independently.
</rule>

<rule severity="must" topic="result-ordering">
## Result Merging

Results are returned as an array in the same order as the input requests. Each result has its own metadata (total_rows, filtered_rows) reflecting that request's filter state.

```json
[
  {"data": [...], "metadata": {"total_rows": 1000, "filtered_rows": 50}},
  {"data": [...], "metadata": {"total_rows": 1000, "filtered_rows": 1000}}
]
```
</rule>

<example name="invoke-compose">
## Calling pulse_compose

```json
{
  "request": "{\"requests\":[{\"cohort\":{\"filename\":\"students.pulse\"},\"aggregations\":[{\"type\":\"AGG_COUNT\",\"field\":\"score\"}]}]}"
}
```

The response is the standard envelope. Each request's result is one entry in `data` in the same order as the input. Per-request `metadata` reflects that request's filter state.
</example>

<reference>
## Validate before executing

Call `pulse_predict` on each `Request` inside the batch (or run them as a sequence through `pulse_predict` calls) to check field references, type compatibility, and aggregator-categorical interactions before paying for `pulse_compose`. Predict has no batch mode in v1; loop per element.

For parallel execution and fail-fast control (CLI-side flags), see https://frankbardon.github.io/pulse/cli/api-compose.html.
</reference>

<rule severity="caveat" topic="batch-size">
## Limits

There is no hard limit on the number of requests in a ComposedRequest, but each request adds processing time proportional to its filter/aggregate complexity. For very large batches, consider whether the cohort data is better served by a single request with appropriate grouping.
</rule>
