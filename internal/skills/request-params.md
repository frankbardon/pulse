---
name: request-params
description: Request.Params — named constants a request's expressions read as params.<name> instead of splicing values into expression text; types, refusals, the read-only rule and Compose / chain inheritance.
type: guide
kind: design
applies_to: process, compose, predict
covers: [params, Request.Params, params.name, PULSE_PARAMS_INVALID, PULSE_PARAMS_UNDECLARED, PULSE_PARAMS_FIELD_COLLISION]
requires: [capability:params]
---

# Request params

`"params": {...}` on a request declares constants; an expression reads one as `params.name` (or `params["name"]`). Change the value, never the expression text: no string splicing, no quoting bugs, one expression shape per question.

<!-- feature: FILTER_EXPRESSION -->
```json
{
  "params": {"from_wave": "a", "min_age": 30},
  "filterers": [{"type": "FILTER_EXPRESSION", "expression": "panel == params.from_wave && age >= params.min_age"}]
}
```
<!-- /feature -->

## Values

| Value | Binds as |
|---|---|
| number | exact: an integer is int64 (uint64 above int64 range), else float64 |
| string, bool | itself |
| list, ONE kind (all numbers, strings or bools) | a typed list: `id in params.ids` |

`null`, an invalid name (a Go-style identifier is required) or a mixed list → `PULSE_PARAMS_INVALID` (`details.reason`).

## Where they bind

<!-- feature: ATTR_FORMULA -->`ATTR_FORMULA`,<!-- /feature --><!-- feature: FILTER_EXPRESSION --> `FILTER_EXPRESSION`,<!-- /feature --> `total()` and the holdout expression.<!-- feature: OVERLAY_FORMULA --> `OVERLAY_FORMULA` reads them too.<!-- /feature --> Not synth rules, not `FilterToFile`.<!-- feature: capability:facet --> A `FacetSchema` request's filterers read the facet's own `params` (same rules, same refusals).<!-- /feature --> Params are READ-ONLY constants folded per run: an expression cannot write one, and a run sees only its own values.

## Refusals (predict raises the same)

- `params.x` where `x` is not declared → `PULSE_PARAMS_UNDECLARED`.
- A cohort field, derived column or vector NAMED `params`, while the request reads `params.` → `PULSE_PARAMS_FIELD_COLLISION`. That column stays readable as bare `params` or `$env["params"]`.
- A bare `params` is never a parameter read.

## Compose and chains

A top-level `params` on a Compose batch or a process chain merges into EVERY sub-request (slots, fits, chain stages); a sub-request's own key wins. A Compose-level formula overlay reads the top-level params. `--echo-request` shows the merged view. Same on the CLI `api` leaves and the MCP tools.

## Gotchas

- `u64` fields compare exactly against params, literals and each other (`==`, `<`, `in`); arithmetic still goes through float64.
- A profile hiding `capability:params` makes the slot unknown (`PULSE_REQUEST_UNKNOWN_FIELD`).
- Not request templating: templates fill a request BEFORE it is validated; params are the request's own constants, read at compile time.
<!-- feature: capability:templates -->
- A template fills params with a marker, `"params":{"w":{"$var":"w"}}` (the number stays a number); `{{w}}` inside an expression is refused `PULSE_TEMPLATE_EXPR_SPLICE` (`request-templating`).
<!-- /feature -->

## See

- `expression-language`, `compose-requests`, `process-chain`, `request-envelope`
- `pulse_examples_search tags=[feature-engineering]`
