---
name: debugging-with-predict
description: Iterating on a request using api predict
type: guide
applies_to: predict
---

# Debugging with Predict

## Overview

The `api predict` command validates a processing request without executing it. This enables fast iteration on request construction by catching errors before committing to a full processing run.

## Workflow

1. **Draft your request** as a JSON file.
2. **Run predict**: `pulse api predict --request draft.json --json`
3. **Review the output**: check for errors and warnings.
4. **Fix issues** and re-run predict until clean.
5. **Execute**: `pulse api process --request draft.json --json`

## What Predict Checks

Predict validates the following aspects of a request:

- **Cohort existence**: Does the referenced `.pulse` file exist and have a valid header?
- **Field references**: Do all field names in aggregations, filters, groups, and attributes reference fields that exist in the schema?
- **Type compatibility**: Are aggregations applied to compatible field types?
- **Categorical warnings**: Does the request apply numeric aggregations to categorical fields?
- **Grouper validity**: Is GROUP_ROUNDED applied only to numeric fields? Is the interval positive?
- **Filter consistency**: Are FILTER_INCLUDE/FILTER_EXCLUDE values parseable for the target field type?
- **Attribute configuration**: Are formula expressions syntactically valid? Are labels unique?
- **Output format**: Is the requested output format supported?

## Reading Warnings

Predict output includes a list of warnings, each with:

- **code**: The error code (e.g., `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`)
- **message**: A human-readable description of the issue
- **field**: The field name involved, if applicable

Warnings do not prevent execution by default. They indicate potential issues that may produce misleading results.

## Strict Mode

Use `--strict` to treat warnings as errors:

```
pulse api predict --request draft.json --json --strict
```

In strict mode, any warning causes predict to exit with a non-zero status code. This is useful in CI pipelines or when you want to enforce best practices.

## Common Predict Scenarios

### Scenario: Wrong Field Name

If you reference a field that does not exist in the schema, predict returns an error immediately rather than failing mid-processing.

### Scenario: Categorical Aggregation

If you apply AGG_SUM to a categorical field, predict emits `PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL`. Switch to AGG_FREQUENCY or AGG_COUNT.

### Scenario: Invalid Filter Values

If you use FILTER_RANGE with non-numeric values on a numeric field, predict catches the parsing error.

### Scenario: Formula Syntax

If an ATTR_FORMULA expression has a syntax error or references a nonexistent field, predict reports the issue with the exact expression location.

## Integration with Compose

Predict also works with ComposedRequest. Each sub-request is validated independently, and all errors/warnings are collected and returned together.
