```yaml
name: type-set-u128
kind: type
description: 16-byte multi-select bitmask over an inline dictionary of up to 128 labels.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 16 bytes per record: 2 little-endian 64-bit words, `words[0]` = bits 0..63 first (the low 8 bytes equal a `set_u64` with the same selections). Bit `i` set = dictionary entry `i` selected. Too wide for the `uint64` value API: `ReadFieldValue` / `WriteFieldValue` refuse it with `ENCODING_TYPE_MISMATCH` rather than truncate (`IsWideSet()`); wide masks travel as `encoding.SetMask`.

## Range

Up to `128` dictionary entries (`MaxSetEntries()`). **The empty mask is a value — "nothing selected" — never null.** Inference picks the smallest rung that holds the sampled tokens (`65`..`128` lands here). Every record pays the full width whether or not it sets a high bit. An INFERRED rung a later cell outgrows is promoted up the set ladder (to `set_u256` at most) and the row imports (`PULSE_IMPORT_WIDTH_PROMOTED`); a DECLARED rung (`--schema`, an override, a source schema) is a ceiling — a cell past it drops its row (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_SET_OVERFLOW`).

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap, the only null signal; a null row still stores its full mask bytes. No mask value is reserved.

## Dictionary

Present (`HasDictionary()`): an inline schema block shared by every record, at most `128` entries. Shard archives widen a set whose dictionary union outgrows the rung (to the narrowest rung that holds it, MANDATORY warning `PULSE_SHARD_SET_WIDENED`); only a union past `set_u256` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. `pulse widen --field F --to set_…` widens one cohort in place. A bit at or past the dictionary length is corrupt data (`ENCODING_INVALID`, `set_member_out_of_range`). An SPSS multiple-dichotomy response set imports as a set field beside its constituent columns ([`spss-cohorts`](spss-cohorts.md)).

## See

- Skills: [`cohort-null-states`](cohort-null-states.md) (empty vs null), [`cohort-width-overflow`](cohort-width-overflow.md), [`cohort-sharding`](cohort-sharding.md).
- Cross-link: [`type-set-u64`](type-set-u64.md), [`type-set-u256`](type-set-u256.md), [`op-agg-set-frequency`](op-agg-set-frequency.md), [`op-attr-set-has`](op-attr-set-has.md).
