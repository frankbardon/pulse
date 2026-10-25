# Vectors and Matrices

A **vector** names a battery of numeric fields once; a **matrix** slot
turns a vector into a result in which every member is compared with
every member. Nine operators ship: `MAT_COVARIANCE`,
`MAT_CORRELATION` (Pearson by default; Spearman or Kendall tau-b under
`params.method`), `MAT_PARTIAL_CORRELATION`, `MAT_RELIABILITY`,
`MAT_PCA`, `MAT_FACTOR`, `MAT_COLLINEARITY`, `MAT_DISTANCE` (which
compares bucket centroids instead of members) and `MAT_SEM` (a
structural equation model in lavaan / semopy syntax). All are additive: a request without
`vectors` / `matrices` is byte-identical to before, and `format_version`
stays `"1.1"`. The slots are gated by the `capability:matrices` feature.

```json
{
  "cohort": {"filename": "training_data.pulse"},
  "vectors": [{"name": "money", "fields": ["price", "income", "label"]}],
  "matrices": [
    {"type": "MAT_CORRELATION", "vector": "money",
     "params": {"missing": "pairwise", "summary": {"top_pairs": 3}}}
  ]
}
```

## Vectors

`vectors: [{name, fields | pattern, labels?, coerce?, kind?, standardize?}]`, exactly one of
`fields` / `pattern`. A literal field keeps the caller's order; a glob
(`q*`) expands in place in schema order; `pattern` is an unanchored
regexp in schema order. Members are integer and float fields;
`packed_bool` needs `coerce: "binary"`; categorical, `set_*`, `date`,
`datetime` and `decimal128` are refused with `PULSE_VECTOR_MEMBER_TYPE`.
Other codes: `PULSE_VECTOR_EMPTY`, `_DUPLICATE`, `_LABELS_MISMATCH`,
`_UNKNOWN`, `_INVALID`, and `_NAME_COLLISION` for a name equal to a
schema field or to a feature output, attribute or window label of the
request (an expression reads a vector by name); a vector no operator
slot or expression (formula, expression filter, `total()` argument)
names warns `PULSE_VECTOR_UNREFERENCED`. `Pulse.Predict` echoes the resolved members
as `resolved_vectors`.

A literal member may also name a column the same request computes: a
feature output, an attribute label or — when windows run over the record
rows (no groups, aggregations or crosstab) — a window label, e.g. an
`ATTR_FORMULA` imputation. The matrix reads the value the request writes
on each record, so the result is byte-identical to materializing the
column first (`Pulse.Materialize`) and fitting over it: a null computed
value drops the row listwise, a two-pass attribute among the members
(`ATTR_ZSCORE`, a `total()` formula) runs the request buffered and serial,
and `standardize` z-scores the computed values like any member. Globs and
`pattern` match schema fields only. Only a `MAT_*` reads such a vector: a
test, attribute, grouper or aggregation `vector`, an expression and a
`MAT_DISTANCE` `params.between.vector` refuse it with
`PULSE_VECTOR_INVALID` (`reason` `computed_member_unsupported`, `slot`,
`member`).

`standardize: "zscore"` z-scores every member with its population SD
(divisor Σw, n unweighted; frequency weights respected) over the rows
the matrix keeps after its listwise drop — per result on a grouped
request. The engine rescales the merged co-moment once at finalize, so
the slot stays streamable and mergeable and every path returns the same
bits: `MAT_COVARIANCE` returns the correlation matrix and `MAT_SEM` fits
it, exactly as if the members had been z-scored beforehand. A member
with no spread is left as it is (its figures stay as without
standardize). Only a listwise co-moment `MAT_*` reads a standardized
vector; a test, attribute, grouper or aggregation `vector`, an
expression, `MAT_DISTANCE` (which has its own `params.standardize`),
`missing: "pairwise"` and a rank method refuse it with
`PULSE_VECTOR_INVALID` (`reason` `standardize_unsupported`, `slot`).

## Matrices

`matrices: [{name?, type, vector | fields, params?, weight?, encoding?}]`
returns `Response.Matrices`, one `types.MatrixResult` per spec in
request order:

| Key | Meaning |
|---|---|
| `primary` | the matrix, a `types.MatrixValues` `{kind, encoding, row_keys, column_keys, labels?, values}` |
| `auxiliary` | pairwise: `n`, each pair's row count, same shape; under `params.inference`: `p`, `ci_lower`, `ci_upper` (see Inference) |
| `vectors` | `top_pairs` (`[{row, col, r, n}]`) for `MAT_CORRELATION` with `summary.top_pairs` |
| `scalars` | `determinant`, null unless the matrix is positive definite |
| `warnings` | `PULSE_MATRIX_*` data-quality findings |
| `group_key`, `group_header` | set on a per-bucket result |

`encoding` is `full` (default), `upper` (row r carries the p - r cells
from the diagonal) or `records`. Undefined cells are `null`, keys kept.

`records` writes every matrix of the result (the primary and each
auxiliary, rectangular ones included) as row records:
`{kind, encoding, columns, rows, labels?}`, `rows` one object per row in
`row_keys` order — `{"key": <row key>, <column>: value, ...}` — with
undefined cells `null`. `MAT_SEM`'s `auxiliary.estimates` rows also carry
`lval`, `op`, `rval`, `parameter` and `free`. The numbers are exactly the
`full` encoding's; a member named `key` is refused (`SERVICE_VALIDATION`,
`records_key_collision`), and a data-derived column of that name (a
`MAT_DISTANCE` bucket label) fails the slot. In Go, `MatrixValues.Values`
keeps the full layout and `MatrixValues.Records()` derives the rows.

## Missing data and weights

