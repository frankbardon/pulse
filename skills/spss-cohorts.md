---
name: spss-cohorts
description: SPSS .sav / .zsav import — the schema-authoritative dictionary, derived columns (<var>_missing siblings and multiple-dichotomy set_* columns), the numeric-vs-categorical missing-value split, codes-not-labels dictionaries, the metadata sidecar, and the PULSE_SPSS_* diagnostics. Read this when a cohort came from SPSS, or when a cohort has more columns than its source file had variables.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, zsav, import, derived columns, user-missing values, multiple-response sets, metadata sidecar]
---

# SPSS cohorts

An SPSS system file is the one import source whose schema Pulse does **not** infer, and the one that can produce a cohort with more columns than the source had variables. Both facts trip people up. This skill is the SPSS surface; `cohort-schema-design` stays the general `.pulse` schema surface.

## Three things that surprise people first

1. **The cohort has columns the `.sav` never declared.** `<var>_missing` siblings and a `set_*` column per multiple-dichotomy question. They are additive, deliberate, and registered — see *Derived columns*. Count columns from `pulse_inspect` / `ReadHeader`, never from the SPSS variable count.
2. **Categorical columns show codes, not labels.** `"1"` / `"2"`, not `Male` / `Female`. See *Dictionaries hold codes*.
3. **Do not point `PULSE_LABEL_TABLES_DIR` at a cohort directory.** It parses **every** `.json` beneath it and hard-fails `pulse.New` on anything that is not a label table — including the `.spss.json` sidecar sitting next to the cohort. See the landmine note below.

## Schema-authoritative import

The `.sav` dictionary DECLARES every column, so `io/spss` implements `io.SchemaAwareReader` and the sample-and-vote pass in `io/infer.go` is skipped — for `pulse import spss`, `pulse import auto`, `pulse_import` and `pulse convert` alike. So the inference-steering slots (`SampleRows`, `SetInferenceMinPct`, `SetDelimiters`, `ColumnTypeOverrides`) are inert, and there is **no null promotion**: declared nullability is a contract, an unexpected null is `PULSE_IMPORT_ROW_ERROR` rather than a silent widening, and `promoted_fields` is always empty. An explicit `ImportJob.Schema` still wins outright.

| SPSS | Pulse | Why |
|---|---|---|
| numeric (F/E/COMMA/DOT/PCT…) | `f64` | No integer narrowing by range probe — a probe would type two otherwise identical files differently. |
| numeric with value labels | `categorical_u8/u16/u32` | Width from the distinct count; overflow past `u32` → `PULSE_SPSS_CATEGORICAL_OVERFLOW` (hard error — dropping values is worse). |
| string (A*) | `categorical_*` | Near-unique columns warn `PULSE_SPSS_CARDINALITY_HIGH` (free-text signature) and still import. |
| very long string (>255 bytes) | one `categorical_*` column | Record `7/14` segments it across several physical variables; Pulse rejoins the RAW bytes and decodes once. |
| DATE/ADATE/EDATE/SDATE/JDATE | `date`, or `datetime` on `PULSE_SPSS_DATE_WIDENED` | Widens when a value carries a time of day or predates 1970 — `date` is an unsigned epoch **day**. |
| DATETIME/TIME/DTIME | `datetime` (epoch **seconds**) | A fractional-second / non-finite / out-of-int64 value demotes the column to `f64` raw SPSS seconds with `PULSE_SPSS_TEMPORAL_PRECISION`. |
| system-missing (sysmis) | null (bitmap bit) | The one missing state the format has a sentinel for. |
| numeric user-missing | null + a `<var>_missing` sibling | See *Missing values*. |
| categorical/string user-missing | ordinary dictionary entries, kept verbatim and FLAGGED | See *Missing values*. |
| multiple-DICHOTOMY set (`7/5`, `7/7`, `7/19`) | every constituent as its own column, PLUS a derived `set_*` | Additive, never a replacement. |
| multiple-CATEGORY set | N separate `categorical_*` columns; definition on the sidecar only | Positional and duplicate-tolerant, so it is genuinely not a set. |

