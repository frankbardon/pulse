# kfold-cv — Resample: "kfold" value

## Motivation
Phase 5 shipped `Resample ∈ {"", "jackknife", "bootstrap"}`. Jackknife
is leave-one-out (n folds of size 1); bootstrap is sampling with
replacement. K-fold cross-validation — the standard model-evaluation
protocol in nearly every applied-ML workflow — is conspicuously absent.
Currently blocked: anyone who wants to report cross-validated R² or
deviance has to either roll their own outside Pulse or settle for
jackknife (overkill for performance, under-powered for variance
estimation).

## Proposed operator / spec shape
```go
// In RegressionSpec; reuses the existing Resample field.
Resample = "kfold"

// New fields gated by Resample == "kfold":
Folds     int    // default 5
Stratify  string // optional field name; stratify folds by this column
RNGSeed   int64  // already exists; reused for fold assignment
```

`RegressionResult` already populates jackknife / bootstrap standard
errors. For k-fold, the natural emissions are:

- `CVMSE`, `CVR2`, `CVDeviance` per-fold means and SDs.
- Optional `FoldFits []RegressionResult` for debugging — guarded by a
  request flag to keep responses small.

Adding `CV*` fields is an additive contract extension.

## Algorithm sketch
1. Shuffle row indices using `RNGSeed` (if `Stratify` set, shuffle
   within each stratum).
2. Partition into `Folds` roughly-equal chunks.
3. For each fold: fit on the complement, score on the fold.
4. Aggregate fold-level metrics; report mean + SD.

Implementation reuses the existing `resample.go` plumbing — same
buffered orchestration, different index permutation. Cheaper than
bootstrap because no replacement and fewer refits (5 vs. typically
1000).

## Streamability
Buffered. K refits each touching `~n` rows.

## Error codes
- `PROCESSING_REGRESSION_KFOLD_TOO_FEW_FOLDS` — `Folds < 2`.
- `PROCESSING_REGRESSION_KFOLD_TOO_FEW_ROWS` — `n / Folds < p + 1`
  (training partition under-determined).
- `PROCESSING_REGRESSION_KFOLD_INVALID_STRATIFY` — `Stratify` field is
  numeric or absent from schema.

## Update Demand impact
- "regression modifier" row fires — `skills/regression-modeling.md`
  gains a k-fold section.
- New error codes → fixup metadata + manifest.
- `descriptor/capabilities_regressions.go` modifier metadata extends.
- The `TestCanStreamRequest_RegressionMatrix` matrix gains
  `Resample:"kfold"` rows.

## Dependency cost
None — entirely on top of existing infrastructure.

## Estimated phase count
1 — single focused phase. The smallest of the modifier-shaped items.

## Open questions
- Default `Folds`: 5 or 10? 5 is faster, 10 is the textbook default.
  Recommend 5; cheap to bump later.
- Stratification semantics for continuous targets — quantile-bin then
  stratify? Recommend disallowing `Stratify` on continuous Target in
  v1 (error code above) and revisiting if users ask.
- `Stratify` field default: empty (no stratification) or auto-stratify
  on categorical targets? Recommend explicit only.
- Repeated k-fold (run the whole protocol R times with different
  seeds) — defer; cheap to add later as a `Repeats` field.
