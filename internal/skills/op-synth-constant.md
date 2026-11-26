---
name: op-synth-constant
description: Emit a constant value on every row; useful for unit testing, sentinel columns, and placeholder fields.
kind: operator
category: SYNTH
operator: constant
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, data-quality]
---

Synth distributions emit per-row values; no `Response.Components`.

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `value` | any | (required) | Per-row payload, interpreted by the declared field type. |
| `options` | list | — | `set_*` fields only: dictionary entries in bit order, pre-registered at schema-build time (as `set_bernoulli`'s); on any other field type an unknown key, refused. |

`value` is checked against the field type once, before any row is written.

## Inputs

field `type:` — any `.pulse` field type. Categorical takes a string, `set_*` a list of option names, numeric/`date` a number or bool (`decimal128` also a string).

## Output

Same `value` on every row. Consumes no randomness.

<!-- generated: contract -->

## Gotchas

- Missing `value` param → `SERVICE_VALIDATION` ("requires param value").
- Any other params key (e.g. `frequencies` copied from a `set_bernoulli`) → `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path`.
- A `value` the field cannot hold (text on a number field) → `SERVICE_VALIDATION`.
- Consumes NO RNG — like `monotonic_from`, adding / removing a constant field preserves byte-equality of every other field's stream.
- A constant categorical still writes a one-entry dictionary; for a constant boolean, `bernoulli` with `p` `0` / `1`.

## See

- Skills: `synthetic-data`, `op-synth-monotonic-from`, `op-synth-bernoulli`
