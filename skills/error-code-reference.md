---
name: error-code-reference
description: Recovery playbook per error code
type: reference
applies_to: process, compose, predict
---

# Error Code Reference

<skill_overview>
Every error in Pulse carries a typed error code that identifies the category and domain of the failure. Invoke this skill when an envelope returns a non-empty `errors` or `warnings` array and you need the cause and recovery for a specific code.
</skill_overview>

<rule severity="caveat" topic="internal-codes">
## Shared *_INTERNAL note

All `*_INTERNAL` codes (`ENCODING_INTERNAL`, `PROCESSING_INTERNAL`, `SERVICE_INTERNAL`, `DATA_INTERNAL`, `CLI_INTERNAL`) indicate Pulse bugs. File an issue with the reproducer and the JSON envelope.
</rule>

<reference>
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
</reference>

<reference>
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
</reference>

<reference>
## SERVICE Domain

Errors from the HTTP/API layer and service operations.

### SERVICE_VALIDATION

**Description**: Request validation failure at the service boundary.

**Causes**:
- Malformed JSON in request body
- Missing required fields (e.g., cohort filename)
- Invalid enum values for aggregation/filter/group types
- Use of a removed attribute type (e.g., `ATTR_RANK` — replaced by `WIN_RANK`)

**Recovery**:
- Validate the request JSON structure.
- Ensure all required fields are present.
- For removed attribute types, follow the migration hint in the error message; e.g., `ATTR_RANK` request emits a hint with `details.replacement = "WIN_RANK"`. See `skills/window-operations.md`.
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
</reference>

<reference>
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
</reference>

<reference>
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
</reference>

<reference>
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

### PULSE_WINDOW_INVALID

**Description**: Structural validation failure for a window operation. Predict rejects requests that violate the window contract before execution.

**Causes**:
- Unknown `WIN_*` type
- Missing `order_by` (every window operator requires at least one order key)
- `order_by` field references an unknown field or is not orderable (categorical / bool / packed_bool order keys are rejected)
- `partition_by` references an unknown field
- `frame` present on a non-frame operator (LAG, LEAD, ROW_NUMBER, RANK, DENSE_RANK, PCT_CHANGE)
- `frame` missing on a frame-required operator (RUNNING_SUM, RUNNING_AVG, MOVING_AVG, EWMA)
- `frame.mode` is not `"rows"`
- `MOVING_AVG` without bounded `preceding` and `following`
- `EWMA` `params.alpha` outside `(0, 1]`
- `LAG`/`LEAD`/`PCT_CHANGE`/`RUNNING_*`/`MOVING_AVG`/`EWMA` `field` is non-numeric
- Output `label` collides with an aggregation, group, or attribute label

**Recovery**:
- Run `pulse predict --json` and read the `details` payload — it identifies the offending window index and rule.
- Fix the request per the rule above. Most fixes are mechanical: add `order_by`, drop the `frame` for ROW_NUMBER/RANK/DENSE_RANK, or pick a numeric field for the value-bearing operators.
- See `skills/window-operations.md` for the full contract and per-operator semantics.

### PULSE_FEAT_TARGET_LEAKAGE_RISK

**Description**: `FEAT_TARGET_ENCODE` was requested without a preceding `FEAT_TRAIN_TEST_SPLIT` in the same `features` list. The encoder's per-category mean is computed across every row in the cohort, which means rows destined for the validation/test partitions contribute target signal to the training feature.

**When it fires**:
- Predict scans `req.Features` in order; if it sees a `FEAT_TARGET_ENCODE` before any `FEAT_TRAIN_TEST_SPLIT`, it emits this code as a warning.
- In `--strict` mode the warning is upgraded to an error.

**Recovery**:
- Reorder features so `FEAT_TRAIN_TEST_SPLIT` precedes every `FEAT_TARGET_ENCODE`.
- See `skills/feature-engineering.md` for the leakage trap discussion.

### PULSE_DECIMAL_OVERFLOW

