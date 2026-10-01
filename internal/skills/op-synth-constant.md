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

## Params

- `value` — any, required. Per-row payload. Interpreted by the declared field type at write time.

The sampler stores `value` as raw `any` — it never converts; the writer-side cast does the interpretation.

## Inputs

field `type:` — any of the 17 `.pulse` field types. Categorical fields treat `value` as a dictionary entry (string), numeric as a number, `date` as days-since-epoch.

## Output

Same `value` returned on every row. RNG state untouched.

## Gotchas

- Missing `value` param → `SERVICE_VALIDATION` ("requires param value").
- Type mismatch between `value` and field `type:` is caught at the writer cast — `PULSE_SYNTH_VALUE_INVALID` or a related code.
- Consumes NO RNG — like `monotonic_from`, adding / removing a constant field preserves byte-equality of every other field's stream.
- A constant categorical still emits a one-entry dictionary block (one byte per row at `categorical_u8`); for a packed-bool equivalent use `bernoulli` with `p=0` / `p=1`.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `op-synth-monotonic-from`, `op-synth-bernoulli`
