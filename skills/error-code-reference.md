---
name: error-code-reference
description: Read Pulse error envelopes (DOMAIN_CATEGORY codes across CLI/DATA/ENCODING/PROCESSING/PULSE/SERVICE) and call pulse_errors_lookup for per-code Message + Fixup detail. Use when a tool call returns errors[] or warnings[] you don't recognize.
type: guide
applies_to: process, compose, predict
---

# Error Code Reference

<skill_overview>
This skill teaches you HOW to use Pulse's typed error system — how the envelope is shaped, what the DOMAIN_CATEGORY codes mean, the repair workflow predict hands you, and where to fetch per-code detail. It is intentionally short: per-code prose and fixup templates live behind the `pulse_errors_lookup` MCP tool and the `pulse errors lookup CODE` CLI leaf so the bootstrap context stays lean. Treat this skill as the orientation; treat the lookup tool as the catalog.
</skill_overview>

<reference>
## How errors are returned

Every CLI `--json` envelope and every MCP response carries the same shape:

```json
{
  "format_version": "1.0",
  "data": { ... },
  "errors": [{"code": "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL", "message": "...", "details": {...}}],
  "warnings": [{"code": "PULSE_FIELD_DESCRIPTION_LOW_QUALITY", "message": "...", "details": {...}}]
}
```

- `errors` is fatal — the requested operation did not run, or ran and produced no usable result.
- `warnings` is diagnostic — the operation ran; the call surfaced a concern the caller should know about.
- Both arrays are always present (empty `[]`, never `null`) so a streaming JSON parser can dispatch on length.
- Every entry carries `code` (the typed identifier) and `message` (a human sentence); `details` is shape-free per-code metadata.
</reference>

<reference>
## Naming convention: DOMAIN_CATEGORY

Every code splits on the first underscore. The prefix is the domain — the part of Pulse that produced the failure — and the suffix is a coarse category.

| Domain | What it covers | Common cause |
|---|---|---|
| `ENCODING` | `.pulse` binary format — header, schema, dictionaries | Corrupt file or incompatible binary version |
| `PROCESSING` | Operator runtime and pipeline plumbing | Bad operator config, divide-by-zero, group construction |
| `SERVICE` | Pre-execution request validation | Missing required field, schema mismatch, unknown operator type |
| `DATA` | Tabular I/O (CSV, NDJSON, Parquet, Excel) | Bad delimiter, malformed row, unknown format |
| `CLI` | Argument and flag parsing | Wrong flag spelling, missing value, bad JSON arg |
| `PULSE` | Pulse-specific semantic warnings and feature gates | Categorical aggregation, decimal overflow, test-design violations |

The shared `*_INTERNAL` suffix (e.g. `ENCODING_INTERNAL`) signals a Pulse invariant violation. Those codes carry `fixup_not_applicable: true` — file an issue with the reproducer rather than mutating the request.
</reference>

<workflow id="repair" name="error-repair-workflow">
## Repair workflow

1. Read the envelope's first error or warning entry — `code` and `message`.
2. If you authored the request: run `pulse_predict` first. Predict returns the same envelope plus a `data.suggestions[]` array carrying structured fixup proposals (`path`, `current`, `proposed`, `confidence`) generated directly from the per-code metadata. Apply the highest-confidence suggestion and re-run.
3. If `suggestions` is empty or the code is unfamiliar, call `pulse_errors_lookup` with `code=<the code>`. The tool returns the canonical `message` plus the `fixups[]` template list (action class, optional request path, hint sentence, ranked examples).
4. For systematic exploration (e.g. "what `PULSE_TEST_*` codes might fire for this test?"), call `pulse_errors_lookup` with `domain=PULSE` or `query="paired"` and scan the result list.
</workflow>

<reference>
## Common codes (top five you'll see)

The catalog has ~60 codes total. These five carry most of the day-to-day repair workload:

- **`SERVICE_VALIDATION`** — Request failed pre-execution validation. The fixup is mechanical: call `pulse_predict --json` on the request first; the predict envelope names the exact missing or malformed field path.
- **`PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`** — Numeric aggregation (SUM, AVERAGE, etc.) on a categorical field. Use `AGG_MODE`, `AGG_FREQUENCY`, `AGG_DISTINCT_COUNT`, or `AGG_COUNT` instead — or pick a numeric field for the numeric semantics.
- **`PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL`** — Aggregation that has no decimal128 implementation (e.g. `AGG_MEDIAN`, `AGG_PERCENTILE` in v1). Decimal fields support exact `AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_VARIANCE`, `AGG_STDDEV`, `AGG_COUNT`, `AGG_DISTINCT_COUNT`.
- **`PULSE_FIELD_DESCRIPTION_LOW_QUALITY`** — Field description empty, under 10 characters, or in the generic-word denylist (`n/a`, `tbd`, etc.). Supply a sentence describing what the field represents, including units.
- **`PULSE_TEST_INSUFFICIENT_N`** — Statistical test received fewer non-null observations than its minimum (typically n<30 per group). Widen upstream filters, or pick a test that tolerates small samples (e.g. Fisher exact for 2×2 instead of chi-square).

Every other code carries the same shape: a canonical `message` and one or more `fixup` templates. Fetch detail with `pulse_errors_lookup` rather than memorising the catalog.
</reference>

<example name="lookup-via-mcp">
## Looking up a code via MCP

```json
{"name": "pulse_errors_lookup", "arguments": {"code": "PULSE_TEST_TUKEY_REQUIRES_K_GE_3"}}
```

Returns a 1-element array with `code`, `domain`, `message`, and a `fixups[]` list. On miss, returns an empty array (not an error).

Enumeration:

```json
{"name": "pulse_errors_lookup", "arguments": {"domain": "PULSE"}}
{"name": "pulse_errors_lookup", "arguments": {"query": "decimal"}}
```

`code`, `domain`, and `query` may be combined — the filters are ANDed.
</example>

<example name="lookup-via-cli">
## Looking up a code via CLI

```
pulse errors lookup PULSE_TEST_TUKEY_REQUIRES_K_GE_3
pulse errors list --domain PULSE
pulse errors list --query "decimal" --json
```

Default output is a human-friendly indented form; pass `--json` for the envelope shape.
</example>

<see_also>
- debugging-with-predict — the predict-first workflow that surfaces structured suggestions before you hit a runtime error
- getting-started — Pulse vocabulary and the request envelope shape
- mcp-integration — the full MCP tool catalog, including `pulse_errors_lookup`
</see_also>
