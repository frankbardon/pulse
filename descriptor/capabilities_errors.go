package descriptor

import (
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// errorMetaTable lists per-code description and severity overrides. Codes
// not present default to severity="error" and a description derived from
// the code name. TestManifestErrorCodesComplete enforces that every entry
// in errors.AllCodes() has an explicit row here, so descriptions stay
// curated rather than auto-generated.
var errorMetaTable = []ErrorMeta{
	// ENCODING
	{Code: "ENCODING_INVALID", Severity: "error", Description: "The .pulse file header or schema block is malformed (bad magic, unsupported format version, or invalid field-type byte)."},
	{Code: "ENCODING_IO", Severity: "error", Description: "An I/O failure occurred while reading or writing the binary .pulse file."},
	{Code: "ENCODING_TYPE_MISMATCH", Severity: "error", Description: "A value cannot be encoded into its declared field type (out of range, wrong sign, or wrong unit)."},
	{Code: "ENCODING_INTERNAL", Severity: "error", Description: "Internal invariant violated inside the encoding layer; report with reproducer."},
	// PROCESSING
	{Code: "PROCESSING_CONFIG", Severity: "error", Description: "An operator's configuration is invalid (missing params, wrong type, out-of-range value)."},
	{Code: "PROCESSING_STATE", Severity: "error", Description: "Pipeline state-machine invariant violated mid-run (typically a streaming-pass ordering bug)."},
	{Code: "PROCESSING_RUNTIME", Severity: "error", Description: "An operator failed during row processing (formula compile failure, divide-by-zero, etc.)."},
	{Code: "PROCESSING_GROUP", Severity: "error", Description: "Group construction failed (e.g., quantile with zero-variance input, malformed bucket key)."},
	{Code: "PROCESSING_INTERNAL", Severity: "error", Description: "Internal invariant violated inside the processing layer; report with reproducer."},
	// SERVICE
	{Code: "SERVICE_VALIDATION", Severity: "error", Description: "The request failed pre-execution validation (missing required fields, invalid combinations, schema mismatch)."},
	{Code: "SERVICE_RESOURCE", Severity: "error", Description: "A required resource could not be opened (missing .pulse file, unreadable path)."},
	{Code: "SERVICE_REGISTRY", Severity: "error", Description: "The request named an operator that is not registered (typo in type field, deprecated identifier)."},
	{Code: "SERVICE_INTERNAL", Severity: "error", Description: "Internal invariant violated inside the service orchestration layer; report with reproducer."},
	// DATA
	{Code: "DATA_FILE", Severity: "error", Description: "A data file could not be opened or its layout is invalid."},
	{Code: "DATA_PARSE", Severity: "error", Description: "Tabular input (CSV / TSV / JSON / Parquet) failed to parse at a row boundary."},
	{Code: "DATA_CONFIG", Severity: "error", Description: "An I/O job's configuration is invalid (conflicting flags, unknown format, bad target path)."},
	{Code: "DATA_CALCULATION", Severity: "error", Description: "A calculation over data values failed (e.g., reading a field that does not exist on the record)."},
	{Code: "DATA_INTERNAL", Severity: "error", Description: "Internal invariant violated inside the data layer; report with reproducer."},
	// CLI
	{Code: "CLI_INPUT", Severity: "error", Description: "A CLI argument or flag is malformed (missing value, unknown subcommand, bad JSON)."},
	{Code: "CLI_OUTPUT", Severity: "error", Description: "Output generation failed (file write error, terminal too narrow, format unsupported)."},
	{Code: "CLI_COMMAND", Severity: "error", Description: "A CLI command failed to dispatch or returned a non-zero status."},
	{Code: "CLI_INTERNAL", Severity: "error", Description: "Internal invariant violated inside the CLI binary; report with reproducer."},
	// PULSE
	{Code: "PULSE_IMPORT_SCHEMA_AMBIGUOUS", Severity: "error", Description: "Schema inference observed conflicting types in the same column across the sample window."},
	{Code: "PULSE_IMPORT_ROW_ERROR", Severity: "error", Description: "A row could not be imported due to a per-cell encoding or parse failure."},
	{Code: "PULSE_EXPORT_ROW_ERROR", Severity: "error", Description: "A row could not be exported due to a per-cell value-to-string conversion failure."},
	{Code: "PULSE_IMPORT_CATEGORICAL_OVERFLOW", Severity: "error", Description: "A categorical column's observed dictionary exceeded the declared width capacity (u8 / u16 / u32)."},
	{Code: "PULSE_IMPORT_CATEGORICAL_UNBOUNDED", Severity: "warning", Description: "The categorical inference heuristic believes the column has unbounded cardinality and should be modeled as a string."},
	{Code: "PULSE_IMPORT_DESCRIPTION_TOO_LONG", Severity: "error", Description: "A field description exceeds the 1000-byte cap defined for the .pulse header."},
	{Code: "PULSE_AGG_NOT_MEANINGFUL_FOR_CATEGORICAL", Severity: "warning", Description: "A numeric aggregation (SUM, AVERAGE, etc.) was requested on a categorical field."},
	{Code: "PULSE_FIELD_DESCRIPTION_LOW_QUALITY", Severity: "warning", Description: "A field description is empty, too short, or matches a generic-word denylist (\"n/a\", \"tbd\", etc.)."},
	{Code: "PULSE_WINDOW_INVALID", Severity: "error", Description: "A window operator's configuration is invalid (bad frame, alpha out of bounds, no order key, label collision)."},
	{Code: "PULSE_FEAT_TARGET_LEAKAGE_RISK", Severity: "warning", Description: "FEAT_TARGET_ENCODE was requested without a prior FEAT_TRAIN_TEST_SPLIT; encoded values include rows that should be held out."},
	{Code: "PULSE_DECIMAL_OVERFLOW", Severity: "error", Description: "A decimal arithmetic result exceeds the decimal128(38) representable range."},
	{Code: "PULSE_DECIMAL_PRECISION_LOSS", Severity: "warning", Description: "A decimal aggregation fell back to f64 because intermediate state would have overflowed decimal128(38)."},
	{Code: "PULSE_DECIMAL_DIVIDE_BY_ZERO", Severity: "error", Description: "A decimal divide operation observed a zero divisor."},
	{Code: "PULSE_GEO_INVALID_POINT", Severity: "error", Description: "A point parse failed or |lat|>90 / |lon|>180."},
	{Code: "PULSE_GEO_INVALID_POLYGON", Severity: "error", Description: "A WKT POLYGON parse failed or the outer ring is not closed."},
	{Code: "PULSE_GEO_ANTIMERIDIAN_AMBIGUOUS", Severity: "warning", Description: "An AGG_GEO_BBOX input crosses the 180/-180 meridian, where a flat min/max bbox is ambiguous."},
	{Code: "PULSE_GEO_INVALID_RESOLUTION", Severity: "error", Description: "An H3 resolution is out of [0, 15] or finer than a cell's native resolution when walking parents."},
	{Code: "PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL", Severity: "warning", Description: "An aggregation has no defined semantics for the decimal128 field type (e.g., AGG_MEDIAN, AGG_PERCENTILE in v1)."},
	{Code: "PULSE_AGG_NOT_MEANINGFUL_FOR_GEO", Severity: "warning", Description: "A numeric aggregator was requested on a geospatial field (point_f64 / h3_cell)."},
	{Code: "PULSE_SYNTH_DISTRIBUTION_UNKNOWN", Severity: "error", Description: "A synth spec referenced a distribution kind not registered in the synth package."},
	{Code: "PULSE_SYNTH_CONSTRAINT_INFEASIBLE", Severity: "error", Description: "Rejection sampling for declared constraints exceeded the allowed rejection rate; the generator gives up rather than produce biased output."},
	{Code: "PULSE_PROFILE_FIELD_UNSUPPORTED", Severity: "warning", Description: "The profile layer cannot summarize this field type (e.g., point_f64 / h3_cell); the field is skipped."},
	{Code: "PULSE_TEST_UNKNOWN_TYPE", Severity: "error", Description: "The request referenced a TestType not registered in either the row-test or post-test registry."},
	{Code: "PULSE_TEST_FIELD_NOT_NUMERIC", Severity: "error", Description: "A test requires a numeric field but the named field resolves to a categorical, geo, or otherwise non-numeric schema type."},
	{Code: "PULSE_TEST_INVALID_ALPHA", Severity: "error", Description: "The request's Alpha value lies outside (0, 1)."},
	{Code: "PULSE_TEST_INSUFFICIENT_N", Severity: "warning", Description: "A test received fewer non-null observations than the minimum needed to compute its statistic."},
	{Code: "PULSE_TEST_VARIANCE_ZERO", Severity: "warning", Description: "One or more groups have zero sample variance, making the t- or F-statistic undefined."},
	{Code: "PULSE_TEST_SPLIT_GROUPS_LT_2", Severity: "error", Description: "A two-sample or ANOVA test observed fewer than the required number of distinct groups in the SplitBy field."},
	{Code: "PULSE_TEST_CONTINGENCY_DEGENERATE", Severity: "error", Description: "A chi-square contingency table is empty or has a single row/column."},
	{Code: "PULSE_TEST_EXPECTED_COUNT_TOO_LOW", Severity: "warning", Description: "A chi-square cell's expected count is below 5; the asymptotic χ² approximation is unreliable."},
	{Code: "PULSE_TEST_FIELD2_NOT_NUMERIC", Severity: "error", Description: "Field2 for a paired or bivariate test (PEARSON_R, PAIRED_T) resolves to a non-numeric schema type."},
	{Code: "PULSE_TEST_SUCCESS_VALUE_MISSING", Severity: "error", Description: "TEST_PROP_Z did not supply Params.success; the test cannot decide which category counts as a positive outcome."},
	{Code: "PULSE_TEST_CORRELATION_UNDEFINED", Severity: "warning", Description: "Pearson r encountered a column with zero variance; r and its t-statistic are undefined."},
	{Code: "PULSE_TEST_PAIRED_LENGTH_MISMATCH", Severity: "warning", Description: "A paired test encountered rows with one of the paired columns null while the other is present; drop-pair semantics applied."},
	{Code: "PULSE_TEST_TIES_DOMINATE", Severity: "warning", Description: "A rank-based test observed ties on ≥ 50 % of input values; the asymptotic approximation loses accuracy."},
	{Code: "PULSE_TEST_SUBJECT_MISSING", Severity: "warning", Description: "A repeated-measures test found at least one subject missing one or more conditions; incomplete subjects dropped by default."},
	{Code: "PULSE_TEST_BALANCED_DESIGN_REQUIRED", Severity: "error", Description: "A repeated-measures test observed unequal cell counts; only balanced designs are supported in v1."},
	{Code: "PULSE_TEST_TUKEY_REQUIRES_K_GE_3", Severity: "error", Description: "Tukey HSD requires k ≥ 3 groups; a t-test or two-proportion z is the appropriate alternative for k = 2."},
	{Code: "PULSE_TEST_SHAPIRO_N_BOUND", Severity: "warning", Description: "A Shapiro-Wilk request observed n above the supported limit (5000); use an asymptotic normality test instead."},
	{Code: "PULSE_TEST_FISHER_R_OR_C_GT_2", Severity: "error", Description: "Fisher exact requires a 2×2 table; r×c support lands separately."},
}

// errorCapabilities returns metadata for every entry in errors.AllCodes(),
// sorted by code for deterministic manifest output.
func errorCapabilities() []ErrorMeta {
	idx := make(map[string]ErrorMeta, len(errorMetaTable))
	for _, m := range errorMetaTable {
		idx[m.Code] = m
	}
	codes := errors.AllCodes()
	out := make([]ErrorMeta, 0, len(codes))
	for _, c := range codes {
		s := string(c)
		m, ok := idx[s]
		if !ok {
			// Fallback (TestManifestErrorCodesComplete catches this so it
			// should never appear in production output).
			m = ErrorMeta{Code: s, Severity: "error", Description: "Undocumented error code; add an entry to errorMetaTable."}
		}
		m.Domain = errorDomain(s)
		// Populate the manifest's prose Fixup hints from the per-code
		// metadata table in errors/. The structured Fixup (action,
		// path, examples) stays reachable in-process via
		// errors.Code.Fixup(req); the manifest carries only the prose
		// Hint slice so the wire payload stays slim.
		if meta, ok := errors.MetadataFor(c); ok && !meta.FixupNotApplicable {
			hints := make([]string, 0, len(meta.Fixups))
			for _, fx := range meta.Fixups {
				if strings.TrimSpace(fx.Hint) == "" {
					continue
				}
				hints = append(hints, fx.Hint)
			}
			if len(hints) > 0 {
				m.Fixups = hints
			}
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// errorDomain returns the domain prefix of a code (the segment before the
// first underscore).
func errorDomain(code string) string {
	if i := strings.IndexByte(code, '_'); i > 0 {
		return code[:i]
	}
	return code
}
