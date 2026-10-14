```yaml
name: op-attr-mahalanobis
description: Per-row Mahalanobis distance D² (or its chi-square p) across a vector of numeric members, from a cohort-wide fit; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_MAHALANOBIS
type: reference
applies_to: process, compose, predict
examples_tags: [outlier-detection, multivariate, streaming-friendly]
```

Attributes emit row-level scalars; they do not produce `Response.Components`.

## Use when

Adds to every row how unusual its mix of several numeric fields is, allowing for how those fields usually vary together.

Questions it answers:

- Which respondents gave an unusual pattern of answers across the whole battery, even if no single answer is extreme?
- Which transactions have an odd combination of amount, frequency and basket size?

Use something else:

- `ATTR_ZSCORE` when only one field matters.

## Params

- `output` — `d2` (default) or `p` (χ² upper tail on p df). `missing` — `listwise` only.
- `fit_on` — `"filtered"` (default) or an inline filterer array picking the FIT rows; every filtered row is scored.
- `fit` — a bound vector fit's name (a compose `fits` entry over one matrix or fitted-attribute battery, or a loaded `populations` entry: [`compose-fits`](compose-fits.md)), scored against instead of fitting (XOR `fit_on`: `fit_and_fit_on`); its own weight basis governs. Matched BY LABEL to the members (`labels` — inline `fields` only); mismatch `PULSE_VECTOR_REFERENCE_MISMATCH`, unbound `PULSE_MODEL_UNKNOWN`, a regression's name `PULSE_MODEL_KIND_MISMATCH`.
- `weight` — both kinds; fit only (N* = Σw or n_eff, cov M2*/(N* − 1)); a zero-weight row is scored, not fitted.

Slot: exactly one of `vector` (a `vectors` entry) / `fields` (inline, globs ok); no `field`. Default label `ATTR_MAHALANOBIS_<vector>` or `_<first fields entry>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64` (`packed_bool` via `coerce: binary`); source columns only.

## Output

One `f64` per record: D² = (x − μ)ᵀS⁻¹(x − μ) ≥ 0, or its p. A null member → null.

## Reading the output

- `value`: With output d2 (the default): the row's squared Mahalanobis distance D² = (x - mean)' S^-1 (x - mean) across the p fields, the distance from the fitted average row in units of the fields' own spread and correlation; 0 is exactly average and it is never negative. With output p: the chance of a D² at least this large under a chi-square curve on p degrees of freedom, so a small value marks an unusual row.

## Gotchas

- Fit is cohort-wide, never per group; groupers or features force buffered.
- Singular fit or N* ≤ p: all scores null + `PULSE_MATRIX_SINGULAR` / `PULSE_MATRIX_INSUFFICIENT_N` warning.
- Two-pass, never merged.

## See

- `pulse_examples_search tags=[outlier-detection, multivariate]`
- Skills: [`attribute-composition`](attribute-composition.md), [`multivariate-design`](multivariate-design.md), [`op-attr-zscore`](op-attr-zscore.md)