**A value-label set naming only missing codes does NOT make a variable categorical.** `INCOME` labelled solely on 97/98/99 is a continuous measurement whose labels annotate its missing states. The rule: labels code the variable when at least one names a value that is neither user-missing nor sysmis.

## Missing values — and why the two arms differ

SPSS separates `refused` / `don't know` / `not applicable` / `sysmis`. The Pulse null bitmap is ONE bit: it records *that* a value is absent, never *why*. Both naive mappings lose — keeping the codes as data makes `AGG_SUM` add 99999 per refusal; collapsing everything to null destroys the item-non-response distinction survey weighting depends on. So the mapping splits, **by substrate**:

| | Numeric | Categorical / string |
|---|---|---|
| Where can the reason live? | Nowhere — `f64` has no dictionary, the bitmap is one bit | Already there — the code IS a dictionary entry |
| Mapping | analytic column nulled + generated `<var>_missing` sibling | code kept verbatim, entry FLAGGED |
| Cost of the other choice | reason lost outright | a redundant sibling on EVERY item of an all-categorical survey — 200 questions become 400 columns for no new information |
| `--spss-missing` | `auto` (sibling) / `null` (no sibling) | no effect — there is no sibling to suppress |

**The asymmetry is deliberate; do not "fix" it.** Both arms are "preserve, do not degrade" and differ only because the substrate does.

**The numeric sibling.** `<var>_missing` sits immediately after its source as a `categorical_u8` (widening as needed). Dictionary ID `0` is always `"sysmis"`, then the DECLARED discrete codes in spec order, then every further missing value the data carried, first-seen. **A range is never enumerated** — only observed members get entries, which is what drives the widening. The reason text is the file's value label for that code, else the code itself; a label colliding with another reason loses to the code and `PULSE_SPSS_VALUE_COLLISION` warns. A PRESENT value renders the sibling null — the empty reason is the bitmap bit, deliberately not a dictionary entry. `--spss-missing=null` / `spss.WithMissingMode(spss.MissingNull)` drops the siblings: identical nulls, reason no longer per-row (the specification still rides the sidecar). An unrecognised mode is `PULSE_SPSS_MISSING_MODE_INVALID`, never a silent default.

**The categorical flag.** `Q1: 1=Yes, 2=No, 9=Refused` maps to a `categorical_u8` holding `"1"`, `"2"`, `"9"`; the refused row still says `9`. Record `7/22` long-string missing values bind to the same `variable.missing` slot a record type 2 spec does, so strings need no branch. WHICH entries are missing-coded is recorded on two surfaces: the sidecar's per-entry `variables[].categories[].missing` (additive `omitempty` — a file with no such codes writes a byte-identical document and `SidecarFormatVersion` does not move), and `PULSE_SPSS_CATEGORICAL_USER_MISSING` at import — **one informational diagnostic per FILE**, never per variable, prose naming the first few and `Details["missing_categories"]` carrying every field → flagged-entries pair uncapped. Nothing is wrong when it fires; the loss it guards is downstream, where a percentage base silently includes the refusal category.

**Exclude over the CODE:** `{"type":"FILTER_EXCLUDE","field":"Q1","values":["9"]}` — never `["Refused"]`. The dictionary holds codes, and a value not in it is a loud `PROCESSING_CONFIG` rather than a filter matching nothing.

## Multiple-response sets

SPSS is the one source that DECLARES a multi-select question; every other path guesses one from delimited strings. A multiple-**dichotomy** set therefore gets a derived `set_*` bitmask — but **beside** its constituents, never instead of them, because a bit cannot separate `Q1B=0` ("shown it, did not pick it") from `Q1B=.` ("never asked"). An MD set yields **N + 1** columns: every constituent keeps its own null bit and its own `<var>_missing` sibling, and the derived column sits immediately after the LAST constituent (a summary must not precede its parts).

