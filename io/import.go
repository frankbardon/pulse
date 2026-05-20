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

	// Records are encoded into a separate buffer during the read loop so
	// the per-row scratch slice can be reused. The records buffer is
	// appended to the main buffer after the schema is written, since the
	// schema must precede records and dictionaries are populated as rows
	// are converted.
	var recordsBuf bytes.Buffer
	var rowErrors []RowError
	rowsImported := 0
	rowNum := 0

	// Reusable per-row scratch slices. Narrow types share the uint64 slice;
	// wide types (decimal128) write 16 raw bytes via a parallel slice.
	// Single goroutine via ReadRows callback, so reuse is safe.
	vals := make([]uint64, len(schema.Fields))
	wideBytes := make([][16]byte, len(schema.Fields))
	wideUsed := make([]bool, len(schema.Fields))
	nullMask := make([]bool, len(schema.Fields))
	bitmapSize := schema.BitmapByteSize()

	err := j.Source.ReadRows(ctx, func(row []string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rowNum++

		for i := range wideUsed {
			wideUsed[i] = false
			nullMask[i] = false
		}

		rowOK := true
		for i, f := range schema.Fields {
			colIdx := f.CsvColumnIdx
			var raw string
			if colIdx < len(row) {
				raw = strings.TrimSpace(row[colIdx])
			}

			nullCell := isNullToken(raw)
			if nullCell {
				if !f.Nullable {
					rowErrors = append(rowErrors, RowError{
						Row: rowNum,
						Err: errors.NewCodedErrorWithDetails(
							errors.PULSE_IMPORT_ROW_ERROR,
							fmt.Sprintf("row %d, column %q: null value in non-nullable field", rowNum, f.Name),
							map[string]any{"row": rowNum, "column": f.Name},
						),
					})
					rowOK = false
					break
				}
				nullMask[i] = true
				if isWideFieldType(f.Type) {
					wideBytes[i] = encoding.EncodeDecimal128(encoding.ZeroDecimal128())
					wideUsed[i] = true
				} else {
					vals[i] = 0
				}
				continue
			}

			if isWideFieldType(f.Type) {
				wb, err := convertValueWide(raw, f, dicts[i])
				if err != nil {
					rowErrors = append(rowErrors, RowError{
						Row: rowNum,
						Err: errors.NewCodedErrorWithDetails(
							errors.PULSE_IMPORT_ROW_ERROR,
							fmt.Sprintf("row %d, column %q: %v", rowNum, f.Name, err),
							map[string]any{"row": rowNum, "column": f.Name},
						),
					})
					rowOK = false
					break
				}
				wideBytes[i] = wb
				wideUsed[i] = true
				continue
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
				rowOK = false
				break
			}
			vals[i] = v
		}
		if !rowOK {
			return nil // skip row, continue processing
		}

		// Encode this row immediately. Buffer is owned per call; values
		// are not retained across iterations.
		for i, f := range schema.Fields {
			if wideUsed[i] {
				if _, err := recordsBuf.Write(wideBytes[i][:]); err != nil {
					return err
				}
				continue
			}
			if f.Type.IsBitPacked() {
				// Bit-packed types: write as single byte for simplicity.
				recordsBuf.WriteByte(byte(vals[i]))
				continue
			}
			if err := encoding.WriteFieldValue(&recordsBuf, f.Type, vals[i]); err != nil {
				return err
			}
		}

		// Per-record null bitmap, if schema has any nullable field.
		if bitmapSize > 0 {
			bitmap := make([]byte, bitmapSize)
			for i, f := range schema.Fields {
				if !f.Nullable {
					continue
				}
				if nullMask[i] {
					encoding.BitmapSetNull(bitmap, i)
				}
			}
			if err := encoding.WriteBitmap(&recordsBuf, bitmap); err != nil {
				return err
			}
		}

		rowsImported++
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Now write schema (dictionaries are populated).
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return nil, err
	}

	// Append the encoded records. No record count prefix — the format is
	// header + schema + records per §5.3. Record count is derived from
	// file size and per-record byte size.
	if _, err := buf.Write(recordsBuf.Bytes()); err != nil {
		return nil, err
	}

	// Write to filesystem.
	if err := afero.WriteFile(j.FS, j.Target, buf.Bytes(), 0644); err != nil {
		return nil, err
	}

	return &ImportReport{
		RowsImported: rowsImported,
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

// isNullToken reports whether raw is one of the recognized null-sentinel
// tokens. Matching is case-insensitive: "", "null", "na", "n/a" (any case).
// The fixed set is small enough that a single ToLower + length-gated switch
// outperforms repeated strings.EqualFold calls in the import hot loop.
func isNullToken(raw string) bool {
	switch len(raw) {
	case 0:
		return true
	case 2: // "na" / "NA"
		return (raw[0] == 'n' || raw[0] == 'N') && (raw[1] == 'a' || raw[1] == 'A')
	case 3: // "n/a" / "N/A"
		return (raw[0] == 'n' || raw[0] == 'N') && raw[1] == '/' && (raw[2] == 'a' || raw[2] == 'A')
	case 4: // "null" / "NULL" / etc.
		return (raw[0] == 'n' || raw[0] == 'N') &&
			(raw[1] == 'u' || raw[1] == 'U') &&
			(raw[2] == 'l' || raw[2] == 'L') &&
			(raw[3] == 'l' || raw[3] == 'L')
	}
	return false
}

// isWideFieldType reports whether the field type uses the 16-byte wide
// import path (decimal128).
func isWideFieldType(ft encoding.FieldType) bool {
	return ft == encoding.FieldTypeDecimal128
}

// convertValueWide converts a non-null string value to the 16-byte
// representation for wide field types. Null cells are handled by the
// caller before this is called.
func convertValueWide(raw string, f encoding.Field, _ *encoding.Dictionary) ([16]byte, error) {
	switch f.Type {
	case encoding.FieldTypeDecimal128:
		d, parsedScale, err := encoding.ParseDecimal128(raw)
		if err != nil {
			return [16]byte{}, err
		}
		// Rescale to the field's declared scale.
		if parsedScale != f.Scale {
			d, err = d.Rescale(parsedScale, f.Scale)
			if err != nil {
				return [16]byte{}, err
			}
		}
		if !d.FitsPrecision(f.Precision) {
			return [16]byte{}, errors.NewCodedErrorWithDetails(
				errors.PULSE_DECIMAL_OVERFLOW,
				"decimal value exceeds field precision",
				map[string]any{"value": raw, "precision": f.Precision, "scale": f.Scale})
		}
		return encoding.EncodeDecimal128(d), nil
	default:
		return [16]byte{}, fmt.Errorf("not a wide field type: %s", f.Type)
	}
}

// convertValue converts a non-null string value to the uint64
// representation for the given type. Null cells are handled by the caller
// before this is called; convertValue can assume raw is non-null.
func convertValue(raw string, ft encoding.FieldType, dict *encoding.Dictionary) (uint64, error) {
	switch ft {
	case encoding.FieldTypeU4:
		v, err := strconv.ParseUint(raw, 10, 8)
		if err != nil {
			return 0, err
		}
		if v > 0x0F {
			return 0, fmt.Errorf("value %d exceeds u4 range", v)
		}
		return v, nil

	case encoding.FieldTypeU8:
		v, err := strconv.ParseUint(raw, 10, 8)
		return v, err

	case encoding.FieldTypeU16:
		v, err := strconv.ParseUint(raw, 10, 16)
		return v, err

	case encoding.FieldTypeU32:
		v, err := strconv.ParseUint(raw, 10, 32)
		return v, err

	case encoding.FieldTypeU64:
		v, err := strconv.ParseUint(raw, 10, 64)
		return v, err

	case encoding.FieldTypeF32:
		f, err := strconv.ParseFloat(raw, 32)
		if err != nil {
			return 0, err
		}
		return uint64(math.Float32bits(float32(f))), nil

	case encoding.FieldTypeF64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return 0, err
		}
		return math.Float64bits(f), nil

	case encoding.FieldTypeDate:
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
		return parseBoolValue(raw)

	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
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
