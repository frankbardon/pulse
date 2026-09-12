---
name: spss-cohorts
description: SPSS .sav / .zsav import and .sav writing — the schema-authoritative dictionary, derived columns (<var>_missing siblings and multiple-dichotomy set_* columns), the numeric-vs-categorical missing-value split, codes-not-labels dictionaries, the metadata sidecar, the dictionary + data-section writer, and the PULSE_SPSS_* diagnostics. Read this when a cohort came from SPSS, when a cohort has more columns than its source file had variables, or when emitting a .sav.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, sav, zsav, import, export, derived columns, user-missing values, multiple-response sets, metadata sidecar]
---

# SPSS cohorts

The one import source whose schema Pulse does **not** infer, and the one producing a cohort wider than its source dictionary. Read and write in one file — the write half is defined by the read half's derived columns, sidecar and charset. General `.pulse` schema surface: `cohort-schema-design`.

## Three things that surprise people first

1. **Columns the `.sav` never declared** — `<var>_missing` siblings, one `set_*` per multiple-dichotomy question. Count columns from `pulse_inspect` / `ReadHeader`, never from the SPSS variable count.
2. **Categorical columns hold codes** — `"1"` / `"2"`, not `Male` / `Female`.
3. **Never point `PULSE_LABEL_TABLES_DIR` at a cohort directory** — it parses every `.json` beneath it.

## Schema-authoritative import

`.sav` declares every column ⇒ `io/spss` implements `io.SchemaAwareReader`, `io/infer.go`'s sample-and-vote pass skipped for `pulse import spss` / `pulse import auto` / `pulse_import` / `pulse convert` alike. Hence:

- Inference-steering slots inert: `SampleRows`, `SetInferenceMinPct`, `SetDelimiters`, `ColumnTypeOverrides`.
- **No null promotion** — declared nullability is a contract; an unexpected null is `PULSE_IMPORT_ROW_ERROR`, never a silent widening. `promoted_fields` always empty.
- Explicit `ImportJob.Schema` still wins.

| SPSS | Pulse | Why |
|---|---|---|
| numeric (F/E/COMMA/DOT/PCT…) | `f64` | no range-probe narrowing — a probe types two identical files differently |
| numeric with value labels | `categorical_u8/u16/u32` | width from distinct count; past `u32` → `PULSE_SPSS_CATEGORICAL_OVERFLOW` (hard; dropping values is worse) |
| string (A*) | `categorical_*` | near-unique → `PULSE_SPSS_CARDINALITY_HIGH` (free-text signature), still imports |
| very long string (>255 bytes) | one `categorical_*` | record `7/14` segments it; Pulse rejoins RAW bytes, decodes once |
| DATE/ADATE/EDATE/SDATE/JDATE | `date`, else `datetime` + `PULSE_SPSS_DATE_WIDENED` | widens on a time-of-day or pre-1970 value — `date` is unsigned epoch **days** |
| DATETIME/TIME/DTIME | `datetime` (epoch **seconds**) | fractional-second / non-finite / out-of-int64 demotes to `f64` raw SPSS seconds + `PULSE_SPSS_TEMPORAL_PRECISION` |
| system-missing (sysmis) | null (bitmap bit) | the one missing state with a format sentinel |
| numeric user-missing | null + `<var>_missing` sibling | *Missing values* |
| categorical/string user-missing | dictionary entries, verbatim + FLAGGED | *Missing values* |
| multiple-DICHOTOMY set (`7/5`, `7/7`, `7/19`) | every constituent PLUS a derived `set_*` | additive, never a replacement |
| multiple-CATEGORY set | N `categorical_*`; definition sidecar-only | positional, duplicate-tolerant ⇒ not a set |

**Labels naming only missing codes do NOT make a variable categorical** — `INCOME` labelled solely on 97/98/99 is continuous. Labels code the variable iff one names a value that is neither user-missing nor sysmis.

## Missing values — and why the two arms differ

SPSS separates `refused` / `don't know` / `not applicable` / `sysmis`; the Pulse bitmap is ONE bit — *that* a value is absent, never *why*. Both naive mappings lose: codes-as-data makes `AGG_SUM` add 99999 per refusal; all-to-null destroys the item-non-response distinction weighting needs. Split **by substrate**:

