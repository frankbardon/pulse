---
name: cohort-width-overflow
description: What happens when a value outgrows its field width — inferred categorical and numeric rungs promote on the full row pass, declared widths refuse row by row, set cardinality and shard dictionary overflow, and how convert reports it.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [width overflow, width promotion, categorical, set fields, column_type_overrides, import, convert]
---

# Width overflow

Part of the `.pulse` schema surface; entry skill `cohort-schema-design` (field-type matrix, selection heuristics).

## Width overflow

- `PULSE_IMPORT_CATEGORICAL_OVERFLOW` / `PULSE_IMPORT_CATEGORICAL_UNBOUNDED` — categorical width exceeded or dict unbounded. A full DECLARED rung is a capacity violation, not a row condition: every later unseen category would be lost for the rest of the file (an import row-errors each one; an INFERRED rung promotes instead, below). On a declared rung `ConvertJob.Run` refuses outright — it stops at the offending cell (`row` / `column` / `type` / `max_entries` / `value` in details) and writes no `--keep-pulse` intermediate.
- `PULSE_IMPORT_SET_OVERFLOW` — `set_*` cardinality exceeded width.
- `PULSE_SHARD_DICT_WIDTH_OVERFLOW` — shard insert would expand union dict past declared width. For a `set_*` field this now fires only past `set_u256`; below that the archive auto-widens (`cohort-sharding`, Cohesion).

**Inferred widths promote; declared widths refuse.** Inference sizes `categorical_*` rungs (≤200 distinct in the sample → `u8`) and integer widths from the first `--sample-rows` rows. On the full row pass an INFERRED field whose value outgrows that is promoted to the narrowest type holding it: `categorical_u8` → `u16` → `u32`; `u4` → `u8` → `u16` → `u32` → `u64` for a non-negative integer; `u4`..`u32` → `f64` for any other number (never from `u64`, where f64 is not exact); `f32` → `f64` for a value outside f32's RANGE (past `MaxFloat32`, or a non-zero magnitude f32 flushes to zero) — the same test inference chose `f32` by, so a value f32 merely rounds (`0.1`) never promotes. A non-boolean in a `packed_bool` stays a row error: no type holds both losslessly. Rows already read are re-strided in memory — the cohort is byte-identical to one inferred from the whole file, and a cohort that never overflows is unchanged. One `PULSE_IMPORT_WIDTH_PROMOTED` per field (`ImportReport.WidthWarnings` / `width_warnings`). `pulse convert` over an inferred schema runs the same per-cell decision: it passes cell text through unchanged, reports the promoted types and warnings (`ConvertReport.WidthWarnings`), and its `--keep-pulse` cohort is byte-identical to a plain import. A declared width — `--schema`, `column_type_overrides`, an authoritative SPSS / Arrow / Parquet schema — never promotes: its overflow stays a per-row `PULSE_IMPORT_ROW_ERROR`, as does a non-number and a dictionary past `categorical_u32`. A `column_type_overrides` column is stricter still: any value it cannot hold refuses the import (`PULSE_IMPORT_OVERRIDE_INVALID`). Parent-group widths, the viability gate and constant elision are judged on the promoted widths; `import predict` sees them only on its measured pass.

Mitigation for declared widths: pick them with growth headroom up front.
