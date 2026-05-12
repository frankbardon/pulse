---
name: import-best-practices
description: Schema inference, fail-closed semantics, null markers
type: guide
applies_to: inspect, predict
---

# Import Best Practices

<skill_overview>
Pulse imports are fail-closed and schema-driven: review an inferred schema, write meaningful descriptions, then materialize a `.pulse` cohort. Import itself is a CLI / library operation — there is no `pulse_import` MCP tool today. This skill describes what the imported data looks like once it reaches the parser, plus the warnings predict raises on the resulting cohort, so you can guide a user through fixing schema-level issues from inside the session.
</skill_overview>

<rule severity="note" topic="no-mcp-tool">
## Import has no MCP tool today

`.pulse` files are produced offline by `pulse import csv|tsv|ndjson|jsonarray|parquet|arrow|excel` (CLI) or `pulse.Pulse.Import` (library). The LLM cannot trigger an import directly; it can only inspect what was already imported and report quality issues the user should fix at import time. For CLI usage, point a human at https://frankbardon.github.io/pulse/cli/cohort-inspect.html and the import chapters in mdBook.
</rule>

<reference>
## What the importer guarantees you see

Once a cohort has been imported, `pulse_inspect` exposes:

- The final schema: field name, type, byte offset, bit position, optional description.
- Per-field categorical dictionaries (truncated to 100 entries by default; full via `InspectOptions.FullDict` on the library side).
- The format version byte. The current value is `0x01`.

If an import was rejected, no `.pulse` file is written and there is nothing for you to inspect.
</reference>

<reference>
## Schema inference (offline, then committed to the header)

- `--sample-rows` defaults to 500 and has a minimum of 50. The user should pass 1000+ for production data.
- Inference picks the narrowest numeric type that fits every sampled value.
- Categorical detection runs by cardinality; widen the dictionary type if the import expects more distinct values than the sample shows.
</reference>

<rule severity="caveat" topic="inference-risks">
## Inference risks worth flagging

- Small samples miss edge cases — a `u8`-typed column may overflow once a later row carries a value above 255.
- Mixed types in the same column trigger `PULSE_IMPORT_SCHEMA_AMBIGUOUS`.
- Empty strings interpreted as nulls can flip a column to a non-nullable string when the user expected a nullable numeric.

If you spot any of these in `pulse_inspect` output (e.g. a column that should be nullable but the schema says `u8`), recommend the user re-import with `--schema` and an explicit type.
</rule>

<rule severity="must" topic="fail-closed">
## Fail-closed semantics

- A single unencodable row aborts the entire import.
- No partial `.pulse` file is written when the import fails.
- Each row failure surfaces as `PULSE_IMPORT_ROW_ERROR` with the row number and offending field.

You will not see `PULSE_IMPORT_ROW_ERROR` in a `pulse_process` envelope — it fired at import time. The error code documentation in `error-code-reference` is for triaging the user's import failure when they paste the message back to you.
</rule>

<reference>
## Null markers

The import recognizes the following case-insensitive null tokens out of the box:

- `""` (empty string)
- `null`
- `na`
- `n/a`

Custom sentinels (e.g., `-999`) are not auto-detected; the user must clean them in the source pipeline before import or use a nullable type and pre-clean the value. Encountering a null for a non-nullable field type aborts the import with `PULSE_IMPORT_ROW_ERROR`.
</reference>

<rule severity="must" topic="descriptions">
## Field descriptions

- Cap is 1000 bytes. Exceeding it raises `PULSE_IMPORT_DESCRIPTION_TOO_LONG` at import time.
- Empty descriptions, descriptions under 10 characters, or any of the generic tokens `n/a`, `na`, `none`, `tbd`, `todo`, `unknown`, `field`, `data`, `value`, `column` raise `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`. This warning is surfaced by `pulse_predict` against the imported cohort, not at import time, so you can flag it during a session.
- Write concise, third-person, present-tense sentences. Mention units, valid range, and domain context when relevant.
</rule>

<see_also>
- `cohort-schema-design` — choosing field types, nullability tradeoffs, and categorical width selection (including `PULSE_IMPORT_CATEGORICAL_OVERFLOW` and `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`).
- `error-code-reference` — full recovery playbook for every import error code.
</see_also>
