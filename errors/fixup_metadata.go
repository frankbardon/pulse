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
	PULSE_EXPORT_ROW_ERROR: {
		Message: "A row could not be exported due to a per-cell value-to-string conversion failure.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data to regenerate the .pulse file; the dictionary or encoding state is inconsistent.",
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
	PULSE_GEO_INVALID_POINT: {
		Message: "A point parse failed or |lat|>90 / |lon|>180.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import with the latitude and longitude columns mapped in the correct order; WKT writes POINT(lon lat) but Pulse stores (lat, lon).",
			},
		},
	},
	PULSE_GEO_INVALID_POLYGON: {
		Message: "A WKT POLYGON parse failed or the outer ring is not closed.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Filterers", "*", "Polygon"},
				Hint:   "Supply a single closed outer ring with first vertex equal to last; v1 rejects MULTIPOLYGON and inner-ring holes.",
			},
		},
	},
	PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS: {
		Message: "An AGG_GEO_BBOX input crosses the 180/-180 meridian, where a flat min/max bbox is ambiguous.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Aggregations", "*", "Type"},
				Hint:   "Use AGG_GEO_CENTROID (the 3D unit-sphere algorithm handles antimeridian crossings) or split the cohort by hemisphere with FILTER_RANGE on longitude.",
			},
		},
	},
	PULSE_GEO_INVALID_RESOLUTION: {
		Message: "An H3 resolution is out of [0, 15] or finer than a cell's native resolution when walking parents.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Groups", "*", "Params", "resolution"},
				Hint:   "Pick a resolution in [0, 15]; for h3_cell input, pick at most the cell's native resolution (parent walks only go coarser).",
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
	PULSE_AGG_NOT_MEANINGFUL_FOR_GEO: {
		Message: "A numeric aggregator was requested on a geospatial field (point_f64 / h3_cell).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Aggregations", "*", "Type"},
				Hint:     "Geo fields support AGG_GEO_CENTROID, AGG_GEO_BBOX, AGG_COUNT, AGG_DISTINCT_COUNT; pick one of those, or group by the cell and aggregate a numeric field.",
				Examples: []any{"AGG_GEO_CENTROID", "AGG_GEO_BBOX", "AGG_COUNT"},
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
		Message: "The profile layer cannot summarize this field type (e.g., point_f64 / h3_cell); the field is skipped.",
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
		Message: "A test requires a numeric field but the named field resolves to a categorical, geo, or otherwise non-numeric schema type.",
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
	PULSE_QUERY_UNRESOLVED: {
		Message: "The natural-language query parser could not map a token to any operator, schema field, or bucket within the edit-distance budget.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Re-phrase using one of the canonical shapes in skills/request-recipes.md (\"<agg> <field>\", \"<agg> <field> by <field>\", \"top N <field> by count\"); confirm every field reference appears in pulse inspect --json output.",
			},
		},
	},
	PULSE_QUERY_AMBIGUOUS: {
		Message: "A query token matched multiple schema fields at the same edit distance; the parser picked the lexically first match.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Re-phrase using the full schema field name to remove the ambiguity, or edit the resolved request to point at the intended field.",
			},
		},
	},
}
