# Execution modes — categorical-outcome scores

Per-topic slice of the execution-modes reference. Index (Mode index + section map): `.claude/reference/execution-modes.md` — load it first, then only the slice(s) your engine work touches. The scoring path this extends is `execution-modes/compose.md` (Scoring a bound fit).

## Categorical-outcome scores

A fit that predicts a probability per level scores through `model.ProbabilityScorer` (`internal/model/probability.go`): the full per-row vector over `Outcomes()`, the one kernel the column scorers, a holdout's proper scores and per-level effects all read. A fitted ordinal model rebuilds it from the coefficients + `outputs.link` / `levels` alone, so a reloaded model file scores bit-identically.

- **One slot is one column:** `model.OutcomeScorer` over `ScoreParams.Output` / `Level` (`Category` is declared but refused until a type takes it). The adapter routes the whole reference through `RegressionAdapter.ParamScorer` ahead of `Scorer`.
- **The outcome rule** `checkOutcome` closes `model.CheckScoreFor`, so predict and runtime refuse alike (reasons `outcome_unsupported`, `output_unknown`, `outcome_required`, `outcome_unused`, `outcome_unknown`, `output_scale`). An unknown level is judged against `Signature.Outcomes` when known (the run's set, a loaded model file, a `fits` entry pinning `params.levels`), else only by the run.

**`REG_MULTINOMIAL`.** A native built-in, so its scorer is a `regression.BuiltinScorer` case and the type is in `model.scorableBuiltins` (`TestBuiltinScorer_MatchesScorableType`). `regression.MultinomialFitted` rebuilds the fit from `Result.Coefficients` (`"<category>:<term>"`), `Categories` and `Reference` — what a model file keeps under feature `categories` — and computes each row through `multinomialSoftmax`, the same kernel the engine runs; the reference oracle reads this production scorer.

- Outputs: `probability` (the default, so a bare slot is `outcome_required`) and `class` (a 0-based index into `Categories`). `scale: link` is the category's log-odds against the reference — on a penalised (symmetric) fit, which has no reference, the category's own xᵀβ_k — and needs `category`.
- `model.NamesOwnCoefficients` marks it (beside every extension type): `Bind` NaN-fills the per-predictor slots, `coef()` reads `"<category>:<term>"` only, a model file needs no `(intercept)`, and `CheckScoreFor` refuses `terms` / a `lower` / `upper` `bound`.
- `Signature.Outcomes` are `Result.Categories`, from the bound fit or a loaded file — never from a `fits` entry (the data decide them).
- `model.ScoresWholeVectorByDefault` keeps the params.fit fitted / residual / design slots off it (`type_unsupported`: their default score would need a category).

**Holdout.** A `holdout` on `REG_ORDINAL` / `REG_MULTINOMIAL` runs (predict ≡ runtime). `fitHoldout` (`internal/processing/regression_holdout.go`) branches on `model.OutcomeType` BEFORE building a RowScorer (multinomial's default column needs a category) and scores through `regression.BoundProbabilities` → `regression.HoldoutOutcomeFigures`.

- A row is scored when its target is present, its label (categorical, else `'g'`-formatted as ordinal levels) is among `Outcomes()`, the vector is finite and the weight passes. Weighted means in record order: `log_loss` −ln p_y (p_y = 0 → +Inf → null), `brier` Σ_k (p_k − o_k)², `accuracy` [first argmax = y], ordered `rps` Σ_{k<K} (F_k − O_k)²/(K − 1).
- A held-out outcome the fit lacks is skipped; an undecodable label is the slot's `ENCODING_INVALID`.
- `types.RegressionHoldout`'s figures are all `*float64` `omitempty`, so a numeric fit writes `rmse` / `mae` / `r2` byte-identically and a categorical one only its scores.
- Guidance resolves `holdout.<key>` through `holdoutOutputs` (`interpretation_validate.go`), apart from the plain-fit table (`TestHoldoutOutputs_AreResultTags`). Oracles are the R `predict(type = "probs")` scores on fixed holdout rows (`TestRegOrdinal_HoldoutMatchesR`, `TestReferenceOracle_MultinomialMatchesR`).
