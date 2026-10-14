---
name: op-overlay-std-residual
kind: operator
category: OVERLAY
operator: OVERLAY_STD_RESIDUAL
description: Per-cell Haberman adjusted standardized residual of the host crosstab, with expected counts and two-sided p-values as parts.
type: reference
applies_to: process
examples_tags: [overlay, cross-tabulation, hypothesis-test]
---

Overlays decorate the host; no `Response.Components`.

<!-- generated: use-when -->

## Params

`Scope` must be `cell`. `Ref` empty (implicit-margin). `Level`/`Within` `0`.

<!-- feature: capability:multiplicity -->
- `multiplicity` — optional; adjusts `parts.p` into `payload.p_adjusted` / `significant_adjusted`; raw p untouched.
<!-- /feature -->

## Host shape

MATRIX crosstab.

## Output

MATRIX residual `(O − E) / sqrt(E (1 − r/N) (1 − c/N))`, `E = r c / N` (R `chisq.test()$stdres`). Parts `expected` and `p` (two-sided normal), MATRIX on the same keys. `Summary`: `Baseline` 0, `Min`/`Max`/`Count` of defined residuals.

<!-- generated: reading-the-output -->

## Gotchas

- Weights as `op-overlay-chisq-matrix` (Kish-scaled under probability).
- Absent or undefined (NaN) cell = 0 (missing). `E = 0` ⇒ residual, p `null`. Any `E < 5` ⇒ one `PULSE_OVERLAY_EXPECTED_LOW`.
- Host < 2×2, zero total or a `Ref` ⇒ `PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`.
<!-- feature: AGG_MODE -->
- Code cell (`AGG_MODE`) ⇒ `PROCESSING_CONFIG` `categorical_code`.
<!-- /feature -->

## See

- Skills: `overlay-system`, `op-overlay-chisq-matrix`, `op-overlay-fisher-exact-cell`.
