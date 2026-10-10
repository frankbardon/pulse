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

## Placeholders

`{{axis}}` inside a string splices the value as text; `{"$var":
"axis"}` as a whole value keeps its JSON type. A `$when` guard may name
an axis. Every axis must be referenced by the body, an authored label
pattern or the overlays, and a placeholder that names no axis is
refused. This is a purpose-built substitution, not an expression
language and not the stored-template surface: it runs before the body
is decoded.

The substituted body decodes **strictly** into a `Request`: an unknown
key or a wrong-typed value fails with `PULSE_SWEEP_INVALID`, reason
`request_decode`, even where a plain compose body would ignore it.

## Labels

Without `label`, a slot is named by its `axis=value` pairs joined with
`_`. An authored pattern may use any axis. The body must not set its own
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
adds a `sweep` summary to the result: `axes` (name and value count),
`mode`, `expanded_count` (sweep slots only) and `labels`. A malformed
sweep is `PULSE_SWEEP_INVALID` with `details.field` and
`details.reason`.

## Ranking

`sweep.rank` is `{by, order, top}`. `by` is a dot path into each sweep
slot's unshaped response (an object key, or on a list the element whose
`name` or `label` equals the segment, never an index; for example
`components.aggregations.rev.n`); `order` is `asc` (default) or `desc`; `top`
keeps the first N. `ComposedResponse.Ranking` lists `{label, value, rank}`
in rank order. Explicit slots are never ranked. A null value, or a path
missing in one slot only, excludes that slot with a `PULSE_SWEEP_RANK_PATH`
warning on its own response; a non-number, ambiguous or unresolved path
refuses the batch with the same code (`details.reason` `syntax`, `missing`,
`not_number`, `ambiguous`). A ranked slot always computes the ranked part
even when its `return` excludes it, and `ranking` is never trimmed or
rounded by the Compose `return`. Under `--stream` one trailing
`{"ranking": [...]}` line follows the rows.

Known gap: a figure serialised with `omitempty` disappears at exactly 0
(for example `residual_std_err`), so that slot reads as missing and is
excluded rather than ranked first (TODO #300).

## Echo and return shaping

With `Options.EchoRequest` / `--echo-request` the echo carries the
expanded slots the service ran (`ComposedResponse.NormalizedRequest`,
not on the wire). The MCP `pulse_compose` default `return` of `standard`
also fills a sweep body that sets no `return`; an authored one is never
overwritten. The library default is unchanged.

See also: [Parallel Compose](parallel-compose.md), the
[payload schema](../contract/payload-schema.md#compose-sweep).
