```yaml
name: synth-model-selection
description: How synth `--fit-models` chooses predictors — the top-K collapse, the variance-explained floor, marginal scoring and refit-on-rank-deficiency, main effects only, zero-predictor models — and how thin levels are shrunk by ridge rather than dropped.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [profile create, models, predictor selection, shrinkage, top-k]
requires: [capability:synth]
```

# Synth model predictor selection

## Predictor selection — automatic, absolute, no knob

Two mechanisms, this order (`internal/synth/profile_models_select.go`).

**(1) Top-K collapse — structural.** A candidate categorical contributes one column per top-`--top-k` level plus ONE catch-all carried as an ordinary level named `"other"` (`otherCategoryLabel`) — the same collapse `Categorical.Top` and `categorical_pairs` apply, so collapsed buckets mean one thing throughout a document. This is what makes the flag WORK, not a refinement: unbounded, a wide survey cohort blows past `maxModelColumns`, every target is skipped, and `--fit-models` produces NOTHING on real data. The catch-all is never the reference level.

**(2) Variance-explained floor — statistical**, over the collapsed set. A candidate FIELD enters iff the **adjusted** share of the target's total sum of squares between the candidate's groups reaches `synth.minVarianceExplained` (**0.01**, Cohen's small-effect boundary). Package constant, not a `ProfileOptions` field (`TestModelSelection_NoUserFacingOverride` — no capture flag but `--top-k` moves an admission). **NOT significance** — at large n everything is significant, so a p-value gate is "all predictors enter" in disguise, silently, since every coefficient it admits is real and merely negligible. An absolute floor is n-invariant by construction (`TestModelSelection_AdmissionInvariantToRowCount`). Adjusted, not raw, η², so a wide collapsed field is not admitted for its width alone.

**Scoring is MARGINAL** — one candidate at a time against the RAW target, never against another's residual — so two collinear candidates (a banding beside the exact value; a region nested in a DMA) both clear and both enter; incremental scoring would make the admitted set depend on candidate order. Redundancy is the **solver's** job: a design refused as rank-deficient is refitted with its weakest-scoring admitted predictor dropped, up to `maxModelRefitRounds` (4) times, so the survivor of a nesting is the one explaining more. A functional-dependency probe is NOT attempted — the dependency that bites is LINEAR, not functional.

Selection also prunes columns degenerate ON THE ROWS THE FIT ADMITS: selection scores pairwise, the fit deletes listwise, so a level with pairwise support but no listwise rows is an identically-zero column the solver refuses. A single-level candidate drops by the same arithmetic, not a special case.

**MAIN EFFECTS only — no interactions.** One coefficient per (field, level), summed; cannot express "brand X matters only in region Y". A real limit: one pairwise interaction between two collapsed 33-column fields is 1,089 columns, and the scoring rule, the wire format and the recovery refit all assume one term per (field, level).

**A target no candidate clears is NOT skipped** — it gets a **zero-predictor model** (own mean + spread, `r2: 0`), warned `carries no predictors: …`, never `skipped: …`: a complete model and a failed capture mean opposite things. `maxModelColumns` (256) is a backstop against a pathological schema, not a working limit (the accumulator is O(p²) per row).

## Thin-level shrinkage

Selection picks FIELDS; this decides how far to trust their individual LEVELS. Top-K bounds RANK, not SUPPORT; listwise deletion cuts again; a coefficient from 4 rows is noise generation reproduces as a confident invented offset.

Trigger: a design column whose **listwise** support (rows the fit ADMITS, not cohort frequency) falls below `internal/synth.minLevelObservations` (**50**) ⇒ the whole fit switches to the engine's ridge, `Penalty: "l2"`, `alpha = 50 / n_obs` — never a hand-rolled penalty. Its own constant, NOT `MinPairObservations` (30): that asks "enough co-occurrences for a correlation to mean anything", this asks "enough rows for a free coefficient to describe the level rather than the sample".

`alpha` is a pseudo-count — the engine adds `n·alpha` to each Gram diagonal, so a level keeps `n_j/(n_j + 50)` of its free coefficient (3 rows keep 6%, 50 keep half, 500 keep 91%). **It SCALES with thinness rather than switching on at the threshold**, which is why one global alpha suffices. Shrinkage is toward the field's **reference level** (what a dummy coefficient measures); a thin level collapsing onto the baseline is the conservative reading. Never refused, never dropped. `shrinkage_alpha` rides the wire because a shrunk and a free coefficient are different kinds of number and nothing else says which you hold; `alpha × n_obs` recovers the pseudo-count.

Three invisible consequences, none fixable by a different alpha:

1. Well-supported columns move too, by ≈`50/n_j` — bounded, small, not zero.
2. GATED on a thin column being present ⇒ clean designs stay exact OLS, at the cost of a bounded discontinuity at the boundary.
3. A penalized Gram is positive-definite ⇒ the rank-deficiency refusal the refit uses as its redundancy signal does NOT fire; collinear predictors are jointly shrunk instead of one being dropped.

Warnings reuse `thinSupportWarning`, doubly bounded: aggregated by (field, level) across targets (thinness is a LEVEL property, so per-(target, level) emission restates one finding once per model), reporting the thinnest support and the model count, then `maxThinLevelWarnings` (20) thinnest-first plus a counted summary. Lower `--top-k` to fold rare levels into `"other"`.

## See

- [`synth-models`](synth-models.md) — capture and wire shape.
- [`synth-model-draw`](synth-model-draw.md) — how the `"other"` catch-all fires at generation.
