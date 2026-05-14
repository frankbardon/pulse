# online-glm — streaming stochastic-gradient GLM

## Motivation
`REG_GLM` is buffered: IRLS reads the full dataset multiple times. For
datasets that don't fit in working memory — `pulse_import` ingests of
hundreds of millions of rows, or continuous re-fitting on append-only
event streams — a buffered fit is the wrong shape. A stochastic-gradient
GLM that updates β online would let Pulse fit logistic / Poisson
models with `O(p)` memory and a single pass. Currently blocked: any
large-cardinality use case that already streams through Pulse for
aggregation has to truncate or sample before doing classification.

## Proposed operator / spec shape
Architectural question: is this a `Mode` on `REG_GLM` or a new operator
family?

**Route A — Mode on REG_GLM:**
```go
// Addition to RegressionSpec:
Solver string // "irls" (default) | "sgd" | "adam"
LearningRate float64
EarlyStop    bool
```
Pros: same operator, users opt in via a flag. Cons: streamability
becomes spec-dependent — `Streamable()` now checks Solver, and the
manifest streamability table grows a footnote.

**Route B — new operator family:**
```go
const SGD_GLM RegressionType = "SGD_GLM"
// or even a new top-level operator slot SGDSpec separate from RegressionSpec
```
Pros: keeps the buffered/streaming distinction at the type level (one
line in `Streamable()`). Cons: duplicates much of the GLM contract.

Recommend Route A with explicit per-spec downgrade in `Streamable()`.

## Algorithm sketch
- **SGD with momentum** or **Adam** over the negative log-likelihood
  of the chosen family. Mini-batch is awkward in a streaming context
  (would need re-shuffling); per-row updates are the natural fit.
- **Early stopping:** hold out a tail or a reservoir-sampled subset
  for validation; stop when validation loss stops improving.
- **Step-size schedules:** constant for diagnostic purposes,
  `1/sqrt(t)` decay for convergence guarantees.

Single-pass with one validation pass for diagnostics — effectively
1.5× streaming.

## Streamability
Streaming with `O(p)` memory (β vector + Adam state). Spec-conditional:
`Solver: "sgd"` or `"adam"` streams; default `"irls"` stays buffered.

## Error codes
- `PROCESSING_REGRESSION_SGD_DIVERGED` — loss exploded (learning rate
  too high or scale mismatch).
- `PROCESSING_REGRESSION_INVALID_SOLVER` — unknown `Solver` value.

## Update Demand impact
- Possibly no new entry in `types.AllRegressionTypes()` (Route A) — but
  `types/streamability.go` gains spec-conditional logic that the
  existing `TestRegressionStreamabilityMatchesTypes` will need to
  cover for every Solver.
- `RegressionResult` reuses existing fields. Note that p-values /
  standard errors from SGD are not directly comparable to IRLS — a
  doc warning is needed.
- New error codes → fixup metadata + manifest.

## Dependency cost
None new.

## Estimated phase count
2 — engine + Solver dispatch, validation-loop / standard-error
treatment + skill section.

## Open questions
- Standard errors for SGD fits: bootstrap-on-subsample? Sandwich
  estimator from the gradient's empirical covariance? Recommend
  bootstrap.
- Does `Resample` make sense paired with SGD? Bootstrap-SGD is
  conceptually fine but multiplies the cost by `BootstrapIters` —
  worth documenting but not blocking.
- Mini-batching vs. per-row updates: per-row is simpler for true
  streaming; defer mini-batches to a follow-up if SGD lands.
- Does Pulse acquire the "online learning" framing this implies (as
  in: should we also offer online OLS via the same path)? Probably
  yes, but defer past v1.