- `missing: "listwise"` (default) drops a row with any null member;
  `"pairwise"` uses, per cell, the rows where both members are present.
  A pairwise matrix of three or more members may fail to be positive
  semi-definite (`PULSE_MATRIX_NOT_PSD`: detection only on the
  covariance and correlation operators, fatal on a decomposition
  operator — see Partial correlation).
- `max_drop_share` (listwise) warns `PULSE_MATRIX_LISTWISE_HEAVY_DROP`.
- `fit: "<name>"` emits the matrix from a bound vector fit — a Compose
  `fits` entry over one matrix or fitted-attribute battery, or a saved
  population file loaded with `populations: [{name, path}]` (a vector
  fit's `save` writes one) — instead of this request's rows: no row is read, members align to the fit BY LABEL
  and the fit's weight basis governs. Every co-moment operator takes it;
  `MAT_DISTANCE`, rank correlation methods and a declared reliability
  scale range refuse it, as do `missing` / `max_drop_share` beside it
  and a grouped request.
- `PULSE_MATRIX_INSUFFICIENT_N` and `PULSE_MATRIX_ZERO_VARIANCE` flag thin
  or constant members. A constant member nulls its correlation row and
  column; the covariance keeps an exact 0.
- Weights follow [Row Weighting](weighting.md); a weight-0 row counts
  toward `n` and adds no mass. Inference's row floors (`params.inference`)
  count only rows with a positive weight, as the single-pair tests do.

## Rank correlation

`MAT_CORRELATION` `params.method`: `pearson` (default), `spearman` or
`kendall` (tau-b). A rank method ranks each member first (pairwise:
each pair re-ranked over its own rows, as R's
`cor(use = "pairwise.complete.obs")`), and each cell equals
`TEST_SPEARMAN_R` / `TEST_KENDALL_TAU` over the pair's rows. It keeps
every admitted row until finalize, so the request runs buffered and
serially (predict: `streamable` and `mergeable` false,
`row_buffer_bytes`); it takes frequency weights only (a probability
weight is `PULSE_WEIGHT_UNSUPPORTED`); `params.inference` gives its
p-values and intervals (see Inference).

## Inference

`params.inference: true` on `MAT_CORRELATION` (any `method`) and
`MAT_PARTIAL_CORRELATION` adds three auxiliary matrices, the primary's
twins cell for cell (same axis, same `encoding`, diagonal `null`):

- `p` — each pair's two-sided p-value, t = r·√(df / (1 − r²)) on
  Student's t, the arithmetic `TEST_PEARSON_R` runs;
- `ci_lower`, `ci_upper` — the 1 − `params.alpha` (default 0.05)
  Fisher-z interval tanh(atanh(r) ∓ z·se).

df and se rest on the inference size N*: the rows unweighted, Σw under
frequency weights, Kish's n_eff under probability weights. A Pearson
cell uses its own pair's N* under `missing: "pairwise"`: df = N* − 2,
se = 1/√(N* − 3). A partial correlation over g held-fixed fields uses
df = N* − 2 − g and se = 1/√(N* − 3 − g), with one N* for the matrix —
the fold's under listwise, the smallest pair's under pairwise (warned
`PULSE_MATRIX_PAIRWISE_N_STAR`), as `ppcor` does. A cell too thin for
its p or interval is `null` (never collapsed to r) and
`PULSE_MATRIX_INSUFFICIENT_N` (scope `inference`) names it; under
probability weights a too-small n_eff adds `PULSE_WEIGHT_LOW_NEFF`.

Under a rank method (`spearman` / `kendall`) each cell's p is R
`cor.test(method = …)`'s at its defaults, over the cell's own rows
(n = N*, Σw under a frequency weight — every weight above 1 is a tie):
without ties the exact test (Kendall below n = 50; Spearman's AS 89 up
to n = 1290), otherwise the tie-corrected normal approximation
(Kendall, no continuity correction) or t on n − 2 (Spearman) — the
same p `TEST_KENDALL_TAU` and `TEST_SPEARMAN_R` report, so a cell
equals the two-field test on the same pair. `cor.test` reports no
interval for a rank correlation; Pulse's is Fisher z with the method's
standard error — √((1 + ρ²/2)/(n − 3)) for Spearman (Bonett & Wright,
2000), √(0.437/(n − 4)) for Kendall (Fieller, Hartley & Pearson, 1957;
5 rows) — and [r, r] at |r| = 1.

The figures ride `matrices[*].auxiliary`. The `standard` preset (the MCP
default) keeps them on every spec that set `inference` — with the
pairwise `auxiliary.n` beside `p` and, under a multiplicity block, the
adjusted twins — and drops the auxiliary of a spec that did not; a
`return` that drops them (`minimal`, or an exclude) skips the
computation. Without a multiplicity block nothing corrects for the many
pairs. Off (the default) the response is
byte-identical to a request without the key.

## Partial correlation

`MAT_PARTIAL_CORRELATION` reads each pair's Pearson correlation with
other fields held fixed, off the precision matrix of the members'
correlation (the same co-moments as `MAT_CORRELATION`, so it streams,
merges and takes both weight kinds). `params.control` is `"all"`
(default: every pair controls for every other member, as
`ppcor::pcor`) or a list of numeric fields: each output pair then
controls for exactly those (`ppcor::pcor.test(x, y, Z)`), a listed
member leaves the output axis, and a listed field outside the members
joins the fold and its missing-data mode (projection and
`Components.Matrices` count it). No scalars; pairwise adds
`auxiliary.n`, and `params.inference` the per-pair p-values and
intervals (see Inference). If any input correlation is undefined every cell is
`null`.

It is a **decomposition** operator, so its input must be usable: a
pairwise input that is not positive semi-definite is refused with the
fatal `PULSE_MATRIX_NOT_PSD` (predict flags the risk as
`pairwise_psd_risk`) unless `params.repair: "nearest"`, which replaces
it by the nearest correlation matrix — Higham's alternating projections
with Dykstra's correction, `Matrix::nearPD(corr = TRUE)`'s settings
(at most 100 iterations, convergence 1e-7, eigenvalue floor 1e-8 of the
largest) — and keeps the code as a warning carrying
`frobenius_adjustment`, `iterations` and `converged`. A member or
control that is a linear mix of the others is `PULSE_MATRIX_SINGULAR`
with `rank`, `condition_number` and `dependent_fields`.

## Scale reliability

`MAT_RELIABILITY` checks a battery of items before it is summed into a
score. Its `primary` is the inter-item correlation matrix; `scalars`
carry Cronbach's `alpha` (on the covariance, `psych::alpha` raw_alpha),
`alpha_standardized` (from the mean inter-item r), `mean_inter_item_r`
and McDonald's `omega`; `vectors` carry, per item in axis order, the
corrected `item_total_r` (the item against the sum of the others),
`alpha_if_deleted`, `item_mean` and `item_sd`.

Under a probability weight the covariance is `cov.wt`'s "unbiased"
form, scaled by n_eff/(n_eff − 1). With `missing: "pairwise"` every
entry rests on its own rows, so each takes its own pair's Kish n_eff
(an item's variance takes the rows where that item is present). An
entry whose n_eff is at most 1 is `null`, and so is every figure it
reaches: `alpha`, `alpha_if_deleted` and `item_total_r`.

Reverse-worded items are named in `params.reverse` together with the
battery's `scale_min` / `scale_max`: each value becomes
`scale_min + scale_max − x` before the fold, so the operator streams and
merges like `MAT_CORRELATION` and takes both weight kinds. A reverse
list without the range, or any item value outside a declared range, is
`PROCESSING_CONFIG` — values are never clamped and the range is never
read off the data.

`omega` is `(Σλ)² / ((Σλ)² + Σψ)` from a one-factor minimum-residual fit
of the inter-item correlation (`psych::fa(nfactors = 1, fm = "minres")`);
`components.matrices[i].operator` reports the fit's `iterations` and
`converged`. It is `null` with a warning when the battery has 2 items
(`PULSE_MATRIX_NOT_IDENTIFIED`), when the fit is a Heywood case
(`PULSE_MATRIX_HEYWOOD`), or when a pairwise table is not positive
semi-definite and `params.repair` is not `"nearest"`
(`PULSE_MATRIX_NOT_PSD` as a warning — alpha needs no such input and is
always reported). Alpha is read against George & Mallery's (2003) bands;
omega is not banded.

## Principal components

`MAT_PCA` decomposes the members' correlation (`params.on`
`"correlation"`, the default) or covariance (`"covariance"`). Its
`primary` is the loadings — eigenvector × √eigenvalue — as a
**rectangular** matrix (`kind: "rectangular"`): `row_keys` are the
members, `column_keys` the kept components `PC1` … `PCk`, always written
in full. `auxiliary.eigenvectors` has the same shape. `vectors` carry
every `eigenvalue` (descending), its `explained_variance` share and the
`cumulative` share, the `communalities` (each member's summed squared
loadings over the kept components) and `kmo_msa`; `scalars` carry the
overall `kmo`, Bartlett's sphericity test (`bartlett_chisq`,
`bartlett_df`, `bartlett_p`) and `components_retained`.

