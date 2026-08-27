package io

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// minSampleRows is the minimum number of rows that InferSchema will try to sample.
const minSampleRows = 50

// defaultSampleRows is the default sample size when no value is specified.
const defaultSampleRows = 500

// defaultSetInferenceMinPct is the default percentage of non-null
// sampled cells that must contain the inferred delimiter for a column
// to be classified as a set_* field type. Set 0 on InferOptions to
// accept this default; values >100 are clamped to 100.
const defaultSetInferenceMinPct = 30

// setInferenceDelimiterPriority is the order in which the inference
// heuristic probes delimiters for multi-select detection. Pipe first
// because it almost never appears in legitimate categorical text;
// semicolon second; comma is intentionally absent (a comma-delimited
// cell inside CSV is unparseable through the CSV reader).
var setInferenceDelimiterPriority = []string{"|", ";"}

// DefaultSetDelimiter is the delimiter assumed by convertValue when no
// per-column delimiter has been recorded (explicit-schema imports that
// skipped the inference pass). Always |.
const DefaultSetDelimiter = "|"

// InferenceWarning records a non-fatal observation during inference.
type InferenceWarning struct {
	Column  string
	Message string
}

// InferOptions parameterises InferSchemaWithOptions. The zero value is
// safe — SampleRows / SetInferenceMinPct fall back to their package
// defaults and ColumnTypeOverrides is treated as empty.
type InferOptions struct {
	// SampleRows caps how many rows InferSchema reads before
	// finalizing column types. Defaults to defaultSampleRows when
	// <= 0; min-clamped to minSampleRows.
	SampleRows int
	// SetInferenceMinPct is the minimum percentage of non-null
	// sampled cells that must contain the inferred delimiter for the
	// column to be classified as set_*. Defaults to
	// defaultSetInferenceMinPct (30) when 0.
	SetInferenceMinPct int
	// ColumnTypeOverrides maps column name to forced FieldType,
	// bypassing the inference heuristics entirely for those columns.
	// For set_* / categorical_* types the dictionary is still built
	// from observed sample values during the inference pass; the
	// override only fixes the column's storage type.
	ColumnTypeOverrides map[string]encoding.FieldType
}

// InferenceResult bundles the inference output. The Delimiters map is
// populated for columns classified as set_*; callers (importers) use
// it to pack per-cell masks during the row pass.
type InferenceResult struct {
	Schema     *encoding.Schema
	Warnings   []InferenceWarning
	Delimiters map[string]string
}

// InferSchema samples up to sampleRows rows from reader and proposes a Schema.
// If sampleRows <= 0, defaultSampleRows is used. The minimum is minSampleRows.
// Wrapper over InferSchemaWithOptions that drops the delimiter map; only
// CSV/TSV/Excel paths that ignore set-typed import packing should call
// this entry. Importers should call InferSchemaWithOptions directly.
func InferSchema(reader Reader, sampleRows int) (*encoding.Schema, []InferenceWarning, error) {
	res, err := InferSchemaWithOptions(reader, InferOptions{SampleRows: sampleRows})
	if err != nil {
		return nil, nil, err
	}
	return res.Schema, res.Warnings, nil
}

