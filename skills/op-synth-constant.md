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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `value` | any | required | Per-row payload. Interpreted by the declared field type at write time. |

The sampler stores `value` as raw `any` — it never converts; the writer-side cast does the interpretation.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | any of the 17 `.pulse` field types. Categorical fields treat `value` as a dictionary entry (string), numeric as a number, `date` as days-since-epoch. |

## Output

Same `value` returned on every row. RNG state untouched.

## Gotchas

- Missing `value` param → `SERVICE_VALIDATION` ("requires param value").
- Type mismatch between `value` and field `type:` is caught at writer cast — surfaces as `PULSE_SYNTH_VALUE_INVALID` or a related coded error.
- Does NOT consume RNG — like `monotonic_from`, adding / removing a constant field preserves byte-equality of every other field's stream.
- A constant categorical field still emits a one-entry dictionary block (one byte per row at `categorical_u8`); use `bernoulli` with `p=0` / `p=1` if you want a packed-bool equivalent.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-monotonic-from`, `op-synth-bernoulli`