`params.components` chooses k: an integer (at most the member count),
`"kaiser"` (every eigenvalue above 1 — the default on a correlation,
refused on a covariance, whose eigenvalues are in the members' units),
or `{"variance": share}` (the fewest leading components whose cumulative
share reaches it). On a covariance it is required. Sign and order follow
`linalg.SymEigen`: eigenvalues descending, each eigenvector's
largest-magnitude entry positive — portable across architectures, with
values agreeing to the eigensolver's last digits.

KMO (`psych::KMO`) and Bartlett (`psych::cortest.bartlett`) always read
the correlation. Bartlett's sample size follows the weighted-inference
rule: the rows listwise, Σw under frequency weights, Kish n_eff under
probability weights, and under pairwise deletion the smallest pair's
(`PULSE_MATRIX_PAIRWISE_N_STAR` says so). A singular correlation nulls
KMO and Bartlett with a `PULSE_MATRIX_SINGULAR` warning, the components
still reported. Like the partial correlation, a non-PSD pairwise input
is refused unless `params.repair` is `"nearest"`. KMO is read against
Kaiser's (1974) bands.

`params.rotation` (default `"none"`) rotates the kept components
orthogonally toward simple structure: `"varimax"` exactly as R's
`stats::varimax` with Kaiser normalization does, `"quartimax"`,
`"geomin_orthogonal"` (`params.epsilon`, default 0.01) and `"entropy"`
(minimum entropy) as GPArotation's `quartimax`, `geominT` and `entropy`
do (gradient projection, no normalization, one start). An oblique
criterion is refused (`SERVICE_VALIDATION`, reason
`rotation_oblique_unsupported`): components are uncorrelated by
construction — use `MAT_FACTOR` for correlated factors. The rotated loadings
become the `primary` — columns ordered by the variance each accounts
for, each signed so its loadings sum positive, keys still `PC1` …
`PCk` — the unrotated loadings move to `auxiliary.unrotated`, and
`vectors.rotated_variance` lists each rotated column's sum of squared
loadings. Eigenvalues, the explained and cumulative shares, the
communalities (unchanged by an orthogonal rotation), the eigenvectors,
KMO and Bartlett are never rotated. A single kept component is left as
is. Without the knob the output is byte-identical to before.

## Centroid distances

`MAT_DISTANCE` answers "how far apart are the segments?": a grouper in
`params.between` cuts the rows into buckets — `GROUP_CATEGORY`,
`GROUP_KMEANS`, any other built-in or a registered extension grouper —
each bucket's
centroid is its (weighted) mean of every member, and the `primary` is the
k × k distance matrix between centroids, keyed by the bucket labels in the
grouper's own order (`cluster_1` … by size for k-means), zero on the
diagonal. A date-family between (`GROUP_DATE`, `GROUP_DATE_RANGES`) reads
local calendar days in its own `tz`, else the request `time_zone`, exactly
like a `groups` slot; predict echoes the zone at slot
`matrices[i].params.between`. An extension between buckets with its
buffered `Group`, so the spec runs buffered and serial.

```json
{"type": "MAT_DISTANCE", "fields": ["age", "income"],
 "params": {"between": {"type": "GROUP_CATEGORY", "field": "region"},
            "metric": "euclidean", "standardize": true}}
```

`params.metric` is a name from the shared `linalg/metric` registry
(listed under `capabilities.matrix.metrics`): `euclidean` (default,
`stats::dist`), `sq_euclidean`, `manhattan`, `chebyshev`, `cosine` (1 − cos,
null against a zero-length centroid), `hellinger` and `aitchison` (for
compositions: they read non-negative centroids, are refused beside
`standardize`, and null a distance touching a negative coordinate — or an
aitchison zero part — with `PULSE_MATRIX_METRIC_DOMAIN`) or `mahalanobis` —
over the pooled within-bucket covariance, which allows for how the members
vary together (it reads the raw centroids; it is scale-free, so
`standardize` changes nothing). `params.standardize` z-scores every member
over the bucketed rows first (their mean and N* − 1 spread, R `scale()`); a
member with zero spread has no z-score and is skipped.

Missing coordinates follow the registry's one policy: a coordinate
undefined on either centroid is dropped pairwise and the element sums
(`euclidean`, `sq_euclidean`, `manhattan`) are rescaled by p / p_observed,
as R `stats::dist()` does; `mahalanobis` needs every coordinate.
`params.min_valid` (default 1) is the fewest complete member pairs a
distance needs — below it the distance is null.
`auxiliary.centroids` holds the k × p centroids (rows the buckets),
`centroids_standardized` and `pooled_covariance` ride their options, and
`vectors.sizes` / `counts` give each bucket's weight mass and rows. Rows
the grouper places in no bucket (a null key, an include rejection, a
k-means row with a null member) are excluded and counted in
`components.matrices[].operator.n_null_key`; missing data is listwise
only. With a streaming grouper the matrix streams and merges (one
co-moment per bucket); `GROUP_KMEANS` and `GROUP_QUANTILE` keep the rows
for their whole-set pass and run serially. Weighted centroids under
frequency and probability weights; a k-means `between` takes frequency
weights only.

## Factor analysis

`MAT_FACTOR` is an exploratory factor analysis of the members'
correlation: where PCA summarises all of the spread, a factor model
explains only the spread the members share, leaving each its own
uniqueness. `params.factors` is the number of factors — an integer (at
most the member count) or `"kaiser"`, the default (the correlation's
eigenvalues above 1). `params.extraction` picks the solver, each
starting from the squared multiple correlations: `"minres"` (the
default) minimises the off-diagonal residuals, as `psych::fa(fm =
"minres")` does — Pulse solves to the exact optimum, where psych's
optimiser stops about 1e-5 short — and `"principal_axis"` iterates the
communalities on the diagonal exactly as `psych::fa(fm = "pa")` does
(until their sum moves by at most 0.001, at most 50 iterations), and
`"ml"` is Jöreskog's maximum likelihood over the uniquenesses, each held
in [0.005, 1], as `stats::factanal` and `psych::fa(fm = "ml")` fit it —
again solved to the exact optimum where their optimiser stops short.

The `primary` is the p × k loadings, a **rectangular** matrix with
`column_keys` `F1` … `Fk`, each column signed so its loadings sum
positive. `params.rotation` rotates them with the same engine as
`MAT_PCA`: orthogonal `"varimax"` (`stats::varimax`), `"quartimax"`,
`"geomin_orthogonal"` or `"entropy"`, or oblique `"promax"`
(`stats::promax` with power `params.kappa`, default 4), `"oblimin"`
(`GPArotation::oblimin` with `params.gamma`, default 0) or `"geomin"`
(`GPArotation::geominQ`); the geomin pair takes `params.epsilon`
(GPArotation's `delta`, default 0.01). Every criterion but varimax and
promax is gradient projection from a single start, as GPArotation's
default, so geomin in particular may settle in a local optimum. Rotated, the columns are ordered by the variance each
accounts for, the unrotated loadings move to `auxiliary.unrotated`, and
an oblique rotation adds `auxiliary.factor_correlation` — the k × k
correlation of the factors, a square matrix keyed `F1` … `Fk`.
`vectors` carry each member's `communalities` (unchanged by any
rotation) and `uniquenesses` (1 − communality) and each factor's
`variance_accounted`; `scalars` carry `rmsr` (the root-mean-square of
the correlations the factors leave unexplained), the empirical residual
chi-square (`empirical_chisq`, `df`, `empirical_p` — psych's residual
size, not a likelihood test; `empirical_p` is null when `df` ≤ 0), `kmo`
and Bartlett's test as on `MAT_PCA`, the fit's `iterations` and
`converged` (1 or 0) and `factors_retained`. Maximum likelihood adds
the likelihood test and fit indices psych reports: `likelihood_chisq`
(the Bartlett-corrected likelihood-ratio chi-square on `df`) and its
`likelihood_p`, the Tucker-Lewis index `tli`, the RMSEA point estimate
`rmsea` with its confidence interval `rmsea_lower` / `rmsea_upper`
(psych's `RMSEA` lower / upper: the noncentral chi-square inverted at
coverage `params.conf_level`, 0.9 by default as in `psych::fa`, and
rescaled by (N* − 1)·df; a bound psych's search range cannot reach, or
one on the wrong side of the point estimate, is null) and `bic` — all
but the chi-square null when `df` ≤ 0. They
need a positive-definite correlation (a singular one nulls the ML fit
with `PULSE_MATRIX_SINGULAR`) and an inference size above 1 + (2p +
5)/6 + 2k/3 (below it they are null with `PULSE_MATRIX_INSUFFICIENT_N`).

A count the members cannot identify — (p − k)² < p + k, so one factor
needs 3 members, two need 5 and three need 6 — returns null loadings
and fit figures with `PULSE_MATRIX_NOT_IDENTIFIED` (KMO and Bartlett
still compute). A member whose uniqueness falls to 0 or below (a
Heywood case; under `"ml"`, held at the 0.005 floor) is reported as
fitted with `PULSE_MATRIX_HEYWOOD`, and an
extraction or rotation that stops at its iteration cap warns
`PULSE_MATRIX_NOT_CONVERGED`. Like the partial correlation, a non-PSD
pairwise input is refused unless `params.repair` is `"nearest"`.
Weighted under frequency and probability weights. KMO is read against
Kaiser's (1974) bands; the loadings, `rmsr`, `tli` and `rmsea` are
unbanded.

## Collinearity

`MAT_COLLINEARITY` checks candidate regression predictors before a
model is fitted. Its members are the predictors only — there is no
response, so the figures are the same whatever outcome is later
modelled. `vectors.vif` is each predictor's variance inflation factor,
the diagonal of the inverse correlation matrix (`car::vif` on an `lm`
over the same predictors), and `vectors.tolerance` its reciprocal; the
`primary` is the predictors' correlation matrix and `scalars.max_vif`
the largest VIF.

Belsley's diagnostics (`perturb::colldiag`) ride beside them:
`vectors.condition_indices` (ascending, the first exactly 1),
`scalars.condition_number` (the largest) and
`auxiliary.variance_decomposition`, a **rectangular** matrix with a row
per variable and a column per dimension `D1` … `Dq` in condition-index
order — each row is that variable's coefficient variance split across
the dimensions, summing to 1. By default they are computed on the
scaled, **uncentered** predictors with an intercept column (Belsley's
recommendation, which also shows collinearity with the intercept; the
first row is `(intercept)`), rebuilt from the slot's co-moments as
W(Σ + μμᵀ), so the operator stays streamable and mergeable.
`params.center: true` computes them on the centered predictors (their
correlation) instead.

An exactly collinear set has no inverse and is refused with
`PULSE_MATRIX_SINGULAR`, whose `dependent_fields` name the members in
the dependency. Like the partial correlation, a non-PSD pairwise input
is refused unless `params.repair` is `"nearest"`. Weighted under
frequency and probability weights (the figures are scale-free, so the
two kinds agree). Neither VIF nor the condition index is banded: 5 or 10
for VIF and 30 for the condition index are rules of thumb only
(O'Brien 2007).

## Structural equation models

`MAT_SEM` fits a structural equation model — a confirmatory factor
analysis, a path model, higher-order factors, regressions on latent
qualities — to the members' covariance by maximum likelihood.
`params.model` is the model in lavaan / semopy syntax, one statement per
line: `f =~ a + b + c` (measurement; `a`'s loading is fixed to 1),
`y ~ f + x` (regression; `y1, y2 ~ f` for several), `a ~~ b`
(covariance); `l*b` shares a parameter label `l` across every use and
`0.5*b` fixes a value. Its observed variables must be exactly the
members, matched by label; absent, the model is one factor `F1` over
every member.

```json
{
  "cohort": {"filename": "survey.pulse"},
  "vectors": [{"name": "battery", "fields": ["q1", "q2", "q3", "q4", "q5", "q6"]}],
  "matrices": [
    {"type": "MAT_SEM", "name": "cfa", "vector": "battery",
     "params": {"model": "service =~ q1 + q2 + q3\nvalue =~ q4 + q5 + q6"}}
  ]
}
```

**The default setup is semopy.** A default fit reproduces
`semopy.Model(desc).fit(data)` (semopy 2.3.11) to rounding: the same
variable classification and parameter set, the Wishart ML objective over
the biased (divisor N) covariance, a port of SciPy's SLSQP run with
semopy's settings (so it stops where semopy stops — at `ftol` 1e-6,
short of the optimum), semopy's expected-information standard errors and
its `calc_stats` fit indices, conventions included (`loglik` is the
minimised discrepancy F, `aic` = 2(k − F), `gfi` equals `nfi`). Code
written against semopy ports 1 for 1. `params.solver: "fisher"` runs
damped Fisher scoring to the optimum instead (a semopy fit at a small
`ftol` lands on the same figures); `params.information: "rank2"`
computes the gradient and information from the rank-2 structure of
∂Σ/∂θ — the same figures, much cheaper on large models.

`primary` is the model-implied covariance Σ(θ̂) over the members.
`auxiliary.estimates` is semopy's parameter table: a row per
`"lval op rval"` (`op` `~` for loadings and regressions, `~~` for
variances and covariances), columns `estimate`, `std_estimate`,
`std_err`, `z_value`, `p_value` — the last three null on a fixed
parameter; `auxiliary.residual_covariance` is S − Σ(θ̂); `vectors`
carry each row's parameter name and whether it is free; `scalars` carry
`chi2` with `chi2_p_value` on `dof`, the baseline `chi2_baseline` on
`dof_baseline`, `cfi`, `tli`, `rmsea`, `gfi`, `agfi`, `nfi`, `aic`,
`bic`, `loglik`, `objective`, `n`, `n_params`, `iterations` and
`converged`. None is banded: fit-index cut-offs are rules of thumb.

Listwise only. Under a frequency weight the fit is exactly semopy's on
the expanded rows (N = Σw); a probability weight is refused
(`PULSE_WEIGHT_UNSUPPORTED`: semopy has no design-based form). A
rank-deficient covariance is fitted as semopy's default fit fits it
(`params.singular: "nearest"`, the default): raw while its Cholesky
factorization succeeds, however near singular; otherwise to
statsmodels' `cov_nearest` substitute (the correlation form's
eigenvalues raised to 0.01, the variances kept) with the warning
`PULSE_MATRIX_NEAREST_PD` (`n`, `p`, `min_eigenvalue`), every figure
against the adjusted matrix. A zero-spread member (raw, or the zeros
of a standardized one) has no substitute — semopy raises — and fails
the slot with `PULSE_MATRIX_SINGULAR`; `params.singular: "refuse"`
refuses every rank-deficient covariance that way. A solver stop short
of success keeps the last iterate with `PULSE_MATRIX_NOT_CONVERGED`; a
model with more parameters than moments is fitted with
`PULSE_MATRIX_NOT_IDENTIFIED`. Streamable and mergeable: the fit reads
only the merged covariance and N (and the merged means under a mean
structure).

**A mean structure switches to lavaan.** `params.meanstructure: true`,
or any `~ 1` line in the model, models the means too, as lavaan's
`meanstructure = TRUE`: every observed intercept free (a pure
predictor's mean fixed to its sample mean, lavaan's `fixed.x`), every
latent mean fixed to 0 unless `f ~ 1` frees it; `x ~ 0*1` fixes an
intercept and `x ~ a*1` labels it (identify a free latent mean by fixing
its marker's intercept: `f ~ 1` with `x1 ~ 0*1`). The fit then follows
lavaan's conventions, not semopy's: Fisher scoring to the optimum,
unbounded variances, lavaan's χ², degrees of freedom (the means
counted), CFI / TLI / NFI / RMSEA, `gfi` / `agfi` as lavaan's
`gfi_lisrel` / `agfi_lisrel`, `loglik` the log-likelihood and `aic` /
`bic` from it — the same covariance model can report a different `chi2`
on the two paths, so compare fits on one convention. The estimates gain
`"x ~1"` rows and 95% Wald `ci_lower` / `ci_upper` columns, and
`auxiliary.implied_means` / `observed_means` (one `mean` column per
member) are emitted. Parity: lavaan 0.7.3's ML fit within 1e-4 on
estimates and SEs, 1e-3 on fit indices.

**Multi-group fits.** `params.group` names a categorical, `packed_bool`
or `u4` / `u8` field whose levels are the groups of ONE joint fit —
lavaan's `group =`, configural: every parameter free per group, except
that a labelled parameter (`a*x`) is one value in every group, as lavaan
reads a label. A group fit always models the means and follows lavaan's
conventions. The levels are the field's values that hold rows after the
filters, in dictionary order (else ascending); a row whose group is null
is dropped (listwise). Each group's mean and covariance merge on their
own, so the fit stays streamable and mergeable, and a frequency weight
applies within each group (n_g = Σw).

```json
{"type": "MAT_SEM", "name": "waves", "vector": "battery",
 "params": {"model": "service =~ q1 + q2 + q3\nvalue =~ q4 + q5 + q6",
            "group": "wave", "group_reference": "2"}}
```

The estimate rows are keyed `"[level] lval op rval"` in level order,
with `vectors.group` (each row's level) and `vectors.label` (its model
label, empty when unlabelled) beside `parameter` / `free`. `primary` is
the `group_reference` level's implied covariance (default the first
level); per level the auxiliaries are `implied_covariance.<level>`,
`residual_covariance.<level>`, `implied_means.<level>` and
`observed_means.<level>`. The scalars are joint — `chi2` sums the
groups' n_g·F_g, `dof` and the baseline sum over the groups, `rmsea`
carries lavaan's multiple-group factor √G — plus `chi2_group_<level>` and
`n_group_<level>`. A request that also sets `groups`, another field
type, a one-entry dictionary, or a group field that is a member is
refused (`SERVICE_VALIDATION`); fewer than two levels with rows, a level
with no more rows than observed variables, or a `group_reference` level
without rows fails the slot alone with `PULSE_MATRIX_INSUFFICIENT_N`.
Parity: lavaan 0.7.3's `group = "wave"` fit within the same tolerances.

