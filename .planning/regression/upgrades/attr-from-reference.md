# attr-from-reference — Option B: ATTR_REG_* by-name reference

## Motivation
Phase 7 locked Option A: every `ATTR_REG_FITTED` / `ATTR_REG_RESIDUAL` /
`ATTR_REG_LEVERAGE` attribute carries its own `RegressionSpec`-shaped
slice and refits internally during the attribute pass. When a request
both reports a regression (in the `Regressions` slot) and emits per-
row residuals from the same fit, the work is done twice — full IRLS
or full Gram-solve for the regression slot, then again for each
attribute. For large datasets this doubles or triples request latency
needlessly. Currently blocked: nothing is functionally blocked, but
the pattern is the most-requested ergonomic improvement to come out
of Phase 7 internal testing.

## Proposed operator / spec shape
Add an optional `From` field on `AttributeSpec` for the regression
attribute family:

```go
// AttributeSpec (regression branch):
From string `json:"from,omitempty"` // name of a RegressionSpec in the
                                    // same Request.Regressions slot
```

When `From` is set, the attribute resolves the named regression's fit
(coefficients + cached design matrix info) and emits per-row outputs
without refitting. When `From` is empty, the existing Option-A path
(self-contained refit) is preserved — fully backward compatible.

Validation rule: setting both `From` and any of the attribute's own
regression-shaped fields (`Target`, `Predictors`, `Family`, etc.) is
an error.

## Algorithm sketch
1. During request validation, build a name → RegressionSpec map from
   `Request.Regressions`.
2. For each `ATTR_REG_*` with `From != ""`, look up the spec and
   verify it exists.
3. During execution, the regression slot runs first (already does);
   the engine stashes the fit (coefficients, design-matrix metadata,
   final IRLS weights for GLM leverage) keyed by spec `Name`.
4. The attribute pass reads the stash, applies the fit row-by-row.

The cross-slot stash is internal — it does not extend the wire shape.

## Streamability
Mixed:
- If the referenced regression streamed and the attribute emits ŷ /
  residuals, the attribute can also stream once the regression
  finalizes — same shape as today's `ATTR_REG_FITTED`.
- If the referenced regression was buffered (GLM, regularized OLS,
  resampled), the attribute is buffered.

So `From`-bearing attributes inherit the parent's streamability.

## Error codes
- `PROCESSING_REGRESSION_ATTR_REF_UNKNOWN` — `From` names a regression
  not in `Request.Regressions`.
- `PROCESSING_REGRESSION_ATTR_REF_CONFLICT` — both `From` and per-attr
  regression fields populated.
- `PROCESSING_REGRESSION_ATTR_REF_INCOMPATIBLE` — e.g.,
  `ATTR_REG_LEVERAGE` referencing a GLM before
  `glm-leverage.md` lands.

## Update Demand impact
- Not a new operator. `skills/attribute-composition.md` and
  `skills/regression-modeling.md` need an Option-B section.
- New error codes → fixup metadata + manifest.
- `descriptor/capabilities_regressions.go` (attribute side) extends
  with the new field.
- `RegressionResult` shape unchanged. Wire-compatible.

## Dependency cost
None.

## Estimated phase count
1 — single focused phase. Naturally pairs with `glm-leverage.md`.

## Open questions
- Stash lifetime: per-request only, never persisted? Yes — keeping
  the stash request-local avoids interaction with the `.pulse` file
  format. Persistence is what `predict-new-data.md` is for.
- Does this enable spec-aware optimizations beyond just skipping the
  refit (e.g., reuse the Cholesky factor)? Probably yes; flag for
  implementation review.
- Should the by-reference attribute be allowed to override anything?
  Recommend strictly no — Option B is "use parent's fit verbatim."
  Override semantics open a too-large design surface.
