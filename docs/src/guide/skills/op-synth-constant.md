```yaml
name: op-synth-constant
description: Emit a constant value on every row; useful for unit testing, sentinel columns, and placeholder fields.
kind: operator
category: SYNTH
operator: constant
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, data-quality]
```

Synth distributions emit per-row values; no `Response.Components`.

## Use when

Puts the same value on every row, for a placeholder field or a fixed flag in a test fixture.

Questions it answers:

- How do I fill a column with the same country code?
- How do I add a fixed version field to test data?

Use something else:

- `monotonic_from` when each row needs a distinct ID.
- `weighted_categorical` when the value should vary over a few labels.
- `bernoulli` when a flag should be set on only some rows.

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

## Contract

- Applies to: `any` fields.

## Gotchas

- Missing `value` param → `SERVICE_VALIDATION` ("requires param value").
- Any other params key (e.g. `frequencies` copied from a `set_bernoulli`) → `PULSE_REQUEST_UNKNOWN_FIELD` with `details.path`.
- A `value` the field cannot hold (text on a number field) → `SERVICE_VALIDATION`.
- Consumes NO RNG — like `monotonic_from`, adding / removing a constant field preserves byte-equality of every other field's stream.
- A constant categorical still writes a one-entry dictionary; for a constant boolean, `bernoulli` with `p` `0` / `1`.

## See

- Skills: [`synthetic-data`](synthetic-data.md), [`op-synth-monotonic-from`](op-synth-monotonic-from.md), [`op-synth-bernoulli`](op-synth-bernoulli.md)
