---
id: U30
slug: matrix-extensions-hardening
title: "Embedders can add their own matrix operators, and the matrix stack is proven at scale"
track: Vector & matrix
size: M
status: not-started
depends_on: [U25, U28, U29]
soft_depends_on: []
blocks: [U32]
todo_items: [156, 157, 158, 270]
branch: matrix-extensions-hardening
---

# U30 — matrix-extensions-hardening

**Outcome:** Embedders can add their own matrix operators, and the matrix stack is proven at scale.

**Track:** Vector & matrix · **Size:** M · **Depends on:** [U25](U25-multivariate-tests-segmentation.md), [U28](U28-matrix-overlays.md), [U29](U29-vector-field-types.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`MatrixOpRegistration` and the `MAT` naming-policy namespace with probe validation; benchmarks at p = 50/256 over 1M/10M rows (peak heap, shard scaling); examples-library entries for every new operator.

## References

**Theme documents (read before starting):**
- [vector-matrix 05 — Cross-cutting concerns](../v1.0.0-vector-matrix/05-cross-cutting.md) — X7 Extension points
- [vector-matrix 06 — Phasing](../v1.0.0-vector-matrix/06-phasing.md) — E10 (committed part)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#156** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) `MatrixOpRegistration` and the `MAT` naming-policy namespace; probe validation
- [ ] **#157** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) Benchmarks: p = 50 / 256 at 1M and 10M rows; peak heap; shard scaling
- [ ] **#158** (10. Vector & matrix — operators › E10 — Extensions & hardening (committed part)) Examples-library entries for every new operator
- [ ] **#270** (10. Vector & matrix — operators › Follow-ups from U24) Matrix hardening: a deterministic test for the `MAT_COLLINEARITY` not-positive-definite Belsley warning path (intercept collinearity past double precision), and a per-pair Σw² for probability-weighted pairwise `item_sd` in the co-moment state

## Scope

**In scope**
- Extension registration + probe
- Benchmarks
- Examples completeness

**Out of scope**
- Stretch operators

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(matrix-extensions-hardening/E<n>-S<m>): …`; close each epic with `milestone(matrix-extensions-hardening/E<n>): vertical slice complete — <epic title>`.

### E1 — Embedders can extend matrices
- S1: `MatrixOpRegistration` + naming policy + probe + manifest/predict parity

### E2 — Proven at scale and documented by example
- S1: benchmarks + recorded baselines
- S2: examples for every new operator

## Acceptance criteria

- [ ] An extension `MAT_ACME_X_Y` registers, appears in manifest/predict/MCP identically to built-ins, and is probe-validated
- [ ] Benchmarks are committed with peak-heap figures
- [ ] `TestEveryOperatorHasAnExampleTag` passes for all new operators
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestExtensions_*` extended
- `TestEveryOperatorHasAnExampleTag`

## Update Demand companions

- `docs/src/internals/extension-points.md`; CLAUDE.md naming-policy regex

## Handed on from U16

- **`BlockMerger` through `extend`.** The blocked merge opt-in (`internal/processing/block_merge.go`) is internal; `MatrixOpRegistration` (#156) must expose it, or an embedder `MAT_*`-style operator is bit-unstable across worker counts. Today `MAT_` is NOT in the extension naming regex and `matrixFinalizers` has no extension registry (`.claude/reference/matrix-and-vectors.md`, Engine).
- **Benchmarks** (#157) start from the grouped cost figures predict reports (`estimated_bytes` per bucket count) and `TestMatrixGrouped_WorkerInvariant`.


## Human inputs & decisions

- None.
