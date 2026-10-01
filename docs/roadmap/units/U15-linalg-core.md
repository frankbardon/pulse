---
id: U15
slug: linalg-core
title: "One trusted linear-algebra core and a mergeable co-moment accumulator, with no user-visible change"
track: Vector & matrix
size: M
status: not-started
depends_on: [U02, U02b]
soft_depends_on: [U11]
blocks: [U16]
todo_items: [73, 74, 75, 76, 77]
branch: linalg-core
---

# U15 — linalg-core

**Outcome:** One trusted linear-algebra core and a mergeable co-moment accumulator, with no user-visible change.

**Track:** Vector & matrix · **Size:** M · **Depends on:** [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md) · **Soft:** [U11](U11-weighting-descriptive.md) · **Unblocks:** [U16](U16-matrix-result.md)

## Summary

Create `linalg/` (gonum-backed Cholesky, SymEigen, SVD, QR, tolerances, sign convention, ordering) with an import-boundary gate. Migrate synth's hand-rolled Cholesky and regression's solve onto it with byte-identical goldens. Build the weighted, mergeable co-moment accumulator with a deterministic merge tree.

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F1 linalg, F2 co-moment accumulator
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X3 Numerical stability, X4 Determinism
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E1

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#73** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) `linalg/` with Cholesky, SymEigen, SVD, QR, tolerances, sign convention and ordering; import-boundary gate
- [ ] **#74** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Synth Cholesky migrated onto `linalg` (fidelity goldens byte-identical)
- [ ] **#75** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Regression solve and inverse migrated onto `linalg`
- [ ] **#76** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Weighted co-moment accumulator (listwise and pairwise) with exact merge; property tests
- [ ] **#77** (6. Vector & matrix — foundation › E1 — Linear-algebra core & co-moment accumulator) Deterministic merge tree under `ShardWorkers` / `DecodeWorkers`

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

- [ ] All existing goldens (incl. synth fidelity) byte-identical
- [ ] `linalg` imports nothing from `processing/`, `service/`, `descriptor/`
- [ ] The accumulator's merged result equals the single-pass result within the documented tolerance, identically across worker counts
- [ ] Eigenvector signs follow the convention on every platform in CI
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `linalg` import-boundary gate (name e.g. `TestLinalgImportBoundary`)
- Merge determinism test

## Update Demand companions

- CLAUDE.md Architecture (new `linalg/`)
- `.claude/reference/matrix-and-vectors.md` (start it)

## Human inputs & decisions

- None.

## Notes

- If U11 has landed, the accumulator takes the shared weight accessor; otherwise it takes a raw weight column and U11 adapts it.
