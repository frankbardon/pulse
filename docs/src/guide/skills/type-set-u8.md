```yaml
name: type-set-u8
kind: type
description: 1-byte multi-select bitmask over an inline dictionary of up to 8 labels.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 1 byte per record, read as one bitmask (not bit-packed). Bit `i` set = dictionary entry `i` selected.

## Range

Up to `8` dictionary entries (`MaxSetEntries()`). **The empty mask is a value — "nothing selected" — never null.** Inference reads a source column as a set when at least 30% of its sampled non-null cells hold a `|` (else `;`) delimiter and average more than one token, and picks the smallest rung that holds the sampled tokens. Import never promotes a set rung: a cell past it drops its row (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_SET_OVERFLOW`). Declare a wider rung in the schema, or raise `--sample-rows`.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap, the only null signal; a null row still stores its full mask bytes. No mask value is reserved.

## Dictionary

Present (`HasDictionary()`): an inline schema block shared by every record, at most `8` entries. Shard archives widen a set whose dictionary union outgrows the rung (to the narrowest rung that holds it, MANDATORY warning `PULSE_SHARD_SET_WIDENED`); only a union past `set_u256` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. `pulse widen --field F --to set_…` widens one cohort in place. A bit at or past the dictionary length is corrupt data (`ENCODING_INVALID`, `set_member_out_of_range`). An SPSS multiple-dichotomy response set imports as a set field beside its constituent columns ([`spss-cohorts`](spss-cohorts.md)).

## See

- Skills: [`cohort-null-states`](cohort-null-states.md) (empty vs null), [`cohort-width-overflow`](cohort-width-overflow.md), [`cohort-sharding`](cohort-sharding.md).
- Cross-link: [`type-set-u16`](type-set-u16.md), [`op-agg-set-frequency`](op-agg-set-frequency.md), [`op-attr-set-has`](op-attr-set-has.md).
