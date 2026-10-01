# Vector & Matrix Math — v1.0.0 Overview

**Status:** proposal · **Target:** v1.0.0 · **Scope tier:** broad

## Why this theme

Pulse already answers "what is the distribution of X, broken down by Y" extremely well — aggregators, groupers, crosstabs, overlays, tests and regressions. What it cannot answer today is any question about **many columns at once**:

- *Which of these 20 rating items move together?* (correlation matrix)
- *Do these 8 items form one reliable scale?* (Cronbach's α)
- *What are the 3 underlying dimensions behind 25 attitude statements?* (PCA / factor analysis)
- *Which respondents answered in a statistically unusual pattern?* (Mahalanobis distance)
- *Where do brands sit relative to each other on a perceptual map?* (correspondence analysis of a brand × attribute crosstab)
- *If 12% of customers churn from tier A to B each month, where will the base be in 6 months?* (transition matrix power / steady state)
- *Do two segments differ on a whole profile of measures, not just one?* (Hotelling's T², MANOVA)

All of those reduce to the same small set of primitives: a **vector** (an ordered set of numeric values per row), a **co-moment matrix** accumulated over rows, and a handful of **decompositions** (Cholesky, eigen, SVD, inverse). Pulse has these primitives only in private corners today:

| Where | What | Limitation |
|---|---|---|
| `processing/regression/` | gonum `SymDense` + Cholesky solve, IRLS, centered Gram accumulator | private to regression; coefficient covariance never surfaced |
| `synth/copula.go` | hand-rolled Cholesky on `[][]float64` | duplicate implementation; not reusable |
| `TEST_PEARSON_R` | streaming two-variable cross-product | pairwise only; N columns = N² requests |
| Crosstab `MatrixPayload` | a labeled 2-D grid | an aggregation table, no linear-algebra ops |

The v1.0.0 theme lifts these into a first-class, self-describing surface that obeys every existing Pulse rule: no-execute predict, manifest-declared capabilities, atomic skills, typed coded errors, mergeable streaming where mathematically possible, and overlays that never mutate the base.

## Decisions captured in the planning interview

| Question | Decision |
|---|---|
| Primary use cases | **Multivariate statistics** and **matrix operations on results**. Not AI/embedding features. ML-adjacent features admitted only when they have a classical-statistics justification (see below). |
| Audience | All four: survey / market research, business / ops analytics, scientific / general stats, and harness/LLM-driven analysis via MCP. |
| Vector representation | **Both** — *virtual vectors* (named column lists, zero format change) first, plus a *native fixed-dimension vector field type*. |
| Matrix outputs | A **new typed matrix result slot** (`Response.Matrices`), additive, `format_version` stays `"1.1"`. |
| Ops over results | **New `OVERLAY_*` kinds** — read-only decorations on crosstab and matrix hosts. |
| Scope | **Broad**, tiered into *committed*, *stretch* and *post-1.0*. |
| Document location | `docs/roadmap/` in the repo. |

## Documents in this set

| # | Document | Covers |
|---|---|---|
| 00 | this file | vision, decisions, feature map, "why add these" |
| 01 | [Foundation](01-foundation.md) | `linalg/` core, co-moment accumulator, virtual vectors, native `vec_*` field type, expr-lang vector functions |
| 02 | [Matrix result shape](02-matrix-result.md) | `Response.Matrices`, `MatrixResult`, components, payload schema, predict/manifest, LLM-compact output |
| 03 | [Multivariate statistics](03-multivariate-statistics.md) | `MAT_*` operators, multivariate tests, row-level attributes, vector aggregators, segmentation grouper, rolling windows, regression additions |
| 04 | [Matrix ops on results](04-matrix-ops-on-results.md) | new overlay kinds on crosstab / matrix / Compose hosts: correspondence analysis, Markov, raking, similarity, matrix formula |
| 05 | [Cross-cutting concerns](05-cross-cutting.md) | weighting, missing data, numerical stability, determinism, execution modes, errors, extensions, MCP, Update Demand impact |
| 06 | [Phasing & open questions](06-phasing.md) | epics, ordering, dependency graph, risks, decisions still needed |
| 07 | [Similarity & distance](07-similarity-and-distance.md) | which embedding-style functions (cosine, dot, top-k, Jaccard…) fit non-embedding vectors, the metric registry, vector `kind` defaults |

## Feature map

Tier key: **C** = committed for v1.0.0 · **S** = stretch (v1.0.0 if capacity allows, otherwise v1.1) · **P** = post-1.0.

| Area | Feature | Tier | Primary audience |
|---|---|---|---|
| Foundation | `linalg/` package (gonum-backed), synth Cholesky consolidated onto it | C | all |
| Foundation | Mergeable weighted co-moment accumulator | C | all |
| Foundation | Virtual vectors (`Request.Vectors`) | C | all |
| Foundation | Native `vec_f32` / `vec_f64` field types | C | scientific, ops |
| Foundation | expr-lang vector functions (`dot`, `norm`, `vsum`, `vmean`, `v[i]`) | C | all |
| Similarity | Shared metric registry (`linalg/metric`) | C | all |
| Similarity | Vector `kind` (`measure`/`scale`/`composition`/`binary`) with metric defaults + unsuited-metric warning | C | all, harness |
| Similarity | Centering / ipsatization expr functions (`vcenter`, `vzscore`, `vnormalize`) | C | survey |
| Similarity | Set similarity on `set_*` fields (`jaccard`, `dice`, `hamming`, `overlap`) | C | survey, ops |
| Similarity | `MAT_SET_AFFINITY` (co-occurrence / lift matrix of a multi-select) | S | survey, ops |
| Similarity | Exact top-k nearest rows (mergeable heap) | S | ops, survey |
| Result | `Response.Matrices` + `MatrixResult` | C | all |
| Result | Matrix capability block in manifest; predict shape | C | harness |
| Multivariate | `MAT_COVARIANCE`, `MAT_CORRELATION` (Pearson / Spearman / Kendall) | C | all |
| Multivariate | `MAT_PARTIAL_CORRELATION` | C | scientific, survey |
| Multivariate | `MAT_RELIABILITY` (α, ω, item-total, α-if-deleted) | C | survey |
| Multivariate | `MAT_PCA` (+ scree, loadings, explained variance) | C | all |
| Multivariate | `MAT_FACTOR` (EFA, varimax / promax, KMO, Bartlett) | S | survey |
| Multivariate | `MAT_COLLINEARITY` (VIF, condition index) | C | scientific |
| Multivariate | `MAT_CANONICAL_CORRELATION` | P | scientific |
| Multivariate | `MAT_DISTANCE` (between groups / profiles) | S | survey, ops |
| Tests | `TEST_HOTELLING_T2`, `TEST_MANOVA`, `TEST_BOX_M`, `TEST_MARDIA` | C / C / S / S | scientific, survey |
| Row-level | `ATTR_MAHALANOBIS` | C | survey QA, ops anomaly |
| Row-level | `ATTR_PC_SCORE`, `ATTR_FACTOR_SCORE` | C / S | survey, scientific |
| Row-level | `ATTR_SCALE_SCORE` (battery mean/sum with min-valid rule) | C | survey |
| Row-level | `ATTR_VEC_DISTANCE` / `ATTR_PROFILE_SIMILARITY` to a reference profile | S | survey, ops |
| Aggregation | `AGG_VEC_MEAN` (centroid), `AGG_VEC_SUM` | C | all |
| Grouping | `GROUP_KMEANS` (seeded segmentation, Euclidean-only) | C | survey, ops |
| Windows | `WIN_ROLLING_CORR`, `WIN_ROLLING_BETA` | S | ops, finance |
| Regression | coefficient covariance matrix (`vcov`) on `RegressionResult` | C | scientific |
| Regression | multivariate (multi-Y) OLS | P | scientific |
| Overlays | `OVERLAY_CORRESPONDENCE` (perceptual map coordinates) | C | survey |
| Overlays | `OVERLAY_STD_RESIDUAL` (adjusted standardized residuals) | C | survey |
| Overlays | `OVERLAY_MARKOV` (row-stochastic, n-step, steady state) | C | ops |
| Overlays | `OVERLAY_RAKE` (IPF to target margins) | C | survey |
| Overlays | `OVERLAY_PROFILE_SIMILARITY` (row/col similarity matrix) | S | survey |
| Overlays | `OVERLAY_SERIATION` (cluster ordering hint) | S | survey, harness |
| Overlays | `OVERLAY_MATRIX_FORMULA` (transpose, multiply, inverse over layers) | S | ops, scientific |
| Overlays | ~~`OVERLAY_CORR_PVALUE`~~ dropped; matrix p-value correction is opt-in `MatrixSpec.multiplicity` (statistical integrity) | — | all |
| Overlays | `OVERLAY_MATRIX_CONGRUENCE` (Compose: compare two matrices) | S | survey, scientific |
| Synth | `Spec` accepts a declared correlation matrix; fidelity report adds matrix distance | S | all |
| Extensions | `MatrixOpRegistration` (`MAT_` namespace) | C | embedders |

## Features beyond the initial ask — and why to add them

You asked to steer clear of AI / embedding features but to be told when an adjacent feature earns its place. These are the ones that sound "ML" but are classical, deterministic statistics with long histories in Pulse's target domains. Each has a clear reason to exist in a tabular statistics engine; none requires a model, training loop or external service.

**1. Native vector field type (`vec_f32[N]`) — even without embeddings.**
Embeddings are the famous use, but not the main one for Pulse's audience. A fixed-dimension numeric array per row describes:
- *survey grids* — a 15-item Likert battery or a brand × attribute rating block, stored as one field instead of 15 sibling columns with fragile naming conventions;
- *time profiles* — a 24-value hourly load curve, a 12-month seasonality profile, a 52-week activity vector per customer;
- *compositional data* — a share-of-wallet split across 8 categories that must sum to 1;
- *instrument output* — spectra, multi-sensor readings.
In each, the columns only mean something *together*. A native type keeps them co-located in the fixed-width row (good for decode), lets the schema declare dimension labels once, and lets every `MAT_*` operator take one field name instead of a 15-element list. Virtual vectors cover the existing-cohort case; the native type covers new imports. Recommendation: **add**, committed.

**2. `GROUP_KMEANS` — segmentation.**
Clustering is often filed under ML, but k-means segmentation has been a core deliverable in market research since the 1970s ("needs-based segments", "attitudinal clusters"). In Pulse it is simply a **grouper whose buckets are computed** — it outputs a categorical key like any `GROUP_*`, so every aggregator, crosstab and overlay works on the segments unchanged. With a fixed seed, k-means++ initialization and documented tie-breaking it is fully deterministic, so it fits Pulse's golden-test discipline. **Decided: committed** (planning review).

**3. `ATTR_MAHALANOBIS` — multivariate outlier / data-quality scoring.**
Sometimes sold as "anomaly detection", but it is one formula: distance from the centroid scaled by the inverse covariance. In surveys it flags straight-liners, speeders and fabricated interviews; in ops it flags unusual store/day combinations that no single metric would catch. It reuses the covariance matrix from `MAT_COVARIANCE`. Recommendation: **add**, committed.

**4. `ATTR_PC_SCORE` / `ATTR_FACTOR_SCORE` — projection.**
"Dimensionality reduction" sounds ML, but scoring rows on principal components or factors is how survey analysts turn 25 statements into 3 interpretable indices that then flow into crosstabs and regressions. Without row-level scores, PCA output is a dead end. Recommendation: **add PC scores committed, factor scores stretch** alongside `MAT_FACTOR`.

**5. Profile similarity (cosine / correlation distance between rows of a crosstab).**
The same maths as embedding similarity, applied to a different question: *which brands have the most similar image profiles?* or *which regions have the most similar product mix?* It operates on aggregated results, not on raw embedding vectors, and its output is a small labeled matrix. Recommendation: **add as stretch** overlay. The full treatment of which embedding-style functions fit Pulse's vectors — and the cosine-on-ratings trap — is [07](07-similarity-and-distance.md).

Features deliberately **left out**: nearest-neighbour / vector search indexes, model training (neural, gradient boosting), any feature whose output depends on an external model, and approximate methods whose results change between runs. They do not fit Pulse's deterministic, self-describing contract, and the user has ruled them out.

## Guiding principles for the theme

1. **Matrices are results, not new execution engines.** Every feature reduces to "accumulate a sufficient statistic over rows, then decompose". The accumulation reuses the existing streaming / shard / parallel machinery; the decomposition is a pure function at flush.
2. **Mergeable where the maths allows.** Co-moment matrices merge exactly (Chan et al. pairwise update), so covariance, correlation, PCA, VIF, reliability and Mahalanobis-fit all stream, shard and parallelize. Rank correlations and k-means do not, and must declare so.
3. **Additive wire changes only.** New slots are `omitempty`; `format_version` stays `"1.1"`.
4. **Predict knows the shape.** A `p × p` matrix's dimensions, labels and memory cost are known from the schema alone, before a record is read.
5. **Deterministic output.** Eigenvector sign, factor ordering, cluster label assignment and iteration order are fixed by documented conventions, so goldens and LLM consumers see stable output.
6. **Weighted by design.** Survey data is weighted; every `MAT_*` accumulator accepts a weight field from day one, not as a retrofit.