| | Numeric | Categorical / string |
|---|---|---|
| where can the reason live? | nowhere — `f64` has no dictionary, bitmap is one bit | already there — the code IS a dictionary entry |
| mapping | column nulled + generated `<var>_missing` sibling | code verbatim, entry FLAGGED |
| cost of the other choice | reason lost outright | a redundant sibling on EVERY item — 200 questions → 400 columns, no new information |
| `--spss-missing` | `auto` (sibling) / `null` (none) | no effect — no sibling to suppress |

**The asymmetry is deliberate; do not "fix" it.** Both arms preserve rather than degrade; only the substrate differs.

**Numeric sibling** `<var>_missing` — `categorical_u8` (widens), immediately after its source.

- Dict ID `0` = `"sysmis"`, then DECLARED discrete codes in spec order, then further observed missing values first-seen. **Ranges are never enumerated** — observed members only, which drives the widening.
- Reason text = the file's value label for the code, else the code. Label colliding with another reason loses to the code + `PULSE_SPSS_VALUE_COLLISION`.
- PRESENT value ⇒ sibling null: the empty reason is the bitmap bit, not an entry.
- `--spss-missing=null` / `spss.WithMissingMode(spss.MissingNull)` drops siblings — same nulls, reason no longer per-row (the specification still rides the sidecar). Unrecognised mode ⇒ `PULSE_SPSS_MISSING_MODE_INVALID`, never a default.

**Categorical flag** — `Q1: 1=Yes, 2=No, 9=Refused` → `categorical_u8` holding `"1"`, `"2"`, `"9"`; the refused row still says `9`. Record `7/22` long-string missing values bind to the same `variable.missing` slot a record type 2 spec does, so strings need no branch. Which entries are missing-coded is recorded twice:

- sidecar `variables[].categories[].missing` — additive `omitempty`; no such codes ⇒ byte-identical document, `SidecarFormatVersion` unmoved.
- `PULSE_SPSS_CATEGORICAL_USER_MISSING` — **one informational diagnostic per FILE**, never per variable; prose names the first few, `Details["missing_categories"]` carries every field → entries pair uncapped. Nothing is wrong when it fires; the loss is downstream, where a percentage base silently includes the refusal category.

**Exclude over the CODE:** `{"type":"FILTER_EXCLUDE","field":"Q1","values":["9"]}`, never `["Refused"]` — a value outside the dictionary is a loud `PROCESSING_CONFIG`, not a filter matching nothing.

## Multiple-response sets

SPSS is the one source that DECLARES a multi-select; every other path guesses from delimited strings. A multiple-**dichotomy** set gets a derived `set_*` mask **beside** its constituents, never instead — a bit cannot separate `Q1B=0` ("shown, not picked") from `Q1B=.` ("never asked"). MD set ⇒ **N + 1** columns; each constituent keeps its own null bit and `<var>_missing` sibling; the derived column follows the LAST constituent (a summary must not precede its parts).

- Dictionary holds constituent **field names**, not option labels — cohort-unique, so injective for free. Labels stay on `variables[].label`.
- Mask uses the **declared counted value**, never a guessed `1`. A user-missing code sets no bit, and is not evidence of an answer.
- Set name loses its `$` (`$media` → `media`) — a sigil is no legal expr-lang identifier. Full name on `derived[].set_name`.
- **Three row states:** option(s) selected → bits set; answered, nothing selected → **empty mask**, a real "none of these", NOT null; all constituents missing → null.
- `PULSE_SPSS_MR_SET_NOT_DERIVED` — WARNING, import succeeds — on >**64 constituents** (no wider set type), an undeclared or duplicated member, a counted value that will not compare against a numeric member, or a constituent whose field name holds the set delimiter `|` or IS a null token. The additive design paying out: a set that does not derive costs ergonomics, never data.

**Multiple-CATEGORY sets derive nothing — a fidelity call.** N answer SLOTS over a shared value-label set, so slot ORDER ("first choice" vs "third") and a REPEATED code (two slots both `2`) are real; a bitmask is unordered and idempotent. Members import as ordinary `categorical_*` as if the definition were absent; only the definition rides the sidecar.

## Derived columns and the `derived` registry

`payload.derived` names every SYNTHESISED column ⇒ a column absent from it is a source variable **by construction**. Name-matching cannot substitute: `_missing` is a legal SPSS suffix (`income_missing` is not hypothetical) and a set column matches no pattern.

