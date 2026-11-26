```yaml
name: type-categorical-u8
kind: type
description: 1-byte categorical with inline dictionary up to 256 labels.
type: reference
applies_to: inspect, predict
```

## Bytes

Fixed-width: 1 byte per record holding the dictionary index. Not bit-packed. The dictionary lives once in the schema block, not in the records.

## Range

Up to `256` distinct labels (`MaxCategoricalEntries()`). Fits narrow string enums: country, status, response option, segment. Inference picks it for a string column with at most 200 distinct sampled values, and refuses one whose sample (50+ rows) is all unique (`PULSE_IMPORT_CATEGORICAL_UNBOUNDED`). An INFERRED field that outgrows the rung during import is promoted to `categorical_u16` (`PULSE_IMPORT_WIDTH_PROMOTED`); a DECLARED rung (`--schema`, an authoritative source schema) is a ceiling — each row past it is dropped (`PULSE_IMPORT_ROW_ERROR`, reason `PULSE_IMPORT_CATEGORICAL_OVERFLOW`), and `pulse convert` refuses outright. Pick a declared rung with headroom.

## Null

Orthogonal: `Nullable: true` puts the field in the per-record null bitmap. Index `0` is the first dictionary entry, never null (import stores a placeholder `0` under a null). `IsCategorical()` is `true`; the default aggregator is `AGG_MODE_COUNT`; the default grouper is `GROUP_CATEGORY`.

## Dictionary

Present (`HasDictionary()`), at most `256` entries. Shard archives union-merge dictionaries (canonical entries first, an arriving shard's records re-indexed), but a categorical rung never widens: a union past `256` is `PULSE_SHARD_DICT_WIDTH_OVERFLOW`. A non-null index at or past the dictionary length is corrupt data; readers that key, label or render it refuse `ENCODING_INVALID` (`categorical_index_out_of_range`).

## See

- Skills: [`cohort-schema-design`](cohort-schema-design.md) (Field-type matrix), [`cohort-width-overflow`](cohort-width-overflow.md), [`cohort-sharding`](cohort-sharding.md).
- Cross-link: [`type-set-u8`](type-set-u8.md) (multi-select).
