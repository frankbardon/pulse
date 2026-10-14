---
name: op-attr-mahalanobis
description: Per-row Mahalanobis distance D² (or its chi-square p) across a vector of numeric members, from a cohort-wide fit; fit_on fits on a subset and still scores every row.
kind: operator
category: ATTR
operator: ATTR_MAHALANOBIS
type: reference
applies_to: process, compose, predict
examples_tags: [outlier-detection, multivariate, streaming-friendly]
---

Attributes emit row-level scalars; they do not produce `Response.Components`.

<!-- generated: use-when -->

## Params

- `output` — `d2` (default) or `p` (χ² upper tail on p df). `missing` — `listwise` only.
- `fit_on` — `"filtered"` (default) or an inline filterer array picking the FIT rows; every filtered row is scored.
- `fit` — a bound vector fit's name, scored against instead of fitting (XOR `fit_on`: `fit_and_fit_on`); its own weight basis governs. Matched BY LABEL to the members (`labels` — inline `fields` only); mismatch `PULSE_VECTOR_REFERENCE_MISMATCH`, unbound `PULSE_MODEL_UNKNOWN`.
- `weight` — both kinds; fit only (N* = Σw or n_eff, cov M2*/(N* − 1)); a zero-weight row is scored, not fitted.

Slot: exactly one of `vector`<!-- feature: capability:matrices --> (a `vectors` entry)<!-- /feature --> / `fields` (inline, globs ok); no `field`. Default label `ATTR_MAHALANOBIS_<vector>` or `_<first fields entry>`.

## Inputs

Members: `u4`…`u64`, `f32`/`f64` (`packed_bool` via `coerce: binary`); source columns only.

## Output

One `f64` per record: D² = (x − μ)ᵀS⁻¹(x − μ) ≥ 0, or its p. A null member → null.

<!-- generated: reading-the-output -->

## Gotchas

- Fit is cohort-wide, never per group; groupers or features force buffered.
- Singular fit or N* ≤ p: all scores null + `PULSE_MATRIX_SINGULAR` / `PULSE_MATRIX_INSUFFICIENT_N` warning.
- Two-pass, never merged.

## See

- `pulse_examples_search tags=[outlier-detection, multivariate]`
- Skills: `attribute-composition`, `multivariate-design`, `op-attr-zscore`