| `kind` | Fold action | Opt out |
|---|---|---|
| `numeric_missing` | CONSUMED — its per-row ID decides what its one source variable writes wherever that variable is null | `--spss-missing=null` |
| `multiple_dichotomy` | DROPPED — every bit re-reads a constituent still in the cohort | none by design; one column per set |

- `kind` is a **CLOSED vocabulary** (`spss.DerivedKinds()`), one action each via `spss.DerivedFoldFor`, which reports `false` otherwise — an older binary meeting a newer document stops rather than defaults.
- Entries are self-sufficient (`Derived.Complete()`): a reason sibling carries its reason dictionary (ID ↔ reason ↔ SPSS code ↔ label), the only record of which state each row was in; a set column carries `set_name` + `sources` in BIT order.
- Derived columns INTERLEAVE ⇒ `variables[].position` is a cohort position, not a source ordinal.
- **Nothing derived ⇒ `"derived": []`, never a missing key** — "nothing derived" and "cannot tell you" are different answers.

**Export-transparent by fold, not by name.** `foldDerived` (`io/spss/dict_fold.go`) consumes the registry at plan time, so an emitted `.sav` carries exactly the source's own variables. `restore` binds a sibling to its variable; the encoder then writes the recorded SPSS code into every null instead of sysmis, from `Derived.Reasons`, never re-derived. `drop` releases a set column after checking its constituents are emitted. The encoder is driven by `DictionaryPlan.Columns` alone, so an unbound field is decoded (the record stride demands it) and written nowhere — a derived column cannot leak out as a variable even if the fold missed it.

**The audit is worth more than the fold.** `DictionaryPlan.UnboundFields` = every cohort field no emitted variable is written from; on the sidecar path, exactly the derived columns. Unaccounted ⇒ `PULSE_SPSS_COLUMN_UNMAPPED`: a column leaving the export silently, the outcome this path exists to refuse. Checked on the synthesised path too, where the registry is empty and every field must bind. An entry the binary cannot honour ⇒ `PULSE_SPSS_DERIVED_UNFOLDABLE`, four shapes — unknown `kind` from a newer import; `numeric_missing` missing its `reasons`; an entry naming an unemitted source column; an entry naming a column also emitted as a variable. A refusal: both available guesses are invisible data faults.

Sibling name colliding with a real variable (case-insensitively, as SPSS names are) ⇒ `PULSE_SPSS_DERIVED_NAME_COLLISION` naming both sides — hard ERROR for a sibling, warning on the MD-set arm (pure convenience).

## Dictionaries hold codes, not labels

A labelled variable's `categorical_*` dictionary holds `"1"`, `"2"`, … — source codes in source order, because entry order IS the on-wire encoding. Two SPSS codes may legitimately share one value label, so a label-keyed dictionary collapses them and destroys the code. Labels arrive at **output time** via a `LabelTable` (`label-display`), never from the cohort. Text that is a null sentinel (`""`, `NA`, `N/A`, `NULL`) imports as null + `PULSE_SPSS_NULL_TOKEN_COLLISION`.

**`PULSE_LABEL_TABLES_DIR` skips our sidecar.** It parses every `*.json` there as a label table, excluding Pulse's own sidecars by suffix FIRST — `.spss.json` i.e. `cohort.pulse.spss.json` (`spss.SidecarSuffix`) and the managed-import `cohort.pulse.meta.json` — so a skipped sidecar registers no table and no longer fails `pulse.New`. Exclusion by name, not tolerance: any other unparseable `*.json` still hard-fails naming its path.

## The metadata sidecar

An import writes `cohort.pulse.spss.json` beside the cohort (`spss.SidecarSuffix`, the `imports.Sidecar` convention — NOT `.meta.json`, which a managed import writes for the same cohort). It holds every dictionary element the `.pulse` format has no slot for: measure levels, print/write formats, records `7/17` file and `7/18` variable attributes (kept **distinct**), record `6` documents, weight variable, compression bias, `nominal_case_size`, original short names, declared BYTE widths + `7/14` segmentation, MR/MC set and `7/5` variable-set definitions, the declared charset in the file's own spelling, product name, missing-value specs in all three shapes. EVERY response set is recorded — MC sets and MD sets that refused to derive included — because the block records DEFINITIONS, a different question from which columns are synthetic.

