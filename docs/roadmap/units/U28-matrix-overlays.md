---
id: U28
slug: matrix-overlays
title: "Crosstabs and matrices gain residuals, perceptual maps, flow projections, raking and corrected p-values"
track: Vector & matrix
size: L
status: not-started
depends_on: [U24, U13]
soft_depends_on: []
blocks: [U30, U31]
todo_items: [145, 146, 147, 148, 149, 150]
branch: matrix-overlays
---

# U28 — matrix-overlays

**Outcome:** Crosstabs and matrices gain residuals, perceptual maps, flow projections, raking and corrected p-values.

**Track:** Vector & matrix · **Size:** L · **Depends on:** [U24](U24-matrix-operators.md), [U13](U13-multiplicity.md) · **Unblocks:** [U30](U30-matrix-extensions-hardening.md), [U31](U31-guidance-guides.md)

## Summary

The MATRIX_RESULT overlay host with the `Ref.Matrix` reference family, `OVERLAY_STD_RESIDUAL`, `OVERLAY_CORRESPONDENCE`, `OVERLAY_MARKOV`, `OVERLAY_RAKE`, and `MatrixSpec.multiplicity` wired to the shared correction core (producing `p_adjusted`).

## References

**Theme documents (read before starting):**
- [vector-matrix 04 — Matrix ops on results](../v1.0.0-vector-matrix/04-matrix-ops-on-results.md) — Hosts, Committed kinds
- [statistical-integrity 02 — Multiple comparisons](../v1.0.0-statistical-integrity/02-multiple-comparisons.md) — Output (MatrixResult p_adjusted)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#145** (10. Vector & matrix — operators › E6 — Matrix operations on results (overlays)) MATRIX_RESULT overlay host and the `Ref.Matrix` reference family
- [ ] **#146** (10. Vector & matrix — operators › E6 — Matrix operations on results (overlays)) `OVERLAY_STD_RESIDUAL`
- [ ] **#147** (10. Vector & matrix — operators › E6 — Matrix operations on results (overlays)) `OVERLAY_CORRESPONDENCE`
- [ ] **#148** (10. Vector & matrix — operators › E6 — Matrix operations on results (overlays)) `OVERLAY_MARKOV`
- [ ] **#149** (10. Vector & matrix — operators › E6 — Matrix operations on results (overlays)) `OVERLAY_RAKE`
- [ ] **#150** (10. Vector & matrix — operators › E6 — Matrix operations on results (overlays)) `MatrixSpec.multiplicity` → `p_adjusted` auxiliary matrix via the shared correction core (replaces the dropped `OVERLAY_CORR_PVALUE`)

## Scope

**In scope**
- New host + ref family
- Four overlay kinds
- Matrix multiplicity wiring

**Out of scope**
- Stretch overlays (similarity, seriation, matrix formula, congruence)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(matrix-overlays/E<n>-S<m>): …`; close each epic with `milestone(matrix-overlays/E<n>): vertical slice complete — <epic title>`.

### E1 — Crosstab cells explain themselves
- S1: `OVERLAY_STD_RESIDUAL`
- S2: `OVERLAY_CORRESPONDENCE` (payload-shape decision: two-layer vs `Series2`)

### E2 — Flows and targets
- S1: `OVERLAY_MARKOV` (steady state, n-step, absorbing)
- S2: `OVERLAY_RAKE` (IPF; convergence warning)

### E3 — Matrices carry corrected p-values
- S1: MATRIX_RESULT host + `Ref.Matrix`
- S2: `MatrixSpec.multiplicity` → `p_adjusted`

## Acceptance criteria

- [ ] Residuals match published worked examples
- [ ] CA coordinates match a reference (e.g. R `ca`) up to the documented sign convention
- [ ] Markov steady state matches power iteration; reducible chains warn
- [ ] Overlay-free responses remain byte-identical
- [ ] Matrix `p_adjusted` matches the U13 reference values
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllOverlayKinds`; `Test*_OverlayFreeByteIdentical`; overlay predict/runtime misshape twins

## Update Demand companions

- `skills/op-overlay-*.md`; `overlay-system.md` (host + ref family, fenced)
- `types.AllOverlayKinds()`; `OverlayStreamability` rows

## Inherited from U13

- **Reuse the shipped core and fold.** Correction core: `internal/processing/multiplicity` (`Adjust`, `FamilySize`; R `p.adjust` fixtures). Config: `types.Multiplicity{method,family,alpha}`; families `layer|row|column|request|compose` — add `matrix` to `types.AllMultiplicityFamilies()` and the validity matrix in `internal/descriptor/multiplicity_resolve.go` (one resolver for predict and runtime). Fold: `internal/service/multiplicity_fold.go` (`multFamilies`, `multFamilyKey`) and the overlay p-site table `overlayPSites` in `multiplicity_overlay.go` — every new inferential kind needs a row (`TestOverlayPSites_CoverEveryInferentialKind` enforces it) and outputs `p_adjusted` / `significant_adjusted` beside the raw p, never in place. Contract: `.claude/reference/execution-modes.md` (Multiplicity); skill `skills/multiplicity-correction.md`.

## Human inputs & decisions

- Decide the correspondence-analysis payload shape (open question in vm6)