- The dictionary holds constituent **field names**, not option labels — names are unique in a cohort so the dictionary is injective for free. Labels stay on the sidecar's `variables[].label`.
- The mask uses the **declared counted value**, never a guessed `1`. A user-missing code sets no bit and is not evidence the row was answered.
- The set name loses its `$` (`$media` → `media`) — a leading sigil is not a legal expr-lang identifier. The full name rides `derived[].set_name`.
- **Three row states.** Some option selected → bits set. Answered but nothing selected → **empty mask**, a real "none of these", NOT null. Every constituent missing → null.
- `PULSE_SPSS_MR_SET_NOT_DERIVED` (a WARNING; the import still succeeds) fires when the set exceeds **64 constituents** (there is no wider set type), names an undeclared or duplicated member, carries a counted value that will not compare against a numeric member, or has a constituent whose Pulse field name contains the set delimiter `|` or IS a null token. The additive design paying out: a set that does not derive costs ergonomics, never data.

**Multiple-CATEGORY sets derive nothing, and that is a fidelity call.** An MC set is N answer SLOTS over a shared value-label set, so slot ORDER ("first choice" vs "third") and a REPEATED code (two slots may both hold `2`) are both real; a bitmask is unordered and idempotent and would destroy both. Each member imports as an ordinary `categorical_*` column exactly as if the definition were absent; only the definition rides the sidecar.

## Derived columns and the `derived` registry

`payload.derived` names every column the import SYNTHESISED — and a column absent from it is a source variable **by construction**. Name-matching cannot substitute: `_missing` is a legal SPSS name suffix (a survey declaring `income_missing` is not hypothetical) and a set column's name matches no pattern at all.

| `kind` | Fold action | Opt out |
|---|---|---|
| `numeric_missing` | CONSUMED — its per-row ID decides what its one source variable writes wherever that variable is null | `--spss-missing=null` |
| `multiple_dichotomy` | DROPPED — every bit re-reads a constituent still in the cohort | none by design; costs one column per set |

`kind` is a **CLOSED vocabulary** (`spss.DerivedKinds()`), each mapping to one action via `spss.DerivedFoldFor`, which reports `false` for anything else so an older binary meeting a newer document stops rather than defaults. Every entry is self-sufficient (`Derived.Complete()`): a reason sibling carries its reason dictionary (ID ↔ reason ↔ SPSS code ↔ label), the only record of which state each row was in; a set column carries `set_name` plus `sources` in BIT order. Derived columns are INTERLEAVED, so `variables[].position` is a cohort position, not a source ordinal. **A cohort that derived nothing writes `"derived": []`, never a missing key** — "nothing was derived" and "this document cannot tell you" are different answers.

There is **no SPSS writer today**, so no fold runs; the registry exists so that when one lands it is mechanical rather than heuristic.

A generated sibling name colliding with a real variable (case-insensitively, as SPSS names are) is `PULSE_SPSS_DERIVED_NAME_COLLISION` naming both sides — a hard ERROR for a sibling, only a warning on the MD-set arm, because the set column is pure convenience.

## Dictionaries hold codes, not labels

A `categorical_*` dictionary for a labelled variable holds `"1"`, `"2"`, … — the source's own codes in the source's own order, because entry order IS the on-wire encoding. Two SPSS codes may legitimately share one value label, so a label-keyed dictionary would collapse them and destroy the code. Analysts get labels at **output time** through a `LabelTable` (see `label-display`), never from the cohort. A cell whose text is a null sentinel (`""`, `NA`, `N/A`, `NULL`) imports as null and warns `PULSE_SPSS_NULL_TOKEN_COLLISION`.

**Landmine — `PULSE_LABEL_TABLES_DIR` is not a cohort directory.** The loader parses **every** `*.json` under that directory as a label table and a file it cannot parse is a hard `pulse.New` failure, not a skip. Our sidecar is `cohort.pulse.spss.json` and a managed import writes `cohort.pulse.meta.json` — so pointing the variable at the directory holding your cohorts fails startup with `pulse: parsing label table …/survey.pulse.spss.json`. This is exactly the mistake an analyst chasing labels makes. Keep label tables in their own directory.

## The metadata sidecar

