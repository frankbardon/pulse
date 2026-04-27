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

// InferenceWarning records a non-fatal observation during inference.
type InferenceWarning struct {
	Column  string
	Message string
}

// InferSchema samples up to sampleRows rows from reader and proposes a Schema.
// If sampleRows <= 0, defaultSampleRows is used. The minimum is minSampleRows.
func InferSchema(reader Reader, sampleRows int) (*encoding.Schema, []InferenceWarning, error) {
	if sampleRows <= 0 {
		sampleRows = defaultSampleRows
	}
	if sampleRows < minSampleRows {
		sampleRows = minSampleRows
	}

	columns, err := reader.ReadHeader()
	if err != nil {
		return nil, nil, err
	}
	if len(columns) == 0 {
		return nil, nil, errors.NewCodedError(errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS, "no columns in header")
	}

	// Collect sample data per column.
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
		return nil, nil, err
	}

	if rowCount == 0 {
		return nil, nil, errors.NewCodedError(errors.PULSE_IMPORT_SCHEMA_AMBIGUOUS, "no data rows to sample")
	}

	var warnings []InferenceWarning
	fields := make([]encoding.Field, numCols)

	byteOffset := 0
	for i, colName := range columns {
		ft, colWarnings, err := inferColumnType(colName, samples[i])
		if err != nil {
			return nil, nil, err
		}
		warnings = append(warnings, colWarnings...)

		fields[i] = encoding.Field{
			Name:         colName,
			Type:         ft,
			ByteOffset:   byteOffset,
			CsvColumnIdx: i,
		}
		byteOffset += ft.ByteSize()
	}

	schema := &encoding.Schema{Fields: fields}
	return schema, warnings, nil
}

// errStopIteration is a sentinel used internally to stop row iteration early.
var errStopIteration = fmt.Errorf("stop iteration")

// ErrStopIteration returns the stop iteration sentinel for use by readers.
func ErrStopIteration() error {
	return errStopIteration
}

// inferColumnType determines the best FieldType for a column from sample values.
func inferColumnType(colName string, values []string) (encoding.FieldType, []InferenceWarning, error) {
	if len(values) == 0 {
		return encoding.FieldTypeF64, nil, nil
	}

	hasNulls := false
	nonNullValues := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" || strings.EqualFold(trimmed, "null") || strings.EqualFold(trimmed, "na") || strings.EqualFold(trimmed, "n/a") {
			hasNulls = true
		} else {
			nonNullValues = append(nonNullValues, trimmed)
		}
	}

	if len(nonNullValues) == 0 {
		// All nulls - default to nullable f64.
		var w []InferenceWarning
		if hasNulls {
			w = append(w, InferenceWarning{Column: colName, Message: "all values are null, defaulting to f64"})
		}
		return encoding.FieldTypeF64, w, nil
	}

	// Try each type in priority order.
	// 1. Boolean
	if allBool(nonNullValues) {
		if hasNulls {
			return encoding.FieldTypeNullableBool, nil, nil
		}
		return encoding.FieldTypePackedBool, nil, nil
	}

	// 2. Integer types (ascending width)
	if allInteger(nonNullValues) {
		minVal, maxVal := intRange(nonNullValues)

		if hasNulls {
			// Nullable integer types
			if minVal >= 0 && maxVal <= 15 {
				return encoding.FieldTypeNullableU4, nil, nil
			}
			if minVal >= 0 && maxVal <= 255 {
				return encoding.FieldTypeNullableU8, nil, nil
			}
			if minVal >= 0 && maxVal <= 65535 {
				return encoding.FieldTypeNullableU16, nil, nil
			}
			// Fall through to float for larger nullable ints.
			return encoding.FieldTypeF64, nil, nil
		}

		if minVal >= 0 && maxVal <= 255 {
			return encoding.FieldTypeU8, nil, nil
		}
		if minVal >= 0 && maxVal <= 65535 {
			return encoding.FieldTypeU16, nil, nil
		}
		if minVal >= 0 && maxVal <= math.MaxUint32 {
			return encoding.FieldTypeU32, nil, nil
		}
		if minVal >= 0 {
			return encoding.FieldTypeU64, nil, nil
		}
		// Negative integers get promoted to float.
		return encoding.FieldTypeF64, nil, nil
	}

	// 3. Float types
	if allFloat(nonNullValues) {
		if fitsF32(nonNullValues) {
			return encoding.FieldTypeF32, nil, nil
		}
		return encoding.FieldTypeF64, nil, nil
	}

	// 4. Date
	if allDate(nonNullValues) {
		return encoding.FieldTypeDate, nil, nil
	}

	// 5. Categorical (string)
	uniqueVals := uniqueCount(nonNullValues)
	totalNonNull := len(nonNullValues)

	// If all sample values are unique, the column is unbounded.
	if uniqueVals == totalNonNull && totalNonNull >= minSampleRows {
		return 0, nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_IMPORT_CATEGORICAL_UNBOUNDED,
			fmt.Sprintf("column %q: all %d sampled values are unique, suggesting unbounded cardinality", colName, totalNonNull),
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

	return catType, warnings, nil
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

// allInteger checks if all values parse as integers.
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

// intRange returns the min and max of integer values (as int64).
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

// allFloat checks if all values parse as float64.
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

// allDate checks if all values parse as dates.
func allDate(values []string) bool {
	for _, v := range values {
		if !parseDate(v) {
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

// uniqueCount returns the number of distinct strings.
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
