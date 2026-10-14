# Vectors and Matrices

A **vector** names a battery of numeric fields once; a **matrix** slot
turns a vector into a result in which every member is compared with
every member. Eight operators ship: `MAT_COVARIANCE`,
`MAT_CORRELATION` (Pearson by default; Spearman or Kendall tau-b under
`params.method`), `MAT_PARTIAL_CORRELATION`, `MAT_RELIABILITY`,
`MAT_PCA`, `MAT_FACTOR`, `MAT_COLLINEARITY` and `MAT_DISTANCE` (which
compares bucket centroids instead of members). All are additive: a request without
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

`vectors: [{name, fields | pattern, labels?, coerce?}]`, exactly one of
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

`encoding` is `full` (default) or `upper` (row r carries the p - r cells
from the diagonal). Undefined cells are `null`, keys kept.

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

`params.rotation: "varimax"` (default `"none"`) rotates the kept
components orthogonally toward simple structure, exactly as R's
`stats::varimax` with Kaiser normalization does. The rotated loadings
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
`MAT_PCA`: `"varimax"` (orthogonal, `stats::varimax`), `"promax"`
(oblique, `stats::promax` with power `params.kappa`, default 4) or
`"oblimin"` (oblique, `GPArotation::oblimin` with `params.gamma`,
default 0). Rotated, the columns are ordered by the variance each
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
