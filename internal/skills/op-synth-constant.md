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

`value` is checked and normalised to the field's row shape once, at spec parse — never at write time.

## Inputs

field `type:` — any `.pulse` field type. Categorical takes a string, `set_*` a list of option names, numeric/`date` a number or bool (`decimal128` also a string).

## Output

Same `value` returned on every row. RNG state untouched.

<!-- generated: contract -->

## Gotchas

- Missing `value` param → `SERVICE_VALIDATION` ("requires param value").
- Any other params key (e.g. `frequencies` copied from a `set_bernoulli`) → `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path`; it was silently ignored before.
- A `value` the field cannot hold (text on a number field) → `SERVICE_VALIDATION` at spec parse.
- Consumes NO RNG — like `monotonic_from`, adding / removing a constant field preserves byte-equality of every other field's stream.
- A constant categorical still emits a one-entry dictionary block (one byte per row at `categorical_u8`); for a packed-bool equivalent use `bernoulli` with `p=0` / `p=1`.

## See

- Skills: `synthetic-data`, `op-synth-monotonic-from`, `op-synth-bernoulli`
