---
name: type-set-u256
kind: type
description: 32-byte multi-select bitmask over an inline dictionary of up to 256 labels — the widest set rung.
type: reference
applies_to: inspect, predict
---

## Bytes

Fixed-width: 32 bytes per record: 4 little-endian 64-bit words, `words[0]` = bits 0..63 first (the low 8 bytes equal a `set_u64` with the same selections). Bit `i` set = dictionary entry `i` selected. Too wide for the `uint64` value API: `ReadFieldValue` / `WriteFieldValue` refuse it with `ENCODING_TYPE_MISMATCH` rather than truncate (`IsWideSet()`); wide masks travel as `encoding.SetMask`.

## Range

Up to `256` dictionary entries (`MaxSetEntries()`). **The empty mask is a value — "nothing selected" — never null.** Inference picks the smallest rung that holds the sampled tokens (`129`..`256` lands here). Every record pays the full width whether or not it sets a high bit. The widest rung, a hard ceiling: a source with more distinct tokens infers no set type, and a cell past 256 entries drops its row (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_SET_OVERFLOW`).

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap, the only null signal; a null row still stores its full mask bytes. No mask value is reserved.

## Dictionary

Present (`HasDictionary()`): an inline schema block shared by every record, at most `256` entries. A shard union past 256 entries is `PULSE_SHARD_DICT_WIDTH_OVERFLOW` — no wider rung exists. A bit at or past the dictionary length is corrupt data (`ENCODING_INVALID`, `set_member_out_of_range`).<!-- feature: io_format:spss --> An SPSS multiple-dichotomy response set imports as a set field beside its constituent columns (`spss-cohorts`).<!-- /feature -->

## See

- Skills: `cohort-null-states` (empty vs null), `cohort-width-overflow`, `cohort-sharding`.
- Cross-link: `type-set-u128`, `op-agg-set-frequency`, `op-attr-set-has`.
