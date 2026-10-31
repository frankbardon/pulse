```yaml
name: synth-disclosure-report
description: The fidelity report's `disclosure` section — how close a `synth from-profile` twin sits to real records (exact-match rate over every output column, Gower distance to closest record on a seeded sample), how it is switched and sized (`--disclosure-sample`, `--no-disclosure`), and its charge against the instance memory limit.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, fidelity report, disclosure, exact match, distance to closest record, DCR, Gower, --disclosure-sample, --no-disclosure]
requires: [capability:synth]
```

# Synth disclosure report

A twin carries NO formal privacy guarantee: no noise is added, so a generated row can repeat a real one. The `disclosure` section measures how close the twin sits to the source, so a data owner reads a number before sharing it. It is evidence, not a guarantee.

## When it is written

On every `--fidelity-report` (`SynthOptions.FidelityReportPath` with `SourceCohort`), tagged or `--synthetic-only` alike: both read the same tagged cohort, so the section is byte-identical between the two layouts. `--no-disclosure` (`SynthOptions.DisableDisclosure`) leaves it out. It never moves the generated rows: the output is byte-identical with the section on or off.

## Fields

| Key | Meaning |
|---|---|
| `distance` | always `gower` |
| `columns` | output columns compared (`_synthetic` excluded) |
| `source_rows`, `synthetic_rows` | partition sizes |
| `exact_matches`, `exact_match_rate` | generated rows equal to SOME source row on EVERY column (null equals null), over ALL generated rows |
| `sample_rows` | generated rows the distance was measured over |
| `dcr_median`, `dcr_p5` | median and 5th percentile (type-7) of each sampled row's distance to its closest source row, in [0, 1] |
| `error` | set instead of the distance figures when the source has no rows |

**Read `dcr_p5` first.** An exact copy is distance 0, so a `dcr_p5` of 0 means at least 5% of the sampled rows copy a source row on every column. A twin built only from low-cardinality columns (a few categoricals and flags) copies rows by construction: the measure is honest about it, not wrong.

## The Gower distance

The mean, over every output column, of a per-column term in [0, 1]:

- numbers, `decimal128`, `u4`, `date`, `datetime`: `|a - b| / range`, the range taken over the SOURCE rows, clamped at 1. A zero range gives 0 for equal values and 1 otherwise. NaN equals only NaN.
- `categorical_*`, `packed_bool`: 0 when equal, else 1.
- `set_*`: the Jaccard distance `|A xor B| / |A or B|`. Two empty selections give 0.
- `vec_*`: the mean of the per-element terms, each element with its own source range.
- null: 0 when both sides are null, 1 when only one is.

## Sample and cost

`--disclosure-sample N` (`SynthOptions.DisclosureSample`, default 10000; a negative value is `SERVICE_VALIDATION`) caps the rows the distance reads. Past the cap the rows are drawn from the section's own counter-based stream, seeded by `--seed`, never the generation RNG. The exact-match count always reads every generated row (it is a hash lookup).

The cost is sample x source row comparisons. The working set (every source row decoded, plus its exact-match key) is charged against `Options.Limits.MaxEstimatedMemory` BEFORE generation. Past it, the call is refused `PULSE_LIMIT_EXCEEDED` (`limit: max_estimated_memory`) and writes nothing. Lower the cap, pass `--no-disclosure`, or raise the limit. The default limit is unlimited.

## Determinism

The section is byte-identical for a fixed seed and the same on every architecture. Terms are summed in column order and only added or divided, never fused, and each sampled row's minimum is its own, so the worker count cannot move a bit.

## See

- [`synth-fidelity-report`](synth-fidelity-report.md) — the report's other sections.
- [`synthetic-data`](synthetic-data.md) · [`synth-determinism`](synth-determinism.md).