Load-bearing payload: the **`code ↔ label ↔ Pulse dictionary ID` triple** per categorical column. Pulse IDs are positional, SPSS codes arbitrary ⇒ the only place the LABELS live. Per-entry flags `labelled` / `observed` / `missing` keep a declared-but-unused code, an appended unlabelled code and a user-missing code all representable. **Build a `LabelTable` from this file.**

Document `{format_version, kind, fingerprint, payload}`; `payload` flat and self-contained so it can later be lifted verbatim into a `.pulse` schema metadata block (deferred, not rejected — needs a `FormatVersion` bump). `fingerprint` = SHA-256 + size + mtime over the **`.pulse` cohort**, not the source `.sav`, mirroring the sidecar index's O(1) staleness check. Written via the optional `io.SidecarEmitter`, called by `ImportJob.Run` **after** the cohort write; a source not implementing it ⇒ byte-identical import, no sidecar.

### Reading it back — and why absent and stale are not the same answer

`spss.LoadSidecar(fs, cohort, spss.WriterOptions{})` — read path and the write side's first act (`pulse export spss` reaches it for you; no leaf reads the sidecar alone). Returns a `SidecarResolution`; `resolution.Synthesise()` is the single question — *must I build a default dictionary from the `.pulse` schema alone?*

| State | Verdict | Code | Then |
|---|---|---|---|
| no file | warning | `PULSE_SPSS_SIDECAR_ABSENT` | synthesise a default; **the normal case** for synth / CSV output |
| size or mtime moved | **error** | `PULSE_SPSS_SIDECAR_STALE` | nothing — no resolution returned at all |
| not JSON / foreign `kind` / unknown `format_version` / bad digest | **error** | `PULSE_SPSS_SIDECAR_INVALID` | nothing |
| `IgnoreSidecar` set, file present | warning | `PULSE_SPSS_SIDECAR_IGNORED` | synthesise a default |

- **The split overrides a flatter "a lost sidecar is a warning".** Absent is benign — the cohort never had source metadata. Stale is the highest-fidelity-risk state there is: a complete, plausible dictionary over changed data yields a `.sav` where `IF q1 EQ 5` addresses a category that moved — authoritative-looking, wrong, undetectable downstream. So a refusal returns **no resolution object**; no shape exists in which a caller holds the stale document and writes it by accident.
- Size + mtime, never a hash, for `PULSE_INDEX_STALE`'s reason: hashing a multi-GB cohort per export costs more than the export. Same residual gap (an in-place edit preserving both); `Document.VerifyDigest(fs, cohort)` is the full SHA-256 recompute.
- `WriterOptions{IgnoreSidecar: true}` (`--ignore-sidecar` on `pulse export spss` / `pulse convert`) suppresses the **read**, not the verdict: a healthy sidecar is ignored too (the flag never flips with an mtime), an unreadable one cannot block, both refusals downgrade to the warning path, and the warning deliberately cannot say which refusal it silenced. **No option applies a stale dictionary** — recorded metadata or synthesised default, never fresh-or-stale.
- Load normalises `multiple_response_sets[].fields`: additive under `omitempty` with no `SidecarFormatVersion` bump, so an ABSENT `fields` key means "written before the slot existed" ⇒ back-filled from `variables[].short_name` (case-insensitive, first declaration wins, unknown member → `""`). WRONG LENGTH ⇒ rejected — index-for-index with `variables`, and a repair would bind members to the wrong columns.

## Reading real files

