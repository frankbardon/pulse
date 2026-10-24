---
name: sem-fits
description: SEM fits in Compose — a fits entry holding one MAT_SEM matrix fits the structural equation model once and binds its parameters by name; ATTR_SEM_SCORE params.fit scores any slot's rows from it, row-local and bit-equal to the inline fit. The fit request, the response shape, scoring rules, refusals and failures.
kind: design
type: guide
applies_to: compose, predict
covers: [SEM, SEM fit, latent score, factor score, fits, compose, MAT_SEM, ATTR_SEM_SCORE]
requires: [MAT_SEM, ATTR_SEM_SCORE, capability:compose_fits]
---

# SEM fits

A **SEM fit** is a Compose `fits` entry (`compose-fits`) whose request holds exactly one `MAT_SEM` matrix and no regression. It fits the model ONCE, before any slot, and binds its parameters under the entry's `name`. Use it when several slots score the same latents — waves, segments, a train / holdout split — or when the fit rows differ from the scored rows.

```jsonc
{
  "fits": [{"name": "brand", "request": {
    "cohort": {"filename": "survey.pulse"},
    "matrices": [{"type": "MAT_SEM", "vector": "ratings",
                  "params": {"model": "quality =~ q1 + q2 + q3\nvalue =~ v1 + v2 + v3", "group": "wave",
                             "group_equal": ["loadings", "intercepts"]}}]}}],
  "requests": [{"label": "scored", "cohort": {"filename": "survey.pulse"},
    "attributes": [{"type": "ATTR_SEM_SCORE", "label": "value_score", "vector": "ratings",
                    "params": {"fit": "brand", "latent": "value"}}],
    "groups": [{"type": "<category grouper>", "field": "wave"}],
    "aggregations": [{"type": "<mean>", "field": "value_score"}]}]
}
```

## The fit

- Every `MAT_SEM` knob applies (`op-mat-sem`, `sem-modeling`): the model, `meanstructure`, `group` / `group_equal` / `group_partial`, the solver settings. Its own cohort, filters and weight decide the fit rows.
- The response's `fits.<name>` is `{"sem": {...}}`: `operator`, `model`, member `labels` / `members`, the model's `observed` / `inner` / `latents` orders, `estimator`, `conventions`, the knobs, `basis`, `n`, `sum_weights`, `dof`, `n_params`, the solver's `converged` / `status` / `iterations`, and `groups` — per level its `n`, `sum_weights` and the matrices `lambda` (observed × inner), `beta`, `psi`, `theta`, `nu`, `alpha`, `sample_mean` at full precision. The fit's own estimates table is not repeated; run `MAT_SEM` in a slot to read it.
- A `save` on a SEM fit is refused (`PULSE_MODEL_SAVE_INVALID`, `reason: sem_unsupported`).

## Scoring from it

`ATTR_SEM_SCORE` with `params.fit` takes only `latent`, `method`, `labels` and `missing`; any model knob or `fit_on` beside it is refused (`fit_and_model`, `fit_and_fit_on`). The slot:

- aligns its members to the fit's labels BY LABEL (`PULSE_VECTOR_REFERENCE_MISMATCH` when they differ);
- reads each row's group from the fit's `group` field, matched by level LABEL, so a fit taken on one cohort scores any cohort holding the members and the field; a level the fit has no parameters for scores null with `PULSE_SEM_SCORE_LEVEL_UNSEEN`;
- scores every row to the bit of the inline fit with the same knobs over the same fit rows;
- is row-local: it streams beside groupers and features, fuses into a crosstab, and merges across shards and chain stages (the inline form is two-pass and buffers there).

The inline fit's warnings carry over: too few fit rows or negative degrees of freedom null every score; a solver stop short of its tolerance or the nearest positive-definite substitute scores with a warning.

## Kinds and failures

- One namespace holds regression, vector and SEM fits. A SEM fit named by a vector consumer (a fitted attribute, a matrix `params.fit`) or a regression score, or a vector / regression fit named by `ATTR_SEM_SCORE`, is `PULSE_MODEL_KIND_MISMATCH` (`details.kind`, `expected`); an unbound name is `PULSE_MODEL_UNKNOWN` listing the SEM fits.
- A `MAT_SEM` failing on its data (a thin or single group, a singular covariance, an undefined input) fails the fit alone: its entry is `{"sem": {operator, error}}`, compose `warnings` gain `PULSE_SLOT_FAILED`, and every score from it is null with `PULSE_FIT_UNAVAILABLE`.
- `PredictCompose` lists the fit as `{name, cohort, sem: {operator, labels, members}}` and judges every reference's name, kind and labels as the runtime does.

## See

- `compose-fits` — the `fits` slot, names, failures.
- `op-attr-sem-score` — the scoring attribute.
- `sem-modeling` — conventions, means and multi-group fits.
- `vector-row-scores` — the row-local versus two-pass rule.
