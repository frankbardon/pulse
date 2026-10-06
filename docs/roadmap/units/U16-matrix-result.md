---
id: U16
slug: matrix-result
title: "A first correlation matrix, end to end, through the library, CLI and MCP"
track: Vector & matrix
size: L
status: not-started
depends_on: [U15, U11]
soft_depends_on: [U07]
blocks: [U24, U26, U29]
todo_items: [78, 79, 80, 81, 82, 83, 84, 172]
branch: matrix-result
---

# U16 — matrix-result

**Outcome:** A first correlation matrix, end to end, through the library, CLI and MCP.

**Track:** Vector & matrix · **Size:** L · **Depends on:** [U15](U15-linalg-core.md), [U11](U11-weighting-descriptive.md) · **Soft:** [U07](U07-guidance-metadata.md) · **Unblocks:** [U24](U24-matrix-operators.md), [U26](U26-vector-expr-functions.md), [U29](U29-vector-field-types.md)

## Summary

Add virtual vectors (`Request.Vectors`) and the typed matrix result (`Request.Matrices` / `Response.Matrices`, `MatrixResult`, symmetric/upper encoding). Covers components floor, manifest `Matrix` block, predict shape/cost, payload-schema golden, size controls, missing-data modes, and the first two operators `MAT_COVARIANCE` and `MAT_CORRELATION` (Pearson).

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F3 Virtual vectors
- [vector-matrix 02 — Matrix result shape](../v1.0.0-vector-matrix/02-matrix-result.md) — R1–R5
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X2 Missing data
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E2

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#78** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `Request.Vectors`: resolution, hashing, projection, predict echo
- [ ] **#79** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `Request.Matrices` / `Response.Matrices`, `MatrixResult`, and the symmetric/upper-triangle `MatrixPayload` encoding
- [ ] **#80** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) Matrix components floor; manifest `Matrix` capability block; predict shape and cost
- [ ] **#81** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) Payload-schema golden regenerated; `.claude/reference/matrix-and-vectors.md`
- [ ] **#82** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `MaxMatrixDim`, `precision` and `top_pairs` controls
- [ ] **#83** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `MAT_COVARIANCE`
- [ ] **#84** (6. Vector & matrix — foundation › E2 — Virtual vectors & matrix result) `MAT_CORRELATION` (Pearson), with parity against `TEST_PEARSON_R`
- [ ] **#172** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Missing-data modes documented and tested; PSD refusal / `repair: "nearest"`

## Scope

**In scope**
- Vectors: resolution, hashing, projection, predict echo
- Matrices request/response, `MatrixResult`, encoding
- Components, manifest, predict, schema
- `precision`, `top_pairs`; `MaxMatrixDim` reserved (enforced via U19 limits)
- Listwise/pairwise + PSD refusal / `repair: nearest`
- `MAT_COVARIANCE`, `MAT_CORRELATION` Pearson with `TEST_PEARSON_R` parity
- `MAT` registry category + Purpose for both operators

**Out of scope**
- Other `MAT_*` (U24)
- Native vector types (U29)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(matrix-result/E<n>-S<m>): …`; close each epic with `milestone(matrix-result/E<n>): vertical slice complete — <epic title>`.

### E1 — Requests can name vectors
- S1: `Request.Vectors` resolution (fields/glob/pattern), schema order, hashing, `NeededFields` expansion, predict echo

### E2 — Responses can carry matrices
- S1: `Request.Matrices` / `Response.Matrices`, `MatrixResult`, symmetric/upper encoding
- S2: components floor, manifest `Matrix` block, predict shape and cost; schema golden
- S3: missing-data modes, PSD check, Higham repair, precision/top_pairs

### E3 — First correlation matrix end to end
- S1: `MAT_COVARIANCE`
- S2: `MAT_CORRELATION` Pearson + parity vs N² `TEST_PEARSON_R`
- S3: CLI rendering + MCP output + atomic skills + examples

## Acceptance criteria

- [ ] A Pearson correlation matrix matches N² `TEST_PEARSON_R` runs byte-for-byte
- [ ] Weighted covariance passes the unity/frequency parity gates
- [ ] Pairwise deletion that yields a non-PSD matrix warns `PULSE_MATRIX_NOT_PSD`
- [ ] Streaming, sharded and parallel runs give identical matrices
- [ ] `format_version` stays "1.1"; schema golden regenerated
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New `TestSkillsCoverAllMatrixOps` + `TestManifestMatrixOpsComplete`
- Matrix parity tests
- Example tags for both operators

## Update Demand companions

- CLAUDE.md Output Format Contract (new Response slot) + `.claude/reference/matrix-and-vectors.md` (long form; keeps CLAUDE.md ≤ 50 KB)
- `update-demand.md` rows for `Request.Vectors` / `Matrices` / `Response.Matrices` and the `MAT` category
- `skills/op-mat-covariance.md`, `skills/op-mat-correlation.md`; `response-components.md`
- `docs/src/contract/payload-schema.md`

## Human inputs & decisions

- None.

## Handed on from U15

U15 (linalg-core) left these to this unit. Contract: `.claude/reference/matrix-and-vectors.md`; engine wiring: `.claude/reference/execution-modes.md` (Blocked merge).

- **Build on `linalg.CoMoment`.** `MAT_COVARIANCE` / `MAT_CORRELATION` should implement the internal `processing.BlockMerger` opt-in (`internal/processing/block_merge.go`). Their state is per-block `CoMoment`s keyed by absolute record index. A two-level `linalg.MergeTree` runs at `Finalize`.
- **Inherited gate.** `TestCoMomentMergeTree_WorkerInvariant` (`internal/service/comoment_merge_tree_test.go`) proves serial == any `DecodeWorkers` == any `ShardWorkers` bitwise for a `_test.go`-only reducer. The real `MAT_*` registrations must pass the same invariance. This acceptance criterion ("streaming, sharded and parallel runs give identical matrices") rides on it. Caveat: a multi-shard archive vs its single-file twin may differ in the last bits; only a one-shard archive is bit-equal.
- **Join and chain paths.** Records built by a join (`copyStateInto`) and by ProcessChain intermediate stages carry no merge position. A `BlockMerger` refuses them `PROCESSING_INTERNAL`. Decide: stamp positions on those paths, or refuse `MAT_*` with joins / on chain stages ≥ 1 at predict with a user-facing code.
- **Remaining `PULSE_MATRIX_*` codes.** U15 shipped `PULSE_MATRIX_SINGULAR` and `PULSE_MATRIX_SHAPE_MISMATCH` only. `PULSE_MATRIX_NOT_PSD` (pairwise non-PSD warning) and any others are this unit's to add. Also decide whether `PULSE_MATRIX_SINGULAR` gains `details.rank` / `details.condition_number` on the reference Cholesky and `FactorSPD` paths; today they carry `pivot` and `reason` / `n` only.
- **Weight semantics.** `CoMoment` counts a `w = 0` row toward `N`, while the existing weighted aggregators skip it. `MAT_*` parity gates against `TEST_PEARSON_R` / weighted aggregators must account for that.
