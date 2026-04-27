package io

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Run executes the import job, converting tabular source into a .pulse file.
func (j *ImportJob) Run(ctx context.Context) (*ImportReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("ImportJob.FS is required")
	}

	schema := j.Schema
	var inferWarnings []InferenceWarning

	// Infer schema if not provided.
	if schema == nil {
		rr, ok := j.Source.(ResetReader)
		if !ok {
			return nil, fmt.Errorf("schema inference requires a ResetReader source")
		}
		var err error
		schema, inferWarnings, err = InferSchema(j.Source, j.SampleRows)
		if err != nil {
			return nil, err
		}
		if err := rr.Reset(); err != nil {
			return nil, fmt.Errorf("resetting reader after inference: %w", err)
		}
		// Skip header after reset.
		if _, err := j.Source.ReadHeader(); err != nil {
			return nil, err
		}
	}
	_ = inferWarnings

	// Build dictionaries for categorical fields.
	dicts := make(map[int]*encoding.Dictionary)
	for i := range schema.Fields {
		if schema.Fields[i].Type.IsCategorical() {
			if schema.Fields[i].Dictionary == nil {
				schema.Fields[i].Dictionary = encoding.NewDictionary()
			}
			dicts[i] = schema.Fields[i].Dictionary
		}
	}

	// Write the .pulse file.
	var buf bytes.Buffer

	if err := encoding.WriteHeader(&buf); err != nil {
		return nil, err
	}

	// We need to collect all rows first to build dictionaries,
	// then write schema + records.
	type rowData struct {
		values []uint64
	}

	var rows []rowData
	var rowErrors []RowError
	rowNum := 0

	err := j.Source.ReadRows(ctx, func(row []string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rowNum++
		vals := make([]uint64, len(schema.Fields))

		for i, f := range schema.Fields {
			colIdx := f.CsvColumnIdx
			var raw string
			if colIdx < len(row) {
				raw = strings.TrimSpace(row[colIdx])
			}

			v, err := convertValue(raw, f.Type, dicts[i])
			if err != nil {
				rowErrors = append(rowErrors, RowError{
					Row: rowNum,
					Err: errors.NewCodedErrorWithDetails(
						errors.PULSE_IMPORT_ROW_ERROR,
						fmt.Sprintf("row %d, column %q: %v", rowNum, f.Name, err),
						map[string]any{"row": rowNum, "column": f.Name},
					),
				})
				return nil // continue processing
			}
			vals[i] = v
		}

		rows = append(rows, rowData{values: vals})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Now write schema (dictionaries are populated).
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return nil, err
	}

	// Write each record. No record count prefix — the format is header + schema + records
	// per §5.3. Record count is derived from file size and per-record byte size.
	for _, r := range rows {
		for i, f := range schema.Fields {
			if f.Type == encoding.FieldTypePackedBool || f.Type == encoding.FieldTypeNullableBool || f.Type == encoding.FieldTypeNullableU4 {
				// Bit-packed types: write as single byte for simplicity.
				buf.WriteByte(byte(r.values[i]))
				continue
			}
			if err := encoding.WriteFieldValue(&buf, f.Type, r.values[i]); err != nil {
				return nil, err
			}
		}
	}

	// Write to filesystem.
	if err := afero.WriteFile(j.FS, j.Target, buf.Bytes(), 0644); err != nil {
		return nil, err
	}

	return &ImportReport{
		RowsImported: len(rows),
		Schema:       schema,
		RowErrors:    rowErrors,
	}, nil
}