An import writes `cohort.pulse.spss.json` beside the cohort (`spss.SidecarSuffix`, the `imports.Sidecar` naming convention — deliberately NOT `.meta.json`, which a managed import writes for the same cohort). It carries every dictionary element the `.pulse` byte format has no slot for: measure levels, print/write formats, records `7/17` file and `7/18` variable attributes (kept **distinct**), record `6` documents, the weight variable, compression bias, `nominal_case_size`, original short names, declared BYTE widths + `7/14` segmentation, MR/MC set and `7/5` variable-set definitions, the declared charset in the file's own spelling, the product name, and missing-value specs in all three shapes. Every response set is recorded — including MC sets and MD sets that refused to derive — because the block records DEFINITIONS, a different question from which columns are synthetic.

Its load-bearing payload is the **`code ↔ label ↔ Pulse dictionary ID` triple** per categorical column: Pulse IDs are positional, SPSS codes arbitrary, so this is the only place the LABELS live. Per-entry flags `labelled` / `observed` / `missing` keep a declared-but-unused code, an appended unlabelled code and a user-missing code all representable. **This is the file to build a `LabelTable` from** — and the file that breaks `pulse.New` if you point `PULSE_LABEL_TABLES_DIR` at the directory holding it.

The document is `{format_version, kind, fingerprint, payload}`, with `payload` flat and self-contained so it can later be lifted verbatim into a `.pulse` schema metadata block (deferred, not rejected: it needs a `FormatVersion` bump). `fingerprint` is SHA-256 + size + mtime over the **`.pulse` cohort**, not the source `.sav`, mirroring the sidecar index's O(1) staleness check. Written via the optional `io.SidecarEmitter`, called by `ImportJob.Run` **after** the cohort write; a source not implementing it yields a byte-identical import and no sidecar.

### Reading it back — and why absent and stale are not the same answer

`spss.LoadSidecar(fs, cohort, spss.WriterOptions{})` is the library read path (there is no CLI leaf yet). It answers with a `SidecarResolution`; `resolution.Synthesise()` is the single question — *must I build a default dictionary from the `.pulse` schema alone?*

| State | Verdict | Code | What follows |
|---|---|---|---|
| no file | warning | `PULSE_SPSS_SIDECAR_ABSENT` | synthesise a default; **the normal case** for synth / CSV output |
| size or mtime moved | **error** | `PULSE_SPSS_SIDECAR_STALE` | nothing — no resolution is returned at all |
| not JSON / foreign `kind` / unknown `format_version` / bad digest | **error** | `PULSE_SPSS_SIDECAR_INVALID` | nothing |
| `IgnoreSidecar` set and a file present | warning | `PULSE_SPSS_SIDECAR_IGNORED` | synthesise a default |

**The split is deliberate and overrides a flatter "a lost sidecar is a warning".** Absent is benign — the cohort never had source metadata. Stale is the highest-fidelity-risk state available: the dictionary is complete and plausible, so applying it to changed data yields a `.sav` where `IF q1 EQ 5` addresses a category that moved. It looks authoritative, it is wrong, and nothing downstream can tell. So a refusal returns **no resolution object** — there is no shape in which a caller holds the stale document and writes it by accident.

The check is size + mtime, never a hash, for the reason `PULSE_INDEX_STALE` gives: hashing a multi-GB cohort per export costs more than the export. Same residual gap (an in-place edit preserving both), same authoritative answer — `Document.VerifyDigest(fs, cohort)` recomputes the full SHA-256 for a verify-style pass.

`WriterOptions{IgnoreSidecar: true}` (a `--ignore-sidecar` flag once the export leaf lands) suppresses the **read**, not merely the verdict: a healthy sidecar is ignored too, so the flag's effect never flips with an mtime, and an unreadable one cannot block you. It downgrades both refusals to the warning path. **No option applies a stale dictionary** — the choice is recorded metadata or synthesised default, never fresh or stale.

