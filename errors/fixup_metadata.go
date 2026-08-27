package errors

// codeMetadata is the per-code Metadata registry. Every entry in
// allCodes MUST have a row here — TestCodesHaveFixups enforces it.
// Codes with no mechanical repair carry FixupNotApplicable: true,
// which is the honest signal (absence is a bug, not a feature).
//
// Path templates use abstract pointers (e.g. ["Aggregations", "*",
// "Type"]) rather than concrete indices. Concrete-index resolution
// against a specific Request lives in predict-suggestions; this table
// stays request-agnostic.
var codeMetadata = map[Code]Metadata{
	// ---------- ENCODING ----------
	ENCODING_INVALID: {
		Message: "The .pulse file header or schema block is malformed (bad magic, unsupported format version, or invalid field-type byte).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data to regenerate the .pulse file; the existing file is corrupt or was written by an incompatible binary.",
			},
		},
	},
	ENCODING_IO: {
		Message: "An I/O failure occurred while reading or writing the binary .pulse file.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Cohort", "Filename"},
				Hint:   "Verify the file path is reachable, the process has read/write permission, and the underlying filesystem has free space.",
			},
		},
	},
	ENCODING_TYPE_MISMATCH: {
		Message: "A value cannot be encoded into its declared field type (out of range, wrong sign, or wrong unit).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Widen the field type (u8 -> u16, f32 -> f64) or pre-clean the source data to fit the declared type, then re-import.",
			},
		},
	},
	ENCODING_INTERNAL: {
		Message:            "Internal invariant violated inside the encoding layer; report with reproducer.",
		FixupNotApplicable: true,
	},

	// ---------- PROCESSING ----------
	PROCESSING_CONFIG: {
		Message: "An operator's configuration is invalid (missing params, wrong type, out-of-range value).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Run pulse predict --json on the request first; the predict envelope names the offending operator and parameter so the fix is mechanical.",
			},
		},
	},
	PROCESSING_STATE: {
		Message:            "Pipeline state-machine invariant violated mid-run (typically a streaming-pass ordering bug).",
		FixupNotApplicable: true,
	},
	PROCESSING_RUNTIME: {
		Message: "An operator failed during row processing (formula compile failure, divide-by-zero, etc.).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Attributes", "*", "Formula"},
				Hint:   "Guard the formula with a null/zero check (e.g. wrap denominators in an iff(x == 0, null, x) before dividing).",
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Add a FILTER_RANGE that excludes the rows that trip the runtime error before the operator runs.",
			},
		},
	},
	PROCESSING_GROUP: {
		Message: "Group construction failed (e.g., quantile with zero-variance input, malformed bucket key).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Groups", "*", "Type"},
				Hint:   "Swap GROUP_QUANTILE for GROUP_RANGE on near-constant fields, or GROUP_CATEGORY for a high-cardinality numeric to GROUP_ROUNDED.",
			},
		},
	},
	PROCESSING_INTERNAL: {
		Message:            "Internal invariant violated inside the processing layer; report with reproducer.",
		FixupNotApplicable: true,
	},
	PROCESSING_REGRESSION_NOT_IMPLEMENTED: {
		Message: "The requested regression operator is registered but its engine has not landed yet (Phase 0 stub).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Regressions", "*", "Type"},
				Hint:     "Phase 0 lands the API surface only; remove the regression slot until the matching engine ships, or pin to a Pulse release that includes the operator.",
				Examples: []any{"REG_OLS", "REG_GLM", "REG_BAYES_LINEAR"},
			},
		},
	},
	PROCESSING_REGRESSION_RANK_DEFICIENT: {
		Message: "The predictor design matrix has collinear columns; XᵀX is singular and the closed-form solve cannot proceed.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Regressions", "*", "Penalty"},
				Hint:     "Add a regularization penalty (l2 / ridge is the conventional fix for collinearity) or drop the redundant predictor from Predictors[].",
				Examples: []any{"l2", "elasticnet"},
			},
		},
	},
	PROCESSING_REGRESSION_NO_CONVERGE: {
		Message: "An iterative regression fit (IRLS for REG_GLM, coordinate descent for regularized REG_OLS) failed to converge within MaxIters.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Regressions", "*", "MaxIters"},
				Hint:   "Raise MaxIters, loosen Tol, rescale the predictors, or drop a near-collinear column; persistent non-convergence usually points to a separable / degenerate design.",
			},
		},
	},
	PROCESSING_REGRESSION_SINGULAR_GRAM: {
		Message: "The Gram matrix XᵀX remained non-invertible even after regularization.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Regressions", "*", "Alpha"},
				Hint:   "Increase Alpha to lift the conditioning, or drop the degenerate (all-zero / duplicated) predictor from Predictors[].",
			},
		},
	},
	PROCESSING_REGRESSION_INVALID_FAMILY: {
		Message: "REG_GLM was requested with a Family outside the supported set.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Regressions", "*", "Family"},
				Hint:     "Set Family to one of binomial (logistic), poisson, or gamma; check the regression-modeling skill for the family / link compatibility matrix.",
				Examples: []any{"binomial", "poisson", "gamma"},
			},
		},
	},
	PROCESSING_REGRESSION_INVALID_LINK: {
		Message: "The requested Link function is incompatible with the chosen Family.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Regressions", "*", "Link"},
				Hint:     "Use the family-default Link (binomial → logit, poisson → log, gamma → log) or pick a Link from the family's supported set in the regression-modeling skill.",
				Examples: []any{"logit", "log", "identity"},
			},
		},
	},
	PROCESSING_REGRESSION_INSUFFICIENT_DATA: {
		Message: "The filtered record set has fewer observations than predictors + 1; the model is unidentifiable.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Loosen the filters so n ≥ p + 1 (one observation per predictor plus the intercept), or trim Predictors[] to a smaller set.",
			},
		},
	},

	// ---------- SERVICE ----------
	SERVICE_VALIDATION: {
		Message: "The request failed pre-execution validation (missing required fields, invalid combinations, schema mismatch).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Call pulse predict --json on the request; predict reports the exact missing or malformed field path so the fix is mechanical.",
			},
		},
	},
	SERVICE_RESOURCE: {
		Message: "A required resource could not be opened (missing .pulse file, unreadable path).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Cohort", "Filename"},
				Hint:   "Verify the cohort filename exists under PULSE_DATA_DIR (or the configured DataDir) and the process has read access.",
			},
		},
	},
	SERVICE_REGISTRY: {
		Message: "The request named an operator that is not registered (typo in type field, deprecated identifier).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Hint:   "Check the operator name against pulse manifest --json (Components section); spelling and casing must match exactly.",
			},
		},
	},
	SERVICE_INTERNAL: {
		Message:            "Internal invariant violated inside the service orchestration layer; report with reproducer.",
		FixupNotApplicable: true,
	},

	// ---------- DATA ----------
	DATA_FILE: {
		Message: "A data file could not be opened or its layout is invalid.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Verify the input file exists, is non-empty, and matches the declared format (CSV / TSV / NDJSON / Parquet / Excel).",
			},
		},
	},
	DATA_PARSE: {
		Message: "Tabular input (CSV / TSV / JSON / Parquet) failed to parse at a row boundary.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Inspect the offending row with pulse sample --rows N; common causes are inconsistent column counts, stray quotes, or wrong delimiter.",
			},
		},
	},
	DATA_CONFIG: {
		Message: "An I/O job's configuration is invalid (conflicting flags, unknown format, bad target path).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Re-run with --help; check that the format flag matches the file extension and the schema template names existing columns.",
			},
		},
	},
	DATA_CALCULATION: {
		Message: "A calculation over data values failed (e.g., reading a field that does not exist on the record).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Verify every field reference in the request appears in pulse inspect --json output for the cohort.",
			},
		},
	},
	DATA_INTERNAL: {
		Message:            "Internal invariant violated inside the data layer; report with reproducer.",
		FixupNotApplicable: true,
	},

	// ---------- CLI ----------
	CLI_INPUT: {
		Message: "A CLI argument or flag is malformed (missing value, unknown subcommand, bad JSON).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Re-run with --help on the subcommand to confirm flag names and required values.",
			},
		},
	},
	CLI_OUTPUT: {
		Message: "Output generation failed (file write error, terminal too narrow, format unsupported).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Pick a writable output path with free disk space, or omit --output to stream to stdout.",
			},
		},
	},
	CLI_COMMAND: {
		Message: "A CLI command failed to dispatch or returned a non-zero status.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Check the wrapped error in the envelope details; for processing leaves, run pulse predict --json on the request first.",
			},
		},
	},
	CLI_INTERNAL: {
		Message:            "Internal invariant violated inside the CLI binary; report with reproducer.",
		FixupNotApplicable: true,
	},

	// ---------- PULSE ----------
	PULSE_IMPORT_SCHEMA_AMBIGUOUS: {
		Message: "Schema inference observed conflicting types in the same column across the sample window.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Increase --sample-rows so the inference window sees a representative subset, or supply --schema with an explicit type for the offending column.",
			},
		},
	},
	PULSE_IMPORT_ROW_ERROR: {
		Message: "A row could not be imported due to a per-cell encoding or parse failure.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Inspect the reported row index in details; pick a wider or nullable field type, or pre-clean the source value before re-importing.",
			},
		},
	},
	PULSE_IMPORT_NULL_PROMOTED: {
		Message: "Warning-class — an inferred import found a null cell in a column that the bounded inference sample had marked non-nullable, so the field was promoted to nullable and the import continued. Details carry `promoted_fields` (the field names widened to nullable). The written .pulse is correct; this warning only records that the sample missed the null. Never fires for explicit --schema imports, where a null in a declared non-nullable field stays a PULSE_IMPORT_ROW_ERROR.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "To suppress: raise --sample-rows so the inference window covers the null, or supply an explicit --schema marking the field \"nullable\": true. No action is required to accept the import — the promoted field is already correct.",
			},
		},
	},
	PULSE_EXPORT_ROW_ERROR: {
		Message: "A row could not be exported due to a per-cell value-to-string conversion failure.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data to regenerate the .pulse file; the dictionary or encoding state is inconsistent.",
			},
		},
	},
	PULSE_EXPORT_FIELD_UNKNOWN: {
		Message: "An ExportJob.Includes / ConvertJob.Includes entry names a field that does not appear in the source schema.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Includes", "*"},
				Hint:   "Run pulse inspect on the source .pulse file to list valid field names, then correct or drop the offending --include entry. Pass no --include flags to export every field.",
			},
		},
	},
	PULSE_IMPORT_CATEGORICAL_OVERFLOW: {
		Message: "A categorical column's observed dictionary exceeded the declared width capacity (u8 / u16 / u32).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Widen the categorical type (categorical_u8 -> categorical_u16, categorical_u16 -> categorical_u32) and re-import.",
			},
		},
	},
	PULSE_IMPORT_SET_OVERFLOW: {
		Message: "A multi-select column's observed dictionary exceeds the largest set width (set_u64 holds at most 64 entries).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Denormalize to one row per (record, element) pair, or wait for set_u128. If the dictionary is actually bounded, raise --sample-rows or supply a force_type schema hint.",
			},
		},
	},
	PULSE_IMPORT_CATEGORICAL_UNBOUNDED: {
		Message: "The categorical inference heuristic believes the column has unbounded cardinality and should be modeled as a string.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import with the column declared as a non-categorical type; if cardinality is truly bounded, raise --sample-rows to confirm.",
			},
		},
	},
	PULSE_IMPORT_DESCRIPTION_TOO_LONG: {
		Message: "A field description exceeds the 1000-byte cap defined for the .pulse header.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Trim the description to <= 1000 bytes; keep what the field represents, drop the prose narrative.",
			},
		},
	},
	PULSE_IMPORT_FORMAT_UNKNOWN: {
		Message: "The source extension is not one of the supported import formats and no explicit format hint was supplied.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Pass an explicit format (csv, tsv, ndjson, jsonarray, parquet, arrow, excel) on the import call, or rename the source file with a supported extension.",
			},
		},
	},
	PULSE_IMPORT_SOURCE_MISSING: {
		Message: "The source file referenced by an import call (or by a managed-import sidecar) could not be opened.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Verify the source path exists and is readable; for managed-import sidecars, the original file may have moved or been deleted — re-import from the new location.",
			},
		},
	},
	PULSE_IMPORT_HANDLE_EXISTS: {
		Message: "A managed-import handle of the requested name already exists in the imports pool.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Pass overwrite=true to replace the existing handle, choose a different name, or drop the existing handle first.",
			},
		},
	},
	PULSE_IMPORT_SOURCE_FORBIDDEN: {
		Message: "The absolute source path resolves outside the import jail; the Manager only reads files under the configured root (default: process working directory).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Move the source file under the jail root (typically the directory the CLI / MCP server was invoked from), or pass a different root via pulse.Options.ImportSourceJailRoot.",
			},
		},
	},
	PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL: {
		Message: "A numeric aggregation (SUM, AVERAGE, etc.) was requested on a categorical field.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Aggregations", "*", "Type"},
				Hint:     "Use AGG_MODE, AGG_FREQUENCY, AGG_DISTINCT_COUNT, or AGG_COUNT for categorical fields.",
				Examples: []any{"AGG_MODE", "AGG_FREQUENCY", "AGG_DISTINCT_COUNT", "AGG_COUNT"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Aggregations", "*", "Field"},
				Hint:   "If the numeric semantics matter, pick a non-categorical numeric field on the same cohort.",
			},
		},
	},
	PULSE_FIELD_DESCRIPTION_LOW_QUALITY: {
		Message: "A field description is empty, too short, or matches a generic-word denylist (\"n/a\", \"tbd\", etc.).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Provide a sentence (>= 10 characters) describing what the field represents, including units and domain meaning.",
			},
		},
	},
	PULSE_WINDOW_INVALID: {
		Message: "A window operator's configuration is invalid (bad frame, alpha out of bounds, no order key, label collision).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Windows", "*", "OrderBy"},
				Hint:   "Every WIN_* operator requires at least one OrderBy key; supply a date or numeric field.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Windows", "*", "Frame"},
				Hint:   "Drop Frame for ROW_NUMBER, RANK, DENSE_RANK, LAG, LEAD, PCT_CHANGE; supply Frame with bounded preceding/following for MOVING_AVG, EWMA, RUNNING_*.",
			},
		},
	},
	PULSE_FEAT_TARGET_LEAKAGE_RISK: {
		Message: "FEAT_TARGET_ENCODE was requested without a prior FEAT_TRAIN_TEST_SPLIT; encoded values include rows that should be held out.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Features"},
				Hint:   "Insert a FEAT_TRAIN_TEST_SPLIT operator before every FEAT_TARGET_ENCODE in the features list.",
			},
		},
	},
	PULSE_DECIMAL_OVERFLOW: {
		Message: "A decimal arithmetic result exceeds the decimal128(38) representable range.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Aggregations", "*", "Type"},
				Hint:   "Use AGG_AVERAGE instead of AGG_SUM for large series; the implementation falls back to f64 and emits PULSE_DECIMAL_PRECISION_LOSS instead of failing.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "Pick a coarser decimal scale or split the cohort so each partial sum stays within 38 digits.",
			},
		},
	},
	PULSE_DECIMAL_PRECISION_LOSS: {
		Message: "A decimal aggregation fell back to f64 because intermediate state would have overflowed decimal128(38).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Split the cohort or pre-aggregate by a coarser grouping so each partial sum fits in 38 digits; ignore the warning if auditor-grade precision is not required.",
			},
		},
	},
	PULSE_DECIMAL_DIVIDE_BY_ZERO: {
		Message: "A decimal divide operation observed a zero divisor.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Pre-filter zero divisors with FILTER_RANGE (min > 0 or max < 0) or guard the formula with an explicit non-zero check.",
			},
		},
	},
	PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL: {
		Message: "An aggregation has no defined semantics for the decimal128 field type (e.g., AGG_MEDIAN, AGG_PERCENTILE in v1).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Aggregations", "*", "Type"},
				Hint:     "Decimal fields support exact AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX, AGG_VARIANCE, AGG_STDDEV, AGG_COUNT, AGG_DISTINCT_COUNT; pick one of those.",
				Examples: []any{"AGG_SUM", "AGG_AVERAGE", "AGG_MIN", "AGG_MAX"},
			},
		},
	},
	PULSE_SYNTH_DISTRIBUTION_UNKNOWN: {
		Message: "A synth spec referenced a distribution kind not registered in the synth package.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Hint:   "Check the distribution name against synth.AllDistributions(); common typos: gaussian -> normal, power_law -> pareto.",
			},
		},
	},
	PULSE_SYNTH_CONSTRAINT_INFEASIBLE: {
		Message: "Rejection sampling for declared constraints exceeded the allowed rejection rate; the generator gives up rather than produce biased output.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Relax the constraint, switch to a distribution that concentrates inside the constraint region, or raise max_rejection_rate if you accept the cost.",
			},
		},
	},
	PULSE_PROFILE_FIELD_UNSUPPORTED: {
		Message: "The profile layer cannot summarize this field type; the field is skipped.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Synthesize the affected field from an explicit schema spec instead of relying on profile-driven mode.",
			},
		},
	},
	PULSE_TEST_UNKNOWN_TYPE: {
		Message: "The request referenced a TestType not registered in either the row-test or post-test registry.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Tests", "*", "Type"},
				Hint:   "Check the test name against types.AllTestTypes(); confirm the test is registered for the intended tier (Tests vs PostTests).",
			},
		},
	},
	PULSE_TEST_FIELD_NOT_NUMERIC: {
		Message: "A test requires a numeric field but the named field resolves to a categorical or otherwise non-numeric schema type.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "Field"},
				Hint:   "Pick a numeric field (u*, f*, nullable_u*, decimal128) or use TEST_CHISQ with Rows/Cols for categorical association.",
			},
		},
	},
	PULSE_TEST_INVALID_ALPHA: {
		Message: "The request's Alpha value lies outside (0, 1).",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Tests", "*", "Alpha"},
				Hint:     "Pick a value in (0, 1); common choices are 0.10, 0.05, 0.01. Omit Alpha to accept the 0.05 default.",
				Examples: []any{0.05, 0.01, 0.10},
			},
		},
	},
	PULSE_TEST_INSUFFICIENT_N: {
		Message: "A test received fewer non-null observations than the minimum needed to compute its statistic.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Widen upstream filters so >= 30 non-null rows remain per group, or pick a test that tolerates small samples (Fisher exact for 2x2 instead of chi-square).",
			},
		},
	},
	PULSE_TEST_VARIANCE_ZERO: {
		Message: "One or more groups have zero sample variance, making the t- or F-statistic undefined.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "Field"},
				Hint:   "Field is constant in the affected group; pick a different field or use TEST_KS for distribution comparison on near-constant data.",
			},
		},
	},
	PULSE_TEST_SPLIT_GROUPS_LT_2: {
		Message: "A two-sample or ANOVA test observed fewer than the required number of distinct groups in the SplitBy field.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "SplitBy"},
				Hint:   "Pick a SplitBy field that resolves to >= 2 distinct categories after filtering, or relax upstream filters.",
			},
		},
	},
	PULSE_TEST_CONTINGENCY_DEGENERATE: {
		Message: "A chi-square contingency table is empty or has a single row/column.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Verify both Rows and Cols resolve to fields with more than one observed level; aggregate rare levels into an \"other\" bucket if needed.",
			},
		},
	},
	PULSE_TEST_EXPECTED_COUNT_TOO_LOW: {
		Message: "A chi-square cell's expected count is below 5; the asymptotic chi-square approximation is unreliable.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Tests", "*", "Type"},
				Hint:     "Use TEST_FISHER_EXACT for small 2x2 tables; for larger tables, pool rare levels to raise per-cell expected counts.",
				Examples: []any{"TEST_FISHER_EXACT"},
			},
		},
	},
	PULSE_TEST_FIELD2_NOT_NUMERIC: {
		Message: "Field2 for a paired or bivariate test (PEARSON_R, PAIRED_T) resolves to a non-numeric schema type.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "Field2"},
				Hint:   "Pick a numeric field (u*, f*, nullable_u*, decimal128), or cast the column via ATTR_FORMULA before running the test.",
			},
		},
	},
	PULSE_TEST_SUCCESS_VALUE_MISSING: {
		Message: "TEST_PROP_Z did not supply Params.success; the test cannot decide which category counts as a positive outcome.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Tests", "*", "Params", "success"},
				Hint:   "Supply the dictionary value that represents success (e.g. {\"success\": \"yes\"}); use pulse inspect to list the categorical's dictionary values.",
			},
		},
	},
	PULSE_TEST_CORRELATION_UNDEFINED: {
		Message: "Pearson r encountered a column with zero variance; r and its t-statistic are undefined.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Tests", "*", "Type"},
				Hint:     "Pearson r is undefined when either variable is constant; use TEST_SPEARMAN_R or TEST_KENDALL_TAU for monotonic association on near-constant data, or remove the constant column.",
				Examples: []any{"TEST_SPEARMAN_R", "TEST_KENDALL_TAU"},
			},
		},
	},
	PULSE_TEST_PAIRED_LENGTH_MISMATCH: {
		Message: "A paired test encountered rows with one of the paired columns null while the other is present; drop-pair semantics applied.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Pre-filter rows where either paired column is null with FILTER_EXCLUDE so the effective pair count is explicit; or ignore the warning if the mismatch count is small.",
			},
		},
	},
	PULSE_TEST_TIES_DOMINATE: {
		Message: "A rank-based test observed ties on >= 50% of input values; the asymptotic approximation loses accuracy.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Hint:   "Treat the p-value as advisory; for small n with heavy ties prefer an exact-permutation variant when registered, or accept the effect-direction statistic alone.",
			},
		},
	},
	PULSE_TEST_SUBJECT_MISSING: {
		Message: "A repeated-measures test found at least one subject missing one or more conditions; incomplete subjects dropped by default.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Pre-filter the cohort to retain only fully observed subjects, or ignore the warning if the dropped count is small relative to N.",
			},
		},
	},
	PULSE_TEST_BALANCED_DESIGN_REQUIRED: {
		Message: "A repeated-measures test observed unequal cell counts; only balanced designs are supported in v1.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Filter or pre-aggregate so each subject contributes exactly one observation per condition before running TEST_ANOVA_RM.",
			},
		},
	},
	PULSE_TEST_TUKEY_REQUIRES_K_GE_3: {
		Message: "Tukey HSD requires k >= 3 groups; a t-test or two-proportion z is the appropriate alternative for k = 2.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Tests", "*", "Type"},
				Hint:     "Swap TEST_TUKEY_HSD for TEST_T or TEST_WELCH (continuous) or TEST_PROP_Z (proportions) when only two groups are present.",
				Examples: []any{"TEST_T", "TEST_WELCH", "TEST_PROP_Z"},
			},
		},
	},
	PULSE_TEST_SHAPIRO_N_BOUND: {
		Message: "A Shapiro-Wilk request observed n above the supported limit (5000); use an asymptotic normality test instead.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Sample <= 5000 rows before running TEST_SHAPIRO_WILK, or use an asymptotic normality test (Anderson-Darling, D'Agostino K2) when registered.",
			},
		},
	},
	PULSE_TEST_FISHER_R_OR_C_GT_2: {
		Message: "Fisher exact requires a 2x2 table; rxc support lands separately.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Tests", "*", "Type"},
				Hint:     "Use TEST_CHISQ for rxc tables when expected counts are large enough; or filter the cohort to a 2x2 subset with FILTER_INCLUDE.",
				Examples: []any{"TEST_CHISQ"},
			},
		},
	},
	// ---------- PULSE — EXTENSIONS / LOOKUP ----------
	PULSE_EXTENSION_NAME_INVALID: {
		Message: "An embedder registration name does not match the required pattern <CATEGORY>_<NAMESPACE>_<NAME> with uppercase ASCII segments.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Hint:     "Rename the registration so it matches ^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_[A-Z][A-Z0-9]+_[A-Z][A-Z0-9_]+$, e.g. AGG_ACME_BRAND_SCORE.",
				Examples: []any{"AGG_ACME_BRAND_SCORE", "ATTR_ACME_ADJUSTMENT"},
			},
		},
	},
	PULSE_EXTENSION_NAME_RESERVED: {
		Message: "An embedder registration uses a namespace segment reserved for Pulse internals (BUILTIN / STANDARD / CORE / PULSE).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Pick a namespace that matches the embedder module (e.g. ACME for a fictional embedder); reserved namespaces are off-limits.",
			},
		},
	},
	PULSE_EXTENSION_NAME_COLLISION: {
		Message: "An embedder registration name matches a built-in operator name within the same category.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Choose a non-colliding namespaced name; built-in names are reserved for the Pulse-shipped registry.",
			},
		},
	},
	PULSE_EXTENSION_DUPLICATE: {
		Message: "The same operator name was registered more than once in a single pulse.New call.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Drop the redundant registration; only one Factory may bind a given name per Service.",
			},
		},
	},
	PULSE_EXTENSION_STREAMABLE_MISMATCH: {
		Message: "A registration declared streaming capability but its factory did not return the required streaming interface.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Either implement the streaming interface (OnlineAggregator / RowLocalAttribute / TwoPassAttribute) on the returned type, or flip Streamable=false / Mode=buffered to use the buffered path.",
			},
		},
	},
	PULSE_EXTENSION_FACTORY_PANIC: {
		Message: "An embedder factory panicked during probe-validation at registration time.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Factories must tolerate a minimal synthetic spec + schema during probe; guard against nil inputs and avoid panics. Use coded errors instead.",
			},
		},
	},
	PULSE_EXTENSION_PARAM_INVALID: {
		Message: "A ParamMeta entry has missing or contradictory fields (empty Name, unknown JSONType, Required=true with a non-nil Default).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Hint:     "Set Name, set JSONType to one of {string, number, boolean, array, object}, and drop Default when Required=true.",
				Examples: []any{"string", "number", "boolean", "array", "object"},
			},
		},
	},
	PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH: {
		Message: "An extension operator's ComponentsFunc emitted keys that are not declared in the registration's ComponentSchema. The probe rejects the registration so the runtime never sees a half-declared components contract.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Extensions", "*", "ComponentSchema", "Keys"},
				Hint:   "Add the undeclared emitted keys to ComponentSchema.Keys (with the matching JSONType + Mergeability), or remove them from the ComponentsFunc emission. See docs/src/internals/extension-points.md for the per-category contract.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Extensions", "*", "ComponentsFunc"},
				Hint:   "Drop the extra keys from the ComponentsFunc map before returning so the emission stays a subset of ComponentSchema.Keys (plus the universal-floor keys n / n_null / total_n / n_in / n_out / n_null_input the orchestrator owns).",
			},
		},
	},
	PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA: {
		Message: "An extension operator declared a ComponentsFunc without a matching ComponentSchema (or a non-empty ComponentSchema without a ComponentsFunc). Both halves of the contract must be present together; the floor-only path keeps both empty.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Extensions", "*", "ComponentSchema"},
				Hint:   "Add a ComponentSchema with the keys + Mergeability that the ComponentsFunc emits, or drop the ComponentsFunc to fall back to the floor-only path. See docs/src/internals/extension-points.md for examples.",
			},
		},
	},
	PULSE_LOOKUP_TABLE_UNKNOWN: {
		Message: "A runtime expression referenced a lookup table that is not registered on the Service.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Register the table in pulse.Options.Extensions.LookupTables before calling pulse.New, or correct the table name in the expression.",
			},
		},
	},
	PULSE_LOOKUP_MISS: {
		Message: "A lookup() call provided a key tuple that is not present in the table.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Verify the keys passed to lookup() exist in the registered table, or back the table with a Lookup func that returns a sentinel for unknown keys.",
			},
		},
	},
	PULSE_ARCHIVE_MAGIC_INVALID: {
		Message: "The file's leading bytes match neither the single-file Pulse magic (\"PULSE\\x00\\x00\\x00\") nor the zip-archive magic (PK\\x03\\x04).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Cohort", "Filename"},
				Hint:   "Verify the file is a Pulse cohort: a single-file .pulse or a Pulse shard archive. Re-import the source data or pass the correct path.",
			},
		},
	},
	PULSE_ARCHIVE_CORRUPT: {
		Message: "The zip end-of-central-directory or central directory of the shard archive could not be parsed.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Rebuild the archive from its constituent shards via `pulse shard create`; the central directory or EOCD record is missing or truncated (often the result of a crash mid-write).",
			},
		},
	},
	PULSE_SHARD_MISSING: {
		Message: "The named shard is not present in the archive's central directory.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Cohort", "Filename"},
				Hint:   "List the archive's shards via `pulse shard list <archive>` and reference an existing basename, or add the missing shard via `pulse shard add <archive> <shard>`.",
			},
		},
	},
	PULSE_SHARD_HEADER_INVALID: {
		Message: "The shard payload inside the archive failed its magic + format-version check on first read.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "The shard's bytes are not a valid single-file .pulse cohort. Remove the bad shard via `pulse shard remove`, then re-import and re-add the source data.",
			},
		},
	},
	PULSE_SHARD_SCHEMA_MISMATCH: {
		Message: "The incoming shard's structural schema (field count, per-field name/type/byte_offset/bit_position, categorical width) is not byte-equal to the archive's canonical schema.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data using the same schema as the canonical archive (run `pulse inspect <archive>` to view the canonical fields), or rebuild the archive against the new schema via `pulse shard create`.",
			},
		},
	},
	PULSE_SHARD_DICT_DIVERGENCE: {
		Message: "The incoming shard's categorical dictionary is not prefix-related to the canonical dictionary on the same field; reorders and inserts before existing values are rejected.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Align dictionaries upstream so the incoming shard either equals the canonical dictionary up to its length (older snapshot) or extends it at the tail (new values appended). Re-import the source with the corrected dictionary order, or split into a separate archive.",
			},
		},
	},
	PULSE_SHARD_DICT_WIDTH_OVERFLOW: {
		Message: "The categorical dictionary extension would exceed the declared field width's capacity (256 for u8, 65 536 for u16, 2^32 for u32).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Rebuild the archive with a wider categorical type for the affected field (categorical_u8 -> categorical_u16, or categorical_u16 -> categorical_u32). The width is fixed at folder creation; pick growth headroom up front next time.",
			},
		},
	},
	PULSE_SHARD_DESCRIPTION_DIVERGENCE: {
		Message: "An incoming shard's per-field description differs from the canonical schema's; descriptions are advisory and the canonical description wins, but the divergence is surfaced as a warning so embedders can keep cohort metadata in sync.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "Fields", "*", "Description"},
				Hint:   "Update the source data's per-field description to match the canonical archive's, or accept the warning (downstream consumers see the canonical description).",
			},
		},
	},
	PULSE_SHARD_RESERVED_NAME: {
		Message: "Cannot insert a shard whose basename collides with the reserved canonical schema entry (`_schema.pulse`); the reserved name is addressable only through the archive's canonical-schema channel.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Shard", "Basename"},
				Hint:   "Rename the incoming shard to a non-reserved basename (e.g. `20190101.pulse`) before adding it to the archive.",
			},
		},
	},
	PULSE_SHARD_NAME_COLLISION: {
		Message: "Two shards in the same archive share a basename; zip entry names are flat and must be unique within an archive.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Shard", "Basename"},
				Hint:   "Rename one of the colliding shards to a unique basename before insertion. Inspect the existing archive via `pulse shard list <archive>` to confirm the taken names.",
			},
		},
	},
	PULSE_CHAIN_NOT_MERGEABLE: {
		Message: "A ProcessChain stage uses an operator that the v1 chain gate does not yet support (windows, features, tests, regressions, two-pass attributes, AGG_FREQUENCY, AGG_MODE, or a non-mergeable grouper/aggregator).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Stages", "*", "Request"},
				Hint:   "Run the offending stage as a standalone Process call (the details payload names the rejecting stage index) or restructure the stage to use mergeable, scalar-emitting aggregators (COUNT, SUM, AVERAGE, MIN, MAX, RANGE, VARIANCE, STDDEV, DISTINCT_COUNT, NULL_COUNT) with row-local attributes (FORMULA, DATE_PART) and mergeable groupers (GROUP_CATEGORY, GROUP_RANGE).",
			},
		},
	},
	PULSE_CHAIN_EMPTY: {
		Message: "A ProcessChain request must carry at least one stage with a non-nil Request.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Stages"},
				Hint:   "Add at least one ChainStage with a real Request, or call Process directly when only a single stage is needed.",
			},
		},
	},
	PULSE_COMPOSE_LABEL_COLLISION: {
		Message: "Two slots in the ComposedRequest resolve to the same final Label after auto-default synthesis. Compose-only overlay kinds resolve sibling references by label, so names must be unique across slots.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests", "*", "Label"},
				Hint:   "Rename one of the colliding slots, or clear a caller-supplied Label that matches the auto-default (`request_<index+1>`) of another slot. The error details carry the offending label string and the colliding slot indices.",
			},
		},
	},
	PULSE_JOIN_TYPE_MISMATCH: {
		Message: "A join key pair pairs fields whose schema types differ. Hash join keys must compare equal byte-for-byte after normalisation; type mismatches block this.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins", "*", "On"},
				Hint:   "Re-import the right cohort with a matching type for the key field (e.g. cast u32 → u64 to align with the left side), or pre-aggregate one side to expose a compatible key column.",
			},
		},
	},
	PULSE_JOIN_KIND_NOT_IMPLEMENTED: {
		Message: "The requested join Kind is reserved but not implemented yet. v1 supports \"inner\" (and empty == \"inner\"); \"left\", \"outer\", \"anti\" land in a follow-up.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins", "*", "Kind"},
				Hint:   "Set Kind to \"inner\" (or leave it empty) for this Pulse version. Track upstream for outer/left/anti support.",
			},
		},
	},
	PULSE_JOIN_FIELD_UNKNOWN: {
		Message: "A JoinSpec.On entry references a field not present in either the left or right cohort's schema.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins", "*", "On"},
				Hint:   "Verify the field exists in the corresponding cohort via pulse_inspect (left side) or pulse_inspect of the JoinSpec.Right path.",
			},
		},
	},
	PULSE_JOIN_KEYS_EMPTY: {
		Message: "A JoinSpec must carry at least one equi-join key pair under On.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins", "*", "On"},
				Hint:   "Add at least one OnPair {LeftField, RightField} naming the schema columns to equate.",
			},
		},
	},
	PULSE_JOIN_TOO_MANY: {
		Message: "v1 supports exactly one JoinSpec per Request. Multi-join chains land in a follow-up.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins"},
				Hint:   "Split into multiple Process calls — pipe the first join's output through pulse_import or an intermediate .pulse handle, then attach the second join to a fresh Request.",
			},
		},
	},
	PULSE_JOIN_FIELD_COLLISION: {
		Message: "The joined schema would carry two fields with the same name. Set JoinSpec.As to prefix the right-side fields, or rename one side at import.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins", "*", "As"},
				Hint:   "Set As to a short identifier (e.g. \"r_\") so right-side columns become r_<name> in the joined record.",
			},
		},
	},
	PULSE_RANGE_EMPTY: {
		Message: "A labeled-date-range set (GROUP_DATE_RANGES / FILTER_DATE_RANGES inline ranges, or a named range table) was presented with zero ranges.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Supply at least one {label, start, end} range in the operator's Params (or the referenced range table). Each range needs a label; start/end may be omitted for an open bound.",
			},
		},
	},
	PULSE_RANGE_INVALID: {
		Message: "A labeled date range is malformed: a start or end boundary is not a parseable date literal, or the range is bounded on both sides with start after end.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Write boundaries as ISO dates (YYYY-MM-DD) and ensure start <= end. Omit or null a boundary to leave that side open (unbounded).",
			},
		},
	},
	PULSE_RANGE_DUPLICATE_LABEL: {
		Message: "Two ranges in the same labeled-date-range set share a Label; labels are bucket keys and must be unique within a set.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Rename one of the colliding ranges so every label in the set is distinct.",
			},
		},
	},
	PULSE_RANGE_OVERLAP: {
		Message: "Two ranges in the same labeled-date-range set cover an overlapping span of days (bounds are inclusive; open boundaries extend to -inf/+inf), so a record day could map ambiguously to two labels.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Adjust the boundaries so ranges are disjoint. Because bounds are inclusive, make one range end the day before the next begins (e.g. ...-03-31 then ...-04-01). Only one range may have an open lower bound and one an open upper bound.",
			},
		},
	},
	PULSE_RANGE_SOURCE_AMBIGUOUS: {
		Message: "A GROUP_DATE_RANGES / FILTER_DATE_RANGES operator must name exactly one range source; supplying both an inline `ranges` array and a named `table`, or neither, is ambiguous.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "In the operator's Params, provide exactly one of: an inline `ranges` array of {label, start, end} entries, or a `table` naming a registered range table. Remove one when both are present, or add one when neither is.",
			},
		},
	},
	PULSE_RANGE_TABLE_UNKNOWN: {
		Message: "A GROUP_DATE_RANGES / FILTER_DATE_RANGES operator referenced a range table by a name that is not registered.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Set Params.table to a table registered via Options.Extensions.RangeTables or the PULSE_RANGE_TABLES_DIR loader, or register the referenced table before running the request.",
			},
		},
	},
	PULSE_LABEL_FIELD_UNKNOWN: {
		Message: "A LabelBinding references a field name not present in the cohort schema.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Field"},
				Hint:   "Use a field name reported by pulse_inspect; categorical fields only.",
			},
		},
	},
	PULSE_LABEL_FIELD_NOT_CATEGORICAL: {
		Message: "A LabelBinding references a non-categorical field. Labels translate dictionary string values; numeric and date fields have no dictionary key to translate.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Field"},
				Hint:   "Pick a categorical_u8 / categorical_u16 / categorical_u32 field. For numeric translation, derive a categorical column upstream during import.",
			},
		},
	},
	PULSE_LABEL_TABLE_UNKNOWN: {
		Message: "A LabelBinding references a label-table name that is not registered on the Service.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Table"},
				Hint:   "Register the table in pulse.Options.Extensions.LabelTables before calling pulse.New, or correct the table name in the binding.",
			},
		},
	},
	PULSE_LABEL_FIELD_COLLISION: {
		Message: "A LabelBinding in augment mode would emit a sibling \"<field>_label\" column whose name already exists in the request's output schema.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Labels", "*", "Mode"},
				Hint:     "Switch the binding to replace mode, or rename the colliding existing field upstream so \"<field>_label\" is free.",
				Examples: []any{"replace", "augment"},
			},
		},
	},
	PULSE_LABEL_DUPLICATE_BINDING: {
		Message: "Two LabelBinding entries target the same Field. Each field may carry at most one binding per request.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels"},
				Hint:   "Drop the duplicate entry; a single binding governs both replace and augment behaviour for one field.",
			},
		},
	},
	PULSE_LABEL_TABLE_NOT_ENUMERABLE: {
		Message: "A reverse label lookup was attempted against a function-driven table (Lookup closure only, no static Rows map). Reverse search requires enumerable Rows.",
		// No caller-request field to repair: the fix is registration-time
		// (register the table with a static Rows map, e.g. from a cached
		// snapshot, rather than a Lookup-only closure).
		FixupNotApplicable: true,
	},
	PULSE_LABEL_COLLISION: {
		Message: "Two distinct source values resolve to the same label string in replace mode. The output disambiguates with the source value in parentheses (e.g. \"United States (US)\").",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Labels", "*", "Mode"},
				Hint:     "Switch to augment mode if you need the source value preserved verbatim, or clean the table so labels are unique per source value.",
				Examples: []any{"replace", "augment"},
			},
		},
	},
	PULSE_LABEL_LOOKUP_MISS: {
		Message: "One or more categorical values present in the data have no entry in the label table. The output falls back to the raw resolved categorical value.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Table"},
				Hint:   "Extend the label table to cover the unresolved values, or back the table with a Lookup func that synthesises a fallback for unknown keys.",
			},
		},
	},
	PULSE_CROSSTAB_EMPTY_ROWS: {
		Message: "The Crosstab section has no row-axis groupers. A crosstab requires at least one grouper on each axis.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Rows"},
				Hint:   "Add at least one Group entry to Crosstab.Rows; for a single-axis aggregation use a plain Process request with Groups + Aggregations instead.",
			},
		},
	},
	PULSE_CROSSTAB_EMPTY_COLUMNS: {
		Message: "The Crosstab section has no column-axis groupers. A crosstab requires at least one grouper on each axis.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Columns"},
				Hint:   "Add at least one Group entry to Crosstab.Columns; for a single-axis aggregation use a plain Process request with Groups + Aggregations instead.",
			},
		},
	},
	PULSE_CROSSTAB_MISSING_CELL: {
		Message: "The Crosstab section has no Cell aggregation. The cell aggregation is the value emitted per (row-tuple, column-tuple) intersection and is required.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Cell"},
				Hint:     "Set Crosstab.Cell to an Aggregation with Type + Field; AGG_COUNT is the classic count crosstab cell, AGG_AVERAGE / AGG_SUM the typical numeric cells.",
				Examples: []any{"AGG_COUNT", "AGG_AVERAGE", "AGG_SUM"},
			},
		},
	},
	PULSE_CROSSTAB_CONFLICTS_WITH_GROUPS: {
		Message: "A Crosstab section was supplied alongside top-level Groups or Aggregations on the same Request. The two surfaces are mutually exclusive.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups"},
				Hint:   "Remove top-level Groups + Aggregations from the Request; the Crosstab section already lowers to a grouped request internally. If both shapes are needed, split into two Compose requests.",
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE: {
		Message: "The Crosstab section requested a normalization mode whose required margin cannot be computed (e.g. an aggregator whose margin is undefined on an empty axis).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Normalize"},
				Hint:     "Switch Normalize to \"none\" to leave cells un-normalized, or pick a cell aggregator whose margin is defined under the chosen normalization direction.",
				Examples: []any{"none", "row", "column", "total"},
			},
		},
	},
	PULSE_CROSSTAB_AGG_UNCLASSIFIED: {
		Message:            "Internal guard: the margin pass encountered an aggregator with no MarginReducibility classification. Adding a new aggregator without updating AggregationType.MarginReducibility is the only way to reach this code.",
		FixupNotApplicable: true,
	},
	PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE: {
		Message: "The Crosstab section's normalize_level falls outside the valid range for the targeted axis. Valid depths are zero-indexed from the top of the axis: 0 for the first grouper, len(axis)-1 for the leaf.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "NormalizeLevel"},
				Hint:     "Pick a depth in [0, len(axis)-1] where axis = Crosstab.Rows when Normalize=row and Crosstab.Columns when Normalize=column. Omit the field entirely to default to the leaf (the original per-leaf-tuple normalization).",
				Examples: []any{0, 1, 2},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_LEVEL_WITHOUT_NESTED_AXIS: {
		Message: "The Crosstab section set normalize_level without selecting a normalization direction. The level selector only has meaning when Normalize is row or column.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Normalize"},
				Hint:     "Set Crosstab.Normalize to \"row\" or \"column\" to choose which axis the level descends, or drop the normalize_level field to keep raw cell values.",
				Examples: []any{"row", "column"},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_LEVEL_INCOMPATIBLE: {
		Message: "The Crosstab section set normalize_level alongside Normalize=total. Total normalization uses a scalar grand-total denominator with no axis to descend; the level selector applies only to Normalize=row or Normalize=column.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Normalize"},
				Hint:     "Switch Normalize to \"row\" or \"column\" if a partial-depth denominator is intended, or drop normalize_level to keep the scalar grand-total denominator.",
				Examples: []any{"row", "column"},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_WITHIN_OUT_OF_RANGE: {
		Message: "The Crosstab section's normalize_within falls outside the valid range for the OPPOSITE axis. Valid depths are zero-indexed from the top of the other axis: 0 for the first grouper, len(other-axis)-1 for the leaf. The other axis is Crosstab.Columns when Normalize=row and Crosstab.Rows when Normalize=column.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "NormalizeWithin"},
				Hint:     "Pick a depth in [0, len(other-axis)-1] where the other axis is Crosstab.Columns when Normalize=row and Crosstab.Rows when Normalize=column. Omit the field entirely to keep the full row / column marginal denominator (the original behavior).",
				Examples: []any{0, 1},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_WITHIN_WITHOUT_AXIS: {
		Message: "The Crosstab section set normalize_within without selecting a normalization direction. The cross-axis partition selector only has meaning when Normalize is row or column.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Normalize"},
				Hint:     "Set Crosstab.Normalize to \"row\" or \"column\" to choose which axis hosts the cell-marginal numerator, or drop the normalize_within field to keep raw cell values.",
				Examples: []any{"row", "column"},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_WITHIN_INCOMPATIBLE: {
		Message: "The Crosstab section set normalize_within alongside Normalize=total. Total normalization uses a scalar grand-total denominator with no other axis to partition; normalize_within applies only to Normalize=row or Normalize=column.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Normalize"},
				Hint:     "Switch Normalize to \"row\" or \"column\" if a cross-axis partitioned denominator is intended, or drop normalize_within to keep the scalar grand-total denominator.",
				Examples: []any{"row", "column"},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_MAP_VALUED: {
		Message: "The Crosstab section requested a normalize mode (row / column / total) on a cell aggregator whose output is map-valued (AGG_SET_FREQUENCY emits map[string]int per cell). Dividing one map by another is undefined; the normalize directive cannot be applied.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Normalize"},
				Hint:     "Drop the normalize directive (or set it to \"none\") to keep map-valued cells, or swap the cell aggregator to a scalar form (e.g. AGG_SET_CARDINALITY_SUM, AGG_SET_CARDINALITY_AVG) so normalization is defined.",
				Examples: []any{"none"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Cell", "Type"},
				Hint:     "Pick a scalar aggregator if the normalize directive must stay. AGG_SET_CARDINALITY_SUM gives summable popcount per cell; AGG_SET_CARDINALITY_AVG gives mean selections.",
				Examples: []any{"AGG_SET_CARDINALITY_SUM", "AGG_SET_CARDINALITY_AVG"},
			},
		},
	},
	PULSE_REQUEST_UNKNOWN_FIELD: {
		Message: "The request JSON contains a top-level key that is not a recognised Request slot. JSON decoding silently ignores unknown keys, so the intended operation is dropped and the request runs as if it were absent. A common cause is using a manifest operator-catalog field name (\"groupers\", \"aggregators\") as the request key instead of the request slot name (\"groups\", \"aggregations\").",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Hint:     "Rename the offending key to its canonical request slot (the error details carry the nearest valid key). Request slots are: cohort, filterers, features, attributes, groups, aggregations, windows, sort, tests, post_tests, regressions, joins, labels, outputs. The manifest's \"groupers\"/\"aggregators\" name the available OPERATORS; the request slots that hold them are \"groups\"/\"aggregations\".",
				Examples: []any{"groupers -> groups", "aggregators -> aggregations"},
			},
		},
	},
	// ---------- PULSE — OVERLAYS ----------
	PULSE_OVERLAY_KIND_UNKNOWN: {
		Message: "A Request.Overlays entry references an OverlayKind that is not in the catalog (types.AllOverlayKinds()). The validator rejects unknown kinds at predict time; reaching this code at runtime means the request bypassed validation.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Kind"},
				Hint:     "Pick a registered OverlayKind. Check the overlay catalog via pulse manifest --json (overlay-kinds section); spelling and casing must match exactly.",
				Examples: []any{"OVERLAY_INDEX_VS_MARGIN"},
			},
		},
	},
	PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE: {
		Message: "An OverlaySpec's Ref does not match the host shape required by the chosen Kind. OVERLAY_INDEX_VS_MARGIN requires a Ref.Margin pointer with a known MarginAxis (row / column / grand) AND a MATRIX-shaped host (Request.Crosstab non-nil).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Ref", "Margin"},
				Hint:     "Set Ref.Margin = {Axis: \"row\" | \"column\" | \"grand\"} for OVERLAY_INDEX_VS_MARGIN; add Request.Crosstab to provide a MATRIX host so the margin slot has a denominator.",
				Examples: []any{"row", "column", "grand"},
			},
		},
	},
	PULSE_OVERLAY_COMPONENTS_REQUIRED: {
		Message: "An overlay kind that reads per-cell components (n / weight sums / Welford triples / margin counts) ran against a host built with components disabled. The OVERLAY_PAIRWISE_* family needs the components block to resolve each pair leg's sample size.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"DisableComponents"},
				Hint:     "Re-run with components enabled: clear Options.DisableComponents (and any per-request disable_components override) so Response.Components.Crosstab carries the cell n / Welford triples the pairwise overlay divides by.",
				Examples: []any{false},
			},
		},
	},
	PULSE_OVERLAY_SCOPE_UNSUPPORTED: {
		Message: "An OverlaySpec named a Scope that is not supported for the chosen Kind. OVERLAY_INDEX_VS_MARGIN currently supports Scope=cell only; row / column / total / matrix / group land in later releases alongside the matching payload shapes.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Scope"},
				Hint:     "Set Scope to \"cell\" for OVERLAY_INDEX_VS_MARGIN; the wider projection scopes land in later releases.",
				Examples: []any{"cell"},
			},
		},
	},
	PULSE_OVERLAY_REF_ZERO: {
		Message: "An overlay handler observed a zero, missing, or non-finite margin denominator. The affected cell stays absent on the overlay payload; the warning carries the row / column index and margin axis so callers can audit the failing cells. Warning-class — surfaced as a Response.Warning, never as an envelope error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Pre-filter rows that contribute zero margin sums (e.g. FILTER_EXCLUDE on the empty / null axis), or pick a cell aggregator whose denominator is bounded away from zero (AGG_COUNT margins are non-negative integers; AGG_SUM on signed numeric fields may legitimately produce zero).",
			},
		},
	},
	PULSE_OVERLAY_EXPECTED_LOW: {
		Message: "An inferential overlay (OVERLAY_CHISQ_MATRIX / CHISQ_ROW / CHISQ_COL / FISHER_EXACT_CELL / CHISQ_VS_POP) observed an expected count below the χ² approximation's reliability threshold (canonical rule: any expected cell below 5; Fisher's 2×2 rule: any cell < 1 OR ≥ 20% cells < 5). The statistic is still emitted alongside; the warning flags rows / columns / cells / categories where the approximation may be unreliable. Warning-class — surfaced as a Response.Warning, never as an envelope error. PRD FR-J1 shares the code across the χ² / Fisher overlay surfaces — Crosstab MATRIX-host kinds and Facet GROUP-host CHISQ_VS_POP emit the same warning shape so renderers can lift it into a single \"approximation may be unreliable\" surface regardless of host shape.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Crosstab", "Rows"},
				Hint:   "Lower the nested-axis Level (collapse a finer grouper) or widen the cells (raise observed counts) so the contingency margins reach the χ² reliability threshold — coarsen GROUP_DATE buckets, widen GROUP_RANGE intervals, or drop a deep grouper on the row axis. Alternatively, switch to OVERLAY_FISHER_EXACT_CELL (exact 2×2 p-values) which stays exact in the low-count regime.",
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Crosstab", "Columns"},
				Hint:   "Same as the row-axis fix — coarsen or drop a column-axis grouper so the contingency cells aggregate enough observations to drive expected counts past the reliability threshold.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"FacetRequest", "DiscreteTopK"},
				Hint:   "OVERLAY_CHISQ_VS_POP fix: raise the subset record count by widening the FacetRequest filter (drop a restrictive Filterer) OR coarsen the facet's discrete dictionary (the host's DiscreteTopK ceiling) so categories merge and observed counts rise. Population frequency is fixed at the population cohort level; only subset_N (sum of host counts) and the number of categories scale the expected counts.",
			},
		},
	},
	PULSE_OVERLAY_LEVEL_OUT_OF_RANGE: {
		Message: "An OverlaySpec's Level selector exceeds the nested-axis depth of the relevant host axis. Valid depths are zero-indexed from the top of the axis: Row axis depth = len(Crosstab.Rows), Column axis depth = len(Crosstab.Columns). Mirrors PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE on the crosstab normalize_level surface.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Level"},
				Hint:     "Pick a depth in [0, len(axis)-1] where axis = Crosstab.Rows for row-axis overlays and Crosstab.Columns for column-axis overlays. Omit the field entirely to default to the leaf (the deepest grouper on the axis).",
				Examples: []any{0, 1, 2},
			},
		},
	},
	PULSE_OVERLAY_PARAM_MISSING: {
		Message: "An OverlaySpec did not supply a Params entry that the chosen Kind requires. OVERLAY_INDEX_VS_ROLLING_MEAN requires Params[\"window\"] (positive integer) per the WIN_* operator convention; the window width lives on Params, not on Ref. Surfaced at both predict (descriptor.validateOverlayIndexVsRollingMean) and runtime (processing.applyIndexVsRollingMean) with Details carrying the kind and the missing param name.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params"},
				Hint:     "Set Params on the OverlaySpec to a JSON object containing the required keys for the chosen Kind. For OVERLAY_INDEX_VS_ROLLING_MEAN populate `{\"window\": N}` where N is a positive integer naming the rolling-window width (e.g. {\"window\": 3} for a 3-point rolling mean). Per the WIN_* operator convention, the window width is the only required param today.",
				Examples: []any{map[string]any{"window": 3}, map[string]any{"window": 7}, map[string]any{"window": 12}},
			},
		},
	},
	PULSE_OVERLAY_FORMULA_PARSE_ERROR: {
		Message: "An OVERLAY_FORMULA spec's Params[\"formula\"] string failed to parse as an expr-lang expression. The formula was syntactically invalid — typically unbalanced parentheses, a stray operator, an unterminated string literal, or a typo on a keyword. Surfaced at both predict (descriptor.validateFormulaOverlay) and runtime (processing.compileFormulaProgram) so authors catch the failure before evaluation begins. Details carry {formula, parse_error, kind, index} — the parser's own position-aware error string is preserved verbatim under Details.parse_error so renderers can surface the offending column / token alongside the offending input.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "formula"},
				Hint:     "Re-read the formula string with the parser message in mind (Details.parse_error names the offending position or token). Common fixes: balance every '(' with a matching ')', terminate every '\"' string literal, and confirm operators are valid expr-lang operators (+, -, *, /, %, **, ==, !=, <, <=, >, >=, &&, ||, !). The expr-lang grammar is documented under docs/src/internals/extension-points.md (the same evaluator backs ATTR_FORMULA and FILTER_EXPRESSION, so any expression that parses there parses here).",
				Examples: []any{map[string]any{"formula": "(cell - margin_grand) / sd_grand"}, map[string]any{"formula": "value / total"}},
			},
		},
	},
	PULSE_OVERLAY_FORMULA_TYPE_MISMATCH: {
		Message: "An OVERLAY_FORMULA expression returned a value whose Go type cannot be coerced to a numeric Statistic (float64). The runtime accepts float64 / float32 / int / int64 natively, widens bool to 0.0 / 1.0, and rejects everything else (strings, maps, slices, nil, etc.) — overlays must emit numeric statistics so the renderer can plot them. Surfaced at runtime by processing.applyFormula after expr.Run returns. Details carry {returned_type, formula, kind, index} so the renderer can surface both the offending Go type and the offending input.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "formula"},
				Hint:     "Ensure the formula's top-level expression evaluates to a numeric value (float64 / float32 / int / int64) or a bool (bool widens to 0.0 / 1.0). If the formula uses a custom function registered via pulse.Options.Extensions.ExprFunctions, verify the function returns a numeric Go type rather than a string, slice, or composite type — see docs/src/internals/extension-points.md for the ExprFunctions contract. If the formula uses lookup() against a LookupTables registration, verify the row value column is numeric.",
				Examples: []any{map[string]any{"formula": "cell / margin_row"}, map[string]any{"formula": "(value - prior) / prior"}},
			},
		},
	},
	PULSE_OVERLAY_FORMULA_INVALID_IDENT: {
		Message: "An OVERLAY_FORMULA expression references an identifier (variable or function) that is not allowed for the resolved host shape. The per-shape variable namespace is fixed: MATRIX hosts expose `cell, margin_row, margin_col, margin_grand, sd_row, sd_col, sd_grand` (plus `ref_cell` and the dotted `slot.<label>.cell` namespace on Compose hosts); SERIES hosts expose `value, total, prior` (plus opt-in `baseline` when Params[\"baseline_position\"] is set, plus `ref_value` on Compose hosts); SCALAR hosts expose `value` (plus `ref` on Compose hosts). The function namespace is the expr-lang stdlib plus every pulse.Options.Extensions.ExprFunctions registration and (when LookupTables are registered) the auto-injected lookup() builtin. Surfaced at predict (descriptor.validateFormulaOverlay) by walking the parsed AST. Details carry {unknown_identifier, host_shape, allowed | allowed_funcs, formula, kind, index}; Compose-only `slot.<label>.<field>` identifiers on a Request host additionally carry {reason: \"slot namespace is Compose-only\"}.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "formula"},
				Hint:     "Check the allowed identifier set for shape Details.host_shape against Details.allowed (variables) or Details.allowed_funcs (functions) — the per-shape namespace table lives in the FORMULA section of skills/overlay-system.md. If the identifier was meant to be a custom function, register it via pulse.Options.Extensions.ExprFunctions before calling pulse.New (see docs/src/internals/extension-points.md for the registration recipe — naming policy ^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH)_*$ applies to operators, not to expr functions; expr function names are free-form). If the identifier was meant to be a variable not in the per-shape table, register a custom kind via pulse.Options.Extensions.OverlayKinds — FORMULA cannot widen its variable namespace from outside. Compose `slot.<label>.<field>` identifiers only resolve on Compose-host overlays (FacetRequest.Overlays / ComposedRequest.Overlays), not on Request.Overlays.",
				Examples: []any{map[string]any{"formula": "(cell - margin_grand) / sd_grand"}, map[string]any{"formula": "value / total"}, map[string]any{"formula": "(slot.us.cell - slot.uk.cell) / slot.world.cell"}},
			},
		},
	},
	PULSE_OVERLAY_REF_UNKNOWN: {
		Message: "An overlay handler named a reference that does not resolve to a known slot on the host. Three arms today: (1) sibling-reference overlays (OVERLAY_DELTA_VS_SIBLING / OVERLAY_INDEX_VS_SIBLING) name a Sibling (Field, Value) pair that does not match any observed axis-key value on the SERIES host; (2) baseline-index overlays (OVERLAY_INDEX_VS_BASELINE / OVERLAY_DELTA_VS_BASELINE / OVERLAY_INDEX_VS_ROLLING_MEAN / OVERLAY_YOY) name a Position ordinal outside [0, host.GroupCount()) — Details carry {baseline_index, series_length}; (3) Facet-host population overlays (OVERLAY_INDEX_VS_POP / OVERLAY_ZSCORE_VS_POP / OVERLAY_CHISQ_VS_POP / OVERLAY_KS_VS_POP) name a population field absent from the host FacetResult — Details carry {field, available_fields}. The affected layer surfaces NaN statistics across every present entry (arms 1–2) or fails fast (arm 3). Warning-class for arms 1–2, runtime error for arm 3 — surfaced as a Response.Warning or envelope error depending on arm.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Ref", "Sibling", "Field"},
				Hint:   "Set Ref.Sibling.Field to one of the Request.Groups[*].Field names. Run pulse predict --json on the request to confirm the grouper fields the request lowers to; categorical grouper Fields are the typical match.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Ref", "Sibling", "Value"},
				Hint:   "Set Ref.Sibling.Value to an observed axis-key value for the named Field. Run pulse inspect --json on the cohort to enumerate the field's dictionary values, or pulse facet --field <name> to confirm the post-filter observed set.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Ref", "BaselineIndex", "Position"},
				Hint:   "Set Ref.BaselineIndex.Position to a 0-based ordinal in [0, host series length). Run pulse predict --json or process --json with no overlay to confirm the series length the host produces (or process the host first and count Response.Data groups).",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Ref", "Population", "Cohort"},
				Hint:   "Population overlays resolve against a FacetResult — ensure the named field is present in FacetRequest.Fields so the host pass computes per-value counts / Welford state for it. The error Details carry available_fields enumerating the host's resolved slots; pick one of those, or extend FacetRequest.Fields with the missing field name.",
			},
		},
	},
	PULSE_OVERLAY_YOY_FREQUENCY_MISSING: {
		Message: "An OVERLAY_YOY spec did not supply a `frequency` Param either on the OverlaySpec or on the host's GROUP_DATE grouper. The YoY kind cannot infer the correct prior-period stride from the GROUP_DATE `component` slot alone because the per-component stride for 'one year prior' varies by component (annual ⇒ 1 ordinal, quarterly ⇒ 4 ordinals, monthly ⇒ 12 ordinals, weekly ⇒ 52 ordinals, daily ⇒ 365-day calendar arithmetic, hourly ⇒ 365×24-hour arithmetic). The handler reads the explicit `frequency` value from `spec.Params[\"frequency\"]` first (the YoY's own override) and falls back to `req.Groups[0].Params[\"frequency\"]` (the canonical GROUP_DATE authoring slot). Surfaced at both predict (descriptor.validateOverlayYoY) and runtime (processing.applyYoY).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Groups", "0", "Params"},
				Hint:     "Set the host GROUP_DATE grouper's Params to a JSON object carrying `\"frequency\": \"<freq>\"` where <freq> is one of `annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly` and matches the GROUP_DATE `component` slot (annual↔year, quarterly↔quarter, monthly↔month, weekly↔week, daily↔day, hourly↔hour). This is the canonical authoring slot — every OVERLAY_YOY spec sharing the same Request will pick the value up via the fallback read at processing.applyYoY.",
				Examples: []any{map[string]any{"component": "month", "frequency": "monthly"}, map[string]any{"component": "year", "frequency": "annual"}, map[string]any{"component": "quarter", "frequency": "quarterly"}},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params"},
				Hint:     "Populate `OverlaySpec.Params[\"frequency\"]` directly on the YoY overlay if the host GROUP_DATE config is owned by other code (e.g. a shared chain stage). The handler reads this slot first and treats it as a per-overlay override of the grouper's `frequency` Param, so you can ship the YoY decoration without touching the request's grouper config. Allowed values: `annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly` (must match the host's GROUP_DATE `component`: annual↔year, quarterly↔quarter, monthly↔month, weekly↔week, daily↔day, hourly↔hour).",
				Examples: []any{map[string]any{"frequency": "monthly"}, map[string]any{"frequency": "annual"}, map[string]any{"frequency": "quarterly"}, map[string]any{"frequency": "weekly"}, map[string]any{"frequency": "daily"}, map[string]any{"frequency": "hourly"}},
			},
		},
	},
	PULSE_OVERLAY_CHAIN_STAGE_SHAPE_DIVERGENT: {
		Message: "A whole-chain overlay spec (OVERLAY_INDEX_VS_STAGE / OVERLAY_DELTA_VS_STAGE) resolved a Target stage and a Ref stage whose host result shapes disagree (one is matrix, the other series; one is scalar, the other series; etc.). The handler cannot fold per-coordinate arithmetic when target and reference do not share a coordinate grid; the layer surfaces an empty payload that inherits the target stage's shape and the warning carries the offending pair of shapes plus the originating (target_index, ref_index) so callers can audit the chain and collapse one stage so both produce the same shape, or remove the overlay.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Stages"},
				Hint:   "Collapse one stage so both Target and Ref produce the same host shape — switch the Crosstab stage to a Process stage with the same Groups + Aggregations (matrix → series) OR drop the Groups slot from the series-producing stage to fold to a scalar (series → scalar). Both stages must produce matrix-shaped OR both series-shaped OR both scalar-shaped Response objects for the per-coordinate arithmetic to align.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Target"},
				Hint:   "Repoint the OverlaySpec's Target StageRef at a stage whose shape matches the Ref stage. Run pulse predict --json on the chain request to confirm each stage's predicted output shape; ChainResponse.NormalizedRequest echoes the per-stage normalized form for the same purpose.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Ref"},
				Hint:   "Repoint the OverlaySpec's Ref StageRef at a stage whose shape matches the Target stage. Same fix as the Target arm; pick whichever stage is the more authoritative baseline for the renderer's framing.",
			},
		},
	},
	PULSE_OVERLAY_TARGET_UNKNOWN: {
		Message: "An overlay spec named a Target that does not resolve to a known slot or stage. Two host families share this code distinguished by the `which: \"target\"` Detail: (1) Compose overlay spec — a `ComposeOverlaySpec.Targets[j]` entry names a slot label that does not appear in `ComposedRequest.Requests[].Label` (or the slot resolved to nil because that slot failed under FailFast=false); Details carry `slot_label`, the spec `index`, and the offending `target_index`. (2) Chain overlay spec — a whole-chain `ChainOverlaySpec.Target` StageRef whose `Index` lands outside `[0, len(Stages))` or whose `Name` does not match any `ChainStage.Name`; Details carry the offending `stage_index` / `stage_name`. In both arms the handler returns the coded error without producing an overlay layer. Sibling of PULSE_OVERLAY_REFERENCE_UNKNOWN.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Targets"},
				Hint:   "Set `Targets` entries to labels declared in `ComposedRequest.Requests[].Label`. The resolver decodes each Targets[j] string against the slot-label map built once per Compose call; unknown labels (or labels naming a slot that failed under FailFast=false) fail loud. Run pulse predict --json on the ComposedRequest to confirm the resolved slot label set.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Target", "Index"},
				Hint:     "Chain-overlay arm: set Target.Index to a 0-based ordinal in [0, len(Stages)). Run pulse predict --json on the chain request to confirm the stage count; omit Target entirely to default to the latest stage (len(Stages) - 1).",
				Examples: []any{0, 1, 2},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Target", "Name"},
				Hint:   "Chain-overlay arm: set Target.Name to the Name of an existing ChainStage on the request. Names must match exactly (case-sensitive). Run pulse predict --json on the chain request to confirm the stage names the chain executor sees.",
			},
		},
	},
	PULSE_OVERLAY_REFERENCE_UNKNOWN: {
		Message: "An overlay spec named a Reference that does not resolve to a known slot or stage. Two host families share this code distinguished by the `which: \"reference\"` Detail: (1) Compose overlay spec — `ComposeOverlaySpec.Reference` is empty, names a slot label absent from `ComposedRequest.Requests[].Label`, or resolved to nil because that slot failed under FailFast=false; Details carry `slot_label` and the spec `index`. (2) Chain overlay spec — a whole-chain `ChainOverlaySpec.Ref` StageRef whose `Index` is out of range OR `Name` is unmatched, or the spec did not populate Ref at all (Ref has no default unlike Target). The code also covers the missing-reference-cell / missing-reference-row warning on the CHAIN-host DELTA family (OVERLAY_DELTA_VS_STAGE) with `ref_missing: true` (handler folds against an implicit zero reference).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Reference"},
				Hint:   "Set `Reference` to one of the labels in `ComposedRequest.Requests[].Label`. The resolver decodes the Reference string against the slot-label map built once per Compose call; empty, unknown, or failed-slot labels fail loud. Run pulse predict --json on the ComposedRequest to confirm the resolved slot label set.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Ref", "Index"},
				Hint:     "Chain-overlay arm: set Ref.Index to a 0-based ordinal in [0, len(Stages)). Every whole-chain ChainOverlaySpec MUST name a baseline — Ref has no default (unlike Target which defaults to the latest stage). Run pulse predict --json on the chain request to confirm the stage count.",
				Examples: []any{0, 1, 2},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Ref", "Name"},
				Hint:   "Chain-overlay arm: set Ref.Name to the Name of an existing ChainStage on the request. Names must match exactly (case-sensitive). Run pulse predict --json on the chain request to confirm the stage names the chain executor sees.",
			},
		},
	},
	PULSE_OVERLAY_KEY_SET_DIVERGENT: {
		// Minimal entry — full polish (richer Message + per-shape Fixup
		// hints) is deferred.
		Message: "A Compose overlay spec resolved a reference slot and one or more target slots whose per-coordinate key sets disagree — matrix (row × column) tuples present on one slot but absent on another, or series group-keys diverging across slots. Compose-only overlays require strict cross-Request key alignment so the renderer can fold target values against the reference at byte-equal coordinates; tolerant alignment is an explicit non-goal for v1. Details carry the reference slot label, the offending target slot label, and the symmetric difference of the two key sets (`missing` keys present on reference but absent from target; `extra` keys present on target but absent from reference).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests"},
				Hint:   "Re-issue the diverging slot Requests against pre-aligned cohorts so every slot produces the same per-coordinate key set — same Groups slot, same FILTER_* pipeline, same shape-affecting parameters. Compose-only overlays compare slots cell-for-cell at matching coordinates; divergent key sets cannot be folded byte-equal.",
			},
		},
	},
	PULSE_OVERLAY_SCHEMA_DIVERGENT: {
		// Prose points at the predict-side SlotPair surface so MCP
		// planners can fan the per-divergence repair without parsing
		// envelope Details.
		Message: "A Compose overlay spec resolved a reference slot and one or more target slots whose row / column axis schemas disagree in grouper kind, type, or nested depth. Compose-only overlays require structurally identical axis schemas across slots — field names may differ (two slots can rename the same column) but grouper kinds + types + depth must match. Runtime Details carry the `reference` slot label, the offending `target_label`, and canonical `reference_schema` / `target_schema` strings (per-axis kind tuples joined `|`, axes joined `/`); the no-execute predict surface (`descriptor.ValidateCompose`) mirrors the same information via `ComposeValidationResult.OverlaysSchemaDivergence []SlotPair` (one entry per offending (reference, target) pair, Reason = `schema-divergent`). Predict consumers should branch on `Reason` for the divergence class and read `ReferenceLabel` / `TargetLabel` for the offending slots without re-parsing envelope details.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests", "*", "Crosstab", "Rows"},
				Hint:   "Re-issue the diverging slot Request with the same row-axis Groups slot (kind + type + depth) as the reference slot. Field names may differ across slots; grouper kinds + types + nested depth must match.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests", "*", "Crosstab", "Columns"},
				Hint:   "Re-issue the diverging slot Request with the same column-axis Groups slot (kind + type + depth) as the reference slot. Field names may differ across slots; grouper kinds + types + nested depth must match.",
			},
		},
	},
	PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT: {
		// Minimal entry — full polish is deferred.
		Message: "A Compose overlay spec resolved a reference slot and one or more target slots whose host result shapes disagree (one is MATRIX while the other is SERIES, or one is SCALAR while the other is non-SCALAR). Compose overlays fold target values against the reference at byte-equal coordinates; without a shared shape there is no coordinate grid. Details carry the offending `target_label`, the `reference_shape`, and the `target_shape`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests"},
				Hint:   "Re-issue the diverging slot Request so its host result shape matches the reference slot's — either both crosstabs (set Crosstab on both) or both grouped Process results (set Groups + Aggregations on both, no Crosstab on either).",
			},
		},
	},
	PULSE_OVERLAY_DICT_PREFIX_DRIFT: {
		// Minimal entry — full polish (richer Message + per-field Fixup
		// hints) is deferred.
		Message: "A Compose overlay spec opted into ComposeOverlaySpec.Options.DictPrefixFast but the reference slot and one or more target slots carry categorical dictionaries that do not share a byte-equal common prefix. The fast path engages direct-index comparison across slots; divergent dictionaries silently produce incorrect cell alignment under that mode so the runtime fails loud. Default behaviour is the safe by-label join (decode each key via the slot dictionary before comparison) and tolerates arbitrary dict reordering. Details carry the offending `reference` slot label, the `target_label`, the `field` whose dictionaries disagree, and the canonical `reference_dict_prefix` / `target_dict_prefix` strings (entries joined `|`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Options"},
				Hint:   "Drop ComposeOverlaySpec.Options.DictPrefixFast (or set it to false) to fall back to the safe by-label join, which tolerates arbitrary dictionary reordering across slots. The fast path is an opt-in optimization for cohorts whose per-slot dictionaries are known to share a byte-equal prefix.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests"},
				Hint:   "Re-issue the diverging slot Requests against cohorts whose categorical dictionaries share a byte-equal common prefix on the overlay's keying field — typically by importing both cohorts from the same canonical source or by aligning dictionaries upstream via the sharding append-only prefix rule (see encoding.ValidateDictPrefixRule).",
			},
		},
	},
	PULSE_OVERLAY_SLOT_NOT_CROSSTAB: {
		// Minimal entry — full polish (kind-aware Fixup catalogue) is
		// deferred alongside the per-kind matrix-required catalog
		// finalisation.
		Message: "A Compose overlay spec whose Kind requires a MATRIX-shaped (crosstab) host resolved at least one slot — reference or target — that is not a crosstab result. The matrix-required Compose kinds have not yet registered their per-kind shape requirements, so this code is unreachable at runtime. Details carry the `required_shape: \"MATRIX\"`, the offending `target_label`, and the `observed_shape` (`series` / `scalar`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests", "*", "Crosstab"},
				Hint:   "Set Crosstab (Rows + Columns + Cell) on the offending slot Request so it produces a MATRIX host result. Alternatively switch the Overlay Kind to one that supports the slot's current host shape (SERIES / SCALAR) — check the manifest Overlays capability block for the supported shapes per kind.",
			},
		},
	},
	PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP: {
		Message: "A multi-reference COMPOSE-host overlay spec (today: OVERLAY_PROP_Z_PANEL) named more target slots than `OverlayOptions.MaxPanelTargets` allows. Multi-reference overlays bound combinatorial explosion — every additional target enlarges the O(N²) per-cell pairwise fold so the cap is the explicit knob. Per the interview risk paragraph \"Multi-reference combinatorics\", the default cap is 16 and bumping the default requires an interview update; per-request override lives on `ComposeOverlaySpec.Options.MaxPanelTargets`. Details carry the offending `kind`, the `observed` target count, and the active `cap`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Targets"},
				Hint:   "Reduce `Targets` to ≤ `OverlayOptions.MaxPanelTargets` (currently the active cap reported in Details, default 16) or raise the cap via Options. Multi-reference overlays bound combinatorial explosion — every additional target enlarges the O(N²) per-cell pairwise fold so dropping targets is the cheapest fix. If the panel exists to compare a reference cohort against many treatment arms, split it into batches of cap-sized sub-panels, each emitting its own OVERLAY_PROP_Z_PANEL layer.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "*", "Options", "MaxPanelTargets"},
				Hint:   "Raise `ComposeOverlaySpec.Options.MaxPanelTargets` to a value ≥ the observed target count if your environment can absorb the O(N²) per-cell pairwise z-test cost. Per the interview risk paragraph the default 16 is the explicit upper bound; raising it without an interview update is a deliberate caller-attested decision.",
			},
		},
	},
	PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED: {
		Message: "CSV export does not support overlay embedding; %d overlay layer(s) dropped. Warning-class — the host CSV body is written verbatim (byte-identical to a pre-overlay export) and the dropped overlay layers are reported via this warning so callers can audit which layers fell off. CSV is the LCD of tabular formats and consumer tools (Excel, R, pandas, awk) cannot uniformly parse any overlay convention — research/export-embedding-shape.md § 7 locks the warn-and-skip semantic. The TSV adapter shares the CSV writer surface and inherits the same warn-and-skip behaviour; the warning code stays CSV-flavoured. Details carry `layer_count`, `layer_names`, and `layer_kinds` so a renderer can surface the dropped layer slate without re-reading the source Response.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"ExportJob", "Target"},
				Hint:     "To preserve overlays in cross-format export, target an overlay-aware writer instead of CSV / TSV. Arrow (--format arrow) carries overlays as a top-level LIST<STRUCT> column family; Parquet (--format parquet) rides the same Arrow schema through the pqarrow bridge; Excel (--format excel) emits one sheet per layer named `__overlay_<layer_name>`; NDJSON (--format ndjson) appends a single trailing line `{\"_overlays\": [...]}` after the last record. All four formats carry the full layer slate without dropping any payload.",
				Examples: []any{"arrow", "parquet", "excel", "ndjson"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"ExportJob", "IncludeOverlays"},
				Hint:     "To suppress this warning while keeping CSV output, set ExportJob.IncludeOverlays=false (or --include-overlays=false on the CLI). The CSV body is byte-identical with or without the opt-out — the toggle only controls whether the warning fires. Same applies to ConvertJob.IncludeOverlays when the CSV target is the export half of a convert chain.",
				Examples: []any{false},
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Request"},
				Hint:   "To inspect overlay results without exporting, use `pulse api process` with --echo-request and read the JSON envelope's `data.overlays` slot. The envelope renders the full OverlayLayer slate inline so callers can audit the payload before deciding which export format preserves the layers they care about.",
			},
		},
	},
	PULSE_OVERLAY_YOY_INCOMPATIBLE_FREQUENCY: {
		Message: "An OVERLAY_YOY spec named a `frequency` Param outside the supported set (`annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly`). The supported set is the minimum frequency catalog needed to cover the GROUP_DATE component family — finer-than-hourly or coarser-than-annual frequencies are explicit non-goals in v1. Calendar-week / day-of-week realignment is also an explicit non-goal: weekly frequency uses calendar-week-aligned `i - 52` arithmetic and daily frequency uses exact-key lookup against the host key index (Feb 29 in a non-leap prior year emits NaN). Surfaced at both predict (descriptor.validateOverlayYoY) and runtime (processing.applyYoY) with Details carrying the offending `frequency` value plus the supported list.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params"},
				Hint:     "Replace the OverlaySpec.Params[\"frequency\"] value with one of the six supported frequencies: `annual` | `quarterly` | `monthly` | `weekly` | `daily` | `hourly`. The frequency must match the host GROUP_DATE grouper's component (annual↔year, quarterly↔quarter, monthly↔month, weekly↔week, daily↔day, hourly↔hour). Alternatively move the frequency slot onto `Groups[0].Params[\"frequency\"]` so the YoY handler reads it from the canonical GROUP_DATE config.",
				Examples: []any{map[string]any{"frequency": "annual"}, map[string]any{"frequency": "quarterly"}, map[string]any{"frequency": "monthly"}, map[string]any{"frequency": "weekly"}, map[string]any{"frequency": "daily"}, map[string]any{"frequency": "hourly"}},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Groups", "0"},
				Hint:     "If your data is at a finer granularity than the supported frequency set, switch the host GROUP_DATE grouper to a coarser component+frequency pair before the YoY overlay sees it. Worked example: hourly raw rows aggregated to a daily YoY trend ⇒ set `Groups[0]` to `{\"Type\": \"GROUP_DATE\", \"Field\": \"<date_field>\", \"Params\": {\"component\": \"day\", \"frequency\": \"daily\"}}` and drop the overlay-level frequency override. The same pattern lifts daily raw rows to monthly (`component: month, frequency: monthly`), monthly to quarterly, or quarterly to annual. GROUP_DATE will do the coarser bucketing; the YoY handler then computes prior-period diffs on the coarser key stride.",
				Examples: []any{map[string]any{"Type": "GROUP_DATE", "Field": "event_date", "Params": map[string]any{"component": "day", "frequency": "daily"}}, map[string]any{"Type": "GROUP_DATE", "Field": "event_date", "Params": map[string]any{"component": "month", "frequency": "monthly"}}, map[string]any{"Type": "GROUP_DATE", "Field": "event_date", "Params": map[string]any{"component": "quarter", "frequency": "quarterly"}}, map[string]any{"Type": "GROUP_DATE", "Field": "event_date", "Params": map[string]any{"component": "year", "frequency": "annual"}}},
			},
		},
	},

	// ---------- PULSE: point-lookup index (E1) ----------
	PULSE_INDEX_MISSING: {
		Message: "No sidecar point-lookup index was found on disk for the requested LookupRequest.Field. Point lookups probe a prebuilt sidecar index (encoding.SidecarIndexPath derives its path from the cohort file and key field) rather than scanning the cohort — the index must be built once before any Lookup call against that field can succeed.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Build the sidecar index for this field first: `pulse index build <cohort> --key <field>` (or the equivalent Service.BuildIndex(ctx, path, []string{field}) library call). Re-run the lookup once the sidecar file exists at the derived `<cohort>.<keyhash>.idx` path.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Field"},
				Hint:   "Alternatively, point the lookup at a field that already has a built sidecar index — list existing sidecars alongside the cohort file (same directory, `.idx` suffix) to see which key fields are ready.",
			},
		},
	},
	PULSE_LOOKUP_NOT_FOUND: {
		Message: "The sidecar point-lookup index was found and read successfully, but LookupRequest.Value has no matching entry in the resolved hash bucket — no record in the cohort carries that key value for LookupRequest.Field.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Value"},
				Hint:   "Verify the literal Value is spelled/typed exactly as it appears in the source data (categorical values are case-sensitive dictionary lookups; numeric values must parse to the same on-wire representation as the indexed field). If the cohort was re-imported or the key column's data changed since the index was built, rebuild the sidecar via `pulse index build` — a stale index silently returns not-found for keys added after the last build.",
			},
		},
	},
	PULSE_LOOKUP_AMBIGUOUS: {
		Message: "The requested key matched more than one row in the cohort, but LookupRequest.Multiplicity is (or defaults to) \"assert_unique\", which fails rather than silently picking one match. This is the default mode precisely so a duplicate-key surprise never returns a single, arbitrary row without the caller knowing more than one candidate exists.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Keys"},
				Hint:     "Add another key column (or a composite Keys tuple) that uniquely identifies the record you want — e.g. combine the ambiguous field with an id/timestamp column and rebuild the sidecar index for that composite key via `pulse index build <cohort> --key <field1> --key <field2>`.",
				Examples: []any{[]any{map[string]any{"field": "region", "value": "east"}, map[string]any{"field": "id", "value": "4"}}},
			},
			{
				Action:   FixupSetDefault,
				Path:     []string{"Multiplicity"},
				Hint:     "If duplicate matches are expected and acceptable, opt into a non-failing mode explicitly: set Multiplicity to \"first\" to take the lowest row-id deterministically, or \"all\" to get every matching row back in LookupResult.Rows.",
				Examples: []any{"first", "all"},
			},
		},
	},
	PULSE_INDEX_STALE: {
		Message: "The sidecar point-lookup index's embedded content-hash fingerprint does not match the current .pulse cohort file — the cohort was mutated (re-imported, re-exported, or otherwise rewritten) after the sidecar was last built. Lookup refuses to serve rows against a stale index rather than risk returning silently wrong results; there is no automatic scan-fallback or auto-rebuild.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Rebuild the sidecar index against the current cohort content: `pulse index build <cohort> --key <field>` (or the equivalent Service.BuildIndex(ctx, path, keyFields) library call, or a quick freshness check first via `pulse index verify <cohort> --key <field>` / Service.VerifyIndex). Re-run the lookup once the rebuild completes.",
			},
		},
	},
	PULSE_INDEX_UNSUPPORTED_SHARDED: {
		Message: "Point-lookup index build, lookup, and verify only support single-file .pulse cohorts in v1 — the target resolved to a shard archive (leading bytes matched the zip magic \"PK\\x03\\x04\" rather than the single-file \"PULSE\" magic). Row-id addressing (the sidecar's bucket entries point directly at a record's byte offset within one contiguous file) only has meaning for a single contiguous record region, which a multi-shard archive does not present. Sharded point-lookup support is a future/follow-up, not planned for this v1.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Point-lookup against a sharded cohort is not supported in v1 — there is no per-archive workaround that indexes across shards. If you only need to look up within a SINGLE shard, extract it as a standalone single-file cohort via the anchor syntax (`archive.pulse#shard.pulse`) and build/query the index against that anchor path instead — the anchor opens the named shard as if it were its own single-file `.pulse` cohort, so `pulse index build \"archive.pulse#shard.pulse\" --key <field>` and subsequent lookups work exactly as they do against any single-file cohort. This does not give you a cross-shard index; it only lets you index one shard at a time.",
			},
		},
	},
	PULSE_TEMPLATE_NOT_FOUND: {
		Message: "No template is registered under the requested name. Names come from templates declared programmatically or loaded from a template directory (filename minus .json); lookup is exact and case-sensitive.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Template"},
				Hint:   "List the registered template names and re-issue the render with one of them; if the template is meant to come from disk, confirm the file sits in the configured template directory with a .json extension and that the directory is actually being loaded.",
			},
		},
	},
	PULSE_TEMPLATE_INVALID: {
		Message: "A template document failed declaration validation: malformed JSON, a missing or empty name, a body that is not a JSON object, a duplicate variable name, or an uninterpretable variable declaration. The document is rejected at registration so it can never be rendered.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Fix the offending template document named in the error details: give it a non-empty unique name, a JSON-object body, and one declaration per variable name. Re-register (or reload the template directory) once the document parses.",
			},
		},
	},
	PULSE_TEMPLATE_TARGET_UNKNOWN: {
		Message: "A template's target is absent or does not name one of the five public request roots, or a target-specific facade method was handed a template declaring a different target. Targets are spelled lowercase on the wire — request, composed, chain, facet, sample — and the target selects the type the rendered JSON decodes into, so it cannot be inferred from the body.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Target"},
				Hint:     "Set the template's target to exactly one of the lowercase wire spellings — request (types.Request), composed (types.ComposedRequest), chain (types.ChainRequest), facet (types.FacetRequest), sample (types.SampleRequest) — picking the one whose shape the template body already writes. The Go type name is not the wire value: \"ComposedRequest\" is rejected, \"composed\" is accepted. When the details carry expected_target the target is valid but the calling method cannot return it — RenderTemplateRequest handles only \"request\", so call RenderTemplate(name, vars) instead and read the Rendered pointer the declared target names.",
				Examples: []any{"request", "composed", "chain", "facet", "sample"},
			},
		},
	},
	PULSE_TEMPLATE_VAR_MISSING: {
		Message: "A variable declared required resolved to no value at render time: the caller supplied nothing for it and its declaration carries no default.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Variables", "*"},
				Hint:   "Supply a value for the variable named in the error details under the render call's variables map, or give that variable a default in the template declaration so it resolves without a caller-supplied value.",
			},
			{
				Action: FixupRemoveParam,
				Hint:   "If the variable is genuinely optional, drop required from its declaration — an optional variable with no value simply leaves its guarded slot out of the rendered request.",
			},
		},
	},
	PULSE_TEMPLATE_VAR_UNKNOWN: {
		Message: "The caller supplied a variable the template does not declare. Unknown variables are rejected rather than ignored, because silently dropping one yields a request that looks parameterised but is not.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Variables", "*"},
				Hint:   "Remove the unexpected key from the render call's variables map (a typo against a declared name is the usual cause — compare it against the template's declared variable list), or add a declaration for it to the template if the template really should accept it.",
			},
		},
	},
	PULSE_TEMPLATE_VAR_TYPE: {
		Message: "A supplied value — or a declaration's own default — does not match the variable's declared type.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Variables", "*"},
				Hint:   "Send the value in the declared type rather than a coercible near-miss (a number as a JSON number, not a quoted string; a boolean as true/false, not \"true\"), or widen the variable's declared type in the template to the type you actually pass.",
			},
		},
	},
	PULSE_TEMPLATE_VAR_ENUM: {
		Message: "A supplied value is not a member of an enum variable's declared values set. Membership is exact and case-sensitive.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Variables", "*"},
				Hint:   "Replace the value with one of the permitted members listed in the error details, matching case exactly, or add the new member to the variable's values set in the template declaration.",
			},
		},
	},
	PULSE_TEMPLATE_UNRESOLVED: {
		Message: "An unguarded variable marker survived substitution — a marker in the template body had no value to substitute. Rendering fails rather than emitting a request that carries a literal marker string.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Variables", "*"},
				Hint:   "Declare the marker named in the error details as a template variable and supply a value (or a default) for it; if the marker is meant to be a literal, escape it in the template body so substitution leaves it alone.",
			},
		},
	},
	PULSE_TEMPLATE_RENDER_INVALID: {
		Message: "Substitution succeeded but the rendered JSON failed strict decode into the template's target request type — typically an unknown field in the body, or a substituted value landing in a slot whose type it does not fit.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Correct the template body against the target request type's shape: drop or rename the unknown field the decode fault names, and make sure each variable marker sits in a slot whose type matches the variable's declared type. Render with the same variables again to confirm.",
			},
		},
	},

	// ---------- PULSE (SPSS) ----------
	PULSE_SPSS_DICT_INVALID: {
		Message: "The SPSS .sav dictionary is structurally malformed: the file does not open with the $FL2/$FL3 magic, the layout code identifies neither byte order, a record carries an unknown type tag or an out-of-range field, or a record appears where the format does not allow it. The error details name the record and the byte offset at which the fault was detected.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Confirm the file really is an SPSS system file and not a .por, .zsav renamed, or an unrelated file with a .sav extension; open it in SPSS or PSPP and re-save it as a system file, then import the re-saved copy.",
			},
		},
	},
	PULSE_SPSS_DICT_TRUNCATED: {
		Message: "The SPSS .sav file ends part way through its dictionary: a record claimed more bytes than the file contains, or the stream ran out before the record type 999 terminator. The bytes present parse cleanly as far as they go, which is what separates this from a malformed dictionary.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-transfer or re-export the file — a truncated .sav is almost always an interrupted copy, download, or export. Compare the byte size against the source before importing again.",
			},
		},
	},
	PULSE_SPSS_EXTENSION_UNKNOWN: {
		Message: "The SPSS .sav dictionary carries a record type 7 extension subtype this reader does not interpret. This is a warning, not a failure: the record's framing was read so the dictionary walk stayed aligned, its bytes were retained verbatim, and the rest of the file parsed normally. Real SPSS versions emit subtypes the published format description does not list, so an unrecognised subtype is expected rather than exceptional.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "No action is needed unless the subtype named in the details carries data you require. If it does, report the subtype so the reader can learn it; meanwhile re-saving the file from a different SPSS or PSPP version usually drops writer-specific extension records.",
			},
		},
	},
	PULSE_SPSS_EXTENSION_INVALID: {
		Message: "A record type 7 extension subtype this reader does interpret carried a payload that does not match the shape the format defines for it — an element size or count the subtype does not allow, a payload that ran out early, or a field naming a variable the dictionary does not contain. This is a warning: the record's framing was sound so the dictionary walk stayed aligned and the raw bytes were retained; only the interpretation of that one record was dropped.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Check what the skipped subtype named in the details would have supplied — long variable names, measurement levels, the character encoding — and whether the import still carries it. Re-saving the file from SPSS or PSPP rewrites the extension records and usually clears a malformed one.",
			},
		},
	},
	PULSE_SPSS_COMPRESSION_UNSUPPORTED: {
		Message: "The SPSS .sav dictionary parsed cleanly but its data section uses a compressed encoding this reader cannot yet decode — bytecode compression (the SPSS default) or ZSAV zlib block compression. Only the uncompressed encoding is read today. Reading a compressed data section as though it were uncompressed would produce plausible-looking garbage, so the import stops instead.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-save the file without compression: in SPSS, File > Save As with the compression option cleared, or in PSPP `SAVE OUTFILE='plain.sav' /UNCOMPRESSED.` Import the uncompressed copy.",
			},
		},
	},
	PULSE_SPSS_DATA_TRUNCATED: {
		Message: "The SPSS .sav data section ends part way through a case: the bytes following the dictionary terminator are not a whole multiple of the case width the dictionary declares. The dictionary parsed cleanly, so the file is a valid system file that was cut short rather than a malformed one.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-transfer or re-export the file — a data section ending mid-case is almost always an interrupted copy, download, or export. Compare the byte size against the source before importing again.",
			},
		},
	},
	PULSE_SPSS_DATA_CASE_COUNT_MISMATCH: {
		Message: "The number of cases the SPSS .sav data section actually holds disagrees with the count the file declares in its header (or in the record 7/16 64-bit case count). This is a warning: every complete case present is read, because discarding rows the file plainly contains to honour a writer's miscount would lose data.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Compare the declared and actual counts in the error details against what SPSS or PSPP reports for the same file. A writer that miscounts is usually harmless, but a shortfall can also mean a truncated transfer — re-export the file and check the counts agree.",
			},
		},
	},
	PULSE_SPSS_CATEGORICAL_OVERFLOW: {
		Message: "An SPSS variable maps to a Pulse categorical field, but its distinct values would need more dictionary entries than categorical_u32 can hold. This is a hard error rather than a warning because the only alternative is discarding values, and a cohort silently missing rows of a variable is worse than a refused import.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "Supply an explicit ImportJob.Schema that maps the variable named in the details to a different type, or drop the variable from the source file before importing. A variable with more than four billion distinct values is almost always free text that belongs outside the analytic cohort.",
			},
		},
	},
	PULSE_SPSS_CARDINALITY_HIGH: {
		Message: "An SPSS variable mapped to a Pulse categorical field whose distinct-value count is a large fraction of the case count — the signature of a free-text 'other, please specify' variable. This is a warning and the import proceeds: the mapping is lossless, and the cost is a large inline dictionary block that every read of the cohort pays for.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "If the variable named in the details is free text rather than a coded category, consider excluding it from the import, or raise the reader's cardinality warning fraction if a near-unique categorical is expected for this source.",
			},
		},
	},
	PULSE_SPSS_TEMPORAL_PRECISION: {
		Message: "An SPSS variable carrying a date or time print format holds at least one value the matching Pulse temporal type cannot represent exactly — a fractional second, a non-finite double, or a second count outside the int64 range — so the variable was mapped to f64 raw SPSS seconds instead. This is a warning: the raw seconds are lossless and the print format is retained for export, so only the ergonomics of a typed temporal column are lost.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "No action is needed if second-resolution timestamps are not required. To force a typed temporal column, round the variable to whole seconds in SPSS or PSPP before exporting, or supply an explicit ImportJob.Schema naming the type you want.",
			},
		},
	},
	PULSE_SPSS_DATE_WIDENED: {
		Message: "An SPSS variable carrying a day-resolution print format (DATE, ADATE, EDATE, SDATE or JDATE) was mapped to the Pulse datetime type rather than date, because at least one of its values carries a time of day that day resolution would truncate, or falls before 1970-01-01, which the unsigned epoch-day date representation cannot express. This is a warning: datetime holds every such value exactly and the date-family groupers accept it by documented day truncation.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "No action is needed — GROUP_DATE and the date-range operators accept a datetime field and truncate it to the day. Supply an explicit ImportJob.Schema only if the column must be a date, accepting that pre-1970 values and times of day will not survive.",
			},
		},
	},
	PULSE_SPSS_VALUE_COLLISION: {
		Message: "Two distinct SPSS values of one variable resolve to the same Pulse categorical dictionary entry, so the original-code-to-dictionary-ID mapping is no longer one-to-one and an export back to .sav cannot tell which code to re-emit. This is a warning: the file is otherwise readable and the recorded code-to-label-to-ID triple carries both codes against the shared ID, so the collision is visible rather than silent.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Check the variable named in the details in SPSS or PSPP: two codes sharing one value label, or a label whose text equals another code's own value, is usually a data-entry mistake in the source dictionary. Give each code a distinct label and re-export.",
			},
		},
	},
	PULSE_SPSS_MEASURE_LEVEL_MISMATCH: {
		Message: "An SPSS variable whose record 7/11 measurement level is 'scale' carries value labels, so it mapped to a Pulse categorical field. Its smart defaults are therefore AGG_FREQUENCY / GROUP_CATEGORY rather than the AGG_SUM / GROUP_RANGE the declared level implies. This is a warning: every code and label is preserved, but the analytic defaults will not be the ones the source file declared.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "This is the usual shape of a continuous variable that labels only its missing codes. Name the aggregator and grouper explicitly in the request instead of relying on smart defaults, or supply an explicit ImportJob.Schema typing the variable f64 if the labels are not needed.",
			},
		},
	},
	PULSE_SPSS_NULL_TOKEN_COLLISION: {
		Message: "An SPSS string value or value-label key is one of the import pipeline's null sentinel tokens (the empty string, NA, N/A or NULL, in any case), so cells carrying it import as null and its dictionary entry is unreachable. An all-blank string is SPSS's own de facto missing-string convention and reading it as null is intended; a literal NA stored as data is a real value the shared import path collapses, and that collapse is reported here rather than left silent.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "If the value named in the details is meant as data rather than as missing, recode it in SPSS or PSPP to a token the import path does not treat as null before exporting.",
			},
		},
	},
}
