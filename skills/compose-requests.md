---
name: compose-requests
description: When and how to use ComposedRequest
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

<example name="cli-compose">
## CLI Usage

```
pulse api compose --request composed.json [--json]
```

The `--json` flag wraps the output in an envelope with timing metadata.
</example>

<reference>
## Predict Mode

Use `pulse api predict` to validate a ComposedRequest before execution. Predict mode checks field references, type compatibility, and aggregator-categorical interactions for all requests in the batch.
</reference>

<rule severity="caveat" topic="batch-size">
## Limits

There is no hard limit on the number of requests in a ComposedRequest, but each request adds processing time proportional to its filter/aggregate complexity. For very large batches, consider whether the cohort data is better served by a single request with appropriate grouping.
</rule>
