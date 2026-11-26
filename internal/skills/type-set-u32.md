---
name: type-set-u32
kind: type
description: 4-byte multi-select bitmask over an inline dictionary of up to 32 labels.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 4 bytes per record, little-endian, read as one bitmask (not bit-packed). Bit `i` set = dictionary entry `i` selected.

## Range

Up to `32` dictionary entries (`MaxSetEntries()`). **The empty mask is a value — "nothing selected" — never null.** Inference picks the smallest rung that holds the sampled tokens (`17`..`32` lands here). Import never promotes a set rung: a cell past it drops its row (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_SET_OVERFLOW`). Declare a wider rung in the schema, or raise `--sample-rows`.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap, the only null signal; a null row still stores its full mask bytes. No mask value is reserved.

## Dictionary

Present (`HasDictionary()`): an inline schema block shared by every record, at most `32` entries. Shard archives widen a set whose dictionary union outgrows the rung (to the narrowest rung that holds it, MANDATORY warning `PULSE_SHARD_SET_WIDENED`); only a union past `set_u256` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. `pulse widen --field F --to set_…` widens one cohort in place. A bit at or past the dictionary length is corrupt data (`ENCODING_INVALID`, `set_member_out_of_range`).<!-- feature: io_format:spss --> An SPSS multiple-dichotomy response set imports as a set field beside its constituent columns (`spss-cohorts`).<!-- /feature -->

## See

- Skills: `cohort-null-states` (empty vs null), `cohort-width-overflow`, `cohort-sharding`.
- Cross-link: `type-set-u16`, `type-set-u64`, `op-agg-set-frequency`, `op-attr-set-has`.
