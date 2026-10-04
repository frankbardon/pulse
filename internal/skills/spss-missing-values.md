---
name: spss-missing-values
description: How SPSS user-missing and system-missing values land in a cohort — the numeric <var>_missing sibling versus the flagged categorical code, why the two arms deliberately differ, --spss-missing, and filtering on the code rather than the label.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [SPSS, user-missing values, sysmis, missing siblings, --spss-missing]
requires: [io_format:spss]
---

# SPSS missing values

Part of the SPSS surface; entry skill `spss-cohorts`. The sibling column is registered as a derived column: `spss-response-sets` (Derived columns and the `derived` registry).

## Missing values — and why the two arms differ

SPSS separates `refused` / `don't know` / `not applicable` / `sysmis`; the Pulse bitmap is ONE bit — *that* a value is absent, never *why*. Both naive mappings lose: codes-as-data makes a sum aggregator add 99999 per refusal; all-to-null destroys the item-non-response distinction that nonresponse adjustment needs. Split **by substrate**:

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
- `--spss-missing=null` / `io.ReaderOptions{SPSS: io.SPSSReaderOptions{MissingMode: io.SPSSMissingNull}}` drops siblings — same nulls, reason no longer per-row (the specification still rides the sidecar). Unrecognised mode ⇒ `PULSE_SPSS_MISSING_MODE_INVALID`, never a default.

**Categorical flag** — `Q1: 1=Yes, 2=No, 9=Refused` → `categorical_u8` holding `"1"`, `"2"`, `"9"`; the refused row still says `9`. Record `7/22` long-string missing values bind to the same `variable.missing` slot a record type 2 spec does, so strings need no branch. Which entries are missing-coded is recorded twice:

- sidecar `variables[].categories[].missing` — additive `omitempty`; no such codes ⇒ byte-identical document, `SidecarFormatVersion` unmoved.
- `PULSE_SPSS_CATEGORICAL_USER_MISSING` — **one informational diagnostic per FILE**, never per variable; prose names the first few, `Details["missing_categories"]` carries every field → entries pair uncapped. Nothing is wrong when it fires; the loss is downstream, where a percentage base silently includes the refusal category.

**Exclude over the CODE:** a value filter names `"9"`, never `"Refused"` — a value outside the dictionary is a loud `PROCESSING_CONFIG`, not a filter matching nothing.

<!-- feature: FILTER_EXCLUDE -->
```json
{"type":"FILTER_EXCLUDE","field":"Q1","values":["9"]}
```
<!-- /feature -->
