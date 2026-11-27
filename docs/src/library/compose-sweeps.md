# Compose Sweeps

**Audience:** Go embedders, CLI users and agents who run one question
over a grid of parameters.

`ComposedRequest.Sweep` (`types.SweepSpec`) is one request body plus
axes of values. Before any slot runs, Pulse expands it into ordinary
Compose slots appended after the explicit `Requests`. Each expanded slot
then runs exactly like a hand-written one: same limits, same labels,
same overlay fold, serial or parallel. The same payload works through
`Compose`, `ComposeParallel`, `PredictCompose`, `pulse api compose`,
`pulse api predict-compose`, `pulse_compose` and `pulse_predict`
(`composed`). It is gated by `capability:compose_sweep`.

```json cohort=experiment
{
  "sweep": {
    "axes": [
      {"name": "region", "values": ["north", "south"]},
      {"name": "cut",    "values": [10, 20, 30]}
    ],
    "mode": "grid",
    "label": "{{region}}_{{cut}}",
    "request": {
      "cohort": {"filename": "experiment.pulse"},
      "filterers": [{"type": "FILTER_INCLUDE", "field": "region", "values": [{"$var": "region"}]}],
      "aggregations": [{"type": "AGG_SUM", "field": "revenue", "label": "rev_{{cut}}"}]
    }
  }
}
```

## Axes and modes

An axis has a unique identifier `name` and a non-empty list of scalar
`values` (string, number or boolean; numbers keep the digits as
written). `mode` `grid` (default) is the Cartesian product, first axis
slowest, so a 2-value and a 3-value axis give 6 slots. `zip` walks the
axes in lockstep and every axis must be the same length.

### Sampling modes: `random` and `lhs`

To search a continuous space without enumerating a grid, set `mode` to
`random` or `lhs`, a `samples` count, and give every axis a `range`
instead of `values`:

```json norun elided-request
{"sweep": {"mode": "lhs", "samples": 64, "rng_seed": 7,
  "axes": [{"name": "decay",  "range": {"min": 0, "max": 0.9}},
           {"name": "lambda", "range": {"min": 0.001, "max": 10, "scale": "log"}}],
  "request": {"...": "...", "params": {"decay": {"$var": "decay"}, "lambda": {"$var": "lambda"}}}}}
```

`random` draws each axis value independently and uniformly from its
range (`scale: log` draws uniformly in log space, for a parameter that
spans decades; it needs `min > 0`). `lhs` is a Latin hypercube: each
axis is cut into `samples` equal-probability strata and every stratum is
drawn exactly once, so even a small `samples` covers every axis evenly.
Draws stay inside `[min, max]` and render in their shortest round-trip
form. `integer: true` on a range draws whole numbers only (a lag, a
window width): the draw spreads over `[ceil(min) − ½, floor(max) + ½]`
in the axis's scale and rounds, so every integer in `[min, max]` is
reachable and renders in integer form (`3`, never `3.0`); under `lhs`
each stratum maps to a run of integers. An `integer` range must hold an
integer.

The draws are a pure function of the spec and the seed, so predict lists
the same slots the runtime runs and a run replays exactly. A non-zero
`rng_seed` is used as given; `0` (or absent) derives a seed from the
whole `sweep` block's wire form with only `rng_seed` excluded
(`axes`, `mode`, `samples`, `label` and `rank` included), its raw `request` and
`overlays` bodies taken in canonical form (object keys sorted at every
depth, whitespace dropped, numbers and strings kept as written), so two
bodies differing only in key order or spacing draw identically while
any value change draws anew. The effective seed is echoed
as `ComposedResponse.SweepSeed` (`sweep_seed`) and on predict's summary
as `rng_seed`; pin it to replay a zero-seed run.

A zero seed derives from the spec alone, never from the data: the same
request draws the same sample points whatever cohort it runs on. Set an
explicit `rng_seed` to draw a different set.

The sampling modes are gated by `capability:compose_sweep_sampling`
(which needs `capability:compose_sweep`). An instance that hides it
refuses `samples`, `rng_seed` or an axis `range` as
`PULSE_REQUEST_UNKNOWN_FIELD`, and `random` / `lhs` as an unknown mode
(`PULSE_SWEEP_INVALID`, reason `mode_unknown`, `valid` grid / zip), the
same way predict does.

