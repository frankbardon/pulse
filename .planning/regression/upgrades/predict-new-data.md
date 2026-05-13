# predict-new-data — score a saved fit on new rows

## Motivation
Today every Pulse request is self-contained: regression fits live and
die inside a single `Process` call. Real ML workflows train once and
score many times — fit a model on history, score it on this morning's
new arrivals, score it again tomorrow. Currently blocked: any pipeline
that wants a stable production model has to re-fit on each scoring
call (expensive, statistically inconsistent across runs) or extract
coefficients out-of-band and reimplement scoring outside Pulse.

## Proposed operator / spec shape
Two viable designs, differing in fit-persistence strategy:

**Route A — in-request handoff:**
```go
// Already exists implicitly: pulse_compose runs a chain of requests
// and threads results between them. Extend the threading so a
// regression result can be referenced by a downstream score operator.

const REG_SCORE RegressionType = "REG_SCORE"

// New RegressionSpec branch:
FitRef string // composed-request reference to an upstream regression
              // result, e.g. "step1.regressions.my_logit"
```

**Route B — new `.fit` artifact format:**
Persist a fitted model to disk as a new artifact alongside `.pulse`:
```
fit_v1 magic + serialized RegressionSpec + Coefficients + scaling
constants + reference-encoding dictionary metadata
```
A new CLI leaf `pulse score --fit model.fit --data new.pulse` reads
both and emits scores.

Route A is the lower-impact path — it stays inside the engine and
needs no file-format change. Route B is the better long-term story
but is a substantially larger commitment (file format, version
compat, key management).

Recommend Route A in v1, Route B as a separate later upgrade.

## Algorithm sketch
Route A:
1. Compose step 1 fits the regression and emits `RegressionResult`.
2. Compose step 2's `REG_SCORE` spec references step 1's result by
   name; the compose engine threads the coefficient vector + schema
   alignment data.
3. Step 2 emits per-row predictions as an attribute, not as a new
   regression result (because nothing was fit in step 2).

Route B: same logic but with `.fit` deserialization at the boundary.

## Streamability
Streaming — applying a fixed coefficient vector is `O(p)` per row,
no aggregation needed. Faster than the original fit.

## Error codes
- `PROCESSING_REGRESSION_SCORE_REF_UNKNOWN` — `FitRef` not resolvable.
- `PROCESSING_REGRESSION_SCORE_SCHEMA_MISMATCH` — new data's schema
  doesn't match the fit's expected predictors (missing field,
  type mismatch, dictionary mismatch on categorical).
- `PROCESSING_REGRESSION_SCORE_INCOMPLETE_FIT` — referenced regression
  didn't converge or has zero coefficients.

## Update Demand impact
- Route A: `REG_SCORE` is a new regression type → registered
  regression operator row fires. `skills/regression-modeling.md`
  scoring section.
- Route B: large — new file format invariant in CLAUDE.md "Byte-layout
  invariants" section + format-version bump on the new artifact + new
  CLI leaf `pulse score`.
- New error codes → fixup metadata + manifest.
- The pulse_compose MCP tool likely needs a small schema extension to
  describe inter-step references; check `internal/mcp/mcptools/meta.go`.

## Dependency cost
Route A: none. Route B: none beyond the new artifact format itself.

## Estimated phase count
- Route A: 2 — compose-reference plumbing + scoring operator + skill.
- Route B: 3+ — file format design, persistence, deserialization,
  schema-alignment, CLI leaf. Higher risk of design rework.

## Open questions
- Does Route A's "score is an attribute, not a regression" framing
  introduce confusion? Alternative: emit a degenerate `RegressionResult`
  whose `Coefficients` echo the parent and whose per-row outputs go to
  a paired attribute.
- For Route B: how do we handle reference-encoded categorical
  predictors when the new data's dictionary differs? Either reject
  with a clear error or build a dictionary-remapping pass.
- Should the scoring operator support standard errors / prediction
  intervals? In v1, no — point predictions only. Document and defer.
- Forward-compat: if we ship Route A first, can we extend to Route B
  later without breaking Route A users? Yes — they're independent
  surfaces. Important to confirm before committing.
