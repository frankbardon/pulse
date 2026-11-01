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
		Message: "The .pulse file is malformed: its header or schema block (bad magic, unsupported format version, or invalid field-type byte), or a record whose categorical index or set bit lies outside its field's dictionary.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data to regenerate the .pulse file; the existing file is corrupt or was written by an incompatible binary.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "If details.version is outside details.supported_versions, a newer Pulse wrote the file: upgrade this binary to one that reads that format version, or re-import the source with this binary (old format versions stay readable forever).",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "If details.reason is categorical_index_out_of_range or set_member_out_of_range, a record of details.field stores a code its dictionary (details.dictionary_size entries) does not hold: the record data is corrupt. Restore the cohort or shard from its source or re-import it; for a shard archive, `pulse shard verify` checks every stored shard's CRC.",
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
				Hint:   "Widen the field type (u8 -> u16, f32 -> f64) or pre-clean the source data to fit the declared type, then re-import. For a set_* column an in-place widen is available instead: pulse widen COHORT --field F --to set_u128|set_u256.",
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
		Message: "Group construction failed (e.g., quantile with zero-variance input, malformed bucket key, a key containing the NUL composite-key separator under two or more groupers).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Groups", "*", "Type"},
				Hint:   "Swap GROUP_QUANTILE for GROUP_RANGE on near-constant fields, or GROUP_CATEGORY for a high-cardinality numeric to GROUP_ROUNDED.",
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Groups", "*", "Type"},
				Hint:   "A grouper key containing a NUL byte (\\x00) cannot be combined with another grouper (details.reason key_contains_separator): fix the extension grouper to emit NUL-free keys, or run it as the only grouper.",
			},
		},
	},
	PROCESSING_INTERNAL: {
		Message:            "Internal invariant violated inside the processing layer; report with reproducer.",
		FixupNotApplicable: true,
	},
	PROCESSING_PANIC: {
		Message: "An operation panicked (an embedder operator or expression function, or an engine bug) and was recovered into this error; the instance stays usable.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Hint:   "Inspect details.stack: when its top frames are embedder code, fix that operator or expression function to return a coded error instead of panicking, or drop it from the request.",
			},
			{
				Action: FixupReplaceOperator,
				Hint:   "When details.stack shows only Pulse engine frames, report it as a Pulse bug with the request and the stack (details.panic_type names the panic's Go type; the value is never kept).",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Options", "DisablePanicRecovery"},
				Hint:   "To crash with the original panic instead of receiving this error (e.g. under a debugger or a test), set pulse.Options.DisablePanicRecovery to true.",
			},
		},
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
		Message: "An iterative regression fit (IRLS for a generalized linear model, coordinate descent for a regularized least-squares fit) failed to converge within MaxIters.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Regressions", "*", "MaxIters"},
				Hint:   "Raise MaxIters, loosen Tol, rescale the predictors, or drop a near-collinear column; persistent non-convergence usually points to a separable / degenerate design.",
			},
		},
	},
	PROCESSING_REGRESSION_SEPARATION: {
		Message: "A binomial REG_GLM fit is separated: a predictor splits the outcome perfectly, or almost perfectly, so fitted probabilities reach numerically 0 or 1 while its coefficient keeps growing; the maximum-likelihood estimate does not exist and no coefficients are reported. details.predictors names the separating predictors.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Regressions", "*", "Penalty"},
				Hint:     "Add penalty: l2 (a ridge penalty keeps every coefficient finite), or drop the named predictor, merge its rare level, or check that it is not recorded after the outcome.",
				Examples: []any{"l2"},
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
	PROCESSING_REGRESSION_DIAGNOSTIC: {
		Message: "A regression fit's own diagnostic crossed its threshold while the fit still reported figures — for a sampler, chains that did not mix (R-hat above, or effective sample size below, its bound), post-warmup divergences or tree-depth saturation. details.reason names the diagnostic and details carries its figures; the figures may not be trustworthy. A warning on the result and the response; under Strict it fails the slot with this code.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Regressions", "*", "Params"},
				Hint:   "Raise the model's own warmup / draws / iteration params, or tighten its priors or rescale the inputs, until the diagnostic named in details.reason clears; the regression's skill lists its thresholds and params.",
			},
		},
	},
	PROCESSING_REGRESSION_VCOV_UNSUPPORTED: {
		Message: "The regression cannot report the coefficient covariance it was asked for: vcov: true on lasso / elastic net (with or without a Resample / Selection modifier), or a robust covariance (hc0–hc3, cluster, hac) beside a penalty, a Resample, a Selection or on a type without it, or on rows that span fewer than two clusters or repeat an order_by value. details.reason names which.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Regressions", "*", "Vcov"},
				Hint:   "reason penalty with no covariance block: drop vcov from this regression, or fit it unpenalized or with penalty \"l2\" (ridge); resample and selection keep vcov. details.penalty names the blocking penalty.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Regressions", "*", "Covariance"},
				Hint:   "A robust covariance (details.covariance_type): drop the covariance block, or the penalty / resample / selection / bounds it names (reason penalty, resample, selection, bounds); reason type: fit REG_OLS, REG_GLM or REG_BAYES_LINEAR; reason clusters: cluster on a field with at least two levels in the filtered rows; reason order_by_ties: order_by a field unique per row (one series).",
			},
		},
	},
	PROCESSING_VECTOR_LENGTH_MISMATCH: {
		Message: "A pairwise vector expression function (dot, dist or cosine) received two vectors of different lengths; the run fails instead of returning NaN. When both lengths are fixed by the request — literal arrays, or bare named vectors (a Request.Vectors member count, a vec_* field's dim) — predict reports it and the run refuses it at build, before any row; a computed argument (a nested call, a slice) is checked per row. details.function names the call, details.left and details.right the two lengths.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Attributes", "*", "Expression"},
				Hint:   "Make both vector arguments list the same number of elements in the same order, e.g. dot([a, b, c], [1, 0, 0]); pad a missing position with nil (read as NaN and skipped pairwise) rather than dropping it.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Filterers", "*", "Expression"},
				Hint:   "The same rule applies in a FILTER_EXPRESSION or a total() argument: compare vectors of equal length only.",
			},
		},
	},
	PROCESSING_AUC_OUTCOME_NOT_BINARY: {
		Message: "An AGG_AUC slot met an outcome value other than 0 or 1 (NaN included) on a row whose score and outcome both answered: the area under the ROC curve needs a binary outcome, and Pulse never guesses a threshold. The run fails. Predict refuses only an outcome column whose type cannot hold 0 / 1; a value is seen at run time. details.aggregation names the operator, details.outcome the column, details.value the first offending value.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Aggregations", "*", "Params", "outcome"},
				Hint:   "Point params.outcome at a 0/1 column or a packed_bool, or derive one first: an ATTR_FORMULA such as `status == 2 ? 1 : 0` (or an ATTR_CODE_IN over a categorical) and score against its label.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Filterers"},
				Hint:   "If only a few rows carry another code, drop them with a filter (FILTER_RANGE or FILTER_EXPRESSION on the outcome) before the slot reads them.",
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
		Message: "A row could not be imported due to a per-cell encoding or parse failure. Raised per row on ImportReport.RowErrors while SOME rows still import — and raised as the FATAL return of ImportJob.Run when a non-empty source yields zero importable rows, in which case details carry `rows_read`, `rows_failed`, `first_row` and `first_error`, and no .pulse file is written. A source with no data rows at all is an empty cohort, not this error.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Inspect the reported row index in details; pick a wider or nullable field type, or pre-clean the source value before re-importing. When every row failed, `first_error` names the one cell conversion to fix first — it is usually a single column whose declared type the whole source disagrees with.",
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
	PULSE_IMPORT_ROWS_DROPPED: {
		Message: "Warning-class — a partial import: some rows imported and some were skipped as PULSE_IMPORT_ROW_ERROR (a null in a non-nullable field, a value the field type cannot hold). Raised once per import on every entry point (import <format>, import auto, convert --keep-pulse and convert's target rows, the library Import / ImportFile / Convert, the managed-import pool over MCP too), beside the per-row RowErrors. Details carry `dropped` (rows skipped), `imported` (rows written) and `first_failure` {`row` (1-based source data row), `column` (empty when the failure names none), `reason`}; on convert also `stage` (`target` or `keep_pulse`). Under strict — CLI --strict, ImportJob / ConvertJob.StrictRows, imports.Spec.StrictRows, pulse.Options.Strict — it is the FATAL error instead and nothing is written. Total failure (no row imported) stays the fatal PULSE_IMPORT_ROW_ERROR.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Read details.first_failure: fix that column — declare it nullable in the schema, pick a type that holds the value, or clean the source — and re-import. To accept the partial import deliberately, keep the warning; to refuse it, pass --strict (or set StrictRows / Options.Strict).",
			},
		},
	},
	PULSE_IMPORT_WIDTH_PROMOTED: {
		Message: "Warning-class — an inferred import met a value, past the bounded inference sample, that the width inferred from the sample cannot hold, so the field was promoted to the narrowest type that holds it and the row imported instead of becoming a PULSE_IMPORT_ROW_ERROR. Ladders: categorical_u8 → categorical_u16 → categorical_u32 when the dictionary outgrows its rung; u4 → u8 → u16 → u32 → u64 for a larger non-negative integer; u4..u32 → f64 for any other number; f32 → f64 for a value outside f32's range when f32 was inferred under the --float f32 opt-in (the default infers f64) (past MaxFloat32, or a non-zero magnitude f32 flushes to zero — a value f32 merely rounds never promotes). Every step is lossless — every value already imported is exact in f64. A non-boolean in a packed_bool stays a row error. ConvertJob.Run (`pulse convert`) promotes an inferred schema the same way, on ConvertReport.WidthWarnings. Details carry `field`, `from` (the inferred type), `to` (the written type) and `source_row` (the first 1-based source row that forced it). No value changes. Never fires for an explicit --schema or an authoritative source schema: their overflow stays a row error (on convert, a full categorical rung is the fatal PULSE_IMPORT_CATEGORICAL_OVERFLOW). Never fires for a column_type_overrides column either: its overflow refuses the import with PULSE_IMPORT_OVERRIDE_INVALID.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "No action is required — the cohort holds every row at the promoted width. To make the width explicit, raise --sample-rows so inference sees the wide values, or pin the type with an explicit --schema (a value past it is then a row error) or column_type_overrides (a value past it then refuses the whole import with PULSE_IMPORT_OVERRIDE_INVALID).",
			},
		},
	},
	PULSE_IMPORT_OVERRIDE_INVALID: {
		Message: "A column_type_overrides (ImportJob.ColumnTypeOverrides / ImportSpec.ColumnTypeOverrides) entry cannot be honoured, so the import is refused and no cohort is written. Three causes, told apart by details: the override names a column the source header does not carry (`column`, `type`, `columns` — names match the header exactly, case and whitespace included); a present value in an overridden column cannot be represented in the forced type, such as 300 into u8, a non-integer into u16 or a non-date into date (`column`, `type`, `value`, `row` — the 1-based source data row — and `reason`), checked on every row, inside the inference sample and past it; or the job has no inferred schema to override because an explicit Schema was supplied or the source carries an authoritative schema (SPSS) (`reason`). An override is applied exactly or refused — it is never narrowed, widened or ignored, and an out-of-range value never becomes a skipped row.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"ColumnTypeOverrides", "*"},
				Hint:   "Correct the override's column name to the exact header spelling, or drop the entry. When a value does not fit, choose a type that holds every value (u16 for 300, f64 for a fraction, categorical_* for free text) or remove the override and let inference pick the width.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "For an explicit-schema import or an SPSS source, drop ColumnTypeOverrides and declare the column's type in the explicit Schema instead — an authoritative source dictionary is not re-typed by an override.",
			},
		},
	},
	PULSE_IMPORT_SCHEMA_FIELD_MISSING: {
		Message: "An explicit import schema (--schema / ImportJob.Schema / ConvertJob.Schema) names a field the source has no column for, so the import is refused before any row is read and nothing is written. An explicit schema is matched to the source's columns BY NAME — exact spelling, case and whitespace included — never by position; the order of the schema's fields is the cohort's field order and need not match the source. For NDJSON and JSON-array sources the columns are the keys of the FIRST object. Details carry `field` (the schema field) and `columns` (the source header).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "Fields", "*", "Name"},
				Hint:   "Rename the schema field to the exact column spelling listed in details.columns, or remove the field from the schema. `pulse import schema-template INPUT` emits a schema whose names are the source header verbatim.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "For an NDJSON / JSON-array source whose first object omits an optional key, write that key (as JSON null) on the first object, so the column set the first object declares covers every schema field.",
			},
		},
	},
	PULSE_IMPORT_SCHEMA_COLUMN_UNMAPPED: {
		Message: "The source carries a column that no field of the explicit import schema names, so the import is refused before any row is read rather than silently dropping that column's data. Columns are matched to schema fields by exact name. Details carry `column` (the unmapped source column) and `fields` (the schema's field names). The opt-out --ignore-extra (ImportJob.IgnoreExtra / ConvertJob.IgnoreExtra) drops every unmapped column instead.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "Fields"},
				Hint:   "Add a field for the column to the schema (its name exactly as in details.column), or correct a schema field whose name was meant to match it.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"IgnoreExtra"},
				Hint:   "Pass --ignore-extra (IgnoreExtra: true) to import only the schema's fields and drop every other source column on purpose.",
			},
		},
	},
	PULSE_IMPORT_SCHEMA_DUPLICATE: {
		Message: "By-name matching of an explicit import schema found a name it cannot resolve to exactly one column, so the import is refused before any row is read. Either the schema declares the same field name twice (details: `field`, `side` = \"schema\"), or the source header repeats a column name (details: `column`, `side` = \"header\"). A repeated header name is refused when the schema names it, or when extra columns are not being ignored.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "Fields", "*", "Name"},
				Hint:   "Remove or rename the repeated schema field so every field name is unique.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "Rename the repeated source column so every header name is unique, then name the one you want in the schema. A repeated column the schema does not name is tolerated under --ignore-extra.",
			},
		},
	},
	PULSE_IMPORT_SCHEMA_KEY_LATE: {
		Message: "Under an explicit import schema, an NDJSON or JSON-array source carries a key in a later object that the first object did not, so the import is refused at that row. The first object's keys are the source's column set; a key first seen later has no column, and importing past it would silently drop its values. Details carry `column` (the key), `row` (the 1-based data row it first appears on) and `columns` (the first object's keys). The opt-out --ignore-extra (IgnoreExtra) drops such keys instead.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Write every key on every object (a missing value as JSON null) so the first object declares the full column set, then name the key in the schema.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"IgnoreExtra"},
				Hint:   "Pass --ignore-extra (IgnoreExtra: true) when the late key is not wanted in the cohort.",
			},
		},
	},
	PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN: {
		Message: "An explicit import schema file declares a field whose `type` is not a registered field type, so the schema is refused before any row is read. The name is matched exactly (lower case, as `pulse import schema-template` emits it) and never coerced — an unknown name no longer falls back to f64. Details carry `field`, `type` (the name as written) and `valid` (every registered type name). Raised by every schema-file loader: `import <format> --schema`, `import predict --schema` and `convert --schema`.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Correct the field's `type` to one of the names in details.valid (e.g. f64, not float or double), or regenerate the file with `pulse import schema-template INPUT` and edit from there.",
			},
		},
	},
	PULSE_IMPORT_SCHEMA_INVALID: {
		Message: "An explicit import schema (--schema file, ImportSpec.Schema, an MCP import `schema` argument) cannot be used as written, so the import is refused before any row is read and nothing is written. The document must be a JSON array of field objects {name, type, nullable, description, precision, scale} listing at least one field; over MCP an entry key outside that shape is refused rather than silently dropped (a misspelled `nullable` would otherwise import the field non-nullable). A schema is also refused on a .pulse source, which is used as-is and never re-encoded. Details carry `reason` (malformed, unknown_key, empty, passthrough) plus `error`, `key` or `index` where they apply.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "*"},
				Hint:   "Write the schema as a JSON array of {\"name\", \"type\", \"nullable\", \"description\"} objects (precision / scale for decimal128 only), spelling each key exactly; `pulse import schema-template INPUT` emits a valid document to edit.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Schema"},
				Hint:   "For a .pulse source drop the schema: the cohort already carries its own. Omit the schema entirely to let inference type the columns.",
			},
		},
	},
	PULSE_IMPORT_FLOAT_WIDTH_INVALID: {
		Message: "The import float-width knob names neither f64 nor f32, so the import is refused before the source is read. It chooses the width inference gives an all-float column: f64 (the default; the empty string means the same) keeps every decimal exact to ~15-17 significant digits, f32 halves the bytes but rounds to ~7. Details carry `value` (as given) and `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"FloatWidth"},
				Hint:   "Pass --float f64 (FloatWidth \"f64\", or omit it) for exact decimals, or --float f32 to opt into the narrower width.",
			},
		},
	},
	PULSE_IMPORT_DST_AMBIGUOUS: {
		Message: "A naive datetime literal (no `Z`, no ±HH:MM offset) in a column read in an import source zone names a wall-clock time that zone shows twice — the repeated hour of a DST fall-back — and the DST policy is the default \"error\", so the import is refused at the first such row and nothing is written. Details carry `row` (the 1-based source data row), `column`, `value` and `zone`. A literal that carries its own `Z` or offset is never ambiguous: it names its instant and the source zone is not consulted.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"DSTPolicy"},
				Hint:   "Choose how the repeated hour resolves: --dst-policy earlier (ImportJob.DSTPolicy \"earlier\") takes the first occurrence, at the pre-transition offset; later takes the second. Every resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "Or fix the source: write the value with its offset (2026-10-25T02:30:00+02:00) or in UTC with a `Z` suffix, which no source zone reinterprets.",
			},
		},
	},
	PULSE_IMPORT_DST_NONEXISTENT: {
		Message: "A naive datetime literal (no `Z`, no ±HH:MM offset) in a column read in an import source zone names a wall-clock time that zone never shows — the skipped hour of a DST spring-forward — and the DST policy is the default \"error\", so the import is refused at the first such row and nothing is written. Details carry `row` (the 1-based source data row), `column`, `value` and `zone`. A fixed ±HH:MM source zone has no transitions and never raises this.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"DSTPolicy"},
				Hint:   "Choose how the skipped time resolves: --dst-policy earlier (ImportJob.DSTPolicy \"earlier\") reads it at the pre-transition offset (Europe/Berlin 2026-03-29 02:30 → 01:30Z); later at the post-transition offset (→ 00:30Z). Every resolved value is counted in a PULSE_IMPORT_DST_RESOLVED warning.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "Or fix the source: a wall clock the zone skips usually means the column was not recorded in that zone — check --source-tz, or write the values with their offset or a `Z` suffix.",
			},
		},
	},
	PULSE_IMPORT_DST_RESOLVED: {
		Message: "Warning-class — an import with a source zone and a non-default DST policy (\"earlier\" or \"later\") resolved at least one naive wall-clock value that the zone showed twice (a fall-back overlap) or never (a spring-forward gap). The import succeeded; this warning exists so the shift is never silent. Details carry `policy`, `ambiguous_n` and `nonexistent_n` — counts of resolved values across the whole source pass. Under the default policy \"error\" such a value refuses the import instead (PULSE_IMPORT_DST_AMBIGUOUS / PULSE_IMPORT_DST_NONEXISTENT).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "No action is required when the policy is the intended reading. To see exactly which rows were resolved, re-run with the default --dst-policy error, which refuses at the first one naming its row.",
			},
		},
	},
	PULSE_IMPORT_TIMESTAMP_TRUNCATED: {
		Message: "Warning-class — an Arrow or Parquet source carried native timestamp values with a non-zero sub-second fraction (millisecond, microsecond or nanosecond units). A `datetime` field holds whole epoch seconds, so each such value was floored to the earlier second — toward the past, also before 1970 — and the import succeeded. Raised once per source pass, never per row; details carry `truncated_n` (values floored) and `columns` (sorted names of the affected columns). Values that were already whole seconds raise nothing.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "No action is required when second resolution is enough. To keep the fraction, store it in its own numeric column at the source (e.g. milliseconds within the second) or round the timestamps there before importing.",
			},
		},
	},
	PULSE_EXPORT_ROW_ERROR: {
		Message: "A row could not be exported due to a per-cell value-to-string conversion failure, or because the target format's writer refused it. Raised per row on ExportReport.RowErrors while SOME rows still export — and raised as the FATAL return of ExportJob.Run when a non-empty cohort yields zero exported rows, in which case details carry `rows_read`, `rows_failed`, `first_row` and `first_error`. An empty cohort exports zero rows legitimately and is not this error.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data to regenerate the .pulse file; the dictionary or encoding state is inconsistent. When every row failed, suspect the TARGET rather than the cohort — `first_error` is the writer's own refusal, and a column type the format cannot represent rejects every row identically.",
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
	PULSE_EXPORT_COLUMN_COLLISION: {
		Message: "Expanding a vector field into one column per element would emit a column name another column of the export already has.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Includes", "*"},
				Hint:   "Drop the colliding scalar field (or the vector) from the export with --include, or export to a format with native list cells (ndjson, jsonarray, arrow, parquet), which keep the vector as one column.",
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
		Message: "A multi-select column's observed dictionary exceeds the largest set width (set_u256 holds at most 256 entries).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Widen the set type up the ladder (set_u64 -> set_u128 -> set_u256) and re-import; above 256 entries, denormalize to one row per (record, element) pair. If the dictionary is actually bounded, raise --sample-rows or supply a force_type schema hint.",
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
	PULSE_IO_FORMAT_UNSUPPORTED: {
		Message: "The io factory was asked for a format it cannot build in that direction: an empty or unknown format identifier, or the native pulse format, which is opened directly rather than through a tabular reader or writer.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Pass one of the tabular formats io.Formats() lists (csv, tsv, ndjson, jsonarray, parquet, arrow, excel, spss), or derive it from the file name with io.FormatFromPath and check Format.CanRead / Format.CanWrite before constructing.",
			},
			{
				Action: FixupSetDefault,
				Hint:   "For a .pulse cohort, skip the factory: open it with pulse.Open or import / process it directly; no tabular reader or writer is involved.",
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
	PULSE_IMPORT_VECTOR_INVALID: {
		Message: "An import-time vector declaration cannot be folded, so the import is refused before any row is read and no cohort is written. details.reason names the case: malformed (the `--vector` / `--vector-kind` value does not parse), element_invalid, kind_invalid, scale_invalid (not both bounds, non-finite, or min >= max), kind_unknown_vector (a kind for an undeclared vector), kind_duplicate, duplicate_name, column_unknown, pattern_invalid, pattern_no_match, no_columns (a bare `--vector NAME` on a source with no typed lists — CSV / TSV / Excel), dim_out_of_range (the folded column count, or a native list's length, is outside 2..1024), column_claimed (a column folded by two vectors, or by a vector and a parent group), name_collision (the vector's name is a source column it does not fold), authoritative_source (an SPSS source), and against an explicit schema not_declared, dim_mismatch or type_mismatch. A native list column (bare `--vector NAME`, Arrow / Parquet LIST, a JSON array) is also refused mid-pass, with no cohort written, on list_length_varies (a list whose length differs from the first non-null list's), element_not_numeric (a string — a quoted number included — or boolean element), not_a_list (a scalar cell), list_dim_unknown (no non-null list to take a dim from) and malformed_list_cell (an io.ListAwareReader filled a ListCell outside its contract: an undeclared State, or Nulls neither nil nor as long as Values); details carry column and row.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Vectors"},
				Hint:   "Write each vector as `--vector NAME[:f32|f64]:COL1,COL2,...` (members in element order) or `--vector NAME:/REGEX/` (members are the matching columns in source order; the first capture group becomes each element's label), folding 2..1024 columns, each column in at most one vector and in no `--group`. Give a kind with `--vector-kind NAME:measure|scale|composition|binary[:MIN:MAX]` naming a declared vector.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "*", "dim"},
				Hint:   "With an explicit schema, type the vector field vec_f32 / vec_f64 with the dim its declaration folds, and declare it with --vector so the import knows which columns hold its elements.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Vectors"},
				Hint:   "A native list column becomes a vector only when every non-null list has the same length (2..1024) and holds only numbers and nulls: pad or drop the ragged rows at the source, cast string elements to numbers, or leave the column undeclared to import it as a set_* field; on CSV / TSV / Excel name the element columns instead (`--vector NAME:COL1,COL2,...`).",
			},
		},
	},
	PULSE_GROUP_DECLARATION_INVALID: {
		Message: "A parent-group declaration is malformed: it does not parse as `KEY[,KEY...]:MEMBER[,MEMBER...]` (or `MEMBER[,MEMBER...]`), declares no members, names a field twice within one group, or names a key that is not a member of its group.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups"},
				Hint:   "Write each group as `--group key1[,key2]:member1[,member2]` (key fields, a colon, then the fields the key determines), or `--group f1,f2` for a plain tuple group with no key check. Name every field once per group; field names containing ',' or ':' cannot be declared from the CLI — use io.ImportJob.Groups.",
			},
		},
	},
	PULSE_GROUP_FIELD_UNKNOWN: {
		Message: "A parent-group declaration names a field the cohort's schema does not have.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups", "*"},
				Hint:   "Use the exact field name from the imported schema (`pulse import predict --json` lists it); names are case-sensitive and match the source column header.",
			},
		},
	},
	PULSE_GROUP_FIELD_CONFLICT: {
		Message: "One field is named by two parent-group declarations; groups are independent and a field belongs to at most one.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups", "*"},
				Hint:   "Remove the field from all but one of the groups named in details.group_labels. If the two groups share a key, declare them as one group.",
			},
		},
	},
	PULSE_GROUP_MEMBER_NOT_CONSTANT: {
		Message: "A declared parent group is not one: two rows carry the same key tuple but disagree on a non-key member (value or null state). details.field names the member and details.row / details.source_row a row that disagrees with an earlier row of the same key.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups", "*"},
				Hint:   "Move the field named in details.field out of the group (it varies within the parent), or add it to the group's key if it is part of what identifies the parent. If the source should have been constant per key, fix the upstream join.",
			},
		},
	},
	PULSE_GROUP_ENTRIES_EXHAUSTED: {
		Message: "A parent group's dictionary would hold more distinct tuples than its u32 per-row index can address (2^32).",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Groups", "*"},
				Hint:   "Drop the group: a group with this many distinct tuples is near-unique per row and saves no space. Group only the fields that repeat as a parent block.",
			},
		},
	},
	PULSE_GROUP_TOO_NARROW: {
		Message: "A declared parent group's members occupy no more bytes per row than the 4-byte index that would replace them, so grouping could only grow the file; the group was dropped and its members stay in the row.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Groups", "*"},
				Hint:   "Drop the group, or add the other fields that repeat with it (the rest of the parent block) so its members outweigh the index; details.member_row_bytes must exceed details.index_width.",
			},
		},
	},
	PULSE_DEDUP_LOW_RATIO: {
		Message: "A parent group's dedup ratio (rows per distinct tuple) is below the ratio floor, or its dictionary makes the file no smaller; the group was still written, with its dictionary resident in memory.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Groups", "*"},
				Hint:   "Drop the group if details.byte_delta is not negative or details.dictionary_bytes is too much to hold resident; a ratio near 1 means the tuples are nearly unique per row.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"StrictDedup"},
				Hint:   "To keep a deliberately low-ratio group, re-run without --strict (it then writes with this warning) or lower --dedup-ratio-floor; a floor of 1 leaves only the grows-the-file check.",
			},
		},
	},
	PULSE_MATERIALIZE_SLOT_UNSUPPORTED: {
		Message: "A materialize request carries a slot that does not keep one row per source record; only features, filterers, attributes, windows, sort, weight and time_zone are accepted.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"*"},
				Hint:   "Remove the slot named in details.slot, or run the request through Process instead; a materialized cohort can then be processed with that slot as an ordinary request.",
			},
		},
	},
	PULSE_MATERIALIZE_NO_DERIVED: {
		Message: "A materialize request carries no features, attributes or windows, so there is no derived column to append to the cohort.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Features"},
				Hint:   "Add at least one feature, attribute or window slot; to copy filtered records without new columns, write the filtered cohort instead.",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_UNDECLARED: {
		Message: "A materialize request carries a feature whose output columns cannot be named before the run, so the output schema cannot be laid out.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Features", "*", "Type"},
				Hint:   "Replace the feature named in details.feature with a built-in operator, or fix its params or source field so it yields at least one output column (predict lists the columns each feature emits).",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC: {
		Message: "A derived value is not numeric, so it cannot be stored in the f64 column a materialize run writes for it; nothing was written.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Windows", "*", "Field"},
				Hint:   "Point the window named by details.column at a numeric field, or give a lag/lead a numeric default; details.value_type names the value that was refused.",
			},
		},
	},
	PULSE_MATERIALIZE_SIDECARS_INVALIDATED: {
		Message: "A materialize run overwrote a cohort whose sidecars are now stale; nothing was rebuilt.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Path:   []string{"InvalidatedSidecars"},
				Hint:   "Run each rebuild command the result's invalidated_sidecars lists (details.sidecars names their paths), or delete the stale sidecars.",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_ONLY: {
		Message: "The request carries the output slot, which lays out the cohort a materialize run writes and is accepted only by materialize.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Output"},
				Hint:   "Remove the output block, or run the request through materialize (pulse api materialize / pulse_materialize) to write the reshaped cohort.",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_INVALID: {
		Message: "The materialize output block is malformed: it sets both columns and exclude, keeps no column, has a column entry without from, names an unknown type, gives a precision / scale out of range or on a non-decimal type, sets on_cast_error to an unknown value, without a type or recode, or to null on a non-nullable column, recodes a column that is not categorical or a label to null on a non-nullable column, gives a recode.order that is not exactly the recoded labels once each, or gives shards.max_records ≤ 0. details.reason names the rule and details.path the offending key.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Output", "Exclude"},
				Hint:   "Use columns (the closed list to keep, in order) OR exclude (the columns to drop), never both; keep at least one column; give every columns entry a from naming a source field or derived column.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Output", "Shards", "MaxRecords"},
				Hint:   "Give output.shards.max_records a positive row count per shard, or drop output.shards to write a single file.",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_UNKNOWN_COLUMN: {
		Message: "A materialize output column names neither a source field nor a derived column of the request.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Output", "Columns", "*", "From"},
				Hint:   "Name a source field (pulse inspect lists them) or a feature / attribute / window label of the same request; details.suggestion names the closest match when there is one.",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_DUPLICATE: {
		Message: "Two materialize output columns are written under the same name after renaming.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Output", "Columns", "*", "As"},
				Hint:   "Give one of the entries named by details.path and details.first a distinct as; the same from may appear twice under different as names.",
			},
		},
	},
	PULSE_MATERIALIZE_OUTPUT_NULL: {
		Message: "A materialize output column declared nullable: false holds a null, so nothing was written.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Output", "Columns", "*", "Nullable"},
				Hint:   "Drop nullable: false from the column named by details.column (or set it true), or add a filterer that removes the rows where it is null (details.row is the first output row found).",
			},
		},
	},
	PULSE_MATERIALIZE_CAST_UNSUPPORTED: {
		Message: "A materialize output column's type names a field type its column cannot cast to. Legal: numeric (u4..u64, f32, f64, decimal128) to numeric, numeric to and from packed_bool, numeric to and from date / datetime, date to and from datetime, a set to a wider set rung, a categorical to another categorical width (its dictionary must fit) or to numeric, numeric to a categorical; vectors keep their type.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Output", "Columns", "*", "Type"},
				Hint:   "Drop the type from the column named by details.column, or name a type its from_type casts to (details.reason says why this pair is refused).",
			},
		},
	},
	PULSE_MATERIALIZE_CAST_FAILED: {
		Message: "A stored value does not fit a materialize output column's cast type (a fraction to an integer, a value past the range, NaN to an exact type, a value other than 0 / 1 to packed_bool, a categorical label that is not a number, or one distinct value more than a categorical holds), so nothing was written.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Output", "Columns", "*", "OnCastError"},
				Hint:   "Set on_cast_error: \"null\" on the column named by details.column (it must be nullable) to write a null in place of each such value, pick a wider type, or filter out the rows (details.row is the first output row found, details.value the value).",
			},
		},
	},
	PULSE_MATERIALIZE_CAST_NULLED: {
		Message: "A materialize output column with on_cast_error: null nulled values its cast could not represent; details.count says how many.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Output", "Columns", "*", "OnCastError"},
				Hint:   "Expected when on_cast_error is null. To fail instead of nulling, drop on_cast_error; to keep the values, pick a wider type for the column named by details.column.",
			},
		},
	},
	PULSE_MATERIALIZE_RECODE_UNKNOWN_LABEL: {
		Message: "A materialize output column's recode.map names a label its source column's dictionary does not hold (the source cohort changed, or the label is misspelled), so nothing was written.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Output", "Columns", "*", "Recode", "Map"},
				Hint:   "Rename or drop the recode.map key details.label (details.suggestion names the closest source label when there is one); pulse inspect --full-dict lists the dictionary of the column named by details.from.",
			},
		},
	},
	PULSE_MATERIALIZE_GROUP_NOT_INHERITED: {
		Message: "A materialize output does not carry one of the source's parent groups, because a member is dropped or re-coded (type, recode or bucket); its members are written as ordinary row fields.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Output", "Groups"},
				Hint:   "Expected when the column named by details.member changes. To regroup, declare output.groups over the written names; to write no group and silence this, set output.groups to []; to keep the group, write details.member unchanged.",
			},
		},
	},
	PULSE_RAKE_INVALID: {
		Message: "The rake request is malformed, so nothing is read or written. It needs a source `cohort`, an `out` path that is not the source, and at least one margin; each margin is either `field` + `targets` or two or more `fields` + `cells` (each cell one value per field, no tuple twice). Margin, `by` and `base_weight` fields must exist, a margin or `by` field must be categorical, packed_bool or an unsigned integer and appear once, a base weight must be numeric with no null, negative or non-finite value. Targets are finite and non-negative with at least one positive; `targets_as: shares` sums to 1 or 100; `missing` is `refuse` or `base`; caps satisfy 0 <= min_weight <= 1 <= max_weight; max_iter, tol and total are non-negative. A `weight_field` the source already has needs `overwrite` and may not be a margin or `by` field. details.reason names the rule and details.field the request path.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"margins", "*", "field"},
				Hint:   "Name an existing categorical, packed_bool or unsigned-integer field per margin (once each), with finite non-negative targets.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"weight_field"},
				Hint:   "Choose a new weight_field name, or set overwrite to replace the existing column.",
			},
		},
	},
	PULSE_RAKE_UNKNOWN_CATEGORY: {
		Message: "A rake target names a category its margin field cannot hold: not a dictionary entry of the categorical, not `true` / `false` for packed_bool, or not a canonical in-range decimal integer for an integer field. details carry margin, field and category.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"margins", "*", "targets"},
				Hint:   "Key every target by the field's stored category spelling (pulse inspect --full-dict lists a categorical's entries).",
			},
		},
	},
	PULSE_RAKE_UNTARGETED_CATEGORY: {
		Message: "Records hold a margin category that has no target in that margin, so raking cannot place them. details carry margin, fields, values, rows and first_row.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"margins", "*", "targets"},
				Hint:   "Add a target for the category (0 gives its records weight 0), or filter those records out of the cohort before raking.",
			},
		},
	},
	PULSE_RAKE_MISSING_VALUE: {
		Message: "Records have a null margin (or `by`) value and the rake's missing policy is `refuse` (the default). details carry field, rows and first_row.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"missing"},
				Hint:   "Set missing to \"base\" so a record keeps its base factor for a margin it has no value for, or recode the nulls; a null `by` value is always refused.",
			},
		},
	},
	PULSE_RAKE_EMPTY_CELL: {
		Message: "A margin category has a positive target but no record (or only zero-weight records) in its group, so no weight can reach the target. details carry margin, fields, values, target and, under `by`, group.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"margins", "*", "targets"},
				Hint:   "Collapse the empty category into a neighbour in both the data and the targets, or set its target to 0.",
			},
		},
	},
	PULSE_RAKE_NOT_CONVERGED: {
		Message: "Raking stopped at max_iter with an achieved share still further than tol from its target; the cohort was written with the last sweep's weights. Binding caps or contradictory margins are the usual cause. details carry iterations, residual, tol and, under `by`, group.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"max_iter"},
				Hint:   "Raise max_iter, loosen min_weight / max_weight, or check that the margins' targets describe one population.",
			},
		},
	},
	PULSE_RAKE_SIDECARS_INVALIDATED: {
		Message: "A rake run overwrote a cohort whose sidecars are now stale; nothing was rebuilt.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Path:   []string{"InvalidatedSidecars"},
				Hint:   "Run each rebuild command the result's invalidated_sidecars lists (details.sidecars names their paths), or delete the stale sidecars.",
			},
		},
	},
	PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL: {
		Message: "A facet asks a numeric statistic (numeric_percentiles, include_histogram) of a non-numeric field; predict warns, and the facet omits that statistic for the field. (A numeric aggregation over a categorical is refused PULSE_FIELD_TYPE_UNSUPPORTED.)",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Fields"},
				Hint:   "Ask numeric_percentiles / include_histogram of a numeric field; a categorical facet field's value_counts already carry its distribution.",
			},
		},
	},
	PULSE_FACET_VALUES_TRUNCATED: {
		Message: "Warning-class — a facet's discrete_top_k cut a discrete field's value list to its top-K values by count (ties by value), so the dropped values are absent from fields.<name>.discrete.values; truncated_at counts them. Details carry `field`, `discrete_top_k`, `distinct` (the field's distinct values) and `dropped`, plus `additive: true` on an additive_fields tally. The counts of the kept values are exact. Predict-facet raises the same code up front for a discrete_top_k under 5, with only `discrete_top_k` in details (it reads no record, so it cannot say which field will be cut).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"DiscreteTopK"},
				Hint:   "Raise discrete_top_k to at least details.distinct (or set 0 for no cap) to see every value; keep it to read only the top values.",
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
	PULSE_REGRESSION_WINDOW_UNAVAILABLE: {
		Message: "A regression names a window label where the windows run over grouped or aggregated output rows, so no record carries the window value the fit would read.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Path:   []string{"Windows"},
				Hint:   "Materialize the window first (pulse api materialize with the windows and their filters), then run the regression over the derived cohort, naming the window's column; or drop the groups, aggregations and crosstab so the windows run over the record rows.",
			},
		},
	},
	PULSE_REGRESSION_INVALID: {
		Message: "A regression slot names a type no registry on this instance resolves (never registered, hidden, or an embedder model whose extension is not registered); predict and every runtime surface refuse it alike.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Regressions", "*", "Type"},
				Hint:   "The type (details.type, at details.regression_index) names no registered REG_* model on this instance: pick one from the manifest's regression list.",
			},
			{
				Action: FixupRequiresReschema,
				Path:   []string{"Regressions", "*", "Type"},
				Hint:   "An embedder model (REG_<NAMESPACE>_<NAME>) is an extension: register its regression on pulse.Options.Extensions (Go embedders) before pulse.New, or run on an instance that registers it.",
			},
		},
	},
	PULSE_ATTRIBUTE_WINDOW_ORDER: {
		Message: "An attribute reads a window label, but only a row-local expression formula (no total()) or bound-model score runs after the windows, and only when the windows run over the record rows (no groups, aggregations or crosstab, or windows-first: one of them reads a window output).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Path:   []string{"Attributes"},
				Hint:   "Windows-first: rewrite the step as a row-local expression formula (no total()) or ATTR_REG_PREDICT and name its label (or the window label itself) in an aggregation, grouper or crosstab axis — the windows then run first over the record rows and the aggregations read the values off each record; or drop the groups, aggregations and crosstab so the windows run over the record rows.",
			},
			{
				Action: FixupRequiresReschema,
				Path:   []string{"Attributes"},
				Hint:   "Materialize the window first (pulse api materialize with the windows and their filters), then run the attribute over the derived cohort, naming the window's column.",
			},
		},
	},
	PULSE_WINDOW_INVALID: {
		Message: "A window operator's configuration is invalid (unknown window type, bad frame, bad params, no order key, no value field, label collision); predict and every runtime surface refuse it alike.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Windows", "*", "Type"},
				Hint:   "An unknown window type (details.type, at details.window_index) names no registered WIN_* operator on this instance: pick one from the manifest's window list.",
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Windows", "*", "Type"},
				Hint:   "A retired window type (renamed before v1.0.0, no alias) is refused with details.renamed_to naming its replacement — WIN_ADSTOCK is now WIN_DISTRIBUTED_LAG: set Type to the details.renamed_to value; the params are unchanged.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Windows", "*", "OrderBy"},
				Hint:   "Every WIN_* operator requires at least one OrderBy key (WIN_SHARE / WIN_LIFT only when params.cumulative is true); supply a date or numeric field.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Windows", "*", "Frame"},
				Hint:   "Drop Frame for ROW_NUMBER, RANK, DENSE_RANK, LAG, LEAD, PCT_CHANGE, DELTA, SHARE, LIFT; supply Frame (mode \"rows\") for MOVING_AVG (both bounds), EWMA, RUNNING_*.",
			},
			{
				Action: FixupRequiresReschema,
				Path:   []string{"Windows", "*"},
				Hint:   "details.reason windows_first_reads_aggregate: an aggregation, grouper or crosstab axis reads a window output, so the windows run first over the records and no aggregation label exists yet — drop the window-output read from the aggregations (the windows then run over the aggregated rows again), or materialize the windows first and aggregate the derived cohort in a second request.",
			},
		},
	},
	PULSE_FIELD_TYPE_UNSUPPORTED: {
		Message: "An operator slot names a field whose type the operator does not accept (details.accepts_types lists the types it does).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Name a field of one of details.accepts_types (a categorical for split_by, a contingency axis or stratify), or derive one first (ATTR_FORMULA, FEAT_ONE_HOT for a categorical, ATTR_SET_HAS / ATTR_SET_POPCOUNT for a set).",
			},
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Aggregations", "*", "Type"},
				Hint:     "A numeric aggregation over a categorical field would read its dictionary index: count its values instead.",
				Examples: []any{"AGG_MODE", "AGG_MODE_COUNT", "AGG_DISTINCT_COUNT", "AGG_COUNT"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Attributes", "*", "Field"},
				Hint:   "ATTR_PREVIOUS_STATE reads a categorical_* or packed_bool state: a state stored as coded integers has no labels to carry, so re-import it as a categorical (an explicit import schema typing the column categorical_u8) and name that field.",
			},
		},
	},
	PULSE_FEAT_TARGET_LEAKAGE_RISK: {
		Message: "FEAT_TARGET_ENCODE without params.fit_on averages every record's outcome, the validation/test records' included (and, without folds, the record's own): features run before filters, so a preceding train/test split changes no value. With folds but no fit_on the warning is reworded — no record sees its own outcome, every record still sees the holdout's. Only fit_on silences it. Predict decides from the request alone and warns; Process (a Compose slot too) repeats the warning on the response's warnings with predict's details {feature, feature_index} (+ folds); under strict both refuse — predict as an error, Process before any record is read.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Features", "Params", "fit_on"},
				Hint:   "Set params.fit_on to the training rows — e.g. \"split == 0\" after a FEAT_TRAIN_TEST_SPLIT labelled split — so only they feed the category means; add folds (with rng_seed) to encode the training rows out-of-fold as well.",
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Features"},
				Hint:   "For descriptive use, keep the in-sample encoding knowing it is not a held-out estimate.",
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
		Message: "A built-in aggregation with no decimal128 implementation (e.g., median or percentile in v1) names a decimal128 field; predict and the runtime refuse it.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Aggregations", "*", "Type"},
				Hint:     "Decimal fields support exact AGG_SUM, AGG_AVERAGE, AGG_MIN, AGG_MAX, AGG_VARIANCE, AGG_STDDEV, AGG_COUNT, AGG_DISTINCT_COUNT; pick one of those.",
				Examples: []any{"AGG_SUM", "AGG_AVERAGE", "AGG_MIN", "AGG_MAX"},
			},
		},
	},
	PULSE_ATTR_CODE_NOT_IN_DICTIONARY: {
		Message: "An ATTR_CODE_IN code is absent from the categorical field's dictionary in this cohort, so it matches no row and adds nothing to the share.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Attributes", "*", "Params", "codes"},
				Hint:   "Codes on a categorical field are dictionary LABELS, matched exactly (case included); check them against pulse inspect --full-dict and correct or drop each one listed in details.missing_codes.",
			},
		},
	},
	PULSE_ATTR_SCALE_OUT_OF_RANGE: {
		Message: "An ATTR_SCALE_SCORE member value lies outside the battery's declared scale range, so it was scored as missing (never clamped or reversed); rows left below min_valid score null.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Attributes", "*", "Params", "scale_min"},
				Hint:   "Check the declared range against the data: set params.scale_min / scale_max (or the kind \"scale\" vector's bounds) to the battery's true range, or drop the out-of-range codes (e.g. a 99 'don't know') with a filter or recode first. details.n_out_of_range counts the values, details.n_rows the rows.",
			},
		},
	},
	PULSE_PREVIOUS_STATE_AMBIGUOUS_TIE: {
		Message: "An ATTR_PREVIOUS_STATE subject has two or more records at one time point (the same order value) carrying different states, so its next time point has no single previous state; those records are null, never a guessed order.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Attributes", "*", "OrderField"},
				Hint:   "Order by a finer field (a datetime instead of a date) or drop the duplicate records first (pulse dedup, or a filter) so each subject has one record per time point. details.n_ambiguous counts the nulled records, details.n_subjects_ambiguous the subjects affected.",
			},
		},
	},
	PULSE_ADVISORY_TWO_GROUP_TEST_MANY_GROUPS: {
		Message: "A two-sample test splits its field by a grouping with more than two groups, but it compares exactly two; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Tests", "*", "Type"},
				Hint:   "Use the many-group comparison named in details.suggested, or filter split_by down to the two groups you mean to compare.",
			},
		},
	},
	PULSE_ADVISORY_MANY_TESTS: {
		Message: "The request emits many uncorrected p-values and names no multiplicity block, so some may fall below alpha by chance alone; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Multiplicity"},
				Hint:     "Merge details.suggested into the request — a multiplicity block such as {\"method\": \"holm\"} — to add adjusted p-values beside the raw ones.",
				Examples: []any{map[string]any{"multiplicity": map[string]any{"method": "holm"}}},
			},
		},
	},
	PULSE_ADVISORY_CATEGORICAL_AS_NUMERIC: {
		Message: "A mean-family aggregator or parametric test treats a field as a quantity, but the cohort's SPSS metadata records it as nominal, so its codes are labels; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Hint:   "Count the codes instead — use the operator named in details.suggested, or group by the field — unless the codes really are quantities and the SPSS measure level is out of date.",
			},
		},
	},
	PULSE_ADVISORY_ORDINAL_PARAMETRIC: {
		Message: "A parametric test treats a field as evenly spaced, but the cohort's SPSS metadata records it as ordinal, so its levels are ordered without a known spacing; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Tests", "*", "Type"},
				Hint:   "Use the rank-based test named in details.suggested, which reads only the order of the levels.",
			},
		},
	},
	PULSE_ADVISORY_WEIGHT_AVAILABLE_UNUSED: {
		Message: "The cohort's SPSS metadata records a weighting variable, but no slot resolves a weight, so every row counts once; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Weight"},
				Hint:     "Merge details.suggested into the request — a weight block naming the SPSS weighting variable — to weight every weight-aware slot; leave it out when unweighted figures are what you want.",
				Examples: []any{map[string]any{"weight": map[string]any{"field": "WT"}}},
			},
		},
	},
	PULSE_ADVISORY_EXTRA_PASS: {
		Message: "An ATTR_FORMULA calls total(expr), so the run makes an extra pre-pass over the filtered rows to sum each argument before it evaluates any row; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Attributes", "*", "Expression"},
				Hint:   "No change is needed when the cohort-wide total is what you want. To skip the extra pass, divide two sums after aggregation with AGG_RATIO, or replace total(expr) with a constant read from an earlier run.",
			},
		},
	},
	PULSE_ADVISORY_JACKKNIFE_COST: {
		Message: "A regression runs the leave-one-out jackknife over a large cohort, so the run refits the model once per row and its cost grows with the square of the row count; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Regressions", "*", "jackknife_groups"},
				Hint:     "Set jackknife_groups (details.suggested) for a delete-a-group jackknife: g refits instead of one per row, the groups drawn by rng_seed. Or set jackknife_by to a field whose values are natural clusters.",
				Examples: []any{map[string]any{"jackknife_groups": 50}},
			},
		},
	},
	PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED: {
		Message: "A regression fits penalty l1 or elasticnet, which selects its predictors from the data, so its standard errors, p-values and confidence intervals have no valid analytical form and are reported null; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Regressions", "*", "resample"},
				Hint:     "Set resample to bootstrap or jackknife (details.suggested): the replicate standard errors, p-values and intervals of the penalised estimator replace the nulls. Or fit penalty l2 (closed-form ridge covariance) or no penalty for analytical inference.",
				Examples: []any{map[string]any{"resample": "bootstrap"}},
			},
		},
	},
	PULSE_ADVISORY_CONSTRAINT_ACTIVE: {
		Message: "A REG_OLS fit's bounds.nonnegative constraint bound (a coefficient is held at 0), so the estimator is constrained and its classical standard errors, p-values, confidence intervals and covariance are not valid and are reported null; the point estimates and R² stand. This is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Regressions", "*", "resample"},
				Hint:     "Set resample to bootstrap (details.suggested): each replicate refits the bounded model, and the replicate standard errors, p-values and intervals replace the nulls. Or drop the bound on the predictors in details.active for the unconstrained fit's classical inference.",
				Examples: []any{map[string]any{"resample": "bootstrap"}},
			},
		},
	},
	PULSE_ADVISORY_SERIES_AXIS_NOT_DATE: {
		Message: "A series overlay that reads its rows in order (a prior, rolling, baseline or sibling overlay) runs over two or more groupers with no params.partition_by, so it partitions by every key but the last and runs its series down the last grouper, which is not a date grouper; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups"},
				Hint:   "Put the date grouper (details.date_axis, when present) last in Request.Groups so the series runs through time within each segment.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "partition_by"},
				Hint:     "If the last grouper is the order you mean, set params.partition_by to the leading key columns to state it; an explicit partition_by silences the advisory.",
				Examples: []any{map[string]any{"partition_by": []any{"warehouse"}}},
			},
		},
	},
	PULSE_ADVISORY_EXTENSION_PARAMS_UNDECLARED: {
		Message: "A slot names an embedder extension operator whose registration declares no params, and carries a params object: its keys pass to the operator unvalidated, so a misspelled key is not refused as it is on a built-in; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Params"},
				Hint:   "Embedder: declare the operator's params (ParamMeta) on its Extensions registration; every params key is then checked and an unknown one is refused PULSE_REQUEST_UNKNOWN_FIELD with a suggestion, and every declared value is checked against its JSONType and Required flag (PULSE_REQUEST_PARAM_INVALID). Caller: check details.keys against the operator's documentation.",
			},
		},
	},
	PULSE_ADVISORY_COMPOSE_FAMILIES_SPLIT: {
		Message: "The compose request's slots emit many p-values into two or more separate correction families, and each family is corrected on its own, so no correction spans the batch; this is an advisory, not an error.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Requests", "*", "Multiplicity", "Family"},
				Hint:     "To correct the readout as one family, set family \"compose\" on the multiplicity block of each slot named in details.slots (or on the compose request's own block, when the slots name no family of their own), with one method across the batch. Keep the split when each slot answers a separate question.",
				Examples: []any{map[string]any{"multiplicity": map[string]any{"method": "holm", "family": "compose"}}},
			},
		},
	},
	PULSE_SUPPRESS_ADVISORY_UNKNOWN: {
		Message: "Options.SuppressAdvisories names a code that is not a registered PULSE_ADVISORY_* advisory code.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Hint:   "Drop the code or correct its spelling; details.valid lists every advisory code this build registers.",
			},
		},
	},
	PULSE_RECOMMEND_INTENT_UNKNOWN: {
		Message: "Recommend or an examples search was asked for an intent that is not in the intent taxonomy, or one this instance hides.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Set intent to one of: benchmark, change_over_time, compare_groups, composition, data_quality, describe, distribution_shape, drivers, flows, lookup, measure_construct, prepare, relationship, segment, simulate; details.valid lists them.",
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
	PULSE_SYNTH_SOURCE_REQUIRED: {
		Message: "A tagged synthetic top-up call (synth from-profile) needs a source cohort path to copy real rows from; none was given.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Pass the original cohort's path as the source (--source on the CLI, or the SourceCohort field on SynthOptions).",
			},
		},
	},
	PULSE_SYNTH_OUTPUT_REQUIRED: {
		Message: "Synthesis never writes in place; an output path distinct from the source cohort is always required.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Pass a new .pulse output path (--output on the CLI, or the output argument to Pulse.Synth).",
			},
		},
	},
	PULSE_SYNTH_OUTPUT_COLLISION: {
		Message:            "The output path resolves to the same file as the source cohort; refused so the source is never opened for write or mutated.",
		FixupNotApplicable: true,
	},
	PULSE_SYNTH_ALREADY_TAGGED: {
		Message:            "The source cohort already declares a _synthetic field, so a tagged top-up run would produce an ambiguous second provenance column.",
		FixupNotApplicable: true,
	},
	PULSE_SYNTH_PROFILE_SCHEMA_MISMATCH: {
		Message: "The profile-derived spec's fields don't line up (name/type/decimal precision-scale, in order) with the source cohort's own schema.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-run `profile create` against the exact source cohort being topped up, then retry synth from-profile with the fresh profile.",
			},
		},
	},
	PULSE_SYNTH_RULE_FIELD_UNKNOWN: {
		Message: "A synth rule names a field the spec does not declare, so the rule could never fire.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*"},
				Hint:   "Check the field name against the spec's `fields[].name` (for a profile-derived run, dump it with `synth from-profile --emit-spec`); a rule over an undeclared field is refused, never ignored.",
			},
		},
	},
	PULSE_SYNTH_RULE_EXPR_INVALID: {
		Message: "A synth rule's `when` predicate or one of its `set_expr` expressions does not compile against the row environment.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "When"},
				Hint:   "Compile-check the expression against the row's own types: every scalar (including packed_bool) is a number, so write `flag == 1` not bare `flag`; a categorical is a string; a set_* is a map (`flags[\"opt\"]`); test absence with `isnull(field)`. `when` must return a bool.",
			},
		},
	},
	PULSE_SYNTH_RULE_VALUE_INVALID: {
		Message: "The value a synth rule wants to write cannot be written to its target field: wrong shape, a number outside the field type's range, or a value outside the domain the field's distribution declares. `details.slot` says whether it came from a `set` literal or a `set_expr` result.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "Set"},
				Hint:   "Match the literal to the target field's type: a number (or true/false) for a scalar, within that type's range; a declared `params.values` entry for a categorical_*; an array of declared `params.options` for a set_*.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "SetExpr"},
				Hint:   "Match the expression's RESULT to the target: a bool (lands as 1/0) or a number for any scalar, in that type's range; a declared category string for a categorical_*; a list of declared options for a set_*. A decimal128 takes a number from an expression and an exact string only from `set`.",
			},
		},
	},
	PULSE_SYNTH_RULE_EMPTY: {
		Message: "A synth rule declares no action slot, so it could only evaluate its `when` and discard the answer.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Rules", "*"},
				Hint:   "Give the rule an action — `set`, `set_expr`, `set_null` or `null_together` — or delete the rule.",
			},
		},
	},
	PULSE_SYNTH_RULE_CONFLICT: {
		Message: "One synth rule names the same field in two slots that disagree about what happens to it.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Rules", "*"},
				Hint:   "Drop the field from one of the two slots; if both outcomes are genuinely wanted, split them into two rules, which apply in declaration order with last write wins.",
			},
		},
	},
	PULSE_SYNTH_RULE_BLOCK_INVALID: {
		Message: "A synth rule's `null_together` block does not name at least two distinct fields.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Rules", "*", "NullTogether"},
				Hint:   "Name at least two distinct fields in the block, or use `set_null` for a single field — `null_together` exists to make one null decision cover several fields.",
			},
		},
	},
	PULSE_SYNTH_RULE_FIELD_NOT_NULLABLE: {
		Message: "A synth rule's `set_null` names a field that is not declared nullable, so the row would carry the type's zero rather than a null and the file could not show the rule fired.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Path:   []string{"Fields", "*", "Nullable"},
				Hint:   "Declare the field `\"nullable\": true` in the spec — the fix is on the FIELD, not the rule. On a profile-derived run the field is nullable only if the source had nulls in it: dump the spec with `synth from-profile --emit-spec`, add the flag, and generate with `synth from-schema`.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Rules", "*", "SetNull"},
				Hint:   "Or drop the field from `set_null` if a real 0 is what the cohort should carry — a non-nullable field has no way to record absence, and `null_together` will not help: it copies a gate's decision rather than making one.",
			},
		},
	},
	PULSE_SYNTH_RULE_OWNERSHIP_INVALID: {
		Message: "A synth rule declares `\"owns_nulls\": true` but names no field in `set_null`, so the ownership claim has nothing to apply to.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Rules", "*", "SetNull"},
				Hint:   "Name the fields whose absence this rule is the only source of. `owns_nulls` is scoped to `set_null` and to nothing else: it discards those fields' own `null_rate` draw so the rule's gate is the single source of their nulls.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Rules", "*", "OwnsNulls"},
				Hint:   "Or drop `owns_nulls`. A `null_together` block already discards every non-gate member's own null_rate by copying the gate's decision, so a block needs no ownership flag.",
			},
		},
	},
	PULSE_SYNTH_EXPR_NONDETERMINISTIC: {
		Message: "A synth row expression (a constraint, a rule's `when` or a `set_expr` value) calls `now()`. The wall clock answers differently on every run, so the spec could never generate byte-identical output for one seed; the synth row environment does not provide it.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "SetExpr"},
				Hint:   "Replace `now()` with a fixed value: a literal epoch-day or epoch-second number, or a field drawn from a `date` / `datetime` distribution with explicit bounds. A generated row has no wall-clock time of its own.",
			},
		},
	},
	PULSE_SYNTH_LAG_FIELD_INVALID: {
		Message: "A synth row expression's `lag(name, n[, default])` call does not name a declared field with a string literal. The field name sizes the cross-row state before the first row is drawn, so it cannot be computed or misspelled.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "SetExpr"},
				Hint:   "Write the field as a quoted literal the spec declares: `lag(\"spend\", 1, 0)`, not `lag(spend, 1)` or `lag(\"sp\" + \"end\", 1)`. Check the spelling against `fields[].name`.",
			},
		},
	},
	PULSE_SYNTH_LAG_OFFSET_INVALID: {
		Message: "A synth row expression's `lag(name, n[, default])` call has an `n` that is not an integer literal from 1 to 1024. The offset sizes the cross-row buffer before the first row is drawn, so it must be a literal, and the cap bounds that buffer.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "SetExpr"},
				Hint:   "Write n as a whole-number literal between 1 and 1024: `lag(\"spend\", 1)` reads the previous accepted row, `lag(\"spend\", 52)` the row 52 accepted rows back. A computed, fractional, zero or negative offset is refused.",
			},
		},
	},
	PULSE_SYNTH_RULE_DRAW_INVALID: {
		Message: "A synth row expression misuses a random draw: `uniform()` or `bernoulli(p)` is called from a constraint, with the wrong arguments, or `bernoulli(p)` is given a `p` outside [0, 1] (NaN included). Draws are a rule's: each call site reads its own seeded stream, so a constraint that drew would make acceptance itself random.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Rules", "*", "SetExpr"},
				Hint:   "Call `uniform()` with no arguments and `bernoulli(p)` with one probability in [0, 1]; clamp a computed one (`bernoulli(min(1, max(0, pd)))`). To filter on a draw, store it in a rule (`{\"set_expr\": {\"u\": \"uniform()\"}}` on an `\"output\": false` field) and constrain the field.",
			},
		},
	},
	PULSE_SYNTH_DST_NONEXISTENT: {
		Message: "A `uniform_datetime` field drew a local wall-clock time its zone never shows — the skipped hour of a DST spring-forward — and the field's `dst_policy` is the default \"error\", so generation stops and nothing is written. Details carry `field`, `local_time` (the naive reading), `tz` (the zone) and `dst_policy`. A UTC or fixed-offset zone has no transitions and never raises this.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Fields", "*", "Params"},
				Hint:   "Set the field's params.dst_policy: \"earlier\" reads the skipped time at the pre-transition offset (Europe/Berlin 2026-03-29 02:30 → 01:30Z), \"later\" at the post-transition offset (→ 00:30Z) — the import's --dst-policy spellings. The run then warns how many generated times were resolved.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Fields", "*", "Params"},
				Hint:   "Or give the transition hour no weight: a 0 in params.hour_weights at the skipped local hour (02 in most of Europe and North America) means no time in it is ever drawn.",
			},
		},
	},
	PULSE_SYNTH_DST_AMBIGUOUS: {
		Message: "A `uniform_datetime` field drew a local wall-clock time its zone shows twice — the repeated hour of a DST fall-back — and the field's `dst_policy` is the default \"error\", so generation stops and nothing is written. Details carry `field`, `local_time` (the naive reading), `tz` (the zone) and `dst_policy`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Fields", "*", "Params"},
				Hint:   "Set the field's params.dst_policy: \"earlier\" takes the first occurrence (pre-transition offset), \"later\" the second — the import's --dst-policy spellings. The run then warns how many generated times were resolved.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Fields", "*", "Params"},
				Hint:   "Or give the repeated hour no weight: a 0 in params.hour_weights at the repeated local hour (01 in North America, 02 in most of Europe) means no time in it is ever drawn.",
			},
		},
	},
	PULSE_SYNTH_FIDELITY_MODEL_INVALID: {
		Message: "A supplied fidelity model request cannot be refit: it carries no `regressions`, or the synth call has no source cohort or no fidelity report path to write the refit into. The call is refused before generation and nothing is written.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Regressions"},
				Hint:   "Give the request at least one regression (the same `regressions` array `pulse process` reads); its cohort is ignored, the refit runs on the source cohort and on the generated rows.",
			},
			{
				Action: FixupSetDefault,
				Hint:   "Pass the model together with a source cohort and a fidelity report path (--source and --fidelity-report on synth from-profile; SourceCohort and FidelityReportPath on SynthOptions).",
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
		Message: "A numeric test's field resolves to a categorical or set schema type, which carries no number to test.",
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
	PULSE_TEST_PARTITION_INVALID: {
		Message: "A post-test's partition on a grouped request cannot be read. With two or more groupers every post_tests entry runs once per partition — the result rows sharing the leading group keys, every key but the last by default — so a trend runs down the last grouper within each segment. params.partition_by must be a leading prefix of the group key columns ([] means no partition: one run over every row), and every post-test of one request must partition alike, because Response.PostTests is one flat post-tests x partitions list. Raised at predict and at runtime with the same Details {index, type, partition_by, groups, reason, field, depths}: reason is not_string_list, unknown_field, not_prefix or mixed_depth.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"PostTests", "*", "Params", "partition_by"},
				Hint:     "Name the first k entries of Details.groups, in order (groups [warehouse, week] admits [warehouse]); omit partition_by to partition by every key but the last, or pass [] for one run over every row.",
				Examples: []any{map[string]any{"partition_by": []any{"warehouse"}}, map[string]any{"partition_by": []any{}}},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"PostTests", "*", "Params", "partition_by"},
				Hint:   "mixed_depth: give every post-test the same partition_by (Details.depths lists each one's depth), or put the post-tests that partition differently in their own request (a Compose slot each).",
			},
		},
	},
	PULSE_TEST_SPLIT_GROUPS_LT_2: {
		Message: "A two-sample or ANOVA test observed fewer than the required number of distinct groups in the SplitBy field. On a grouped request it is also raised at predict and runtime when SplitBy names a grouper field (details.reason split_is_grouper: every group holds one level) or a post-test's SplitBy names one of its partition key columns (details.reason split_is_partition_key: every partition holds one level), and it rides a warning naming details.group_key when one group is too sparse (that group's statistic and p_value are null; the others proceed).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "SplitBy"},
				Hint:   "Pick a SplitBy field that resolves to >= 2 distinct categories after filtering, or relax upstream filters.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups", "*", "Field"},
				Hint:   "split_is_grouper: a grouped request runs the test once per group; drop the SplitBy field from groups, or run the across-groups test as its own ungrouped request.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"PostTests", "*", "Params", "partition_by"},
				Hint:     "split_is_partition_key: a post-test runs once per partition (every key but the last by default); pass partition_by [] to compare across the partition keys in one run, or reorder Request.Groups so the split key comes last.",
				Examples: []any{map[string]any{"partition_by": []any{}}},
			},
		},
	},
	PULSE_TEST_CONTINGENCY_DEGENERATE: {
		Message: "A chi-square contingency table is empty or has a single row/column. On a grouped request it is also raised at predict and runtime when Rows or Cols names a grouper field (details.reason split_is_grouper), and it rides a warning naming details.group_key when one group's table is degenerate (that group's statistic and p_value are null; the others proceed).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Verify both Rows and Cols resolve to fields with more than one observed level; aggregate rare levels into an \"other\" bucket if needed.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups", "*", "Field"},
				Hint:   "split_is_grouper: a grouped request runs the test once per group; drop the Rows / Cols field from groups.",
			},
		},
	},
	PULSE_TEST_EXPECTED_COUNT_TOO_LOW: {
		Message: "A chi-square cell's expected count, or a two-proportion z-test group's expected successes or failures under the null rate, is below 5; the asymptotic chi-square / normal approximation is unreliable.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Tests", "*", "Type"},
				Hint:     "Use TEST_FISHER_EXACT for small 2x2 tables (a two-group proportion test is one: split_by x success); for larger tables, pool rare levels to raise per-cell expected counts.",
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
		Message: "TEST_PROP_Z did not supply a usable Params.success; the test cannot decide which category counts as a positive outcome.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Tests", "*", "Params", "success"},
				Hint:   "Supply the dictionary value that represents success, spelt exactly as the outcome's dictionary holds it (e.g. {\"success\": \"yes\"}); a categorical success no entry matches is refused — use pulse inspect to list the dictionary values.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Tests", "*", "Params", "success"},
				Hint:   "On a packed_bool outcome success is one of its two levels, exactly \"1\" (true) or \"0\" (false) — details.valid_levels; there are no true / false aliases.",
			},
		},
	},
	PULSE_TEST_EXPECTED_INVALID: {
		Message: "TEST_CHISQ params.expected (goodness-of-fit proportions, label to proportion) cannot define a fit: it is set with cols, lists fewer than two labels or an empty one, holds a proportion that is not a finite number above 0, does not sum to 1 within 1e-9, or (packed_bool rows) names a label other than \"0\" / \"1\". details.reason says which.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Tests", "*", "Params", "expected"},
				Hint:     "List every rows level with its planned share, each above 0, summing to exactly 1 (e.g. a 50/50 split); Pulse never renormalises.",
				Examples: []any{map[string]any{"expected": map[string]any{"control": 0.5, "treatment": 0.5}}},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "Cols"},
				Hint:   "with_cols: goodness of fit reads rows alone; drop cols, or drop params.expected for the independence test.",
			},
		},
	},
	PULSE_TEST_EXPECTED_LABEL_MISSING: {
		Message: "A TEST_CHISQ goodness-of-fit run observed rows levels (details.labels) that params.expected does not list; the proportions are never renormalised over what was seen.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Tests", "*", "Params", "expected"},
				Hint:   "Add each missing label with its expected share (re-balancing the others so the total stays 1), or filter those rows out before the test; check the spelling against pulse inspect's dictionary.",
			},
		},
	},
	PULSE_TEST_REFERENCE_INVALID: {
		Message: "A two-group test's params.reference (the split_by level signed figures are read against) is set without split_by, or is not a level of the split_by field (\"0\" / \"1\" on a packed_bool, a dictionary entry on a categorical). details.reason says which.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Tests", "*", "Params", "reference"},
				Hint:     "Name the control arm exactly as the split_by dictionary spells it (pulse inspect lists the levels); on a packed_bool use \"1\" or \"0\". Omit it for alphabetical order.",
				Examples: []any{map[string]any{"reference": "control"}},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "SplitBy"},
				Hint:   "without_split_by: reference orders the two split_by groups; set split_by, or drop reference for the one-sample form.",
			},
		},
	},
	PULSE_TEST_REFERENCE_MISSING: {
		Message: "A two-group test saw two split_by groups (details.groups) and neither is params.reference: the filtered data (or this group of a grouped run) never shows that level.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Tests", "*", "Params", "reference"},
				Hint:   "Name one of the two observed groups as the reference, or filter the rows to the two arms you mean to compare.",
			},
		},
	},
	PULSE_TEST_COVARIATE_INVALID: {
		Message: "A test's params.covariate (the pre-period column a two-group mean or proportion test adjusts by, CUPED) cannot be used: the test takes no covariate, split_by is missing, the column is not numeric, or it is the test's own field. details.reason says which.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Tests", "*", "Type"},
				Hint:     "unsupported_test: only the two-group mean and proportion tests take a covariate — TEST_WELCH, TEST_T with split_by, TEST_Z_TWO_SAMPLE, TEST_PROP_Z with split_by; drop covariate on any other test.",
				Examples: []any{"TEST_WELCH"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "Params", "covariate"},
				Hint:   "Name a numeric column measured BEFORE assignment (a pre-period value of the outcome is best), other than the test's own field; without_split_by: set split_by — the adjustment compares two groups (drop covariate on a one-sample t or a p0 test).",
			},
		},
	},
	PULSE_TEST_P0_INVALID: {
		Message: "TEST_PROP_Z params.p0 (the one-proportion test's hypothesised success rate) is not strictly between 0 and 1, or is set together with split_by.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Tests", "*", "Params", "p0"},
				Hint:     "Give p0 as a proportion strictly between 0 and 1 (e.g. 0.5 for a planned 50/50 split).",
				Examples: []any{map[string]any{"p0": 0.5}},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Tests", "*", "SplitBy"},
				Hint:   "with_split_by: p0 selects the one-proportion test; drop split_by, or drop p0 for the two-proportion test.",
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
		Message: "A rank-based test, or a two-sample distribution test on its asymptotic p, observed ties on >= 50% of input values: the test then mostly compares the share of tied values, not their order, and the asymptotic approximation loses accuracy.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Hint:   "Treat the p-value as advisory: with this many ties it mostly reflects how often each group hits the tied value(s). Compare those shares directly (TEST_CHISQ or TEST_PROP_Z on a tied-vs-not indicator), or for small n prefer an exact-permutation variant when registered.",
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
				Hint:     "Rename the registration so it matches ^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|MAT|REG)_[A-Z][A-Z0-9]+_[A-Z](?:[A-Z0-9_]*[A-Z0-9])?$ with the prefix of its registration slot (MatrixOps: MAT_, Regressions: REG_), e.g. AGG_ACME_BRAND_SCORE.",
				Examples: []any{"AGG_ACME_BRAND_SCORE", "ATTR_ACME_ADJUSTMENT", "MAT_ACME_SCATTER", "REG_ACME_HILL"},
			},
		},
	},
	PULSE_EXTENSION_NAME_RESERVED: {
		Message: "An embedder registration uses a namespace segment reserved for Pulse internals (BUILTIN / STANDARD / CORE / PULSE), or an expression function name Pulse reserves (`total`, the formula attribute's cohort-total term).",
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
		Message: "A name was registered more than once in a single pulse.New call: an operator (or expr function) registered twice in its category, or a label table, range table or population declared both programmatically on Options.Extensions and by a file in its directory (LabelTablesDir / RangeTablesDir / PopulationsDir or their PULSE_*_DIR variables). The kind is under `category`, the name under `name` and, for a directory collision, the file under `path`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Drop the redundant registration; only one Factory may bind a given name per Service.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "For a directory collision, remove or rename the file under `path`, or drop the programmatic entry — each table or population has exactly one source.",
			},
		},
	},
	PULSE_EXTENSION_STREAMABLE_MISMATCH: {
		Message: "A registration's streaming declaration disagrees with the interface its factory returns: it declared streaming capability without the required streaming interface, or (features, checked both ways) declared Streamable=false for a value that implements extend.StreamingFeatureComputer, or (regressions, checked both ways) declared Streamable=true for a value that is not an extend.StreamingRegression (Merge included) or Streamable=false for one that is not an extend.BufferedRegression.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Either implement the streaming interface (OnlineAggregator / RowLocalAttribute / TwoPassAttribute / StreamingGrouper / StreamingFeatureComputer) on the returned type, or flip Streamable=false / Mode=buffered to use the buffered path.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "For a feature (details category \"feature\"), make Streamable equal whether the returned value implements extend.StreamingFeatureComputer: the runtime streams on the value while predict reads the declaration, so set Streamable=true for a streaming value, or drop PrePass/Finalize/EmitRow from a buffered one.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "For a regression (details category \"regression\"), a Streamable=true model must implement extend.StreamingRegression — UpdateRow, Merge and Finalize (details.reason missing_merge names a model without Merge) — and a Streamable=false one extend.BufferedRegression (Fit); flip Streamable or add the missing methods.",
			},
		},
	},
	PULSE_EXTENSION_FANOUT_MISMATCH: {
		Message: "A grouper registration's FansOut declaration disagrees with whether its factory returns extend.MultiKeyStreamingGrouper.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Set FansOut=true when the returned grouper implements MultiKeyStreamingGrouper (KeysForRow fans one record into several buckets), FansOut=false when it maps each record to exactly one key. Declaring false for a multi-key grouper is the silent over-count the check exists to prevent — fix the declaration, not the check.",
			},
		},
	},
	PULSE_EXTENSION_PURPOSE_INVALID: {
		Message: "An extension registration's Purpose (or a test registration's Interpretation) breaks a guidance validity rule; details name the registration and the failing rule.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Fix the field the rule names: Plain non-empty and at most 140 characters; at least one intent from pulse.Intents(); at least two Questions; at least one NotFor entry whose Use names a registered operator (built-in or another extension) or a <kind>:<name> feature; UseCases keyed by survey/ops/science/harness; Level basic/intermediate/advanced; Glossary IDs from pulse.Glossary(), listing every jargon term Plain uses. Or omit Purpose entirely — it is optional.",
			},
		},
	},
	PULSE_EXTENSION_SKILL_INVALID: {
		Message: "A skill file in Extensions.Skills fails validation; details name the skill and the reason (layout, stem, frontmatter, name, kind, operator, category, requires, sections, budget, fence, fence_coverage, see).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Ship only top-level .md files: op-<category>-<kebab>.md documenting a registered extension operator (stem = op- + the operator name lowercased, _ to -; kind: operator; category: and operator: matching it) or ext-<kebab>.md (kind: design). Keep frontmatter name equal to the stem, carry the family's required ## sections, stay within the budget (op-* 1200 bytes, ext-* 6000 bytes of body), close every <!-- feature: … --> fence and name only features or registered operators in it, fence every other operator, <kind>:<name> feature or feature-owned pulse_* tool the body names, and point ## See only at skills the instance carries.",
			},
		},
	},
	PULSE_EXTENSION_SKILL_COLLISION: {
		Message: "An Extensions.Skills stem is already taken by a built-in skill or by another embedder skill file.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Rename the file: embedder skills never override or shadow the built-in pack (pulse_skills_list lists every taken stem), and each stem may be shipped once across every merged Extensions value. Topical embedder skills use the ext-<kebab> prefix, which no built-in carries.",
			},
		},
	},
	PULSE_EXTENSION_EXAMPLE_INVALID: {
		Message: "An example file in Extensions.Examples fails validation; details name the example and the reason (layout, meta, name, category, description, tags, intents, operators, capabilities, body, operators_body, edge_coverage).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Ship only top-level .json files, each a request body plus a _meta block with only the keys name, category, description, tags, operators, intents, capabilities: name and category lowercase (letters, digits, - or _), a description, tags from the canonical example taxonomy, intents from the intent taxonomy (pulse.Intents), operators registered on the instance (built-in or yours) and exactly the body's \"type\" values, capabilities only as non-operator feature names (capability:stream). The body must decode strictly as its request root (requests → compose, stages → process chain, fields → facet, otherwise process), and every operator named elsewhere in the body or description must be reachable by an edge (list it in a type or overlays[].kind, or drop the mention).",
			},
		},
	},
	PULSE_EXTENSION_EXAMPLE_COLLISION: {
		Message: "An Extensions.Examples name is already taken by a built-in example or by another embedder example file.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Rename _meta.name: embedder examples never override or shadow the built-in library (pulse_examples_search lists every taken name), and each name may be shipped once across every merged Extensions value. Prefixing names with your namespace (acme-…) avoids both.",
			},
		},
	},
	PULSE_EXTENSION_MERGEABLE_MISMATCH: {
		Message: "An aggregator, grouper or attribute registration declared Mergeable=true but cannot merge: it is not Streamable (an attribute: not Mode row_local), its factory does not return the merge interface (extend.MergeableAggregator; extend.MergeableGrouper for a grouper that emits components; extend.MergeableAttribute for an attribute that emits components), or its ComponentSchema declares keys with Mergeability \"none\".",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Declare Mergeable=true only alongside Streamable=true (an attribute: Mode row_local), implement Merge(other extend.OnlineAggregator) error on an aggregator (MergeState(other extend.Grouper) error on a grouper that emits components, MergeTally(other extend.RowLocalAttribute) error on an attribute that emits components) to fold other's state into the receiver, and classify ComponentSchema.Mergeability as \"mergeable\" or \"partial\" — or set Mergeable=false and the parallel reducers run the operator serially.",
			},
		},
	},
	PULSE_EXTENSION_MARGIN_REDUCIBILITY_MISMATCH: {
		Message: "An aggregator registration declared a MarginReducibility it cannot honour: the class is unknown, or it is a fusable class (summable, mean_reducible, independent) on a registration that is not also Mergeable and Streamable.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Declare MarginReducibility as one of summable, mean_reducible, independent or recompute; a fusable class (anything but recompute) admits the operator as a fused crosstab cell and needs Mergeable=true and Streamable=true alongside it — or omit it and the crosstab cell runs buffered.",
			},
		},
	},
	PULSE_EXTENSION_ARRAY_VALUED_MISMATCH: {
		Message: "An aggregator registration declared ArrayValued it cannot honour: without VectorInput, with a MarginReducibility, or with a factory whose value does not implement extend.RichAggregator.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "An array value is one figure per vector member: declare VectorInput=true beside ArrayValued, leave MarginReducibility empty (a crosstab refuses an array-valued cell), and return a value implementing extend.RichAggregator whose Rich gives a []float64 of the member count — or drop ArrayValued and return one figure.",
			},
		},
	},
	PULSE_EXTENSION_MATRIX_OUTPUT_MISMATCH: {
		Message: "A matrix registration's operator — finalized at pulse.New over a synthetic three-member co-moment, or on any run — returned no Primary matrix or filled an Auxiliary, Vectors or Scalars key its OutputKeys do not declare.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Always return MatrixOutput.Primary, and list every Auxiliary, Vectors and Scalars key Finalize can fill in the registration's OutputKeys (a declared key the operator leaves unfilled on some input is fine) — or stop filling the undeclared key named in details.undeclared.",
			},
		},
	},
	PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH: {
		Message: "A regression registration's model — fitted at pulse.New over a small synthetic row set, or on any run — returned no coefficient, an empty or repeated coefficient name, an Outputs key its OutputSchema does not declare, (when the spec set vcov) no Vcov or one that is not square over the coefficients, or a Warnings entry that is nil, has an empty message (invalid_warning) or a code that is not a registered Pulse code (unregistered_warning_code).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Return at least one RegressionCoefficient, each with a unique non-empty Name; declare every Outputs key the model can fill in the registration's OutputSchema (a declared key left unfilled is fine) or stop filling the undeclared key named in details.undeclared; and when the registration declares Vcov, return a Vcov square over Coefficients, in their order, whenever spec.Vcov is set; give every Warnings entry a non-empty Message and a registered Pulse code (PROCESSING_REGRESSION_DIAGNOSTIC with a details.reason for the model's own diagnostics).",
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
		Message: "A ParamMeta entry has missing or contradictory fields (empty Name, unknown JSONType, Required=true with a non-nil Default, an ExactlyOneOf group with one member or a Required / defaulted member, or an Example that is not a JSON value of its JSONType — reason example_type_mismatch), or a registration declares a contradictory flag (details.reason names it, e.g. count_semantic_vector_input).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Hint:     "Set Name, set JSONType to one of {string, number, boolean, array, object}, and drop Default when Required=true. An ExactlyOneOf group needs two or more members, each Required=false with no Default. An Example must marshal to a JSON value of the param's JSONType (details.expected / got).",
				Examples: []any{"string", "number", "boolean", "array", "object"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Extensions", "Aggregators", "*", "CountSemantic"},
				Hint:   "reason count_semantic_vector_input: CountSemantic declares one count per slot, which a VectorInput / ArrayValued aggregator (one figure per member) never finishes as — drop CountSemantic, or register a single-field aggregator.",
			},
		},
	},
	PULSE_EXTENSION_COMPONENT_SCHEMA_MISMATCH: {
		Message: "An extension operator's components emitter (its ComponentsFunc, or the operator value's own Components() method — details.emitter names which) emitted keys that are not declared in the registration's ComponentSchema. The probe rejects the registration so the runtime never sees a half-declared components contract.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Extensions", "*", "ComponentSchema", "Keys"},
				Hint:   "Add the undeclared emitted keys to ComponentSchema.Keys (with the matching JSONType + Mergeability), or remove them from the ComponentsFunc (or Components() method) emission. See docs/src/internals/extension-points.md for the per-category contract.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Extensions", "*", "ComponentsFunc"},
				Hint:   "Drop the extra keys from the ComponentsFunc map before returning so the emission stays a subset of ComponentSchema.Keys (plus the universal-floor keys n / n_null / total_n / n_in / n_out / n_null_input the orchestrator owns).",
			},
		},
	},
	PULSE_EXTENSION_MISSING_COMPONENT_SCHEMA: {
		Message: "An extension operator has a components emitter — a ComponentsFunc, or an operator value with its own Components() method (details.reason self_emitter_without_schema_keys) — but declares no ComponentSchema.Keys (or declares Mergeability without Keys). Both halves of the contract must be present together; the floor-only path keeps both empty.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Extensions", "*", "ComponentSchema"},
				Hint:   "Add a ComponentSchema with the keys + Mergeability that the ComponentsFunc (or the value's Components() method) emits, or drop the emitter to fall back to the floor-only path. See docs/src/internals/extension-points.md for examples.",
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
	PULSE_COHORT_COMPRESSED: {
		Message: "The path holds a zstd-compressed transfer artifact (`.pulse.zst`), not a cohort. Compression is transport-only: Pulse never opens a compressed file as a cohort, because fixed-stride random access, mmap, point lookup and parallel decode all need the uncompressed bytes at rest.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Cohort", "Filename"},
				Hint:   "Decompress first: `pulse import transfer --input cohort.pulse.zst --output cohort.pulse` (library: Pulse.ImportTransfer), then pass the resulting .pulse path. The result is byte-identical to the cohort that was exported.",
			},
		},
	},
	PULSE_TRANSFER_INVALID: {
		Message: "The transfer compress or decompress request was refused; details.reason names why (level, not_a_cohort, already_compressed, not_zstd, corrupt_stream, output_exists).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "level: pass a zstd level in 1..22 (default 3). not_a_cohort / already_compressed: `pulse export transfer` takes an uncompressed .pulse cohort or shard archive. not_zstd: `pulse import transfer` takes a .pulse.zst produced by `pulse export transfer`; an uncompressed .pulse needs no import. corrupt_stream: the artifact was truncated or damaged in transit — re-transfer it. output_exists: remove the output or pass --overwrite.",
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
		Message: "The incoming shard's structural schema (field count, per-field name/type/byte_offset/bit_position, categorical width, nullability, group layout) does not match the archive's canonical schema; details.dimension names the dimension when set (e.g. \"nullable\": a shard field is nullable where the archive's is not, or non-nullable with a different null-bitmap width or on a group member).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-import the source data using the same schema as the canonical archive (run `pulse inspect <archive>` to view the canonical fields), or rebuild the archive against the new schema via `pulse shard create`.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "For details.dimension \"nullable\": nullability never widens across shards. Re-import the shard with the archive's nullability for that field, or rebuild the archive seeding with a shard that declares the field nullable (`pulse shard create` takes the first shard's schema as canonical).",
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
		Message: "An incoming shard's per-field description — or a vector field's element labels, kind or scale range (details.dimension vector_labels / vector_kind / vector_scale) — differs from the canonical schema's; this metadata is advisory and the canonical copy wins, but the divergence is surfaced as a warning so embedders can keep cohort metadata in sync.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema", "Fields", "*", "Description"},
				Hint:   "Update the source data's per-field description (or the vector's labels / kind / scale range) to match the canonical archive's, or accept the warning (downstream consumers see the canonical metadata).",
			},
		},
	},
	PULSE_SHARD_SET_WIDENED: {
		Message: "A `shard add` merged a dictionary that outgrew a set_* field's bitmask, so the field was promoted to a wider rung and every record of every shard in the archive was re-laid-out. The archive is correct and no action is required; the warning exists because the rewrite is expensive and must never be silent.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "No action needed — the archive was widened in place and remains openable. To avoid the rewrite next time, import the source at the wider set rung up front, and watch `pulse shard verify`'s set-width headroom to see a widen coming.",
			},
		},
	},
	PULSE_SHARD_GROUPS_REWRITTEN: {
		Message: "A `shard create` / `shard add` re-encoded shard bytes to fit the archive's parent-group layout: a grouped shard was stored flattened in an ungrouped archive, a shard was re-encoded into a grouped archive's groups, or a constant group the arriving shard disagreed with was promoted to an indexed group across every shard (details.archive_rewritten). The archive is correct; the warning exists because the rewrite is expensive and must never be silent.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "No action needed — every shard decodes to the same values. To avoid the rewrite, import or `pulse dedup` every shard with the archive's `--group` declarations (and without `--elide-constants` for a column that varies between shards), and watch `pulse shard verify`'s group_index_headroom: a constant group reports zero headroom because its next distinct value promotes it archive-wide.",
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
	PULSE_SHARD_COUNT_LIMIT: {
		Message: "The archive would hold more than 65535 shards, the most the `_schema.pulse` shard_count (a u16) records; the write was refused before anything was written.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Shard", "Paths"},
				Hint:   "Concatenate small shards into fewer, larger `.pulse` files before archiving them, or split the data across more than one archive. `details.shard_count` is the count the write would have produced and `details.limit` the ceiling.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Output", "Shards", "MaxRecords"},
				Hint:   "A materialize run with output.shards: raise max_records so the written rows fit 65535 shards (`details.row` is the first row past the limit), or filter the rows written.",
			},
		},
	},
	PULSE_SHARD_INDEXES_UPDATED: {
		Message: "A `shard add` / `shard remove` kept the archive's point-lookup indexes in step: an add built the new shard's sidecar for every archive index, a remove deleted the removed shard's sidecar, and the index manifest's shard coverage was updated. Every index still answers lookups; the warning exists because files beside the archive were rewritten and that must never be silent.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "No action needed — details.indexes names each index kept (keys, logical index_path, the shard's shard_index_path). `pulse index list <archive>` shows the coverage and `pulse index verify <archive> --key <field>` re-checks it. An add that re-encodes the stored shards (a set widen or group promotion) keeps nothing and reports invalidated_sidecars instead: rebuild with `pulse index build <archive> --key <field>`.",
			},
		},
	},
	PULSE_CHAIN_NOT_MERGEABLE: {
		Message: "A ProcessChain stage uses an operator that the v1 chain gate does not yet support (windows, features, tests, regressions, two-pass attributes, or a non-mergeable grouper/aggregator), or carries a crosstab (a crosstab answers a matrix, not the rows the next stage reads).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Stages", "*", "Request"},
				Hint:   "Run the offending stage as a standalone Process call (the details payload names the rejecting stage index) or restructure the stage to use mergeable aggregators (COUNT, SUM, AVERAGE, MIN, MAX, RANGE, VARIANCE, STDDEV, DISTINCT_COUNT, NULL_COUNT, FREQUENCY, MODE, MODE_COUNT) with row-local attributes (FORMULA, DATE_PART) and mergeable groupers (any but GROUP_QUANTILE, GROUP_DATE, GROUP_KMEANS).",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Stages", "*", "Request", "Crosstab"},
				Hint:   "A crosstab cannot run as a chain stage (the message reason names it): run it as its own Process request, over the cohort or over a cohort materialised from the chain's rows.",
			},
		},
	},
	PULSE_CHAIN_STAGE_JOIN: {
		Message: "Only ProcessChain stage 0 may carry Joins: a later stage reads the previous stage's output rows, not a cohort, so there is no left side to join.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Stages", "*", "Request", "Joins"},
				Hint:   "Move the JoinSpec onto stage 0 (the details name the offending stage), or run the joined step as a standalone Process call and chain from its output.",
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
	PULSE_SWEEP_INVALID: {
		Message: "The ComposedRequest `sweep` block is malformed, so nothing runs — predict refuses it identically. A sweep needs at least one axis; every axis `name` is an identifier ([A-Za-z_][A-Za-z0-9_]*) unique within the sweep, and every axis carries a non-empty `values` list of scalars (numbers, strings or booleans). `mode` is `grid` (the default), `zip`, `random` or `lhs`, and zip needs every axis the same length (the sampling modes' own rules are PULSE_SWEEP_MODE_MISMATCH / _RANGE_INVALID / _SAMPLES_INVALID). `request` is the slot request as a JSON object with axis placeholders; `overlays`, when set, is a JSON array. A `rank` needs a non-empty `by`, an `order` of `asc` or `desc`, and a `top` of at least 1 when set. Every axis must be named by a `{{axis}}` or `{\"$var\": \"axis\"}` placeholder in the request, the `label` pattern or the overlays, and every placeholder must name an axis. Each substituted request is decoded strictly — an unknown key (under `unknown_field`) or a mistyped value is refused, naming the slot under `slot` and its axis `values` — and must not set `label` (the sweep `label` pattern labels each slot, and may not render empty). The offending path is under `field`, the rule under `reason`, the axis name under `axis` and, where one applies, the bad value under `value` and the accepted values under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"sweep", "axes", "*", "values"},
				Hint:   "Give every axis a unique identifier `name` and at least one scalar value; under `mode: zip` give every axis the same number of values.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"sweep", "mode"},
				Hint:     "Set `mode` to one of the values under `valid`, or drop it for grid.",
				Examples: []any{"grid", "zip", "random", "lhs"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"sweep", "request"},
				Hint:   "Reference every axis with `{{axis}}` (inside a string) or `{\"$var\": \"axis\"}` (a whole value), name only declared axes, correct the key under `unknown_field`, and move any `label` into sweep.label.",
			},
		},
	},
	PULSE_BANNER_INVALID: {
		Message: "The ComposedRequest `banner` block is malformed, so nothing runs — predict refuses it identically. A banner needs a base `request` (the cohort, weight, filterers and the shared crosstab rows / cell / margins / normalize) that sets no `label` (reason `request_label_set`: slots are labelled `<section>:<group>`), no crosstab `columns` (reason `columns_set`: the groups own the columns) and no crosstab shape `long` (reason `shape_long`: the stitch reads the matrix). It needs at least one section (`sections_empty`) and at least one group or `total: true` (`groups_empty`). Every section and group label is non-empty (`label_empty`) and unique among its kind (`label_duplicate`); with the Total block no group may be labelled `Total` (`label_reserved`). Every group needs at least one column grouper (`columns_empty`). The offending path is under `field`, the rule under `reason` and the label under `label` where one applies.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"banner", "request", "crosstab", "columns"},
				Hint:   "Move the column groupers into banner.groups[*].columns, drop `label` from the base request and keep the crosstab in matrix shape.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"banner", "groups", "*", "label"},
				Hint:   "Give every section and group a non-empty label unique among its kind, at least one column grouper per group, and at least one group or `total: true`.",
			},
		},
	},
	PULSE_FIT_INVALID: {
		Message: "A ComposedRequest `fits` entry is malformed, so nothing runs — predict refuses it identically. Every fit needs a non-empty `name` and a `request` holding exactly ONE regression (reason `regression_count`, the count under `count`) and no `groups` or `crosstab` (reason `grouped`): a fit is one model over one row set, fit once before any slot. A missing name is `name_missing`, a missing request `request_missing`. The entry index is under `fit`, its name under `name`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"fits", "*", "request", "regressions"},
				Hint:   "Give the fit request exactly one regression; fit a second model as its own `fits` entry with its own name.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"fits", "*", "request", "groups"},
				Hint:   "Drop `groups` / `crosstab` from the fit request — a fit is one ungrouped model. Fit one model per subset with a filter in each fit request instead.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"fits", "*", "request", "matrices", "*", "params", "estimator"},
				Hint:   "Under `details.reason` `sem_ordinal`, a SEM fit binds a maximum-likelihood MAT_SEM only: drop the ordinal `estimator` (and `ordered`) from the fit request, or run the DWLS / WLSMV fit as a plain `matrices` entry of a slot.",
			},
		},
	},
	PULSE_MODEL_NAME_COLLISION: {
		Message: "Two bound models share a name: `fits` entries, `models` entries and `populations` entries bind ONE namespace, and every name must be unique in it, because a reference names a model by that name alone. The instance's registered populations (Options.Extensions.Populations, the populations directory) are bound beneath every request in the same namespace, so an entry may not reuse one of their names either. The name is under `name`; two fits name the second entry's index under `fit`, any collision with a `models` or `populations` entry or a registered population names both bindings under `sources` (each {slot, index} plus a loaded file's path; a registered population is {slot: \"registered\"}).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"fits", "*", "name"},
				Hint:   "Rename one of the colliding fits and update the references to it.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"models", "*", "name"},
				Hint:   "Load the model under a name no fit or other entry uses, and update the references to it.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"populations", "*", "name"},
				Hint:   "Load the population under a name no fit, model or other entry uses, and update the params.fit references to it.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "When `sources` names a registered population, reference it with params.fit directly — it is already bound — instead of loading or fitting the name again.",
			},
		},
	},
	PULSE_MODEL_UNKNOWN: {
		Message: "A reference names a model nothing binds in this request. Names resolve against the ComposedRequest `fits` entries, the request's `models` and `populations` entries and the instance's registered populations (one namespace). The unknown name is under `name`, the bound names of the kind the reference reads under `available`: a vector-fit reference (a fitted vector attribute's or a matrix slot's `params.fit`) lists the bound vector fits, a regression reference (a regression score or coef()) the bound regressions.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"fits", "*", "name"},
				Hint:   "Reference one of the names under `available`, or add a `fits` entry with that name.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"populations"},
				Hint:   "To score against a saved vector fit, load its population file under that name: `populations: [{name, path}]`.",
			},
		},
	},
	PULSE_MODEL_KIND_MISMATCH: {
		Message: "A reference names a bound fit of the other kind. One namespace holds regression fits (and loaded model files) and vector fits: a regression score and coef() read a regression; a fitted vector attribute's or a matrix slot's `params.fit` reads a vector fit (a `fits` entry over one matrix or fitted-attribute battery, or a loaded `populations` entry). The name is under `name` / `fit`, its kind under `kind`, the kind the reference reads under `expected`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"fits", "*", "name"},
				Hint:   "Reference a fit of the expected kind, or add a `fits` entry of that kind under a new name.",
			},
		},
	},
	PULSE_MODEL_SCORE_INVALID: {
		Message: "A scoring slot's parameters do not fit, so nothing runs — predict refuses it identically. A per-row model score takes params `{fit, terms?, scale?, bound?}`: `fit` names a bound model (required, reason `fit_missing`); `terms` is a non-empty list of distinct model terms (`terms_empty`, `term_duplicate`); `scale` is `response` (default) or `link` (`scale_unknown`) and applies to a generalized-linear fit only (`scale_not_glm`); `bound` is `point` (default), `lower` or `upper` (`bound_unknown`), and a `lower` / `upper` bound needs `terms` to list exactly one predictor (`bound_terms`) — it is that term's coefficient-interval contribution, not a prediction interval. A fitted-value or leverage attribute's `params.fit` takes `{fit}`, a residual's `{fit, kind, residual_type}` (`residual_type` beside kind `standardized` only), an influence attribute's `{fit, measure}`; a leverage, influence or standardized / studentized residual slot reads the bound fit's design, so the fit must be an unweighted, unpenalized least-squares or generalized-linear fit with `vcov: true` and the classical covariance, no selection, resample or bounds (`params_invalid`, what is missing under `requires`; any other type is `type_unsupported`). Either way the slot takes no `field`, `target`, `predictors` or `penalty` (nor, with `params.fit`, `alpha` / `l1_ratio`) — the bound model supplies them (`slot_fields`). Only a least-squares, generalized-linear or Bayesian-linear fit scores (`type_unsupported`; the regression type is under `type`), and an extension regression's fit only when its registration supplies a Scorer (`no_scorer`). Any other params shape is `params_invalid`. The rule is under `reason`, the slot's operator under `attribute`, the fit under `fit`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "fit"},
				Hint:   "Set params.fit to the name of a `fits` entry, and drop target / predictors / penalty from the slot.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"attributes", "*", "params", "scale"},
				Hint:   "Drop params.scale unless the fit is generalized-linear; a least-squares or Bayesian score is already on the target's scale.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"fits", "*", "request", "regressions", "*", "vcov"},
				Hint:   "For a leverage / influence / standardized / studentized slot (details.requires vcov), set vcov: true on the bound fit's regression; for requires unweighted set its weight to null.",
			},
		},
	},
	PULSE_MODEL_TERM_UNKNOWN: {
		Message: "A reference names a term the bound model does not have. A scoring attribute's params.terms and the coef() term argument may name the fit's predictors (as the fit request wrote them) and `(intercept)`. The fit is under `fit`, the term under `term`, the terms it may name under `available`. A sweep_axis(fit, axis) call raises it too, with `reason`: `not_sweep_bound` (the fit has no `from_sweep`, so no winning axes) or `axis_unknown` (the sweep has no such axis; its axes under `axes`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "terms"},
				Hint:   "List only names under `available`; add `(intercept)` to include the intercept.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Pass coef() a term under `available` as its second argument.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Pass sweep_axis() a fit bound with `from_sweep` and an axis under `axes`.",
			},
		},
	},
	PULSE_MODEL_STAT_UNKNOWN: {
		Message: "A coef(fit, term, stat) call names a stat it cannot read. The stat is `point` (the coefficient, the default when omitted), `lower` / `upper` (its confidence bounds at the fit's conf_level) or `se` (its standard error). An all-literal call is refused by predict; a computed one when it evaluates. The fit is under `fit`, the stat under `stat`, the accepted stats under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Pass one of `valid` as coef()'s third argument, or drop it for the coefficient.",
			},
		},
	},
	PULSE_MODEL_INTERVALS_UNAVAILABLE: {
		Message: "A reference reads a coefficient interval or standard error from a bound model that reports none (a degenerate fit, such as an intercept-only generalized-linear model, carries no standard errors and so no intervals): a coef() stat `lower` / `upper` / `se`, or a scoring attribute's `bound` `lower` / `upper`. Raised when the fit is bound — predict cannot see a fit's numbers. A single term's missing figure (a lasso-dropped predictor's interval) is not this refusal: it reads as null. The fit is under `fit`, the setting under `stat` or `bound`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "bound"},
				Hint:   "Score the point coefficient (drop params.bound), or fit a model with predictors so it reports intervals.",
			},
		},
	},
	PULSE_FORMULA_TOTAL_SITE: {
		Message: "A total(expr) call appears outside a formula attribute. total() is the formula attribute's cohort total — the sum of a row-level expression over the rows that pass the filters, computed in a pre-pass — and no other expression site (an expression filter, an overlay formula) runs that pass. The site is under `site`, the expression under `expression`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Compute the cohort-total term in an ATTR_FORMULA attribute and read its label downstream.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"filterers", "*", "expression"},
				Hint:   "Remove the total() call from the filter expression.",
			},
		},
	},
	PULSE_FORMULA_TOTAL_NESTED: {
		Message: "A formula attribute's total(expr) call has another total() inside its argument. A total's argument is a row-level expression, summed over the rows that pass the filters; a total of totals is refused. The offending call is under `call`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Move the inner total() out of the argument (a total is a constant, so total(x * total(y)) is total(x) * total(y)).",
			},
		},
	},
	PULSE_FORMULA_TOTAL_ARITY: {
		Message: "A formula attribute's total() call does not take exactly one argument. total(expr) sums one row-level expression over the rows that pass the filters. The offending call is under `call`, its argument count under `arguments`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Pass total() exactly one row-level expression, such as total(spend) or total(week >= 52 ? spend : 0).",
			},
		},
	},
	PULSE_FORMULA_COMPILE_ERROR: {
		Message: "A formula or filter expression (or a total() argument) does not compile: a syntax error, an unknown name or function, or an operation the operand types do not support — a categorical field binds as its string label, a decimal128 as an exact decimal, a set as its label list, every other field as a float64, a null as nil. The compile runs once, at build, or on the first record when the expression names a feature output or an earlier attribute label. The message carries expr-lang's reason; details.compile_error holds the reason (with its line:column) and details.expression the source.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"attributes", "*", "expression"},
				Hint:     "Read details.compile_error for the offending token or operation, then fix the formula: balance parentheses, spell field names as the schema does, compare a categorical with a quoted label (region == \"west\"), and call only the documented functions (skills/expression-language.md).",
				Examples: []any{map[string]any{"expression": "(revenue - cost) / revenue"}, map[string]any{"expression": "region == \"west\" ? 1 : 0"}},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"filterers", "*", "expression"},
				Hint:     "Read details.compile_error and fix the filter expression the same way; a filter must return a bool.",
				Examples: []any{map[string]any{"expression": "age >= 18 && region in [\"west\", \"east\"]"}},
			},
		},
	},
	PULSE_PARAMS_INVALID: {
		Message: "A request `params` entry is not a parameter. A parameter is a number (kept exact: an integer binds as an integer, so a large id compares exactly), a string, a bool, or a list of ONE of those kinds; it is never null, and its name is an identifier so `params.<name>` can spell it. The entry is under `param`, the failure under `reason`: null_value, invalid_name, mixed_list or unsupported_value (an object, a nested list, a non-finite number).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"params", "*"},
				Hint:     "Give the parameter a value: a number, string, bool or a one-kind list. To leave a parameter unset, drop its entry (an expression reading it is then refused as undeclared) rather than writing null.",
				Examples: []any{map[string]any{"params": map[string]any{"from_wave": 3, "waves": []any{3, 4, 5}, "region": "west"}}},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"params"},
				Hint:   "Rename an entry whose name is not an identifier (start with a letter or underscore; letters, digits and underscores only), and update every params.<name> that reads it.",
			},
		},
	},
	PULSE_PARAMS_UNDECLARED: {
		Message: "An expression reads `params.<name>` for a name the request's `params` object does not declare. Parameters are declared on the request (`\"params\": {\"<name>\": value}`) and read read-only in every expression of that request (formulas, filter expressions, total() arguments, a regression holdout, overlay formulas). The expression is under `expression`, the missing name under `param`, the declared names under `declared`.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"params"},
				Hint:     "Declare the parameter on the request, or correct the spelling in the expression to one of `declared`.",
				Examples: []any{map[string]any{"params": map[string]any{"from_wave": 3}}},
			},
		},
	},
	PULSE_PARAMS_FIELD_COLLISION: {
		Message: "An expression reads `params.<name>`, but `params` is also a column the expression can read: a schema field, a derived column (feature output, attribute label) or a named vector called params. A bare identifier always reads the column, so the request-parameter namespace is shadowed on this cohort and the request is refused rather than guessing. The column itself stays readable as bare `params` or `$env[\"params\"]`; there is no spelling that reaches the parameters past it.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Path:   []string{"cohort"},
				Hint:   "Rename the cohort field called params (re-import with a different column name), then read the parameters as params.<name>.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "label"},
				Hint:   "When the column is a derived one (an attribute label, a feature output or a vector named params), rename it in the request.",
			},
		},
	},
	PULSE_MODEL_PREDICTOR_MISSING: {
		Message: "The scoring slot has no column for one of the bound model's predictors: no schema field, feature output or earlier attribute label carries the name. Scoring reads the model's predictors from the slot's own columns and never infers one — a one-hot reference level is an ABSENT predictor of the fit, not a missing column. The predictor is under `field`, the fit under `fit`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"features"},
				Hint:   "Repeat in the scoring slot the features that built the fit's predictor columns (e.g. the same FEAT_LOG / FEAT_ONE_HOT).",
			},
		},
	},
	PULSE_MODEL_PREDICTOR_NOT_NUMERIC: {
		Message: "A bound model's predictor column is a schema field of a type a regression cannot read on the scoring frame (a categorical or a set), so it cannot enter a linear predictor. The predictor is under `field`, its type under `field_type`, the fit under `fit`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"features"},
				Hint:   "Score against a numeric column: build the fit and the scoring slot from the same numeric feature output (e.g. FEAT_ONE_HOT indicators for a categorical).",
			},
		},
	},
	PULSE_MODEL_SAVE_INVALID: {
		Message: "A `save` path (on a regression or a `fits` entry) cannot name its file, so nothing runs — predict refuses it identically. A save path is relative to the instance filesystem root, names a file ending in `.pulse-model.json` (a regression or regression fit: a model file) `.pulse-population.json` (a vector fit: a population file) or `.pulse-sem.json` (a SEM fit: a SEM fit file) with a name before the suffix, and never climbs above the root with `..`; one request or Compose batch never saves two fits to the same file (reason `duplicate`, the two writers under `sites`). Every path in a batch is judged before any fit or slot runs. The path is under `path`, the rule under `reason` (`path_empty`, `suffix`, `name_missing`, `escapes_root`, `duplicate`), the offending regression's index under `regression`, the fit's under `fit` and a batch slot's under `request`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"regressions", "*", "save"},
				Hint:   "Name the model file under the data root with the `.pulse-model.json` suffix, e.g. `models/mmm.pulse-model.json`.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"fits", "*", "save"},
				Hint:   "Give the fit's `save` a path under the data root: `.pulse-model.json` for a regression fit, `.pulse-population.json` for a vector fit (e.g. `pops/brand.pulse-population.json`), `.pulse-sem.json` for a SEM fit.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"regressions", "*", "save"},
				Hint:   "For `duplicate`, give each saving regression or fit its own file (e.g. one per fit name), or drop the extra `save`.",
			},
		},
	},
	PULSE_MODEL_WRITE_FAILED: {
		Message: "Writing a model or population file failed on the filesystem: creating its directory, the temp file, writing, syncing or renaming it over the target (the stage is under `stage`, the file under `path`, the model or population under `name`). The write is atomic, so the target keeps its previous content or stays absent; the fit itself succeeded.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"regressions", "*", "save"},
				Hint:   "Save to a writable location under the data root (check permissions and free space), or drop `save` to run the fit without persisting it.",
			},
		},
	},
	PULSE_MODEL_OVERWRITTEN: {
		Message: "A model or population file save replaced an existing file at its path (under `path`; the model or population under `name`, a fit's index under `fit`). The save succeeded and the new file is complete; the warning exists so a replaced model or population is never silent.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"regressions", "*", "save"},
				Hint:   "No action needed if replacing the model was intended. To keep both, save under a new path (e.g. a dated file name).",
			},
		},
	},
	PULSE_MODEL_FILE_INVALID: {
		Message: "A model, population or SEM fit file cannot be parsed: it is not JSON (reason `json`), has no `model_format_version` / `population_format_version` / `sem_format_version` (`version_missing`), or its document has an unknown key, a mistyped value or a structural fault — a predictor without a coefficient; a population whose labels, means or co-moment do not match in shape, whose co-moment is not symmetric, or whose counts or weight sums are impossible; a SEM fit whose matrices do not match its observed and inner variables or whose model does not re-specify them (`schema`). The file is under `path`; a `models` entry loading it adds its index under `model`, a `populations` entry under `population`, and either its name under `name`; a registered population (Options.Extensions.Populations or the populations directory, refused at pulse.New) carries its registered name under `registered`.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-save the model or population from its fit with `save`; these files are written by Pulse and should not be hand-edited.",
			},
		},
	},
	PULSE_MODEL_VERSION_UNSUPPORTED: {
		Message: "A model file's `model_format_version` (or a population file's `population_format_version`) is not one this build reads (the file under `path`, its version under `version`, the readable ones under `supported`; a registered population's name under `registered`).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Re-save the model or population with this build (rerun the fit with `save`), or load it with the Pulse version that wrote it.",
			},
		},
	},
	PULSE_MODEL_REGRESSION_UNREGISTERED: {
		Message: "A `models` entry loads a model file saved from an extension regression's fit (its type under `type`) that this instance does not register, or registers but hides under its feature profile — without the registration nothing can score the model, so nothing runs; predict refuses it identically. The entry's index is under `model`, its name under `name`, the file under `path`.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Load the model on an instance that registers its regression type (pulse.Options.Extensions.Regressions, with a Scorer) and whose feature profile enables it, or drop the `models` entry.",
			},
		},
	},
	PULSE_MODEL_REF_INVALID: {
		Message: "A `models` or `populations` entry cannot name its file, so nothing runs — predict refuses it identically. An entry needs a `name` (the name its references use) and a `path` under the instance filesystem root ending in `.pulse-model.json` or `.pulse-sem.json` (a `models` entry: the suffix picks a model file or a SEM fit file) or `.pulse-population.json` (a `populations` entry), with a file name before the suffix and no `..` above the root. The entry's index is under `model` or `population`, the rule under `reason` (`name_empty`, `path_empty`, `suffix`, `name_missing`, `escapes_root`). A blank Options.Extensions.Populations key is refused at pulse.New the same way (reason `name_empty`, under `registered`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"models", "*", "path"},
				Hint:   "Point `path` at the file a `save` wrote, e.g. `models/mmm.pulse-model.json` or a SEM fit's `fits/brand.pulse-sem.json`.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"models", "*", "name"},
				Hint:   "Give the entry the name its scoring references use.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"populations", "*", "path"},
				Hint:   "Point `path` at the population file a vector fit's `save` wrote, e.g. `pops/brand.pulse-population.json`.",
			},
		},
	},
	PULSE_MODEL_FILE_UNREADABLE: {
		Message: "A `models` or `populations` entry's file cannot be read: it does not exist (reason `missing`) or reading it failed — a directory, permissions or an I/O fault (`unreadable`). Every model and population file is read before any record is read (in a batch or chain, before anything runs), so nothing ran. The file is under `path`, the entry's index under `model` or `population` and its name under `name`; a populations-directory file that cannot be read fails pulse.New, its registered name under `registered`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"models", "*", "path"},
				Hint:   "Point `path` at an existing model file under the data root (a regression or fit `save` writes one).",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"populations", "*", "path"},
				Hint:   "Point `path` at an existing population file under the data root (a vector fit's `save` writes one).",
			},
		},
	},
	PULSE_FOR_EACH_INVALID: {
		Message: "A `for_each` on a window, feature or attribute item is malformed, so nothing runs — predict refuses it identically. A for_each needs at least one axis; every axis `name` is an identifier ([A-Za-z_][A-Za-z0-9_]*) unique within it, and every axis carries a non-empty `values` list of scalars (numbers, strings or booleans). `mode` is `grid` (the default) or `zip` — the sampling modes `random` / `lhs` and a `range` axis are compose-`sweep` only (reasons `mode_unknown` / `range_unsupported`) — and zip needs every axis the same length. Every axis must be named by a `{{axis}}` (inside a string such as `label` or `field`) or `{\"$var\": \"axis\"}` (a whole value inside `params`) placeholder in the item, and every placeholder must name an axis. Each substituted item is decoded strictly — an unknown key (under `unknown_field`) or a mistyped value is refused, naming the combination under `index` and its axis `values`. A for_each inside a compose `sweep` body is refused (reason `nested_in_sweep`): fan the item out with the sweep's own axes, or move it to an explicit request. The offending path is under `field`, the rule under `reason`, the axis name under `axis` and, where one applies, the bad value under `value` and the accepted values under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"windows", "*", "for_each", "axes"},
				Hint:   "Give every axis a unique identifier `name` and at least one scalar value; under `mode: zip` give every axis the same number of values.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"windows", "*", "for_each", "mode"},
				Hint:     "Set `mode` to one of the values under `valid`, or drop it for grid.",
				Examples: []any{"grid", "zip"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"windows", "*", "label"},
				Hint:   "Reference every axis with `{{axis}}` in a string (label, field) or `{\"$var\": \"axis\"}` inside params, name only declared axes, and correct the key under `unknown_field`. A for_each item works the same under `features` and `attributes`.",
			},
		},
	},
	PULSE_SWEEP_EXPR_SPLICE: {
		Message: "The ComposedRequest `sweep` places an axis placeholder `{{name}}` inside an expression string of its request or overlays (a filterer or attribute expression, a regression holdout, an overlay formula), so nothing runs — predict refuses it identically. Splicing an axis value's text into an expression would let the value rewrite the expression's logic, so a sweep never substitutes there. The path is under `field`, the placeholder under `placeholder` and the expression slot under `slot`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"sweep", "request", "params"},
				Hint:   "Carry the axis value through the request's params instead: add `\"params\": {\"x\": {\"$var\": \"x\"}}` to the sweep request (the marker keeps the value's type) and read it in the expression as `params.x`. To sweep whole expressions, give the axis the expression strings as values and supply the slot with a `{\"$var\": \"x\"}` marker.",
				Examples: []any{map[string]any{
					"axes": []any{map[string]any{"name": "w", "values": []any{1, 2}}},
					"request": map[string]any{
						"params":    map[string]any{"w": map[string]any{"$var": "w"}},
						"filterers": []any{map[string]any{"type": "FILTER_EXPRESSION", "expression": "wave == params.w"}},
					},
				}},
			},
		},
	},
	PULSE_FOR_EACH_EXPR_SPLICE: {
		Message: "A window, feature or attribute item carrying a `for_each` places an axis placeholder `{{name}}` inside an expression string (an attribute expression, or a params-borne fit or centroid filter expression), so nothing runs — predict refuses it identically. Splicing an axis value's text into an expression would let the value rewrite the expression's logic, so an item fan-out never substitutes there. The path is under `field`, the placeholder under `placeholder` and the expression slot under `slot`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Vary the value with a Compose `sweep` instead: its request carries `\"params\": {\"x\": {\"$var\": \"x\"}}` (the marker keeps the value's type) and the expression reads `params.x`, one slot per axis value. For an expression inside an item's params (a fit_on), an axis may instead supply the whole expression with a `{\"$var\": \"x\"}` marker whose values are the complete expression strings.",
				Examples: []any{map[string]any{
					"sweep": map[string]any{
						"axes": []any{map[string]any{"name": "k", "values": []any{2, 3}}},
						"request": map[string]any{
							"params":     map[string]any{"k": map[string]any{"$var": "k"}},
							"attributes": []any{map[string]any{"type": "ATTR_FORMULA", "label": "x_by_k", "expression": "week * params.k"}},
						},
					},
				}},
			},
		},
	},
	PULSE_SWEEP_MODE_MISMATCH: {
		Message: "The ComposedRequest `sweep` mixes enumeration and sampling, so nothing runs — predict refuses it identically. Modes `grid` and `zip` walk each axis's explicit `values` list; modes `random` and `lhs` draw `samples` slots, each axis from a continuous `range` {min, max, scale}. An axis never mixes the two: a `range` axis under grid / zip (reason `range_under_enumeration`), a `values` axis under random / lhs (`values_under_sampling`), or an axis setting both (`values_and_range`) is refused, and so are `samples` / `rng_seed` under grid / zip (`samples_under_enumeration` / `seed_under_enumeration`). The offending path is under `field`, the rule under `reason`, the mode under `mode` and the axis under `axis` where one is involved.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"sweep", "mode"},
				Hint:     "Use `random` or `lhs` (with `samples`) to sample range axes, or `grid` / `zip` to walk value lists.",
				Examples: []any{"lhs", "random", "grid"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"sweep", "axes", "*", "range"},
				Hint:     "Under random / lhs give every axis a `range` {\"min\", \"max\"} and no `values`; under grid / zip give every axis `values` and no `range`.",
				Examples: []any{map[string]any{"name": "decay", "range": map[string]any{"min": 0.0, "max": 0.9}}},
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"sweep", "samples"},
				Hint:   "Drop `samples` and `rng_seed` from a grid / zip sweep — its slot count is the axis product (grid) or the common axis length (zip), and it draws nothing.",
			},
		},
	},
	PULSE_SWEEP_RANGE_INVALID: {
		Message: "A sampled sweep axis's `range` cannot be drawn from, so nothing runs — predict refuses it identically. Under mode random / lhs every axis needs a `range` (reason `range_missing`) with finite `min` and `max` (`bound_missing`, `bound_not_finite`), `min < max` (`range_empty`), a `scale` of `linear` (the default) or `log` (`scale_unknown`), and `min > 0` under scale log (`log_min_not_positive`). The offending path is under `field`, the rule under `reason`, the axis under `axis` and the bounds under `min` / `max` where they apply.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"sweep", "axes", "*", "range"},
				Hint:     "Set finite bounds with `min` below `max`; under `scale: log` make `min` positive (a log-uniform draw needs log min).",
				Examples: []any{map[string]any{"min": 0.0001, "max": 10.0, "scale": "log"}},
			},
		},
	},
	PULSE_SWEEP_SAMPLES_INVALID: {
		Message: "A sampling sweep (mode `random` or `lhs`) has no usable `samples`, so nothing runs — predict refuses it identically. `samples` is the number of slots drawn and must be at least 1 (reason `samples_missing` when absent, `samples_invalid` when below 1). The drawn slots, plus the explicit ones, still count against MaxComposeSlots (default 1000; PULSE_LIMIT_EXCEEDED). The path is under `field`, the rule under `reason`, the mode under `mode` and the value under `value`.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"sweep", "samples"},
				Hint:     "Set `samples` to the number of slots to draw — at least 1, and within MaxComposeSlots together with the explicit requests (raise Options.Limits.MaxComposeSlots deliberately for a larger search).",
				Examples: []any{64, 256},
			},
		},
	},
	PULSE_SWEEP_RANK_PATH: {
		Message: "The sweep `rank.by` path does not parse, or does not resolve to one number in a slot's response. The rank reads each sweep slot's unshaped response JSON at that path: dotted segments, where a segment on an object is a key and a segment on a list selects the one element whose `name` or `label` equals it (never an index). The batch is refused when the path ends on a non-number, is ambiguous (two elements match, or the match's name and label differ) or resolves in no sweep slot (reason `syntax`, `not_number`, `ambiguous` or `missing`). As a WARNING on a slot's own response it means that slot is left out of the ranking: its value is null (undefined) or the path is missing there only. The path is under `by`, the slot under `label`, the failing segment under `segment`. As the `error` of a sweep-bound fit (`fits[].from_sweep`, reason `empty`) it means no sweep slot was ranked, so the fit binds failed and readers see PULSE_FIT_UNAVAILABLE.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"sweep", "rank", "by"},
				Hint:   "Point `by` at a single numeric value every sweep slot's response carries, e.g. a regression statistic or one aggregate cell.",
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
			{
				Action: FixupReplaceField,
				Path:   []string{"Joins", "*", "On"},
				Hint:   "When `details.reason` is \"set_key\" or \"vector_key\", the key is a set_* or vec_f32 / vec_f64 column, which has no single equality value on either side: join on a scalar key column instead (the set or vector column still rides through the join).",
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
		Message: "A labeled-range set (the inline ranges of a date-range grouper or filterer, a numeric-range grouper or a materialize output column's bucket, or a named range table) was presented with zero ranges.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Supply at least one {label, start, end} range in the operator's Params (or the referenced range table). Each range needs a label; start/end may be omitted for an open bound.",
			},
		},
	},
	PULSE_RANGE_INVALID: {
		Message: "A labeled range is malformed: a date boundary is not a parseable date literal or a date range starts after it ends, or a numeric (half-open) range does not start strictly before it ends.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Write date boundaries as ISO dates (YYYY-MM-DD) and ensure start <= end; write numeric boundaries as JSON numbers with start < end ([start, end) is half-open). Omit or null a boundary to leave that side open (unbounded).",
			},
		},
	},
	PULSE_RANGE_DUPLICATE_LABEL: {
		Message: "Two ranges in the same labeled-range set share a Label; labels are bucket keys and must be unique within a set.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Rename one of the colliding ranges so every label in the set is distinct.",
			},
		},
	},
	PULSE_RANGE_OVERLAP: {
		Message: "Two ranges in the same labeled-range set overlap (date bounds are inclusive, numeric bounds half-open [start, end); open boundaries extend to -inf/+inf), so a record could map ambiguously to two labels.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Adjust the boundaries so ranges are disjoint. Date bounds are inclusive: make one range end the day before the next begins (e.g. ...-03-31 then ...-04-01). Numeric bounds are half-open: let one range end where the next begins (e.g. [0, 91) then [91, 104)). Only one range may have an open lower bound and one an open upper bound.",
			},
		},
	},
	PULSE_RANGE_SOURCE_AMBIGUOUS: {
		Message: "A range grouper or filterer (or a materialize output column's bucket) must name exactly one range source; supplying both an inline `ranges` array and a named `table` (`range_table` on a bucket), or neither, is ambiguous.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "In the operator's Params, provide exactly one of: an inline `ranges` array of {label, start, end} entries, or a `table` naming a registered range table. Remove one when both are present, or add one when neither is.",
			},
		},
	},
	PULSE_RANGE_TABLE_UNKNOWN: {
		Message: "A range grouper or filterer (or a materialize output column's bucket.range_table) referenced a range table by a name that is not registered.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Set Params.table to a table registered via Options.Extensions.RangeTables or the PULSE_RANGE_TABLES_DIR loader, or register the referenced table before running the request.",
			},
		},
	},
	PULSE_RANGE_TABLE_KIND_MISMATCH: {
		Message: "A range operator (or a materialize output column's bucket) referenced a registered range table of the other kind: a table with ISO date bounds is a date table, one with JSON number bounds a numeric table. A bucket reads a numeric table on a numeric column and a date table on a date / datetime column.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Use GROUP_DATE_RANGES / FILTER_DATE_RANGES with a date table and GROUP_NUMERIC_RANGES with a numeric table; pulse_range_tables (Pulse.RangeTables) reports each table's kind. Otherwise point Params.table at a table of the operator's kind, or author the ranges inline.",
			},
		},
	},
	PULSE_LABEL_FIELD_UNKNOWN: {
		Message: "A LabelBinding references a field name not present in the cohort schema (on a convert, the schema convert resolves from the source). Every export format and convert refuse it before writing, rather than labelling nothing.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Field"},
				Hint:   "Use a field name reported by pulse_inspect: a categorical field, or on an export or convert a u4 / u8 / u16 / u32 code field.",
			},
		},
	},
	PULSE_LABEL_FIELD_NOT_CATEGORICAL: {
		Message: "A LabelBinding references a non-categorical field. Labels translate dictionary string values; numeric and date fields have no dictionary key to translate. An export (every format) or a convert also accepts an integer-coded u4 / u8 / u16 / u32 field.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Field"},
				Hint:   "Pick a categorical_u8 / categorical_u16 / categorical_u32 field (on an export or convert, a u4 / u8 / u16 / u32 code field too). For float, u64 or date translation, derive a categorical column upstream during import.",
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
		Message: "Two LabelBinding entries target the same Field. Each field may carry at most one binding per request, export or convert.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels"},
				Hint:   "Drop the duplicate entry; a single binding governs both replace and augment behaviour for one field.",
			},
		},
	},
	PULSE_LABEL_KEY_INVALID: {
		Message: "A label binding on an integer-coded field (u4 / u8 / u16 / u32, on any export format or a convert) uses a table key that is not the canonical decimal spelling of a value the field can store; the stored integers are the codes, so the key could never label a value.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Table"},
				Hint:   "Bind a table keyed by the field's stored integers in plain decimal (\"1\", \"7\" — no leading zeros, signs or fractions, within the type's range); details carry the offending key and the field's maximum.",
			},
		},
	},
	PULSE_LABEL_TABLE_NOT_ENUMERABLE: {
		Message: "A reverse label lookup, or a .sav value-label binding on an integer-coded field, needs the table's entries, but the table is function-driven (Lookup closure only, no static Rows map or Order).",
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
	PULSE_LABEL_NOT_APPLIED: {
		Message: "A label binding was not applied: its field is not an output column, axis or group key of the response, or it is an augment-mode binding on a matrix axis, whose keys have no sibling column.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Field"},
				Hint:   "Bind a field the request groups by (a grouper key, a crosstab row or column axis), or drop the binding.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels", "*", "Mode"},
				Hint:   "For a crosstab matrix axis use mode \"replace\" (the axis keys become the labels), or request shape \"long\" to keep augment-mode sibling columns.",
			},
		},
	},
	PULSE_CROSSTAB_EMPTY_ROWS: {
		Message: "The Crosstab section has no row-axis groupers. A crosstab requires at least one row grouper; the column axis is optional (absent columns yield one implicit \"Total\" column).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Rows"},
				Hint:   "Add at least one Group entry to Crosstab.Rows; for a single-axis aggregation use a plain Process request with Groups + Aggregations instead.",
			},
		},
	},
	PULSE_CROSSTAB_EMPTY_COLUMNS: {
		Message: "No longer raised: a Crosstab section with no column-axis groupers runs with one implicit \"Total\" column (U47). The code is retained so lookups of it keep resolving.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Columns"},
				Hint:   "Omit Crosstab.Columns for a single Total column holding every record whose row key resolved, or add Group entries for a real column axis.",
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
	PULSE_CROSSTAB_MARGIN_AGG_INVALID: {
		Message: "An entry in the Crosstab section's margin_aggregations slot is structurally malformed: either a null element or an entry with no aggregation type. Auxiliary margin aggregations carry the same shape as the cell aggregation (type + field, optional label + params).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "MarginAggregations", "*", "Type"},
				Hint:     "Give every margin_aggregations entry an aggregation type, or drop the entry. The canonical auxiliary base is AGG_DISTINCT_COUNT over the respondent key beside a weighted cell metric; AGG_COUNT and AGG_SUM are the other common choices.",
				Examples: []any{"AGG_DISTINCT_COUNT", "AGG_COUNT", "AGG_SUM"},
			},
		},
	},
	PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL: {
		Message: "Two margin_aggregations entries resolve to the same effective label, or one collides with the cell aggregation's label. The effective label is Label when set and TYPE_field otherwise; margin components are keyed by it, so the duplicate would overwrite the figure beside it.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "MarginAggregations", "*", "Label"},
				Hint:     "Set an explicit, distinct Label on each margin_aggregations entry. Two entries of the same type over the same field are indistinguishable without one, and the cell aggregation's own label is already taken.",
				Examples: []any{"unweighted_base", "weighted_base"},
			},
		},
	},
	PULSE_CROSSTAB_CELLS_INVALID: {
		Message: "The Crosstab.Cells slot is malformed: Cell and Cells are both set, an entry is null, or an entry has no aggregation type and smart defaults could not infer one from its field. Cells is the multi-measure form of Cell — each entry is one measure, rendered as a stub row.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Cells", "*", "Type"},
				Hint:     "Give every Cells entry an aggregation Type (or a field whose schema type infers one), drop null entries, and set either Cell (one measure) or Cells (several), never both.",
				Examples: []any{"AGG_AVERAGE", "AGG_COUNT", "AGG_SUM"},
			},
		},
	},
	PULSE_CROSSTAB_CELLS_DUPLICATE_LABEL: {
		Message: "Two Crosstab.Cells entries resolve to the same effective label (Label when set, otherwise TYPE_field). Each measure is addressed by its label on every row key, so the two stub rows would be indistinguishable.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Cells", "*", "Label"},
				Hint:     "Set an explicit, distinct Label on each Cells entry; two entries of the same type over the same field need one.",
				Examples: []any{"mean_score", "top2_share"},
			},
		},
	},
	PULSE_CROSSTAB_MARGIN_AGG_UNOBSERVED: {
		Message: "margin_aggregations were declared on a Crosstab section that displays no margin (margins.rows / margins.columns / margins.grand are all false). Auxiliary aggregations are evaluated into the row / column / grand margin accumulators only, and are emitted only where that margin is displayed, so nothing carries them back. A normalize direction does not carry them either: the margin it requires is a normalization denominator, and an auxiliary is never a denominator. The request is legal and runs; the figures are accumulated and then discarded.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Crosstab", "Margins"},
				Hint:     "Turn on the margin that should carry the auxiliary figure (margins.rows / margins.columns / margins.grand), or drop margin_aggregations. Setting a normalize direction is NOT enough — it computes its implied margin as a denominator and emits no auxiliary figure there.",
				Examples: []any{map[string]any{"columns": true}, map[string]any{"rows": true, "grand": true}},
			},
		},
	},
	PULSE_CROSSTAB_NORMALIZE_MAP_VALUED: {
		Message: "The Crosstab section requested a normalize mode (row / column / total) on a cell aggregator whose output is map-valued (a set-frequency aggregator emits map[string]int per cell). Dividing one map by another is undefined; the normalize directive cannot be applied.",
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
		Message: "The request JSON contains a key that is not a recognised slot of the object it sits in — at the request root or at any depth (a sort entry, an aggregation, a crosstab cell, a Compose slot, a chain stage, a sweep body, a for_each item). Every JSON request root (CLI request files, MCP tool arguments, Compose sweep bodies and for_each items) is decoded strictly, so the key is refused rather than ignored: an ignored key would silently drop the intended operation. A slot this instance's feature profile hides is refused the same way and is never listed as valid. A common cause is using a manifest operator-catalog field name (\"groupers\", \"aggregators\") as the request key instead of the request slot name (\"groups\", \"aggregations\"). Below the root `details.path` locates the first offending key (\"sort[0].direction\", \"aggregations[0].weight\") and `valid_keys` lists that object's keys; `details.request` / `fit` / `stage` (Compose slot, fit, chain stage) or `slot` (sweep) / `index` (for_each combination) say which nested request it is in. Synth documents decode the same way: a spec, a `--rules` file (its file in `details.file`) or a profile document refuses an unknown key, and a spec field's `params` refuses a key its distribution does not read (`fields[0].params.rate` → `lambda`); correlation entries are `{a, b, rho}`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Hint:     "Rename the offending key to its canonical request slot: the error details carry the nearest valid key under suggestions and every slot this instance accepts under valid_keys. The manifest's \"groupers\"/\"aggregators\" name the available OPERATORS; the request slots that hold them are \"groups\"/\"aggregations\".",
				Examples: []any{"groupers -> groups", "aggregators -> aggregations", "correlations[0].correlation -> rho (synth spec)"},
			},
		},
	},
	PULSE_REQUEST_PARAM_INVALID: {
		Message: "A slot's params object on an embedder extension operator that declares its params carried a value of the wrong JSON type, omitted a param the registration marks Required, or carried none or more than one member of a declared ExactlyOneOf group. Every extension category (aggregator, attribute, filterer, grouper, window, feature, test, matrix, regression) checks a declared operator's params against its manifest entry before any operator code runs, so predict refuses exactly what execution would. `details.path` locates the param (\"aggregations[0].params.scale\"), `details.reason` is \"wrong_type\" (with `expected` and `got` JSON types; a null value is the wrong type), \"missing_required\" or \"exactly_one_of\" (path `<slot>.params`, with `group`, its `members` and the members `present`), checked in that order after unknown keys, and `details.operator` / `param` name it. A declared Default is never filled in: the factory applies it.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Hint:     "Caller: supply the param at details.path as a JSON value of the type in details.expected (the operator's manifest entry, extensions.<category>[].params, lists every param with its json_type and required flag). Embedder: if the value is legitimately of another type, widen the registration's ParamMeta.JSONType.",
				Examples: []any{map[string]any{"params": map[string]any{"scale": 2}}},
			},
			{
				Action:   FixupReplaceField,
				Hint:     "reason exactly_one_of: supply exactly one of details.members in the params object — drop all but one of details.present, or add one when it is empty (the manifest marks each member with exactly_one_of).",
				Examples: []any{map[string]any{"params": map[string]any{"span": 7}}},
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
		Message: "An OverlaySpec's Ref does not match the host shape required by the chosen Kind. The index-vs-margin overlay, for example, requires a Ref.Margin pointer with a known MarginAxis (row / column / grand) AND a MATRIX-shaped host (Request.Crosstab non-nil).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Ref", "Margin"},
				Hint:     "Set Ref.Margin = {Axis: \"row\" | \"column\" | \"grand\"} for OVERLAY_INDEX_VS_MARGIN; add Request.Crosstab to provide a MATRIX host so the margin slot has a denominator.",
				Examples: []any{"row", "column", "grand"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Groups"},
				Hint:   "OVERLAY_YOY needs EXACTLY ONE GROUP_DATE grouper (details.date_groupers counts those sent, details.groupers every group; a lone non-date grouper names itself in details.host_grouper). Add the GROUP_DATE grouper, or drop the extra one; the other keys may stay — YoY runs once per combination of them.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Kind"},
				Hint:     "When details.hosts is present the kind runs on another overlay slot: details.host is the slot it was sent on (request | compose | chain | facet), details.hosts every slot that runs it. Move the spec there (a Compose kind such as OVERLAY_T_VS_REF goes on ComposedRequest.Overlays, comparing two slots; a stage kind on ChainRequest.Overlays; a population kind on FacetRequest.Overlays), or pick a kind this slot runs.",
				Examples: []any{"compose", "chain", "facet", "request"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Kind"},
				Hint:     "When details.shapes is present the kind runs on Request.Overlays but not over this Process host shape: details.shape is the host the request makes (matrix = a crosstab; series = groups + aggregations, no crosstab), details.shapes every shape the kind runs on. Pick a kind for this shape (a share or margin kind on a crosstab, a prior / baseline / total kind on a grouped request), or reshape the request. A series OVERLAY_FORMULA runs on ComposedRequest.Overlays. On ComposedRequest.Overlays a two-sample t / z vs-ref kind over a crosstab slot (details.slot) carries the same keys: compare grouped slots, or use its per-cell twin over the crosstab slots.",
				Examples: []any{"matrix", "series"},
			},
		},
	},
	PULSE_OVERLAY_COMPONENTS_REQUIRED: {
		Message: "An overlay kind that reads per-cell components (n / weight sums / Welford triples / margin counts) ran against a host built with components disabled. The OVERLAY_PAIRWISE_* family needs the components block to resolve each pair leg's sample size, so predict reports it too (Options.DisableComponents, or the request's disable_components: true; Details {index, kind}); a Compose two-sample vs-ref overlay scoring a grouped slot's moment aggregation reads each group's mean / variance / n from that slot's per-group components (details.reason components_disabled, details.slot the slot).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"DisableComponents"},
				Hint:     "Re-run with components enabled: clear Options.DisableComponents (and any per-request disable_components override) so Response.Components.Crosstab carries the cell n / Welford triples the pairwise overlay divides by. On Compose the override is per slot: set disable_components: false on the slot details.slot names (it re-opens an engine-off gate), or score a scalar mean with the params triple instead.",
				Examples: []any{false},
			},
		},
	},
	PULSE_OVERLAY_SCOPE_UNSUPPORTED: {
		Message: "An OverlaySpec named a Scope that is not supported for the chosen Kind. The index-vs-margin overlay, for example, currently supports Scope=cell only; row / column / total / matrix / group land in later releases alongside the matching payload shapes.",
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
		Message: "An overlay handler observed a zero, missing, or non-finite margin denominator. The affected cell stays absent on the overlay payload; the warning carries the row / column index and margin axis so callers can audit the failing cells. A transition-chain overlay also raises it, details `reason` zero_row_absorbing / absorbing_row_synthesized, for a state with no observed move out that it makes absorbing (the unit row). Warning-class — surfaced as a Response.Warning, never as an envelope error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Pre-filter rows that contribute zero margin sums (e.g. FILTER_EXCLUDE on the empty / null axis), or pick a cell aggregator whose denominator is bounded away from zero (AGG_COUNT margins are non-negative integers; AGG_SUM on signed numeric fields may legitimately produce zero).",
			},
		},
	},
	PULSE_OVERLAY_EXPECTED_LOW: {
		Message: "An inferential overlay (the chi-square matrix / row / column, standardized residual, symmetry, Fisher exact cell or chi-square-vs-population overlay) observed an expected count below the χ² approximation's reliability threshold (canonical rule: any expected cell below 5; the symmetry test: any state pair whose expected count under symmetry, (n_ij + n_ji) / 2, is below 5, details low_expected_pairs / zero_pairs; Fisher's 2×2 rule: any cell < 1 OR ≥ 20% cells < 5). The statistic is still emitted alongside; the warning flags rows / columns / cells / categories where the approximation may be unreliable. Warning-class — surfaced as a Response.Warning, never as an envelope error. PRD FR-J1 shares the code across the χ² / Fisher overlay surfaces — Crosstab MATRIX-host kinds and Facet GROUP-host CHISQ_VS_POP emit the same warning shape so renderers can lift it into a single \"approximation may be unreliable\" surface regardless of host shape.",
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
		Message: "An OverlaySpec's Level selector exceeds the nested-axis depth of the relevant host axis. Valid depths are zero-indexed from the top of the axis: Row axis depth = len(Crosstab.Rows), Column axis depth = len(Crosstab.Columns). Mirrors PULSE_CROSSTAB_NORMALIZE_LEVEL_OUT_OF_RANGE on the crosstab normalize_level surface. The same code refuses an out-of-range overlay Params value, at predict and at runtime alike: a rolling-window Params[\"window\"] that is not a positive integer or sits below the kind's floor (details.min: 1 for the rolling-mean index, 2 for the rolling z-score, whose sample SD is undefined over one point), and a series formula overlay's Params[\"baseline_position\"] that is not a non-negative integer.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Level"},
				Hint:     "Pick a depth in [0, len(axis)-1] where axis = Crosstab.Rows for row-axis overlays and Crosstab.Columns for column-axis overlays. Omit the field entirely to default to the leaf (the deepest grouper on the axis).",
				Examples: []any{0, 1, 2},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "window"},
				Hint:     "Use a whole number at or above details.min — 2 or more for the rolling z-score; window 1 of the rolling-mean index indexes each row against the one before it.",
				Examples: []any{2, 3, 7},
			},
		},
	},
	PULSE_OVERLAY_PARAM_MISSING: {
		Message: "An OverlaySpec did not supply a Params entry that the chosen Kind requires. The rolling-mean index overlay requires Params[\"window\"] (positive integer) per the WIN_* operator convention; the window width lives on Params, not on Ref. Surfaced at both predict (descriptor.validateOverlayIndexVsRollingMean) and runtime (processing.applyIndexVsRollingMean) with Details carrying the kind and the missing param name. A Compose two-sample t / z overlay (vs-ref or per-cell) scoring a bare scalar mean on a side needs that side's variance and sample size — there is no default (details.reason params_missing; details.side reference | target, details.params the missing keys, details.slot the slot). A formula overlay with no usable params.formula — Params absent or not an object, the key absent, an empty or non-string value — is this code on every host (Request crosstab / grouped, Compose), predict and runtime alike, Details {index, kind, param: \"formula\", reason}; a formula that is present but does not parse is PULSE_OVERLAY_FORMULA_PARSE_ERROR. A pairwise proportion overlay with an EXPLICIT params.p_source cell_value_pct on a crosstab whose normalize is not none is this code too, predict and runtime alike, Details {kind, param: \"p_source\", p_source, normalize}: every normalised cell is already a 0..1 share, so dividing it by 100 would score each pair silently wrong (the omitted p_source reads the share as is).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params"},
				Hint:     "Set Params on the OverlaySpec to a JSON object containing the required keys for the chosen Kind. For OVERLAY_INDEX_VS_ROLLING_MEAN populate `{\"window\": N}` where N is a positive integer naming the rolling-window width (e.g. {\"window\": 3} for a 3-point rolling mean). Per the WIN_* operator convention, the window width is the only required param today.",
				Examples: []any{map[string]any{"window": 3}, map[string]any{"window": 7}, map[string]any{"window": 12}},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params"},
				Hint:     "Two-sample overlay (details.reason params_missing): supply the keys details.params names — variance_target / variance_ref and sample_size_target / sample_size_ref, or the symmetric variance / sample_size for both sides — each a finite number > 0. Or let the slot carry the moments: score the moment aggregator (Welford mean, variance and n — on a crosstab cell, any aggregator whose components carry mean and variance) with components enabled, and no params are read.",
				Examples: []any{map[string]any{"variance": 4.0, "sample_size": 30}, map[string]any{"variance_target": 2.5, "variance_ref": 3.1, "sample_size_target": 40, "sample_size_ref": 38}},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "p_source"},
				Hint:     "Pairwise proportion kind on a normalised crosstab (details.param p_source, details.normalize set): drop p_source — the default on a normalised host reads each cell as the 0..1 share it already is — or set it to cell_value. Keep cell_value_pct only for a normalize none host whose cells are 0..100 percentages.",
				Examples: []any{map[string]any{"p_source": "cell_value"}},
			},
		},
	},
	PULSE_OVERLAY_FORMULA_PARSE_ERROR: {
		Message: "An OVERLAY_FORMULA spec's Params[\"formula\"] string failed to parse as an expr-lang expression. The formula was syntactically invalid — typically unbalanced parentheses, a stray operator, an unterminated string literal, or a typo on a keyword. A formula that parses but names an unknown variable or function is PULSE_OVERLAY_FORMULA_INVALID_IDENT instead, on every host — the identifier gate runs before the compile. Surfaced at both predict (descriptor.validateFormulaOverlay) and runtime (processing.compileFormulaProgram) so authors catch the failure before evaluation begins. Details carry {formula, parse_error, kind, index} — the parser's own position-aware error string is preserved verbatim under Details.parse_error so renderers can surface the offending column / token alongside the offending input.",
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
		Message: "An OVERLAY_FORMULA expression references an identifier (variable or function) that is not allowed for the resolved host shape. The per-shape variable namespace is fixed: MATRIX hosts expose `cell, margin_row, margin_col, margin_grand, sd_row, sd_col, sd_grand` (plus `ref_cell` and the dotted `slot.<label>.cell` namespace on Compose hosts); SERIES hosts expose `value, total, prior` (plus opt-in `baseline` when Params[\"baseline_position\"] is set, plus `ref_value` on Compose hosts); SCALAR hosts expose `value` (plus `ref` on Compose hosts). The function namespace is the expr-lang stdlib plus every pulse.Options.Extensions.ExprFunctions registration and (when LookupTables are registered) the auto-injected lookup() builtin. Surfaced by predict (descriptor.validateFormulaOverlay) and by the runtime BEFORE it compiles (Request host: processing.requestFormulaIdentRefusal; Compose host: the dispatcher), both through the one AST walk in internal/formulaident, so code, message and Details match. A vector built-in (vsum ... vnormalize) carries {unknown_identifier, allowed_builtins, formula, kind, index} instead. Details carry {unknown_identifier, host_shape, allowed | allowed_funcs, formula, kind, index}; Compose-only `slot.<label>.<field>` identifiers on a Request host additionally carry {reason: \"slot namespace is Compose-only\"}.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "formula"},
				Hint:     "Check the allowed identifier set for shape Details.host_shape against Details.allowed (variables) or Details.allowed_funcs (functions) — the per-shape namespace table lives in the FORMULA section of skills/overlay-system.md. If the identifier was meant to be a custom function, register it via pulse.Options.Extensions.ExprFunctions before calling pulse.New (see docs/src/internals/extension-points.md for the registration recipe — naming policy ^(AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST|SYNTH|MAT)_*$ applies to operators, not to expr functions; expr function names are free-form). If the identifier was meant to be a variable not in the per-shape table, register a custom kind via pulse.Options.Extensions.OverlayKinds — FORMULA cannot widen its variable namespace from outside. Compose `slot.<label>.<field>` identifiers only resolve on Compose-host overlays (FacetRequest.Overlays / ComposedRequest.Overlays), not on Request.Overlays.",
				Examples: []any{map[string]any{"formula": "(cell - margin_grand) / sd_grand"}, map[string]any{"formula": "value / total"}, map[string]any{"formula": "(slot.us.cell - slot.uk.cell) / slot.world.cell"}},
			},
		},
	},
	PULSE_OVERLAY_FIELD_UNKNOWN: {
		Message: "A series overlay's params.field does not name a request aggregation. The param takes the OUTPUT LABEL of one Request.Aggregations entry — its explicit Label, else its TYPE_field default — and the overlay scores that aggregation; absent, it scores the first aggregation. It may instead name one expanded element column \"<label>[<element label>]\" of a params.expand vector aggregation; an element that aggregation does not write is refused too. Raised at predict and at runtime with the same Details {index, kind, field, available}, where available lists the request's aggregation labels in order, each expanded aggregation's element columns after its label. A Compose overlay (INDEX_VS_REF, DELTA_VS_REF, T_VS_REF, Z_VS_REF, PANEL_INDEX_VS_REF) resolves the label on every non-crosstab slot it reads, reference first, and adds Details.slot naming the slot that lacks it. A whole-chain overlay (INDEX_VS_STAGE, DELTA_VS_STAGE) resolves it on the target, then the reference non-crosstab stage, and adds Details.which (target | ref) and Details.stage_index.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "field"},
				Hint:     "Set params.field to one of the labels in Details.available, or give the aggregation you mean an explicit Label and name that. Two aggregations over one input field (AGG_SUM and AGG_MEAN of spend) differ by label, not by field. Omit params.field to score the first aggregation.",
				Examples: []any{map[string]any{"field": "orders"}, map[string]any{"field": "AGG_SUM_spend"}},
			},
		},
	},
	PULSE_OVERLAY_PARTITION_INVALID: {
		Message: "A series overlay's params.partition_by is not a leading prefix of the request's group key columns. With two or more groupers a prior, rolling, baseline, sibling or _TOTAL overlay runs once per partition — the rows sharing the leading keys — so a series never crosses a segment and a total is the partition's own. Absent, it partitions by every key but the last (the series runs down the last grouper); [] means no partition. The year-over-year overlay instead partitions by every non-date key, so its partition_by must name exactly those columns (any order). Raised at predict and at runtime with the same Details {index, kind, partition_by, groups, reason, field}: reason is not_string_list, unknown_field, not_prefix or (year-over-year) not_non_date_keys, groups lists the key columns in order.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "partition_by"},
				Hint:     "Name the first k entries of Details.groups, in order (groups [region, store, day] admits [region] or [region, store]); reorder Request.Groups to put the segment keys first, or omit partition_by to partition by every key but the last. Pass [] to walk every composite row as one series.",
				Examples: []any{map[string]any{"partition_by": []any{"region"}}, map[string]any{"partition_by": []any{}}},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "partition_by"},
				Hint:     "For the year-over-year overlay (reason not_non_date_keys) name every entry of Details.groups except the GROUP_DATE column, once each, or omit partition_by — that is the default.",
				Examples: []any{map[string]any{"partition_by": []any{"region", "store"}}},
			},
		},
	},
	PULSE_OVERLAY_REF_UNKNOWN: {
		Message: "An overlay handler named a reference that does not resolve to a known slot on the host. Three arms today: (1) sibling-reference overlays (delta- and index-vs-sibling) name a Sibling (Field, Value) pair that does not match any observed axis-key value on the SERIES host; (2) baseline-index overlays (index- and delta-vs-baseline, rolling-mean index, year-over-year) name a Position ordinal outside [0, host.GroupCount()) — Details carry {baseline_index, series_length}; a grouped formula overlay's params.baseline_position at or past its partition's length is the same arm with {index, kind, baseline_position, series_length}, which predict raises too when every in-partition grouper is a category grouper over a categorical field (series_length then the dictionary bound); (3) Facet-host population overlays (index, z-score, chi-square and KS vs population) name a population field absent from the host FacetResult — Details carry {field, available_fields}. The affected layer surfaces NaN statistics across every present entry (arms 1–2) or fails fast (arm 3). Warning-class for arms 1–2, runtime error for arm 3 — surfaced as a Response.Warning or envelope error depending on arm.",
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
		Message: "A whole-chain overlay spec (the index-vs-stage or delta-vs-stage overlay) resolved a Target stage and a Ref stage whose host result shapes disagree (one is matrix, the other series; one is scalar, the other series; etc.). A stage's shape is its authored request's — a crosstab is matrix, groups plus aggregations is series, an ungrouped stage is scalar — so a grouped stage against an ungrouped one lands here, never as per-row reference misses. It is warning-class on both sides: predict-chain warns and the chain stays valid, the runtime runs the chain and attaches the warning to the overlay layer; Strict escalates it on neither side. The handler cannot fold per-coordinate arithmetic when target and reference do not share a coordinate grid; the layer surfaces an empty payload that inherits the target stage's shape and the warning carries the offending pair of shapes plus the originating (target_index, ref_index) so callers can audit the chain and collapse one stage so both produce the same shape, or remove the overlay.",
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
		Message: "An overlay spec named a Reference that does not resolve to a known slot or stage. Two host families share this code distinguished by the `which: \"reference\"` Detail: (1) Compose overlay spec — `ComposeOverlaySpec.Reference` is empty, names a slot label absent from `ComposedRequest.Requests[].Label`, or resolved to nil because that slot failed under FailFast=false; Details carry `slot_label` and the spec `index`. (2) Chain overlay spec — a whole-chain `ChainOverlaySpec.Ref` StageRef whose `Index` is out of range OR `Name` is unmatched, or the spec did not populate Ref at all (Ref has no default unlike Target). The code also covers the missing-reference-cell / missing-reference-row warning on the CHAIN-host DELTA family (the delta-vs-stage overlay) with `ref_missing: true` (handler folds against an implicit zero reference). (3) MATRIX_RESULT overlay — `Ref.Matrix.Name` names no `Request.Matrices` spec by its effective name (`MatrixSpec.Name`, else `<TYPE>_<vector>` / `<TYPE>`); Details carry `which: \"matrix\"`, `name`, the request's effective names under `matrices`, the spec `index` and `kind`.",
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
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Ref", "Matrix", "Name"},
				Hint:     "MATRIX_RESULT arm: set Ref.Matrix.Name to one of the names under details.matrices — a matrix spec's `name`, or for an unnamed spec its default `<TYPE>_<vector>` (vector spec) / `<TYPE>` (inline fields).",
				Examples: []any{"MAT_CORRELATION_scores"},
			},
		},
	},
	PULSE_OVERLAY_KEY_SET_DIVERGENT: {
		// Minimal entry — full polish (richer Message + per-shape Fixup
		// hints) is deferred.
		Message: "A Compose overlay spec resolved a reference slot and one or more target slots whose per-coordinate key sets disagree — matrix (row × column) tuples present on one slot but absent on another, or series group-keys diverging across slots. Compose-only overlays require strict cross-Request key alignment so the renderer can fold target values against the reference at byte-equal coordinates; tolerant alignment is an explicit non-goal for v1. Details carry the reference slot label, the offending target slot label, and the symmetric difference of the two key sets (`missing` keys present on reference but absent from target; `extra` keys present on target but absent from reference). A SERIES slot keys on its group columns alone, so predict-compose refuses two SERIES slots whose group key columns differ by name, Details {index, reference, target, reference_fields, target_fields}. Only slots of the same shape are compared: a grouped slot against an ungrouped one is PULSE_OVERLAY_SLOT_SHAPE_DIVERGENT.",
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
		Message: "A Compose overlay spec resolved a reference slot and one or more target slots whose host result shapes disagree (one is MATRIX while the other is SERIES, or one is SCALAR while the other is non-SCALAR). A slot's shape is its authored request's: a crosstab is MATRIX, groups plus aggregations is SERIES, anything else (an ungrouped request) is SCALAR — so a grouped slot paired with an ungrouped one lands here on predict-compose and runtime alike, not under PULSE_OVERLAY_KEY_SET_DIVERGENT. Compose overlays fold target values against the reference at byte-equal coordinates; without a shared shape there is no coordinate grid. Details carry the spec `index` and `kind`, the `reference` slot label, the offending `target_label`, the `reference_shape`, and the `target_shape`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Requests"},
				Hint:   "Re-issue the diverging slot Request so its host result shape matches the reference slot's — either both crosstabs (set Crosstab on both), both grouped Process results (set Groups + Aggregations on both, no Crosstab on either), or both ungrouped (drop Groups from both).",
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
		Message: "A multi-reference COMPOSE-host overlay spec (today: the panel proportion-z and panel index-vs-ref overlays) named more target slots than `OverlayOptions.MaxPanelTargets` allows. Multi-reference overlays bound combinatorial explosion — every additional target enlarges the O(N²) per-cell pairwise fold so the cap is the explicit knob. Per the interview risk paragraph \"Multi-reference combinatorics\", the default cap is 16 and bumping the default requires an interview update; per-request override lives on `ComposeOverlaySpec.Options.MaxPanelTargets`. Details carry the offending `kind`, the `observed` target count, and the active `cap`.",
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
	PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED: {
		Message: "An OVERLAY_PAIRWISE_* spec asked for a distinct-key slab sample size (`params.n_source = \"n_within_distinct\"`) over a pair axis whose summed-across dims include a fan-out grouper (`types.GroupType.FansOut()` — today the per-element set grouper). The slab fixes the first `n_within_depth + 1` pair-axis dims and sums the cells after them; summing per-cell DISTINCT cardinalities equals the slab's true distinct count only when those cells PARTITION the key set. Under a fan-out grouper one key lands in two summed cells, so the n over-states the sample size — silently, and liberally, which is the exact failure the distinct mode exists to remove. A fan-out grouper at depth <= n_within_depth is fine (it sits inside the FIXED prefix and multiplies slabs, not cells), as is one on the opposite axis at any depth. Plain `n_within` is never refused: record counts are additive. Details carry the offending `dim_index`, `group_type`, `field` and `axis` alongside `index`, `kind`, `n_source` and `n_within_depth`. THE COMPOSE-HOST PANEL RAISES THE SAME CODE for the same condition on its own host. The panel proportion-z overlay with `params.n_source = \"row_margin_value_within\"` AND an explicit `n_within_depth` sums each SLOT's row margins over a row-key prefix, so the summed-across dims must partition too. Three differences from the axis-pairing arm, all in the Details: `axis` is always `row` (the panel pairs across SLOTS, so no mode name ever refers to a pair axis); the offending slot is named by BOTH `panel_index` (0 = the reference) and the authored `slot_index`, plus `slot_label`; and every slot is walked, so one offending slot refuses the WHOLE spec rather than dropping out — dropping it would change M, and M sets the length and the pair ordering of every cell's flattened upper-triangular vector. Unlike the axis-pairing arm the panel gates its RECORD-COUNTED-looking mode too, because a panel row margin is whatever that slot's cell aggregator emitted (a count, a distinct cardinality, a weighted sum, a percentage) and the host cannot claim additivity for it. OMITTING `n_within_depth` is always a valid fix on the panel: that form reads the exact per-slot row margin and sums nothing.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "n_within_depth"},
				Hint:     "Raise `n_within_depth` so the fan-out dim named in Details (`dim_index`) falls INSIDE the fixed prefix rather than being summed across. The prefix covers dim positions 0..n_within_depth, so `n_within_depth >= dim_index` makes each fan-out bucket its own slab — every slab then partitions its own keys and the distinct sum is exact. It must stay strictly below the pair-axis dim count.",
				Examples: []any{0, 1, 2},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Rows"},
				Hint:   "Move the fan-out grouper (today only GROUP_SET_PER_ELEMENT) to an OUTER position on the pair axis so the fixed prefix can cover it, or replace it with GROUP_SET_VALUE, which maps each record to exactly one composite bucket and therefore partitions. The same applies to Crosstab.Columns when the overlay Scope is `column`.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Overlays", "*", "Params", "n_within_depth"},
				Hint:   "COMPOSE-host OVERLAY_PROP_Z_PANEL only: remove `n_within_depth` entirely. The omitted-depth form of `row_margin_value_within` reads the EXACT per-slot row margin and sums nothing at all, so no key can land in two summed rows however the row axis fans out — it is never gated, and it is byte-identical to the panel's legacy `row_margin_value` leg wherever that margin is present. Use this when you wanted a per-row denominator rather than a within-prefix one.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"Overlays", "*", "Params", "n_source"},
				Hint:     "Switch to a sample-size mode that does not sum distinct cardinalities. To KEEP the distinct-key count, use `row_margin_distinct` / `column_margin_distinct`: a margin accumulates over raw records rather than folding cells, so it is exact under a fan-out grouper and is never gated — at the cost of a margin-wide denominator instead of a slab one. `n_within` sums per-cell RECORD counts, which are additive under any grouper — correct, but it counts records rather than respondents. `cell_n_unweighted`, `row_margin_n` and `column_margin_n` read a single figure and never sum across a fan-out dim.",
				Examples: []any{"row_margin_distinct", "column_margin_distinct", "n_within", "cell_n_unweighted", "row_margin_n", "column_margin_n"},
			},
		},
	},
	PULSE_OVERLAY_EXPORT_CSV_UNSUPPORTED: {
		Message: "CSV export does not support overlay embedding; %d overlay layer(s) dropped. Warning-class — the host CSV body is written verbatim (byte-identical to a pre-overlay export) and the dropped overlay layers are reported via this warning so callers can audit which layers fell off. CSV is the LCD of tabular formats and consumer tools (Excel, R, pandas, awk) cannot uniformly parse any overlay convention — research/export-embedding-shape.md § 7 locks the warn-and-skip semantic. The TSV adapter shares the CSV writer surface and inherits the same warn-and-skip behaviour; the warning code stays CSV-flavoured. Details carry `layer_count`, `layer_names`, and `layer_kinds` so a renderer can surface the dropped layer slate without re-reading the source Response.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"ExportJob", "Target"},
				Hint:     "To preserve overlays in cross-format export, target an overlay-aware writer instead of CSV / TSV. Arrow (--format arrow) carries overlays as a top-level LIST<STRUCT> column family; Parquet (--format parquet) rides the same Arrow schema through the pqarrow bridge; Excel (--format excel) emits one sheet per layer named `__overlay_<layer_name>`; NDJSON (--format ndjson) appends a single trailing line `{\"_overlays\": [...]}` after the last record. All four formats carry every layer; Arrow, Parquet and NDJSON also carry a layer's named parts, while Excel writes the primary payload only (PULSE_OVERLAY_EXPORT_PARTS_DROPPED).",
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
	PULSE_OVERLAY_EXPORT_PARTS_DROPPED: {
		Message: "Export embeds the primary payload only; %d overlay layer(s) lost their named parts. Warning-class — each layer's primary payload is written as usual and its `parts` (the secondary payloads of a multi-output kind, OverlayPayload.Parts) are dropped because the target's per-layer layout has no slot for them (Excel: one sheet per layer). Details carry `layer_count`, `layer_names` and `parts` (layer name → part names) so callers can audit what fell off.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"ExportJob", "Target"},
				Hint:     "To keep the parts, export to a format that writes each overlay layer as JSON: NDJSON (--format ndjson, a trailing `_overlays` line), Arrow (--format arrow) or Parquet (--format parquet). Each carries the layer's `payload.parts` byte-for-byte as the JSON envelope does.",
				Examples: []any{"ndjson", "arrow", "parquet"},
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Request"},
				Hint:   "To read the parts without exporting, take them from the JSON envelope: `data.overlays[i].payload.parts.<part>`. A `return` include of `overlays[*].payload.parts.<part>` selects one part.",
			},
		},
	},
	PULSE_OVERLAY_NONNEGATIVE_REQUIRED: {
		Message: "The overlay reads its crosstab host as a table of non-negative masses (counts or sums), and the host is not one: either the cell aggregation is not a count or a sum (an average, a rate or a code is not a mass a row or column profile can be built from), or a cell holds a negative or non-finite value. Correspondence analysis — and the Markov and raking kinds — divide each cell by its row and column totals, the symmetry test counts each cell as moves between two states, and the retention rates divide each cell by its row total, which only means something when every cell is a non-negative amount. Details carry `kind` plus `aggregation` and `slot` (the cell aggregation) or `row`, `column` and `value` (the first offending cell).",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"Crosstab", "Cell", "Type"},
				Hint:     "Score the crosstab cell with a count (AGG_COUNT, weighted or not) or a sum of a non-negative field (AGG_SUM), so every cell is a mass the overlay can turn into row and column profiles.",
				Examples: []any{"AGG_COUNT", "AGG_SUM"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Cell", "Field"},
				Hint:   "An AGG_SUM cell went negative: sum a field that cannot be negative (a quantity, an amount spent), or filter out the negative records with a FILTER_EXPRESSION before the crosstab runs.",
			},
		},
	},
	PULSE_OVERLAY_MATRIX_NOT_SQUARE: {
		Message: "The overlay reads its crosstab host as a square table of transitions — rows are the from-state, columns the to-state (the Markov, symmetry and retention overlays) — and the two axes do not name the same states. States are matched by key, never by position, so the row and column key sets must be equal as sets: the same number of groupers on both axes, and every state that appears as a row must appear as a column and the reverse. A crosstab only emits keys it observed, so a state nobody leaves (or nobody enters) during the window is missing from one axis; a column-only state named in params.absorbing is the one exception — its row is known by definition (the unit row), so the overlay synthesizes it instead of refusing. The symmetry test and the retention rates refuse only the grouper-count mismatch: a state on one axis only is a row or column of zeros there (nobody started or ended in it). Details carry `kind` plus either `row_groupers` / `column_groupers` (the axes have different grouper counts) or `row_only` / `column_only` (the state names found on one axis only, the Markov overlay).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Crosstab", "Columns"},
				Hint:   "Group the rows and the columns the same way — one grouper each, on the from-state and the to-state fields (the same categories on both, e.g. from_tier x to_tier) — so both axes carry the same kind of key.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Crosstab", "Rows"},
				Hint:   "A state appears on one axis only. A column-only state nobody leaves (churned) is absorbing: name it in params.absorbing and the overlay adds its unit row. Otherwise record a transition for it on the other axis, or filter out the records that mention it with a FILTER_EXPRESSION before the crosstab runs.",
			},
		},
	},
	PULSE_OVERLAY_MARKOV_NOT_ERGODIC: {
		Message: "Warning: the OVERLAY_MARKOV chain has no single long-run distribution, so the layer omits its steady_state part. Either the chain is reducible — some states cannot be reached from others (an absorbing state, or two groups of states that never exchange members; details `classes` lists the communicating classes) — or it is periodic, cycling through groups of states with a fixed rhythm (details `period`). With reason `absorption_not_certain` the warning instead lists the `states` that can wander off into a closed group outside params.absorbing: their absorption_time is null (infinite). Every other part still emits.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Overlays", "Params", "absorbing"},
				Hint:     "A chain with absorbing states (churned, closed) is reducible by design: read the absorption_time part (time to reach them) and the step_<n> / distribution_<n> projections instead of a steady state, and name every absorbing state in params.absorbing.",
				Examples: []any{[]string{"churned"}},
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Crosstab", "Rows"},
				Hint:   "A periodic or split chain often comes from too short an observation window or too fine states: widen the window or merge states so every state can reach every other, then the steady state is defined.",
			},
		},
	},
	PULSE_OVERLAY_RAKE_TARGET_MISMATCH: {
		Message: "The OVERLAY_RAKE targets do not line up with the crosstab host. With reason `keys`, an axis's target map and the host's keys on that axis differ: `target_only` lists target keys the host does not carry, `host_only` host keys the targets leave out. Raking refuses rather than dropping either side, because a silently dropped key would mislabel the fit. A crosstab only emits keys it observed, so a category with no records is missing from the host even when the field's dictionary holds it. With reason `totals_differ`, targets_as `counts` gave row and column totals that differ (`row_total`, `column_total`): no table can match both margins.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Overlays", "Params", "row_targets"},
				Hint:     "Give exactly one target per host key on each axis — the same spelling as the crosstab's row and column keys — and drop targets for categories the data never holds (or filter the host so it holds only the targeted categories).",
				Examples: []any{map[string]any{"north": 0.25, "south": 0.25, "east": 0.25, "west": 0.25}},
			},
			{
				Action:   FixupSetDefault,
				Path:     []string{"Overlays", "Params", "targets_as"},
				Hint:     "Counts targets must share one grand total across rows and columns; when the sources disagree, pass shares instead (targets_as \"shares\"), which are normalized per axis and scaled to the host grand total.",
				Examples: []any{"shares"},
			},
		},
	},
	PULSE_OVERLAY_GROUPS_REQUIRED: {
		Message: "The overlay summarizes a result across its group buckets, but the request carries no Request.Groups: an ungrouped matrix spec has one result, so there is nothing to summarize across. OVERLAY_MATRIX_GROUP_SUMMARY needs the matrix spec computed once per group bucket.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Groups"},
				Hint:     "Add a grouper so every matrix spec is computed once per bucket, then the summary reads every bucket's matrix.",
				Examples: []any{[]map[string]any{{"type": "GROUP_CATEGORY", "field": "region"}}},
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"Overlays", "*"},
				Hint:   "On an ungrouped request the matrix result already is the whole-cohort figure: drop the group-summary overlay and read Response.Matrices directly.",
			},
		},
	},
	PULSE_OVERLAY_RAKE_NOT_CONVERGED: {
		Message: "Warning: the OVERLAY_RAKE fit stopped at params.max_iter sweeps with a fitted margin still further than params.tolerance (relative) from its target. The fitted table and factors still emit, with summary.parameters.converged 0 and the final residual. A fit that cannot converge usually meets structural zeros that make the targets unreachable — a row whose cells are all zero cannot gain a positive target — or targets far from the observed table.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Overlays", "Params", "max_iter"},
				Hint:     "Raise max_iter (or loosen tolerance) when the residual is still falling; a residual that stalls means the targets are unreachable from this table's pattern of zeros.",
				Examples: []any{1000},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Overlays", "Params", "row_targets"},
				Hint:   "Check targets against the table's zeros: a category with no observed cells cannot be raked to a positive target — merge it with a neighbour or give it a zero target.",
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
		Message: "No sidecar point-lookup index was found on disk for the requested LookupRequest.Field. Point lookups probe a prebuilt sidecar index (encoding.SidecarIndexPath derives its path from the cohort file and key field) rather than scanning the cohort — the index must be built once before any Lookup call against that field can succeed. A row-comparison attribute's {record: {cohort, keys}} reference runs this same lookup before the run and refuses with this code, located on the slot (`attribute`).",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Build the sidecar index for this field first: `pulse index build <cohort> --key <field>` (or the equivalent Service.BuildIndex(ctx, path, []string{field}) library call). Re-run the lookup once the sidecar file exists at the derived `<cohort>.<keyhash>.idx` path.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Field"},
				Hint:   "Alternatively, point the lookup at a field that already has a built sidecar index. `pulse index list <cohort>` (or Service.ListIndexes) reports every built index with its ordered key tuple — read that rather than globbing `.idx` files beside the cohort, which is unavailable on object storage: it is served from the keyless manifest `<cohort>.indexes.json`, needs no directory listing, and a sidecar's own filename is a hash OF its key tuple and so cannot be interpreted without the manifest.",
			},
		},
	},
	PULSE_LOOKUP_NOT_FOUND: {
		Message: "The sidecar point-lookup index was found and read successfully, but LookupRequest.Value has no matching entry in the resolved hash bucket — no record in the cohort carries that key value for LookupRequest.Field. A row-comparison attribute's {record: {cohort, keys}} reference runs this same lookup before the run and refuses with this code, located on the slot (`attribute`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "reference", "record", "keys"},
				Hint:   "For a comparison {record} reference, name a key tuple the record's cohort (record.cohort, else the request's own) holds — each value as text, spelled as in the data.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Value"},
				Hint:   "Verify the literal Value is spelled/typed exactly as it appears in the source data (categorical values are case-sensitive dictionary lookups; numeric values must parse to the same on-wire representation as the indexed field). If the cohort was re-imported or the key column's data changed since the index was built, rebuild the sidecar via `pulse index build` — a stale index silently returns not-found for keys added after the last build.",
			},
		},
	},
	PULSE_LOOKUP_AMBIGUOUS: {
		Message: "The requested key matched more than one row in the cohort, but LookupRequest.Multiplicity is (or defaults to) \"assert_unique\", which fails rather than silently picking one match. This is the default mode precisely so a duplicate-key surprise never returns a single, arbitrary row without the caller knowing more than one candidate exists. A row-comparison attribute's {record: {cohort, keys}} reference runs this same lookup before the run and refuses with this code, located on the slot (`attribute`). A {record} reference is always assert-unique.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "reference", "record", "keys"},
				Hint:   "For a comparison {record} reference, add key columns until the tuple names one record (and build the sidecar index over that composite key) — a stored reference must be exactly one row.",
			},
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
		Message: "The sidecar point-lookup index's embedded content-hash fingerprint does not match the current .pulse cohort file — the cohort was mutated (re-imported, re-exported, or otherwise rewritten) after the sidecar was last built. On a shard archive (one sidecar per shard) the same code refuses a shard the index does not cover — added after the build, or replaced (a different size or zip central-directory CRC32 under the same name) — and details.shards_uncovered names those shards: a lookup never silently skips one. Lookup refuses to serve rows against a stale index rather than risk returning silently wrong results; there is no automatic scan-fallback or auto-rebuild. A row-comparison attribute's {record: {cohort, keys}} reference runs this same lookup before the run and refuses with this code, located on the slot (`attribute`). `shard add` / `shard remove` emit it as a warning (details.reason index_upkeep_publish_failed) when keeping the archive's index in step failed after the archive itself was rewritten.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Rebuild the sidecar index against the current cohort content: `pulse index build <cohort> --key <field>` (or the equivalent Service.BuildIndex(ctx, path, keyFields) library call, or a quick freshness check first via `pulse index verify <cohort> --key <field>` / Service.VerifyIndex). Re-run the lookup once the rebuild completes.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "For a shard archive, details.shards_uncovered lists the shards the index does not cover. Rebuild the archive's index (`pulse index build <archive.pulse> --key <field>` rewrites every shard's sidecar and the manifest's coverage); `pulse index verify` reports the same shards as shards_uncovered.",
			},
		},
	},
	PULSE_INDEX_UNSUPPORTED_SHARDED: {
		Message: "No longer raised: point-lookup index build, lookup, verify, list and drop accept a shard archive, indexed one sidecar per shard plus a manifest entry recording the covered shards (U51). The code is retained so lookups of it keep resolving.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Build the index on the archive itself — `pulse index build <archive.pulse> --key <field>` — and look up against the archive path; an anchored shard (`archive.pulse#shard.pulse`) reads the same per-shard sidecar.",
			},
		},
	},
	PULSE_INDEX_MANIFEST_INVALID: {
		Message: "The sidecar index manifest beside this cohort (\"cohort.pulse.indexes.json\") exists but is not a readable manifest document — malformed JSON, a foreign \"kind\", an unrecognised \"format_version\", or an entry naming no index file. That manifest is the keyless catalog that makes each sidecar index's ordered key tuple discoverable without a directory listing (a sidecar's own filename is a 16-hex-digit HASH of its key tuple, so it cannot be opened without already knowing the tuple). A manifest that cannot be parsed is refused rather than silently ignored: falling back to a directory listing succeeds on a local disk and answers \"this cohort has no indexes\" on object storage, which is precisely the backend-dependent divergence the manifest exists to remove. An ABSENT manifest is NOT this error — that is the ordinary state of a corpus indexed before the manifest existed, and the directory-listing fallback covers it.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Rewrite the manifest from the indexes that actually exist: run `pulse index build <cohort> --key <field[,field...]>` for each key tuple you need (a build upserts its own manifest entry and seeds the document from the sidecars already on disk), or delete the unreadable `<cohort>.pulse.indexes.json` file to fall back to the directory-listing path on a filesystem that supports it. The `.idx` sidecars themselves are untouched by this error — only the catalog is unreadable.",
			},
		},
	},
	PULSE_INDEX_MANIFEST_STALE: {
		Message: "The sidecar index manifest parses cleanly but names an index file that is not present on disk — an index was removed out of band (a bare `rm` of the `.idx` file rather than `pulse index drop`, or a corpus that synced the manifest without every sidecar). The listing is refused rather than quietly skipping the entry: a listing that reports two indexes for a cohort whose manifest claims three is indistinguishable from a correct answer, and a caller that then composes a lookup key for the missing tuple gets PULSE_INDEX_MISSING for data the manifest said was there.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Pick one: rebuild the named key tuple with `pulse index build <cohort> --key <field[,field...]>` (the details' key fields name it), or drop it with `pulse index drop <cohort> --key <field[,field...]>` — drop prunes the manifest entry even when the sidecar file is already gone, which is the repair path for exactly this state.",
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

	PULSE_TEMPLATE_AXIS_COLLISION: {
		Message: "The template declares a variable with the same name as an axis of a for_each item or a Compose sweep in its body. Inside that item (or the sweep's request, overlays and label) the renderer leaves the axis placeholders for the for_each or sweep expansion, so a same-named template variable would make each such placeholder ambiguous.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Variables", "*"},
				Hint:   "Rename either the template variable or the for_each or sweep axis named in the error details so the two no longer share a name, and update the placeholders that refer to the renamed one.",
			},
		},
	},
	PULSE_TEMPLATE_EXPR_SPLICE: {
		Message: "The template body interpolates a variable with `{{name}}` inside an expression string (a filterer or attribute expression, a regression holdout, an overlay formula). Splicing text into an expression would let the variable's value rewrite the expression's logic, so the template is refused when it is loaded. A `for_each` or `sweep` axis token in an expression string is refused the same way, since that expansion would splice the axis value's text. The error details name the variable or axis, the body path and the slot.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"body", "params"},
				Hint:   "Pass the value as a request parameter instead: add `\"params\": {\"x\": {\"$var\": \"x\"}}` to the request body (the marker keeps the value's type) and read it in the expression as `params.x`. A `{\"$var\": \"f\"}` marker may still supply the whole expression string, and `{{{{` still writes a literal `{{`.",
				Examples: []any{map[string]any{
					"params":    map[string]any{"from_wave": map[string]any{"$var": "from_wave"}},
					"filterers": []any{map[string]any{"type": "FILTER_EXPRESSION", "expression": "wave == params.from_wave"}},
				}},
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
	PULSE_SPSS_FILE_EMPTY: {
		Message: "The SPSS source contains no bytes at all — a zero-length .sav / .zsav file, or an in-memory reader constructed over a nil or empty buffer. There is no header to read, so this is reported separately from a truncated dictionary: a truncated file stopped part way through a record, while an empty one never had a first record.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Check the path points at the file you meant and that the export or download actually wrote it — a zero-length file is usually a created-but-never-filled target, a failed transfer, or a shell redirection that truncated the destination before the writer ran.",
			},
		},
	},
	PULSE_SPSS_ENDIANNESS_MISMATCH: {
		Message: "The SPSS .sav file states its byte order twice and the two statements contradict each other. The header layout code identifies one order (it is always 2 or 3, so reading it both ways is unambiguous) while the record 7/3 machine-integer endianness field names the other. Unlike the charset cross-check this is a hard error, because byte order governs every multi-byte field in the file: picking the wrong one does not lose a label, it yields a complete file of plausible and wrong numbers.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-save the file from SPSS or PSPP. A file whose header and 7/3 record disagree was almost certainly rewritten in place by a tool that byte-swapped one and not the other, and the values it holds cannot be trusted even where they decode.",
			},
		},
	},
	PULSE_SPSS_MAGIC_FLAG_MISMATCH: {
		Message: "The SPSS system file's 4-byte magic and its header compression flag disagree about whether the file is a ZSAV: $FL3 (the ZSAV magic) with compression flag 0 or 1, or $FL2 with compression flag 2. This is a warning and the file still reads — the compression flag is the operative statement, because it is what says how the data section is encoded, while the magic is a coarse generation label a re-saving tool can leave stale.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "No action is needed if the data imported correctly; the compression flag decided and it is the field that describes the bytes. If the import failed further on, re-save the file from SPSS or PSPP so the magic and the flag agree.",
			},
		},
	},
	PULSE_SPSS_VALUE_LABELS_DROPPED: {
		Message: "A record 3/4 value-label pair in the SPSS .sav dictionary names variables it cannot be bound to: a set mixing variables of different type or width, a set attached to a string wider than the 8 bytes a record type 3 value slot holds (those belong in the record 7/21 extension), or an element index landing on a string continuation rather than on a variable. That one label set was dropped and the rest of the file imported normally. Value labels are display metadata, so refusing the whole file would cost the data to save the labels — and binding the set anyway would attach labels to the wrong values without saying so.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "The imported values are unaffected; only the labels for the variable named in the details are missing. Re-saving the file from a current SPSS or PSPP normally rewrites long-string value labels into the record 7/21 extension, which Pulse does read.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Labels"},
				Hint:   "Supply the labels yourself if they matter: register a label table (Options.Extensions.LabelTables or PULSE_LABEL_TABLES_DIR) and reference it from the request's LabelBinding.Table.",
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
	PULSE_SPSS_VERY_LONG_STRING_INVALID: {
		Message: "The SPSS .sav record 7/14 very-long-string segmentation could not be reassembled. SPSS stores a string wider than 255 bytes as several physical variables of 255 bytes each and records how to rejoin them in record 7/14; this file's record names a variable that is not in the dictionary, states a width the scheme cannot express, or is followed by physical variables whose widths do not match the width it declares. This is a warning, not a failure: the record only says how to JOIN columns that are already present, so nothing is lost — the segments import as the separate columns the dictionary literally declares, under their own generated names, with the value split across them.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Look for the segment columns in the imported cohort — the named variable plus the ones immediately after it — and concatenate them if the whole value is needed. Re-saving the file from SPSS or PSPP rewrites record 7/14 and usually clears a segmentation that disagrees with the variable records.",
			},
		},
	},
	PULSE_SPSS_COMPRESSION_UNSUPPORTED: {
		Message: "The SPSS .sav dictionary parsed cleanly but its data section declares a compression flag the format does not define. All three defined encodings import: uncompressed (flag 0), bytecode compression (flag 1, SPSS's save default) and ZSAV zlib blocks (flag 2, what a .zsav carries). Reading a data section under the wrong encoding would produce plausible-looking garbage, so an unrecognised flag stops the import instead of being guessed at.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-export the file from SPSS or PSPP — `SAVE OUTFILE='survey.sav'.` writes a bytecode-compressed file, `SAVE OUTFILE='survey.sav' /UNCOMPRESSED.` an uncompressed one, and `SAVE OUTFILE='survey.zsav' /ZCOMPRESSED.` a ZSAV. All three import.",
			},
		},
	},
	PULSE_SPSS_ZSAV_INVALID: {
		Message: "The SPSS .zsav file's ZSAV block index does not describe the file it sits in. The index is the 24-byte ZHEADER at the head of the data section plus the ZTRAILER it points at, carrying one entry per zlib block with that block's offset and size in both compressed and uncompressed form. Those entries must tile the compressed region exactly — each block beginning where the previous one ended — and the trailer's length must match the block count it declares. The reported block number is where the tiling first broke. Nothing else in the file says where a block starts, so the reader cannot inflate past that point.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-export the file rather than repairing the index by hand: in SPSS, File > Save As with type 'SPSS Statistics (*.zsav)', or in PSPP `SAVE OUTFILE='survey.zsav' /ZCOMPRESSED.`. `pulse errors lookup PULSE_SPSS_ZSAV_INVALID` prints the block-index semantics.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "If re-exporting is not possible, save an uncompressed copy instead — `SAVE OUTFILE='survey.sav' /UNCOMPRESSED.` — which drops the block index entirely.",
			},
		},
	},
	PULSE_SPSS_ZSAV_BLOCK_CORRUPT: {
		Message: "One zlib block of the SPSS .zsav data section could not be inflated, or inflated to a size other than the one its block-index entry declares. The index itself was coherent — the offsets were right — so the bytes at them are damaged rather than mis-addressed. A block that yields the wrong number of bytes is as fatal as one that fails outright: the decompressed blocks are concatenated into a single command stream, so a short or long block shifts every value after it onto the wrong variable.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Fetch the file again — a damaged zlib block is normally a truncated or corrupted transfer. Compare the byte length against the source before re-importing.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "If the source copy is itself damaged, re-export from SPSS or PSPP: `SAVE OUTFILE='survey.zsav' /ZCOMPRESSED.`, or `SAVE OUTFILE='survey.sav'.` for a bytecode-compressed .sav.",
			},
		},
	},
	PULSE_SPSS_COMPRESSION_INVALID: {
		Message: "The SPSS .sav file declares bytecode compression (the SPSS save default) but its command stream does not match the dictionary that precedes it: a command asked for an all-spaces string segment where the dictionary declares a numeric variable, or for the system-missing sentinel where it declares a string, or the header's compression bias is not a usable number. The stream has lost sync with the dictionary, so every element after that point would be read against the wrong variable. Decoding on would produce numbers rather than an error, so the import stops.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-export the file from SPSS or PSPP rather than repairing it by hand — a desynchronised command stream means the bytes on disk no longer describe the dictionary, and no reader can recover the intended values. `pulse errors lookup PULSE_SPSS_COMPRESSION_INVALID` prints the byte offset semantics; the reported offset is where the stream stopped agreeing.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "If the file was produced by a non-SPSS writer, re-save it through SPSS or PSPP (`GET FILE='in.sav'. SAVE OUTFILE='out.sav'.`) so the data section is rewritten by a conforming encoder.",
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
		Message: "An SPSS variable carrying a day-resolution print format (DATE, ADATE, EDATE, SDATE or JDATE) was mapped to the Pulse datetime type rather than date, because at least one of its values carries a time of day that day resolution would truncate. A pre-1970 value alone never widens: date is signed epoch days. This is a warning: datetime holds every such value exactly and the date-family groupers accept it by documented day truncation.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "No action is needed — GROUP_DATE and the date-range operators accept a datetime field and truncate it to the day. Supply an explicit ImportJob.Schema only if the column must be a date, accepting that times of day will not survive.",
			},
		},
	},
	PULSE_SPSS_TYPE_NOT_RESTORED: {
		Message: "A .sav variable carried a Pulse type hint (the record 7/18 @PulseType attribute a Pulse export writes for a column SPSS cannot type natively), but the import did not restore that type: the hint is unreadable, names a type the variable's SPSS shape cannot carry, or a value does not fit it exactly (a u64 at or past 2^53, a fractional value under an integer type, a decimal past its scale or precision). This is a warning: the variable maps as if it carried no hint — a plain numeric becomes f64 — so every stored value is kept and nothing is truncated.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "No action is needed if f64 is acceptable. To keep the narrower type, correct the offending value in the .sav (details.value) or supply an explicit ImportJob.Schema naming the type, accepting that the import then refuses a value that does not fit.",
			},
		},
	},
	PULSE_SPSS_INTEGER_INEXACT: {
		Message: "A .sav export wrote a u64 value past 2^53 into its variable's double. Past 2^53 a double cannot tell neighbouring integers apart (2^53+1 is stored as 2^53), so the file may not hold the value the cohort held. The export warns and writes the case; under Strict it fails and writes nothing. From export predict the same code is a risk on a u64 variable: only the data can say whether a value is that large.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Target"},
				Hint:   "Export the cohort to a format with a 64-bit unsigned integer (parquet, arrow) or to text (csv, ndjson), which keep every u64 exactly. Keep the .sav only if the column is never used as an exact key: an identifier past 2^53 does not survive a double.",
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
		Message: "An SPSS variable whose record 7/11 measurement level is 'scale' carries value labels, so it mapped to a Pulse categorical field. Its smart defaults are therefore the categorical ones (frequency aggregation, category grouping) rather than the numeric ones (sum aggregation, range grouping) the declared level implies. This is a warning: every code and label is preserved, but the analytic defaults will not be the ones the source file declared.",
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
	PULSE_SPSS_CHARSET_UNSUPPORTED: {
		Message: "The SPSS .sav file declares a character encoding this reader cannot decode with: either record 7/20 names a charset that resolves to nothing known, record 7/3 carries a character code with no charset behind it (EBCDIC, DEC Kanji), or the charset is not an ASCII superset and so cannot carry the format's own space padding and ASCII record delimiters. Decoding the file with a guessed codepage would produce text that is plausible and wrong, so the import stops here instead.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Re-save the file from SPSS or PSPP with a Unicode encoding (`SET UNICODE ON` before `SAVE`, or `pspp-convert` with `--encoding=UTF-8`), which writes record 7/20 as UTF-8. If you know the real charset and the file mislabels it, pass spss.WithCharset(\"windows-1252\") to the reader to override the declaration.",
			},
		},
	},
	PULSE_SPSS_CHARSET_INVALID: {
		Message: "A byte sequence in the SPSS .sav file is not decodable in the charset the file declares — an undefined byte in a single-byte codepage, or invalid UTF-8 in a file declaring UTF-8. It is reported rather than substituted: the usual behaviour of a text decoder is to emit U+FFFD replacement characters, which would turn a codepage mismatch into a cohort full of replacement characters no later stage could tell from real data. The details name the charset and, where the fault is in a variable, the variable.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "The file's declared charset is usually wrong rather than the bytes. Pass spss.WithCharset(\"<charset>\") to the reader with the encoding the data is really in, or re-save the file from SPSS or PSPP as UTF-8 so the declaration and the bytes agree.",
			},
		},
	},
	PULSE_SPSS_CHARSET_MISMATCH: {
		Message: "The SPSS .sav file states its character encoding twice and the two statements disagree: the record 7/20 name resolves to one charset and the record 7/3 character code to another. This is a warning and the 7/20 name is used, because the name is strictly more expressive than the number — 7/3 cannot tell ISO-8859-1 from windows-1252 — and writers routinely leave the legacy numeric field at an ASCII default while naming the real charset in 7/20.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "No action is needed if the text imported correctly. If it did not, the 7/3 code may be the accurate one: pass spss.WithCharset with that charset to override the name the file declares.",
			},
		},
	},
	PULSE_SPSS_CHARSET_UNENCODABLE: {
		Message: "A string held by the cohort contains a character that has no representation in the charset the emitted .sav declares, so the export stopped. It is reported rather than substituted: the encoders will replace an unsupported rune with '?' or 0x1A on request, and a substituted character is indistinguishable from data once it is on the wire — a windows-1252 label reading \"Z?rich\" has lost the name of a city and says nothing about having done so. The usual cause is a cohort edited since it was imported: text produced by a Pulse operation is UTF-8 and need not be expressible in the legacy codepage the source file declared. Details name the target charset, the variable and the offending value.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Charset"},
				Hint:   "Write the file in a charset that can carry the text. Set spss.WriterOptions.Charset to \"UTF-8\" — modern SPSS, PSPP, haven and pyreadstat all read a UTF-8 .sav — and the emitted record 7/20 declares it, so the file stays self-describing.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Or change the value. The details carry the exact string; replace the offending character in the cohort (or in the source data) with one the target codepage defines.",
			},
		},
	},
	PULSE_SPSS_NAME_INVALID: {
		Message: "A name being written into a .sav dictionary is not one SPSS can carry. An SPSS variable name is at most 64 BYTES once encoded in the emitted file's charset, opens with a letter or one of '@', '#', '$', carries only letters, digits and '.', '_', '$', '#', '@', and does not end with '.'. Pulse field names are unconstrained, so a cohort that was not itself read from a .sav routinely holds a name this rule rejects — a space, a leading digit, a bracket. It is a refusal rather than a rename because every way an illegal name fails is quiet: record 7/13 is a tab-separated list of SHORT=LONG pairs with no escape, so a name carrying '=' or a tab re-parses as a different pair list and some other variable silently acquires this one's name. Non-ASCII letters are accepted; SPSS's reserved syntax keywords are not rejected. Details carry the offending name under `variable` and the cohort field under `cohort_field`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Rename the cohort field named in `cohort_field` to a legal SPSS identifier before exporting — letters, digits and '.', '_', '$', '#', '@', opening with a letter, 64 bytes or fewer. `pulse inspect` lists the cohort's field names.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Charset"},
				Hint:   "If the fault is the 64-BYTE ceiling on a name that is 64 characters or fewer, the name is overflowing because of the charset it is being encoded into. Set spss.WriterOptions.Charset to a single-byte codepage that covers the text, or shorten the name.",
			},
		},
	},
	PULSE_SPSS_NAME_COLLISION: {
		Message: "Two variables an SPSS export would emit answer to one name. SPSS variable names are unique without regard to case, so `Region` and `REGION` are one name and the second record 7/13 mapping for it is dropped, leaving a column in the file that no name reaches. The same fault applies one level down to the eight-byte record type 2 SHORT name, which records 7/5, 7/7, 7/14 and 7/19 key by. It is distinct from PULSE_SPSS_DERIVED_NAME_COLLISION, which is the import-side collision between a generated `<var>_missing` sibling and a variable the source file already declares. Details name the offending name under `variable` and the variable that claimed it first under `collides_with`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Rename one of the two cohort fields so the names differ by more than case. A set_* column is expanded into one indicator variable per dictionary entry, named for that entry, so a collision can also be between a dictionary entry and an ordinary field of the same name — rename either side.",
			},
		},
	},
	PULSE_SPSS_COLUMN_UNMAPPED: {
		Message: "An SPSS export found a cohort column that no emitted variable is written from and that the metadata sidecar's derived-column registry does not account for. Such a column would leave the export silently — present in the .pulse file, absent from the .sav. The registry is what makes this decidable: an import synthesises columns the source dictionary never declared (a `<var>_missing` reason sibling, a multiple-dichotomy `set_*` convenience column) and names every one of them in the sidecar's `derived` block, so a column that is unbound and not in that block is data rather than an artefact. Details name the cohort field under `cohort_field`, the cohort under `cohort` and the sidecar under `sidecar`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "The usual cause is a sidecar that describes an older shape of the cohort. Re-import the source .sav so the sidecar and the cohort are written together, or export with spss.WriterOptions.IgnoreSidecar (--ignore-sidecar) to synthesise the dictionary from the .pulse schema alone, which emits every column as its own variable and drops nothing.",
			},
		},
	},
	PULSE_SPSS_DERIVED_UNFOLDABLE: {
		Message: "The metadata sidecar's derived-column registry describes a column an SPSS export cannot fold back into the file it came from. Four shapes reach it: the entry's `kind` is outside this binary's vocabulary (a document written by a newer import, describing a column whose fold-back is genuinely unknown); the entry is under-populated for its kind, such as a `numeric_missing` entry with no `reasons` dictionary to restore missing codes from; the entry names a source column no emitted variable is written from, so consuming the derived column would discard what it held; or the entry names a column the export is also emitting as a variable, which is the document disagreeing with itself. It is a refusal because both guesses lose — emitting a derived column invents a variable the source never had, and dropping a real one discards data. Details name the derived column under `derived`, its source under `variable` and the sidecar under `sidecar`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"IgnoreSidecar"},
				Hint:   "Export with spss.WriterOptions.IgnoreSidecar (--ignore-sidecar) to synthesise the dictionary from the .pulse schema alone. Nothing is folded, so nothing can fail to fold — every cohort column becomes its own SPSS variable, including the derived ones, at the cost of the source's recorded metadata.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "If the sidecar was written by a newer Pulse than the one exporting, upgrade the exporting binary: an unrecognised derived `kind` describes a column this version has never heard of. Otherwise re-import the source .sav so the cohort and its sidecar are written together.",
			},
		},
	},
	PULSE_SPSS_WIDTH_OVERFLOW: {
		Message: "A string does not fit the fixed-width .sav field the format gives it, measured in the emitted file's charset. SPSS widths are BYTE counts and never rune counts, so transcoding changes them — \"Zurich\" with an umlaut is six bytes in windows-1252 and seven in UTF-8. String VARIABLES are widened automatically to fit; this error is what is left when the format fixes the width: the 32767-byte ceiling on a string variable, an eight-byte record type 2 short name, a value label past the 255 bytes its one-byte length field can count, the 64-byte header file label, or an 80-byte record type 6 document line. It is never a truncation — cutting the field would drop the tail of a value and, in a multi-byte charset, leave a half-character no reader can decode. Details carry the required width, the available width, the variable and the charset.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"Charset"},
				Hint:   "If the text is ASCII-compatible but the target charset is multi-byte, a different charset may fit: set spss.WriterOptions.Charset to \"UTF-8\" or to the single-byte codepage the text belongs to and the byte count drops.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Otherwise shorten the offending text in the cohort. The details name which field overflowed and by how much; a value label, a file label and a document line are all metadata that can be shortened without touching data.",
			},
		},
	},
	PULSE_SPSS_DERIVED_NAME_COLLISION: {
		Message: "An SPSS variable declares user-missing values, so the import would generate a `<var>_missing` sibling column holding the REASON each value is missing — but the file already declares a variable of that name. Two fields of one name cannot be addressed unambiguously, and renaming either one silently would break the mapping back to the source, so the import stops. SPSS variable names are case-insensitive, so the comparison is too. Details name the generated sibling under `derived`, the variable it came from under `variable`, and the existing variable it collides with under `collides_with`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"spss-missing"},
				Hint:   "Import with --spss-missing=null (io.SPSSReaderOptions.MissingMode = io.SPSSMissingNull, spss.WithMissingMode(spss.MissingNull)). No sibling columns are generated, so no name can collide. The cost is real: every user-missing value becomes a plain null and the reason — refused vs. don't know vs. not applicable — is gone from the cohort. The full missing-value specification still rides the metadata sidecar either way.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Rename the colliding variable in SPSS (RENAME VARIABLES) and re-save the .sav. Renaming the real variable rather than the generated one keeps the sibling's `<var>_missing` convention intact, which is what the export path matches on.",
			},
		},
	},
	PULSE_SPSS_MISSING_MODE_INVALID: {
		Message: "The SPSS user-missing handling mode is not one Pulse defines. There are exactly two: `auto` (the default) maps each user-missing value to a null in the analytic column plus a `<var>_missing` sibling carrying the reason, and `null` collapses every user-missing value to a plain null with no sibling. An unrecognised value is refused rather than defaulted, because the two produce different schemas.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"spss-missing"},
				Hint:   "Use --spss-missing=auto or --spss-missing=null (case-insensitive). Omit the flag entirely for `auto`, the fidelity-preserving default.",
			},
		},
	},
	PULSE_SPSS_CATEGORICAL_USER_MISSING: {
		Message: "One or more SPSS variables that mapped to a Pulse categorical column declare user-missing codes, and those codes were kept as ordinary dictionary entries. This is informational — nothing was lost. For a categorical the value IS the label, so the code is already preserved losslessly in the column's own dictionary and no `<var>_missing` sibling is generated; a sibling there would be pure redundancy and would double the schema width of an all-categorical survey. The reason it is reported at all is that a percentage base computed over a coded question silently includes its refusal category unless the analyst excludes it. Details name every flagged variable and its flagged dictionary entries under `missing_categories`; the same flags ride the metadata sidecar as `variables[].categories[].missing`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Filterers"},
				Hint:   "Exclude the missing codes from an analysis base with FILTER_EXCLUDE on the field, listing the dictionary ENTRIES from `missing_categories` — the cohort dictionary holds the SPSS codes, so the value is \"9\", never \"Refused\". A value not in the dictionary is a PROCESSING_CONFIG error, so a mistyped exclusion fails loudly rather than filtering nothing.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Filterers"},
				Hint:   "No action is needed to keep them: the codes are ordinary values and every aggregation counts them. Leave the filterer off when the missing categories belong in the base.",
			},
		},
	},
	PULSE_SPSS_MR_SET_NOT_DERIVED: {
		Message: "An SPSS multiple-DICHOTOMY response set did not get the derived `set_*` convenience column the import normally emits for one. This is informational and the import succeeded: the derived column is ADDITIVE, so every constituent variable of the set was imported as its own ordinary column either way. What is lost is ergonomics — set filters and per-element set grouping over one column — never data. The reason is one of: the set names more than 64 constituents (a set_u64 bitmask has 64 bits); it names a variable no record type 2 declares; it names the same variable twice, which would need one bit to be two; its counted value will not compare against a numeric member; or a constituent's Pulse field name contains the set-token delimiter `|` or IS a null sentinel token (`NA`, `N/A`, `NULL`), either of which would make the derived cell ambiguous. Details name the set under `response_set`, the member count under `distinct`, and any single offending member under `variable`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceOperator,
				Path:   []string{"Groupers"},
				Hint:   "Work from the constituents, which are all present. Each is an ordinary column, so GROUP_CATEGORY / AGG_MODE_COUNT per constituent gives the same per-option counts a GROUP_SET_PER_ELEMENT fan-out over the derived column would have, one request slot per option instead of one.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "For an over-64 set, split it in SPSS (MRSETS /MDGROUP) into sub-sets of 64 or fewer constituents and re-save; each sub-set then derives its own set_* column. For a name that collides with the `|` delimiter or a null token, RENAME VARIABLES on the offending constituent and re-save.",
			},
		},
	},
	PULSE_SPSS_SIDECAR_ABSENT: {
		Message: "No SPSS metadata sidecar (<cohort>.spss.json) was found beside this cohort, so the export is synthesising a default SPSS dictionary from the .pulse schema alone. This is the normal, correct state for a cohort that was never SPSS-derived — synth output, a CSV or Parquet import, a processed result — and the export proceeds. What is not restated is only what the .pulse schema cannot hold: value labels, measure levels, print/write formats, missing-value specifications, multiple-response set definitions and the original 8-byte short names. Every column and every value is unaffected.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "If the cohort DID come from an SPSS file and the labels are wanted, the sidecar has been moved or deleted — it is written beside the cohort at import time and travels with it, so copying a .pulse without its .spss.json leaves it behind. Restore the file, or re-run the import (`pulse import spss -i survey.sav -o cohort.pulse`) to regenerate it. If the cohort was never SPSS-derived, nothing is wrong and this warning needs no action.",
			},
		},
	},
	PULSE_SPSS_SIDECAR_STALE: {
		Message: "The SPSS metadata sidecar beside this cohort no longer matches it: the cohort's byte size or modification time has moved since the sidecar was written, so the dictionary the sidecar carries describes a different version of the data. The export refuses rather than applying it. A stale dictionary is the highest-fidelity-risk state available here — its value codes, labels and missing-value specifications are all complete and plausible, so applying them to changed data produces a .sav that looks authoritative and is wrong, and downstream SPSS syntax reading `IF q1 EQ 5` would silently address a category that is no longer there. This is deliberately an error where an ABSENT sidecar (PULSE_SPSS_SIDECAR_ABSENT) is only a warning.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Regenerate the pair so they agree: re-run the import that produced the cohort (`pulse import spss -i survey.sav -o cohort.pulse`), which rewrites the sidecar against the cohort's current bytes. If the cohort was deliberately rewritten from a different source, delete the stale .spss.json — an absent sidecar is only a warning and the export will proceed on a synthesised default dictionary.",
			},
			{
				Action:   FixupSetDefault,
				Path:     []string{"IgnoreSidecar"},
				Hint:     "To export anyway WITHOUT the sidecar's metadata, pass --ignore-sidecar (spss.WriterOptions.IgnoreSidecar). It downgrades this error to a PULSE_SPSS_SIDECAR_IGNORED warning and takes the synthesised-default-dictionary path — the same output an absent sidecar would give. It does NOT apply the stale dictionary; there is no option that does.",
				Examples: []any{true},
			},
		},
	},
	PULSE_SPSS_SIDECAR_INVALID: {
		Message: "A file exists at the SPSS metadata sidecar's path but is not a sidecar this binary can read — it is not valid JSON, its `kind` is not \"spss\", its `format_version` is not one this binary understands, its fingerprint is not a well-formed digest, or a parallel array inside it breaks the length contract its consumers index against. The export refuses rather than falling back to a synthesised default dictionary: the file's presence asserts that this cohort HAS source metadata, and quietly substituting defaults for it would lose the value labels and missing-value specifications without saying so. Held apart from PULSE_SPSS_SIDECAR_STALE because the fix is different — a stale sidecar is repaired by re-importing, an invalid one by finding out what wrote the file.",
		Fixups: []Fixup{
			{
				Action: FixupRequiresReschema,
				Hint:   "Regenerate the sidecar by re-running the import that produced the cohort (`pulse import spss -i survey.sav -o cohort.pulse`). If the file was hand-edited, restore it from the import instead of repairing it by hand — the fingerprint is over the cohort's bytes and will not survive an edit. A `format_version` this binary does not recognise means the document was written by a NEWER Pulse: upgrade rather than downgrade the document.",
			},
			{
				Action:   FixupSetDefault,
				Path:     []string{"IgnoreSidecar"},
				Hint:     "To export without it, pass --ignore-sidecar (spss.WriterOptions.IgnoreSidecar). The flag suppresses the sidecar READ entirely, so an unreadable file cannot block the export; it downgrades this to a PULSE_SPSS_SIDECAR_IGNORED warning and synthesises a default dictionary.",
				Examples: []any{true},
			},
		},
	},
	PULSE_SPSS_SIDECAR_IGNORED: {
		Message: "An SPSS metadata sidecar exists beside this cohort and --ignore-sidecar (spss.WriterOptions.IgnoreSidecar) was set, so it was not read: the export is synthesising a default SPSS dictionary from the .pulse schema alone. This is an explicit instruction being honoured, not a fault. It is reported as its own code rather than as PULSE_SPSS_SIDECAR_ABSENT because a diagnostic saying \"no sidecar found\" about a file sitting right there would be false. It does not say whether the file it skipped was stale, invalid or perfectly healthy: the flag suppresses the READ, so nothing on this path knows.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"IgnoreSidecar"},
				Hint:     "To use the sidecar's metadata after all, drop --ignore-sidecar and re-run — which will also report whether the sidecar is stale or invalid, since this warning cannot. If the flag was set to work around such a refusal, fix that instead: re-run the import that produced the cohort so the sidecar and the cohort agree, and the flag becomes unnecessary.",
				Examples: []any{false},
			},
		},
	},
	PULSE_SPSS_EXPORT_UNSUPPORTED: {
		Message: "Something in this cohort has no honest .sav representation, so the export stopped instead of writing a file that says something the cohort did not. Three things raise it: a set_* column with an empty dictionary (a response set with no member variable to name), a value the dictionary plan records no SPSS code for (or one two source values collapsed onto), and a rendered row stream handed to the .sav writer with no cohort behind it. The details name the offending column where there is one.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Source"},
				Hint:   "Export from a .pulse COHORT rather than from a row stream: `pulse export spss -i cohort.pulse -o out.sav`. The .sav writer encodes from the cohort's raw storage — dictionary IDs, set masks, the null bitmap — never from rendered cell text, so it needs the cohort itself. `pulse convert data.csv out.sav` reaches it by importing to an intermediate cohort first.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Schema"},
				Hint:   "If the details name a column, that column is the problem. An empty set_* dictionary has no members to emit — drop the column from the export or populate it. A value with no recorded SPSS code comes from a cohort whose categorical dictionary moved since import; re-import from the source .sav, or export with --ignore-sidecar to synthesise a fresh dictionary from the cohort's own text.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"TimeZone"},
				Hint:   "If details.option is --tz: a .sav DATETIME is a naive wall-clock count with no offset slot, so a non-UTC render zone cannot be honoured. Drop --tz (the .sav carries UTC wall-clock values), or export a text format with --tz for offset-bearing local literals.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"Target"},
				Hint:   "Any other output format sidesteps the question entirely: csv, tsv, ndjson, jsonarray, parquet, arrow and excel all take a rendered row stream. `pulse export parquet -i cohort.pulse -o out.parquet`.",
			},
		},
	},
	PULSE_SPSS_NAME_SANITIZED: {
		Message: "One or more cohort field names could not be an SPSS variable name — a space, a bracket, a hyphen, a leading digit, or a name past 64 bytes — and --sanitize-names rewrote them so the export could proceed. Every rename is listed under `renames` as {field, name} pairs in schema order. This is a WARNING, not a failure: the file was written, and its variables carry the rewritten names.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"SanitizeNames"},
				Hint:     "To keep the cohort's own names, drop --sanitize-names and rename the offending columns at the source instead — an SPSS name must open with a letter and carry only letters, digits and '.', '_', '$', '#', '@'. Without the flag the same names are a hard PULSE_SPSS_NAME_INVALID refusal, which is the default precisely because a silent rename is worse.",
				Examples: []any{false},
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"Labels"},
				Hint:   "If downstream syntax references the original names, keep the `renames` list: it is the only record of which emitted variable came from which cohort field. A sidecar-driven export never raises this — names that came from a .sav are legal by construction.",
			},
		},
	},
	PULSE_TIMEZONE_UNKNOWN: {
		Message: "The time-zone name is not one Pulse accepts. A zone is exactly \"UTC\" or an IANA Area/Location name (it contains a '/') resolved against the tz database embedded in the binary. Empty, \"Local\", abbreviations and legacy names without a '/' (EST, GMT, EST5EDT) and offset strings (+05:00) are refused because they depend on the host or name an ambiguous zone. The rejected name is under `tz` in the details.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"TimeZone"},
				Hint:     "Use an IANA Area/Location name such as America/New_York or Europe/Berlin (names are case-sensitive), or exactly UTC.",
				Examples: []any{"America/New_York", "Europe/Berlin", "UTC"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"TimeZone"},
				Hint:     "For a fixed offset with no daylight-saving rules use Etc/GMT±N — note the POSIX sign is INVERTED: Etc/GMT-5 is UTC+05:00 and Etc/GMT+5 is UTC-05:00.",
				Examples: []any{"Etc/GMT-5", "Etc/GMT+8"},
			},
		},
	},
	PULSE_FEATURE_PROFILE_INVALID: {
		Message: "The feature profile is structurally unusable, so pulse.New refused it before checking any feature name. The `reason` detail names the fault: the profile file is missing or unreadable, its body is not well-formed JSON, it carries a key the profile model does not declare (only `profile`, `written_with`, `features`, `behaviour`, `return` and `limits` exist, and each section is decoded strictly too), `features` is absent, a feature is listed more than once, a `limits` value is below -1 or its `request_timeout` is not a Go duration (`invalid_limits`; the key is under `limit`, the value as written under `value`), its `return` section does not resolve against the profile's own feature set (`invalid_return`, checked after names and dependencies: the resolver's code is under `return_code`, its details under `return` — a path only a feature the profile omits has is refused like a nonexistent one), both Options.FeatureProfile and Options.FeatureProfileFile were set, or pulse.ExampleFeatureProfile was given a name that is not a published example (`unknown_example`; the published names are under `examples`).",
		Fixups: []Fixup{
			{
				Action:   FixupRemoveParam,
				Path:     []string{"FeatureProfileFile"},
				Hint:     "Set exactly one of Options.FeatureProfile (a Go value) and Options.FeatureProfileFile (a JSON file read through the instance filesystem, relative to its root); setting both is refused rather than silently preferring one.",
				Examples: []any{"profiles/self-serve.json"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"features"},
				Hint:     "Give the profile a `features` array — it is required, may be empty, and must not repeat a name (every repeated name is listed under `duplicates`). Remove any key other than `profile`, `written_with`, `features`, `behaviour`, `return` and `limits`; for `invalid_limits`, set the key named under `limit` to 0 (default), -1 (no limit) or a positive value (`request_timeout` as a duration such as \"30s\"); for `invalid_return`, fix the `return` path, preset or precision named under `return`, or enable the feature that owns the path.",
				Examples: []any{`{"profile": "self-serve", "features": []}`},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"FeatureProfileFile"},
				Hint:   "For a `file_unreadable` reason, check that the path in the `path` detail exists under the instance filesystem root (DataDir, or the root of Options.FS) and is a readable file; for `malformed_json`, validate the body with a JSON linter.",
			},
		},
	},
	PULSE_FEATURE_PROFILE_UNKNOWN: {
		Message: "A feature name is not one this build knows, so pulse.New refused it. Every unknown name is listed under the `unknown` detail with a `reason`: `unregistered` (no built-in feature or registered extension operator has that name), `pattern` (wildcards such as `TEST_*` or `capability:*` are never expanded — list each feature), `wrong_kind` (operators are spelled bare, every other kind as `<kind>:<name>`; `did_you_mean` gives the one valid spelling), `core_surface` (inspect, predict, manifest and the other core surfaces are always present and are not features), or `newer_than_running` (the built-in feature arrived in the release named under `since`, after the running `version`). The same refusal covers an extension registration whose DependsOn names an unknown feature; those entries also carry `extension` and `category`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"features"},
				Hint:     "Replace each entry under `unknown` with an exact feature name: use `did_you_mean` for a `wrong_kind` entry, list the concrete names instead of a pattern, and drop core surfaces, which need no listing. `pulse manifest --json` lists the operator names this build registers.",
				Examples: []any{"AGG_SUM", "capability:process", "io_format:csv"},
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"features"},
				Hint:   "For a `newer_than_running` entry, remove the feature from the profile or upgrade Pulse to at least the release under `since`. For an unknown extension DependsOn entry, fix the name in the registration or register the extension it names.",
			},
		},
	},
	PULSE_FEATURE_PROFILE_DEPENDENCY: {
		Message: "The feature profile enables a feature without a feature it depends on, so pulse.New refused it. A feature's dependencies are an AND of any-of groups — an operator, for example, needs at least one request host (`capability:process`, `capability:compose` or `capability:process_chain`). Every unmet group is listed under the `unmet` detail, naming the enabled `feature` and the `requires_any_of` group; an extension registration's DependsOn entries are single-name groups. An instance Options value that acts on a feature is checked the same way: its `unmet` entry names the `option` instead of a `feature` (listed under `options`) — `Options.DefaultWeight` needs `capability:weighting`, `Options.DefaultMultiplicity` needs `capability:multiplicity`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"features"},
				Hint:     "For each entry under `unmet`, add at least one name from `requires_any_of` to the profile's `features`, or remove the `feature` that needs it.",
				Examples: []any{"capability:process"},
			},
			{
				Action:   FixupRemoveParam,
				Path:     []string{"Options.DefaultWeight"},
				Hint:     "For an `unmet` entry naming an `option` (Options.DefaultWeight, Options.DefaultMultiplicity), either add the required feature to the profile or leave that Options value unset.",
				Examples: []any{"capability:weighting"},
			},
		},
	},
	PULSE_WEIGHT_INVALID_ROWS: {
		Message: "Rows were left out of a weighted figure because their weight was not usable: null, negative, NaN or infinite, or not a whole number under `kind: frequency`. A zero weight is valid and simply contributes nothing. Invalid weights are never coerced — the excluded rows are counted, by reason, under `by_reason` {null, negative, nan_inf, non_integer_frequency}, with the total under `count` and the weight column under `field`. This is a WARNING: the figures were computed from the remaining rows. Under strict mode it is an error.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"weight", "field"},
				Hint:     "Check that `field` is the intended weight column; a column with many nulls or negatives is often not a weight at all.",
				Examples: []any{"wt"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"weight", "kind"},
				Hint:     "If every excluded row is `non_integer_frequency`, the column is a probability (sampling) weight rather than a replication count — set `kind` to probability.",
				Examples: []any{"probability"},
			},
		},
	},
	PULSE_WEIGHT_LOW_NEFF: {
		Message: "A weighted test ran on a group whose effective sample size is below what the test needs. Under weight kind `probability` the test's standard errors and degrees of freedom read Kish's effective n, n_eff = (Σw)²/Σw², not the row count — uneven weights shrink it, so a group with enough rows can still carry very little information. The test was computed, but treat its p-value as fragile. `test` and `type` name the test, `group` the short group (absent on an unsplit test), `n_eff` its effective n and `min_required` the floor the test applies to a row count. A weighted least-squares regression reports the same warning when its n_eff is at or below the predictor count + 1 (a GLM: below it) — its residual degrees of freedom N* − p − 1 are then zero or negative, so its standard errors, residual standard error, adjusted R² and p-values are undefined and reported as null; `regression` names the slot (its name, else `regressions[i]`) in place of `test` / `group`. This is a WARNING; under strict mode it is an error.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"split_by"},
				Hint:   "Split on a coarser categorical (fewer, larger groups), or widen the filters, so the short group gathers more rows.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"weight", "kind"},
				Hint:     "If the weights are integer replication counts rather than sampling weights, set `kind` to frequency: the test then reads Σw as its sample size.",
				Examples: []any{"frequency"},
			},
		},
	},
	PULSE_WEIGHT_UNSUPPORTED: {
		Message: "A row weight is in force on a slot whose operator cannot honour it, so the request is refused rather than answered unweighted or wrongly weighted. Three cases: the operator has a weighted form only under weight kind `frequency` (integer replication counts) and the weight in force is kind `probability` — `kind` names the kind in force and `supported_kinds` the kinds the operator accepts; the operator has no standard weighted form at all — a post-test (it reads aggregated rows), a regression resample or selection modifier, certain tests, attributes and overlays — and `reason` says why (`alternative` names a weighted twin where one exists); or the operator's weighted form has not landed yet. An aggregation over a decimal128 field is refused too, with the decimal column under `value_field`: the decimal path has no weighted form. An instance default weight refuses these slots exactly like an explicit one: each must opt out. The slot is under `slot`, its operator under `operator` and the weight column under `field`; the weight may come from the slot, the request or the instance default.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"weight"},
				Hint:     "Set `weight: null` on the refused slot (named in `slot`) to run that slot unweighted while the rest of the request stays weighted — null opts out, an absent key inherits the request weight and then the instance default.",
				Examples: []any{nil},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"weight", "kind"},
				Hint:     "When `supported_kinds` lists only `frequency` and your weights are integer replication counts, weight the slot with `kind: frequency` instead; a probability (sampling) weight has no standard form for that operator.",
				Examples: []any{"frequency"},
			},
			{
				Action: FixupReplaceOperator,
				Path:   []string{"type"},
				Hint:   "When `alternative` is present, use that operator: it computes the weighted form of the refused one.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"weight"},
				Hint:   "Or remove the request-level `weight` (and leave the instance default unset) if no slot of this request should be weighted.",
			},
		},
	},
	PULSE_EXTENSION_NOT_WEIGHT_AWARE: {
		Message: "A row weight reached an embedder-registered operator whose registration does not declare that it consumes weights, so it would have silently run unweighted: an explicit slot or request `weight` on an aggregator (an inherited instance default is skipped on an aggregator instead), or any weight in force — the instance default included — on an attribute, test or matrix operator. The slot is under `slot`, the operator under `operator` and the weight column under `field`.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"weight"},
				Hint:     "Set `weight: null` on the slot to run the operator unweighted explicitly.",
				Examples: []any{nil},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"WeightAware"},
				Hint:   "If you own the operator, read the row weight from extend.Record and declare WeightAware on its registration.",
			},
		},
	},
	PULSE_MULTIPLICITY_INVALID: {
		Message: "A `multiplicity` block cannot be honoured, so the request is refused before any record is read — predict refuses it identically. A block sets `method` (none, bonferroni, holm, bh, by), `family` (which p-values are corrected together) and `alpha` (the overlay significance level, inside (0, 1)); each falls through on its own from the slot to the request (a Compose slot: then the Compose request) to the instance default. Families by surface: a test or post-test takes `request` (default) or `compose`; a request overlay `layer` (default), `row`, `column`, `request` or `compose`; a Compose overlay `layer`, `row`, `column` or `compose`; a facet overlay `layer`, `row` or `column`. `row` and `column` need an overlay kind with a MATRIX payload, and `compose` exists only inside Compose. An inherited family the surface does not offer falls back to the surface default; only a family set on the slot itself, or one the request or Compose request cannot use anywhere, is refused. A test reads its own `alpha`, so `alpha` on a test's block is refused, and the Tukey HSD post-test is already corrected, so an explicit non-none method on it is refused (an inherited one skips it). The slot is under `slot`, the offending key under `key`, its value under `value`, the rule under `reason` and the accepted values, where a closed set applies, under `valid`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"multiplicity", "method"},
				Hint:     "Use one of the methods listed under `valid`: holm (family-wise error) or bh (false discovery rate) are the usual choices; none opts out.",
				Examples: []any{"holm", "bh"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"multiplicity", "family"},
				Hint:     "Use a family the slot's surface offers (listed under `valid`), or drop `family` to take the surface default: `request` for tests, `layer` for overlays.",
				Examples: []any{"layer", "request"},
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"multiplicity", "alpha"},
				Hint:   "Remove `alpha` from a test's block and set the test's own `alpha` instead; elsewhere pick a value strictly between 0 and 1.",
			},
			{
				Action:   FixupSetDefault,
				Path:     []string{"multiplicity", "method"},
				Hint:     "On the Tukey HSD post-test set `method` to none or drop the block: its p-values are already corrected for its own comparisons.",
				Examples: []any{"none"},
			},
		},
	},
	PULSE_MULTIPLICITY_CONFLICT: {
		Message: "Members of one `request` or `compose` correction family resolved to different methods, so the request is refused before any record is read — predict refuses it identically. A family is corrected by exactly one method: a `request` family pools one request's tests, post-tests and request-family overlays, a `compose` family every compose-family member across a Compose request's slots and its Compose overlays. Each member's method falls through from its own block to the request, the Compose request and the instance default; a member whose method resolves to none is not in the family. The family is under `family`, the distinct methods under `methods` and the first member slot holding each method under `slots`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"multiplicity", "method"},
				Hint:     "Set the same `method` on every member named under `slots`, or set it once on the request (or Compose request) and drop it from the members.",
				Examples: []any{"holm"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"multiplicity", "family"},
				Hint:     "Move a member that needs its own method into another family: an overlay can correct per `layer` instead.",
				Examples: []any{"layer"},
			},
		},
	},
	PULSE_OPTIMIZE_INVALID: {
		Message: "The optimize package refused the problem before solving it (or when a callback returned a result of the wrong shape): the start is empty or not finite, a callback is nil, a bound slice is not one entry per variable, a lower bound is above its upper, a tolerance (Acc, CostTol, StepTol) is negative or not finite, the iteration cap is negative, a constraint has no type, there are more equality constraints than variables, or (LeastSquares) the residual is empty, changes length or is not finite at the start, or the Jacobian has the wrong shape or is not finite at the start. Details carry `reason` (`nil_callback`, `empty_x0`, `bounds_length`, `bounds_crossed`, `acc`, `cost_tol`, `step_tol`, `max_iter`, `non_finite_x0`, `constraint_type`, `too_many_equalities`, `gradient_length`, `empty_residual`, `residual_length`, `jacobian_shape`, `non_finite_start`, `non_finite_jacobian`) and, where they apply, `index`, `constraint`, `row`, `want` and `got`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Fix the field named by details.reason: give every variable a finite start, size Lower / Upper to one entry per variable (or nil), keep each lower bound at or below its upper, set every constraint's Type to Eq or Ineq, and return a fixed-length residual and an m×n Jacobian that are finite at the start.",
			},
			{
				Action: FixupRemoveParam,
				Hint:   "Drop or merge redundant equality constraints so there are no more of them than variables.",
			},
		},
	},
	PULSE_OPTIMIZE_NOT_CONVERGED: {
		Message: "The optimize solver stopped before its convergence test passed — the iteration limit, a positive directional derivative in the line search, or a singular or rank-deficient subproblem — at a point that still meets the constraints. The final iterate is returned with the error. Details carry `status` (the solver's exit mode), `message`, `iterations` and `max_violation` (Minimize) or `cost` (LeastSquares, whose only short stop is the iteration limit, status 4).",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Hint:   "Raise MaxIter, or loosen Acc toward its 1e-6 default, when details.status is the iteration limit (9 from Minimize, 4 from LeastSquares).",
			},
			{
				Action: FixupReplaceField,
				Hint:   "Start from a different x0, rescale the variables to similar magnitudes, or check that the gradient matches the objective: a wrong or noisy gradient is the usual cause of a positive directional derivative (status 8).",
			},
		},
	},
	PULSE_OPTIMIZE_INFEASIBLE: {
		Message: "The optimize solver could not satisfy the constraints: the linearised constraints were incompatible, the equality constraints were singular, or the run stopped with a constraint violated by more than ten times the accuracy goal. The final iterate is returned with the error. Details carry `status`, `message`, `iterations` and `max_violation`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Check the constraints and bounds admit a common point: loosen a bound or an inequality, or remove an equality that contradicts another.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "Start nearer the feasible region, or check each constraint's Grad matches its Fn.",
			},
		},
	},
	PULSE_MCMC_INVALID: {
		Message: "The mcmc package refused the run before sampling: the model is nil or has dimension below 1, Chains, Warmup, Draws, MaxTreeDepth or Workers is negative, TargetAccept is outside (0, 1), Init is not one finite value per dimension, or the log density or its gradient is not finite at Init. Details carry `reason` (`nil_model`, `dim`, `chains`, `warmup`, `draws`, `max_tree_depth`, `workers`, `target_accept`, `init_length`, `init_not_finite`, `init_density_not_finite`) and, where they apply, `chain`, `index`, `got` and `want`. No result is returned.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Fix the option named by details.reason: leave a count at 0 for its default or make it positive, keep TargetAccept strictly between 0 and 1, and size Init to the model's Dim() with finite values.",
			},
			{
				Action: FixupRemoveParam,
				Hint:   "Drop Init (or move it inside the model's support) when details.reason is init_density_not_finite, so each chain draws its own finite start.",
			},
		},
	},
	PULSE_MCMC_FAILED: {
		Message: "The mcmc sampler could not run on the model: no Uniform(-2, 2) starting point had a finite log density and gradient (`init_not_found`), the step-size heuristic grew without bound (`step_size_unbounded`, an improper posterior) or collapsed to zero (`step_size_zero`, a discontinuous density or a wrong gradient), or the adapted metric was not finite (`metric_not_finite`). Details carry `reason` and `chain`. No result is returned.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Supply Options.Init at a point inside the support when details.reason is init_not_found.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "Check the model: give every parameter a proper prior (step_size_unbounded) and verify the gradient against finite differences (step_size_zero, metric_not_finite).",
			},
		},
	},
	PULSE_OPTIMIZE_SPEC_INVALID: {
		Message: "A ComposedRequest `optimize` block is malformed, so nothing runs — predict refuses it identically. Every block needs a `name` unique among the blocks (reason `name_missing`, `name_duplicate`), at least one variable (`variables_missing`), a non-empty `objective` (`objective_missing`) and a `direction` of `maximize` or `minimize` (`direction_missing`, `direction_unknown`). Every constraint needs a non-empty `expr` (`constraint_expr_missing`) and a `type` of `eq` (expr = 0) or `ineq` (expr >= 0) (`constraint_type_unknown`), and there may be no more eq constraints than variables (`too_many_equalities`). `starts`, when set, is 1 to 1000 (`starts_invalid`; absent is 8); `tolerance` is a finite number >= 0 (`tolerance_invalid`; 0 is 1e-6) and `max_iterations` >= 0 (`max_iterations_invalid`; 0 is 100). The block index is under `optimize`, the offending path under `field`, the rule under `reason` and, where they apply, the value under `value` and the accepted values under `valid`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"optimize", "*", "direction"},
				Hint:     "Name the block, give it at least one variable and a non-empty objective, and set `direction` to maximize or minimize.",
				Examples: []any{"maximize", "minimize"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"optimize", "*", "constraints", "*", "type"},
				Hint:     "Give every constraint an `expr` and a `type` of eq (expr = 0) or ineq (expr >= 0); keep the eq constraints no more numerous than the variables.",
				Examples: []any{"eq", "ineq"},
			},
			{
				Action:   FixupSetDefault,
				Path:     []string{"optimize", "*", "starts"},
				Hint:     "Drop `starts` for the default 8, or set 1 to 1000; drop `tolerance` / `max_iterations` for the solver defaults.",
				Examples: []any{8, 32},
			},
		},
	},
	PULSE_OPTIMIZE_VARIABLE_INVALID: {
		Message: "A decision variable of a ComposedRequest `optimize` block cannot be solved over, so nothing runs — predict refuses it identically. Every variable needs a `name` (reason `name_missing`) that is an identifier ([A-Za-z_][A-Za-z0-9_]*, `name_invalid`), is not `params` nor an expression function's name (`name_reserved`) and is unique within the block (`name_duplicate`). `lower` and `upper` are both required (`bound_missing`): a number or `\"params.<name>\"` naming a batch param (an identifier, `bound_invalid`) that holds a finite number (`bound_not_numeric`; an undeclared param is PULSE_PARAMS_UNDECLARED). `lower` may not exceed `upper` (`bounds_crossed`; equal fixes the variable), and a `start`, when set, lies within them (`start_out_of_bounds`; absent starts at the midpoint). The block index is under `optimize`, the path under `field`, the rule under `reason`, the variable under `variable` and, where they apply, `param`, `lower`, `upper` and `value`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"optimize", "*", "variables", "*", "name"},
				Hint:   "Give every variable a unique identifier name that is neither `params` nor a function name (hill, exp, coef, …).",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"optimize", "*", "variables", "*", "lower"},
				Hint:     "Set both bounds — numbers or \"params.<name>\" references to numeric batch params — with lower <= upper, and keep any start between them.",
				Examples: []any{0, "params.budget_floor"},
			},
		},
	},
	PULSE_OPTIMIZE_EXPRESSION_INVALID: {
		Message: "An `optimize` block's objective or constraint expression cannot be evaluated, so nothing runs — predict refuses it identically. The expressions read the block's decision variables, `params.<name>` (the batch params), coef(fit, term[, stat]) over the batch's `fits` and the expression built-ins (hill(), exp(), log(), pow(), …); no record field is in reach. Reason `unknown_identifier`: the expression reads a name that is none of those (the name is under `identifier`, the decision variables under `valid`). Reason `compile_error`: the expression does not compile (expr-lang's reason under `compile_error`). Reason `not_numeric`: it yields something other than a number (the type under `result_type`). An all-literal coef() call naming an unknown fit, term or stat is PULSE_MODEL_UNKNOWN / PULSE_MODEL_TERM_UNKNOWN / PULSE_MODEL_STAT_UNKNOWN, located the same way. The block index is under `optimize`, the path under `field` and the source under `expression`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"optimize", "*", "objective"},
				Hint:   "Read only the block's variables, params.<name>, coef() and the built-ins; declare a missing name as a variable, or pass a constant through the batch `params`.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"optimize", "*", "constraints", "*", "expr"},
				Hint:   "Write every constraint as a numeric expression: eq holds when it is 0, ineq when it is >= 0 (`params.budget - (tv + search)` for a spend cap).",
			},
		},
	},
	PULSE_MATRIX_SINGULAR: {
		Message: "A matrix could not be factored, solved against, inverted or decomposed. Usually it is singular or not positive definite: a Cholesky pivot came out zero or negative, or its condition number is past the solver's ceiling. In data terms some variables are exact (or near-exact) linear combinations of others — a duplicated or constant column, a one-hot set with every level included, or fewer usable rows than variables. The same code covers a decomposition (eigen, SVD, QR, rank, condition number) handed a NaN or infinite element (details `reason` = `non_finite`) or whose iteration did not converge (`reason` = `no_convergence`). Details carry `reason` where the routine classifies the failure (also `not_positive_definite`, `ill_conditioned`), `pivot` for a reference Cholesky failure, `attempts` / `ridge` when a ridge schedule was exhausted, and `condition_number` from the SPD solve / inverse. A failed Cholesky factorisation (reference or SPD) of a finite matrix also carries `rank` (under the rank tolerance), `condition_number` (null when rank-deficient) and, when the matrix is rank-deficient, `dependent_indices` — the axis positions in the linear dependency; a matrix operator adds `matrix` and names those members as `dependent_fields`. An operator whose other outputs stay defined may raise it as a per-matrix WARNING instead, naming the figures it nulls under `outputs`.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Hint:   "Drop one of the members listed in details.dependent_fields (or at details.dependent_indices): they form a linear dependency, so any one of them is redundant given the others.",
			},
			{
				Action: FixupRemoveParam,
				Hint:   "Drop the redundant variable: a column that is constant, duplicated, or a linear combination of the other inputs.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "Supply more rows than variables, or filter out the rows that make the inputs collinear.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "For details.reason non_finite or no_convergence: remove (or impute) the NaN / infinite values before the matrix is built, and rescale variables whose magnitudes differ by many orders so the decomposition can converge.",
			},
		},
	},
	PULSE_MATRIX_SHAPE_MISMATCH: {
		Message: "Linear-algebra operands have dimensions that do not fit together: a matrix that must be square is not, rows have different lengths, a vector's length differs from the matrix order, the data length is not rows times columns, an operand is missing, or two accumulators of different shape or missing-value mode were merged. Details name the offending dimensions.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Pass operands whose dimensions agree: the same variable list, in the same order, on both sides.",
			},
		},
	},
	PULSE_VECTOR_INVALID: {
		Message: "A `vectors` entry is malformed, so the request is refused before any record is read — predict refuses it identically. Each entry needs a non-empty `name` and exactly one of `fields` (literal member names, or glob entries using `*`, `?` or `[...]`) and `pattern` (a regular expression over field names); every `fields` entry must be non-empty, every glob and regular expression must compile, and `coerce`, when set, must be a known value. The entry's path is under `vector`, its name under `name`, the rule under `reason` and, where one applies, the offending value under `value` and the accepted values under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "pattern"},
				Hint:   "Keep exactly one of `fields` and `pattern` on the vector: list the members, or match them with one regular expression.",
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"vectors", "*", "coerce"},
				Hint:     "Set `coerce` to one of the values under `valid`, or drop it.",
				Examples: []any{"binary"},
			},
		},
	},
	PULSE_VECTOR_EMPTY: {
		Message: "A vector resolved to no field: none of its `fields` entries or its `pattern` matched a schema field, so it has no members. A `pattern` is a Go regular expression matched against every field name (unanchored); a glob entry in `fields` matches whole names. The entry's path is under `vector`, its name under `name` and the expression that matched nothing under `fields` or `pattern`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "pattern"},
				Hint:   "Inspect the cohort's field names and correct the pattern or glob so it matches the intended columns.",
			},
		},
	},
	PULSE_VECTOR_MEMBER_TYPE: {
		Message: "A vector member has a field type a vector cannot carry. Members must be integer (u4, u8, u16, u32, u64) or float (f32, f64) fields. A packed_bool member is admitted only with `coerce: \"binary\"` (read as 0 / 1). Categorical, set, date, datetime and decimal128 members are always refused: a category code or a date is not a measurement, and decimal128 would lose precision in the floating-point accumulators. The vector is under `vector` / `name`, the member under `field` and its type under `field_type`.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"vectors", "*", "coerce"},
				Hint:     "For a packed_bool member, set `coerce` to binary to read it as 0 / 1.",
				Examples: []any{"binary"},
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "Remove the member named under `field`, or narrow the pattern so it matches only numeric fields.",
			},
		},
	},
	PULSE_VECTOR_DUPLICATE: {
		Message: "A vector name or member is repeated. Vector names are unique per request (the clashing entries are under `indices`), and one vector lists each member once after its glob or pattern entries expand (the vector is under `vector` / `name`, the repeated member under `field`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "name"},
				Hint:   "Rename one of the clashing vectors, or merge them into one.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "Drop the literal entry a glob already covers, or narrow the glob so the member appears once.",
			},
		},
	},
	PULSE_VECTOR_FIELD_NOT_SCALAR: {
		Message: "A native vector field (vec_f32 / vec_f64) was named where a single value is read — a scalar aggregation, grouper, attribute, feature, filter, test or regression slot (or an operator param naming a field), a window field / order_by / partition_by, a sort key, a weight, a date / zone slot or a point-lookup index key. A vector has dim values per row, so there is no one number to read. `details.field` / `field_type` / `dim` describe the field and `details.slot` the slot; `details.suggest`, when present, names the vector operator that replaces the slot. A slot that asks only whether the field answered (a count of present or null rows, a null filter) accepts a vector field, as does every vector slot.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"aggregations", "*", "type"},
				Hint:     "Aggregate the whole vector with a vector aggregator naming it under `vector` (drop `field`): AGG_VEC_MEAN for the element-wise mean, AGG_VEC_SUM for the element-wise sum, AGG_VEC_SD for the element-wise sample standard deviation.",
				Examples: []any{"AGG_VEC_MEAN", "AGG_VEC_SUM", "AGG_VEC_SD"},
			},
			{
				Action:   FixupReplaceOperator,
				Path:     []string{"groups", "*", "type"},
				Hint:     "Group rows by the vector with GROUP_KMEANS naming it under `vector` (drop `field`); a vector has no category or range to bucket.",
				Examples: []any{"GROUP_KMEANS"},
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "To read ONE element as a number, declare a virtual vector whose `fields` name it as `field[k]` (1-based) and use a vector slot or an expression over it; scalar slots read scalar columns only. Matrices (MAT_*), vector tests and vector attributes take the field directly under `vector`.",
			},
		},
	},
	PULSE_VECTOR_NAME_COLLISION: {
		Message: "A vector's name is also a schema field's name, or a feature output, attribute or window label of the same request. An expression (a formula attribute, an expression filter, a total() argument) reads a named vector as an identifier beside those columns, so a vector may not share one's name. The vector is under `vector` / `name`; a field under `field`, a derived column under `label` with its slot (`feature`, `attribute`, or `window` / `window_index`). `details.native` is true when the field is a native vector field (vec_f32 / vec_f64): it is already a vector every slot and expression can name directly, and a request never shadows it.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "name"},
				Hint:   "Rename the vector to a name no schema field or output label uses (for example add a `_vec` suffix), and update every slot and expression that names it.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*"},
				Hint:   "When `details.native` is true, drop the vectors entry and name the native vector field directly in the slot or expression; to re-kind, re-label or re-scope it, keep the entry under a different name whose `fields` list the native field (or single elements as `field[k]`, 1-based).",
			},
		},
	},
	PULSE_VECTOR_LABELS_MISMATCH: {
		Message: "A vector's `labels` count differs from its resolved member count: labels are matched to members by position, one each. The vector is under `vector` / `name`, the two counts under `labels` and `members`, and the resolved member list under `resolved` — run predict to see `resolved_vectors` before writing labels for a glob or pattern.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "labels"},
				Hint:   "Supply exactly one label per member listed under `resolved`, in that order.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "labels"},
				Hint:   "Drop `labels` to use the member field names as labels.",
			},
		},
	},
	PULSE_VECTOR_UNKNOWN: {
		Message: "An operator slot names a vector the request's `vectors` does not define. The unknown name is under `vector`, the slot under `slot` and the names the request does define under `defined`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors"},
				Hint:   "Define the vector under `vectors`, or reference one of the names listed under `defined`.",
			},
		},
	},
	PULSE_VECTOR_UNREFERENCED: {
		Message: "Warning: a vector is defined but no operator slot references it, so it is resolved and validated but computes nothing. Its name is under `name`.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors"},
				Hint:   "Reference the vector from an operator slot, or remove it from `vectors`.",
			},
		},
	},
	PULSE_VECTOR_METRIC_UNKNOWN: {
		Message: "A distance or similarity metric was refused: the name is not registered, the metric has no form in the direction the operator needs (only cosine has both a similarity and a distance form; no other metric is converted between the two), or the operator does not accept it. The metric is under `metric`, the cause under `reason`, the direction under `form` and the accepted names under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"params", "metric"},
				Hint:   "Use one of the metric names listed under `valid`; for the opposite direction pick a metric registered in that direction rather than converting one.",
			},
		},
	},
	PULSE_VECTOR_METRIC_REQUIRED: {
		Message: "An operator that compares rows needs a distance or similarity metric and was given none, with no default to fall back on. The direction it needs is under `form` and the accepted names under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"params", "metric"},
				Hint:   "Set `metric` to one of the names listed under `valid`.",
			},
		},
	},
	PULSE_VECTOR_METRIC_UNSUITED: {
		Message: "An explicitly named metric fits the declared kind of the vector it reads poorly: cosine ignores magnitude and level, which a scale or measure vector's values carry, and the Euclidean family ignores that a composition's parts sum to a whole. It is a warning (an error under strict mode); the run uses the metric as named. The vector is under `vector`, its kind under `kind`, the metric under `metric`, the request path under `slot` and the kind's default in that direction under `suggested`. A defaulted metric never raises it.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"params", "metric"},
				Hint:   "Use the metric under `suggested`, or drop `metric` and let the vector's kind pick it; in an expression, cosine(vcenter(a), vcenter(b)) is the correlation a scale vector suits.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "kind"},
				Hint:   "If the kind is wrong (the values are not what it says), correct the vector's `kind`.",
			},
		},
	},
	PULSE_VECTOR_REFERENCE_UNKNOWN: {
		Message: "A reference vector could not be resolved: ref(\"table\", key) or a {table, key} reference names a table not registered in Options.Extensions.ReferenceVectors, its table argument is not a string literal (the table's labels must be known before any row), the key names no profile of the table, or a comparison slot's {population: name} reference names no bound vector fit (reason `population_unknown`, the name under `population`). The cause is under `reason`, the table under `table`, the key under `key` and the registered names (the table's keys, or the bound vector fits) under `valid`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"expression"},
				Hint:   "Name a table and key listed under `valid` (the manifest's extensions.reference_vectors lists every table with its labels and keys), and spell the table as a string literal.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "reference", "population"},
				Hint:   "For reason population_unknown, name a fit listed under `valid`, or bind it: a request `populations` entry, a registered population, or a `fits` entry of the enclosing compose request.",
			},
		},
	},
	PULSE_VECTOR_REFERENCE_MISMATCH: {
		Message: "A reference vector's labels do not match the vector it is compared with. A reference is matched by label, never by position: the two label sets must be equal (reason `labels_differ`; `missing` lists the vector's labels the reference lacks, `extra` the reference's labels the vector lacks), and a reference cannot be compared with a vector that carries no labels, such as a literal array (reason `unlabeled_partner`). A fitted vector attribute's bound fit (`params.fit`, named under `fit`) and a comparison slot's {population} reference (named under `population`) align to the slot's member labels by the same rule. A comparison slot's {record} reference is read by member COLUMN name instead: its cohort (under `cohort`) must hold every member as an integer, float, packed_bool or vector-element column (reason `record_members_absent`; the members it lacks under `missing`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "labels"},
				Hint:   "Give the compared vector labels equal to the reference's `reference_labels` (any order), or compare with a reference table whose labels are the vector's.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "labels"},
				Hint:   "For a bound fit (`fit` in details), label the slot's inline fields with params.labels equal to the fit's `reference_labels`, or score a vector whose labels are the fit's.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"expression"},
				Hint:   "For `unlabeled_partner`, compare ref() with a named vector (Request `vectors`), another ref(), or vcenter / vzscore / vnormalize of one, not a literal array.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "reference", "record", "cohort"},
				Hint:   "For `record_members_absent`, look the record up in a cohort that carries the slot's member columns (`missing`) as numeric columns, or compare only members it holds.",
			},
		},
	},
	PULSE_MATRIX_UNSUPPORTED_SOURCE: {
		Message: "A `matrices` slot sits on a request whose rows the matrix operators cannot fold, so the request is refused before any record is read — predict refuses it identically. Matrices read cohort rows directly: a Request that also carries `joins` (details `source` = `join`) and a ProcessChain stage after stage 0 (details `source` = `chain_stage`, which reads the previous stage's output rows; the stage is under `stage` / `stage_name`) are not supported. The number of matrix slots is under `matrices`.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"matrices"},
				Hint:   "Move the `matrices` slot into its own Request over the cohort, without `joins`; Compose can run it beside the joined request.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"stages", "*", "request", "matrices"},
				Hint:   "Put the matrices on chain stage 0, which reads the cohort, or run them as a separate Process request.",
			},
		},
	},
	PULSE_MATRIX_HOST_CONFLICT: {
		Message: "A `matrices` slot sits on a Request whose host cannot carry it: a Request with a `crosstab` (details `host` = `crosstab`) does not compute matrices. The request is refused before the crosstab dispatch — predict refuses it identically. The number of matrix slots is under `matrices`.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"matrices"},
				Hint:   "Run the matrices as a separate Request without `crosstab`; Compose can carry both requests in one call.",
			},
		},
	},
	PULSE_MATRIX_NOT_PSD: {
		Message: "A pairwise matrix (`params.missing` = `pairwise`) is not positive semidefinite: each pair was computed over its own rows, and together the figures are inconsistent — no single data set produces them. Detected with the reference Cholesky on the matrix scaled by its diagonal, with a 1e-10 tolerance; details name the failing pivot (`pivot` axis index, `member`). On the covariance and correlation operators it is a warning and the matrix is returned unchanged; downstream methods that need a valid covariance or correlation matrix will fail or mislead on it. On a decomposition operator (partial correlation) it is FATAL unless `params.repair` is `nearest`, which replaces the input by its nearest correlation matrix (Higham alternating projections, Matrix::nearPD) and keeps this code as a warning carrying `frobenius_adjustment`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"matrices", "*", "params", "repair"},
				Hint:   "On a decomposition operator (partial correlation), set `repair: \"nearest\"` to run on the nearest correlation matrix; the warning reports how far it moved (`frobenius_adjustment`).",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"matrices", "*", "params", "missing"},
				Hint:   "Use `missing: \"listwise\"` so every pair shares the same complete rows (a listwise matrix is always positive semidefinite).",
			},
		},
	},
	PULSE_MATRIX_LISTWISE_HEAVY_DROP: {
		Message: "Listwise deletion dropped more of the filter-passing rows than `params.max_drop_share` allows: every row with any member null is dropped, so the matrix describes a smaller, possibly unrepresentative subset. Details carry `dropped`, `rows`, `share` and `max_drop_share`. Emitted only when `max_drop_share` is set.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"matrices", "*", "params", "max_drop_share"},
				Hint:   "Switch to `missing: \"pairwise\"` (and drop `max_drop_share`) to keep every row a pair can use, or remove the member with the most nulls from the vector.",
			},
		},
	},
	PULSE_MATRIX_INSUFFICIENT_N: {
		Message: "A matrix or, under pairwise deletion, one or more pairs rest on fewer than 2 rows or on no weight mass, so their cells are undefined (null) or degenerate. Details: `scope` `matrix` with `n` and `sum_weights`, or `scope` `pairs` with `pairs` [{row, col, n}]. Scope `bartlett` (a PCA or factor analysis) and `likelihood` (a maximum-likelihood factor analysis, which needs N* above 1 + (2p + 5)/6 + 2k/3) carry `n_star` and `min_n_star`: the inference size is too small for that test, whose figures are null. A centroid-distance matrix adds scope `buckets` (`buckets`: buckets with no weighted complete row, whose centroids and distances are null) and scope `pooled` (`n_star`, `buckets`: a mahalanobis distance with no more rows than buckets, so every distance is null).",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"filters"},
				Hint:   "Widen the filters (or the group bucket) so more rows reach the matrix, or drop the sparse member from the vector.",
			},
		},
	},
	PULSE_MATRIX_ZERO_VARIANCE: {
		Message: "One or more members have zero spread over the rows that reach them (details `members`): every correlation touching such a member is null, its covariance row and column are 0, and the determinant is null.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "Drop the constant member from the vector or the inline `fields`.",
			},
		},
	},
	PULSE_VECTOR_REFERENCE_MISSING: {
		Message: "A reference-comparison attribute slot's per-row reference found nothing for some rows. A {table, key_field} reference: the key was null (`n_key_null`) or its value is not a key of the table (`n_key_unmatched`; `keys` lists up to 10 of them). A {centroid: {by}} reference (`reference` \"centroid\", the column under `by`): the by value was null or no row the centroid filter admits carries it; a by-less centroid that no row reached misses every row. Those rows score null; `n_reference_missing` counts them. A runtime warning, raised with or without components.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Attributes", "*", "Params", "reference"},
				Hint:   "Register a profile for each value in details.keys in the Extensions.ReferenceVectors table (an unsigned-integer key_field matches by its base-10 form, e.g. \"7\", never \"07\"), filter the rows out first, or compare against one profile with {table, key}.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "reference", "centroid", "filter"},
				Hint:   "For a centroid reference, widen centroid.filter so it admits rows of every by value in details.keys (or drop by for one cohort centroid), or filter the unmatched rows out of the request.",
			},
		},
	},
	PULSE_VECTOR_METRIC_DOMAIN: {
		Message: "A reference-comparison attribute's metric reads values only within its domain (hellinger / aitchison: non-negative parts, aitchison no zero; a set metric: 0/1 values), and some rows hold values outside it. Those rows score null; details carry `attribute`, `type`, `metric`, `n_domain_invalid` (the rows) and `reasons`. A runtime warning, raised with or without components.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Attributes", "*", "Params", "metric"},
				Hint:   "Pick a metric defined on the members' values (euclidean or manhattan for signed values, cosine for direction), or recode the members first (replace aitchison zero parts, dichotomise for a set metric).",
			},
		},
	},
	PULSE_MATRIX_METRIC_DOMAIN: {
		Message: "MAT_DISTANCE's metric reads non-negative values (hellinger, aitchison) and some bucket centroids hold a negative coordinate, or an aitchison zero part whose log-ratio is undefined. The distances touching those centroids are null; details carry `matrix`, `metric`, `pairs` (the refused centroid pairs), `buckets` and `reasons` (`negative`, `zero`).",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"matrices", "*", "params", "metric"},
				Hint:   "Drop `params.metric` (euclidean) or pick a metric defined on signed values (manhattan, cosine) for members that can be negative; for aitchison, replace zero parts before comparing compositions.",
			},
		},
	},
	PULSE_MATRIX_NOT_CONVERGED: {
		Message: "An iterative matrix routine stopped at its documented iteration cap before meeting its convergence tolerance, so the figures it returned are its last iterate, not an optimum. Raised as a per-matrix warning by the one-factor minres fit behind reliability ω, by the nearest-correlation repair (`params.repair` = `nearest`, beside its PULSE_MATRIX_NOT_PSD warning; the repaired matrix is still positive definite but may not be the nearest), by a factor analysis's extraction (`solver` `fa_minres` / `fa_pa` / `fa_ml`; principal axis stops after psych's 50 iterations) and by a loading rotation (`varimax` / `gpa_oblimin`); a fitted factor or component score attribute raises it fit-scoped (`details.scope` `fit`) and still scores every row from the last iterate. A k-means grouper raises it as a response warning (`solver` `kmeans_lloyd`, `group` naming its key column in place of `matrix`) when its best start reaches `params.max_iter`; its clusters are the last iterate — raise `max_iter`. Details carry `matrix`, `solver`, `iterations`, `max_iterations` and `tolerance`. The cap and tolerance are fixed constants, not request knobs. A structural-equation bootstrap (`params.resample`) whose replicates refit fewer than 10 times FAILS its slot alone with this code (`details.reason` `bootstrap_too_few`, with `bootstrap_requested`, `bootstrap_attempted`, `bootstrap_succeeded` and `minimum`).",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"matrices", "*", "params", "resample"},
				Hint:   "Under `details.reason` `bootstrap_too_few`, the model rarely refits on resampled rows: drop `resample` for the analytic standard errors, or simplify the model (fewer free parameters) and rerun.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "Non-convergence usually means a member barely relates to the others or two members are near-duplicates: drop the weakest or redundant member from the vector and rerun.",
			},
			{
				Action: FixupSetDefault,
				Path:   []string{"matrices", "*", "params", "missing"},
				Hint:   "Under `missing: \"pairwise\"`, switch to `missing: \"listwise\"`: a listwise matrix is internally consistent, which removes the usual cause of a slow repair or fit.",
			},
		},
	},
	PULSE_MATRIX_HEYWOOD: {
		Message: "A factor fit gave at least one member a uniqueness at or below zero (a Heywood case: the member's loading is at least its own standard deviation, an impossible negative error variance). A reliability's one-factor fit withholds the figure built on it (`omega` is null; alpha and the item statistics are unaffected); a factor analysis keeps its loadings as fitted, never repaired, so read them with care; its maximum-likelihood extraction holds each uniqueness at or above 0.005, so there a member held at that floor is the Heywood case. Details carry `matrix`, `output`, `members` and `uniquenesses`. A factor score attribute raises it fit-scoped (`details.scope` `fit`): its regression scores use the loadings as fitted, while its Bartlett scores, whose weights divide by each uniqueness, are all null when one is at or below zero.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "A Heywood case usually means a member is almost a copy of another, or the battery has too few items to pin the factor down: drop the near-duplicate member, or add items, and rerun.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "factors"},
				Hint:   "In a factor analysis, extract one factor fewer: too many factors for the battery is the other usual cause.",
			},
		},
	},
	PULSE_MATRIX_NOT_IDENTIFIED: {
		Message: "A model the operator fits is not identified on this many members, so the figure it would give is withheld and null. A reliability's `omega` needs a one-factor fit, which needs at least 3 items (2 items have one correlation for two loadings); alpha and the item statistics still compute (details `matrix`, `output`, `members`, `min_members`). A factor analysis's k factors need (p - k)^2 >= p + k (1 factor needs 3 members, 2 need 5, 3 need 6), so its loadings and fit figures are null; KMO and Bartlett's test still compute (details `matrix`, `output`, `members`, `factors`, `max_factors`). A factor score attribute raises it fit-scoped (`details.scope` `fit`) and nulls every score, also when its `factors` \"kaiser\" keeps fewer factors than the `factor` it scores.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "Add a third item to the vector to get omega; with two items, read `alpha` (Spearman-Brown for two items) instead.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "factors"},
				Hint:   "In a factor analysis, set params.factors to at most details.max_factors, or add members to the vector.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "params", "factors"},
				Hint:   "On a factor score, set params.factors to an identified count of at least params.factor, or score a factor the Kaiser rule keeps.",
			},
		},
	},
	PULSE_MATRIX_NEAREST_PD: {
		Message: "A structural equation model under `singular` \"nearest\" found its sample covariance not positive definite (fewer rows than members, a linear dependency among them, or a zero-spread member of a standardized vector) and was fitted to semopy's substitute, statsmodels' cov_nearest with threshold 0.01: the correlation form's eigenvalues below 0.01 raised to it, the variances kept (a zero-spread standardized member enters at unit variance, uncorrelated). Every figure, the residual covariance included, is against the adjusted matrix, not the data's. Details carry `matrix`, `n`, `p` and `min_eigenvalue` (the correlation form's smallest eigenvalue before the adjustment).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"vectors", "*", "fields"},
				Hint:   "Fit on more rows than members, or drop a member that is constant or a combination of others, so the sample covariance is positive definite.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "singular"},
				Hint:   "Set params.singular to \"refuse\" to get PULSE_MATRIX_SINGULAR instead of a fit to an adjusted matrix.",
			},
		},
	},
	PULSE_MATRIX_DEFINED_NONFINITE: {
		Message: "A structural equation model's defined parameter (a `name := expr` line of params.model) is not finite at the fitted estimate — a division by zero, or the log or square root of a value outside its domain — so its estimate-table row has every figure null (estimate, std_estimate, std_err, z_value, p_value, ci_lower, ci_upper), as does every := line reading it; the fit and every other row stand. Details carry `matrix` and `defined` (the names, in model order). In a grouped fit (params.group) a := reading the per-group label of a level with no rows after the filters warns once per such label with `reason` \"empty_group\", `label`, `level`, `group_field` and `defined`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "model"},
				Hint:   "Rewrite the := expression so it is defined at the estimate: divide by a parameter that is not zero, and take log / sqrt of a positive quantity (abs() or a square first).",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"filterers", "*"},
				Hint:   "For reason empty_group: keep the level the := reads in the filtered rows, or rewrite the := over the labels of levels that hold rows.",
			},
		},
	},
	PULSE_MATRIX_GROUP_LEVEL_EMPTY: {
		Message: "A multi-group structural equation model (params.group) has group-field levels — a categorical's dictionary entries, packed_bool's 0 / 1, a derived group's params.group_levels entries — that hold no rows after the filters. Each such level is no group: no estimate rows, no chi2_group_ / n_group_ scalars, and a := line reading its per-group label is null (PULSE_MATRIX_DEFINED_NONFINITE, reason empty_group); the fit over the other levels stands. Emitted once per slot. Details carry `matrix`, `group_field` and `levels` (the empty levels, in level order). An attribute scoring rows by such a fit, fitted inline, warns the same over the known levels without fit rows (`attribute` in place of `matrix`; a row of such a level scores null); under Strict that fails the run.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"filterers", "*"},
				Hint:   "Widen the filters so every level the model names keeps rows, or accept the fit over the levels that hold rows.",
			},
		},
	},
	PULSE_MATRIX_GROUP_LEVEL_UNDECLARED: {
		Message: "A multi-group structural equation model grouped by a column the request computes (params.group naming an attribute, feature or window label) met rows whose group value is no level of the fit: not among params.group_levels (matched exactly), or not an integral number. Each such row is dropped listwise (counted in n_listwise_dropped) and joins no group; the fit over the levels that hold rows stands. Emitted once per slot. Details carry `matrix`, `group_field` and `count` (the rows dropped so). An attribute scoring rows by such a fit (inline, or bound to a fit grouped by a computed column) scores each such row null and warns once with `attribute` in place of `matrix` (`fit` when bound) and the scored rows' `count`; under Strict that fails the run.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "group_levels"},
				Hint:   "Declare every value the computed group takes that should be a group, or omit group_levels to group by every integral value observed.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"filterers", "*"},
				Hint:   "Filter out the rows whose group value is no level, so the drop is explicit.",
			},
		},
	},
	PULSE_MATRIX_GROUP_LEVEL_LIMIT: {
		Message: "A multi-group structural equation model grouped by a column the request computes, without params.group_levels, takes its levels from the data — every distinct integral value — and its rows held more than 256 of them. The fit keeps one moment state per level, so a group that is a continuous measure or a row identifier is refused, never fitted; the slot fails alone. Details carry `matrix`, `reason` (observed_levels), `group_field` and `limit` (256). An attribute scoring rows by such a fit, fitted inline, nulls every score with this fit-scoped warning (`attribute` in place of `matrix`).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "group_levels"},
				Hint:   "Declare the levels the fit compares in params.group_levels; rows whose value is no declared level are dropped listwise instead of opening a group.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"attributes", "*", "expression"},
				Hint:   "Compute a group with a few integer levels (an ATTR_FORMULA mapping each row to its level), not the raw measure.",
			},
		},
	},
	PULSE_MATRIX_TOO_MANY_CATEGORIES: {
		Message: "A structural equation model fitted by an ordinal estimator (params.estimator \"dwls\" or \"wlsmv\") declares a member ordered whose rows hold more than 32 distinct values — one threshold per category boundary, and an asymptotic covariance growing with them — so the member is a continuous measure, not a rating scale, and the slot fails alone. Details carry `matrix`, `variable`, `categories` (its distinct values) and `limit` (32).",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "ordered"},
				Hint:   "Drop the member from params.ordered so it enters as a numeric variable (polyserial / Pearson correlations), or recode it into at most 32 ordered categories before the fit.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"matrices", "*", "params", "estimator"},
				Hint:   "Fit every member as continuous with the default \"ml\" estimator (drop estimator and ordered).",
			},
		},
	},
	PULSE_MATRIX_PAIRWISE_N_STAR: {
		Message: "An inferential figure over a pairwise matrix needs one sample size, but under pairwise deletion every cell rests on its own rows, so the figure uses the smallest pair's inference size (pair N unweighted, pair sum of weights under frequency weights, scaled to Kish n_eff under probability weights) — the conservative choice: Bartlett's sphericity test, and a factor analysis's empirical chi-square and maximum-likelihood fit figures. Details carry `matrix`, `outputs`, `n_star` and the pair as `row` / `col`.",
		Fixups: []Fixup{
			{
				Action: FixupSetDefault,
				Path:   []string{"matrices", "*", "params", "missing"},
				Hint:   "Set params.missing \"listwise\" to test on the complete rows, one N for every cell; keep pairwise when the smallest pair is large enough for the test to be read.",
			},
		},
	},
	PULSE_RETURN_INVALID: {
		Message: "The `return` block cannot be read, so the request is refused before any record is read — predict refuses it identically. `preset` must be full, standard or minimal; `precision` is significant digits from 1 to 17 (0 or absent means unlimited); every include / exclude path is JSON keys joined by `.`, with `[*]` stepping into every array element and a trailing `*` on a map-key segment as a prefix glob (`tests[*].details.effect_*`). The offending key is under `key`, its value under `value`, a path's position under `index`, the rule under `reason` and, for a preset, the accepted names under `valid`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"return", "preset"},
				Hint:     "Use one of the presets listed under `valid`, or drop `preset` and list paths under `include`.",
				Examples: []any{"standard", "minimal"},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"return", "precision"},
				Hint:     "Use 1–17 significant digits, or drop `precision` for unlimited.",
				Examples: []any{4, 6},
			},
			{
				Action:   FixupReplaceField,
				Path:     []string{"return", "include"},
				Hint:     "Spell the path as keys joined by `.`, `[*]` for array elements and `*` only at the end of a map key.",
				Examples: []any{"data[*].region", "tests[*].p_value"},
			},
		},
	},
	PULSE_RETURN_PATH_UNKNOWN: {
		Message: "A `return` include / exclude path names nothing in this instance's Response, so the request is refused before any record is read — predict refuses it identically. Paths use the payload schema's JSON names (`pulse schema`); an object takes a key, an array takes `[*]`, a `*` glob applies only to map keys, and `data[*].<column>` must be a column the request produces (a group field, an aggregation, attribute, window or feature label, a schema field on an ungrouped run, or a `<field>_label` sibling). The path is under `path`, its position under `key` / `index`, the failing segment under `at`, the rule under `reason`, the keys available there under `valid` and the nearest one, when close, under `suggestion`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"return", "include"},
				Hint:   "Replace the failing segment with the key under `suggestion`, or one listed under `valid`.",
			},
			{
				Action: FixupRemoveParam,
				Path:   []string{"return", "exclude"},
				Hint:   "Drop the path if the part it names is not produced by this request.",
			},
		},
	},
	PULSE_RETURN_PATH_UNMATCHED: {
		Message: "Warning-class — a `return` include path reaching into an open map (an operator component map, test details, data rows under a join or crosstab) matched nothing in the executed Response. The response was shaped; the path simply selected nothing. The path is under `path`.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"return", "include"},
				Hint:   "Check the key against the full response (`return` absent) or the operator's component schema in the manifest, then correct or drop the path.",
			},
		},
	},
	PULSE_SLOT_FAILED: {
		Message: "Warning-class — one regression, test or matrix slot failed on its data (a data-dependent runtime refusal: separation, non-convergence, a rank-deficient design, too few rows, zero variance, a singular matrix) while every other slot of the response survived. The failed result keeps its label / name and type and carries `error` {code, message, details} — the refusal's own code and fixups — with every numeric field absent. Details carry `slot_kind`, `index`, `label` and `code` (plus `group_key` for one bucket of a grouped matrix). Under Options.Strict the run fails with the slot's own code instead.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Look up the slot's own error code (details.code) and apply its fixup to that slot only, then rerun just that slot; the other slots' results are already valid.",
			},
			{
				Action: FixupRequiresReschema,
				Hint:   "When the slot's data cannot carry the fit or test (too few rows, a constant column, perfectly separated or collinear predictors), filter or reshape the input before rerunning the slot.",
			},
		},
	},
	PULSE_SEM_SCORE_LEVEL_UNSEEN: {
		Message: "Warning-class — an ATTR_SEM_SCORE slot with params.group scored rows whose group level holds no fit rows (params.fit_on left them out, or no filtered row of that level reached the fit), so the joint multi-group fit has no parameters to score them with: their scores are null, counted in details.rows per level (details.levels), and every other row is scored with its own group's parameters. details.fit_levels lists the levels the fit holds. On an ordinal fit (estimator dwls / wlsmv) the same warning, details.reason category_unseen, counts the rows holding an ordered member value that is none of the fit's categories (details.variables, rows per variable, categories the fit's values): those rows have no category probability and score null.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Widen params.fit_on (or the request filters) so every level — and, on an ordinal fit, every category — you need scored reaches the fit, or filter the unseen rows out of the request when they should not be scored.",
			},
		},
	},
	PULSE_FIT_UNAVAILABLE: {
		Message: "Warning-class — an attribute slot reads a Compose fit that failed on its data (its `fits` entry carries `error`): scoring it, a formula naming it in a literal coef() call, a fitted vector attribute's params.fit or a {population} reference. No model is bound under that name, so the slot's column is null on every row (aggregations over it count the rows in n_null) and every other slot of the response is computed normally. Details carry `fit` (the fit's name), `attribute` (the reading slot's label) and `code` (the fit's own error code). Under Options.Strict the failed fit fails the whole batch with its own code before any slot runs.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Hint:   "Look up the fit's own error code (details.code) and apply its fixup to the fit request named by details.fit, then rerun the batch; the reading slot needs no change.",
			},
			{
				Action: FixupReplaceField,
				Hint:   "Point the slot's params.fit, coef() call or population reference at another fit that converged, or drop the slot when the failed fit cannot be repaired.",
			},
		},
	},
	PULSE_AUC_DELONG_WEIGHTED: {
		Message: "A note, not an error: a weighted AGG_AUC slot reports the weighted AUC and Gini exactly, but its components `se`, `ci_lower` and `ci_upper` are null and `se_note` carries this code. No weighted DeLong variance is defensible (the placement variances assume independent unit rows), so the interval is withheld rather than computed on the wrong sample size.",
		Fixups: []Fixup{
			{
				Action: FixupRemoveParam,
				Path:   []string{"Aggregations", "*", "Weight"},
				Hint:   "Set \"weight\": null on the AGG_AUC slot to get the unweighted AUC with its DeLong standard error and 95% interval.",
			},
		},
	},
	PULSE_LIMIT_INVALID: {
		Message: "An instance resource limit in Options.Limits holds a negative value other than -1, so pulse.New refused it before any instance existed. Every limit field uses one encoding: 0 means the built-in default, -1 (pulse.Unlimited) means no limit, and a positive value is the bound (nanoseconds for RequestTimeout). The snake_case limit name is under `limit` and the refused value under `value`.",
		Fixups: []Fixup{
			{
				Action:   FixupSetDefault,
				Path:     []string{"Limits"},
				Hint:     "Set the field named by `limit` to 0 for the built-in default, -1 (pulse.Unlimited) to disable the limit, or a positive bound.",
				Examples: []any{0, -1, 10000000},
			},
		},
	},
	PULSE_LIMIT_EXCEEDED: {
		Message: "The request breached one of the instance's effective resource limits, so it was refused. The limit is named under `limit` (snake_case), its configured value under `configured`, the value the request reached under `observed` and the Go option under `option`; the message repeats the configured value. A request cannot raise an instance limit — only the host can.",
		Fixups: []Fixup{
			{
				Action: FixupReplaceField,
				Path:   []string{"Limits"},
				Hint:   "Raise the limit on the instance: set Options.Limits.<Field> (named under `option`), the feature profile `limits.<limit>` key, or the CLI's root `--limit <limit>=<value>` flag (accepted by every `pulse` command); -1 disables it.",
			},
			{
				Action: FixupReplaceField,
				Path:   []string{"request"},
				Hint:   "Reduce the request so it stays within the configured value: group on a lower-cardinality field, filter first, narrow the crosstab axes or the matrix fields, split the Compose or chain into smaller calls, or shrink the join's right side.",
			},
		},
	},
	PULSE_DOCS_EXPORT_DIR_NOT_EMPTY: {
		Message: "The reference export refused its output directory: it is not empty and carries no `.pulse-docs-export` marker, so the export cannot tell its own files from yours. Nothing was written. An empty directory, a missing one, or one a previous export wrote (it carries the marker) is overwritten in place, and only the files the marker lists are ever deleted. The directory is under `path`, the marker file name under `marker`.",
		Fixups: []Fixup{
			{
				Action:   FixupReplaceField,
				Path:     []string{"dir"},
				Hint:     "Pass an empty or previously exported directory: a new path (it is created), an empty directory, or the directory an earlier export wrote.",
				Examples: []any{"docs/reference"},
			},
		},
	},
}
