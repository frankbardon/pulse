---
name: import-best-practices
description: Schema inference, fail-closed semantics, null markers
type: guide
applies_to: inspect, predict
---

# Import Best Practices

## Workflow A — Analyze non-`.pulse` source

Use this workflow when starting from a CSV, TSV, NDJSON, Parquet, or Excel file.

1. Generate a schema template by sampling the source. Output goes to stdout — redirect it to a file you will edit:

   ```
   pulse import schema-template input.csv > schema.json
   ```

   The emitted JSON is an array of `{"name", "type", "description"}` objects with empty descriptions.

2. Edit `schema.json`. Fix any inferred types you disagree with, then write a meaningful description for every field. Descriptions are mandatory for quality (see below).

3. Run the import with your reviewed schema:

   ```
   pulse import csv --schema schema.json --input input.csv --output cohort.pulse
   ```

   Replace `csv` with `tsv`, `ndjson`, `parquet`, or `excel` as needed. Excel additionally accepts `--sheet`.

4. Inspect the materialized cohort to confirm the schema, descriptions, and dictionary contents:

   ```
   pulse cohort inspect --full-dict --json cohort.pulse
   ```

5. Iterate on `schema.json` and re-import if anything looks wrong. The import is fail-closed, so a clean exit means every row encoded.

## Schema inference

- `--sample-rows` defaults to 500 and has a minimum of 50. Use 1000+ for production data.
- Inference picks the narrowest numeric type that fits every sampled value.
- Categorical detection runs by cardinality; widen the dictionary type if you expect more distinct values than the sample shows.

### Inference risks

- Small samples miss edge cases — a `u8`-typed column may overflow once a later row carries a value above 255.
- Mixed types in the same column trigger `PULSE_IMPORT_SCHEMA_AMBIGUOUS`.
- Empty strings interpreted as nulls can flip a column to a non-nullable string when you expected a nullable numeric.

## Fail-closed semantics

- A single unencodable row aborts the entire import.
- No partial `.pulse` file is written when the import fails.
- Each row failure surfaces as `PULSE_IMPORT_ROW_ERROR` with the row number and offending field.

## Null markers

The import recognizes the following case-insensitive null tokens out of the box:

- `""` (empty string)
- `null`
- `na`
- `n/a`

Custom sentinels (e.g., `-999`) are not auto-detected; treat them in your source pipeline before import or use a nullable type and pre-clean the value. Encountering a null for a non-nullable field type aborts the import with `PULSE_IMPORT_ROW_ERROR`.

## Field descriptions

- Cap is 1000 bytes. Exceeding it raises `PULSE_IMPORT_DESCRIPTION_TOO_LONG`.
- Empty descriptions, descriptions under 10 characters, or any of the generic tokens `n/a`, `na`, `none`, `tbd`, `todo`, `unknown`, `field`, `data`, `value`, `column` raise `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`. The check runs via `pulse api predict`; pass `--strict` there to promote the warning to an error.
- Write concise, third-person, present-tense sentences. Mention units, valid range, and domain context when relevant.

## See also

- `cohort-schema-design` — choosing field types, nullability tradeoffs, and categorical width selection (including `PULSE_IMPORT_CATEGORICAL_OVERFLOW` and `PULSE_IMPORT_CATEGORICAL_UNBOUNDED`).
- `error-code-reference` — full recovery playbook for every import error code.
