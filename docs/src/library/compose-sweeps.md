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

```json
{
  "sweep": {
    "axes": [
      {"name": "region", "values": ["north", "south"]},
      {"name": "cut",    "values": [10, 20, 30]}
    ],
    "mode": "grid",
    "label": "{{region}}_{{cut}}",
    "request": {
      "cohort": {"filename": "sales.pulse"},
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

```json
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
form.

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
bound, `min >= max`, unknown `scale`, log with `min <= 0`) is
`PULSE_SWEEP_RANGE_INVALID`, and a missing or sub-1 `samples` is
`PULSE_SWEEP_SAMPLES_INVALID`. An item `for_each` walks values only.

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
a feature's `params.fit_on`, in the request or the sweep overlays — is
`PULSE_SWEEP_EXPR_SPLICE`, refused before any slot is substituted, by
`PredictCompose` and the runtime alike (details `field`, `placeholder`,
`slot`). Spliced text could rewrite the expression's logic. Carry the
value through the slot request's `params` instead and read it in the
expression:

```jsonc
"sweep": {
  "axes": [{"name": "w", "values": [2, 3]}],
  "request": {
    "params": {"w": {"$var": "w"}},
    "filterers": [{"type": "FILTER_EXPRESSION", "expression": "wave >= params.w"}]
  }
}
```

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
`expanded_count` (sweep slots only; `samples` under random / lhs), the
effective `rng_seed` of a sampling sweep, and `labels`. A malformed
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
`from_sweep_regression_unknown`, `from_sweep_signature_varies` or
`from_sweep_cycle`, judged by predict and the runtime alike before
anything runs.

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
binding runs and answers exactly as before. Skill: `compose-sweep-bind`.

The winner's axis values are readable in the same batch:
`sweep_axis("<fit>", "<axis>")` in an explicit slot's formula or filter
and in an `optimize` expression returns the rank-1 slot's value of that
axis (a number as a float, a string or bool `values` entry as is) — the
decay a steady-state multiplier such as
`(1 - sweep_axis("best", "tv") ^ 8) / (1 - sweep_axis("best", "tv"))`
needs, which no coefficient carries. Its refusals mirror `coef()` and
predict raises them too: an unbound name is `PULSE_MODEL_UNKNOWN`; a fit
not bound from the sweep is `PULSE_MODEL_TERM_UNKNOWN` with
`details.reason: "not_sweep_bound"`, an axis the sweep lacks the same
code with `reason: "axis_unknown"` and the sorted `details.axes`. A
failed bound fit (no winner) nulls a formula with
`PULSE_FIT_UNAVAILABLE` and refuses a filter with the fit's own code.
`sweep_axis` is reserved: an embedder `ExprFunctions` entry of that name
is `PULSE_EXTENSION_NAME_RESERVED`.

## Echo and return shaping

With `Options.EchoRequest` / `--echo-request` the echo carries the
expanded slots the service ran (`ComposedResponse.NormalizedRequest`,
not on the wire). The MCP `pulse_compose` default `return` of `standard`
also fills a sweep body that sets no `return`; an authored one is never
overwritten. The library default is unchanged.

See also: [Parallel Compose](parallel-compose.md), the
[payload schema](../contract/payload-schema.md#compose-sweep).
