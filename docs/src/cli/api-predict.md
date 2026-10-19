# pulse api predict

**Audience:** CLI users validating a request before running it.

`pulse api predict` validates a `types.Request` against a `.pulse`
file's schema **without executing it**. It reads only the header and
schema — never record data — so it's a cheap, safe iteration loop
against arbitrarily large cohorts.

> **LLM agents using MCP:** see the `pulse_predict` MCP tool and the
> `tool-predict` skill. Predict is the LLM's primary
> "would this work?" probe.

## Synopsis

```
pulse api predict --request FILE [--json] [--strict] [--echo-request]
```

## Flags

| Flag | Alias | Type | Default | Purpose |
|---|---|---|---|---|
| `--request`      | `-r` | string | (required) | Request JSON path |
| `--json`         |      | bool   | false      | Emit the standard envelope |
| `--strict`       |      | bool   | false      | Treat warnings as errors |
| `--echo-request` |      | bool   | false      | Include the normalized request on `envelope.request` (distinct from `PredictResult.Request`, which echoes the raw input) |

## Structural ban

`internal/descriptor/predict.go` cannot import `internal/service/` or `internal/processing/`.
This is enforced by `TestPredictNoExecutionImports`. Predict is
guaranteed to never touch the executor.

## Output (text mode)

```
Valid: true
Schema: 7 fields
Warning [PULSE_FIELD_DESCRIPTION_LOW_QUALITY]: field region has an empty description
```

Without `--strict`, that warning would still let the command exit 0.
With `--strict`, the warning becomes an error and the command exits
non-zero.

## Output (`--json`)

```json
{
  "format_version": "1.1",
  "data": {
    "valid": true,
    "schema_info": {"field_count": 7},
    "streamable": false,
    "streamable_reasons": [
      "AGG_MEDIAN on field price"
    ],
    "request": { /* the request as predict resolved it, with defaults applied */ }
  },
  "errors":  [],
  "warnings": [
    {"code": "PULSE_FIELD_DESCRIPTION_LOW_QUALITY", "message": "..."}
  ]
}
```

`streamable` reports whether the request will execute on the
streaming Process path; `streamable_reasons` lists every gate that
forced the buffered path. See [Performance
Notes](../ops/performance.md) for the full streaming/buffered table.

