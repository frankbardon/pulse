```yaml
name: vector-row-scores
description: How the per-row vector scores fit and compare — the vector-or-fields slot and labels, fit_on against a bound params.fit, label matching, two-pass versus row-local execution and merging, the failed-fit and singular-fit nulls, a matrix read from a fit, and every reference form (inline, table, key_field, centroid, population, record) with its missing-key and domain warnings. Topical design; per-operator params in the atomic op-attr-* and op-mat-* skills.
type: guide
kind: design
applies_to: process, compose, predict
covers: [fit_on, fit, labels, reference, centroid, population, record, key_field, min_valid, row score, two-pass, row-local, fit_and_fit_on, fit_and_missing, fit_grouped]
```

# Vector row scores: fits and references

A row score reads one vector — exactly one of `vector` (a `vectors` entry) or inline `fields` (globs ok), never `field` — and emits one `f64` per record; a null member nulls the row. `labels` (inline `fields` only) gives one label per member. Which operator: [`multivariate-design`](multivariate-design.md); which metric: [`vector-metrics`](vector-metrics.md); figure layout: [`multivariate-outputs`](multivariate-outputs.md).

## Fitted scores

- `ATTR_MAHALANOBIS` fits a mean and covariance.
- `ATTR_PC_SCORE` fits a principal component.
- `ATTR_FACTOR_SCORE` fits a factor model.
- `ATTR_VEC_DISTANCE` fits only for `std_euclidean` / `mahalanobis`.
- `ATTR_SEM_SCORE` fits a structural equation model and scores one latent (lavaan's `regression` or `bartlett` method), each group with its own parameters. Its `fit` is a SEM fit: a compose `fits` entry over one SEM matrix, or a saved `.pulse-sem.json` loaded by `models: [{name, path}]` (the suffix picks the kind) — scored bit-equal to the inline fit ([`sem-fits`](sem-fits.md)).

Each fits once, then scores every row; a score is an ordinary field (average it by group, use it as a regression predictor):

- `fit_on` — `"filtered"` (default) or an inline filterer array picking the FIT rows; every filtered row is still scored.
- `fit` — a bound vector fit's name, scored against instead of fitting: a compose `fits` entry over one matrix or fitted-attribute battery, or a loaded `populations` entry ([`compose-fits`](compose-fits.md)). XOR `fit_on` (`fit_and_fit_on`); its own weight basis governs. Matched BY LABEL to the members: a mismatch is `PULSE_VECTOR_REFERENCE_MISMATCH`, an unbound name `PULSE_MODEL_UNKNOWN`, another kind's name `PULSE_MODEL_KIND_MISMATCH`. `ATTR_SEM_SCORE` reads a SEM fit instead ([`sem-fits`](sem-fits.md)).
- `missing` — `listwise` only.
- `weight` — fits only (N* = Σw or n_eff; the Mahalanobis covariance is M2*/(N* − 1)); a zero-weight row is scored, not fitted.

Execution and failure:

- The fit is cohort-wide, never per group. With `fit_on`, groupers or features force buffered — two-pass, never merged.
- With `fit` the score is row-local: one pass, streams beside groupers and features, and merges (shards, chain stages) with the same scores. A fitted distance metric is the exception: it streams, but never merges (shards run serially; a chain stage refuses it).
- A compose fit that failed on its data: every score null + `PULSE_FIT_UNAVAILABLE`.
- A singular fit or N* ≤ p: every score null + a `PULSE_MATRIX_SINGULAR` / `PULSE_MATRIX_INSUFFICIENT_N` warning.

## A matrix from a fit

`params.fit` on a correlation or covariance matrix slot emits the matrix from a bound vector fit, reading no row; matched by label, its basis governs. Refused beside `missing` / `max_drop_share` (`fit_and_missing`), groups (`fit_grouped`) or a rank method (`fit_unsupported`); a regression's name is `PULSE_MODEL_KIND_MISMATCH`. A fit that failed on its data fails that slot alone (its code, `details.fit`).

## References

`reference` (required on the similarity and distance scores) is what each row is compared with, matched BY LABEL, never position; wrong labels or length are `PULSE_VECTOR_REFERENCE_MISMATCH` at predict.

| Form | Compares each row with |
|---|---|
| `[…]` | values in member order |
| `{label: value}` | values by label |
| `{table, key}` | one registered profile ([`vector-metrics`](vector-metrics.md)) |
| `{table, key_field}` | its own profile, keyed by a categorical or `u4`…`u64` field in exact base-10 form (`u64` above 2^53 too) |
| `{centroid: {filter?, by?}}` | the weighted per-member mean of the rows `filter` admits (= `AGG_VEC_MEAN`); `by` = one centroid per exact key, each row against its own |
| `{population: name}` | a bound vector fit's mean ([`compose-fits`](compose-fits.md)); a distance's population need not be its `fit` |
| `{record: {cohort?, keys}}` | one stored row, point-looked-up through its cohort's index (the request's own if omitted: [`cohort-sidecar-index`](cohort-sidecar-index.md)), read by member column (null = a missing element) |

- `min_valid` (default 1) — the fewest complete pairs; below it the row is null. A distance's sums rescale by p / p_observed.
- A null or profile-less `key_field` / `by` row is null and warns `PULSE_VECTOR_REFERENCE_MISSING` (`n_key_null`, `n_key_unmatched`); a domain violation (`hellinger`, `aitchison`) nulls the row and warns `PULSE_VECTOR_METRIC_DOMAIN`. Once per slot; strict does not escalate. Components count them: `n_reference_missing`, `n_domain_invalid`.
- Unbound population → `PULSE_VECTOR_REFERENCE_UNKNOWN`; a failed compose fit (population or `fit`) nulls every row + `PULSE_FIT_UNAVAILABLE`.
- A record member its cohort lacks → `PULSE_VECTOR_REFERENCE_MISMATCH` (`record_members_absent`); lookup refusals (`PULSE_INDEX_*`, `PULSE_LOOKUP_*`) refuse the run, never at predict.

Execution:

- Row-local (no centroid): streams and fuses, the similarity ignores the weight, and it merges — shards / parallel decode fold the per-run tallies (counts and warnings once, cohort-wide); a chain stage admits it.
- A centroid is a pre-pass for every metric: two-pass, weighted, cohort-wide (grouped runs buffered; it keeps a `fit` slot two-pass); it runs serially and a chain refuses it.

## See

- [`multivariate-design`](multivariate-design.md), [`vector-metrics`](vector-metrics.md), [`multivariate-outputs`](multivariate-outputs.md), [`compose-fits`](compose-fits.md), [`cohort-sidecar-index`](cohort-sidecar-index.md)
