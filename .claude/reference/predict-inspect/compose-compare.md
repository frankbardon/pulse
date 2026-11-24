# Predict / Inspect — nested-model comparisons (Compose `compare`)

Per-topic slice of the predict-inspect reference (Compose `compare` at predict). Index: `.claude/reference/predict-inspect.md` — load it first, then only the slice(s) your change touches.

## Nested-model comparisons

Right after `OptimizeRefusal`, predict runs `regcompare.Check` (`internal/regcompare`) — the ONE rule the runtime's Compose calls at the same point, with the instance default weight (for the probability-weight refusal).

- **What it judges:** names; both sides regression fits of one comparable type (`REG_OLS`, `REG_GLM`, `REG_MULTINOMIAL` with equal `reference`, or a type whose declaration carries `Compare` — `descx.ComparableRegressions`, both sides off the same instance view); restricted terms nested in the full fit's; the fit requests equal but for predictors / intercept / reporting knobs; the test against family and weight. A refusal is `PULSE_COMPARISON_INVALID`, returned at once.
- **Summary:** on success `ComposeValidationResult.Compare` (`descriptor.ComparisonSummary {name, restricted, full, test, distribution, terms}`) — the resolved test and distribution the run reports. Nothing is fitted.
- **Run-only checks** fail the entry alone, never the batch: unequal rows (`PULSE_COMPARISON_ROWS_DIFFER`); for SEM pairs `df_not_positive` / `conventions_differ`.
- **Sweep-bound sides** (`from_sweep`): judged against every sweep slot that could win (`regcompare.CheckSweep`, the same expansion both sides use). All refusing is the refusal; otherwise `unresolved {slots, refused, varies}` lists what the winner decides, and the runtime re-judges the winner (a refusal fails the entry alone).
- **Vector fits** compare member-wise. **SEM fits** are judged on their requests alone (`kind_mismatch`, `test_unsupported`, `request_mismatch`, `same_model`, `observed_differ` — each model parsed, no record read) and summarised `lrt` / `chisq` with empty `terms`.
- Parity gates: `TestCompare_PredictMatchesRuntime` (and its ordinal / multinomial / extension / sweep / vector / SEM siblings, `TestCompare*_PredictMatchesRuntime`).