`Pulse.Predict` names the levels it can see on the matrix entry:
`group_levels` (a categorical's dictionary entries, `packed_bool`'s `"0"`
/ `"1"`) with `group_levels_basis` `dictionary` / `boolean`. The list is
an upper bound — a level without rows after the filters is no group —
and the per-level auxiliaries are sized over it. On a `u4` / `u8` field
the levels are the data's: `group_levels_basis` is `unknown`, and the
`matrices` entry of `sizes` is left out. `estimated_bytes`
counts one co-moment state per level beside the base state (the
integer type's value range when the levels are unknown). The manifest
declares the per-level keys as patterns, `implied_means.<level>` or
`chi2_group_<level>`: `<level>` stands for a level's label.

A feature profile can hide the two surfaces: `capability:sem_meanstructure`
(`meanstructure` and `~ 1` lines) and `capability:sem_groups` (`group`,
`group_reference`, `group_equal`, `group_partial` and `c(...)`
modifiers; it needs `capability:sem_meanstructure`). Hidden, their keys
are unknown params keys (`PULSE_REQUEST_UNKNOWN_FIELD`), the model
syntax they own is `bad_params`, and the manifest lists neither their
params nor their output keys.

**Invariance.** `params.group_equal` holds lavaan `group.equal` classes
equal across the groups — `loadings`, `intercepts`, `means`, `residuals`,
`residual.covariances`, `lv.variances`, `lv.covariances`, `regressions`
— and `params.group_partial` frees single rows from them, spelled as
lavaan labels them (`v2~1`, `service=~s2`, `q1~~v1`). Identification
follows lavaan: a marker loading stays fixed at 1 in every group, and
equal intercepts (without `means`) free the latent means outside the
`group_reference` level, whose means stay 0 — so the scalar fit's
non-reference latent means are each factor's shift in marker units. A
labelled row keeps its label's equality instead of its class's. In the
model, lavaan's per-group modifiers `c(a1, a2)*x` and `c(0.5, NA)*x` set
a label or a value per level, in level order (a categorical or
`packed_bool` group field; one entry per level), and `NA*x` frees a
parameter. An unknown `group_equal` value, `thresholds` under the ML
estimator (it needs an ordinal one: `estimator: "wlsmv"` with
`ordered`, the `sem-ordinal` skill), a `group_partial` row the model
lacks, or a modifier whose length is not the level count is refused
(`SERVICE_VALIDATION` `bad_params`, predict alike).

The invariance ladder is one slot per step, or a compose sweep over
`group_equal` — which also reads one comma-separated string, so a sweep
axis (a scalar) can carry it:

```json
{"sweep": {"mode": "zip", "label": "{{step}}",
  "axes": [{"name": "step", "values": ["configural", "metric", "scalar"]},
           {"name": "eq", "values": ["", "loadings", "loadings,intercepts"]}],
  "request": {"cohort": {"filename": "brand_survey.pulse"},
    "matrices": [{"type": "MAT_SEM", "name": "waves", "vector": "brand",
      "params": {"model": "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3\nservice =~ s1 + s2 + s3",
                 "group": "wave", "group_equal": "{{eq}}"}}]}}}
```

Compare `chi2`, `dof`, `cfi` and `rmsea` step to step; Pulse computes no
χ² difference test. Parity: lavaan 0.7.3's `group.equal` /
`group.partial` fits within the same tolerances.

Runnable: `internal/examples/matrices/16_sem_multigroup.json` (mean
structure, configural, scalar and partial slots across
`brand_survey.pulse`'s two waves) and
`internal/examples/compose/09_sem_invariance_sweep.json` (the ladder as
one sweep). The fixture plants a wave-2 shift in the value and service
means, which the scalar step recovers.

**Defined parameters.** A model line `name := expr` is lavaan's
defined parameter: a function of labelled parameters — an indirect
effect `ind := a*b`, a total, a ratio, or a driver's contribution to a
change between groups (`dv := bv*(mv2 - mv1)`, a path times a latent
mean change). The expression takes labels (every level's label of a
per-group `c(a1, a2)*x`), numeric constants, `+ - * / ^` with R's
precedence, parentheses, `sqrt` / `exp` / `log` / `abs` and the names
of earlier `:=` lines. An unknown label, a forward or self reference, a
repeated name or bad syntax is `SERVICE_VALIDATION` `defined_invalid`
(`details.problem`); lavaan's `==` / `<` / `>` lines are
`constraint_unsupported`. A `:=` line switches a covariance-only model
to lavaan's conventions. Each definition is a row `"name := expr"` after
the parameter table (under `group`, after every level, with
`vectors.group` `null` and `vectors.label` the name): the estimate, a
delta-method `std_err` √(gᵀVg) from the expression's exact gradient,
`z_value`, the normal `p_value`, the Wald `ci_lower` / `ci_upper` and
`std_estimate` over the standardized solution. A value that is not
finite at the estimate nulls the row and its dependents with
`PULSE_MATRIX_DEFINED_NONFINITE`. `abs()` is differentiated by its sign
(exact); lavaan's complex-step jacobian reads that slope as 0, so an
`abs()` row matches lavaan's estimate but not its SE. A contribution is
distinguishable from zero when its p is below alpha and its interval
excludes 0. The rows join a correction only through the spec's OWN
`multiplicity` block, whose family is every estimates row with a p (the
free parameters and the `:=` rows): `auxiliary.p_adjusted` /
`significant_adjusted` beside the raw `p_value`, which never moves.
Parity: lavaan 0.7.3 within 1e-4 on estimates, SEs and intervals.

**Interval level.** `params.ci_level` (default 0.95, strictly between 0
and 1) is lavaan's `level`: it sets the Wald `ci_lower` / `ci_upper` of
every lavaan-convention fit, and the percentile interval of a bootstrap.
A semopy-convention fit reports no interval, so `ci_level` there is
`bad_params`.

**Bootstrap.** `params.resample: "bootstrap"` is lavaan's
`se = "bootstrap"`: `bootstrap_iters` replicates (default 1000), each
drawing the listwise rows with replacement — within each level under
`group`, so every replicate keeps each group's n — and refitting the
model. Over the replicates that refit, every free and `:=` row's
`std_err` is the replicates' standard deviation, `z_value` = estimate /
`std_err`, `p_value` its normal p (normal theory over the bootstrap SE,
as lavaan reports it) and `ci_lower` / `ci_upper` the percentile
interval at `ci_level`; the estimates and fit statistics stay the full
data's. `scalars.bootstrap_requested` / `bootstrap_succeeded` echo the
replicates; fewer than 10 successes fail the slot alone
(`PULSE_MATRIX_NOT_CONVERGED`, `reason: bootstrap_too_few`). The same
rows and `rng_seed` give the same bits at any worker or shard count;
without `rng_seed` (or at 0) the seed is derived from the request and
the slot's rows, as a regression's `resample` derives it. The slot
buffers its rows (predict reports the buffered path), any weight is
`PULSE_WEIGHT_UNSUPPORTED`, `params.fit` cannot bind it, and
`bootstrap_iters` has no instance limit (`Options.Limits.RequestTimeout`
stops a long run between replicates).

Runnable: `internal/examples/matrices/17_sem_contribution.json` — each
driver's contribution to the change in `recommend` between the two
waves of `brand_survey.pulse`, with delta-method and bootstrap
intervals.

**Latent scores.** `ATTR_SEM_SCORE` turns a fitted model into a field:
each row's score on one latent (`params.latent`, a higher-order latent
included), lavaan's `lavPredict` — `method: "regression"` (default, the
conditional mean of the latent given the row) or `"bartlett"` (the
unbiased GLS weights). With a mean structure or `group` the scores sit
on the latent-mean scale (the reference level at 0) and each row is
scored with its own group's parameters; otherwise they are centered at
the fit rows. A null member or group scores null; a level with no fit
rows scores null with `PULSE_SEM_SCORE_LEVEL_UNSEEN`. The fit is one of
three:

