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

| Name | Type | Default | Description |
|---|---|---|---|
| `output` | enum | `d2` | `d2` or `p` (χ² upper tail on p df). |
| `fit_on` | any | `filtered` | `"filtered"` or a filterer array picking the FIT rows. |
| `fit` | string | — | A bound vector fit, matched by label; XOR `fit_on` (`vector-row-scores`). |
| `labels` | list | — | Inline `fields`: one label per member. |
| `missing` | enum | `listwise` | `listwise` only. |

`weight` — both kinds; fit only (`vector-row-scores`).

Slot: `vector` or `fields`, no `field` (`vector-row-scores`). Default label `ATTR_MAHALANOBIS_<vector>` or `_<first fields entry>`.

## Inputs

Members: see `## Contract` (`packed_bool` via `coerce: binary`); source columns only.

## Output

One `f64` per record: D² = (x − μ)ᵀS⁻¹(x − μ) ≥ 0, or its p. A null member → null.

<!-- generated: reading-the-output -->

<!-- generated: contract -->

## Gotchas

- Fit is cohort-wide, never per group: `fit_on` is two-pass (buffered beside groupers / features); `fit` is row-local and merges; a failed fit nulls every score + `PULSE_FIT_UNAVAILABLE` (`vector-row-scores`).
- Singular fit or N* ≤ p: all scores null + `PULSE_MATRIX_SINGULAR` / `PULSE_MATRIX_INSUFFICIENT_N` warning.

## See

- `pulse_examples_search tags=[outlier-detection, multivariate]`
- Skills: `vector-row-scores`, `attribute-composition`, `multivariate-design`, `op-attr-zscore`
