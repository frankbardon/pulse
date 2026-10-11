---
id: U26
slug: vector-expr-functions
title: "Formulas and filters can work with whole vectors and sets"
track: Vector & matrix
size: M
status: done
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

Bind vectors into the expr-lang environment and add the function set: element access and reductions, geometry, `argmax`/`argmin`, scalar maths (`sqrt`, `log`, `exp`, `abs`, `pow`), centering (`vcenter`, `vzscore`, `vnormalize`), and set similarity on `set_*` label sets (`jaccard`, `dice`, `hamming`, `overlap`).

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F5 expr-lang vector functions
- [vector-matrix 07 — Similarity & distance](../v1.0.0-vector-matrix/07-similarity-and-distance.md) — S3 centering, S4 set similarity

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#137** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) expr-lang vector bindings, `v[i]` and `len`; `vsum` / `vmean` / `vmin` / `vmax` / `vsd` / `vcount`; `dot` / `norm` / `dist` / `cosine`; `argmax` / `argmin`
- [x] **#138** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Scalar maths functions `sqrt`, `log`, `exp`, `abs`, `pow`
- [x] **#139** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Centering functions `vcenter`, `vzscore`, `vnormalize`
- [x] **#140** (10. Vector & matrix — operators › E5 — Row-wise vector vocabulary & similarity) Set similarity `jaccard`, `dice`, `hamming`, `overlap` on `set_*` fields

## Scope

**In scope**
- Vector bindings + functions
- Scalar maths
- Centering
- Set similarity over label sets (popcount only for dictionary-less sets)
- Named-vector binding (`Request.Vectors` names readable in expressions) and its projection

**Out of scope**
- Metric registry and `kind` defaults (U27)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(vector-expr-functions/E<n>-S<m>): …`; close each epic with `milestone(vector-expr-functions/E<n>): vertical slice complete — <epic title>`.

As built (the plan's two epics became three once named-vector binding joined the scope):

- E1 — Formulas do maths on vectors: `internal/vecmath` kernels; the reserved built-ins (`internal/exprbuiltins`) registered once for the formula attribute, the expression filter, `total()` and the overlay formula; the overlay scalar subset; skills
- E2 — Formulas and filters read named vectors: a `Request.Vectors` name binds as a `[]float64` (so `v[i]` and `len` work natively), projected decode keeps its members, predict counts expression references and refuses name collisions
- E3 — Formulas compare multi-selects: `jaccard`, `dice`, `hamming`, `overlap`; roadmap close-out

## Acceptance criteria

- [x] All functions are NaN-skipping where documented; expression results stay scalar
- [x] Set similarity on `set_u256` is correct at the 256-member boundary
- [x] `op-attr-formula.md` lists the new functions, within budget (it points to `expression-language`, which holds the one function table)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- Expr function unit tests; `TestSkillTokenBudget` for touched skills

## Update Demand companions

- `op-attr-formula.md`, `op-filter-expression.md`
- `attribute-composition.md`

## Handed on

Filed as TODO items (ids are TODO ids, not GitHub issues).

| Found in | What is still open | TODO | Owner |
|---|---|---|---|
| E3 | Reference vectors from a `LookupTable` for expression and profile similarity (compare a row against a stored profile) | #417 | [U27](U27-vector-metrics-aggregates.md) |
| E2-S2 | Overlay-formula predict/runtime callee parity: a request-host unknown callee is `PULSE_OVERLAY_FORMULA_INVALID_IDENT` on predict but `PARSE_ERROR` at runtime; compose-host predict checks no unknown callee beyond the vector built-ins | #418 | [U35](U35-predict-runtime-parity.md) |
| E2-S2 | Static (predict-time) `PROCESSING_VECTOR_LENGTH_MISMATCH` for `dot` / `dist` / `cosine` over named vectors of known, differing lengths | #419 | [U35](U35-predict-runtime-parity.md) |

## Human inputs & decisions

Owner decisions that changed the planned scope:

- **Named-vector binding + projection.** A `Request.Vectors` name is readable in a formula or filter as a `[]float64`; projected decode, two-pass and filter precompute keep its member fields. Not in the original plan.
- **Set similarity is label-based, not mask popcount.** Dictionary-bearing set fields bind as label lists, so two fields with different dictionaries compare by label. Only dictionary-less sets (raw masks) compare by popcount, and only with each other; a mask beside a label set is `PROCESSING_RUNTIME`. A null set gives NaN for all four (`hamming` too); an empty set is a set.
- **A `nil` formula result is a null value**, no longer `PROCESSING_RUNTIME` ("unsupported type <nil>").
- **Reserved names.** Every new built-in is reserved: an embedder `ExprFunction` using one is `PULSE_EXTENSION_NAME_RESERVED` (embedder migration row in `03-embedder-migration.md`). The older set helpers, `coef` and `lookup` keep their shadowing semantics.
- **Overlay scalar subset.** An overlay formula admits only `sqrt`, `log`, `exp`, `pow`; the vector and set-similarity built-ins are `PULSE_OVERLAY_FORMULA_INVALID_IDENT` on predict and runtime.
- **Collision rule includes labels.** `PULSE_VECTOR_NAME_COLLISION` refuses a vector name equal to a schema field or to a feature, attribute or window label, unconditionally.
