---
id: U15
slug: linalg-core
title: "One trusted linear-algebra core and a mergeable co-moment accumulator, with no user-visible change"
track: Vector & matrix
size: M
status: done
depends_on: [U02, U02b]
soft_depends_on: [U11]
blocks: [U16]
todo_items: [73, 74, 75, 76, 77]
branch: linalg-core
---

# U15 — linalg-core

**Outcome:** One trusted linear-algebra core and a mergeable co-moment accumulator, with no user-visible change.

**Track:** Vector & matrix · **Size:** M · **Depends on:** [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md) (landed: `extend` public, `processing` internal) · **Soft:** [U11](U11-weighting-descriptive.md) · **Unblocks:** [U16](U16-matrix-result.md)

## Summary

Create public `linalg/` (FMA-free pure-Go reference Cholesky / SolveSPD / InverseSPD / co-moments; gonum-backed SymEigen, SVD, QR and the regression SPD path; tolerances, sign convention, ordering) with an import-boundary gate. Migrate synth's hand-rolled Cholesky and regression's solve onto it with byte-identical goldens. Build the weighted, mergeable co-moment accumulator with a deterministic merge tree.

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F1 linalg, F2 co-moment accumulator
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X3 Numerical stability, X4 Determinism
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E1

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#73** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) `linalg/` with Cholesky, SymEigen, SVD, QR, tolerances, sign convention and ordering; import-boundary gate
- [x] **#74** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Synth Cholesky migrated onto `linalg` (fidelity goldens byte-identical)
- [x] **#75** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Regression solve and inverse migrated onto `linalg`
- [x] **#76** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Weighted co-moment accumulator (listwise and pairwise) with exact merge; property tests
- [x] **#77** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Deterministic merge tree under `ShardWorkers` / `DecodeWorkers`

## Scope

**In scope**
- `linalg/` + boundary gate
- Synth and regression migrations
- Co-moment accumulator (listwise/pairwise, weighted) + property tests
- Deterministic merge under `ShardWorkers` / `DecodeWorkers`

**Out of scope**
- Any request/response surface (U16)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(linalg-core/E<n>-S<m>): …`; close each epic with `milestone(linalg-core/E<n>): vertical slice complete — <epic title>`.

### E1 — One linear-algebra core
- S1: `linalg/` with policies (tolerance, sign, ordering) + boundary gate
- S2: migrate synth Cholesky (fidelity goldens byte-identical)
- S3: migrate regression solve/inverse

### E2 — A co-moment accumulator that merges exactly
- S1: weighted listwise/pairwise accumulator + merge(a,b) == single-pass property tests
- S2: deterministic merge tree; serial == parallel == sharded

## Acceptance criteria

- [x] All existing goldens (incl. synth fidelity) byte-identical — plus new bitwise pins on the synth factor and correlated draw (`TestCholesky_BitIdenticalToSynthReference`, `TestCholeskyRidge_BitIdenticalToSynthReference`) and regression's SPD path (`TestFactorSPD_MatchesGonumBitwise`, `TestMul_MatchesGonumBitwise`)
- [x] `linalg` imports nothing from `internal/processing/`, `internal/service/`, `descriptor/` (`TestLinalgImportBoundary`; no gonum type on the surface, `TestLinalgSurfaceNamesNoGonum`)
- [x] The accumulator's merged result equals the single-pass result within the documented tolerance (1e-10 relative, `TestCoMomentMergeMatchesSerial`), identically across worker counts (`TestCoMomentMergeTree_WorkerInvariant`, bitwise)
- [x] Eigenvector signs follow the convention on every platform in CI (`TestSymEigenSignRule`, `TestSVDSignRule`, `TestQRSignRule`)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `linalg` import-boundary gate (name e.g. `TestLinalgImportBoundary`)
- Merge determinism test

## Update Demand companions

- CLAUDE.md Architecture (new `linalg/`)
- `.claude/reference/matrix-and-vectors.md` (started: backend policy, tolerances, `CoMoment` semantics, blocked merge)
- `.claude/reference/architecture.md` (public tree, `linalg` guards, two-backend split); `docs/src/library/linalg.md`; migration guide "Changes from U15"

## Human inputs & decisions

- None.

## Notes

- If U11 has landed, the accumulator takes the shared weight accessor; otherwise it takes a raw weight column and U11 adapts it.

## Outcome

- `linalg` is a PUBLIC package (not `internal/`), and `CoMoment` lives inside it rather than in a separate `processing/comoment`.
- **Two backends, by evidence.** A gonum-backed Cholesky changed synth's factor bits on 96% of random SPD inputs and 21% of its rank-deficient factor-or-fail decisions. So the bit-contract kernels (`Cholesky`, `CholeskyRidge`, `SolveSPD`, `InverseSPD`, `CoMoment` `Add` / `Merge`, `MergeTree`) are pure Go and FMA-free. gonum backs `SymEigen`, `SVD`, `QR`, `Rank`, `ConditionNumber` and the `FactorSPD` / `SPDFactor` / `Mul` path regression runs on.
- Errors `PULSE_MATRIX_SINGULAR` and `PULSE_MATRIX_SHAPE_MISMATCH`; synth still raises `SERVICE_VALIDATION`, regression keeps its own rank-deficient codes.
- **Blocked merge.** 4096-record blocks keyed by absolute record index; parallel-decode segments are snapped to block multiples. An internal `processing.BlockMerger` opt-in runs a two-level fixed tree, so serial == any `DecodeWorkers` == any `ShardWorkers`, bitwise. Documented caveat: a multi-shard archive vs its single-file twin may differ in the last bits.

## Handed on

| Item | Owner |
|---|---|
| `MAT_*` behaviour on join and ProcessChain paths: those records carry no merge position and a `BlockMerger` refuses them `PROCESSING_INTERNAL` — stamp them or refuse at predict | U16 |
| The first real `BlockMerger` (a `MAT_*` operator) must pass `TestCoMomentMergeTree_WorkerInvariant`'s invariance through its real registration | U16 |
| `details.rank` / `details.condition_number` are not populated on the reference Cholesky or `FactorSPD` failure paths | U16 (with the remaining `PULSE_MATRIX_*` codes, e.g. `PULSE_MATRIX_NOT_PSD`) |
| Migrate the existing mergeable reducers (`MergeOnline`, equal only within ulps across worker counts) onto the blocked tree; porting a weighted reducer onto `CoMoment` moves its row count on `w = 0` rows (`CoMoment` counts them, the weighted aggregators skip them) | new (deterministic merge for existing reducers) |
| `BlockMerger` is not reachable through `extend`, so extension operators cannot opt in to bit-exact merging | U34 / new (on demand) |
