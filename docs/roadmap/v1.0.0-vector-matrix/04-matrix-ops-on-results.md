# 04 — Matrix operations on results (overlays)

**Decision from the interview:** matrix operations over aggregated results are expressed as **new `OVERLAY_*` kinds**. These are read-only decorations keyed to host coordinates, and they never mutate `Response.Crosstab.Matrix` or `Response.Matrices[i].primary`. That keeps the existing overlay contract intact, so every new kind inherits these behaviours unchanged:
- the predict/runtime misshape twins (`PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE`, …);
- the `OverlayLayer.Warnings` routing;
- the byte-identical overlay-free guarantee.

## Hosts

| Host | Exists? | New in this theme |
|---|---|---|
| MATRIX (Crosstab) | yes | the main target for correspondence analysis, Markov, raking, similarity and residuals |
| **MATRIX_RESULT** (`Response.Matrices[i]`) | **new** | overlays on correlation / covariance / loadings matrices (p-value adjustment, thresholding, seriation) |
| COMPOSE | yes | matrix-vs-matrix comparisons across slots (congruence, matrix formula across slots) |
| SERIES, FACET, CHAIN | yes | not targeted in v1.0.0 |

**Payload shapes.** The existing `scalar | series | matrix` set covers almost everything. Correspondence analysis needs coordinates for **both** axes, and the cleanest representation is two series payloads (row points and column points). Proposal: allow an overlay to emit an additive `Payload.Series2` slot, or alternatively emit two layers. **Open question in 06.**

**Selecting a MATRIX_RESULT host.** A spec on that host carries `Ref.Matrix{Name}`, naming the `MatrixSpec`. This is a new reference family, the seventh, exactly one arm populated as today.

---

## Committed kinds

### `OVERLAY_CORRESPONDENCE` — MATRIX host · scope `matrix`
- **What it computes.** Simple correspondence analysis of the crosstab: the SVD of the standardized residual matrix.
- **Output.**
  - Row and column principal coordinates for `params.dimensions` (default 2).
  - Inertia per dimension (singular values²) and the share of total inertia.
  - Per-point quality (cos²) and contribution.
- **Why.** The **perceptual map**: brand × attribute crosstabs plotted so that similar brands and the attributes that describe them sit close together. It is one of the most-produced charts in market research, and today it requires exporting to another tool. It reads the crosstab Pulse already builds and costs one SVD of a small matrix.
- **Gotchas.**
  - It requires non-negative counts (cell aggregator `AGG_COUNT` / `AGG_SUM` of weights).
  - Rows or columns with a zero margin are dropped with a warning.
  - The sign convention comes from `linalg`.
  - A symmetric vs asymmetric map is chosen with `params.normalization` (`symmetric` | `row_principal` | `column_principal`).

### `OVERLAY_STD_RESIDUAL` — MATRIX host · scope `cell`
- **What it computes.** Adjusted standardized (Haberman) residuals per cell: `(O − E) / sqrt(E · (1 − r/N) · (1 − c/N))`.
- **Why.** `OVERLAY_CHISQ_MATRIX` gives one χ² for the whole table. Analysts need to know **which cells drive it**, and the |residual| > 1.96 rule is the standard "significantly over-/under-represented" flag in tabulation software. It is cheap, and it is buffered like the χ² family.

### `OVERLAY_MARKOV` — MATRIX host · scope `matrix`
- **Input.** A square crosstab of `from_state × to_state`, both axes over the same key set. If the key sets differ, the overlay refuses with `PULSE_OVERLAY_MATRIX_NOT_SQUARE`.
- **Output.**
  - The row-stochastic transition matrix.
  - The n-step projection `Pⁿ` for `params.steps` (default `[1, 3, 6, 12]`).
  - The **steady-state distribution**: the left eigenvector for eigenvalue 1, or power iteration.
  - The expected time to absorption when `params.absorbing` names absorbing states (e.g. `"churned"`).
- **Why.** Customer tier migration, churn flows, funnel progression, brand switching. Ops analysts already build exactly this crosstab and then compute the projections in a spreadsheet.
- **Gotchas.** Reducible or periodic chains get a warning and no steady state. A zero row (a state never left) is treated as absorbing, with a warning.