- **inline** — the attribute takes `MAT_SEM`'s fit knobs and fits once,
  cohort-wide, over the rows its `fit_on` filterers admit (default every
  filtered row), exactly `MAT_SEM`'s fit over those rows; two-pass, so a
  grouped request buffers;
- **bound** — `params.fit` names a Compose `fits` entry holding one
  `MAT_SEM` (a SEM fit, `fits.<name>` = `{"sem": {...}}`); the slot fits
  nothing, is row-local (it streams, fuses and merges) and scores bit-equal
  to the inline fit;
- **loaded** — a fit entry's `save: "fits/brand.pulse-sem.json"` writes
  the fit at full precision; a later request's
  `models: [{"name": "brand", "path": "fits/brand.pulse-sem.json"}]`
  loads it (the suffix picks the kind) and `params.fit: "brand"` scores
  from it bit-equal to the in-compose fit.

```json
{"type": "ATTR_SEM_SCORE", "label": "service_score", "vector": "brand",
 "params": {"fit": "brand", "latent": "service"}}
```

A score is an ordinary `f64` column: average it by wave, or use it as a
regression predictor. A failed fit nulls every score with a warning
(`PULSE_FIT_UNAVAILABLE` for a compose fit, the `PULSE_MATRIX_*` code
inline); a frequency weight weights the fit only. Parity: lavaan 0.7.3
`lavPredict` within 1e-4.