An axis never mixes the two styles: a `range` under `grid` / `zip`, a
`values` list under `random` / `lhs`, or `samples` / `rng_seed` under
`grid` / `zip` is `PULSE_SWEEP_MODE_MISMATCH`. A bad range (missing
bound, `min >= max`, unknown `scale`, log with `min <= 0`, an `integer`
range with no integer in it — reason `integer_range_empty`) is
`PULSE_SWEEP_RANGE_INVALID`, and a missing or sub-1 `samples` is
`PULSE_SWEEP_SAMPLES_INVALID`. An item `for_each` walks values only.

### Adaptive modes: `de` and `bo`

A grid or a sample fixes every point before any slot runs. An adaptive
mode spends a slot `budget` in rounds and chooses each round's points
from the ranked values of the rounds before, so most of the budget lands
near the best region. Use it to find an optimum of one ranked number;
use `lhs` / `random` to map the whole space evenly.

```json norun elided-request
{"sweep": {"mode": "bo", "budget": 40, "bo": {"n_init": 8, "batch": 2},
  "axes": [{"name": "decay",  "range": {"min": 0, "max": 0.95}},
           {"name": "lambda", "range": {"min": 0.001, "max": 10, "scale": "log"}},
           {"name": "form",   "values": ["linear", "log"]}],
  "request": {"...": "..."},
  "rank": {"by": "regressions.mmm.holdout.rmse"}}}
```

`budget` (at least 1) is the sweep's slot count and `rank` its
objective: `rank.by` is minimised under `order: "asc"` and maximised
under `desc`. Both are required (`PULSE_SWEEP_INVALID`, reasons
`adaptive_requires_budget`, `budget_invalid`, `adaptive_requires_rank`).
An axis is a `range` (searched continuously, `scale` and `integer`
honoured) or a `values` list (searched by value index). `samples`
(`samples_under_adaptive`) and `sweep.overlays`
(`overlays_under_adaptive`) are refused under an adaptive mode, and
`budget` under any other mode (`budget_without_adaptive`), all as
`PULSE_SWEEP_MODE_MISMATCH`. The search is seeded like a sampling sweep
(`rng_seed`, or a seed derived from the spec, echoed as `sweep_seed`),
so a run replays bit for bit on any architecture, serial or parallel.
Every list value is rendered once at expansion, so one whose body does
not decode is refused before anything runs, by predict and the run
alike. `MaxComposeSlots` bounds the budget before anything renders.

`mode: "de"` is differential evolution (rand/1/bin): round 0 is a
Latin-hypercube population, each later round one generation, and a
`budget` remainder a final partial generation; a trial replaces its
target when its value is no worse. The optional `de` block sets
`population` (4 to `budget`; default 10 × the axis count, capped at
`budget`), `f` (in (0, 2], default 0.8) and `cr` (in [0, 1], default
0.9). Out-of-range knobs are `PULSE_SWEEP_INVALID`
(`de_population_invalid`, `de_population_over_budget`, `de_f_invalid`,
`de_cr_invalid`). Prefer it for cheap slots, a larger budget and a
rugged objective.

`mode: "bo"` is Bayesian optimisation: round 0 is an initial design,
and each later round fits a Gaussian-process surrogate (Matérn 5/2)
to every point so far and proposes the points that maximise expected
improvement. A list axis enters the surrogate one-hot. The optional
`bo` block sets `init` (`lhs`, the default, or `random`: how the
initial design is drawn), `n_init` (the initial design's size; default
2 × the axis count, capped at `budget`) and `batch` (points per later
round, default 1; a batch above 1 is filled by constant liar). `n_init`
and `batch` take 1 to `budget`. Out-of-range knobs are
`PULSE_SWEEP_INVALID` (`bo_init_invalid`, `bo_n_init_invalid`,
`bo_n_init_over_budget`, `bo_batch_invalid`, `bo_batch_over_budget`); a
surrogate that cannot be fitted (a non-finite objective) fails the run
with `PULSE_SWEEP_INVALID` reason `bo_surrogate_failed`
(`details.mode`, `round`, `cause`). Prefer it for costly slots and a
small budget. Under `ComposeParallel` a round's slots run concurrently,
so a larger `batch` uses more workers per round.

A `de` block under any mode but `de` is `PULSE_SWEEP_MODE_MISMATCH`
(`de_without_de_mode`), and a `bo` block under any mode but `bo` the
same (`bo_without_bo_mode`).

