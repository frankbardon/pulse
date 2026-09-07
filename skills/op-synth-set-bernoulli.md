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

Synth distributions emit per-row values; they do not produce Response.Components.

## Params

| Name | Type | Default | Description |
|---|---|---|---|
| `options` | list[string] | required | Dictionary entries in bit order (bit `i` ↔ `options[i]`). Also pre-registers the field's dictionary at schema-build time — never lazily via first-touch, so bit assignment stays deterministic. |
| `frequencies` | list[float] | `0.5` each | Per-option `P(bit set)`, in `[0, 1]`; length must match `options`. |

Each option draws independently unless `Spec.SetCategoricalPairs` / `SetNumericPairs` / `SetSetPairs` declares that option as the target of a captured joint pair — in that case its bit is resampled from the pair's conditional probability instead of its own marginal `frequencies[i]`.

## Inputs

| Param | Accepted field types |
|---|---|
| field `type:` | `set_u8`/`u16`/`u32`/`u64` — `options` length must not exceed the type's `MaxSetEntries()` (8/16/32/64). |

## Output

Per-row `map[string]bool` (every declared option, true = selected), assembled into the field's bitmask at write time by looking up each selected option's pre-registered dictionary ID — never by first-encounter order.

## Gotchas

- Empty `options`, or a `frequencies` length mismatch, or a frequency outside `[0, 1]` → `SERVICE_VALIDATION`.
- `options` count over the declared `set_*` width's `MaxSetEntries()` → `PULSE_IMPORT_SET_OVERFLOW` at schema build.
- `SpecFromProfile` populates this from `FieldProfile.Set.Options` (E5-S1's per-option marginal frequencies) and wires `SetCategoricalPairs`/`SetNumericPairs`/`SetSetPairs` from `Conditional.Set*Pairs` (E5-S2) only when the paired field also reconstructed to its expected distribution (`weighted_categorical` / `normal` / `set_bernoulli`).
- A profile captured without `--conditional` has no joint sections — every option draws from its own independent marginal, byte-for-byte the pre-joint-structure behavior.
- Determinism: row value is a `map[string]bool`; Go map iteration order is never used for bit assignment (dictionary is pre-populated in `options` order), so the seeded stream stays byte-identical across runs.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `type-set-u8`, `op-synth-weighted-categorical`, `op-synth-bernoulli`
