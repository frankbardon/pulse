```yaml
name: spss-metadata-sidecar
description: The cohort.pulse.spss.json sidecar an SPSS import writes — what it records that the .pulse format cannot, the code-label-dictionary-ID triple labels come from, its fingerprint, and the absent / stale / invalid / ignored read verdicts.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, metadata sidecar, spss.json, value labels, staleness, --ignore-sidecar]
requires: [io_format:spss]
```

# SPSS metadata sidecar

Part of the SPSS surface; entry skill [`spss-cohorts`](spss-cohorts.md). What the export does with it: [`spss-export`](spss-export.md).

## The metadata sidecar

An import writes `cohort.pulse.spss.json` beside the cohort (the `.spss.json` suffix, the `pulse.ImportSidecar` convention — NOT `.meta.json`, which a managed import writes for the same cohort). It holds every dictionary element the `.pulse` format has no slot for: measure levels, print/write formats, records `7/17` file and `7/18` variable attributes (kept **distinct**), record `6` documents, weight variable, compression bias, `nominal_case_size`, original short names, declared BYTE widths + `7/14` segmentation, MR/MC set and `7/5` variable-set definitions, the declared charset in the file's own spelling, product name, missing-value specs in all three shapes. EVERY response set is recorded — MC sets and MD sets that refused to derive included — because the block records DEFINITIONS, a different question from which columns are synthetic.

Load-bearing payload: the **`code ↔ label ↔ Pulse dictionary ID` triple** per categorical column. Pulse IDs are positional, SPSS codes arbitrary ⇒ the only place the LABELS live. Per-entry flags `labelled` / `observed` / `missing` keep a declared-but-unused code, an appended unlabelled code and a user-missing code all representable. **Build a `LabelTable` from this file.**

Document `{format_version, kind, fingerprint, payload}`; `payload` flat and self-contained so it can later be lifted verbatim into a `.pulse` schema metadata block (deferred, not rejected — needs a `FormatVersion` bump). `fingerprint` = SHA-256 + size + mtime over the **`.pulse` cohort**, not the source `.sav`, mirroring the sidecar index's O(1) staleness check. Written via the optional `io.SidecarEmitter`, called by `ImportJob.Run` **after** the cohort write; a source not implementing it ⇒ byte-identical import, no sidecar.

### Reading it back — and why absent and stale are not the same answer

`LoadSidecar(fs, cohort, opts)` (`internal/io/spss`) — read path and the write side's first act (`pulse export spss` reaches it for you; no leaf reads the sidecar alone). Inspect and predict read it too, only for `payload.weight` → `suggested_weight` ([`tool-inspect`](tool-inspect.md)) and, in predict, the measure levels behind the [`predict-advisories`](predict-advisories.md) sidecar codes: every refusal below is SILENT there — no suggestion, no advisory, no warning. Returns a `SidecarResolution`; `resolution.Synthesise()` is the single question — *must I build a default dictionary from the `.pulse` schema alone?*

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
