---
id: U16
slug: matrix-result
title: "A first correlation matrix, end to end, through the library, CLI and MCP"
track: Vector & matrix
size: L
status: done
depends_on: [U15, U11]
soft_depends_on: [U07]
blocks: [U24, U26, U29]
todo_items: [78, 79, 80, 81, 82, 83, 84, 172]
branch: matrix-result
---

# U16 — matrix-result

**Outcome:** A first correlation matrix, end to end, through the library, CLI and MCP.

**Status:** done (PR #317, branch `matrix-result`; no release tag cut by the unit) · **Track:** Vector & matrix · **Size:** L · **Depends on:** [U15](U15-linalg-core.md), [U11](U11-weighting-descriptive.md) · **Soft:** [U07](U07-guidance-metadata.md) · **Unblocks:** [U24](U24-matrix-operators.md), [U26](U26-vector-expr-functions.md), [U29](U29-vector-field-types.md)

## Summary

Add virtual vectors (`Request.Vectors`) and the typed matrix result (`Request.Matrices` / `Response.Matrices`, `MatrixResult`, symmetric/upper encoding). Covers components floor, manifest `Matrix` block, predict shape/cost, payload-schema golden, size controls, missing-data modes, and the first two operators `MAT_COVARIANCE` and `MAT_CORRELATION` (Pearson).

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F3 Virtual vectors
- [vector-matrix 02 — Matrix result shape](../v1.0.0-vector-matrix/02-matrix-result.md) — R1–R5
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X2 Missing data
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E2

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#78** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `Request.Vectors`: resolution, hashing, projection, predict echo
- [x] **#79** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `Request.Matrices` / `Response.Matrices`, `MatrixResult`, and the symmetric/upper-triangle `MatrixPayload` encoding
- [x] **#80** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) Matrix components floor; manifest `Matrix` capability block; predict shape and cost
- [x] **#81** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) Payload-schema golden regenerated; `.claude/reference/matrix-and-vectors.md`
- [x] **#82** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `MaxMatrixDim`, `precision` and `top_pairs` controls — U16 half: `top_pairs` shipped; `precision` → [U17](U17-response-shaping-core.md), `MaxMatrixDim` → [U19](U19-resource-limits.md)
- [x] **#83** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `MAT_COVARIANCE`
- [x] **#84** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `MAT_CORRELATION` (Pearson), with parity against `TEST_PEARSON_R`
- [x] **#172** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Missing-data modes documented and tested; PSD refusal / `repair: "nearest"` — U16 half: missing-data modes + `PULSE_MATRIX_NOT_PSD` detection shipped; `repair: "nearest"` and the non-PSD refusal in decompositions → [U24](U24-matrix-operators.md)

## Scope

**Delivered**
- Vectors: resolution (literal / glob / pattern), hashing, projection, predict echo (`resolved_vectors`)
- Matrices request/response, `MatrixResult`, dedicated `MatrixValues` payload (`full` / `upper`), components floor, manifest `matrices[]` + `matrix` block, predict shape and cost, payload schema
- `MAT_COVARIANCE`, `MAT_CORRELATION` (Pearson), weighted under frequency and probability weights; `MAT` registry category + Purpose / Interpretation
- Missing modes (listwise / pairwise), `auxiliary.n`, warnings (`NOT_PSD` detection, `LISTWISE_HEAVY_DROP`, `INSUFFICIENT_N`, `ZERO_VARIANCE`), `top_pairs`
- Parallel and sharded runs, bit-identical to serial (blocked merge from U15)
- Matrices per group bucket, in final `Response.Data` order including `Request.Sort` (owner decision 2026-10-06, story E4-S3 added mid-effort); predict `bucket_basis` / `estimated_*`
- Refusals shared by predict and runtime: joins / chain stage >= 1 (`PULSE_MATRIX_UNSUPPORTED_SOURCE`), crosstab (`PULSE_MATRIX_HOST_CONFLICT`)

**Handed on** (each has an owner; see the units named)
- `precision` on matrix cells: U17. `MaxMatrixDim` and a buckets x p^2 guard: U19.
- Higham `repair: "nearest"`, non-PSD refusal in decompositions, `PULSE_MATRIX_SINGULAR` rank / condition details, Jacobi vs gonum portability: U24.
- Raw `auxiliary.p` and intervals on correlation, the MATRIX_RESULT host reading `MatrixValues`, matrices inside crosstab arms: U28.
- `BlockMerger` through `extend`: U30.
- Buffered-crosstab nil-axis-grouper panic (pre-existing), docs re-check of the `op-mat-*` skill budgets: U32. Multi-entry `Groups`: U35 (#200).

**Out of scope**
- Other `MAT_*` (U24); native vector types (U29)

## Epics & stories

Delivered as five vertical-slice epics (the original three-epic plan was re-cut during interview).

- **E1 — An ungrouped covariance matrix, end to end:** vectors, `MAT_COVARIANCE`, refusals, Compose / chain stage 0, feature gate.
- **E2 — Parallel and sharded matrices:** bit-identical to serial under `DecodeWorkers` / `ShardWorkers`.
- **E3 — A trustworthy correlation matrix:** `MAT_CORRELATION`, `TEST_PEARSON_R` parity, pairwise, warnings, components, predict, `top_pairs`.
- **E4 — Matrices per group:** S1 per bucket, S2 worker invariance + predict cost, S3 (added 2026-10-06) buckets follow `Request.Sort`.
- **E5 — Contract written down, hand-offs planted:** reference docs, topical skill `matrix-results`, library page, roadmap upkeep.

## Acceptance criteria

- [x] A Pearson correlation matrix matches N^2 `TEST_PEARSON_R` runs within `1e-12 + 1e-12 * |r|` (bitwise on amd64 for unweighted serial runs of up to 4096 rows; arm64 fuses the test's multiply-adds)
- [x] Weighted covariance and correlation match numpy / statsmodels references under both weight kinds; a weight-0 row counts toward `n` (`mat.n == pearson.n + zero_weight_rows`)
- [x] Pairwise deletion that yields a non-PSD matrix warns `PULSE_MATRIX_NOT_PSD`
- [x] Streaming, sharded and parallel runs give identical matrices, per group bucket too (multi-shard archive vs single-file twin: tolerance only)
- [x] Grouped requests return one matrix per non-empty bucket, in `Response.Data` order including `Sort`
- [x] Matrices with joins, chain stage >= 1 and crosstab are refused by predict and runtime identically; crosstab bytes unchanged
- [x] `format_version` stays "1.1"; schema golden regenerated
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllMatrixOps`, `TestManifestMatrixOpsComplete`, `TestMatrixCorrelation_PearsonParity`, `TestMatrixCovariance_WorkerInvariant`, `TestMatrixGrouped_WorkerInvariant`, `TestMatrixRefusal_PredictAndValidatorsMatchRuntime`
- Example tags for both operators (`internal/examples/matrices/`)

## Update Demand companions

- CLAUDE.md Output Format Contract (new Response slot) + `.claude/reference/matrix-and-vectors.md` (long form; keeps CLAUDE.md ≤ 50 KB)
- `update-demand.md` rows for `Request.Vectors` / `Matrices` / `Response.Matrices` and the `MAT` category
- `skills/op-mat-covariance.md`, `skills/op-mat-correlation.md`, topical `skills/matrix-results.md`; `response-components.md`
- `docs/src/contract/payload-schema.md`

## Human inputs & decisions

- None.

## Handed on from U15 (all resolved in U16)

U15 (linalg-core) left these to this unit. Contract: `.claude/reference/matrix-and-vectors.md`; engine wiring: `.claude/reference/execution-modes.md` (Blocked merge).

- **Build on `linalg.CoMoment`.** `MAT_COVARIANCE` / `MAT_CORRELATION` should implement the internal `processing.BlockMerger` opt-in (`internal/processing/block_merge.go`). Their state is per-block `CoMoment`s keyed by absolute record index. A two-level `linalg.MergeTree` runs at `Finalize`.
- **Inherited gate.** `TestCoMomentMergeTree_WorkerInvariant` (`internal/service/comoment_merge_tree_test.go`) proves serial == any `DecodeWorkers` == any `ShardWorkers` bitwise for a `_test.go`-only reducer. The real `MAT_*` registrations must pass the same invariance. This acceptance criterion ("streaming, sharded and parallel runs give identical matrices") rides on it. Caveat: a multi-shard archive vs its single-file twin may differ in the last bits; only a one-shard archive is bit-equal.
- **Join and chain paths.** Records built by a join (`copyStateInto`) and by ProcessChain intermediate stages carry no merge position. A `BlockMerger` refuses them `PROCESSING_INTERNAL`. Decide: stamp positions on those paths, or refuse `MAT_*` with joins / on chain stages ≥ 1 at predict with a user-facing code.
- **Remaining `PULSE_MATRIX_*` codes.** U15 shipped `PULSE_MATRIX_SINGULAR` and `PULSE_MATRIX_SHAPE_MISMATCH` only. `PULSE_MATRIX_NOT_PSD` (pairwise non-PSD warning) and any others are this unit's to add. Also decide whether `PULSE_MATRIX_SINGULAR` gains `details.rank` / `details.condition_number` on the reference Cholesky and `FactorSPD` paths; today they carry `pivot` and `reason` / `n` only.
- **Weight semantics.** `CoMoment` counts a `w = 0` row toward `N`, while the existing weighted aggregators skip it. `MAT_*` parity gates against `TEST_PEARSON_R` / weighted aggregators must account for that.

## Landed notes

- Manifest lists the operators under top-level `matrices[]` (the regression precedent), not `components.matrices`.
- `linalg.CoMoment.Corr` arithmetic changed to the one-root form (migration row in the embedder-migration doc).
- `ZERO_VARIANCE` nulls the correlation row and column only; covariance keeps an exact 0 (numpy-consistent).
- Known gaps recorded in `.claude/reference/matrix-and-vectors.md` (Open edges) with owners: a chain stage holding only matrices is still refused (it needs an aggregator; U28), the buffered-crosstab nil-axis-grouper panic predates U16 (U32).