Every adaptive label is `sample=<slot>_<pattern>` (the authored `label`
pattern, or the default `axis=value` pairs), with `slot` the 0-based
sweep index, so labels stay unique even when a point repeats. A slot
that fails, or whose ranked value is null, is penalised — the search
scores it as the worst value so far and goes on — and leaves the
`ranking`; failures warn `PULSE_SLOT_FAILED` once for the batch
(`details.failed_indices`). Fail-fast `ComposeParallel` aborts at the
failing round and `Strict` fails the batch. A `result()` read may name
an explicit or a round-0 slot; a later round's label is unknown until it
runs, so naming it is `PULSE_RESULT_INVALID` `slot_unknown` in predict
and at run time. `fits[].from_sweep` binds the winner as usual
([Binding the winner as a fit](#binding-the-winner-as-a-fit)).

An adaptive response also carries its round trace. Each `ranking` entry
gains `round` (`RankEntry.Round`, the 0-based round the slot ran in;
round 0 is present, other modes omit it), and
`ComposedResponse.SweepTrace` (`sweep_trace`) lists one
`SweepTraceEntry {round, slots, evaluated, penalised, best, best_so_far,
best_label}` per round, in round order. `slots` is the round's inclusive
`[first, last]` sweep-slot span, `evaluated` the slots it ran and
`penalised` those left unranked (a failed slot's stub, or a null or
missing value at `rank.by`). `best` is the best ranked value among the
round's own slots (by `rank.order`), `best_so_far` the best over rounds
0..`round` and `best_label` that slot's label; ties follow expansion
order. `best` and `best_so_far` are `null` — never 0 — while nothing has
ranked, and `best_label` is then absent. The trace reads the full ranking
before `top`, sits outside the Compose `return` like `ranking` (full
precision), and rides the trailing `--stream` ranking line. Non-adaptive
sweeps are byte-identical to their output without the adaptive rounds. A profiled instance shows `round` and
`sweep_trace` in its payload schema only with
`capability:compose_sweep_adaptive`.

The adaptive modes are gated by `capability:compose_sweep_adaptive`
(which needs the sweep and the sampling modes); an instance that hides
it refuses `budget`, `de` and `bo` as unknown fields and an adaptive
mode as an unknown mode, the same way predict does.

## Placeholders

`{{axis}}` inside a string splices the value as text; `{"$var":
"axis"}` as a whole value keeps its JSON type. A `$when` guard may name
an axis. Every axis must be referenced by the body, an authored label
pattern or the overlays, and a placeholder that names no axis is
refused. This is a purpose-built substitution, not an expression
language and not the stored-template surface: it runs before the body
is decoded.

A sweep never splices an axis value's text into an expression. A
`{{axis}}` inside an expression string — a filterer's or attribute's
`expression`, a regression's `holdout`, an overlay's `params.formula`,
a feature's `params.fit_on`, an embedder extension's expression-capable
param, in the request or the sweep overlays — is
`PULSE_SWEEP_EXPR_SPLICE`, refused before any slot is substituted, by
`PredictCompose` and the runtime alike (details `field`, `placeholder`,
`slot`). Spliced text could rewrite the expression's logic. Carry the
value through the slot request's `params` instead and read it in the
expression. An axis `target` (`params.<name>`, gated by
`capability:params`) writes each slot's axis value into its
`params.<name>` — declaring it, or overriding the body's own value and
the batch's top-level one — so the body needs no placeholder:

```jsonc
"sweep": {
  "axes": [{"name": "w", "values": [2, 3], "target": "params.w"}],
  "request": {
    "params": {"w": 1},
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave >= params.w"}]
  }
}
```

The `{"$var"}` route — `"params": {"w": {"$var": "w"}}` with an
untargeted axis — runs the same slots. The two never
mix on one value: a `{"$var"}` marker naming a targeted axis, or one
standing as the body's own value at a targeted param, is
`PULSE_SWEEP_INVALID` reason `axis_target_conflict`. A target must be
`params.<identifier>` (`target_invalid`), one per param
(`target_duplicate`), and only a sweep axis takes one — an item
`for_each` axis is refused `PULSE_FOR_EACH_INVALID` reason
`target_unsupported`. Predict refuses each identically and echoes an
axis's `target` on its `sweep.axes` summary.

A `{"$var": "axis"}` marker may still supply a whole expression, the
axis values then being the complete expression strings.

The substituted body decodes **strictly** into a `Request`, exactly as a
plain compose slot does: an unknown key fails with
`PULSE_REQUEST_UNKNOWN_FIELD` (`details.path`, a nearest-key suggestion,
plus the sweep `slot` and its axis `values`), and a wrong-typed value
fails with `PULSE_SWEEP_INVALID`, reason `request_decode`.

## Labels

Without `label`, a slot is named by its `axis=value` pairs joined with
`_` — under `random` / `lhs` prefixed `sample=<index>_`, so every drawn
slot is unique. An authored pattern may use any axis. The body must not set its own
`label`, and a pattern that renders empty is refused. Labels stay unique
across explicit and sweep slots (`PULSE_COMPOSE_LABEL_COLLISION`).
Compose overlays name sweep slots by these labels.

## Overlays

`sweep.overlays` is an array of compose overlay specs with
placeholders. It is substituted per expanded slot and appended after the
request's own `Overlays`, in expansion order.

## Limits and predict

`MaxComposeSlots` counts the expanded total (explicit plus sweep); an
over-limit batch is `PULSE_LIMIT_EXCEEDED` before any slot runs
([Tuning Limits](tuning-limits.md)). `PredictCompose` expands too and
applies the same limit, slot-gate and label checks as the runtime. It
adds a `sweep` summary to the result: `axes` (name and value count —
the `samples` count and the `range` for a sampled axis), `mode`,
`expanded_count` (sweep slots only; `samples` under random / lhs,
`budget` under an adaptive mode), the effective `rng_seed` of a sampling
or adaptive sweep, and `labels`. An adaptive sweep's summary adds
`adaptive: true`, reports each range axis's `count` as the budget, and
lists round 0's real labels followed by `null` for every later slot,
whose points are chosen only as the run goes. A malformed
sweep is `PULSE_SWEEP_INVALID` with `details.field` and
`details.reason`.

## Ranking

`sweep.rank` is `{by, order, top}`. `by` is a dot path into each sweep
slot's unshaped response (an object key, or on a list the element whose
`name` or `label` equals the segment, never an index; for example
`components.aggregations.rev.n`); `order` is `asc` (default) or `desc`; `top`
keeps the first N. `ComposedResponse.Ranking` lists
`{label, value, rank, slot, axes}` in rank order: `slot` is the entry's
0-based index among the sweep slots (its response is
`Responses[explicit+slot]`), `axes` maps every axis name to the value
that slot ran with, typed as the axis declared it (numbers stay numbers,
strings and booleans are preserved). Read a winner's axis values from
`Ranking[0].Axes`, never by parsing its label. Explicit slots are never
ranked. A null value, or a path
missing in one slot only, excludes that slot with a `PULSE_SWEEP_RANK_PATH`
warning on its own response; a non-number, ambiguous or unresolved path
refuses the batch with the same code (`details.reason` `syntax`, `missing`,
`not_number`, `ambiguous`). A ranked slot always computes the ranked part
even when its `return` excludes it, and `ranking` is never trimmed or
rounded by the Compose `return`. Under `--stream` one trailing
`{"ranking": [...]}` line follows the rows.

`rank.profile: true` adds `ComposedResponse.RankingProfile`
(`ranking_profile`): per axis, one `{value, best, label, n}` entry per
axis value some ranked slot ran with. `best` is the best ranked value
among the slots with that value (the minimum under `asc`, the maximum
under `desc`, ties in expansion order), `label` the slot that achieved
it and `n` the ranked slots behind it. Only ranked slots count — a
failed slot, or one left out of the ranking, is not an evaluated point
— so a value nothing ranked has no entry and a `random` / `lhs` profile
is as sparse as its sample. A list axis lists its values in declared
order. A range axis (sampling or adaptive) is binned instead:
`rank.profile_bins` (default 10, at least 1, only beside `profile`)
equal-width bins over `[min, max]`, equal log-width under `scale: log`,
one entry per bin some ranked slot fell into, ascending, adding the
bin's `lo` / `hi` edges (`[lo, hi)`, the last bin closed at `max`);
`value` is the draw of the bin's best slot. The profile reads the whole
ranking before `top`, sits outside the Compose `return` like `ranking`,
rides the same `--stream` ranking line, and never changes a sampling
sweep's derived seed (nor does `profile_bins`). Without `profile` the
response is byte-identical.

A figure serialised with `omitempty` disappears from the wire at exactly
0. Rank reads such a figure off the typed response instead, so a fit
whose `residual_std_err`, `r2`, `adj_r2`, `deviance`, `null_deviance` or
`pseudo_r2` is exactly 0 ranks as 0 (first, ascending) — but only when
the fit's regression type computes that figure. A figure the type does
not compute (`residual_std_err` on a `REG_GLM` fit), any other absent
figure and a nil entry still read as missing. The wire is unchanged.

### Binding the winner as a fit

A `fits` entry may carry `from_sweep: {"regression": "<name>"}` instead
of a `request` (`capability:compose_sweep_bind`): it binds the rank-1
slot's regression of that `name`, refitted on that slot's rows, as an
ordinary named fit the explicit slots and `optimize` blocks read
(`coef()`, `ATTR_REG_PREDICT`). It needs `sweep.rank`; the regression's
type, target and predictors must not depend on an axis (predict reads
the fit's signature from the sweep body); and no sweep slot may read the
fit. A malformed binding is `PULSE_FIT_INVALID` with `details.reason`
`from_sweep_with_request`, `from_sweep_no_sweep`, `from_sweep_no_rank`,
`from_sweep_regression_unknown`, `from_sweep_signature_varies`,
`from_sweep_cycle` or `from_sweep_window_cycle` (a sweep slot's window
reads the fit in a scalar param such as a `WIN_DISTRIBUTED_LAG` decay),
judged by predict and the runtime alike before anything runs. An
explicit slot's window may read the winner —
`"decay": "sweep_axis(\"best\", \"decay_tv\")"` — and runs after the
bind.

A batch with a binding runs sweep-first: the authored fits, then the
sweep slots (once — their responses are the batch's) and the rank, then
the bound fit refitted over `ranking[0]`'s slot exactly as an authored
fit of that body would be (same figures, same `save` file), then
`optimize` and the explicit slots. Responses keep index order. The rank
reads each sweep slot after its own multiplicity families fold; a
`compose`-family adjusted figure is not ready yet, so ranking by one is
refused. When no slot is ranked, the fit binds failed: its entry carries
`PULSE_SWEEP_RANK_PATH` (`reason: "empty"`), a `PULSE_SLOT_FAILED`
warning names it under `details.fit`, slots scoring it read null
(`PULSE_FIT_UNAVAILABLE`), and `Strict` fails the batch. A batch with no
binding is unaffected. Skill: `skills/compose-sweep-bind.md`.

The winner's axis values are readable in the same batch:
`sweep_axis("<fit>", "<axis>")` in an explicit slot's formula or filter
and in an `optimize` expression returns the rank-1 slot's value of that
axis (a number as a float, a string or bool `values` entry as is) — the
decay a hand-written steady-state multiplier such as
`(1 - sweep_axis("best", "tv") ^ 8) / (1 - sweep_axis("best", "tv"))`
needs, which no coefficient carries (`response()` reads the bound fit's
adstock record instead: see [Media-Mix Models](mmm.md)). Its refusals mirror `coef()` and
predict raises them too: an unbound name is `PULSE_MODEL_UNKNOWN`; a fit
not bound from the sweep is `PULSE_MODEL_TERM_UNKNOWN` with
`details.reason: "not_sweep_bound"`, an axis the sweep lacks the same
code with `reason: "axis_unknown"` and the sorted `details.axes`. A
failed bound fit (no winner) nulls a formula with
`PULSE_FIT_UNAVAILABLE` and refuses a filter with the fit's own code.
`sweep_axis` is reserved: an embedder `ExprFunctions` entry of that name
is `PULSE_EXTENSION_NAME_RESERVED`.

A bound fit that `save`s keeps its winner: the model file lists feature
`sweep_axes`, the winning slot's axis values as raw JSON (a number,
string or bool reads back exactly). Any later request loading it with
`models: [{name, path}]` — a slot's formula, filter, window param or
`AGG_CONSTANT` `expr`, an `optimize` block, a `Materialize` run — reads
those values through `sweep_axis("<name>", "<axis>")`, as the batch that
fitted it did, and predict judges an unknown axis statically. A model
file without `sweep_axes` (any fit not bound from a sweep) stays
`not_sweep_bound`.

## Echo and return shaping

With `Options.EchoRequest` / `--echo-request` the echo carries the
expanded slots the service ran (`ComposedResponse.NormalizedRequest`,
not on the wire). The MCP `pulse_compose` default `return` of `standard`
also fills a sweep body that sets no `return`; an authored one is never
overwritten. Library calls follow the instance default
([Response Shaping](response-shaping.md#defaults-and-precedence)).

See also: [Parallel Compose](parallel-compose.md), the
[payload schema](../contract/payload-schema.md#compose-sweep).