**Description**: A decimal arithmetic or aggregation result exceeds `decimal128(38)` — the maximum representable absolute value is `10^38 - 1`.

**When it fires**:
- `AGG_SUM` over a decimal field whose accumulated total would not fit in 38 digits.
- Multiplication / division produces a result mantissa beyond 38 digits.
- Importer parses a string with a fractional part wider than 38 digits.

**Recovery**:
- Pick a coarser scale or split the cohort.
- Use `AGG_AVERAGE` instead of `AGG_SUM` for very large series — the implementation falls back to `f64` and emits `PULSE_DECIMAL_PRECISION_LOSS` rather than failing.

### PULSE_DECIMAL_PRECISION_LOSS

**Description**: Warning. An `AGG_AVERAGE` on a decimal128 field saw an intermediate sum that would have overflowed `decimal128(38)`. Pulse fell back to `f64` accumulation. The result is no longer auditor-defensible to the last digit.

**Recovery**:
- For audited workloads: split the cohort or pre-aggregate by a coarser grouping so each partial sum fits in 38 digits.
- For non-audit workloads: ignore the warning.

### PULSE_DECIMAL_DIVIDE_BY_ZERO

**Description**: A decimal `/` operation with a zero divisor. Decimal arithmetic in Pulse never produces NaN or infinity.

**Recovery**:
- Guard the formula with a non-zero check (`FILTER_RANGE` on the divisor field).
- Replace zero divisors with a sentinel before the operation runs.

### PULSE_GEO_INVALID_POINT

**Description**: A `point_f64` value parse failed, or the parsed lat/lon is out of range (`|lat| > 90` or `|lon| > 180`).

**Recovery**:
- Check that the importer maps the source columns in the right order. WKT order is `POINT(lon lat)` even though Pulse stores `(lat, lon)` internally.
- Use `pulse inspect` to confirm the field type and any per-column importer mapping.

### PULSE_GEO_INVALID_POLYGON

**Description**: A WKT POLYGON string failed to parse, or the ring is not closed (first vertex must equal last vertex).

**When it fires**:
- `FILTER_GEO_WITHIN` expression contains malformed WKT.
- POLYGON has fewer than 4 vertices (3 unique + closing).
- `MULTIPOLYGON` (rejected in v1).
- Polygon includes inner rings (holes) — also rejected in v1.

**Recovery**:
- Repair the WKT. POLYGON v1 accepts a single closed outer ring only.
- For complex geometries, decompose into multiple `FILTER_GEO_WITHIN` filters or wait for variable-length geometry support.

### PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS

**Description**: `AGG_GEO_BBOX` saw an input set that crosses the 180/-180 meridian. A flat `(min_lat, min_lon, max_lat, max_lon)` bbox is ambiguous in that case.

**Detection rule**: any pair of points in the input has `|lon_a - lon_b| > 180`.

**Recovery**:
- Split the cohort by hemisphere with a `FILTER_RANGE` on longitude before aggregating.
- Use `AGG_GEO_CENTROID` instead — the 3D unit-sphere algorithm handles antimeridian crossings correctly.

### PULSE_GEO_INVALID_RESOLUTION

**Description**: An H3 resolution parameter is out of range or finer than a cell's native resolution.

**When it fires**:
- `GROUP_H3_CELL` with `resolution` < 0 or > 15.
- `GROUP_H3_CELL` on an `h3_cell` input where the requested resolution is finer than the cell's native resolution (parent walk only goes coarser, never finer).

**Recovery**:
- Pick a resolution in `[0, 15]`.
- For `h3_cell` input: pick a resolution at most equal to the cell's native resolution. Inspect the cohort with `pulse inspect` to see the field's native resolution.

### PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL

**Description**: Predict warning. The requested aggregation has no defined decimal128 implementation in v1.

