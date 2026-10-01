# Statistical integrity — v1.0.0

**Status:** proposal · **Target:** v1.0.0 (both parts committed)

Two gaps can lead to **wrong conclusions without any error** on the data Pulse's core audience uses most:

| Part | Gap today | Consequence |
|---|---|---|
| [01 — Weighting](01-weighting.md) | Only `AGG_WEIGHTED_MEAN` and the pairwise overlays accept a weight. Counts, frequencies, shares, percentiles, crosstab cells, tests and significance overlays all treat every row as 1. | On weighted survey data, unweighted percentages and significance tests are simply wrong. The current workaround (`AGG_SUM` of the weight field as a cell) gives weighted counts but nothing else. |
| [02 — Multiple comparisons](02-multiple-comparisons.md) | No Bonferroni, Holm or Benjamini–Hochberg anywhere. | A crosstab with pairwise significance overlays can run hundreds of tests. Roughly 5% come out "significant" by chance, and nothing tells the reader. |

Both are additive request surfaces (`format_version` stays `"1.1"`), and both should land **before** the vector & matrix operators. That way every new `MAT_*`, multivariate test and matrix overlay is born weight-aware and multiplicity-aware, instead of each one inventing its own.

## Relationship to other themes

- The vector & matrix theme already promised weight support on every `MAT_*` (05, X1) and a correlation-only p-value adjustment overlay. Weighting now **consumes** the shared machinery defined here, and the correlation overlay is **dropped**: matrix p-values are corrected through `MatrixSpec.multiplicity`. Correction is opt-in everywhere (shipped default `none`).
- The guided-analysis advisories `PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED` and `PULSE_ADVISORY_MANY_TESTS` become actionable: each fixup points at the request field defined here.
- Feature profiles: `weighting` and `multiplicity` are capabilities and appear in profile files like any other feature.
