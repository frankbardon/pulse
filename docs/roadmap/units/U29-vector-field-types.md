---
id: U29
slug: vector-field-types
title: "Cohorts can store fixed-length numeric vectors natively"
track: Vector & matrix
size: L
status: done
depends_on: [U16, U27]
soft_depends_on: []
blocks: [U30]
todo_items: [151, 152, 153, 154, 155]
branch: vector-field-types
---

# U29 — vector-field-types

**Outcome:** Cohorts can store fixed-length numeric vectors natively.

**Track:** Vector & matrix · **Size:** L · **Depends on:** [U16](U16-matrix-result.md), [U27](U27-vector-metrics-aggregates.md) · **Unblocks:** [U30](U30-matrix-extensions-hardening.md)

## Summary

`vec_f32` / `vec_f64` field types (type bytes 20/21) with the `VECTORS` schema extension section (tag 2, REQUIRED, carrying dim, labels and `kind`), the `ReadVector` accessor, the compatibility test, import `--vector` folding, export expansion, shard cohesion and parent-group membership, plus full byte-layout documentation.

## References

**Theme documents (read before starting):**
- [vector-matrix 01 — Foundation](../v1.0.0-vector-matrix/01-foundation.md) — F4 Native vector field types
- [vector-matrix 07 — Similarity & distance](../v1.0.0-vector-matrix/07-similarity-and-distance.md) — S2 (kind flag in the VECTORS section)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#151** (10. Vector & matrix — operators › E7 — Native vector field types) `vec_f32` / `vec_f64` encoding and the `VECTORS` schema extension section (tag 2); `ReadVector` accessor
- [x] **#152** (10. Vector & matrix — operators › E7 — Native vector field types) Compatibility test: older cohorts unchanged; older binaries refuse loudly
- [x] **#153** (10. Vector & matrix — operators › E7 — Native vector field types) Import `--vector` folding (CSV, NDJSON, Arrow, Parquet)
- [x] **#154** (10. Vector & matrix — operators › E7 — Native vector field types) Export expansion; shard cohesion; parent-group membership
- [x] **#155** (10. Vector & matrix — operators › E7 — Native vector field types) `type-vec-f32.md`, `type-vec-f64.md`, `byte-layout.md`, CLAUDE.md byte-layout invariants (20 → 22 types)

## Scope

**In scope**
- Encoding + extension section + accessor
- Compat test
- Import folding (CSV/NDJSON/Arrow/Parquet)
- Export expansion; shards; groups
- Docs and skills

**Out of scope**
- `vec_u8` (stretch)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(vector-field-types/E<n>-S<m>): …`; close each epic with `milestone(vector-field-types/E<n>): vertical slice complete — <epic title>`.

### E1 — The format can hold vectors
- S1: type bytes, `VECTORS` section (tag 2), `RequiredFormatVersion` → 0x02
- S2: `ReadVector`; scalar API refuses with `ENCODING_TYPE_MISMATCH`
- S3: compat test (0x01 unchanged; older binary refuses loudly)

### E2 — Vectors flow in and out
- S1: import `--vector NAME:cols|/regex/` + Arrow FixedSizeList + Parquet fixed LIST
- S2: export expansion; shard cohesion (dim strict, labels tolerant); group membership

### E3 — Documented like every type
- S1: `byte-layout.md`, `type-vec-*`, `cohort-schema-design.md`, CLAUDE.md invariants (20 → 22)

## Acceptance criteria

- [ ] `encoding/testdata/format_v1.pulse` still reads and re-writes byte-identically
- [ ] A cohort without vector fields writes exactly as before
- [ ] Round-trip CSV → .pulse (vector) → CSV is lossless for f64
- [ ] Every operator from U16/U24–U27 accepts a native vector wherever it accepts a virtual one
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllFieldTypes`
- `TestShardArchiveLayoutDocumented`
- Compat test

## Update Demand companions

- `.claude/reference/byte-layout.md` (load before starting)
- CLAUDE.md Byte-layout invariants
- `docs/src/format/field-types.md`; `docs/src/cli/flags.md` (`--vector`)

## Handed on

| Item | Destination |
|---|---|
| Vector synthesis: every synth surface refuses a `vec_*` field with `PULSE_SYNTH_VECTOR_UNSUPPORTED` | [U52](U52-synth-realism.md) #450 |
| Public per-output-column export plan for external writers (`iocore.ExportColumn` / `ColumnAwareWriter`) | Done in U29 (not handed on): `io.ColumnAwareWriter`, `io.ExportColumn`, `io.ExportColumnKind` + constants, `io.ColumnFields`; #451 ticked in [U34](U34-extension-validation.md) |

## Landed deviations

- Owner decisions during the unit: vector import is not feature-profile gated (only the matrix operators that consume vectors are); `io.ListAwareReader` / `ListCell` / `ListState` and `io.ListAwareWriter` are public (`RowList` returns an error for a cell the source failed to read); the export column plan (`io.ColumnAwareWriter` / `ExportColumn` / `ColumnFields`) is public; `extend.Record.NumericValue` resolves native element members `emb[k]`; `pulse_recommend` drafts native vectors in vector slots.
- Found and fixed in-unit: presence slots counted every native vector as null; Excel / Arrow / Parquet export typed columns by position (projected or `_label`-augmented exports failed or mis-formatted).
- The effort grew past the original three epics (format, engine, import/export, docs); the migration guide's "Changes from U29" is the complete list.

## Human inputs & decisions

- None.

## Notes

- This is the only file-format change in v1. The design deliberately places it late.
