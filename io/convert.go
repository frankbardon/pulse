package io

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// Run executes the convert job, streaming from source to target.
// If KeepPulseAt is set, also writes an intermediate .pulse file.
func (j *ConvertJob) Run(ctx context.Context) (*ConvertReport, error) {
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

	// Write header to target.
	columns := make([]string, len(schema.Fields))
	for i, f := range schema.Fields {
		columns[i] = f.Name
	}
	if err := j.Target.WriteHeader(columns); err != nil {
		return nil, err
	}

	// Stream rows: read from source, convert, write to target.
	var rowErrors []RowError
	converted := 0
	rowNum := 0

	// If KeepPulseAt is set, also import to a pulse file.
	var importJob *ImportJob
	if j.KeepPulseAt != "" {
		fs := j.FS
		if fs == nil {
			fs = afero.NewMemMapFs()
		}
		// We will write the pulse file after streaming.
		importJob = &ImportJob{
			Source:     nil, // not used directly
			Target:     j.KeepPulseAt,
			Schema:     schema,
			SampleRows: j.SampleRows,
			FS:         fs,
		}
	}

	// Collect rows for potential pulse output.
	type convertedRow struct {
		values []any
	}
	var allRows []convertedRow

	err := j.Source.ReadRows(ctx, func(row []string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rowNum++

		values := make([]any, len(schema.Fields))
		for i, f := range schema.Fields {
			colIdx := f.CsvColumnIdx
			var raw string
			if colIdx < len(row) {
				raw = strings.TrimSpace(row[colIdx])
			}

			// For categorical fields, add to dictionary and write resolved string.
			if f.Type.IsCategorical() && dicts[i] != nil {
				isNull := raw == "" || strings.EqualFold(raw, "null") || strings.EqualFold(raw, "na") || strings.EqualFold(raw, "n/a")
				if !isNull {
					dicts[i].AddWithLimit(raw, f.Type.MaxCategoricalEntries())
				}
				values[i] = raw
			} else {
				values[i] = raw
			}
		}

		if err := j.Target.WriteRow(values); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Err: err})
		} else {
			converted++
		}

		if importJob != nil {
			allRows = append(allRows, convertedRow{values: values})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	// Write intermediate pulse file if requested.
	if importJob != nil && j.KeepPulseAt != "" {
		rr, ok := j.Source.(ResetReader)
		if ok {
			if err := rr.Reset(); err == nil {
				importJob.Source = j.Source
				if _, err := importJob.Run(ctx); err != nil {
					return nil, fmt.Errorf("writing intermediate pulse file: %w", err)
				}
			}
		}
	}

	return &ConvertReport{
		RowsConverted: converted,
		Schema:        schema,
		RowErrors:     rowErrors,
	}, nil
}

// Predict validates the conversion without writing.
func (j *ConvertJob) Predict(ctx context.Context) (*PredictReport, error) {
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