**v1 supported decimal aggregations**: `AGG_SUM`, `AGG_AVERAGE`, `AGG_MIN`, `AGG_MAX`, `AGG_VARIANCE`, `AGG_STDDEV`, `AGG_COUNT`, `AGG_DISTINCT_COUNT`.

**Not supported (v1)**: `AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_ZSCORE`, `AGG_SKEWNESS`, `AGG_KURTOSIS`, `AGG_MODE`, `AGG_FREQUENCY`, `AGG_RANGE`.

**Recovery**:
- Pick a supported aggregation, or cast the field to `f64` via an attribute and aggregate that.

### PULSE_AGG_NOT_MEANINGFUL_FOR_GEO

**Description**: A numeric aggregation was requested on a geospatial field (`point_f64` or `h3_cell`).

**Recovery**:
- Use `AGG_GEO_CENTROID` or `AGG_GEO_BBOX` for `point_f64` value-bearing aggregations.
- For `h3_cell`, group by the cell and aggregate on a numeric field.

### PULSE_SYNTH_DISTRIBUTION_UNKNOWN

**Description**: A synth spec referenced a distribution kind that is not registered in the `synth` package. The generator refuses to invent semantics for an unknown kind.

**Recovery**:
- Use `synth.AllDistributions()` (or `skills/synthetic-data.md`) to confirm the supported distribution names.
- Common cause: typo (`gaussian` vs `normal`, `power_law` vs `pareto`).

### PULSE_SYNTH_CONSTRAINT_INFEASIBLE

**Description**: Rejection sampling for declared synth constraints exceeded the allowed rejection rate (50% by default). The generator refuses to produce a biased or truncated cohort.

**Recovery**:
- Relax the constraint, change the underlying distribution so it concentrates inside the constraint region, or raise `max_rejection_rate` if you accept the cost.
- Inspect the per-row constraint expressions and verify they are stated correctly (e.g. `>=` vs `>`).

### PULSE_PROFILE_FIELD_UNSUPPORTED

**Description**: The cohort profiler encountered a field type it cannot summarize (currently `point_f64` and `h3_cell`). The field is skipped and an entry is added to the profile's `warnings` block.

**Recovery**:
- Use schema-mode synthesis for the affected field instead of profile-driven mode.
- Track follow-up work for native spatial profiling.

### PULSE_TEST_UNKNOWN_TYPE

**Description**: The request referenced a `TestType` not registered in either the row-test or post-test registry.

**Recovery**:
- Check spelling against `types.AllTestTypes()` or `skills/statistical-testing.md`.
- Confirm the test is registered for the intended tier — some tests only run as `tests` (tier 1), others only as `post_tests` (tier 2).

### PULSE_TEST_FIELD_NOT_NUMERIC

**Description**: A statistical test needs a numeric field (`u*`, `f*`, `nullable_u*`, `decimal128`) but the named field resolves to a categorical, geo, or otherwise non-numeric schema type.

**Recovery**:
- Pick a numeric field, or cast the field through an attribute (`ATTR_FORMULA`) before running the test.
- For chi-square (`TEST_CHISQ`), use `Rows` / `Cols` instead of `Field` — both must be categorical.

### PULSE_TEST_INVALID_ALPHA

**Description**: The `alpha` (significance threshold) is outside the open interval (0, 1).

**Recovery**:
- Pick a value in (0, 1). Common choices: 0.10, 0.05, 0.01.
- Leave `alpha` unset to accept the default (0.05).

### PULSE_TEST_INSUFFICIENT_N

**Description**: The test received fewer non-null observations than the minimum required to compute its statistic (typically `n < 2` per group; `n < df + 1` for parametric variants).