Loading also normalises: `multiple_response_sets[].fields` arrived additively under `omitempty` without a `SidecarFormatVersion` bump, so an **absent** `fields` key means "written before the slot existed" and is back-filled from `variables[].short_name` (case-insensitive, first declaration wins, unknown member → `""`) rather than rejected. A `fields` array of the **wrong length** is rejected — it is index-for-index with `variables` and a repaired one would bind members to the wrong columns.

## Reading real files

**All three data encodings read** and produce identical cohorts from identical content: uncompressed, **bytecode** (SPSS's save default; the bias comes from the header, not a hardcoded 100), and **ZSAV** (zlib blocks that inflate to a *bytecode* stream — two layers, not a third encoding; `.zsav` also carries `$FL3` instead of `$FL2`). Pulse never writes ZSAV.

**Text is decoded out of the file's charset** — record `7/20` (a NAME), else `7/3` (a numeric code), else UTF-8; on disagreement `7/20` wins. Spellings fold (`windows-1252` = `cp1252` = `1252`) but never approximately (`1250` ≠ `1252`). Two hard rules: an undecodable byte is an error naming variable and value, **never** a U+FFFD substitution; and declared widths are BYTE counts, so padding is trimmed on raw bytes BEFORE decoding. `--charset` / `spss.WithCharset` overrides a file that mislabels itself — **not yet on `pulse import auto` or `pulse_import`**.

**Either byte order reads.** The header layout code decides; record `7/3` corroborates only. A contradiction is FATAL, unlike the charset cross-check one field away, because byte order governs every count, offset and double — the wrong reading yields a whole file of plausible wrong numbers rather than one bad field.

**Damage has distinct codes because it has distinct fixes.** Fatal: `PULSE_SPSS_FILE_EMPTY`, `_DICT_TRUNCATED`, `_DATA_TRUNCATED`, `_DICT_INVALID` (four damage shapes); `_COMPRESSION_INVALID` (a bytecode command landed where it cannot apply — the stream lost sync with the dictionary); `_ZSAV_INVALID` (a self-inconsistent `ZHEADER`/`ZTRAILER` index, naming the block) / `_ZSAV_BLOCK_CORRUPT`; `_ENDIANNESS_MISMATCH`; `_CHARSET_INVALID` / `_CHARSET_UNSUPPORTED`. Warnings, because declining loses nothing: `_CHARSET_MISMATCH`, `_MAGIC_FLAG_MISMATCH`, `_VALUE_LABELS_DROPPED` (an unbindable record `3`/`4` set — a label is display metadata, so refusing the file would cost the data to save the labels), `_VERY_LONG_STRING_INVALID` (an unusable `7/14` record; the segments import as the columns the dictionary literally declares). No input panics.

**Warnings are load-bearing.** Every non-fatal `PULSE_SPSS_*` diagnostic changes what the cohort MEANS. They ride `ImportReport.SourceWarnings` / `ConvertReport.SourceWarnings` (the `io.SourceWarningEmitter` interface), the `--json` envelope's `warnings` array, and `Warning [CODE]` lines on the text path. `pulse errors lookup CODE` carries the per-code fixup.

## Read-only

There is still no SPSS writer. An SPSS *output* target returns `PULSE_SPSS_EXPORT_UNSUPPORTED` — deliberately distinct from an unknown-format error, because the extension IS recognised. `pulse convert survey.sav out.csv` works; `pulse convert data.csv out.sav` does not, and neither `pulse export` nor `newWriterForFormat` has a `.sav` arm.

What exists today is the write path's **input** side only: `spss.LoadSidecar` + `spss.WriterOptions` (above) and the `derived` registry's fold vocabulary. Nothing emits `.sav` bytes, so no fold runs yet.

## Cross-links

- `cohort-schema-design` — the `.pulse` field-type matrix, nullability bitmap, sidecar index.
- `label-display` — resolving SPSS value labels from the codes the cohort stores.
- `tool-import` — the `pulse_import` MCP surface and its SPSS caveats.
- `session-bootstrap` — `--spss-missing` / `--charset` flag coverage per CLI leaf.
- `docs/src/cli/import-spss.md` — the exhaustive user-facing reference (worked examples, byte-level detail, R cross-checks).
