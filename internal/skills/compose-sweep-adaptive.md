---
name: compose-sweep-adaptive
description: The compose sweep's adaptive search modes — de (differential evolution) and bo (Bayesian optimisation) spend a slot `budget` in rounds, each round's points chosen from the ranked values before. When to search rather than sample, the budget and rank rules, the de / bo knobs, labels, failures, predict's budget report and the refusals.
type: guide
kind: design
applies_to: compose, predict
covers: [SweepSpec, SweepDE, SweepBO, SweepBOInit, budget, differential evolution, Bayesian optimisation, adaptive search, hyperparameter search]
requires: [capability:compose_sweep, capability:compose_sweep_sampling, capability:compose_sweep_adaptive]
---

# Compose sweep adaptive search

A grid or an lhs sample fixes every point before any slot runs. An **adaptive** sweep spends a `budget` of slots in rounds and picks each round's points from the ranked values of the rounds before, so most of the budget lands near the best region. Use it to FIND an optimum of one ranked number; lhs / random MAP the space (`ranking_profile`), a grid covers a few discrete values (`compose-sweep-sampling`, `compose-sweeps`).

```jsonc
{
  "sweep": {
    "mode": "bo",
    "budget": 40,
    "bo": {"n_init": 8, "batch": 2},
    "axes": [
      {"name": "decay",  "range": {"min": 0.0, "max": 0.95}},
      {"name": "lambda", "range": {"min": 0.001, "max": 10, "scale": "log"}},
      {"name": "form",   "values": ["linear", "log"]}
    ],
    "request": {"...": "the body, reading {\"$var\": \"decay\"} / {{lambda}} / {{form}}"},
    "rank": {"by": "regressions.mmm.holdout.rmse"}
  }
}
```

## Which mode

- `bo`: costly slots, a small budget (tens), a smooth objective. A Gaussian-process surrogate (Matérn 5/2) is refitted every round and the next points maximise expected improvement.
- `de`: cheap slots, a larger budget, a rugged or multi-modal objective. A population evolves by rand/1/bin, one generation per round.
- Under `ComposeParallel` a round's slots run concurrently: a `de` round is the whole population; a `bo` round is `batch` points (raise `batch` to use the workers; each pick then sees fewer finished points).

## Budget and rank

- `budget` (>= 1, required) is the slot count; it counts toward `MaxComposeSlots` with the explicit slots, refused before anything runs. `rank` is required: `rank.by` is the objective, minimised under `order: "asc"`, maximised under `desc`.
- Axes: a `range` (searched continuously; `scale` and `integer` honoured, the sampling rules) or `values` (searched by value index; `bo` one-hot encodes them). Every list value is rendered once up front, so an undecodable one is refused before anything runs, by predict too.
- Seeded like lhs: `rng_seed`, else a seed derived from the spec; echoed as `sweep_seed`. The same request replays the same search on any architecture, serial or parallel.

## Mode `de`

Round 0 is a Latin-hypercube population; each later round is one generation; a `budget` remainder is a final partial generation. Optional `de {population, f, cr}`: `population` 4..budget (default 10 × axes, capped at budget), `f` in (0, 2] (default 0.8), `cr` in [0, 1] (default 0.9). A trial replaces its target when it is not worse.

## Mode `bo`

Round 0 is the initial design; each later round fits the surrogate to every point so far and proposes the next. Optional `bo {init, n_init, batch}`: `init` `lhs` (default) or `random`; `n_init` (default 2 × axes, capped at budget) and `batch` (default 1) in 1..budget. A batch above 1 is filled by constant liar. A surrogate that cannot be fitted (a non-finite objective) fails the batch at run time: `PULSE_SWEEP_INVALID` `bo_surrogate_failed` `{mode, round, cause}`.

## Labels, failures, results

- Every label is `sample=<slot>_<pattern>` (the authored `label` pattern or the default `axis=value` pairs); `slot` counts from 0 in run order, so labels stay unique even when a point repeats. Read a winner from `ranking[0].axes`.
- A slot that fails, or whose value at `rank.by` is null, is penalised (scored as the worst value so far, left out of `ranking`) and the search goes on; failures warn `PULSE_SLOT_FAILED` once for the batch (`details.failed_indices`).
<!-- feature: capability:compose_sweep_bind -->
- `fits[].from_sweep` binds the winner (`compose-sweep-bind`).
<!-- /feature -->
- `result()` may read an explicit slot or a round-0 slot by label; a later round's label is unknown until it runs, so reading it is `PULSE_RESULT_INVALID` `slot_unknown`, in predict and at run time.

## Round trace

Each `ranking[]` entry carries `round` (0-based, round 0 present). `sweep_trace` is one entry per round: `{round, slots: [first, last], evaluated, penalised, best, best_so_far, best_label}`. `penalised` = the round's unranked slots (failed stubs, a null value at `rank.by`); `best` the round's best ranked value, `best_so_far` the best over rounds 0..round, both `null` (never 0) while nothing ranked, and `best_label` is then absent. Read over the full ranking, before `rank.top`. It sits outside the compose `return` (full precision); `--stream` adds it to the final `{"ranking"}` line (`[]` when empty). Absent under non-adaptive modes.

## Predict

Predict reports `expanded_count` = `budget`, `adaptive: true`, the effective `rng_seed`, each range axis's `count` = budget, and `labels`: round 0's real labels, then `null` for every later slot. Every slot is checked statically, so a refusal predict reports is the run's.

## Refusals

- `PULSE_SWEEP_INVALID` (`details.reason`): `adaptive_requires_budget`, `budget_invalid`, `adaptive_requires_rank`; `de_population_invalid`, `de_population_over_budget`, `de_f_invalid`, `de_cr_invalid`; `bo_init_invalid`, `bo_n_init_invalid`, `bo_n_init_over_budget`, `bo_batch_invalid`, `bo_batch_over_budget`.
- `PULSE_SWEEP_MODE_MISMATCH`: `samples` (`samples_under_adaptive`) or `sweep.overlays` (`overlays_under_adaptive`) under an adaptive mode; `budget` under another mode (`budget_without_adaptive`); `de` without `mode: "de"` (`de_without_de_mode`), `bo` without `mode: "bo"` (`bo_without_bo_mode`); an axis with both `values` and `range`.
- `PULSE_SWEEP_RANGE_INVALID`: the range rules of `compose-sweep-sampling`.

## See

`compose-sweep-sampling` (ranges, seeds, lhs) · `compose-sweeps` (axes, placeholders, rank) · `compose-sweep-bind` (bind the winner)
