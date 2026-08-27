---
name: spss-cohorts
description: SPSS .sav / .zsav import and .sav writing — the schema-authoritative dictionary, derived columns (<var>_missing siblings and multiple-dichotomy set_* columns), the numeric-vs-categorical missing-value split, codes-not-labels dictionaries, the metadata sidecar, the dictionary + data-section writer, and the PULSE_SPSS_* diagnostics. Read this when a cohort came from SPSS, when a cohort has more columns than its source file had variables, or when emitting a .sav.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, zsav, import, export, derived columns, user-missing values, multiple-response sets, metadata sidecar]
---

# SPSS cohorts

An SPSS system file is the one import source whose schema Pulse does **not** infer, and the one that can produce a cohort with more columns than the source had variables. Both facts trip people up. This skill is the SPSS surface, read AND write, in one file — the write half only makes sense in terms of the read half's derived columns, sidecar and charset decisions, so splitting it would force an agent to fetch both. `cohort-schema-design` stays the general `.pulse` schema surface.

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

**The fold runs, and derived columns are export-transparent.** `foldDerived` (`io/spss/dict_fold.go`) consumes the registry at plan time — never by name-matching — so an emitted `.sav` carries exactly the source's own variables and no `_missing` or `set_*` artefacts. `restore` binds the sibling to its variable and the encoder then writes the recorded SPSS code back into every null instead of the sysmis sentinel, taking the mapping from `Derived.Reasons` and never re-deriving it. `drop` lets a set column go after checking its constituents really are being emitted. The encoder is driven by `DictionaryPlan.Columns` alone, so an unbound field is decoded (the record stride demands it) and written nowhere — a derived column cannot leak out as a variable even if the fold missed it.

**The audit is worth more than the fold.** `DictionaryPlan.UnboundFields` lists every cohort field no emitted variable is written from; on the sidecar path those should be EXACTLY the derived columns. An unbound field the registry does not account for is `PULSE_SPSS_COLUMN_UNMAPPED` — a column about to leave the export silently, the one outcome this path exists to refuse. It is checked on the synthesised path too, where the registry is empty and every field must bind. A registry entry the binary cannot honour is `PULSE_SPSS_DERIVED_UNFOLDABLE` (four shapes: an unknown `kind` from a newer import, a `numeric_missing` entry missing its `reasons`, an entry naming an unemitted source column, an entry naming a column also being emitted as a variable) — a refusal, because both available guesses are invisible data faults.

A generated sibling name colliding with a real variable (case-insensitively, as SPSS names are) is `PULSE_SPSS_DERIVED_NAME_COLLISION` naming both sides — a hard ERROR for a sibling, only a warning on the MD-set arm, because the set column is pure convenience.

## Dictionaries hold codes, not labels

A `categorical_*` dictionary for a labelled variable holds `"1"`, `"2"`, … — the source's own codes in the source's own order, because entry order IS the on-wire encoding. Two SPSS codes may legitimately share one value label, so a label-keyed dictionary would collapse them and destroy the code. Analysts get labels at **output time** through a `LabelTable` (see `label-display`), never from the cohort. A cell whose text is a null sentinel (`""`, `NA`, `N/A`, `NULL`) imports as null and warns `PULSE_SPSS_NULL_TOKEN_COLLISION`.

**Landmine — `PULSE_LABEL_TABLES_DIR` is not a cohort directory.** The loader parses **every** `*.json` under that directory as a label table and a file it cannot parse is a hard `pulse.New` failure, not a skip. Our sidecar is `cohort.pulse.spss.json` and a managed import writes `cohort.pulse.meta.json` — so pointing the variable at the directory holding your cohorts fails startup with `pulse: parsing label table …/survey.pulse.spss.json`. This is exactly the mistake an analyst chasing labels makes. Keep label tables in their own directory.

## The metadata sidecar

An import writes `cohort.pulse.spss.json` beside the cohort (`spss.SidecarSuffix`, the `imports.Sidecar` naming convention — deliberately NOT `.meta.json`, which a managed import writes for the same cohort). It carries every dictionary element the `.pulse` byte format has no slot for: measure levels, print/write formats, records `7/17` file and `7/18` variable attributes (kept **distinct**), record `6` documents, the weight variable, compression bias, `nominal_case_size`, original short names, declared BYTE widths + `7/14` segmentation, MR/MC set and `7/5` variable-set definitions, the declared charset in the file's own spelling, the product name, and missing-value specs in all three shapes. Every response set is recorded — including MC sets and MD sets that refused to derive — because the block records DEFINITIONS, a different question from which columns are synthetic.

