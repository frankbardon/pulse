---
name: import-best-practices
description: Schema inference, fail-closed semantics, null markers
type: guide
applies_to: inspect, predict
---

# Import Best Practices

## Overview

Importing data into the `.pulse` format is the first step in any Pulse workflow. Getting the import right means choosing correct types, handling nulls properly, and validating the schema before committing to a full import.

## Schema Inference

When importing without an explicit schema, Pulse infers field types from a sample of the input data. The inference process:

1. Reads the first N rows (controlled by `--sample-rows`, default 500, minimum 50).
2. For each column, tests candidate types from narrowest to widest.
3. Selects the narrowest type that accommodates all sampled values.
4. Detects categorical columns by cardinality analysis.

### Inference Caveats

- **Small samples may miss edge cases.** A column of small integers in the first 500 rows might be typed as u8, but row 501 could have a value of 300. Increase `--sample-rows` or provide an explicit schema.
- **Ambiguous columns** trigger `PULSE_IMPORT_SCHEMA_AMBIGUOUS`. This happens when the sample contains mixed types.
- **Null markers** affect inference. If nulls are represented as empty strings, the column may be inferred as non-nullable string rather than a nullable numeric type.

## Fail-Closed Semantics

Pulse imports use fail-closed semantics by default:

- If a row cannot be encoded, the import fails entirely (no partial output).
- This prevents silently corrupted data.
- Every row error produces a `PULSE_IMPORT_ROW_ERROR` with the row number and field.

This design ensures that if an import succeeds, every row is correctly encoded.

## Null Markers

Source data represents missing values in various ways:

- Empty string (`""`)
- Literal `"null"` or `"NULL"`
- Literal `"NA"` or `"N/A"`
- Special sentinel values (e.g., `-999`)

Pulse recognizes common null markers during import. For custom null markers, specify them in the schema definition.

When a null is encountered for a non-nullable field type, the import fails with `PULSE_IMPORT_ROW_ERROR`. Choose nullable types for fields that may have missing values.

## Schema Template Workflow

The recommended workflow for new imports:

1. **Generate a template**: `pulse import schema-template input.csv`
2. **Review and edit**: Adjust inferred types, add descriptions, set nullability.
3. **Import with schema**: `pulse import csv --input input.csv --output data.pulse --schema schema.json`
4. **Inspect the result**: `pulse cohort inspect data.pulse`

This workflow gives you full control over the schema while leveraging inference as a starting point.

## Sample Rows

The `--sample-rows` flag controls how many rows are used for schema inference:

- **Default**: 500 rows
- **Minimum**: 50 rows
- **Recommendation**: Use at least 1000 rows for production imports to catch edge cases.

More sample rows increase confidence in type inference but take longer to process.

## Field Descriptions

Every field should have a meaningful description. During import, Pulse validates descriptions:

- Maximum length: 1000 bytes (`PULSE_IMPORT_DESCRIPTION_TOO_LONG` if exceeded)
- Quality check: descriptions that are too short or generic trigger `PULSE_FIELD_DESCRIPTION_LOW_QUALITY`

Good descriptions include:
- What the field represents
- Units (e.g., "Height in centimeters")
- Valid range (e.g., "Score from 0 to 100")
- Domain context (e.g., "PHQ-9 depression screening total")

## Categorical Import Considerations

When importing categorical fields:

- Choose the narrowest width that fits the cardinality: categorical_u8 for up to 256 values, categorical_u16 for up to 65,536.
- Overflow triggers `PULSE_IMPORT_CATEGORICAL_OVERFLOW`.
- If sample analysis suggests unbounded cardinality, `PULSE_IMPORT_CATEGORICAL_UNBOUNDED` is emitted.
- The dictionary is built during import and stored in the schema header.