Runnable: `internal/examples/attributes/25_sem_score.json` (inline,
scores averaged by wave) and
`internal/examples/compose/10_sem_fit_scores.json` (one compose fit,
scores averaged by wave and regressed on by `recommend`).

## Groups, cost and refusals

With `groups`, a matrix is computed per bucket, in `Response.Data` order
(`sort` included). `Pulse.Predict` reports `matrices[]`: `shape`,
`accumulator_bytes`, `streamable`, `mergeable`, `row_buffer_bytes` (a
rank method), `pairwise_psd_risk`, and for a grouped request
`bucket_basis`, `estimated_buckets`, `estimated_cells` and
`estimated_bytes` (an upper bound; omitted when the bucket count depends
on the data). `estimated_bytes` is the run's co-moment state: merge
blocks (`ceil(records / 4096)`, counted per shard for an archive) ×
buckets × `accumulator_bytes`, plus a rank method's row buffer. Matrix dimension is capped by
`Options.Limits.MaxMatrixDim`, and the opt-in
`Options.Limits.MaxEstimatedMemory` counts `estimated_bytes` in the
memory estimate it checks before any record is read.

Refused identically by predict and the run: matrices with `joins` or on a
chain stage after the first (`PULSE_MATRIX_UNSUPPORTED_SOURCE`), and with
`crosstab` (`PULSE_MATRIX_HOST_CONFLICT`).