**Recovery**:
- Loosen upstream filters that may be removing too many rows.
- Choose a different test that tolerates small samples (e.g. exact Fisher's test instead of `TEST_CHISQ` — when available).
- Aggregate or pool groups before running the test.

### PULSE_TEST_VARIANCE_ZERO

**Description**: One or more groups have zero sample variance, making the t- or F-statistic undefined.

**Recovery**:
- Check whether the field is constant in the affected group(s) — that is usually the underlying signal, not a test failure.
- Use a non-parametric alternative (e.g. `TEST_KS` for distribution comparison).

### PULSE_TEST_SPLIT_GROUPS_LT_2

**Description**: A two-sample test (`TEST_T`, `TEST_WELCH`) or `TEST_ANOVA_F` observed fewer than the required number of distinct groups in `split_by` after filtering.

**Recovery**:
- Verify the categorical field actually contains multiple values in the filtered set.
- Loosen filters or change the `split_by` field.

### PULSE_TEST_CONTINGENCY_DEGENERATE

**Description**: A chi-square contingency table is empty or has only a single non-empty row or column. The statistic is undefined in that shape.

**Recovery**:
- Verify both `rows` and `cols` resolve to fields with more than one observed level after filtering.
- Aggregate rare levels into an "other" bucket before running the test.

### PULSE_TEST_EXPECTED_COUNT_TOO_LOW

**Description**: Warning. One or more expected cell counts in a chi-square contingency table fell below 5, making the asymptotic χ² approximation unreliable.

**Recovery**:
- Use Fisher's exact test when sample sizes are small (when added).
- Combine rare levels to increase per-cell expected counts.
- Treat the result as advisory rather than decisive when sample sizes are small.

### PULSE_TEST_FIELD2_NOT_NUMERIC

**Description**: A paired or bivariate test (`TEST_PAIRED_T`, `TEST_PEARSON_R`) was supplied a `field2` that resolves to a non-numeric schema type.

**Recovery**:
- Pick a numeric field (`u*`, `f*`, `nullable_u*`, `decimal128`).
- Cast the column via `ATTR_FORMULA` before running the test.

### PULSE_TEST_SUCCESS_VALUE_MISSING

**Description**: `TEST_PROP_Z` requires `params.success` — the dictionary value treated as a positive outcome on the primary field. Without it the test cannot decide which category counts as a success.

**Recovery**:
- Add `"params": {"success": "yes"}` (or whichever dictionary value represents success in your cohort) to the test spec.
- Use `pulse cohort inspect` to confirm the categorical's dictionary values.

### PULSE_TEST_CORRELATION_UNDEFINED

**Description**: `TEST_PEARSON_R` saw at least one column with zero sample variance. The correlation coefficient and its t-statistic are undefined when either variable is constant.

**Recovery**:
- Confirm the field actually carries variation in the filtered cohort.
- Use Spearman ρ or Kendall τ (when available) for non-parametric monotonic association on near-constant data.
- Remove the constant column from the request.

### PULSE_TEST_PAIRED_LENGTH_MISMATCH

**Description**: A paired test (`TEST_WILCOXON_SR`, future paired variants) encountered rows where one paired column was null while the other was present. Drop-pair semantics apply — the row is excluded from the test — and the mismatch count surfaces as a warning so the caller knows the effective pair count.

**Recovery**:
- If the mismatch count is small relative to N, ignore the warning.
- Pre-filter null pairs with a `FILTER_EXCLUDE` on either column to make the drop explicit.
- Verify the upstream import did not introduce spurious nulls (e.g., a CSV column with empty cells).

### PULSE_TEST_TIES_DOMINATE

**Description**: A rank-based nonparametric test (`TEST_MANN_WHITNEY_U`, `TEST_WILCOXON_SR`, `TEST_KRUSKAL_WALLIS`, `TEST_SPEARMAN_R`, `TEST_KENDALL_TAU`) observed ties on ≥ 50 % of the input values. The normal-approximation / chi-square p-value loses accuracy under heavy ties.

**Recovery**:
- For small n with heavy ties, prefer an exact-permutation variant if/when registered.
- Bin the field upstream (e.g., `FEAT_BUCKETIZE`) only if discretization is acceptable — it does not improve accuracy of the original test.
- Treat the p-value as advisory; the effect-direction statistic is still informative.
</reference>
