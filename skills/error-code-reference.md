---
name: error-code-reference
description: Recovery playbook per error code
type: reference
applies_to: process, compose, predict
---

# Error Code Reference

## Overview

Every error in Pulse carries a typed error code that identifies the category and domain of the failure. This reference lists every error code with its description and recovery steps.

## Shared *_INTERNAL note

All `*_INTERNAL` codes (`ENCODING_INTERNAL`, `PROCESSING_INTERNAL`, `SERVICE_INTERNAL`, `DATA_INTERNAL`, `CLI_INTERNAL`) indicate Pulse bugs. File an issue with the reproducer and the JSON envelope.

## ENCODING Domain

Errors from the binary format and data encoding layer.

### ENCODING_INVALID

**Description**: Invalid data format or structure in a `.pulse` file.

**Causes**:
- Corrupted file header
- Invalid magic bytes
- Schema field count mismatch

**Recovery**:
- Verify the file was written by a compatible version of Pulse.
- Re-import the source data to regenerate the `.pulse` file.
- Check for partial writes (e.g., interrupted import).

### ENCODING_IO

**Description**: I/O failure during read/write operations on encoded data.

**Causes**:
- File not found or permission denied
- Disk full during write
- Network filesystem timeout

**Recovery**:
- Check file path and permissions.
- Ensure sufficient disk space for output files.
- Retry the operation if the failure was transient.

### ENCODING_TYPE_MISMATCH

**Description**: Type conversion or casting error during encoding/decoding.

**Causes**:
- Schema declares a field as u8 but data contains values > 255
- Float-to-integer conversion with fractional values
- Categorical index out of dictionary bounds

**Recovery**:
- Review the schema types for the affected field.
- Use a wider type (e.g., u16 instead of u8) if values exceed the range.
- Re-import with corrected schema.

### ENCODING_INTERNAL

Unexpected failure in the encoder/decoder. Recovery: see shared *_INTERNAL note above.

## PROCESSING Domain

Errors from the processing engine and pipeline.

### PROCESSING_CONFIG

**Description**: Component configuration error in the processing pipeline.

**Causes**:
- Invalid aggregation type
- Missing required fields in a request component
- Incompatible grouper interval (e.g., zero or negative)

**Recovery**:
- Review the request JSON for correctness.
- Use `api predict` to validate the request before executing.

### PROCESSING_STATE

**Description**: Context state management error during processing.

**Causes**:
- Pipeline stage received unexpected state from a previous stage
- Concurrent modification of shared state

**Recovery**:
- Retry the request. If the error persists, simplify the request to isolate the issue.

### PROCESSING_RUNTIME

**Description**: Runtime execution error during processing.

**Causes**:
- Division by zero in formula evaluation
- Arithmetic overflow
- Expression evaluation failure

**Recovery**:
- Check formula expressions for edge cases (division by zero, overflow).
- Add filters to exclude problematic records.
- Use `api predict` to catch issues before execution.

### PROCESSING_GROUP

**Description**: Group-related processing error.

**Causes**:
- GROUP_ROUNDED on a non-numeric field
- Group key resolution failure
- Excessive group count exhausting memory

**Recovery**:
- Verify grouper configuration: field type compatibility and interval values.
- Use GROUP_ROUNDED instead of GROUP_CATEGORY for high-cardinality numeric fields.

### PROCESSING_INTERNAL

Unexpected failure in the processing pipeline. Recovery: see shared *_INTERNAL note above.

## SERVICE Domain

Errors from the HTTP/API layer and service operations.

### SERVICE_VALIDATION

**Description**: Request validation failure at the service boundary.

**Causes**:
- Malformed JSON in request body
- Missing required fields (e.g., cohort filename)
- Invalid enum values for aggregation/filter/group types

**Recovery**:
- Validate the request JSON structure.
- Ensure all required fields are present.
- Use `api predict` to check the request.

### SERVICE_RESOURCE

**Description**: Resource loading or access failure.

**Causes**:
- Cohort file not found at the specified path
- Data directory does not exist
- Insufficient permissions

**Recovery**:
- Verify file paths in the request.
- Check that data directories are accessible.

### SERVICE_REGISTRY

**Description**: Registry lookup failure for a processing component.

**Causes**:
- Unknown aggregation type string
- Unknown filter type string
- Unknown group type string
- Unknown attribute type string

**Recovery**:
- Check the component type string against the manifest (`pulse --json`).
- Ensure correct spelling and casing.

### SERVICE_INTERNAL

Unexpected failure in the service/orchestration layer. Recovery: see shared *_INTERNAL note above.

## DATA Domain

Errors from data file and dataset management operations.

### DATA_FILE

**Description**: File access or format error during data operations.

**Causes**:
- Input file is not a valid CSV/TSV/Parquet/NDJSON/Excel file
- File encoding issues (non-UTF8)
- Truncated file

**Recovery**:
- Verify the input file format.
- Ensure the file is complete and not truncated.
- Check file encoding is UTF-8.

### DATA_PARSE

**Description**: Data parsing or deserialization error.

**Causes**:
- CSV field count mismatch between rows
- Invalid number format in a numeric column
- JSON syntax error in NDJSON input

**Recovery**:
- Inspect the source data for inconsistencies.
- Use `--sample-rows` to test with a subset first.
- Fix data quality issues in the source file.

### DATA_CONFIG

**Description**: Data configuration error.