## Your own matrix operator

An embedder adds a `MAT_<NAMESPACE>_<NAME>` operator through
`Options.Extensions.MatrixOps`. The operator only finalizes: the engine
folds the members, merges the 4096-row blocks, cuts the group buckets
and applies weights and `params.missing`, then hands `Finalize` the
merged, read-only `*linalg.CoMoment`.

```go
// MAT_ACME_SCATTER: each pair's raw weighted co-moment.
type scatter struct{}

func (scatter) Finalize(in *extend.MatrixInput) (extend.MatrixOutput, error) {
    return extend.MatrixOutput{Primary: in.Square(in.CoMoment.PairM2)}, nil
}

p, err := pulse.New(pulse.Options{Extensions: pulse.Extensions{
    MatrixOps: []pulse.MatrixOpRegistration{{
        Name:        "MAT_ACME_SCATTER",
        Description: "Scatter matrix: each pair's raw weighted co-moment.",
        Factory: func(*types.MatrixSpec, *encoding.Schema) (extend.MatrixOperator, error) {
            return scatter{}, nil
        },
        Streamable:  true, // finalize from the co-moment; mergeable across workers
        WeightAware: true,
        OutputKeys:  descriptor.MatrixOutputKeys{Primary: "scatter"},
    }},
}})
```

A request names it like a built-in, and the engine-owned params work
unchanged:

```json
{
  "cohort": {"filename": "training_data.pulse"},
  "matrices": [
    {"type": "MAT_ACME_SCATTER", "fields": ["price", "income", "label"],
     "params": {"missing": "pairwise"}}
  ]
}
```

Because `Finalize` reads only the merged state, the result has the same
bits under every worker setting (next section) as long as `Finalize` is
pure. Registration rules, the `pulse.New` probe, the buffered tier
(`Streamable: false`, which also receives the rows), weights and
multiplicity: [Extension Points](../internals/extension-points.md).

## Determinism

Serial, parallel-decode and sharded runs return bit-identical matrices,
and a multi-shard archive matches its single-file twin bit for bit
(merge blocks key by the global record ordinal, whatever the shard
split). An extension operator's results carry the same guarantee. A correlation cell agrees with the corresponding
`TEST_PEARSON_R` within `1e-12 + 1e-12 * |r|`.

Contract: `.claude/reference/matrix-and-vectors.md`; payload shapes:
[Payload Schema](../contract/payload-schema.md); agent guide: the
`matrix-results` skill; kernels: [Linear Algebra](linalg.md).
