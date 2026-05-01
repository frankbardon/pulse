---
name: attribute-composition
description: z-score, t-score, normalized, formula, date-part — composition rules
type: guide
applies_to: process, compose, predict
---

# Attribute Composition

<skill_overview>
Attributes are derived values computed per-record from existing fields, extending output with calculated columns without modifying underlying cohort data. Invoke this skill when adding z-scores, normalized values, formulas, percentile ranks, or date-part extractions to a request.
</skill_overview>

<reference>
## Attribute Types

### ATTR_ZSCORE

Computes the z-score (standard score) for each record's value on a given field.

- **Formula**: `(value - mean) / stddev`
- **Output**: A floating-point value indicating how many standard deviations from the mean.
- **Null handling**: Null input produces null output.
- **Categorical**: Not applicable; z-scores require numeric input.
- **Use when**: Comparing values across different scales or identifying outliers.

### ATTR_TSCORE

Computes the t-score for each record, which is a linear transformation of the z-score.

- **Formula**: `(z * 10) + 50`
- **Output**: A floating-point value centered at 50 with standard deviation of 10.
- **Null handling**: Null input produces null output.
- **Categorical**: Not applicable.
- **Use when**: You want a normalized score without negative values, commonly used in psychometrics.

### ATTR_NORMALIZED

Normalizes each value to a 0..1 range using min-max normalization.

- **Formula**: `(value - min) / (max - min)`
- **Output**: A floating-point value between 0 and 1 inclusive.
- **Null handling**: Null input produces null output. If max equals min, output is 0.
- **Categorical**: Not applicable.
- **Use when**: You need values on a common scale for comparison or visualization.

### ATTR_FORMULA

Computes a derived value using a runtime expression (evaluated by `expr-lang/expr` v1.17.x) that can reference any field in the record.

- **Config**: `type: "ATTR_FORMULA"`, `field` (the source field used by predict for null-propagation bookkeeping), `label` (output column name), and `expression` (the expression string). There is no `result_type` field.
- **Field references**: by bare field name. Numeric fields (u8/u16/u32/u64/f32/f64/date/nullable_*/packed_bool) appear as numbers. Categorical fields (`categorical_u8`/`u16`/`u32`) are auto-resolved to their **dictionary string** before evaluation, so use string equality (`brand == "Apple"`) or membership (`brand in ["Apple", "Samsung"]`), not the integer index.
- **Null handling**: Null fields are omitted from the expression environment; referencing one yields an evaluation error and the row fails (`PROCESSING_RUNTIME`). Use `??` to provide a fallback (e.g., `weight ?? 0`).
- **Output**: The result is coerced to a number. `float64`, `float32`, `int`, and `int64` pass through; `bool` becomes `1.0`/`0.0`; any other type errors with `PROCESSING_RUNTIME`.
- **Composition**: Formulas can only reference original fields, not other computed attributes (see Composition Rules).
- **Use when**: You need a custom derived field like BMI or a categorical flag.

### ATTR_PERCENTILE

Computes the percentile rank of each record's value within the field distribution.

- **Formula**: `(count of values <= x) / total count * 100`
- **Output**: A floating-point value between 0 and 100.
- **Null handling**: Null input produces null output.
- **Categorical**: Not applicable.
- **Use when**: You need to know where a value falls in the distribution (e.g., "this score is in the 85th percentile").

### ATTR_RANK (removed)

`ATTR_RANK` was removed in this release. Use `WIN_RANK` (with empty `partition_by` and a single ASC `order_by` on the same field) for the equivalent behavior, plus proper tie semantics. See `skills/window-operations.md` for the migration recipe.

### ATTR_DATE_PART

Extracts a date component from a date field and converts it to an integer value suitable for grouping.

- **Params**: `{"part": "<part>"}` where part is one of: `year`, `month`, `day`, `year_month`, `year_month_day`, `month_day`.
- **Output formats**:
  - `year` → YYYY (e.g., 2024)
  - `month` → M (e.g., 3 for March, no zero-padding)
  - `day` → D (e.g., 15, no zero-padding)
  - `year_month` → YYYYM[M] (e.g., 202403)
  - `year_month_day` → YYYYM[M]DD (e.g., 20240315)
  - `month_day` → M[M]DD (e.g., 315 for March 15, 1201 for December 1)
- **Input**: Must be a `date` field. Errors on non-date fields with `PROCESSING_CONFIG`.
- **Null handling**: Null date values produce 0.
- **Use when**: You need to group or aggregate records by date components (e.g., group by year-month to see monthly trends).
</reference>

<reference>
## ATTR_FORMULA operator and function allowlist

- **Allowed operators**:
  - Arithmetic: `+`, `-`, `*`, `/`, `%`, `**` (or `^`) for exponentiation, unary `-` and `+`.
  - Comparison: `==`, `!=`, `<`, `<=`, `>`, `>=`.
  - Logical: `and` / `&&`, `or` / `||`, `not` / `!`.
  - Membership / pattern: `in`, `contains`, `startsWith`, `endsWith`, `matches`.
  - Range / nil-coalescing / pipe: `..`, `??`, `|`.
  - Grouping: parentheses `( ... )`.
  - Ternary: `cond ? a : b`.
- **Allowed functions** (verified against expr-lang v1.17.8 builtins): `abs`, `ceil`, `floor`, `round`, `int`, `float`, `string`, `len`, `type`, `min`, `max`, `sum`, `mean`, `median`, `first`, `last`, `get`, `take`, `keys`, `values`, `concat`, `flatten`, `reverse`, `sort`, `sortBy`, `uniq`, `join`, `split`, `splitAfter`, `replace`, `repeat`, `indexOf`, `lastIndexOf`, `hasPrefix`, `hasSuffix`, `trim`, `trimPrefix`, `trimSuffix`, `lower`, `upper`, `toJSON`, `fromJSON`, `toBase64`, `fromBase64`, `toPairs`, `fromPairs`, `groupBy`, `count`, `find`, `findIndex`, `findLast`, `findLastIndex`, `filter`, `map`, `reduce`, `all`, `any`, `none`, `one`, `bitnot`, `now`, `date`, `duration`, `timezone`. **Common gap**: there is no `sqrt`, `log`, `exp`, `pow`, `sin`, or `cos` builtin — use `**` for powers (e.g., `value ** 0.5` for square root) or precompute trigonometric values upstream.
</reference>

<example name="attr-formula-bmi">
BMI computation from weight and height fields.

```json
{
  "type": "ATTR_FORMULA",
  "field": "weight_kg",
  "label": "bmi",
  "expression": "weight_kg / (height_m * height_m)"
}
```
</example>

<rule severity="must" topic="composition">
## Composition Rules

1. **Attributes are computed after aggregation and grouping.** They operate on the final record set.
2. **Multiple attributes can reference the same source field.** For example, you can compute both z-score and percentile for the same field.
3. **Attributes cannot reference other attributes.** There is no chaining; each attribute reads from the original data.
4. **Labels must be unique.** Each attribute must have a distinct label in the output.
5. **Formula expressions** are evaluated in a sandboxed environment with access only to field values from the current record.
</rule>

<reference>
## Categorical String Access

When a formula references a categorical field, the expression environment provides the string label (not the integer index). This allows expressions like:

<example name="attr-formula-categorical-flag">
```
if(category == "Group A", 1, 0)
```
</example>

The dictionary lookup is performed automatically before expression evaluation. Arithmetic on categorical fields in formulas is not supported; use string comparisons (`==`, `!=`, `in`) or branch on the label and emit a numeric result.
</reference>