// Predict validates the import without writing any output.
func (j *ImportJob) Predict(ctx context.Context) (*PredictReport, error) {
	schema := j.Schema
	var warnings []InferenceWarning

	if schema == nil {
		rr, ok := j.Source.(ResetReader)
		if !ok {
			return nil, fmt.Errorf("schema inference requires a ResetReader source")
		}
		var err error
		schema, warnings, err = InferSchema(j.Source, j.SampleRows)
		if err != nil {
			return nil, err
		}
		if err := rr.Reset(); err != nil {
			return nil, fmt.Errorf("resetting reader after inference: %w", err)
		}
		if _, err := j.Source.ReadHeader(); err != nil {
			return nil, err
		}
	}

	// Count rows.
	rowCount := 0
	err := j.Source.ReadRows(ctx, func(row []string) error {
		rowCount++
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &PredictReport{
		Schema:        schema,
		EstimatedRows: rowCount,
		Warnings:      warnings,
	}, nil
}

// convertValue converts a string value to the uint64 representation for the given type.
func convertValue(raw string, ft encoding.FieldType, dict *encoding.Dictionary) (uint64, error) {
	isNull := raw == "" || strings.EqualFold(raw, "null") || strings.EqualFold(raw, "na") || strings.EqualFold(raw, "n/a")

	switch ft {
	case encoding.FieldTypeU8:
		if isNull {
			return 0, nil
		}
		v, err := strconv.ParseUint(raw, 10, 8)
		return v, err

	case encoding.FieldTypeU16:
		if isNull {
			return 0, nil
		}
		v, err := strconv.ParseUint(raw, 10, 16)
		return v, err

	case encoding.FieldTypeU32:
		if isNull {
			return 0, nil
		}
		v, err := strconv.ParseUint(raw, 10, 32)
		return v, err

	case encoding.FieldTypeU64:
		if isNull {
			return 0, nil
		}
		v, err := strconv.ParseUint(raw, 10, 64)
		return v, err

	case encoding.FieldTypeF32:
		if isNull {
			return 0, nil
		}
		f, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return 0, err
		}
		return uint64(math.Float32bits(float32(f))), nil

	case encoding.FieldTypeF64:
		if isNull {
			return 0, nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, err
		}
		return math.Float64bits(f), nil

	case encoding.FieldTypeDate:
		if isNull {
			return 0, nil
		}
		for _, layout := range dateFormats {
			t, err := time.Parse(layout, raw)
			if err == nil {
				// Store as days since Unix epoch.
				days := t.Unix() / 86400
				return uint64(uint32(days)), nil
			}
		}
		return 0, fmt.Errorf("cannot parse date: %q", raw)

	case encoding.FieldTypePackedBool:
		if isNull {
			return 0, nil
		}
		return parseBoolValue(raw)

	case encoding.FieldTypeNullableBool:
		if isNull {
			return 0, nil // 0 = null sentinel for nullable bool
		}
		b, err := parseBoolValue(raw)
		if err != nil {
			return 0, err
		}
		// Encode: 1 = false, 2 = true (0 = null).
		if b == 1 {
			return 2, nil
		}
		return 1, nil

	case encoding.FieldTypeNullableU4:
		if isNull {
			return 0x0F, nil // sentinel for null
		}
		v, err := strconv.ParseUint(raw, 10, 8)
		if err != nil {
			return 0, err
		}
		if v > 14 {
			return 0, fmt.Errorf("value %d exceeds nullable u4 range", v)
		}
		return v, nil

	case encoding.FieldTypeNullableU8:
		if isNull {
			return 0xFF, nil // sentinel
		}
		v, err := strconv.ParseUint(raw, 10, 8)
		return v, err

	case encoding.FieldTypeNullableU16:
		if isNull {
			return 0xFFFF, nil // sentinel
		}
		v, err := strconv.ParseUint(raw, 10, 16)
		return v, err

	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		if isNull {
			return 0, nil
		}
		if dict == nil {
			return 0, fmt.Errorf("no dictionary for categorical field")
		}
		maxEntries := ft.MaxCategoricalEntries()
		id, err := dict.AddWithLimit(raw, maxEntries)
		if err != nil {
			return 0, err
		}
		return uint64(id), nil

	default:
		return 0, fmt.Errorf("unsupported field type: %s", ft)
	}
}

func parseBoolValue(raw string) (uint64, error) {
	switch strings.ToLower(raw) {
	case "true", "yes", "1", "t", "y":
		return 1, nil
	case "false", "no", "0", "f", "n":
		return 0, nil
	default:
		return 0, fmt.Errorf("cannot parse boolean: %q", raw)
	}
}
