package io

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// exportReadBufferSize is the buffered-reader size used when streaming a
// .pulse file during export. 64KB amortises syscall overhead without holding
// the full catalog in RAM.
const exportReadBufferSize = 64 << 10

// Run executes the export job, converting a .pulse file to a tabular target.
func (j *ExportJob) Run(ctx context.Context) (*ExportReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("ExportJob.FS is required")
	}

	f, err := j.FS.Open(j.Source)
	if err != nil {
		return nil, fmt.Errorf("reading pulse file: %w", err)
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, exportReadBufferSize)

	// Read header.
	if err := encoding.ReadHeader(r); err != nil {
		return nil, err
	}

	// Read schema.
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		return nil, err
	}

	// Hand the source schema to schema-aware writers so they can build
	// native typed columns for decimal128.
	if saw, ok := j.Target.(SchemaAwareWriter); ok {
		saw.SetPulseSchema(schema)
	}

	// Write header to target.
	columns := make([]string, len(schema.Fields))
	for i, f := range schema.Fields {
		columns[i] = f.Name
	}
	if err := j.Target.WriteHeader(columns); err != nil {
		return nil, err
	}

	_, schemaAware := j.Target.(SchemaAwareWriter)

	// Read and export records until EOF. The values slice is hoisted out of
	// the loop and reused per row; every Writer implementation either
	// stringifies, marshals, or copies values before returning, so retaining
	// no references after WriteRow is safe.
	var rowErrors []RowError
	exported := 0
	row := 0
	values := make([]any, len(schema.Fields))

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		hitEOF := false
		for i, f := range schema.Fields {
			if f.Type.IsBitPacked() {
				b, err := r.ReadByte()
				if err != nil {
					hitEOF = true
					break
				}
				values[i] = formatPackedValue(f.Type, b)
				continue
			}

			if f.Type.IsDecimal() {
				d, err := encoding.ReadDecimal128(r)
				if err != nil {
					hitEOF = true
					break
				}
				if schemaAware {
					values[i] = d
				} else {
					values[i] = d.String(f.Scale)
				}
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

		// Apply trailing null bitmap, if schema declares any nullable field.
		if bmSize := schema.BitmapByteSize(); bmSize > 0 {
			bitmap, err := encoding.ReadBitmap(r, bmSize)
			if err != nil {
				break
			}
			for i, f := range schema.Fields {
				if f.Nullable && encoding.BitmapIsNull(bitmap, i) {
					values[i] = ""
				}
			}
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
	recordSize := schema.RecordByteSize()
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
// Null state is applied separately by the export loop via the per-record
// bitmap; this function never sees a null cell.
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
func formatPackedValue(ft encoding.FieldType, b byte) string {
	switch ft {
	case encoding.FieldTypePackedBool:
		if b != 0 {
			return "true"
		}
		return "false"

	case encoding.FieldTypeU4:
		return strconv.Itoa(int(b & 0x0F))

	default:
		return strconv.Itoa(int(b))
	}
}
