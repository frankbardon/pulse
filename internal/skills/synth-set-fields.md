---
name: synth-set-fields
description: How `set_*` (multi-select) fields are profiled per option, captured jointly per option pair, read safely on the wide rungs, and generated deterministically with a pre-registered dictionary.
type: guide
kind: design
applies_to: inspect, predict, manifest
covers: [profile create, synth from-profile, set_bernoulli, set_u128, set_u256, conditional]
requires: [capability:synth]
---

# Synth set fields

### Set (multi-select) field profiling

`set_*` fields are "select all that apply" bitmasks over one shared dictionary — bit `i` ↔ dictionary entry `i`. Each entry profiles as an independent Bernoulli sub-field for MARGINAL purposes: `set.options[i].frequency` is `P(bit i set | field non-null)`.

`profile create` always captures `set = {n, options: [{value, count, frequency}]}` (no flag). **`options` lists EVERY dictionary entry in bit order — never sorted by frequency like `categorical.top`** — so two profiles of one schema compare position-for-position. `n` is the non-null row count. `SpecFromProfile` reconstructs the field as `set_bernoulli` from `options`, bounded only by `MaxSetEntries()`.

**Joint capture.** `--conditional` extends to `set_*` pairs by running the per-pair-type machinery ONCE PER OPTION:

| Section | One entry per | Shape |
|---|---|---|
| `conditional.set_categorical_pairs` | (set option, categorical field) | `{set, option, categorical, cells, n}`; `a_value` fixed `"selected"`/`"not_selected"`, `b_value` the categorical value (own `--top-k` collapse) |
| `conditional.set_numeric_pairs` | (set option, numeric field) | `{set, option, numeric, categories, n}`; `categories` keyed `"selected"`/`"not_selected"` |
| `conditional.set_set_pairs` | option pair between two DIFFERENT set fields (never two options of one field) | `{set_a, option_a, set_b, option_b, cells, n}`, a 2x2 table |

The option axis never needs a top-K collapse (fixed two-value domain). Bounded by `FieldType.MaxSetEntries()` (8/16/32/64/128/256) and `ContingencyCellCap` (128). Thin cells reuse `thinPairWarning`.

## Wide rungs

**`set_u128` / `set_u256` are captured member-for-member, and the read is the whole risk.** A narrow rung arrives in the decoder's wide map as a `uint64`; a wide one arrives as an `encoding.SetMask`. Every site — the marginal, all three conditional pair kinds, the `--fit-models` row snapshot and its dummy design columns, the recovery refit's reader and all four set fidelity sections — resolves it through the shared `setMaskFromWide` / `setMaskFromWideEntry` helpers and NEVER type-asserts `.(uint64)`. A failed assertion yields the zero value, which for a set field is a perfectly plausible "selected nothing": every member captures `frequency: 0.0`, every conditional cell lands in the `not_selected` arm, every model row is deleted listwise (the target reads as `carries no predictors`), every fidelity delta reads exactly `0.0`, and nothing errors. **`setMaskFromWideEntry` is the presence-reporting half** — `ok == false` for a nil map, an absent key (the decoder deletes the entry for a null set field) or a non-mask value, i.e. MISSING rather than empty, which is what a model fit's listwise deletion needs and a marginal does not.

**A dictionary wider than its own rung's mask is reported, never repaired.** Members past `MaxSetEntries()` get no marginal, no pair capture and no fidelity comparison, which is indistinguishable in the document from an option nobody selects — so capture appends `set field "x": dictionary carries N option(s) but the set_uK mask addresses K …` to `Profile.Warnings`, grouped as its own ATTENTION kind (`set options beyond the mask width`) in the terminal summary. Widening is `pulse widen`'s job; the profiler states the loss and moves on.

**Rules are width-agnostic** — a rule addresses a member by option NAME (the row holds a `map[string]bool`), never by bit. But `set_expr` assigns a WHOLE selection: there is no bit-count, single-member edit or mutual-exclusion primitive, so a cardinality constraint over many members is unstated rather than expressible.

## Generation

Each pair resamples ONE option's bit conditioned on the paired field's already-drawn value, in the fixed order set-set → set-categorical → set-numeric, after categorical joint structure and before the correlator. Two options on the SAME set field are two DIFFERENT claim targets, never a conflict (`synth-conflicts`).

**A `set_*` dictionary is pre-populated ONCE from `params.options`' declared order at schema-build time, never lazily during generation** — the row value is a `map[string]bool` and Go randomizes map iteration per process, so pre-registration is what keeps bit assignment (hence determinism) independent of it. `AugmentFromProfile` requires both dictionaries to name the identical option universe in the identical ORDER (`PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH`). A marginal-only profile still generates correctly — joint structure is additive, never required.

## See

- `op-synth-set-bernoulli` · `type-set-u8` · `type-set-u256`.
- `synth-fidelity-report` — the `set_fields` and set pair sections.
- `synth-profile-capture` — the other conditional pair kinds.