Its load-bearing payload is the **`code ↔ label ↔ Pulse dictionary ID` triple** per categorical column: Pulse IDs are positional, SPSS codes arbitrary, so this is the only place the LABELS live. Per-entry flags `labelled` / `observed` / `missing` keep a declared-but-unused code, an appended unlabelled code and a user-missing code all representable. **This is the file to build a `LabelTable` from** — and the file that breaks `pulse.New` if you point `PULSE_LABEL_TABLES_DIR` at the directory holding it.

The document is `{format_version, kind, fingerprint, payload}`, with `payload` flat and self-contained so it can later be lifted verbatim into a `.pulse` schema metadata block (deferred, not rejected: it needs a `FormatVersion` bump). `fingerprint` is SHA-256 + size + mtime over the **`.pulse` cohort**, not the source `.sav`, mirroring the sidecar index's O(1) staleness check. Written via the optional `io.SidecarEmitter`, called by `ImportJob.Run` **after** the cohort write; a source not implementing it yields a byte-identical import and no sidecar.

### Reading it back — and why absent and stale are not the same answer

`spss.LoadSidecar(fs, cohort, spss.WriterOptions{})` is the read path, and the write side's first act — `pulse export spss` reaches it for you; there is no leaf that reads the sidecar on its own. It answers with a `SidecarResolution`; `resolution.Synthesise()` is the single question — *must I build a default dictionary from the `.pulse` schema alone?*

| State | Verdict | Code | What follows |
|---|---|---|---|
| no file | warning | `PULSE_SPSS_SIDECAR_ABSENT` | synthesise a default; **the normal case** for synth / CSV output |
| size or mtime moved | **error** | `PULSE_SPSS_SIDECAR_STALE` | nothing — no resolution is returned at all |
| not JSON / foreign `kind` / unknown `format_version` / bad digest | **error** | `PULSE_SPSS_SIDECAR_INVALID` | nothing |
| `IgnoreSidecar` set and a file present | warning | `PULSE_SPSS_SIDECAR_IGNORED` | synthesise a default |

**The split is deliberate and overrides a flatter "a lost sidecar is a warning".** Absent is benign — the cohort never had source metadata. Stale is the highest-fidelity-risk state available: the dictionary is complete and plausible, so applying it to changed data yields a `.sav` where `IF q1 EQ 5` addresses a category that moved. It looks authoritative, it is wrong, and nothing downstream can tell. So a refusal returns **no resolution object** — there is no shape in which a caller holds the stale document and writes it by accident.

The check is size + mtime, never a hash, for the reason `PULSE_INDEX_STALE` gives: hashing a multi-GB cohort per export costs more than the export. Same residual gap (an in-place edit preserving both), same authoritative answer — `Document.VerifyDigest(fs, cohort)` recomputes the full SHA-256 for a verify-style pass.

`WriterOptions{IgnoreSidecar: true}` (`--ignore-sidecar` on `pulse export spss` / `pulse convert`) suppresses the **read**, not merely the verdict: a healthy sidecar is ignored too, so the flag's effect never flips with an mtime, and an unreadable one cannot block you. It downgrades both refusals to the warning path. **No option applies a stale dictionary** — the choice is recorded metadata or synthesised default, never fresh or stale.

Loading also normalises: `multiple_response_sets[].fields` arrived additively under `omitempty` without a `SidecarFormatVersion` bump, so an **absent** `fields` key means "written before the slot existed" and is back-filled from `variables[].short_name` (case-insensitive, first declaration wins, unknown member → `""`) rather than rejected. A `fields` array of the **wrong length** is rejected — it is index-for-index with `variables` and a repaired one would bind members to the wrong columns.

## Reading real files