// InferSchemaWithOptions is the parameterised inference entry. Returns
// the schema, accumulated warnings, AND the per-column delimiter map
// for columns classified as set_*. The delimiter map is used by
// ImportJob.Run during the row pass to split delimited cells into
// token lists for bit-packing.
func InferSchemaWithOptions(reader Reader, opts InferOptions) (*InferenceResult, error) {
	sampleRows := opts.SampleRows
	if sampleRows <= 0 {
		sampleRows = defaultSampleRows
	}
	if sampleRows < minSampleRows {
		sampleRows = minSampleRows
	}
	minPct := opts.SetInferenceMinPct
	if minPct <= 0 {
		minPct = defaultSetInferenceMinPct
	}
	if minPct > 100 {
		minPct = 100
	}

	columns, err := reader.ReadHeader()
	if err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, errors.NewCodedError(errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS, "no columns in header")
	}

	numCols := len(columns)
	samples := make([][]string, numCols)
	for i := range samples {
		samples[i] = make([]string, 0, sampleRows)
	}

	rowCount := 0
	err = reader.ReadRows(context.Background(), func(row []string) error {
		if rowCount >= sampleRows {
			return errStopIteration
		}
		for i := 0; i < numCols && i < len(row); i++ {
			samples[i] = append(samples[i], row[i])
		}
		rowCount++
		return nil
	})
	if err != nil && err != errStopIteration {
		return nil, err
	}

	if rowCount == 0 {
		return nil, errors.NewCodedError(errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS, "no data rows to sample")
	}

	var warnings []InferenceWarning
	fields := make([]encoding.Field, numCols)
	delimiters := map[string]string{}

	byteOffset := 0
	for i, colName := range columns {
		ft, nullable, delim, colWarnings, err := inferColumnTypeWithOpts(colName, samples[i],
			minPct, opts.ColumnTypeOverrides[colName])
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, colWarnings...)
		if delim != "" {
			delimiters[colName] = delim
		}

		fields[i] = encoding.Field{
			Name:         colName,
			Type:         ft,
			Nullable:     nullable,
			ByteOffset:   byteOffset,
			CsvColumnIdx: i,
		}
		if ft.IsBitPacked() {
			byteOffset++
		} else {
			byteOffset += ft.ByteSize()
		}
	}

	return &InferenceResult{
		Schema:     &encoding.Schema{Fields: fields},
		Warnings:   warnings,
		Delimiters: delimiters,
	}, nil
}

// errStopIteration is a sentinel used internally to stop row iteration early.
var errStopIteration = fmt.Errorf("stop iteration")

// ErrStopIteration returns the stop iteration sentinel for use by readers.
func ErrStopIteration() error {
	return errStopIteration
}

// inferColumnTypeWithOpts is the parameterised inference entry. It
// honours the column-type override (when non-zero), respects the set
// inference threshold for delimited-cell detection, and returns the
// chosen delimiter alongside the field type so importers can pack
// per-row masks consistently. Delegates to inferColumnType for the
// no-override / default-threshold path so the legacy entry stays a
// straight pass-through.
func inferColumnTypeWithOpts(colName string, values []string, minPct int,
	override encoding.FieldType,
) (encoding.FieldType, bool, string, []InferenceWarning, error) {
	if override != 0 {
		ft, nullable, delim, warnings := applyTypeOverride(colName, values, override)
		return ft, nullable, delim, warnings, nil
	}
	if len(values) == 0 {
		return encoding.FieldTypeF64, false, "", nil, nil
	}

	hasNulls := false
	nonNullValues := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" ||
			strings.EqualFold(trimmed, "null") ||
			strings.EqualFold(trimmed, "na") ||
			strings.EqualFold(trimmed, "n/a") {
			hasNulls = true
		} else {
			nonNullValues = append(nonNullValues, trimmed)
		}
	}

	if len(nonNullValues) == 0 {
		var w []InferenceWarning
		if hasNulls {
			w = append(w, InferenceWarning{Column: colName,
				Message: "all values are null, defaulting to f64"})
		}
		return encoding.FieldTypeF64, hasNulls, "", w, nil
	}

	if allBool(nonNullValues) {
		return encoding.FieldTypePackedBool, hasNulls, "", nil, nil
	}
	if allInteger(nonNullValues) {
		minVal, maxVal := intRange(nonNullValues)
		switch {
		case minVal >= 0 && maxVal <= 15:
			return encoding.FieldTypeU4, hasNulls, "", nil, nil
		case minVal >= 0 && maxVal <= 255:
			return encoding.FieldTypeU8, hasNulls, "", nil, nil
		case minVal >= 0 && maxVal <= 65535:
			return encoding.FieldTypeU16, hasNulls, "", nil, nil
		case minVal >= 0 && maxVal <= math.MaxUint32:
			return encoding.FieldTypeU32, hasNulls, "", nil, nil
		case minVal >= 0:
			return encoding.FieldTypeU64, hasNulls, "", nil, nil
		}
		return encoding.FieldTypeF64, hasNulls, "", nil, nil
	}
	if allFloat(nonNullValues) {
		if fitsF32(nonNullValues) {
			return encoding.FieldTypeF32, hasNulls, "", nil, nil
		}
		return encoding.FieldTypeF64, hasNulls, "", nil, nil
	}
	// datetime is probed BEFORE date and the order is load-bearing.
	// infer's own dateFormats list tolerates the ISO-8601 T-forms, so a
	// column of full datetime literals would otherwise classify as
	// `date` and silently lose its time-of-day at import. allDateTime
	// is strict — it delegates to encoding.ParseDateTime, which rejects
	// every date-only layout — so a date-only column falls straight
	// through to the allDate probe below and still classifies as date.
	if allDateTime(nonNullValues) {
		return encoding.FieldTypeDateTime, hasNulls, "", nil, nil
	}
	if allDate(nonNullValues) {
		return encoding.FieldTypeDate, hasNulls, "", nil, nil
	}

	// Set inference: probe known delimiters in priority order. A
	// classification fires when the delimiter appears in at least
	// minPct% of cells AND post-split unique tokens fit in set_u64
	// (≤64) AND average post-split cardinality > 1 (rules out
	// "occasional pipe in a categorical string" misclassifications).
	if ft, delim, ok := probeSetClassification(nonNullValues, minPct); ok {
		return ft, hasNulls, delim, nil, nil
	}

	// Categorical fallback.
	uniqueVals := uniqueCount(nonNullValues)
	totalNonNull := len(nonNullValues)
	if uniqueVals == totalNonNull && totalNonNull >= minSampleRows {
		return 0, false, "", nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_CATEGORICAL_UNBOUNDED,
			fmt.Sprintf("column %q: all %d sampled values are unique, suggesting unbounded cardinality",
				colName, totalNonNull),
			map[string]any{"column": colName, "unique": uniqueVals, "sampled": totalNonNull},
		)
	}
	var warnings []InferenceWarning
	catType := categoricalWidth(uniqueVals)
	if uniqueVals == totalNonNull && totalNonNull < minSampleRows {
		warnings = append(warnings, InferenceWarning{
			Column:  colName,
			Message: fmt.Sprintf("all %d sampled values are unique but sample is small; treating as categorical", totalNonNull),
		})
	}
	return catType, hasNulls, "", warnings, nil
}

