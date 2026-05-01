---
name: getting-started
description: Pulse vocabulary, file format, mental model
type: guide
applies_to: process, compose, sample, facet, inspect, predict, manifest
---

# Getting Started

<skill_overview>
Pulse is a self-describing tabular processing engine over `.pulse` cohort files; this skill teaches the vocabulary, request shape, CLI surface, and pipeline order. Invoke it first when authoring requests or onboarding to any other Pulse skill.
</skill_overview>

<reference>
## Vocabulary

| Term | Meaning |
|---|---|
| Cohort | A `.pulse` binary file: schema header + fixed-width records. |
| Schema | Field list (name, type, description) embedded in the cohort header. |
| Field | One column. Typed with one of 15 field types (u8..categorical_u32). |
| Record | One row. Fixed-width binary block. |
| Aggregation | One of 16 `AGG_*` ops (COUNT, SUM, AVERAGE, ...) producing a per-group scalar. |
| Attribute | One of 7 `ATTR_*` ops producing a per-record derived value. |
| Filterer | One of 4 `FILTER_*` predicates run before grouping. |
| Grouper | One of 5 `GROUP_*` partition strategies run before aggregation. |
| Request | JSON: `{cohort, filterers, groups, aggregations, attributes, outputs}`. |
| ComposedRequest | `{requests: [Request, ...]}` — batch over a shared cohort. |
| Manifest | Self-description envelope: commands, components, cohort_types, skills. |
</reference>

## Workflows

<workflow id="A" name="analyze-non-pulse-source">
### Analyze non-`.pulse` source data

1. `pulse import schema-template data.csv > schema.json` — emit an editable field list with empty descriptions.
2. Edit `schema.json`: fill in `description` for each field; adjust `type` if inference was wrong.
3. `pulse import csv --input data.csv --output data.pulse --schema schema.json` — write the cohort.
4. `pulse cohort inspect data.pulse --json` — confirm fields, types, and dictionaries.
5. `pulse api process --request request.json --json` — run the analysis (see canonical request below).
</workflow>

<workflow id="B" name="convert-between-formats">
### Convert between supported formats

`pulse convert INPUT OUTPUT` auto-detects formats from extensions. Use `--from` / `--to` to override, `--schema` to pin the schema, and `--keep-pulse PATH` to retain the intermediate cohort:

```
pulse convert data.csv data.parquet --keep-pulse cache.pulse
```

Use `pulse convert predict INPUT OUTPUT --json` to validate without writing.
</workflow>

<workflow id="C" name="process-existing-pulse">
### Process an existing `.pulse` file

1. `pulse cohort inspect data.pulse --json` — confirm the schema you are coding against.
2. Author `request.json` matching the schema's field names and types.
3. `pulse api predict --request request.json --json [--strict]` — validate, no execute.
4. `pulse api process --request request.json --json` — execute.
</workflow>

<example name="canonical-process-request">
## Canonical process request

```json
{
  "cohort": {"filename": "data.pulse"},
  "filterers": [
    {"type": "FILTER_INCLUDE", "field": "status", "values": ["active"]}
  ],
  "groups": [
    {"type": "GROUP_CATEGORY", "field": "region"}
  ],
  "aggregations": [
    {"type": "AGG_COUNT", "field": "id", "label": "n"},
    {"type": "AGG_AVERAGE", "field": "score", "label": "mean_score"}
  ],
  "attributes": [],
  "outputs": [{"format": "json"}]
}
```

JSON tags are verified against `types.Request`: `cohort`, `filterers`, `groups`, `aggregations`, `attributes`, `outputs`.
</example>

<reference>
## CLI command tree

```
pulse [--json]                                               # manifest at root
pulse api process    --request FILE [--json]
pulse api compose    --request FILE [--json]
pulse api sample     --input FILE [--count N] [--json]
pulse api facet      --input FILE --field NAME [--json]
pulse api predict    --request FILE [--json] [--strict]
pulse cohort inspect PATH [--json] [--full-dict]
pulse cohort filter  --input FILE --output FILE --filter EXPR [--json]
pulse import csv|tsv|ndjson|jsonarray|parquet|arrow --input F --output F [--schema F] [--sample-rows N] [--json]
pulse import excel   --input F --output F [--schema F] [--sample-rows N] [--sheet S] [--json]
pulse import predict --input F [--format F] [--schema F] [--sample-rows N] [--json]
pulse import schema-template INPUT [--format F] [--sample-rows N]
pulse export csv|tsv|ndjson|jsonarray|parquet|arrow|excel --input F --output F [--json]
pulse export predict --input F [--format F] [--json]
pulse convert INPUT OUTPUT [--from F] [--to F] [--schema F] [--keep-pulse PATH] [--sample-rows N] [--json]
pulse convert predict INPUT OUTPUT [--from F] [--to F] [--sample-rows N] [--json]
pulse skills list  [--json]
pulse skills show  NAME
```
</reference>

<reference>
## Pipeline order

Load -> Filter -> Group -> Aggregate -> Attributes -> Output.
</reference>

<reference>
## Envelope

Every `--json` response: `{"format_version":"1.0","data":{...},"errors":[],"warnings":[]}`.
</reference>

<see_also>
- cohort-schema-design — field types and schema authoring
- aggregation-guide — `AGG_*` operations and filtering
- attribute-composition — `ATTR_*` per-record derivations
- grouper-design — `GROUP_*` partition strategies
- compose-requests — batching with `ComposedRequest`
- debugging-with-predict — iterating on a request before processing
- error-code-reference — every error code by domain
- import-best-practices — schema templates and import tuning
- export-format-selection — picking an output format
</see_also>
