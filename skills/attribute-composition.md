---
name: attribute-composition
description: Compose ATTR_FORMULA, ATTR_ZSCORE, ATTR_TSCORE, ATTR_NORMALIZED, ATTR_PERCENTILE, ATTR_DATE_PART, ATTR_REG_FITTED, ATTR_REG_RESIDUAL, ATTR_REG_LEVERAGE rows for derived columns. Use when adding computed fields, normalizing values, attaching regression diagnostics, or chaining attributes inside a request.
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

### ATTR_REG_FITTED

Per-row fitted value `ŷᵢ = Xᵢ · β + β₀` from a regression refit during the attribute prepass. Two-pass streaming: pass 1 folds the OLS sufficient statistics over filter-passing rows, pass 2 emits `ŷᵢ` per row using the frozen coefficient vector.

- **Spec fields**: `target` (dependent variable, required), `predictors` (list of independent variables, required and non-empty), `penalty` (one of `""`, `"l1"`, `"l2"`, `"elasticnet"`), `alpha` (regularization strength when penalty is non-empty, `> 0`), `l1_ratio` (elastic-net mixing parameter when `penalty == "elasticnet"`, `0 < l1_ratio < 1`), `label`.
- **Field omission**: the standard `field` slot is unused; the attribute reads `target` and `predictors` instead. The synthesized default label is `ATTR_REG_FITTED_<target>` when `label` is empty.
- **Null handling**: rows with any null in `target` or `predictors` are dropped from the fit (listwise deletion, matches REG_OLS). Rows with null predictors at pass 2 emit `0` (mirrors `ATTR_ZSCORE` null semantics).
- **Refit policy**: each `ATTR_REG_*` attribute carries its own spec and refits internally (Option A). Cross-slot fit reuse via a `from` reference is deferred.
- **Streamability**: streamable via the two-pass orchestrator (`iter.Reset()`). No buffering of the full dataset.
- **Use when**: emitting predicted values for residual diagnostics, fit-vs-actual plots, or downstream filtering on prediction error.

<example name="attr-reg-fitted-basic">
```json
{
  "type": "ATTR_REG_FITTED",
  "target": "y",
  "predictors": ["x1", "x2"],
  "label": "y_hat"
}
```
</example>

### ATTR_REG_RESIDUAL

Per-row residual `yᵢ − ŷᵢ` from a regression refit during the attribute prepass. Sums to ≈ 0 when the fit includes the intercept (always true for OLS) and the response is observed for every contributing row.

- **Spec fields**: same shape as `ATTR_REG_FITTED` — `target`, `predictors`, optional `penalty` / `alpha` / `l1_ratio`, `label`.
- **Default label**: `ATTR_REG_RESIDUAL_<target>` when `label` is empty.
- **Null handling**: rows with any null in `target` or `predictors` are dropped from the fit; pass 2 emits `0` for rows missing the target or any predictor.
- **Streamability**: streamable via the two-pass orchestrator.
- **Use when**: flagging outliers (`|residual| > threshold`), running diagnostic plots, or computing studentized residuals downstream (`residual / residual_std_err`).

<example name="attr-reg-residual-outlier-flag">
Compose a residual column, then filter to flag rows with absolute residual above a fixed threshold.

```json
{
  "attributes": [
    {"type": "ATTR_REG_RESIDUAL", "target": "y", "predictors": ["x1", "x2"], "label": "resid"},
    {"type": "ATTR_FORMULA", "field": "resid", "expression": "abs(resid) > 3", "label": "is_outlier"}
  ]
}
```
</example>

### ATTR_REG_LEVERAGE

Per-row hat-matrix diagonal `hᵢᵢ = 1/n + (xᵢ − μ_x)ᵀ · M2_xx⁻¹ · (xᵢ − μ_x)` from an unpenalized OLS refit. Every `hᵢᵢ ∈ [0, 1]` and `Σᵢ hᵢᵢ = p + 1` (standard OLS-with-intercept identity, where `p` is the predictor count).

- **Spec fields**: `target`, `predictors`, `label`. `penalty` MUST be empty — any non-empty value surfaces `PROCESSING_CONFIG`. Penalized leverage uses a different formula (involving the regularized resolvent) and GLM leverage requires the IRLS weight matrix; both deferred.
- **Default label**: `ATTR_REG_LEVERAGE_<target>` when `label` is empty.
- **Null handling**: same listwise-deletion policy as `ATTR_REG_FITTED`; rows with missing predictors emit `0`.
- **Streamability**: streamable via the two-pass orchestrator. The centered-Gram inverse is computed at finalize; per-row leverage in pass 2 is `O(p²)`.
- **Use when**: identifying high-leverage observations (`hᵢᵢ > 2(p+1)/n` is the common rule of thumb), computing Cook's distance externally (`Dᵢ = (rᵢ² / (p+1)) · (hᵢᵢ / (1 − hᵢᵢ)²)` from the residual + leverage columns), or auditing influence on a fit.

<example name="attr-reg-leverage-high-influence">
Emit both fitted values and leverages so a downstream filter can isolate the most influential rows.

```json
{
  "attributes": [
    {"type": "ATTR_REG_FITTED",  "target": "y", "predictors": ["x1", "x2"], "label": "y_hat"},
    {"type": "ATTR_REG_LEVERAGE", "target": "y", "predictors": ["x1", "x2"], "label": "h_ii"}
  ]
}
```
</example>
</reference>

<rule severity="must" topic="regression-attrs">
## Regression-attribute scope (Phase 7)

- `ATTR_REG_FITTED` and `ATTR_REG_RESIDUAL` accept any OLS penalty (`""`, `"l1"`, `"l2"`, `"elasticnet"`); the same `alpha` / `l1_ratio` rules that apply to `REG_OLS` apply here.
- `ATTR_REG_LEVERAGE` is unpenalized OLS only. Specs with any non-empty `penalty` are rejected with `PROCESSING_CONFIG`.
- GLM (`REG_GLM`) and Bayesian (`REG_BAYES_LINEAR`) attributes are not implemented in this phase; the three regression attributes always refit an OLS engine internally.
- Each `ATTR_REG_*` carries its own `target` / `predictors` and refits independently (Option A). To produce a full `RegressionResult` plus per-row diagnostics in one request, declare both a `regressions[]` slot and an `ATTR_REG_*` attribute — the attribute refits independently; the orchestrator does not share fits across slots.
- Studentized residuals, Cook's distance, and DFFITS are derivable from the fitted / residual / leverage columns plus the residual standard error; downstream consumers can compute them via `ATTR_FORMULA` rather than waiting for native operators.
</rule>

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

<section title="Set-typed attributes (multi-select bitmasks)">

For columns typed `set_u8`, `set_u16`, `set_u32`, `set_u64`, two row-local attributes derive per-row scalars from the set mask:

- `ATTR_SET_POPCOUNT` — emits an integer per row equal to the number of labels selected (the bitmask popcount). Use this to compute "how many products does each respondent own".
- `ATTR_SET_HAS` — emits 0/1 per row indicating whether the configured label is selected. Configure via `Params: {"label": "VISA"}`; the bit position is resolved once against the field's dictionary at construction time and reduces to a single bitwise op per row.

Both are streamable (`RowLocalAttribute`). For ad-hoc set predicates inside `ATTR_FORMULA`, the built-in expression helpers `contains`, `has_any`, `has_all`, `has_none`, `popcount`, `set_union`, `set_intersect`, `set_diff`, `set_xor` are always available; `Record.AllValues()` resolves set fields to a sorted `[]string` of labels so the helpers consume natural list operands.

</section>
