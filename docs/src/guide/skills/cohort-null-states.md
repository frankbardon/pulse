```yaml
name: cohort-null-states
description: How a .pulse cohort stores absence — the per-record null bitmap, no in-band sentinels, out-of-sample null promotion on inferred imports, and the null / empty / selected states of set and categorical cells across every import and export format.
type: guide
kind: design
applies_to: inspect, predict, process, compose, sample, facet
covers: [nullability, null bitmap, set fields, categorical, empty mask, null promotion, undefined figures]
```

# Cohort null states

Part of the `.pulse` schema surface; entry skill [`cohort-schema-design`](cohort-schema-design.md) (field-type matrix).

## Nullability + per-record bitmap

When at least one field is `Nullable: true`, every record carries a trailing bitmap of `ceil(field_count / 8)` bytes after the payload:

- Field index `i` → byte `i/8`, bit `i%8` (LSB-first). `1` = null, `0` = present.
- No nullable fields ⇒ no bitmap; zero-overhead path.

The bitmap is the sole null mechanism. No type has an inline sentinel — `decimal128` all-zero bits is decimal zero, not null. Null-skip semantics for sum/mean/percentile are central.

**Inferred imports promote on out-of-sample nulls.** Inference decides nullability from the first `--sample-rows` (default 500) rows only; a null (`""`/`null`/`na`/`n/a`) past that window promotes the field and continues rather than failing — `PULSE_IMPORT_NULL_PROMOTED` + `ImportReport.PromotedFields` / `Result.promoted_fields`. Every inferred text/columnar import (csv, tsv, ndjson, jsonarray, parquet, arrow, excel). An **explicit `--schema`** is a contract: a null in a declared-non-nullable field stays `PULSE_IMPORT_ROW_ERROR`. Avoid surprises by marking sometimes-missing fields `"nullable": true`, or raising `--sample-rows`.

## Null, empty and selected

`set_*` mask bit `i` = label `dict[i]` selected; empty mask is a valid value (NOT null).

**A set cell has THREE states and every import/export format keeps them apart.** Null (no answer), **empty mask** (answered, selected nothing) and a selection are distinct data — "ticked none of these" and "skipped the question" give different denominators. External forms, identical at every rung from `set_u8` to `set_u256`:

| State | Flat text (`csv` / `tsv` / `excel`) | JSON (`ndjson` / `jsonarray`) | `arrow` / `parquet` |
|---|---|---|---|
| null | `""` (any null token) | `null` | validity bit clear |
| empty mask | **`\|`** — a bare delimiter, no token | `"\|"` on write, `[]` also accepted on read | zero-length `LIST<UTF8>` |
| selection | `A\|B` | `"A\|B"` | `["A","B"]` |

One marker (a bare `|`, the default set delimiter) serves every format, so the convention cannot drift between them, and it survives a third-party round trip because it is ordinary cell text — unlike CSV's `,,` versus `,"",`, a spreadsheet has nothing to normalise away. It works because `isNullToken` does not recognise `\|` and the token splitter drops empty tokens, so `\|` yields zero tokens and no dictionary entry. Widening the null-token set to cover a lone delimiter, or retaining empty tokens, re-collapses the two states SILENTLY — both spellings keep importing and only the meaning changes.

**`categorical_*` has the same pair — an empty-string VALUE and a null — and no marker can carry it**, because any text is a legal categorical value. It rides an out-of-band channel instead: the export row spells a null `nil` and an empty value `""` (`io.NullAwareWriter`), and a source declares its own nulls per row (`io.NullAwareReader`). `ndjson` / `jsonarray` / `arrow` / `parquet` carry both states; `csv` / `tsv` / `excel` have one spelling for an absent value and read BOTH back as **null** — a documented gap, asserted in `internal/io/nullcell/`, never a silent one. A blank cell in a non-dictionary column (`u32`, `date`, …) is a null on every format.

## Undefined figures in results

A result figure can be UNDEFINED even when every input is present: a ratio over an all-zero denominator (including a crosstab cell whose rows all carry weight 0), a CI bound under two rows, an index-vs-prior series' first entry, an unfilled rolling window. In Go it is NaN (test `math.IsNaN`); on every JSON surface — `--json` envelopes, `--stream` NDJSON rows, MCP tool results, an embedder's own `json.Marshal` of a result type — it is `null` in place, key kept, and the rest of the response serialises normally. Absent still means "not reported for this kind"; `null` means "reported, undefined here". For an untyped fragment (a streamed row) use `types.MarshalFinite`. Hand a result back unedited (e.g. to `pulse explain --response`): a `null` figure is read as undefined again, never as 0 — do not replace it with a number.
