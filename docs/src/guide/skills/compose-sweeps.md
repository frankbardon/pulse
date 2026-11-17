```yaml
name: compose-sweeps
description: ComposedRequest.sweep — one request body with axis placeholders expands into many ordinary Compose slots (grid or zip), with a label pattern, per-slot overlays, limit counting and predict parity.
type: guide
kind: design
applies_to: compose, predict
covers: [ComposedRequest, SweepSpec, pulse_compose]
requires: [capability:compose_sweep]
```

# Compose sweeps

A **sweep** is one request body plus axes of values. Before any slot runs, Pulse expands it into ordinary Compose slots after the explicit `requests`, each run like a hand-written one ([`compose-requests`](compose-requests.md)). Use it when the same question repeats over a parameter grid; write plain `requests` when the slots differ in shape.

```jsonc
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
      "filterers": [{"type": "<value filter>", "field": "region", "values": [{"$var": "region"}]}],
      "aggregations": [{"field": "revenue", "label": "rev_{{cut}}"}]
    }
  }
}
```

## Axes and modes

- An axis is a unique identifier `name` plus a non-empty list of scalar `values` (string, number, boolean). Numbers keep the digits as written.
- `mode` `grid` (default) is the Cartesian product, first axis slowest: 2 x 3 values => 6 slots. `zip` walks equal-length axes in lockstep: 3 + 3 values => 3 slots.
- `random` / `lhs` draw `samples` seeded slots from continuous axis ranges instead ([`compose-sweep-sampling`](compose-sweep-sampling.md)).
- `de` / `bo` search adaptively: a `budget` of slots in rounds steered by `rank` ([`compose-sweep-adaptive`](compose-sweep-adaptive.md)).

## Placeholders

- `{{axis}}` inside a string splices the value as text. `{"$var": "axis"}` as a whole value keeps its JSON type (a number stays a number). A `$when` guard may name an axis. `{{` inside an object key is refused.
- Every axis must be referenced by the body, an authored `label` pattern or the overlays; a placeholder naming no axis is refused. Both ride `PULSE_SWEEP_INVALID` with `details.reason` (`axis_unreferenced`, `placeholder_unknown`, `placeholder_invalid`).
- NOT expr-lang, not the stored-template surface: substitution precedes decode, never inside an expression (`expression`, `holdout`, overlay `params.formula`, an extension's expression param) — a `{{axis}}` there is `PULSE_SWEEP_EXPR_SPLICE`. A `$var` marker may supply a whole expression.
- To vary a value an expression reads, give the axis `"target": "params.x"` and read `params.x` ([`request-params`](request-params.md)).

## Labels

Omit `label` for the default `axis=value` pairs joined by `_`. The request body must not set its own `label` (`request_label_set`), and a pattern rendering empty is refused (`label_empty`). Labels stay unique across explicit and sweep slots, else `PULSE_COMPOSE_LABEL_COLLISION`. Overlays name sweep slots by these labels.

## Strict body

The substituted body decodes STRICTLY into a `Request`: an unknown key is `PULSE_REQUEST_UNKNOWN_FIELD` (`details.path`, sweep `slot` / `values`), a wrong-typed value `PULSE_SWEEP_INVALID` `request_decode` — before any slot runs.

## Decay-sweep pattern

Rank fits on held-out error: an adstock window with `decay` `{"$var": "decay"}`, one regression with a `holdout`, `rank` `{by: "regressions.<name>.holdout.rmse"}`, `return` excluding `data`. One-call media mix: [`mmm-workflow`](mmm-workflow.md).

## Overlays

`sweep.overlays`: compose overlay specs substituted per slot, appended after the request's own; `{{axis}}` in `reference` / `targets` points each copy at its slot.

## With explicit requests, limits, predict

- `requests` and `sweep` combine: explicit slots first (indices unchanged), sweep slots after. A request with only a `sweep` is valid.
- `MaxComposeSlots` counts the EXPANDED total (explicit + sweep); over the limit is `PULSE_LIMIT_EXCEEDED` before anything runs.
- `pulse_predict` with `composed` (and `PredictCompose`) expands too: the runtime's limit, label and slot checks, plus a `sweep` summary `{axes[{name,count}], mode, expanded_count, labels}`.
- `--echo-request` echoes the expanded slots. MCP fills a missing sweep-body `return` with `standard`.

## Rank

`sweep.rank {by, order, top}` orders the SWEEP slots (never explicit ones) by one number and reports `ranking: [{label, value, rank, slot, axes}]` on the ComposedResponse, 1-based, `order` `asc` (default) or `desc`, `top` keeps the first N (>= 1). `by` is a dot path into each slot's unshaped response: an object key, or on a list the element whose `name` or `label` equals the segment (never an index), ending on a number, e.g. `regressions.fit.residual_std_err`. Two matching elements, or a `name` and `label` that differ, are ambiguous.
- The batch is refused `PULSE_SWEEP_RANK_PATH` (`details.reason` `syntax`, `missing`, `not_number`, `ambiguous`) when `by` resolves in no slot, hits a non-number or is ambiguous.
- A null (NaN) value, or a path missing in this slot only, EXCLUDES that slot with a `PULSE_SWEEP_RANK_PATH` warning on its own response.
- A computed fit figure that is exactly 0 (`r2`, `deviance`, …) ranks as 0 though the wire omits it; one the fit's type does not compute stays missing.
- `slot`: the 0-based sweep index (`responses[explicit + slot]`); `axes`: each axis's typed value in that slot. Read a winner from `ranking[0].axes`, never its label.
- `fits[].from_sweep` binds the winner's regression as a named fit ([`compose-sweep-bind`](compose-sweep-bind.md)).
- `profile: true` adds `ranking_profile` `{axis: [{value, best, label, n}]}`: per axis value (range axis: bin), the best value over the other axes, its slot and the `n` ranked slots behind it (before `top`).
- A ranked slot computes the ranked part even if its `return` excludes it. `ranking` / `ranking_profile` sit outside the compose `return`: always emitted, never rounded.
- `--stream` ends with one `{"ranking":[...]}` line (even `[]`; `ranking_profile` rides it); rank-free sweeps emit none.

## See

[`compose-requests`](compose-requests.md) (slots, labels, overlay fold, parallel) · [`compose-sweep-sampling`](compose-sweep-sampling.md) (random / lhs) · [`compose-sweep-adaptive`](compose-sweep-adaptive.md) (de / bo) · [`overlay-system`](overlay-system.md) (overlay hosts) · [`item-fan-out`](item-fan-out.md) (axes on ONE item; fans out inside each slot) · [`request-templating`](request-templating.md) (the stored-template surface this is not).