`computed_before_filters` lists the output label of every attribute
the engine computes over the whole unfiltered cohort before the filters
run (today only `ATTR_PREVIOUS_STATE`, so a date window keeps each
subject's first in-window transition). Absent when there is none.

For a crosstab request, `crosstab_fusable` reports whether the grid
will be built on the fused one-pass arm (memory proportional to cells +
margins) instead of buffering every filter-passing record, and
`crosstab_fusion_reasons` lists every reason it will not. Both are
absent for a request without a crosstab. Output is identical on either
arm. An engine built with `Options.DisableCrosstabFusion` answers
`false` with that reason.

`limit_findings` lists every instance resource limit
(`Options.Limits`) a predicted figure exceeds, as
`{limit, configured, estimated, grade, details}`; it is absent when nothing
exceeds a limit. A `certain` finding is an exact figure — the request
is refused with `PULSE_LIMIT_EXCEEDED` (`valid: false`), exactly as
`process` would refuse it before reading a record. A `possible` finding
is an upper bound and only a warning. Today predict reports
`max_groups` (`possible`: the group buckets the first grouper can
create — a categorical's dictionary size, a boolean's two values, an
`include` list, a quantile's bins, or a date-ranges grouper's ranges
plus its unmatched bucket — capped by the record count; groupers whose
keys depend on the data report nothing), `max_crosstab_cells`
(`possible`: the crosstab grid, rows × columns, from the same per-grouper
key counts; nothing when an axis grouper's keys depend on the data),
`max_matrix_dim` (a matrix's
member count) and `max_join_build_rows` (a join's right-side record
count, read from the right cohort's header without reading a record).
A run that does create more groups than `max_groups`, or a larger
crosstab grid than `max_crosstab_cells`, is refused
`PULSE_LIMIT_EXCEEDED` mid-scan, with no partial result.

`predict-compose`, `predict-chain` and `predict-facet` carry the same
`limit_findings`. Compose reports `max_compose_slots`, then every slot's
findings with `details.request` naming the slot; a chain reports
`max_chain_stages`, then every stage's findings with `details.stage`
(stage 0 on the cohort's record count; a later stage's rows exist only
at run time, so it never carries a `certain` memory finding); a facet
reports a `possible` `max_groups` (the largest faceted field's
dictionary size) and its `certain` memory estimate.

When the request emits inferential p-values, `p_values` reports
`{total, uncorrected, basis, threshold}`: how many it emits and how many
no `multiplicity` block corrects. An `uncorrected` count at or above
`threshold` (10) is the cue to add one. `basis` is `exact`, `dictionary`
(assumes every dictionary entry of a category axis becomes a bucket, so
it can over- or under-count) or `lower_bound`. See
[Multiple-Comparison Correction](../library/multiplicity.md).

`request` echoes the request **after defaults have been applied** so
you can see what would actually run. To suppress defaults, run with
`--no-defaults` on the executing leaf (`api process`,
`api compose`); predict reports `defaults_applied` regardless.

## Smart defaults

When an `aggregations[]` or `groups[]` slot names a `field` but omits
`type`, Pulse infers the operator from the named field's schema type
before running the request. Predict reports the inferred slot under
`data.defaults_applied` so you can echo back what was filled in:

| Field type | Default aggregation | Default grouper |
|---|---|---|
| `u4`, `u8`..`u64`, `f32`, `f64`, `decimal128` | `AGG_SUM` | `GROUP_RANGE` (interval 10) |
| `categorical_u8`/`u16`/`u32` | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |
| `date` | (none — must be explicit) | `GROUP_DATE` (component `"day"`) |
| `packed_bool` | `AGG_MODE_COUNT` | `GROUP_CATEGORY` |

The `Nullable` flag on a field never changes its default operator — it
only controls per-record null-bitmap participation.

Rules: defaults never override an explicit `type`; they never cross
categories (a missing aggregator does not insert a grouper); statistical
tests (`tests[]`, `post_tests[]`) are not defaulted; filter expressions,
features, attributes, and windows are out of scope.

`--no-defaults` on `pulse api process` /
[`pulse api compose`](api-compose.md) disables the inference pass
entirely and forces every slot to be source-of-truth. Predict still
reports `defaults_applied` so the caller can see what would have been
filled in.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Valid (or valid with warnings, in non-strict mode) |
| 1 | Invalid, or `--strict` with at least one warning — the `errors` of the envelope under `--json`, text mode alike |
| 2 | Usage error (unknown flag, malformed value, missing required flag); no envelope |

## Examples

### Quick validity check

```bash
pulse api predict --request req.json
```

### Programmatic check with envelope

```bash
pulse api predict --request req.json --json | \
    jq -e '.data.valid == true' >/dev/null && echo "OK"
```

### Strict mode for CI

```bash
pulse api predict --request req.json --strict --json
```

### Detect that a request will buffer

```bash
pulse api predict --request req.json --json | \
    jq '.data | {streamable, streamable_reasons}'
```

## Compose, facet and chain requests

Three sibling leaves predict the request shapes `api predict` does not
take. Each reads `--request FILE` (required) and takes `--json`,
`--strict` (every warning the leaf reports becomes an error, `valid:
false`) and `--echo-request` (the envelope's `request` carries the
normalized request — what the matching run leaf echoes: for
`predict-compose` the effective batch, a `sweep` expanded into its slots
and every slot defaults-resolved; for `predict-chain` every stage
defaults-resolved; for `predict-facet` the request as passed).

| Leaf | Request file | `data` on `--json` |
|---|---|---|
| `pulse api predict-compose` | a `ComposedRequest` | the batch verdict: `valid`, the echoed `request`, rejected overlay pairs, overlay cost, the p-value count and each slot's advisories |
| `pulse api predict-facet` | a `FacetRequest` | `valid`, the echoed `request`, `schema_info` and the accepted overlays |
| `pulse api predict-chain` | a `ChainRequest` | `valid`, the echoed `request`, the source schema and each stage's inferred output columns |

`predict-compose` judges every slot with the rules `api predict`
applies to a request (operator names and params, field types, windows,
tests, crosstab, overlays, labels), each slot fault located by
`details.request`; an empty batch (no `requests`, no `sweep`) is
`SERVICE_VALIDATION`, as at run time.

`predict-chain` judges every stage the same way over the stage's
input schema (a later stage's: the columns the stage before it
writes), each stage fault located by `details.stage`. A later stage's
`labels` are applied at run time, so a binding naming an unknown table
or field, or an aggregation output (not categorical), is refused on
both sides.

A refused request comes back as coded `errors` beside `data.valid:
false` (exit 1, under `--json` and in text mode alike). A request with no cohort is
`SERVICE_VALIDATION`; an unreadable cohort is `DATA_FILE`
(`ENCODING_INVALID` for a malformed header). Each fault keeps its own code
in `errors[0]`. Over MCP the same three shapes are `pulse_predict`'s
alternative roots: `composed`, `facet` or `chain`, alone at the root.

```bash
pulse api predict-compose --request batch.json --json | jq '.data.valid'
```

## Common warning codes

| Code | What to do |
|---|---|
| `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL` | A facet's `numeric_percentiles` / `include_histogram` names a non-numeric field, which gets no numeric statistic; facet a numeric field (a numeric aggregation over a categorical is the error `PULSE_FIELD_TYPE_UNSUPPORTED`: use `AGG_COUNT` / `AGG_MODE_COUNT`) |
| `PULSE_ATTR_CODE_NOT_IN_DICTIONARY`        | An `ATTR_CODE_IN` code is not a label of the categorical field's dictionary (it matches no row); fix or drop each entry in `details.missing_codes` |
| `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`      | Edit the schema description; re-import |
| `PULSE_FEAT_TARGET_LEAKAGE_RISK`           | A `FEAT_TARGET_ENCODE` without `params.fit_on` averages all rows' targets (a prior split does not help); set `fit_on` to the training rows (e.g. `"split == 0"`), optionally with `folds`. `folds` alone keeps a reworded warning. `process` repeats it on the response's `warnings` and, under `--strict`, refuses the run |

A field the operator cannot read is an ERROR, not a warning, and it is
the code `Process` raises: `PULSE_FIELD_TYPE_UNSUPPORTED` (pick a field of
`details.accepts_types`), `PULSE_TEST_FIELD_NOT_NUMERIC` for a numeric
test's field, and `PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL` for a built-in
aggregation with no decimal128 implementation over a decimal field
(switch to a decimal-aware aggregator).

The full code-by-code recovery playbook is reachable per-code via
the MCP `pulse_errors_lookup` tool or the `pulse errors lookup CODE`
CLI; see also [Troubleshooting](../ops/troubleshooting.md).

## Related

- [`pulse api process`](api-process.md) — executes a validated request
- [Library: pulse.Predict](../library/overview.md) — Go counterpart
- [Debugging Predict](../internals/debugging-predict.md) — LLM-side iteration recipe