**Causes**:
- Schema template references nonexistent columns
- Invalid field type in schema definition
- Duplicate field names in schema

**Recovery**:
- Review the schema JSON for correctness.
- Ensure field names are unique.
- Use `import schema-template` to generate a valid starting schema.

### DATA_CALCULATION

**Description**: Error during data field access or calculation.

**Causes**:
- Accessing a field by incorrect index
- Calculation overflow on field data
- Dictionary lookup failure for categorical field

**Recovery**:
- Verify field references in the request.
- Check that categorical dictionaries are consistent.

### DATA_INTERNAL

Unexpected failure in the data file/dataset layer. Recovery: see shared *_INTERNAL note above.

## CLI Domain

Errors from command-line interface operations.

### CLI_INPUT

**Description**: Command input or argument error.

**Causes**:
- Missing required flag (e.g., `--input`)
- Invalid flag value (e.g., non-numeric `--count`)
- Unknown subcommand

**Recovery**:
- Run the command with `--help` to see required flags.
- Check flag values for correct types.

### CLI_OUTPUT

**Description**: Output generation or file write error.

**Causes**:
- Cannot write to output path (permissions, disk full)
- Invalid output format specification

**Recovery**:
- Check output path permissions and disk space.
- Verify the output format is supported.

### CLI_COMMAND

**Description**: Command execution error.

**Causes**:
- The underlying operation failed
- A subcommand returned an error

**Recovery**:
- Check the wrapped error message for details.
- Use `api predict` for processing-related commands.

### CLI_INTERNAL

Unexpected failure in the CLI adapter layer. Recovery: see shared *_INTERNAL note above.

## PULSE Domain

Pulse-specific error codes for I/O pipelines, categorical handling, description validation, and aggregation warnings.

### PULSE_IMPORT_SCHEMA_AMBIGUOUS

**Description**: Type ambiguity during schema inference. The importer cannot determine a single best type for a column from the sample data.

**Causes**:
- Column contains mixed types (e.g., integers and strings)
- Sample rows are insufficient to disambiguate

**Recovery**:
- Increase `--sample-rows` to provide more data for inference.
- Provide an explicit schema using `--schema`.
- Use `import schema-template` to generate and hand-edit a schema.

### PULSE_IMPORT_ROW_ERROR

**Description**: A per-row error during import. The row could not be encoded into the target schema.

**Causes**:
- Value out of range for the target type
- Unparseable value in a numeric column
- Null value in a non-nullable field

**Recovery**:
- Inspect the reported row number and field.
- Fix the source data or use a wider/nullable type.

### PULSE_EXPORT_ROW_ERROR

**Description**: A per-row error during export. A record could not be decoded into the target format.

**Causes**:
- Dictionary corruption in a categorical field
- Encoding inconsistency in the `.pulse` file

**Recovery**:
- Re-import the source data to regenerate the `.pulse` file.
- Report the issue if the file was written by Pulse.

### PULSE_IMPORT_CATEGORICAL_OVERFLOW

**Description**: The categorical dictionary exceeds the width capacity of the chosen categorical type.

**Causes**:
- More than 256 distinct values with categorical_u8
- More than 65,536 distinct values with categorical_u16

**Recovery**:
- Use a wider categorical type (categorical_u16 or categorical_u32).
- Review whether the field is truly categorical or should be treated differently.

### PULSE_IMPORT_CATEGORICAL_UNBOUNDED

**Description**: The sample data suggests unbounded cardinality for a categorical field. The number of distinct values grows linearly with sample size, suggesting the field is not truly categorical.

**Causes**:
- Free-text field incorrectly marked as categorical
- ID-like field with unique values per record

**Recovery**:
- Change the field type to a non-categorical type.
- If the field is truly categorical, increase sample size to confirm cardinality is bounded.

### PULSE_IMPORT_DESCRIPTION_TOO_LONG

**Description**: A field description exceeds the 1000-byte limit.

**Causes**:
- Overly verbose field description in the schema

**Recovery**:
- Shorten the field description to under 1000 bytes.
- Focus on essential information: what the field represents, units, and domain semantics.

### PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL

**Description**: A numeric aggregation (SUM, AVERAGE, MIN, MAX, RANGE, MEDIAN, PERCENTILE, STDDEV, VARIANCE, SKEWNESS, KURTOSIS, ZSCORE) was requested on a categorical field. The operation will execute on dictionary indices, but the result has no semantic meaning.

**Causes**:
- Applying any of AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX, AGG_RANGE, AGG_MEDIAN, AGG_PERCENTILE, AGG_STDDEV, AGG_VARIANCE, AGG_SKEWNESS, AGG_KURTOSIS, or AGG_ZSCORE to a categorical field.

**Recovery**:
- Use AGG_COUNT, AGG_DISTINCT_COUNT, AGG_FREQUENCY, or AGG_MODE for categorical fields.
- If you need any of SUM, AVERAGE, MIN, MAX, RANGE, MEDIAN, PERCENTILE, STDDEV, VARIANCE, SKEWNESS, KURTOSIS, or ZSCORE, use a non-categorical numeric field.

### PULSE_FIELD_DESCRIPTION_LOW_QUALITY

**Description**: A field description quality warning. The description is present but may be too short, too generic, or lacks important details.

**Causes**:
- Single-word description
- Description that repeats the field name
- Missing units or domain context

**Recovery**:
- Improve the description with units, range, and domain meaning.
- Include what the field represents, not just its name.
