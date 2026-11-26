```yaml
name: type-categorical-u32
kind: type
description: 4-byte categorical with inline dictionary up to ~4.29B labels.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 4 bytes per record holding the dictionary index, little-endian. Not bit-packed. The dictionary lives once in the schema block, not in the records.

## Range

Up to `4,294,967,295` distinct labels (`MaxCategoricalEntries()`). Fits very large enums and labelled keys; when the labels carry no analytic value, a plain `u32` is cheaper (no dictionary in the schema block). Inference picks it past 60,000 distinct sampled values. The widest rung: an import row past it is dropped (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_CATEGORICAL_OVERFLOW`). In practice the schema block, which holds every label, is the limit.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. Index `0` is the first dictionary entry, never null (import stores a placeholder `0` under a null). `IsCategorical()` is `true`; the default aggregator is `AGG_MODE_COUNT`; the default grouper is `GROUP_CATEGORY`.

## Dictionary

Present (`HasDictionary()`), at most `4,294,967,295` entries. Shard archives union-merge dictionaries (canonical entries first, an arriving shard's records re-indexed), but a categorical rung never widens: a union past `4,294,967,295` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. A non-null index at or past the dictionary length is corrupt data; readers that key, label or render it refuse `ENCODING_INVALID` (`categorical_index_out_of_range`).

## See

- Skills: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix), [`cohort-width-overflow`](cohort-width-overflow.md), [`cohort-sharding`](cohort-sharding.md).
- Cross-link: [`type-set-u32`](type-set-u32.md) (multi-select), [`type-u32`](type-u32.md).
