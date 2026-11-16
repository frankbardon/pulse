```yaml
name: compose-sweep-sampling
description: The compose sweep's seeded sampling modes — random and lhs (Latin hypercube) draw `samples` slots from continuous axis ranges instead of listing values. When to sample rather than enumerate, the range and scale rules, how the seed is derived, echoed and replayed, the slot limit, predict and the refusals.
type: guide
kind: design
applies_to: compose, predict
covers: [SweepSpec, SweepRange, integer range, profile_bins, random sweep, Latin hypercube, lhs, samples, rng_seed, sweep_seed, hyperparameter search]
requires: [capability:compose_sweep, capability:compose_sweep_sampling]
```

# Compose sweep sampling

A grid lists every value; its size multiplies per axis (30 values on 4 axes is 810,000 slots). A **sampling** sweep draws a fixed number of slots from continuous ranges instead, so the cost is `samples` whatever the axis count. Use it for continuous parameters (an adstock decay, a ridge penalty, a window width) or more than two axes; use a grid for a few discrete values ([`compose-sweeps`](compose-sweeps.md)).

```jsonc
{
  "sweep": {
    "mode": "lhs",
    "samples": 200,
    "axes": [
      {"name": "decay",  "range": {"min": 0.0, "max": 0.95}},
      {"name": "lambda", "range": {"min": 0.001, "max": 10, "scale": "log"}},
      {"name": "lag",    "range": {"min": 1, "max": 12, "integer": true}}
    ],
    "request": {"...": "the body, reading {\"$var\": \"decay\"} / {{lambda}}"},
    "rank": {"by": "regressions.mmm.holdout.rmse"}
  }
}
```

## Modes

- `random`: every slot draws every axis independently and uniformly.
- `lhs`: each axis is cut into `samples` equal-probability strata and every stratum is drawn exactly once, paired across axes by a seeded permutation. Even coverage of every axis at any sample count; prefer it to `random`.
- `samples` (>= 1) is required. Every axis carries a `range` `{min, max, scale}` instead of `values`: finite bounds, `min < max`, draws inside `[min, max]`. `scale` `linear` (default) or `log` (log-uniform, for a parameter spanning decades; needs `min > 0`). `integer: true` draws whole numbers (a lag, a window width): every integer in `[min, max]` reachable, rendered `3` not `3.0`; under lhs each stratum maps to a run of integers.
- Draws are numbers in shortest round-trip form; placeholders substitute them as in a grid. Default labels are `sample=<i>_<axis>=<value>` (`i` from 0), unique by construction. A ranked slot's draws come back as numbers in its `ranking` entry's `axes` (its index in `slot`): read them there, not off the label. Under `rank.profile` each range axis is binned: `rank.profile_bins` (default 10) equal-width bins over `[min, max]`, equal log-width under `log`. One entry per bin a ranked slot fell into, ascending, with `lo` / `hi` edges, `best` and `label` the bin's best slot, `value` its draw and `n` the ranked slots in the bin; empty bins are absent.

To FIND an optimum rather than map the space, an adaptive mode (`de`, `bo`) spends a `budget` in rounds steered by the ranked values ([`compose-sweep-adaptive`](compose-sweep-adaptive.md)).

## Seed

- The draws are a pure function of the spec and the seed, identical across architectures, serial and parallel.
- `rng_seed` absent or 0: the seed is derived from the whole `sweep` block except `rng_seed` — `axes`, `mode`, `samples`, `label`, `request`, `overlays` and `rank` (a changed `rank.top` draws anew; `rank.profile` / `profile_bins` never do) — never from the data, the clock or the batch's other slots. The same request draws the same points on any cohort.
- The derivation reads the `request` and `overlays` bodies with object keys sorted at every depth and whitespace dropped; numbers and strings keep their text. Bodies differing only in key order or spacing draw identically; any value change (`0.1` vs `0.10` included) draws anew.
- The effective seed comes back as `sweep_seed` on the response (outside the compose `return`; `--stream` writes one `{"sweep_seed": N}` line) and as `sweep.rng_seed` in predict. Pin it as `rng_seed` to replay a run exactly; set another value to draw a fresh set.

## Limits and predict

- `samples` counts toward `MaxComposeSlots` with the explicit slots (default 1000; raise `Limits.MaxComposeSlots`, CLI root `--limit max_compose_slots=N`, for a large search). Over the limit is `PULSE_LIMIT_EXCEEDED` before anything runs.
- Predict expands exactly: `expanded_count` = `samples`, each axis reports `count` and its `range`, and the labels match the run.

## Refusals

- `PULSE_SWEEP_MODE_MISMATCH` (`details.reason`): a `values` axis under random / lhs, a `range` axis under grid / zip, an axis with both, `samples` or `rng_seed` under grid / zip.
- `PULSE_SWEEP_RANGE_INVALID`: missing range or bound, non-finite bound, `min >= max`, unknown scale, `log` with `min <= 0`, an `integer` range holding no integer (`integer_range_empty`).
- `PULSE_SWEEP_INVALID`: `profile_bins` below 1 or without `profile: true`.
- `PULSE_SWEEP_SAMPLES_INVALID`: `samples` missing or below 1.
- An item `for_each` never samples: random / lhs or a `range` there is `PULSE_FOR_EACH_INVALID`.

## Decay search pattern

Replace a decay grid with an lhs over `[0, 0.95]` and rank on the holdout error. The best slots cluster around the true decay; read the top few, not only the first, and check the winner is stable under a second `rng_seed`. `fits[].from_sweep` binds the winner as a fit and `sweep_axis()` reads its draws, so the search, the fit and an `optimize` are one call ([`compose-sweep-bind`](compose-sweep-bind.md)); media mix: [`mmm-workflow`](mmm-workflow.md).

## See

[`compose-sweeps`](compose-sweeps.md) (axes, placeholders, labels, rank, limits) · [`compose-sweep-adaptive`](compose-sweep-adaptive.md) (de / bo search) · [`compose-requests`](compose-requests.md) (slots and parallel runs) · [`regression-model-options`](regression-model-options.md) (the media-mix decay pattern).
