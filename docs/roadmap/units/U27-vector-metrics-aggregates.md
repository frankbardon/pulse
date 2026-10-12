---
id: U27
slug: vector-metrics-aggregates
title: "Similarity is safe by default, and groups can be summarized as profiles"
track: Vector & matrix
size: L
status: done
depends_on: [U26]
soft_depends_on: []
blocks: [U29]
todo_items: [141, 142, 143, 144, 407, 417]
branch: vector-metrics-aggregates
---

# U27 — vector-metrics-aggregates

**Outcome:** Similarity is safe by default, and groups can be summarized as profiles.

**Track:** Vector & matrix · **Size:** L · **Depends on:** [U26](U26-vector-expr-functions.md) · **Unblocks:** [U29](U29-vector-field-types.md)

## Summary

The shared metric registry `linalg/metric`, vector `kind` (measure/scale/composition/binary) with metric defaults and the `PULSE_VECTOR_METRIC_UNSUITED` warning, `ATTR_SCALE_SCORE`, `AGG_VEC_MEAN` / `AGG_VEC_SUM` (array value with an `expand` option), and, promoted from Stretch by the interview, `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE` against a reference profile (a literal, or an embedder-registered `ReferenceVectors` table read with `ref()`). Size grew from M to L with the owner-added stories below.

## References

**Theme documents (read before starting):**
- [vector-matrix 07 — Similarity & distance](../v1.0.0-vector-matrix/07-similarity-and-distance.md) — S1 metric registry, S2 vector kind
- [vector-matrix 03 — Multivariate statistics](../v1.0.0-vector-matrix/03-multivariate-statistics.md) — Vector aggregators, ATTR_SCALE_SCORE

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#141** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Shared metric registry `linalg/metric`
- [x] **#142** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Vector `kind` (`measure` / `scale` / `composition` / `binary`) with metric defaults and `PULSE_VECTOR_METRIC_UNSUITED`
- [x] **#143** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) `ATTR_SCALE_SCORE`
- [x] **#144** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) `AGG_VEC_MEAN`, `AGG_VEC_SUM` (array value, `expand` option)
- [x] **#407** (10. Vector & matrix — operators › Follow-ups from U25) Fold `MAT_DISTANCE`'s local metric list (`euclidean`, `sqeuclidean`, `manhattan`, `chebyshev`, `cosine`, `mahalanobis`) into the shared `linalg/metric` registry once it lands; one definition per metric
- [x] **#417** (10. Vector & matrix — operators › Follow-ups from U26) Reference vectors from a `LookupTable`: let expression and profile similarity compare a row's vector or set against a stored reference profile (deferred from U26)

## Scope

**In scope**
- Metric registry
- Vector kind + defaults + warning
- `ATTR_SCALE_SCORE`
- Vector aggregators
- Reference profiles: `ReferenceVectors`, `ref()`, `ATTR_PROFILE_SIMILARITY`, `ATTR_VEC_DISTANCE`
- A typed components surface for attributes; shard / single-file bit-exactness for vector figures

