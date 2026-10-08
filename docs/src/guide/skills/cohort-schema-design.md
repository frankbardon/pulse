```yaml
name: cohort-schema-design
description: Entry point for .pulse cohort schemas — the 20-type field matrix, selection heuristics, bit-packed runs, description-length cap, the single-file vs shard-archive shapes, and which focused schema skill answers each nullability, width, parent-group, sharding and sidecar-index question. Use when picking schema types, evaluating storage layout, or interpreting a cohort returned by pulse_inspect.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [u4, u8, u16, u32, u64, f32, f64, decimal128, categorical_u8, categorical_u16, categorical_u32, packed_bool, date, datetime, set_u8, set_u16, set_u32, set_u64, set_u128, set_u256]
```

# Cohort schema design

Pick the right `.pulse` field type, decide nullability, address shards. The schema lives in the cohort header; `pulse_inspect` is the surface for reading it.

## Which skill answers what

| Question | Skill |
|---|---|
| Null bitmap; null promotion on import; null vs empty vs selected in `set_*` / categorical cells per format | [`cohort-null-states`](cohort-null-states.md) |
| A value outgrew its width — promote or refuse? | [`cohort-width-overflow`](cohort-width-overflow.md) |
| Storing a repeated parent block once (`0x02`), `--group`, `dedup`, constant elision | [`cohort-parent-groups`](cohort-parent-groups.md) |
| Shard archive cohesion, auto-widen, anchors, memory, concurrency, moving a cohort | [`cohort-sharding`](cohort-sharding.md) |
| The point-lookup sidecar index: format, staleness, discovery, keyable types | [`cohort-sidecar-index`](cohort-sidecar-index.md) |
| A cohort imported from `.sav` / `.zsav` | [`spss-cohorts`](spss-cohorts.md) |
| One type's bytes, range, null and dictionary rules | atomic `type-<kebab>` |

## Field-type matrix (all 20)

| Name | Bytes | Dict? | Bit-packed? | Notes |
|---|---|---|---|---|
| `u4` | 0 | no | yes | 0..15 |
| `u8` | 1 | no | no | 0..255 |
| `u16` | 2 | no | no | 0..65,535 |
| `u32` | 4 | no | no | 0..~4.29B |
| `u64` | 8 | no | no | |
| `f32` | 4 | no | no | ~7 sig digits; index key = raw bit pattern (`-0.0`/NaN caveat) |
| `f64` | 8 | no | no | ~15 sig digits; same bit-pattern caveat |
| `date` | 4 | no | no | signed int32 epoch days (pre-1970 negative) |
| `datetime` | 8 | no | no | epoch **seconds**, naive UTC; index-key literal parses as a datetime, never a float |
| `packed_bool` | 0 | no | yes | 1 bit |
| `categorical_u8` | 1 | inline, ≤256 | no | dict-encoded; index key = dictionary ID |
| `categorical_u16` | 2 | inline, ≤65,536 | no | |
| `categorical_u32` | 4 | inline, ≤~4.29B | no | |
| `decimal128` | 16 | no | no | per-field `(precision, scale)`; index key = exact mantissa, no float round-trip |
| `set_u8` | 1 | shared, ≤8 labels | no | multi-select bitmask; **not** index-keyable |
| `set_u16` | 2 | shared, ≤16 | no | |
| `set_u32` | 4 | shared, ≤32 | no | |
| `set_u64` | 8 | shared, ≤64 | no | |
| `set_u128` | 16 | shared, ≤128 | no | |
| `set_u256` | 32 | shared, ≤256 | no | widest rung; 256 is a hard ceiling |

`set_*` mask bit `i` = label `dict[i]` selected; empty mask is a valid value (NOT null). Nullability is opt-in per field via the bitmap; all 20 types participate identically. How null, empty and selected stay apart in every format: [`cohort-null-states`](cohort-null-states.md).

`date` and `datetime` are NOT interchangeable — days vs. seconds, a factor of 86,400. Both are accepted by the date grouper, the date-range grouper and the date-range filter, which day-truncate `datetime` to the UTC calendar day. Sub-second timestamps: `u64` microseconds. Resolution, timezone and text-format detail belong to [`type-date`](type-date.md) / [`type-datetime`](type-datetime.md).

## Selection heuristics

Counts / IDs → smallest unsigned width that fits the max. Measurements → `f32`; scores or wide dynamic range → `f64`. Money → `decimal128` (see [`financial-cohorts`](financial-cohorts.md)). Booleans → `packed_bool`; small ordinals (Likert, grades) → `u4`. Strings → always categorical, width by distinct cardinality. Multi-select → `set_*`, smallest width whose cap covers the distinct labels — the width is paid by every record, and above 256 labels there is no set type at all. Sometimes-missing → pick the base type, then `Nullable: true`.

## Bit-packed runs

`u4` and `packed_bool` report `ByteSize() == 0` and share bytes with adjacent packed fields. Place packed fields together for optimal layout. Reordering can change byte offsets even when types are unchanged.

## Field descriptions

Capped at 1000 bytes per field; over-cap → `PULSE_IMPORT_DESCRIPTION_TOO_LONG`. Empty, sub-10-character, or generic ("n/a", "tbd", "unknown", "field", "data", "value", "column") → `PULSE_FIELD_DESCRIPTION_LOW_QUALITY` (warning; error under `--strict`). Style: concise, third-person, present-tense — what the field represents, its units, its domain semantics.

## Sharded cohorts

A `.pulse` path resolves to one of two shapes, dispatched on the leading 4 bytes. **Single-file:** magic `PULSE\x00\x00\x00` + format byte `0x01` or `0x02`, then schema, dicts, records (`0x02` adds a length-prefixed schema extension block carrying parent groups before the records; writers emit `0x01` unless the schema declares a group; `0x01` cohorts stay readable forever, and a binary older than `0x02` refuses a `0x02` cohort loud). **Shard archive:** uncompressed Zip64 (Method 0), magic `PK\x03\x04`, a reserved `_schema.pulse` entry (header-only canonical schema + `SHRD` trailer with `aggregate_record_count` + `shard_count`) plus N standalone shard payloads. Old single-file readers fail loud on archive magic. `archive.pulse#shard.pulse` opens one shard. Go embedders read via `Cohort.Reader()` (archive-order index, canonical schema) and write via `Pulse.NewCohortBuilder` (= explicit-schema import; `Groups` / `ElideConstants` = `--group` / `--elide-constants`; `Shards` splits a new archive; target `a.pulse#new.pulse` appends one shard via `AddShard`): `docs/src/library/cohort-reader.md`. Cohesion, `set_*` auto-widen, grouped shards, memory and concurrency: [`cohort-sharding`](cohort-sharding.md).

## Cross-links

[`financial-cohorts`](financial-cohorts.md) (`decimal128` rules) · [`response-components`](response-components.md) (`data.components.run.shard_count` + `partial_cohort_reason`) · [`aggregation-design`](aggregation-design.md) / [`grouper-design`](grouper-design.md) / [`attribute-composition`](attribute-composition.md) (`set_*` operator surfaces) · [`tool-lookup`](tool-lookup.md) (point-lookup MCP surface on the sidecar format) · [`tool-import`](tool-import.md) (import MCP surface incl. the SPSS format enum) · [`label-display`](label-display.md) (resolving SPSS value labels from stored codes) · [`spss-cohorts`](spss-cohorts.md) (the full `.sav` / `.zsav` surface) · `docs/src/internals/managing-shard-archives.md`.
