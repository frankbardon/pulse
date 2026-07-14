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

	// An inferred import (no user-authored schema) tolerates out-of-sample
	// nulls: a null cell in a column the bounded inference sample marked
	// non-nullable promotes that field to nullable rather than failing the
	// row. Schema == nil is the common case; ImportJob.InferredSchema lets a
	// caller (ConvertJob's KeepPulseAt re-import) opt a pre-built inferred
	// schema into the same tolerance. A user-authored explicit schema keeps
	// the strict PULSE_IMPORT_ROW_ERROR — the declared nullability is a
	// contract, not a guess.
	inferredSchema := schema == nil || j.InferredSchema

	// Infer schema if not provided.
	if schema == nil {
		rr, ok := j.Source.(ResetReader)
		if !ok {
			return nil, fmt.Errorf("schema inference requires a ResetReader source")
		}
		res, err := InferSchemaWithOptions(j.Source, InferOptions{
			SampleRows:          j.SampleRows,
			SetInferenceMinPct:  j.SetInferenceMinPct,
			ColumnTypeOverrides: j.ColumnTypeOverrides,
		})
		if err != nil {
			return nil, err
		}
		schema = res.Schema
		inferWarnings = res.Warnings
		if j.SetDelimiters == nil {
			j.SetDelimiters = res.Delimiters
		} else {
			for k, v := range res.Delimiters {
				if _, present := j.SetDelimiters[k]; !present {
					j.SetDelimiters[k] = v
				}
			}
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

	// Build dictionaries for categorical and set fields. Both share
	// the inline-dictionary block on the .pulse codec; set fields use
	// the dictionary bit-positions as on-wire mask bits.
	dicts := make(map[int]*encoding.Dictionary)
	for i := range schema.Fields {
		if schema.Fields[i].Type.HasDictionary() {
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
	// Field bytes and per-record null bitmaps are encoded into two parallel
	// buffers. Keeping them separate lets an inferred import promote a field
	// to nullable mid-pass (see the null branch below): the null bitmap is
	// always ceil(field_count/8) bytes regardless of how many fields are
	// nullable, so promotion never re-lays-out the fixed field stride. At
	// finalize the two buffers are interleaved into the record region iff the
	// final schema carries any nullable field — byte-identical to inlining
	// the bitmap after each record, and a no-op (field bytes only) when no
	// field is nullable.
	var recordsBuf bytes.Buffer
	var bitmapBuf bytes.Buffer
	var rowErrors []RowError
	rowsImported := 0
	rowNum := 0

	// fullBitmapSize is the per-record bitmap width once any field is
	// nullable. Computed from field count (not nullable count) so it is
	// stable under mid-pass promotion. writeBitmap decides whether to emit a
	// bitmap per record: always for inferred imports (a later promotion may
	// need it) and for explicit schemas that already declare a nullable
	// field.
	fullBitmapSize := (len(schema.Fields) + 7) / 8
	writeBitmap := inferredSchema || schema.HasBitmap()

	// promoted[i] records that field i was widened to nullable because of an
	// out-of-sample null; surfaced via ImportReport.PromotedFields and a
	// PULSE_IMPORT_NULL_PROMOTED warning.
	promoted := make([]bool, len(schema.Fields))

	// Reusable per-row scratch slices. Narrow types share the uint64 slice;
	// wide types (decimal128) write 16 raw bytes via a parallel slice.
	// Single goroutine via ReadRows callback, so reuse is safe.
	vals := make([]uint64, len(schema.Fields))
	wideBytes := make([][16]byte, len(schema.Fields))
	wideUsed := make([]bool, len(schema.Fields))
	nullMask := make([]bool, len(schema.Fields))

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
					if !inferredSchema {
						// Explicit schema declared this field non-nullable —
						// a null here is a contract violation, not a guess.
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
					// Inferred schema: the bounded sample missed this null.
					// Promote the field to nullable and carry on. The field
					// stride is unchanged and every record already reserves a
					// bitmap byte for index i (writeBitmap is true for inferred
					// imports), so prior records — whose bit i is 0 because
					// they were non-null — stay valid.
					schema.Fields[i].Nullable = true
					promoted[i] = true
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

			v, err := convertValue(raw, f.Type, dicts[i], j.setDelimiterFor(f.Name))
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

		// Per-record null bitmap into the parallel buffer. Emitted whenever a
		// bitmap may be needed (writeBitmap); interleaved into the record
		// region at finalize only if the final schema is nullable. A cell is
		// only ever null-masked for a nullable field (explicit non-nullable
		// nulls break the row above), so setting bit i for nullMask[i] is safe.
		if writeBitmap {
			bitmap := make([]byte, fullBitmapSize)
			for i := range schema.Fields {
				if nullMask[i] {
					encoding.BitmapSetNull(bitmap, i)
				}
			}
			if err := encoding.WriteBitmap(&bitmapBuf, bitmap); err != nil {
				return err
			}
		}

		rowsImported++
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Now write schema (dictionaries are populated, promotions applied).
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		return nil, err
	}

	// Append the encoded records. No record count prefix — the format is
	// header + schema + records per §5.3. Record count is derived from file
	// size and per-record byte size. Interleave the parallel bitmap buffer
	// after each record's field bytes iff the final schema is nullable;
	// otherwise the field bytes stand alone (zero-overhead path).
	if schema.HasBitmap() {
		recs := recordsBuf.Bytes()
		bms := bitmapBuf.Bytes()
		fieldStride := schema.RecordByteSize() - fullBitmapSize
		for k := 0; k < rowsImported; k++ {
			if _, err := buf.Write(recs[k*fieldStride : (k+1)*fieldStride]); err != nil {
				return nil, err
			}
			if _, err := buf.Write(bms[k*fullBitmapSize : (k+1)*fullBitmapSize]); err != nil {
				return nil, err
			}
		}
	} else if _, err := buf.Write(recordsBuf.Bytes()); err != nil {
		return nil, err
	}

	// Write to filesystem.
	if err := afero.WriteFile(j.FS, j.Target, buf.Bytes(), 0644); err != nil {
		return nil, err
	}

	// Collect promoted field names in schema order for the report + warning.
	var promotedFields []string
	for i := range schema.Fields {
		if promoted[i] {
			promotedFields = append(promotedFields, schema.Fields[i].Name)
		}
	}

	return &ImportReport{
		RowsImported:   rowsImported,
		Schema:         schema,
		RowErrors:      rowErrors,
		PromotedFields: promotedFields,
	}, nil
}

// Predict validates the import without writing any output.
func (j *ImportJob) Predict(ctx context.Context) (*PredictReport, error) {
	schema := j.Schema
	var warnings []InferenceWarning
	inferredSchema := schema == nil || j.InferredSchema

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

	// Count rows. Predict already walks every row, so for an inferred
	// schema it also finalizes nullability here: a null cell outside the
	// bounded inference sample promotes its field to nullable so the
	// reported schema matches what Run would write. Free — no extra read.
	rowCount := 0
	promoted := make([]bool, len(schema.Fields))
	err := j.Source.ReadRows(ctx, func(row []string) error {
		rowCount++
		if inferredSchema {
			for i := range schema.Fields {
				if schema.Fields[i].Nullable {
					continue
				}
				colIdx := schema.Fields[i].CsvColumnIdx
				if colIdx < len(row) && isNullToken(strings.TrimSpace(row[colIdx])) {
					schema.Fields[i].Nullable = true
					promoted[i] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for i := range schema.Fields {
		if promoted[i] {
			warnings = append(warnings, InferenceWarning{
				Column:  schema.Fields[i].Name,
				Message: "null found outside the inference sample window; field promoted to nullable",
			})
		}
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

// setDelimiterFor returns the configured delimiter for a set-typed
// column, falling back to DefaultSetDelimiter when no entry exists
// (explicit-schema imports or columns whose inference did not produce
// a delimiter hint). Returns "|" for the empty / missing case so
// convertValue always has a deterministic split character.
func (j *ImportJob) setDelimiterFor(name string) string {
	if j == nil || j.SetDelimiters == nil {
		return DefaultSetDelimiter
	}
	if d, ok := j.SetDelimiters[name]; ok && d != "" {
		return d
	}
	return DefaultSetDelimiter
}

// convertValue converts a non-null string value to the uint64
// representation for the given type. Null cells are handled by the caller
// before this is called; convertValue can assume raw is non-null. The
// setDelim argument is consumed only by set_* field types; other types
// ignore it. Pass DefaultSetDelimiter when set_* paths are not exercised.
func convertValue(raw string, ft encoding.FieldType, dict *encoding.Dictionary, setDelim string) (uint64, error) {
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

	case encoding.FieldTypeSetU8, encoding.FieldTypeSetU16, encoding.FieldTypeSetU32, encoding.FieldTypeSetU64:
		if dict == nil {
			return 0, fmt.Errorf("no dictionary for set field")
		}
		delim := setDelim
		if delim == "" {
			delim = DefaultSetDelimiter
		}
		tokens := splitSetTokens(raw, delim)
		var mask uint64
		maxEntries := ft.MaxSetEntries()
		for _, tok := range tokens {
			id, err := dict.AddWithLimit(tok, maxEntries)
			if err != nil {
				return 0, errors.NewCodedErrorWithDetails(
					errors.PULSE_IMPORT_SET_OVERFLOW,
					fmt.Sprintf("set dictionary overflowed %s (max %d entries)", ft, maxEntries),
					map[string]any{"type": string(ft), "max_entries": maxEntries, "token": tok})
			}
			mask |= uint64(1) << id
		}
		return mask, nil

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