**All three data encodings read** and produce identical cohorts from identical content: uncompressed, **bytecode** (SPSS's save default; the bias comes from the header, not a hardcoded 100), and **ZSAV** (zlib blocks that inflate to a *bytecode* stream — two layers, not a third encoding; `.zsav` also carries `$FL3` instead of `$FL2`). Pulse never writes ZSAV.

**Text is decoded out of the file's charset** — record `7/20` (a NAME), else `7/3` (a numeric code), else UTF-8; on disagreement `7/20` wins. Spellings fold (`windows-1252` = `cp1252` = `1252`) but never approximately (`1250` ≠ `1252`). Two hard rules: an undecodable byte is an error naming variable and value, **never** a U+FFFD substitution; and declared widths are BYTE counts, so padding is trimmed on raw bytes BEFORE decoding. `--charset` / `spss.WithCharset` overrides a file that mislabels itself — **not yet on `pulse import auto` or `pulse_import`**.

**Either byte order reads.** The header layout code decides; record `7/3` corroborates only. A contradiction is FATAL, unlike the charset cross-check one field away, because byte order governs every count, offset and double — the wrong reading yields a whole file of plausible wrong numbers rather than one bad field.

**Damage has distinct codes because it has distinct fixes.** Fatal: `PULSE_SPSS_FILE_EMPTY`, `_DICT_TRUNCATED`, `_DATA_TRUNCATED`, `_DICT_INVALID` (four damage shapes); `_COMPRESSION_INVALID` (a bytecode command landed where it cannot apply — the stream lost sync with the dictionary); `_ZSAV_INVALID` (a self-inconsistent `ZHEADER`/`ZTRAILER` index, naming the block) / `_ZSAV_BLOCK_CORRUPT`; `_ENDIANNESS_MISMATCH`; `_CHARSET_INVALID` / `_CHARSET_UNSUPPORTED`. Warnings, because declining loses nothing: `_CHARSET_MISMATCH`, `_MAGIC_FLAG_MISMATCH`, `_VALUE_LABELS_DROPPED` (an unbindable record `3`/`4` set — a label is display metadata, so refusing the file would cost the data to save the labels), `_VERY_LONG_STRING_INVALID` (an unusable `7/14` record; the segments import as the columns the dictionary literally declares). No input panics.

**Warnings are load-bearing.** Every non-fatal `PULSE_SPSS_*` diagnostic changes what the cohort MEANS. They ride `ImportReport.SourceWarnings` / `ConvertReport.SourceWarnings` (the `io.SourceWarningEmitter` interface), the `--json` envelope's `warnings` array, and `Warning [CODE]` lines on the text path. `pulse errors lookup CODE` carries the per-code fixup.

## Writing `.sav` — `pulse export spss`

`spss.BuildDictionary(spss.DictionaryRequest{Schema, Sidecar, Cases, Compression, Options})` emits the dictionary section — header, record `2` variable records, records `3`/`4` value labels, the `7/*` subtypes, the `999` terminator — and returns a `DictionaryPlan`. `spss.NewDataEncoder(plan, schema)` writes the data section (`WriteCohort(r)` drains a `.pulse` record stream, `WriteCase` takes one record, `Finish()` returns the bytes); the file is `plan.Bytes` followed by them. haven / ReadStat and `foreign` both read every value back exactly, in both write modes, including through the CLI end to end on a file `pulse export spss` produced from a `pulse import spss` cohort. **Those checks `t.Skip` without R installed, which is the state on CI** — see *What the fidelity claim rests on*.

**Bytecode compression is the default** — it is what SPSS's own SAVE writes. `WriterOptions{Uncompressed: true}` (`.Compression()` resolves the header flag) writes flat 8-byte elements instead; the two are losslessly equivalent, so the knob trades size for a readable hex dump. **ZSAV emission is not implemented** (`PULSE_SPSS_COMPRESSION_UNSUPPORTED`): Pulse reads it and does not write it. Pass `Cases: -1` when the count is not known up front and `Finish` patches it via `DictionaryPlan.SetCaseCount`, which writes the header int32 **and** record `7/16` together — two disagreeing counts are a file no reader can adjudicate. A `-1` plan emits no `7/16`, so it carries the header count alone.

### The CLI surface, and the one contract mismatch behind it

`pulse export spss -i cohort.pulse -o out.sav` is the leaf; `pulse convert data.csv out.sav` reaches the same writer. Four flags, each one field of `spss.WriterOptions`: `--ignore-sidecar`, `--uncompressed`, `--charset`, `--sanitise-names`. Per-flag detail is in `session-bootstrap` and `docs/src/cli/export-spss.md`.

**The writer is a `pio.CohortWriter`, not an ordinary row writer.** A `.sav` value is derived from a categorical's dictionary **ID**, a `set_*`'s mask **bits** and the **null bitmap** — all three gone once `ExportJob` has rendered a row (a categorical resolves to label text, and two SPSS codes may share one label; a null renders as `""`, which a string categorical can legitimately hold). So `ExportJob.Run` hands over the cohort path and **skips its row loop entirely**; `WriteRow` is never called. Two consequences: `--include` and `--labels` are **refused** with `PULSE_SPSS_EXPORT_UNSUPPORTED` rather than silently ignored (project or relabel into a narrowed cohort first, then export that), and overlays are **warn-and-skip** like CSV. A `convert` from a text source has no cohort at all, so the writer buffers the rows, builds an intermediate cohort in memory through the ordinary import path, and exports that — inferred schema, no sidecar, and it says so.

**Ask before you write — `pulse export predict --format spss`.** The `.sav` writer is the first Pulse writer that can REFUSE, so `pulse export predict` reads `--format` and consults the target through the optional `pio.CohortValidator` contract (`spss.Writer.ValidateCohort`). It runs the writer's own non-data pass — sidecar resolution, dictionary build, name policy, charset transcode, derived fold — and throws the result away, so a predicted refusal is the export's own check, code for code, and `PULSE_SPSS_SIDECAR_ABSENT` predicts as a warning on `PredictReport.TargetWarnings` exactly as it exports as one. Pass the same write flags you will export with (`--sanitise-names` in particular flips a `PULSE_SPSS_NAME_INVALID` refusal into a warning). **It is sound but INCOMPLETE**: nothing that needs a record — a value past a declared width, a character the target charset cannot form, a dictionary ID with no source code — is reachable without the data pass, so passing means no schema-level refusal was found, never that the export cannot fail. Predict writes no file, and with no `--format` it is target-blind exactly as before.

**Names are policed, and the default is a refusal.** A `.pulse` field name is any UTF-8 string; an SPSS variable name is at most 64 bytes, opens with a letter and is unique without regard to case. All three ways an illegal name fails are quiet (records `7/13`, `7/7` and the case-fold rule each produce a well-formed file saying something else), so an offender is `PULSE_SPSS_NAME_INVALID` / `PULSE_SPSS_NAME_COLLISION`. `--sanitise-names` is the opt-in escape hatch for the synthesised path, where a CSV header's spaces and brackets are ordinary: deterministic, collision-safe (against other renames **and** against names that were already legal, which never move), every rename reported as `PULSE_SPSS_NAME_SANITISED` with the full `field → name` list. Inert on the sidecar path — those names came from SPSS.

**Two landmines worth knowing before you reach for `--ignore-sidecar`.** It **cannot round-trip a cohort whose derived MD `set_*` column is still present**: that column's dictionary entries *are* its constituents' field names, so with the registry suppressed, synthesis mints indicator variables `Q1A`/`Q1B` beside the real `Q1A`/`Q1B` — `PULSE_SPSS_NAME_COLLISION`. Export *without* the flag so the registry folds the column away. And it suppresses the sidecar **read**, not merely the staleness verdict: a healthy sidecar is ignored too, an unreadable one cannot block, and the `PULSE_SPSS_SIDECAR_IGNORED` warning deliberately cannot say which refusal it silenced.

Nulls take the missing state the SPSS type actually has: numeric → the **sysmis sentinel**, string → **blanks** (no string sentinel exists, and blank reads back as null), every member of a null `set_*` → sysmis, which keeps null apart from an empty mask on the way back. Anything with no honest form is `PULSE_SPSS_EXPORT_UNSUPPORTED` naming the variable, never a quiet substitution.

Two rules govern the dictionary. **Original SPSS codes, never dictionary positions** — the sidecar triple supplies them; `IF q1 EQ 5` addresses a value, so renumbering silently re-points every reference. **With no sidecar, nothing is invented**: a categorical becomes a STRING variable holding the dictionary text, a `set_*` expands into one indicator variable per entry (named for that entry, so the mask round-trips) plus a `7/7` dichotomy definition, and `CategoryCode.Known` stays `false` so the plan says which it is.

Two things deliberately do NOT reproduce the source: byte order is always **little-endian** (`7/3` agrees) and `prod_name` identifies pulse. The byte-order corollary is easy to miss — a NUMERIC missing-value slot is a `flt64`, so the sidecar's verbatim slots are **byte-reversed** when the source was big-endian; re-emitting them as read would declare eight bytes that decode here as an unrelated subnormal and the variable would silently stop declaring anything missing. A **string** slot is characters and is never reversed. Records `7/21`/`7/22` carry each variable's **FINAL** name (ReadStat refuses a file that spells the short name there). MOYR/QYR/WKYR keep both raw seconds and format code.

**Value labels declared only on user-missing codes come back from the derived registry, not from the categories.** An income column labelled at `97`/`98` and nowhere else is not a coded variable, so the import maps it to plain `f64` and moves those labels into the sibling's `Derived.Reasons` — the only place they survive. The write path re-emits them as ordinary records `3`/`4`; a categories-only export would bring the *code* back while losing what it MEANT, and the re-imported reason column would read as bare numerals.

### What the fidelity claim rests on

Fidelity is this adapter's whole justification, so be precise about which parts are PROVEN and which rest on the PSPP specification alone.

**Gated in CI.** `TestRoundTrip_*` runs import → export → import over a matrix covering all three source encodings, both MR flavours, all three missing-spec shapes, a non-UTF-8 charset, very long strings and both endiannesses. It asserts the re-imported `.pulse` is **byte-identical** to the first (cohort identity, not `.sav` byte-identity, which is unreachable for the correct reasons above), that the emitted `.sav` is a **fixed point** under a second export, and that it declares exactly the source's own variables. `TestRoundTrip_MatrixCoversFR62` fails if an axis loses its last fixture, so nothing can be quietly skipped.

**Verified locally, not in CI.** The R cross-checks (`io/spss/dict_ecosystem_test.go`, including `TestRoundTrippedFile_ReadsIdenticallyInReadStatAndForeign`) hand emitted and cycled files to haven 2.5.5 (ReadStat — the C reader behind haven, pyreadstat and most of what else opens a `.sav`) and to the independent `foreign` 0.8.91, requiring each reader's reading of the cycled file to match its own reading of the source. Both pass. **They `t.Skip` when R, haven or foreign is missing, which is CI's state** — a recorded local result, not a standing gate.

**Corroborated by no independent reader.** MR set subtypes `7/5` / `7/19` — neither haven nor `foreign` exposes MR metadata at all, so nothing outside Pulse has read our set definitions back; the round trip proves only that Pulse reads what Pulse writes. And the two-`int32` form of record `7/11`: Pulse READS both the two- and three-`int32` shapes but always WRITES the three, so the two-`int32` branch is exercised by synthetic fixtures only.

**Not implemented.** ZSAV emission — read-only, with no partial or degraded path.

### Charset on the way out — the same two hard rules, mirrored

**The file is written in the charset its source declared, in the source's own spelling.** A `cp1252` source goes back out as `cp1252` bytes under a `cp1252` record `7/20`, and `7/3` re-emits the source's own character code — including a stale one, because the disagreement is real information. No sidecar means UTF-8. Emitting UTF-8 under a `windows-1252` header would corrupt every non-ASCII label, so the declaration follows the bytes and the bytes follow the source.

**Never a replacement character.** A character the target charset cannot form is `PULSE_SPSS_CHARSET_UNENCODABLE` naming variable, value and code point — never `?`, `0x1A` or U+FFFD. Usual cause: a cohort edited since import, since text a Pulse operation produced is UTF-8. Every encode is decoded back and compared, so a character that encodes but returns as a *different* one (GB18030 does this across the Private Use Area) is refused too.

**Never a silent truncation.** SPSS widths are BYTE counts, so transcoding moves them (`Zürich` is 6 bytes in `windows-1252`, 7 in UTF-8). Widths are recomputed from the ENCODED bytes and a source-recorded width is only ever **widened** — SPSS pads and the read path trims, so widening loses nothing while narrowing would change a declaration the source made. Where the format fixes the width it is `PULSE_SPSS_WIDTH_OVERFLOW` (a string past 32767 bytes, an 8-byte short name, a 255-byte value label, the 64-byte file label, an 80-byte document line).

**Order is the crux: encode, then measure, then segment.** A >255-byte string is re-segmented per `7/14` on a fixed 252-byte stride, so a multi-byte character *can* straddle a boundary — the reader joins pieces before decoding, exactly as the writer encodes the whole value before slicing. Segmenting the UTF-8 form first would put a partial character on the wire.

`--charset` / `WriterOptions{Charset}` overrides the target — the answer to a cohort whose text outgrew its source codepage. `spss.WithCharset` on the read side changes *decoding* only and is not consulted here. An unwritable name is `PULSE_SPSS_CHARSET_UNSUPPORTED`, never a silent fall back. Records `7/10`, `7/17`, `7/18` pass through **verbatim** (the reader never decodes them), which is the one case where overriding the target is lossy — a non-ASCII payload then rides `PULSE_SPSS_CHARSET_MISMATCH` as a warning rather than being guessed at.

## Cross-links

- `cohort-schema-design` — the `.pulse` field-type matrix, nullability bitmap, sidecar index.
- `label-display` — resolving SPSS value labels from the codes the cohort stores.
- `tool-import` — the `pulse_import` MCP surface and its SPSS caveats.
- `session-bootstrap` — `--spss-missing` / `--charset` flag coverage per CLI leaf.
- `docs/src/cli/import-spss.md` — the exhaustive user-facing READ reference (worked examples, byte-level detail, R cross-checks).
- `docs/src/cli/export-spss.md` — the user-facing WRITE reference (the four flags, the sidecar verdicts, the derived fold, the write-side diagnostic table).
