```yaml
name: tool-rake
kind: tool
description: Compute survey weights (rim or cell weighting to inline target shares) and write them as a weight column of a new cohort.
type: reference
applies_to: mcp
```

## When to use

CALL TO COMPUTE SURVEY WEIGHTS THAT MAKE A SAMPLE MATCH KNOWN POPULATION SHARES.
A weight must be a schema field, so `pulse_rake` writes it to a new cohort; point the next request at `out` with `{"weight": {"field": "rake_weight"}}`.

## Input

- `cohort`, `out` (string, required): source and NEW `.pulse` path, relative to the data dir.
- `margins` (array, required): `{field, targets: {category: share}}` (rim) or `{fields: [..], cells: [{values: [..], target}]}` (cell weighting); `targets_as` `shares` (sum 1 or 100) or `counts`.
- `weight_field` (default `rake_weight`), `by` (rake each group, e.g. a wave), `base_weight`, `missing` (`refuse` / `base`), `min_weight` / `max_weight` (relative to the mean), `max_iter`, `tol`, `total`, `overwrite`.

## Output

`target`, `records`, `weight_field`, `iterations`, `converged`, `residual`, `margins` (observed / target / achieved share per category), `groups` (under `by`), `efficiency` (`n_eff`, `deff_kish`, `efficiency_pct`, weight range, `n_capped`), `warnings`, `invalidated_sidecars`.

## Gotchas

- Every category a record holds needs a target (`PULSE_RAKE_UNTARGETED_CATEGORY`); a target the field cannot hold is `PULSE_RAKE_UNKNOWN_CATEGORY`; a malformed request is `PULSE_RAKE_INVALID` (`details.reason`).
- Not converging still writes, with `PULSE_RAKE_NOT_CONVERGED`; caps trade fit for efficiency.
- The weights are PROBABILITY weights. `deff_kish` is the unequal-weighting design effect only.
- Existing `out` without `overwrite` → `SERVICE_VALIDATION`; every refusal happens before anything is written. An SPSS metadata sidecar is carried onto `out`; other source sidecars are not.

## See

- [`survey-weights`](survey-weights.md) — targets, `by`, caps, convergence and the efficiency report.
- [`weighting`](weighting.md) — using the column as a request weight.
