---
name: spss-export
description: Writing .sav — the dictionary + data-section writer, bytecode compression, the four write flags, why the writer takes the whole cohort rather than rendered rows, how a .sav-to-.sav convert carries the source through, and asking export predict first.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, export, convert, export predict, CohortWriter]
requires: [io_format:spss]
---

# SPSS export

Part of the SPSS surface; entry skill `spss-cohorts`. Names, nulls and value rules on the way out: `spss-export-values`; fidelity evidence and charset: `spss-export-fidelity`.

## Writing `.sav` — `pulse export spss`

`BuildDictionary(DictionaryRequest{Schema, Sidecar, Cases, Compression, Options})` (`internal/io/spss`) emits the dictionary section — header, record `2` variable records, records `3`/`4` value labels, the `7/*` subtypes, the `999` terminator — returning a `DictionaryPlan`. `NewDataEncoder(plan, schema)` writes the data section (`WriteCohort(r)` drains a `.pulse` record stream, `WriteCase` takes one record, `Finish()` returns bytes). File = `plan.Bytes` + those.

- **Bytecode compression is the default** (what SPSS's own SAVE writes). `WriterOptions{Uncompressed: true}` (`.Compression()` resolves the header flag) writes flat 8-byte elements; losslessly equivalent, so the knob trades size for a readable hex dump.
- **ZSAV emission is not implemented** — `PULSE_SPSS_COMPRESSION_UNSUPPORTED`.
- `Cases: -1` when the count is unknown up front: `Finish` patches it via `DictionaryPlan.SetCaseCount`, which writes the header int32 **and** record `7/16` together — two disagreeing counts are a file no reader can adjudicate. A `-1` plan emits no `7/16`, carrying the header count alone.

### The CLI surface, and the one contract mismatch behind it

`pulse export spss -i cohort.pulse -o out.sav`; `pulse convert data.csv out.sav` reaches the same writer. Four flags, one per `io.SPSSWriterOptions` field: `--ignore-sidecar`, `--uncompressed`, `--charset`, `--sanitize-names`. Per-flag detail: `session-bootstrap`, `docs/src/cli/export-spss.md`.

**The writer is a `pio.CohortWriter`, not a row writer.** A `.sav` value derives from a categorical's dictionary **ID**, a `set_*`'s mask **bits** and the **null bitmap** — all three gone once `ExportJob` rendered a row (a categorical resolves to label text and two codes may share one label; a null renders `""`, which a string categorical can legitimately hold). So `ExportJob.Run` hands over the cohort path and **skips its row loop**; `WriteRow` is never called. Hence:

- `--include` / `--labels` / non-UTC `--tz` **refused** with `PULSE_SPSS_EXPORT_UNSUPPORTED`, not silently ignored — project or relabel into a narrowed cohort, then export that; a `.sav` DATETIME has no offset slot, so it stays UTC.
- Overlays **warn-and-skip**, like CSV.
- A `convert` has no cohort: the writer buffers rows, builds an intermediate in-memory cohort through the ordinary import path, exports that. From a TEXT source that cohort is inferred and sidecar-less, and it says so (`PULSE_SPSS_SIDECAR_ABSENT`, naming `converted-rows.pulse`).

**`pulse convert survey.sav out.sav` carries the source through** — the `pio.SourceAwareWriter` / `pio.ConvertSource` channel, `SetConvertSource` before `WriteHeader`. The rebuilt cohort takes the source's **declared schema** instead of inferring one, and the source's **sidecar** is written against it (the source `Reader`'s own `WriteSidecar`, which re-fingerprints — the source's copy describes the source cohort and would read back `PULSE_SPSS_SIDECAR_STALE`). Without both, a `.sav` → `.sav` convert was strictly worse than import-then-export: the derived MD column was unrecognisable as derived, so synthesis minted indicator variables colliding with the real constituents (`PULSE_SPSS_NAME_COLLISION` — *every* multiple-dichotomy cohort, at every width), a `set_*` rung re-inferred only as wide as the options somebody ticked, and a value-labelled numeric came back a STRING of bare numerals. Carried only when the emitted rows are faithful to the schema — one cell per field, in field order, so `--include` or a label binding carries nothing and the target infers as before. A text source declares nothing, so inference stays its fallback.

**Ask first — `pulse export predict --format spss`.** The `.sav` writer is the first Pulse writer that can REFUSE, so predict consults the target through the optional `pio.CohortValidator` contract (the SPSS writer's `ValidateCohort`): it runs the writer's own non-data pass (sidecar resolution, dictionary build, name policy, charset transcode, derived fold) and discards it, so a predicted refusal is the export's own check code for code — `PULSE_SPSS_SIDECAR_ABSENT` predicts as a warning on `PredictReport.TargetWarnings` exactly as it exports as one. Pass the flags you will export with; `--sanitize-names` flips a `PULSE_SPSS_NAME_INVALID` refusal into a warning. **Sound but INCOMPLETE:** anything needing a record — a value past a declared width, an unformable character, a dictionary ID with no source code — is unreachable without the data pass, so a pass means no SCHEMA-level refusal was found, never that the export cannot fail. Writes no file; no `--format` ⇒ target-blind as before.
