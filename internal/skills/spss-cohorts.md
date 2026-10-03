---
name: spss-cohorts
description: Entry point for SPSS .sav / .zsav cohorts — the three surprises (extra derived columns, codes-not-labels dictionaries, the label-tables directory trap), why dictionaries hold codes, and which focused SPSS skill answers each import, missing-value, sidecar and .sav-writing question. Read this when a cohort came from SPSS or when emitting a .sav.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, zsav, import, export, derived columns, user-missing values, multiple-response sets, metadata sidecar]
requires: [io_format:spss]
---

# SPSS cohorts

The one import source whose schema Pulse does **not** infer, and the one producing a cohort wider than its source dictionary. Read and write belong together — the write half is defined by the read half's derived columns, sidecar and charset. General `.pulse` schema surface: `cohort-schema-design`.

## Three things that surprise people first

1. **Columns the `.sav` never declared** — `<var>_missing` siblings, one `set_*` per multiple-dichotomy question. Count columns from `pulse_inspect` / `ReadHeader`, never from the SPSS variable count.
2. **Categorical columns hold codes** — `"1"` / `"2"`, not `Male` / `Female`.
3. **Never point `PULSE_LABEL_TABLES_DIR` at a cohort directory** — it parses every `.json` beneath it.

## SPSS import — what it changes about a `.pulse` schema

1. **The schema is not inferred.** An SPSS dictionary DECLARES every column, so `internal/io/spss` implements `io.SchemaAwareReader` and `internal/io/infer.go`'s sample-and-vote pass is skipped: `SampleRows`, `SetInferenceMinPct`, `SetDelimiters` are inert, `ColumnTypeOverrides` is refused (`PULSE_IMPORT_OVERRIDE_INVALID`), and there is **no null promotion** — declared nullability is a contract, so an unexpected null is `PULSE_IMPORT_ROW_ERROR`. An explicit `ImportJob.Schema` still wins.
2. **The cohort can be wider than the source.** Two derived kinds: a `<var>_missing` `categorical_*` sibling per numeric variable declaring user-missing values (the null bitmap is one bit and cannot say *why*), and one `set_*` column per multiple-dichotomy response set, emitted **beside** its constituents. `--spss-missing=null` suppresses the siblings; the `set_*` column has no opt-out.

**Writable.** An import also writes `cohort.pulse.spss.json`, the JSON metadata sidecar holding what the `.pulse` header cannot: value labels, measure levels, missing-value specs, response-set definitions, and which columns were derived. `pulse export spss` reproduces that dictionary and folds the derived columns away at every rung (the fold is keyed on the recorded kind, so a 206-constituent `set_u256` drops and rebuilds its constituents exactly as a `set_u8` does); a cohort that never came from SPSS exports on a synthesised one (`PULSE_SPSS_SIDECAR_ABSENT`, a warning), a **stale** sidecar is an error, and `--include` / `--labels` are refused rather than ignored because the writer encodes from raw storage. `pulse convert x.sav out.sav` builds no cohort on disk, so it carries the source's declared schema AND its sidecar into the writer's in-memory intermediate (`pio.ConvertSource`) — without them the rebuilt cohort is re-inferred from rendered text, which loses a `set_*` rung down to the options actually ticked and made every multiple-dichotomy convert refuse with `PULSE_SPSS_NAME_COLLISION`.

## Which skill answers what

| Question | Skill |
|---|---|
| How does each SPSS type map; why no inference; bytecode / ZSAV / charset / byte order; which damage is fatal? | `spss-import-schema` |
| Where do `refused` / `don't know` codes go; the `<var>_missing` sibling; `--spss-missing`; filtering on a missing code | `spss-missing-values` |
| Multiple-dichotomy vs multiple-category sets; the `derived` registry and its closed `kind` vocabulary | `spss-response-sets` |
| What `cohort.pulse.spss.json` holds; absent vs stale vs invalid vs ignored | `spss-metadata-sidecar` |
| Writing `.sav`: the writer, the four flags, `convert`, `export predict` | `spss-export` |
| Variable-name policy, `set_*` masks, nulls, codes vs positions, byte order on the way out | `spss-export-values` |
| What the round-trip claim rests on; the write-side charset rules | `spss-export-fidelity` |

## Dictionaries hold codes, not labels

A labelled variable's `categorical_*` dictionary holds `"1"`, `"2"`, … — source codes in source order, because entry order IS the on-wire encoding. Two SPSS codes may legitimately share one value label, so a label-keyed dictionary collapses them and destroys the code. Labels arrive at **output time** via a `LabelTable`<!-- feature: capability:labels --> (`label-display`)<!-- /feature -->, never from the cohort; the code ↔ label triple lives in the metadata sidecar (`spss-metadata-sidecar`). Text that is a null sentinel (`""`, `NA`, `N/A`, `NULL`) imports as null + `PULSE_SPSS_NULL_TOKEN_COLLISION`.

**`PULSE_LABEL_TABLES_DIR` skips our sidecar.** It parses every `*.json` there as a label table, excluding Pulse's own sidecars by suffix FIRST — `.spss.json` i.e. `cohort.pulse.spss.json` (the `.spss.json` suffix) and the managed-import `cohort.pulse.meta.json` — so a skipped sidecar registers no table and no longer fails `pulse.New`. Exclusion by name, not tolerance: any other unparseable `*.json` still hard-fails naming its path.

## Cross-links

- `cohort-schema-design` — `.pulse` field-type matrix, nullability bitmap, sidecar index.
<!-- feature: capability:labels -->
- `label-display` — resolving SPSS value labels from the codes the cohort stores.
<!-- /feature -->
<!-- feature: capability:import -->
- `tool-import` — the `pulse_import` MCP surface and its SPSS caveats.
<!-- /feature -->
- `session-bootstrap` — `--spss-missing` / `--charset` flag coverage per CLI leaf.
- `docs/src/cli/import-spss.md` — exhaustive user-facing READ reference (worked examples, byte-level detail, R cross-checks).
- `docs/src/cli/export-spss.md` — user-facing WRITE reference (the four flags, sidecar verdicts, derived fold, write-side diagnostic table).