**Out of scope**
- Stretch similarity operators (top-k, set affinity)
- Group-centroid and point-lookup-record references, `AGG_VEC_SD`, computing on expanded columns, `chi_square` exposure (all handed on, below)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(vector-metrics-aggregates/E<n>-S<m>): …`; close each epic with `milestone(vector-metrics-aggregates/E<n>): vertical slice complete — <epic title>`.

### E1 — Similarity is safe by default
- S1: `linalg/metric` registry; manifest metric list
- S2: vector `kind` defaults + unsuited-metric warning (error under `--strict`)

### E2 — Batteries become scores and profiles
- S1: `ATTR_SCALE_SCORE` (min-valid, reverse scoring)
- S2: `AGG_VEC_MEAN`/`AGG_VEC_SUM` (array value, `expand`); crosstab refuses them
- S3: `expand: true` per-element columns in every mode, predict `columns`, return planning and chain typing
- S4 (owner-added): `Response.Components.Attributes` — a typed components surface for attributes, keyed by `label` + `type` (`ATTR_SCALE_SCORE` reports `n_out_of_range` there)
- S5 (owner-added): a shard archive matches its single-file twin bit for bit for `AGG_VEC_*` and `MAT_*` (merge blocks keyed by global ordinal); extension aggregators may take vector input and return arrays

### E3 — Rows compare to a target profile
- S1: `ReferenceVectors` named table (`Options.Extensions`) and `ref(table, key)` in formulas
- S2: `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE` against a literal, label map, `{table,key}` or `{table,key_field}` reference
- S3: roadmap and docs close-out
- S4 (owner-added): `PULSE_VECTOR_METRIC_DOMAIN` and `PULSE_VECTOR_REFERENCE_MISSING` warnings for rows a reference comparison nulls; `key_field` accepts unsigned-integer columns as well as categorical

## Acceptance criteria

- [x] Raw cosine on a `kind: scale` vector warns, naming the suggested metric. Delivered (E1-S3) as the `PULSE_VECTOR_METRIC_UNSUITED` warning (an error under strict) on predict and the runtime, matrix slots and expression calls alike; the separate `PULSE_ADVISORY_COSINE_ON_SCALE` advisory moved here from [U22](U22-recommend-explain.md) was dropped by the U27 interview — one channel, so the same condition never reports twice
- [x] Vector aggregators merge exactly (streaming/shards). Delivered (E2-S5): a shard archive gives the same bits as its single-file twin across `ShardWorkers` / `DecodeWorkers`; an extension's own `Merge` stays outside the blocked merge, so embedder code is ULP-equal across workers
- [x] `expand: true` yields per-element columns. Delivered (E2-S3): `<label>[<element label>]` scalar columns in every execution mode, named by predict (`aggregations[i].columns`), return planning and chain `stage_schemas`; the columns are presentation only (Sort, windows, post-tests and every overlay host refuse them) — lifting that is handed to [U30](U30-matrix-extensions-hardening.md) #420
- [x] `ATTR_PROFILE_SIMILARITY` / `ATTR_VEC_DISTANCE` score each row against a reference profile (promoted from Stretch). Delivered (E3-S2): `metric` from the registry, `mahalanobis` equals `ATTR_MAHALANOBIS`, weighted path honours the weight class, streaming / buffered / parallel / shard parity; a row the comparison nulls warns (`PULSE_VECTOR_METRIC_DOMAIN`, `PULSE_VECTOR_REFERENCE_MISSING`) (E3-S4)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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

## Handed on from U27

Filed as TODO items (ids are TODO ids, not GitHub issues).

| Found in | What is still open | TODO | Owner |
|---|---|---|---|
| E2-S3 | Sort, overlays and ratios over expanded vector-aggregate columns | #420 | [U30](U30-matrix-extensions-hardening.md) |
| E3 | Group-centroid references (an `AGG_VEC_MEAN` of a named filter as the reference, two-pass) | #423 | [U30](U30-matrix-extensions-hardening.md) |
| E3 | Point-lookup-record references ("rows like customer #4411") | #424 | [U30](U30-matrix-extensions-hardening.md) |
| E2 | `AGG_VEC_SD` | #425 | [U30](U30-matrix-extensions-hardening.md) |
| E1 | `chi_square` is registered but exposed by no consumer; crosstab-profile overlays expose it | #426 | [U28](U28-matrix-overlays.md) |
| E3-S4 | A `u64` `key_field` above 2^53 is matched via its float64 value (same limit as `ATTR_CODE_IN`); no unit owns exact `u64` record access, so it sits with predict / runtime parity | #427 | [U35](U35-predict-runtime-parity.md) |
| E3 | Whether a feature profile can hide `ReferenceVectors` (appended to the `LookupTables` question; a decision, not a TODO item) | — | [U34](U34-extension-validation.md) |

## Human inputs & decisions

Owner decisions that changed the planned scope:

- **Reference vectors and the two attributes were pulled into scope** (#417; both attributes were Stretch). `ReferenceVectors` is a named table registered through `Options.Extensions`; `ref("table", "key")` reads one in a formula (the table must be a literal; a computed key that matches nothing is `PULSE_VECTOR_REFERENCE_UNKNOWN`, a null key is null). Reference vectors are not part of `feature_set_digest`.
- **One channel for unsuited metrics.** `PULSE_VECTOR_METRIC_UNSUITED` (error under strict) replaces the planned `PULSE_ADVISORY_COSINE_ON_SCALE`.
- **Attributes got a components surface** (E2-S4) so `ATTR_SCALE_SCORE` reports `n_out_of_range` as a count, not only in a warning.
- **Bit-exactness across shards** (E2-S5) and extension vector-input / array-valued aggregators were added.
- **Reference comparisons no longer null silently** (E3-S4): `PULSE_VECTOR_METRIC_DOMAIN` and `PULSE_VECTOR_REFERENCE_MISSING`; `key_field` also accepts `u4`–`u64`.
