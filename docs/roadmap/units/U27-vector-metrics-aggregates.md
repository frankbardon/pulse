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
todo_items: [141, 142, 143, 144, 407, 417]
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
- [x] **#142** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Vector `kind` (`measure` / `scale` / `composition` / `binary`) with metric defaults and `PULSE_VECTOR_METRIC_UNSUITED`
- [ ] **#143** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) `ATTR_SCALE_SCORE`
- [x] **#144** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) `AGG_VEC_MEAN`, `AGG_VEC_SUM` (array value, `expand` option)
- [x] **#407** (10. Vector & matrix — operators › Follow-ups from U25) Fold `MAT_DISTANCE`'s local metric list (`euclidean`, `sqeuclidean`, `manhattan`, `chebyshev`, `cosine`, `mahalanobis`) into the shared `linalg/metric` registry once it lands; one definition per metric
- [ ] **#417** (10. Vector & matrix — operators › Follow-ups from U26) Reference vectors from a `LookupTable`: let expression and profile similarity compare a row's vector or set against a stored reference profile (deferred from U26)

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

- [x] Raw cosine on a `kind: scale` vector warns, naming the suggested metric. Delivered (E1-S3) as the `PULSE_VECTOR_METRIC_UNSUITED` warning (an error under strict) on predict and the runtime, matrix slots and expression calls alike; the separate `PULSE_ADVISORY_COSINE_ON_SCALE` advisory moved here from [U22](U22-recommend-explain.md) was dropped by the U27 interview — one channel, so the same condition never reports twice
- [ ] Vector aggregators merge exactly (streaming/shards)
- [x] `expand: true` yields per-element columns. Delivered (E2-S3): `<label>[<element label>]` scalar columns in every execution mode, named by predict (`aggregations[i].columns`), return planning and chain `stage_schemas`; the columns are presentation only (Sort, windows, post-tests and every overlay host refuse them) — lifting that is handed to [U30](U30-matrix-extensions-hardening.md) #420
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Coverage, example-tag and guidance gates

## Update Demand companions

- `skills/op-attr-scale-score.md`, `op-agg-vec-*.md`
- `errors/fixup_metadata.go` (`PULSE_VECTOR_*`)

## Handed on from U25

| Item | Source |
|---|---|
| `MAT_DISTANCE` metrics into the registry | [U25](U25-multivariate-tests-segmentation.md) #407 |

## Handed on from U26

| Item | Source |
|---|---|
| `LookupTable`-sourced reference vectors for expression / profile similarity | [U26](U26-vector-expr-functions.md) #417 |

## Human inputs & decisions

- None.
