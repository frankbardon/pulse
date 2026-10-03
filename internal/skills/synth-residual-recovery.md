---
name: synth-residual-recovery
description: The synth fidelity report's `model_residual_correlations` section — captured vs recovered residual correlation per applied pair, why it is not `pairwise`, its bounded worst-first listing, and why step-target endpoints stay unmeasured.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [synth from-profile, fidelity report, residual_correlations, recovery]
requires: [capability:synth]
---

# Synth residual-correlation recovery

`omitempty` — a report for a profile captured without `--residual-correlations` is byte-identical — and, like every section, scores ONLY relationships generation actually applied (`synth-fidelity-report`).

## Residual-correlation recovery (`model_residual_correlations`)

The `models` question (`synth-model-recovery`) asked BETWEEN fields: per applied residual correlation, captured rho beside recovered. **Deliberately not folded into `pairwise`, and the two section names are the load-bearing distinction** — `pairwise` scores `Spec.Correlations` (value-scale, realized by the copula), this scores `Spec.ResidualCorrelations` (residual-scale, realized by the residual correlator). Different numbers over the same two fields, and `resolveConflicts` excludes a modelled field from `Spec.Correlations` precisely so the two arms fire on DISJOINT field sets.

The recovered side uses the **refit's** residuals, not residuals against the captured coefficients — those fold every coefficient gap `models` already reports into a correlation and state one finding twice. Scale is latent for the reason the coefficients are: generation correlates the `z`s. The model refits produce the residuals as a by-product, so both sections ride one decode of the synthetic partition.

**Bounded because quadratic:** `compared` / `flagged` / `mean_delta` / `max_delta` cover EVERY pair; `pairs` lists only the worst `maxResidualRecoveryPairs` (20) by absolute delta with `omitted` counting the rest, so an unlisted pair recovered at least as well as the last listed. `flagged` fires on `ResidualRecoveryTolerance` (0.10) ALONE — a correlation is unit-free and its SE is under 0.02 over thousands of rows, so a noise-scaled band would collapse onto the floor. Unmeasured pairs ride a separate list carrying `captured_rho` and no recovered slot (`insufficient_overlap` / `no_variance`, plus a recovery-only `no_model_fit`).

**A STAIRCASE endpoint lands in `no_model_fit`, deliberately.** A coefficient can be calibrated because OLS is linear in its response; a correlation cannot — the factor relating two score residuals' correlation to the correlation of the residuals that drove the draws depends on the PAIR's joint distribution rather than either marginal, and no projection removes it. On a survey cohort of staircase targets that leaves `compared` at 1, the rest counted under `no_model_fit` — counted, never silent. **Quote the participant count with the figure**: the total is C(applied models, 2), so the same finding reads as a different fraction on a cohort with a different applied-model count. Closing it needs a polychoric-style bivariate calibration or an ordered-probit refit; `internal/processing/regression` has neither (`binomial`+`probit` is a reserved, unimplemented link).

## See

- `synth-residual-correlations` · `synth-model-recovery` · `synth-correlations`.