// applyTypeOverride bypasses inference for a column. Nullability is
// still derived from the sampled values (null tokens flip the flag).
// For set fields the delimiter probes the same priority list and falls
// back to DefaultSetDelimiter when no probe fires.
func applyTypeOverride(colName string, values []string, override encoding.FieldType,
) (encoding.FieldType, bool, string, []InferenceWarning) {
	hasNulls := false
	nonNullValues := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" ||
			strings.EqualFold(trimmed, "null") ||
			strings.EqualFold(trimmed, "na") ||
			strings.EqualFold(trimmed, "n/a") {
			hasNulls = true
		} else {
			nonNullValues = append(nonNullValues, trimmed)
		}
	}
	if override.IsSet() {
		delim := pickSetDelimiter(nonNullValues)
		if delim == "" {
			delim = DefaultSetDelimiter
		}
		return override, hasNulls, delim, []InferenceWarning{{
			Column:  colName,
			Message: fmt.Sprintf("column %q forced to %s via column_type_overrides", colName, override),
		}}
	}
	return override, hasNulls, "", []InferenceWarning{{
		Column:  colName,
		Message: fmt.Sprintf("column %q forced to %s via column_type_overrides", colName, override),
	}}
}

// pickSetDelimiter scans the priority list and returns the first
// delimiter that appears in at least one sampled cell. Empty when no
// delimiter is found.
func pickSetDelimiter(values []string) string {
	for _, d := range setInferenceDelimiterPriority {
		for _, v := range values {
			if strings.Contains(v, d) {
				return d
			}
		}
	}
	return ""
}

