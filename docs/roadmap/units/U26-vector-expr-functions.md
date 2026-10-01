---
id: U26
slug: vector-expr-functions
title: "Formulas and filters can work with whole vectors and sets"
track: Vector & matrix
size: M
status: not-started
depends_on: [U16]
soft_depends_on: []
blocks: [U27]
todo_items: [137, 138, 139, 140]
branch: vector-expr-functions
---

# U26 — vector-expr-functions

**Outcome:** Formulas and filters can work with whole vectors and sets.

**Track:** Vector & matrix · **Size:** M · **Depends on:** [U16](U16-matrix-result.md) · **Unblocks:** [U27](U27-vector-metrics-aggregates.md)

## Summary

Bind vectors into the expr-lang environment and add the function set: element access and reductions, geometry, `argmax`/`argmin`, scalar maths (`sqrt`, `log`, `exp`, `abs`, `pow`), centering (`vcenter`, `vzscore`, `vnormalize`), and set similarity on `set_*` masks (`jaccard`, `dice`, `hamming`, `overlap`).

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F5 expr-lang vector functions
- [vector-matrix 07 — Similarity & distance](../v1.0.0-vector-matrix/07-similarity-and-distance.md) — S3 centering, S4 set similarity

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#137** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) expr-lang vector bindings, `v[i]` and `len`; `vsum` / `vmean` / `vmin` / `vmax` / `vsd` / `vcount`; `dot` / `norm` / `dist` / `cosine`; `argmax` / `argmin`
- [ ] **#138** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Scalar maths functions `sqrt`, `log`, `exp`, `abs`, `pow`
- [ ] **#139** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Centering functions `vcenter`, `vzscore`, `vnormalize`
- [ ] **#140** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Set similarity `jaccard`, `dice`, `hamming`, `overlap` on `set_*` fields

## Scope

**In scope**
- Vector bindings + functions
- Scalar maths
- Centering
- Set similarity via popcount

**Out of scope**
- Metric registry and `kind` defaults (U27)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(vector-expr-functions/E<n>-S<m>): …`; close each epic with `milestone(vector-expr-functions/E<n>): vertical slice complete — <epic title>`.

### E1 — Formulas speak vectors
- S1: bindings, `v[i]`, `len`, reductions, geometry, argmax/argmin
- S2: scalar maths + centering functions

### E2 — Formulas speak sets
- S1: set similarity over masks incl. `set_u128`/`set_u256`

## Acceptance criteria

- [ ] All functions are NaN-skipping where documented; expression results stay scalar
- [ ] Set similarity on `set_u256` is correct at the 256-member boundary
- [ ] `op-attr-formula.md` lists the new functions, within budget
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Expr function unit tests; `TestSkillTokenBudget` for touched skills

## Update Demand companions

- `op-attr-formula.md`, `op-filter-expression.md`
- `attribute-composition.md`

## Human inputs & decisions

- None.