**All three data encodings read**, identical cohorts from identical content: uncompressed; **bytecode** (SPSS's save default — bias from the header, not a hardcoded 100); **ZSAV** (zlib blocks inflating to a *bytecode* stream — two layers, not a third encoding; `.zsav` carries `$FL3` not `$FL2`). Pulse never writes ZSAV.

**Text decodes out of the file's charset** — record `7/20` (a NAME), else `7/3` (numeric code), else UTF-8; on disagreement `7/20` wins. Spellings fold (`windows-1252` = `cp1252` = `1252`), never approximately (`1250` ≠ `1252`). Two hard rules: an undecodable byte errors naming variable and value, **never** a U+FFFD substitution; declared widths are BYTE counts, so padding is trimmed on raw bytes BEFORE decoding. `--charset` / `spss.WithCharset` overrides a self-mislabelling file — **not yet on `pulse import auto` or `pulse_import`**.

**Either byte order reads.** The header layout code decides; record `7/3` corroborates only. A contradiction is FATAL — unlike the charset cross-check one field away — because byte order governs every count, offset and double: the wrong reading yields a whole file of plausible wrong numbers.

**Damage has distinct codes because it has distinct fixes.**

- Fatal: `PULSE_SPSS_FILE_EMPTY`, `_DICT_TRUNCATED`, `_DATA_TRUNCATED`, `_DICT_INVALID` (four damage shapes); `_COMPRESSION_INVALID` (a bytecode command landed where it cannot apply — stream lost sync with the dictionary); `_ZSAV_INVALID` (self-inconsistent `ZHEADER`/`ZTRAILER` index, naming the block) / `_ZSAV_BLOCK_CORRUPT`; `_ENDIANNESS_MISMATCH`; `_CHARSET_INVALID` / `_CHARSET_UNSUPPORTED`.
- Warnings, because declining loses nothing: `_CHARSET_MISMATCH`, `_MAGIC_FLAG_MISMATCH`, `_VALUE_LABELS_DROPPED` (unbindable record `3`/`4` set — a label is display metadata, so refusing the file would cost the data to save the labels), `_VERY_LONG_STRING_INVALID` (unusable `7/14`; segments import as the columns the dictionary literally declares).
- No input panics.

**Warnings are load-bearing.** Every non-fatal `PULSE_SPSS_*` diagnostic changes what the cohort MEANS. They ride `ImportReport.SourceWarnings` / `ConvertReport.SourceWarnings` (the `io.SourceWarningEmitter` interface), the `--json` envelope's `warnings` array, and `Warning [CODE]` lines on the text path. `pulse errors lookup CODE` carries the fixup.

## Writing `.sav` — `pulse export spss`

`spss.BuildDictionary(spss.DictionaryRequest{Schema, Sidecar, Cases, Compression, Options})` emits the dictionary section — header, record `2` variable records, records `3`/`4` value labels, the `7/*` subtypes, the `999` terminator — returning a `DictionaryPlan`. `spss.NewDataEncoder(plan, schema)` writes the data section (`WriteCohort(r)` drains a `.pulse` record stream, `WriteCase` takes one record, `Finish()` returns bytes). File = `plan.Bytes` + those.

- **Bytecode compression is the default** (what SPSS's own SAVE writes). `WriterOptions{Uncompressed: true}` (`.Compression()` resolves the header flag) writes flat 8-byte elements; losslessly equivalent, so the knob trades size for a readable hex dump.
- **ZSAV emission is not implemented** — `PULSE_SPSS_COMPRESSION_UNSUPPORTED`.
- `Cases: -1` when the count is unknown up front: `Finish` patches it via `DictionaryPlan.SetCaseCount`, which writes the header int32 **and** record `7/16` together — two disagreeing counts are a file no reader can adjudicate. A `-1` plan emits no `7/16`, carrying the header count alone.

### The CLI surface, and the one contract mismatch behind it

`pulse export spss -i cohort.pulse -o out.sav`; `pulse convert data.csv out.sav` reaches the same writer. Four flags, one per `spss.WriterOptions` field: `--ignore-sidecar`, `--uncompressed`, `--charset`, `--sanitize-names`. Per-flag detail: `session-bootstrap`, `docs/src/cli/export-spss.md`.

**The writer is a `pio.CohortWriter`, not a row writer.** A `.sav` value derives from a categorical's dictionary **ID**, a `set_*`'s mask **bits** and the **null bitmap** — all three gone once `ExportJob` rendered a row (a categorical resolves to label text and two codes may share one label; a null renders `""`, which a string categorical can legitimately hold). So `ExportJob.Run` hands over the cohort path and **skips its row loop**; `WriteRow` is never called. Hence:

- `--include` / `--labels` **refused** with `PULSE_SPSS_EXPORT_UNSUPPORTED`, not silently ignored — project or relabel into a narrowed cohort, then export that.
- Overlays **warn-and-skip**, like CSV.
- A `convert` from a text source has no cohort: the writer buffers rows, builds an intermediate in-memory cohort through the ordinary import path, exports that — inferred schema, no sidecar, and it says so.

**Ask first — `pulse export predict --format spss`.** The `.sav` writer is the first Pulse writer that can REFUSE, so predict consults the target through the optional `pio.CohortValidator` contract (`spss.Writer.ValidateCohort`): it runs the writer's own non-data pass (sidecar resolution, dictionary build, name policy, charset transcode, derived fold) and discards it, so a predicted refusal is the export's own check code for code — `PULSE_SPSS_SIDECAR_ABSENT` predicts as a warning on `PredictReport.TargetWarnings` exactly as it exports as one. Pass the flags you will export with; `--sanitize-names` flips a `PULSE_SPSS_NAME_INVALID` refusal into a warning. **Sound but INCOMPLETE:** anything needing a record — a value past a declared width, an unformable character, a dictionary ID with no source code — is unreachable without the data pass, so a pass means no SCHEMA-level refusal was found, never that the export cannot fail. Writes no file; no `--format` ⇒ target-blind as before.

**Names are policed and the default is a refusal.** A `.pulse` field name is any UTF-8 string; an SPSS variable name is ≤64 bytes, opens with a letter, unique ignoring case. All three ways an illegal name fails are quiet — records `7/13`, `7/7` and the case-fold rule each produce a well-formed file saying something else — so an offender is `PULSE_SPSS_NAME_INVALID` / `PULSE_SPSS_NAME_COLLISION`. `--sanitize-names` is the opt-in escape hatch for the synthesised path, where a CSV header's spaces and brackets are ordinary: deterministic, collision-safe against other renames **and** against already-legal names (which never move), every rename reported as `PULSE_SPSS_NAME_SANITIZED` with the full `field → name` list. Inert on the sidecar path — those names came from SPSS.

**`--ignore-sidecar` cannot round-trip a cohort still carrying a derived MD `set_*` column.** Its dictionary entries *are* its constituents' field names, so with the registry suppressed, synthesis mints indicator variables `Q1A`/`Q1B` beside the real `Q1A`/`Q1B` → `PULSE_SPSS_NAME_COLLISION`. Export *without* the flag so the registry folds the column away.

**Nulls take the missing state the SPSS type has:** numeric → **sysmis sentinel**; string → **blanks** (no string sentinel exists, and blank reads back as null); every member of a null `set_*` → sysmis, keeping null apart from an empty mask on the way back. No honest form ⇒ `PULSE_SPSS_EXPORT_UNSUPPORTED` naming the variable, never a quiet substitution.

**Two dictionary rules.** Original SPSS codes, never dictionary positions — the sidecar triple supplies them, and `IF q1 EQ 5` addresses a value, so renumbering re-points every reference. No sidecar ⇒ nothing invented: a categorical becomes a STRING variable holding the dictionary text; a `set_*` expands to one indicator variable per entry (named for that entry, so the mask round-trips) plus a `7/7` dichotomy definition; `CategoryCode.Known` stays `false` so the plan says which it is.

**Two things deliberately do NOT reproduce the source:** byte order is always **little-endian** (`7/3` agrees), and `prod_name` identifies pulse. Easily-missed corollary — a NUMERIC missing-value slot is a `flt64`, so the sidecar's verbatim slots are **byte-reversed** when the source was big-endian; re-emitted as read they declare eight bytes decoding here as an unrelated subnormal, and the variable silently stops declaring anything missing. A **string** slot is characters, never reversed. Records `7/21`/`7/22` carry each variable's **FINAL** name (ReadStat refuses a file spelling the short name there). MOYR/QYR/WKYR keep both raw seconds and format code.

**Value labels declared only on user-missing codes come back from the derived registry, not the categories.** An income column labelled at `97`/`98` and nowhere else is not coded, so import maps it to `f64` and moves those labels into the sibling's `Derived.Reasons` — the only place they survive. Write re-emits them as ordinary records `3`/`4`; a categories-only export would return the *code* while losing what it MEANT, and the re-imported reason column would read as bare numerals.

### What the fidelity claim rests on

Fidelity is this adapter's whole justification, so be precise about PROVEN vs. PSPP-specification-only.

- **Gated in CI.** `TestRoundTrip_*` runs import → export → import over a matrix covering all three source encodings, both MR flavours, all three missing-spec shapes, a non-UTF-8 charset, very long strings, both endiannesses. Asserts: re-imported `.pulse` **byte-identical** to the first (cohort identity — `.sav` byte-identity is unreachable for the reasons above); emitted `.sav` a **fixed point** under a second export; exactly the source's own variables declared. `TestRoundTrip_MatrixCoversFR62` fails if an axis loses its last fixture.
- **Local only, not CI.** `io/spss/dict_ecosystem_test.go` (incl. `TestRoundTrippedFile_ReadsIdenticallyInReadStatAndForeign`) hands emitted and cycled files to haven 2.5.5 (ReadStat — the C reader behind haven, pyreadstat and most of what opens a `.sav`) and to independent `foreign` 0.8.91, requiring each reader's cycled read to match its own source read. Both pass — but they `t.Skip` without R / haven / foreign, which is CI's state. A recorded result, not a gate.
- **Corroborated by no independent reader.** MR set subtypes `7/5` / `7/19` — neither haven nor `foreign` exposes MR metadata, so nothing outside Pulse has read our set definitions back; the round trip proves only that Pulse reads what Pulse writes. Same for the two-`int32` form of record `7/11`: Pulse READS two- and three-`int32` shapes, always WRITES three, so that branch rests on synthetic fixtures.
- **Not implemented.** ZSAV emission — read-only, no partial or degraded path.

### Charset on the way out — the same two hard rules, mirrored

**Written in the charset its source declared, in the source's own spelling.** A `cp1252` source goes out as `cp1252` bytes under a `cp1252` record `7/20`, and `7/3` re-emits the source's own character code — including a stale one, because the disagreement is real information. No sidecar ⇒ UTF-8. UTF-8 under a `windows-1252` header would corrupt every non-ASCII label: the declaration follows the bytes, the bytes follow the source.

**Never a replacement character.** An unformable character ⇒ `PULSE_SPSS_CHARSET_UNENCODABLE` naming variable, value, code point — never `?`, `0x1A` or U+FFFD. Usual cause: a cohort edited since import, since text a Pulse operation produced is UTF-8. Every encode is decoded back and compared, so a character that encodes but returns as a *different* one (GB18030 does this across the Private Use Area) is refused too.

**Never a silent truncation.** SPSS widths are BYTE counts, so transcoding moves them (`Zürich` = 6 bytes `windows-1252`, 7 UTF-8). Widths recompute from the ENCODED bytes and a source-recorded width only ever **widens** — SPSS pads, the read path trims, so widening loses nothing while narrowing changes a declaration the source made. Where the format fixes the width: `PULSE_SPSS_WIDTH_OVERFLOW` (string past 32767 bytes, 8-byte short name, 255-byte value label, 64-byte file label, 80-byte document line).

**Order is the crux: encode, measure, segment.** A >255-byte string re-segments per `7/14` on a fixed 252-byte stride, so a multi-byte character *can* straddle a boundary — the reader joins pieces before decoding, exactly as the writer encodes the whole value before slicing. Segmenting the UTF-8 form first would put a partial character on the wire.

`--charset` / `WriterOptions{Charset}` overrides the target — the answer to a cohort whose text outgrew its source codepage. `spss.WithCharset` is read-side *decoding* only, not consulted here. An unwritable name ⇒ `PULSE_SPSS_CHARSET_UNSUPPORTED`, never a silent fall back. Records `7/10`, `7/17`, `7/18` pass through **verbatim** (the reader never decodes them) — the one case where overriding the target is lossy, so a non-ASCII payload rides `PULSE_SPSS_CHARSET_MISMATCH` as a warning rather than being guessed at.

## Cross-links

- `cohort-schema-design` — `.pulse` field-type matrix, nullability bitmap, sidecar index.
- `label-display` — resolving SPSS value labels from the codes the cohort stores.
- `tool-import` — the `pulse_import` MCP surface and its SPSS caveats.
- `session-bootstrap` — `--spss-missing` / `--charset` flag coverage per CLI leaf.
- `docs/src/cli/import-spss.md` — exhaustive user-facing READ reference (worked examples, byte-level detail, R cross-checks).
- `docs/src/cli/export-spss.md` — user-facing WRITE reference (the four flags, sidecar verdicts, derived fold, write-side diagnostic table).
