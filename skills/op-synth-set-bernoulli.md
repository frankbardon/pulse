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

Synth distributions emit per-row values; no `Response.Components`.

## Params

- `options` — list[string], required. Dictionary entries in bit order (bit `i` ↔ `options[i]`). Pre-registers the field's dictionary at schema-build time — never lazily by first touch, so bit assignment is deterministic.
- `frequencies` — list[float], default `0.5` each. Per-option `P(bit set)` in `[0, 1]`; length must match `options`.

An option draws independently unless `Spec.SetCategoricalPairs` / `SetNumericPairs` / `SetSetPairs` names it as a captured joint pair's target — then its bit is resampled from the pair's conditional probability instead of `frequencies[i]`.

## Inputs

Field `type:` — any set rung, `set_u8`/`u16`/`u32`/`u64`/`u128`/`u256`; `options` length must not exceed the type's `MaxSetEntries()` (8/16/32/64/128/256). The draw is width-agnostic — a member above bit 64 gets the same independent Bernoulli as one below it.

## Output

Per-row `map[string]bool` (every declared option; true = selected), assembled into the bitmask at write time from each selected option's pre-registered dictionary ID, never by first-encounter order.

## Gotchas

- Empty `options`, `frequencies` length mismatch, or a frequency outside `[0, 1]` → `SERVICE_VALIDATION`.
- `options` count over `MaxSetEntries()` → `PULSE_IMPORT_SET_OVERFLOW` at schema build.
- `SpecFromProfile` fills this from `FieldProfile.Set.Options` and wires `SetCategoricalPairs`/`SetNumericPairs`/`SetSetPairs` from `Conditional.Set*Pairs` only when the paired field also reconstructed to its expected distribution (`weighted_categorical` / `normal` / `set_bernoulli`).
- A profile captured without `--conditional` has no joint sections — every option draws its own marginal, byte-for-byte the pre-joint behaviour.
- `SpecFromProfile` carries EVERY captured member, at every rung — `FieldProfile.Set.Options` is bit-ordered and uncapped below `MaxSetEntries()`, so a 206-member `set_u256` round-trips member-for-member. A translation that stopped at 64 would generate a plausible cohort whose high members are simply never selected.
- Go map iteration order is never used for bit assignment (dictionary pre-populated in `options` order), so the seeded stream stays byte-identical.

## See

- `pulse_examples_search tags=[synth]`
- Skills: `synthetic-data`, `type-set-u8`, `op-synth-weighted-categorical`, `op-synth-bernoulli`
