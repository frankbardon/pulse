```yaml
name: type-categorical-u16
kind: type
description: 2-byte categorical with inline dictionary up to 65,536 labels.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 2 bytes per record holding the dictionary index, little-endian. Not bit-packed. The dictionary lives once in the schema block, not in the records.

## Range

Up to `65,536` distinct labels (`MaxCategoricalEntries()`). Fits medium-cardinality enums: city, SKU, vendor. Inference picks it for 201..60,000 distinct sampled values. An INFERRED field that outgrows the rung during import is promoted to `categorical_u32` (`PULSE_IMPORT_WIDTH_PROMOTED`); a DECLARED rung (`--schema`, an authoritative source schema) is a ceiling — each row past it is dropped (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_CATEGORICAL_OVERFLOW`), and `pulse convert` refuses outright. Pick a declared rung with headroom.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. Index `0` is the first dictionary entry, never null (import stores a placeholder `0` under a null). `IsCategorical()` is `true`; the default aggregator is `AGG_MODE_COUNT`; the default grouper is `GROUP_CATEGORY`.

## Dictionary

Present (`HasDictionary()`), at most `65,536` entries. Shard archives union-merge dictionaries (canonical entries first, an arriving shard's records re-indexed), but a categorical rung never widens: a union past `65,536` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. A non-null index at or past the dictionary length is corrupt data; readers that key, label or render it refuse `ENCODING_INVALID` (`categorical_index_out_of_range`).

## See

- Skills: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix), [`cohort-width-overflow`](cohort-width-overflow.md), [`cohort-sharding`](cohort-sharding.md).
- Cross-link: [`type-set-u16`](type-set-u16.md) (multi-select).