### `OVERLAY_RAKE` — MATRIX host · scope `matrix`
- **Input.** A crosstab plus target margins (`params.row_targets`, `params.col_targets`, as counts or shares).
- **Output.**
  - Iterative proportional fitting (IPF) of the cell matrix to the targets.
  - The fitted matrix.
  - Per-cell adjustment factors.
  - The iteration count and the convergence residual.
- **Why.** Raking is how survey data is weighted to known population totals (age × region × gender). As an overlay it answers "what would this table look like weighted to census margins" **without** mutating the base. A rake-to-weights feature that writes a weight column to a new cohort is a natural follow-up (P tier, via filter-to-file style output).
- **Gotchas.** Non-convergence within `max_iter` raises `PULSE_OVERLAY_RAKE_NOT_CONVERGED` as a layer warning, and the payload is still emitted with `converged: false`. Structural zeros are respected.

### ~~`OVERLAY_CORR_PVALUE`~~ — dropped
Correcting correlation-matrix p-values is done by `MatrixSpec.multiplicity` (the shared core in [statistical integrity 02](../v1.0.0-statistical-integrity/02-multiple-comparisons.md)), which adds a `p_adjusted` auxiliary matrix. A separate overlay would have been a second mechanism for the same thing. Because correction is opt-in, the multivariate-design skill and the `PULSE_ADVISORY_MANY_TESTS` advisory carry the warning about 190 uncorrected tests at p = 20.

---

## Stretch kinds

### `OVERLAY_PROFILE_SIMILARITY` — MATRIX host · scope `matrix`
- **What it computes.** A row × row (or column × column, set by `params.axis`) similarity matrix of crosstab profiles, using `correlation` | `cosine` | `chi_square_distance`.
- **Why.** Answers "which brands have the most similar image?" or "which regions have the most similar product mix?". The output is k×k labeled.

### `OVERLAY_SERIATION` — MATRIX and MATRIX_RESULT hosts · scope `matrix`
- **What it computes.** A suggested **row/column order** from hierarchical clustering (average linkage) or from the leading-eigenvector order.
- **Output.** A permutation as a series payload. It never reorders the host itself.
- **Why.** Reordered correlation matrices and crosstabs reveal block structure that is invisible in alphabetical order. As a display hint it respects the no-mutation rule, and renderers and LLMs can apply it. It can also emit the dendrogram merge list for chart rendering.

### `OVERLAY_MATRIX_FORMULA` — MATRIX, MATRIX_RESULT, COMPOSE hosts
- **What it computes.** A small matrix expression language over the host and named earlier layers: `T(A)`, `A @ B`, `inv(A)`, `A * B` (element-wise), `rownorm(A)`, `colnorm(A)`, `diag(A)`, `trace(A)`, `det(A)`.
- **Why.** It is the escape hatch for analysts, matching the role `OVERLAY_FORMULA` plays for scalars. Example: apply a known conversion matrix to a crosstab of counts.
- **Design caution.** expr-lang has no matrix type. The proposal is a separate, tiny, typed evaluator in `linalg` (parse → shape-check at predict → evaluate). Shape errors are caught at **predict** with axis labels in the message (`PULSE_OVERLAY_MATRIX_SHAPE_MISMATCH`).

### `OVERLAY_MATRIX_CONGRUENCE` — COMPOSE host · scope `matrix`
- **What it computes.** Compares the same `MatrixResult` across two Compose slots, e.g. the correlation matrix this wave vs last wave, or segment A's PCA loadings vs segment B's.
- **Output.** Tucker's congruence coefficient per component, the RV coefficient, a Frobenius distance, and a cell-wise difference matrix.
- **Why.** Tracking studies ask "has the structure changed, not just the means?". This is the matrix analogue of the existing `*_VS_REF` family.

---

## Post-1.0

- `OVERLAY_MCA` — multiple correspondence analysis over more than two categorical variables. It needs a Burt-matrix host, which is not a crosstab.
- `OVERLAY_LEONTIEF` — input-output (I − A)⁻¹ multipliers. Niche, but it falls out of `OVERLAY_MATRIX_FORMULA`.
- `OVERLAY_NETWORK_CENTRALITY` — eigenvector / PageRank centrality on a square flow crosstab.

---

## Streamability

All new kinds are **buffered** (`types.OverlayStreamability` = false), consistent with every existing MATRIX-host kind: the fold runs after the host matrix is final. As with the existing overlays, this does **not** force the crosstab itself to buffer. `CanFuseCrosstab` stays blind to `Request.Overlays`.
