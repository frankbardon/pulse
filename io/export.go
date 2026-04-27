package io

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// Run executes the export job, converting a .pulse file to a tabular target.
func (j *ExportJob) Run(ctx context.Context) (*ExportReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("ExportJob.FS is required")
	}

	data, err := afero.ReadFile(j.FS, j.Source)
	if err != nil {
		return nil, fmt.Errorf("reading pulse file: %w", err)
	}

	r := bytes.NewReader(data)

	// Read header.
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}

	// Read schema.
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}

	// Write header to target.
	columns := make([]string, len(schema.Fields))
	for i, f := range schema.Fields {
		columns[i] = f.Name
	}
	if err := j.Target.WriteHeader(columns); err != nil {
		return nil, err
	}

	// Read and export records until EOF.
	var rowErrors []RowError
	exported := 0
	row := 0

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		values := make([]any, len(schema.Fields))
		hitEOF := false
		for i, f := range schema.Fields {
			if f.Type == encoding.FieldTypePackedBool || f.Type == encoding.FieldTypeNullableBool || f.Type == encoding.FieldTypeNullableU4 {
				var b [1]byte
				if _, err := r.Read(b[:]); err != nil {
					hitEOF = true
					break
				}
				values[i] = formatPackedValue(f.Type, b[0], f.Dictionary)
				continue
			}

			raw, err := encoding.ReadFieldValue(r, f.Type)
			if err != nil {
				hitEOF = true
				break
			}
			values[i] = formatFieldValue(f.Type, raw, f.Dictionary)
		}

		if hitEOF {
			break
		}

		if err := j.Target.WriteRow(values); err != nil {
			rowErrors = append(rowErrors, RowError{Row: row + 1, Err: err})
			row++
			continue
		}
		exported++
		row++
	}

	return &ExportReport{
		RowsExported: exported,
		RowErrors:    rowErrors,
	}, nil
}

// Predict validates the export without writing.
func (j *ExportJob) Predict(ctx context.Context) (*PredictReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("ExportJob.FS is required")
	}

	data, err := afero.ReadFile(j.FS, j.Source)
	if err != nil {
		return nil, fmt.Errorf("reading pulse file: %w", err)
	}

	r := bytes.NewReader(data)

	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}

	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}

	// Estimate record count from remaining bytes and per-record size.
	recordSize := 0
	for _, f := range schema.Fields {
		recordSize += f.Type.ByteSize()
	}
	estimatedRows := 0
	if recordSize > 0 {
		estimatedRows = r.Len() / recordSize
	}

	return &PredictReport{
		Schema:        schema,
		EstimatedRows: estimatedRows,
	}, nil
}

// formatFieldValue converts a raw uint64 value back to a string representation.
func formatFieldValue(ft encoding.FieldType, raw uint64, dict *encoding.Dictionary) string {
	switch ft {
	case encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32, encoding.FieldTypeU64:
		return strconv.FormatUint(raw, 10)

	case encoding.FieldTypeF32:
		f := math.Float32frombits(uint32(raw))
		return strconv.FormatFloat(float64(f), 'f', -1, 32)

	case encoding.FieldTypeF64:
		f := math.Float64frombits(raw)
		return strconv.FormatFloat(f, 'f', -1, 64)

	case encoding.FieldTypeDate:
		days := int64(uint32(raw))
		t := time.Unix(days*86400, 0).UTC()
		return t.Format("2006-01-02")

	case encoding.FieldTypeNullableU8:
		if raw == 0xFF {
			return ""
		}
		return strconv.FormatUint(raw, 10)

	case encoding.FieldTypeNullableU16:
		if raw == 0xFFFF {
			return ""
		}
		return strconv.FormatUint(raw, 10)

	case encoding.FieldTypeCategoricalU8, encoding.FieldTypeCategoricalU16, encoding.FieldTypeCategoricalU32:
		if dict != nil {
			return dict.Resolve(uint32(raw))
		}
		return strconv.FormatUint(raw, 10)

	default:
		return strconv.FormatUint(raw, 10)
	}
}

// formatPackedValue formats bit-packed types.
func formatPackedValue(ft encoding.FieldType, b byte, dict *encoding.Dictionary) string {
	switch ft {
	case encoding.FieldTypePackedBool:
		if b != 0 {
			return "true"
		}
		return "false"

	case encoding.FieldTypeNullableBool:
		switch b {
		case 0:
			return ""
		case 1:
			return "false"
		case 2:
			return "true"
		default:
			return ""
		}

	case encoding.FieldTypeNullableU4:
		if b == 0x0F {
			return ""
		}
		return strconv.Itoa(int(b))

	default:
		return strconv.Itoa(int(b))
	}
}
