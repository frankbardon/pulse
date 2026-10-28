# Byte layout — wide-set word order, sidecar files, SPSS fidelity, and projected decode

**How to load.** This file is an INDEX: the preamble below plus a section map. Load it, then ONLY the per-topic slice(s) under `.claude/reference/byte-layout/` your change touches; find one with `grep -l '<identifier>' .claude/reference/byte-layout/*.md`. `.claude/reference/byte-layout/history.md` is superseded wording — do NOT load it by default. A section-qualified pointer at this file resolves through the section map.

Relocated verbatim from CLAUDE.md (section `### Byte-layout invariants`). CLAUDE.md keeps the always-load half inline, compressed — the 9-byte header, the schema block, the dictionary blocks, the record data, the per-record null bitmap, a one-line parent-group summary, the 22 field types and a two-line shard archive variant; the detail those lines dropped is the last paragraph below ("The always-load half, long form"). Everything below is the long form it points at: the wide-set word order (the one genuine `.pulse` payload rule here), the three sidecar files that sit BESIDE a `.pulse` cohort and are not `.pulse` layouts, the SPSS fidelity model in both directions, the default-on projected buffered decode, the standalone regression model file (`.pulse-model.json`), the standalone vector-fit population file (`.pulse-population.json`) and the standalone SEM fit file (`.pulse-sem.json`).

Load it before changing any of them — CLAUDE.md's Update Demand table names this file as a required companion for a `.pulse` file-format change, a registered field type, shard archive layout, the sidecar point-lookup index or its manifest, a registered I/O format (SPSS in particular), and the projection contract.

`format_version` does NOT move for anything described here: none of these surfaces is payload-reachable (`io.PredictReport`, `descriptor.InspectResult`, the sidecar documents and the index format all sit outside `types/`), so `descriptor/testdata/payload-schema.json` is untouched. The sidecar index MANIFEST carries its OWN `format_version` (`"1"`), independent of both `encoding.FormatVersion` and `IndexFormatVersion`.

## Contents

Paragraph order below is the file's order; each entry is that paragraph's bold lead-in.

1. Format version — `0x01` / `0x02`
2. Parent groups — the `0x02` group descriptor
3. Global-constant elision — opt-in, full-pass detection
4. Import-time group declaration — `--group` / `ImportJob.Groups`
5. Per-group viability gate — `PULSE_GROUP_TOO_NARROW` / `PULSE_DEDUP_LOW_RATIO` / `--strict`
6. Retro-dedup — `pulse dedup` / `Pulse.Dedup` / `pulse_dedup`
7. Row-at-a-time builder — `Pulse.NewCohortBuilder`
8. Materialized derived columns — `Pulse.Materialize`
9. Wide-set word order — `set_u128` / `set_u256`
10. Vector fields — `vec_f32` / `vec_f64` and the VECTORS section
11. Temporal word sign — `date` int32 / `datetime` int64
12. Import datetime literals — instant vs wall clock (SPSS, Arrow, Parquet)
13. Shard set-width auto-widen
14. Shard archives with parent groups — `0x02` shards
15. Sidecar point-lookup index
16. Sidecar index MANIFEST — `cohort.pulse.indexes.json` (`internal/encoding.IndexManifestSuffix`)
17. Archive point-lookup index (U51 #359) — one sidecar per shard + the archive manifest's coverage
18. SPSS metadata sidecar
19. SPSS derived columns
20. SPSS export
21. SPSS convert carries the source through
22. Export predict is target-aware
23. Projected buffered decode
24. Transport-only compression — `.pulse.zst` (`pulse export transfer` / `pulse import transfer`)
25. Table-directory sidecar exclusion — `PULSE_LABEL_TABLES_DIR` / `PULSE_RANGE_TABLES_DIR`
26. Model file — `.pulse-model.json` (`model_format_version` 1, 2 or 3)
27. Population file — `.pulse-population.json` (`population_format_version` 1)
28. SEM fit file — `.pulse-sem.json` (`sem_format_version` 1, 2 or 3)
29. CLAUDE.md's sidecar contract sentence, pre-compression
30. The always-load half, long form — header, schema block, bitmap, field types, shard archive variant (relocated at U18 E1-S1)

## Section map

| Slice | Sections and bold leads, in file order |
|---|---|
| `byte-layout/format-and-groups.md` | The contract; Format version — `0x01` / `0x02`; Parent groups — the `0x02` group descriptor; Global-constant elision — opt-in, full-pass detection; Import-time group declaration — `--group` / `ImportJob.Groups`; Per-group viability gate — `PULSE_GROUP_TOO_NARROW` / `PULSE_DEDUP_LOW_RATIO` / `--strict`; Retro-dedup — `pulse dedup` / `Pulse.Dedup` / `pulse_dedup` |
| `byte-layout/writers-and-field-words.md` | Row-at-a-time builder — `Pulse.NewCohortBuilder`; Materialized derived columns — `Pulse.Materialize`; Wide-set word order — `set_u128` / `set_u256`; Vector fields — `vec_f32` / `vec_f64` and the VECTORS section; Temporal word sign — `date` int32 / `datetime` int64; Import datetime literals — instant vs wall clock (SPSS, Arrow, Parquet) |
| `byte-layout/shards-and-index.md` | Shard set-width auto-widen; Shard archives with parent groups — `0x02` shards; Sidecar point-lookup index; Sidecar index MANIFEST — `cohort.pulse.indexes.json` (`internal/encoding.IndexManifestS…; Archive point-lookup index (U51 #359) — one sidecar per shard + the archive manifest's coverage |
| `byte-layout/spss-export-and-decode.md` | SPSS metadata sidecar; SPSS derived columns; SPSS export; SPSS convert carries the source through; Export predict is target-aware; Projected buffered decode; Transport-only compression — `.pulse.zst` (`pulse export transfer` / `pulse import tran…; Table-directory sidecar exclusion — `PULSE_LABEL_TABLES_DIR` / `PULSE_RANGE_TABLES_DIR`… |
| `byte-layout/long-form-and-model-files.md` | The always-load half, long form — header, schema block, bitmap, field types, shard arch…; Model file — `.pulse-model.json` (`model_format_version` 1, 2 or 3); Population file — `.pulse-population.json` (`population_format_version` 1); SEM fit file — `.pulse-sem.json` (`sem_format_version` 1, 2 or 3) |
| `byte-layout/history.md` (not loaded by default) | CLAUDE.md's sidecar contract sentence, pre-compression (relocated verbatim at weighting…; Contract: `.claude/reference/byte-layout.md` — load it before touching any of these. No… |
