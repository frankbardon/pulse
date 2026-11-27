---
name: op-synth-set-bernoulli
description: Multi-select set_* bitmask generation — independent per-option Bernoulli draws, or a joint-structure resample when a paired field is declared.
kind: operator
category: SYNTH
operator: set_bernoulli
type: reference
applies_to: inspect, predict, manifest
examples_tags: [synth, distribution-shape]
---

Synth distributions emit per-row values; no `Response.Components`. No MCP tool runs synth: the CLI (`pulse synth from-schema`) or `Pulse.Synth` does (`synthetic-data`).

<!-- generated: use-when -->

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `options` | list | (required) | Strings: dictionary entries in bit order (bit `i` ↔ `options[i]`), pre-registered at schema build (`synth-set-fields`). |
| `frequencies` | list | `0.5` | Per-option `P(bit set)` in `[0, 1]` (default each); length must match `options`. |

An option draws independently unless a spec `set_categorical_pairs` / `set_numeric_pairs` / `set_set_pairs` entry names it as a joint pair's target — then its bit is resampled from the pair's conditional probability instead of `frequencies[i]`.

## Inputs

Field `type:` — any set rung (`set_u8` … `set_u256`); `options` no more than the type's member capacity.

## Output

Per row, the selected options, written as the field's bitmask (no option selected = the empty mask, not null).

<!-- generated: contract -->

## Gotchas

- Empty `options`, `frequencies` length mismatch, or a frequency outside `[0, 1]` → `SERVICE_VALIDATION`.
- More `options` than the type holds → `PULSE_IMPORT_SET_OVERFLOW` before any row is written.
- A profile-built spec fills this from the captured options and wires the set pairs only when the paired field also reconstructed to its expected distribution (`weighted_categorical` / `normal` / `set_bernoulli`).
- Without `--conditional` every option draws its own marginal.
- A profile carries every captured member at every rung.

## See

- Skills: `synth-set-fields`, `type-set-u8`, `op-synth-weighted-categorical`, `op-synth-bernoulli`