// probeSetClassification decides whether the sampled cells look like a
// multi-select set column under the configured threshold.
func probeSetClassification(values []string, minPct int) (encoding.FieldType, string, bool) {
	for _, delim := range setInferenceDelimiterPriority {
		hits := 0
		for _, v := range values {
			if strings.Contains(v, delim) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		pct := hits * 100 / len(values)
		if pct < minPct {
			continue
		}
		uniq := map[string]struct{}{}
		totalTokens := 0
		for _, v := range values {
			toks := splitSetTokens(v, delim)
			totalTokens += len(toks)
			for _, t := range toks {
				uniq[t] = struct{}{}
			}
		}
		if len(uniq) == 0 || len(uniq) > 64 {
			continue
		}
		// Average post-split cardinality must exceed 1, ruling out
		// columns that only occasionally carry a delimiter inside a
		// free-text categorical string.
		if float64(totalTokens)/float64(len(values)) <= 1.0 {
			continue
		}
		return setWidth(len(uniq)), delim, true
	}
	return 0, "", false
}

// splitSetTokens breaks a raw cell into trimmed, non-empty tokens by
// the given delimiter. No quote handling — delimited values containing
// the delimiter as literal data require the column_type_overrides
// escape hatch.
func splitSetTokens(raw, delim string) []string {
	if raw == "" || delim == "" {
		return nil
	}
	parts := strings.Split(raw, delim)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// setWidth selects the smallest set_* width that fits unique tokens.
// Mirrors categoricalWidth in spirit; cardinality > 64 falls back to
// categorical (caller-side probe filters this case before invocation).
func setWidth(unique int) encoding.FieldType {
	switch {
	case unique <= 8:
		return encoding.FieldTypeSetU8
	case unique <= 16:
		return encoding.FieldTypeSetU16
	case unique <= 32:
		return encoding.FieldTypeSetU32
	case unique <= 64:
		return encoding.FieldTypeSetU64
	}
	// Caller already gated; reaching here is a programming bug.
	return encoding.FieldTypeCategoricalU8
}

// allBool checks if all values are boolean-like.
func allBool(values []string) bool {
	for _, v := range values {
		low := strings.ToLower(v)
		switch low {
		case "true", "false", "yes", "no", "1", "0", "t", "f", "y", "n":
			continue
		default:
			return false
		}
	}
	return true
}

func allInteger(values []string) bool {
	for _, v := range values {
		_, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			// Try unsigned.
			_, err = strconv.ParseUint(v, 10, 64)
			if err != nil {
				return false
			}
		}
	}
	return true
}

func intRange(values []string) (int64, int64) {
	minVal := int64(math.MaxInt64)
	maxVal := int64(math.MinInt64)
	for _, v := range values {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			// Might be a large unsigned value.
			u, _ := strconv.ParseUint(v, 10, 64)
			n = int64(u) // may wrap but we check unsigned path separately
		}
		if n < minVal {
			minVal = n
		}
		if n > maxVal {
			maxVal = n
		}
	}
	return minVal, maxVal
}

func allFloat(values []string) bool {
	for _, v := range values {
		_, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return false
		}
	}
	return true
}

// fitsF32 checks if all float values fit in float32 without loss.
func fitsF32(values []string) bool {
	for _, v := range values {
		f, _ := strconv.ParseFloat(v, 64)
		if f != 0 && (math.Abs(f) < math.SmallestNonzeroFloat32 || math.Abs(f) > math.MaxFloat32) {
			return false
		}
	}
	return true
}

// dateFormats are the formats we attempt when detecting date columns.
var dateFormats = []string{
	"2006-01-02",
	"01/02/2006",
	"2006-01-02T15:04:05Z",
	"2006-01-02T15:04:05",
	"2006/01/02",
	"02-Jan-2006",
}

func allDate(values []string) bool {
	for _, v := range values {
		if !parseDate(v) {
			return false
		}
	}
	return true
}

// allDateTime reports whether every sampled cell is a full ISO-8601
// datetime literal. Unlike allDate — which probes infer's own
// best-effort dateFormats list — this delegates to encoding.ParseDateTime,
// the same authority io/import.go's convertValue uses to produce the
// on-wire epoch-seconds uint64. Sharing the one list is deliberate: a
// column this classifies as datetime is thereby guaranteed to convert
// cell-for-cell during the row pass, with no inference-vs-conversion
// drift to strand rows in PULSE_IMPORT_ROW_ERROR.
//
// ParseDateTime rejects every date-only layout, so a column of plain
// dates returns false here and reaches the allDate probe unchanged.
func allDateTime(values []string) bool {
	for _, v := range values {
		if _, err := encoding.ParseDateTime(v); err != nil {
			return false
		}
	}
	return true
}

func parseDate(v string) bool {
	for _, layout := range dateFormats {
		if _, err := time.Parse(layout, v); err == nil {
			return true
		}
	}
	return false
}

func uniqueCount(values []string) int {
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		seen[v] = struct{}{}
	}
	return len(seen)
}

// categoricalWidth selects the categorical type based on cardinality.
func categoricalWidth(unique int) encoding.FieldType {
	if unique <= 200 {
		return encoding.FieldTypeCategoricalU8
	}
	if unique <= 60000 {
		return encoding.FieldTypeCategoricalU16
	}
	return encoding.FieldTypeCategoricalU32
}
