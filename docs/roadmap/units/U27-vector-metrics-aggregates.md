---
id: U27
slug: vector-metrics-aggregates
title: "Similarity is safe by default, and groups can be summarized as profiles"
track: Vector & matrix
size: M
status: not-started
depends_on: [U26]
soft_depends_on: []
blocks: [U29]
todo_items: [141, 142, 143, 144]
branch: vector-metrics-aggregates
---

# U27 — vector-metrics-aggregates

**Outcome:** Similarity is safe by default, and groups can be summarized as profiles.

**Track:** Vector & matrix · **Size:** M · **Depends on:** [U26](U26-vector-expr-functions.md) · **Unblocks:** [U29](U29-vector-field-types.md)

## Summary

The shared metric registry `linalg/metric`, vector `kind` (measure/scale/composition/binary) with metric defaults and the `PULSE_VECTOR_METRIC_UNSUITED` warning, `ATTR_SCALE_SCORE`, and `AGG_VEC_MEAN` / `AGG_VEC_SUM` (array value with an `expand` option).

## References

**Theme documents (read before starting):**
- [vector-matrix 07 — Similarity & distance](../v1.0.0-vector-matrix/07-similarity-and-distance.md) — S1 metric registry, S2 vector kind
- [vector-matrix 03 — Multivariate statistics](../v1.0.0-vector-matrix/03-multivariate-statistics.md) — Vector aggregators, ATTR_SCALE_SCORE

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#141** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Shared metric registry `linalg/metric`
- [ ] **#142** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Vector `kind` (`measure` / `scale` / `composition` / `binary`) with metric defaults and `PULSE_VECTOR_METRIC_UNSUITED`
- [ ] **#143** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) `ATTR_SCALE_SCORE`
- [ ] **#144** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) `AGG_VEC_MEAN`, `AGG_VEC_SUM` (array value, `expand` option)

## Scope

**In scope**
- Metric registry
- Vector kind + defaults + warning
- `ATTR_SCALE_SCORE`
- Vector aggregators

**Out of scope**
- Stretch similarity operators (top-k, set affinity)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(vector-metrics-aggregates/E<n>-S<m>): …`; close each epic with `milestone(vector-metrics-aggregates/E<n>): vertical slice complete — <epic title>`.

### E1 — Similarity is safe by default
- S1: `linalg/metric` registry; manifest metric list
- S2: vector `kind` defaults + unsuited-metric warning (error under `--strict`)

### E2 — Batteries become scores and profiles
- S1: `ATTR_SCALE_SCORE` (min-valid, reverse scoring)
- S2: `AGG_VEC_MEAN`/`AGG_VEC_SUM` (array value, `expand`); crosstab refuses them

## Acceptance criteria

- [ ] Raw cosine on a `kind: scale` vector warns, naming the suggested metric; this is `PULSE_ADVISORY_COSINE_ON_SCALE`, moved here from [U22](U22-recommend-explain.md) because it needs `kind: scale` vectors, and it rides the predict `advisories[]` slot (the `predict-advisories` skill pattern, suppressible through `Options.SuppressAdvisories`)
- [ ] Vector aggregators merge exactly (streaming/shards)
- [ ] `expand: true` yields per-element columns
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Coverage, example-tag and guidance gates

## Update Demand companions

- `skills/op-attr-scale-score.md`, `op-agg-vec-*.md`
- `errors/fixup_metadata.go` (`PULSE_VECTOR_*`)

## Human inputs & decisions

- None.
