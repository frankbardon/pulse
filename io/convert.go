package io

import (
	"context"
	"fmt"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Run executes the convert job, streaming from source to target.
// If KeepPulseAt is set, also writes an intermediate .pulse file.
func (j *ConvertJob) Run(ctx context.Context) (*ConvertReport, error) {
	schema := j.Schema
	var inferWarnings []InferenceWarning

	// A SchemaAwareReader source hands over an authoritative schema — the
	// source's own dictionary, not a guess — and convert adopts it before
	// inference is even considered, exactly as ImportJob.Run does. Without
	// this, `pulse convert survey.sav out.csv` would re-infer every column
	// type from the cell text the reader renders and throw the source
	// dictionary away, which is the precise fidelity loss SchemaAwareReader
	// exists to prevent. An explicit ConvertJob.Schema still wins outright:
	// the caller is the most specific instruction.
	authoritative := false
	if schema == nil {
		src, err := j.sourceSchema()
		if err != nil {
			return nil, err
		}
		if src != nil {
			schema = src
			authoritative = true
		}
	}

	// When convert INFERS the schema, the intermediate .pulse re-import must
	// inherit the same out-of-sample null tolerance (promote-on-null) — the
	// schema handed to importJob is a guess, not a user contract. An
	// authoritative schema is not inference-originated, so it never promotes:
	// a null in a field the source declares non-nullable stays a row error.
	inferredSchema := !authoritative && j.Schema == nil

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

	includeMask, err := resolveIncludeMask(schema, j.Includes)
	if err != nil {
		return nil, err
	}

	augmentInsertAfter, _, replaceFields := planLabelColumns(schema, j.LabelResolver, includeMask)

	// Hand Response.Overlays to overlay-aware writers so the export
	// half embeds the layers in the format-native sidecar. Same tri-
	// state semantics as ExportJob.Run — *false opts out, nil / *true
	// emits when Overlays carry layers. Must happen BEFORE WriteHeader
	// so the writer's lazily-built schema includes the overlay column.
	if shouldEmitOverlays(j.IncludeOverlays) && len(j.Overlays) > 0 {
		if oaw, ok := j.Target.(OverlayAwareWriter); ok {
			oaw.SetOverlays(j.Overlays)
		}
	}

	// Write header to target. Projection drops excluded fields from
	// the output header; the intermediate .pulse file (when
	// KeepPulseAt is set) still carries the full schema below.
	columns := make([]string, 0, len(schema.Fields))
	for i, f := range schema.Fields {
		if !includeMask[i] {
			continue
		}
		columns = append(columns, f.Name)
		if augmentInsertAfter[i] {
			columns = append(columns, f.Name+"_label")
		}
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
			Source:         nil, // not used directly
			Target:         j.KeepPulseAt,
			Schema:         schema,
			SampleRows:     j.SampleRows,
			FS:             fs,
			InferredSchema: inferredSchema,
		}
	}

	// Collect rows for potential pulse output.
	type convertedRow struct {
		values []any
	}
	var allRows []convertedRow

	err = j.Source.ReadRows(ctx, func(row []string) error {
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

		out := applyExportLabels(values, schema, j.LabelResolver, augmentInsertAfter, replaceFields, includeMask)
		if err := j.Target.WriteRow(out); err != nil {
			rowErrors = append(rowErrors, RowError{Row: rowNum, Err: err})
		} else {
			converted++
		}

		if importJob != nil {
			// Persist the *pre-label* values so the intermediate .pulse
			// file mirrors source semantics; label translation is an
			// output-time overlay, not an on-disk schema change.
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

	report := &ConvertReport{
		RowsConverted:  converted,
		Schema:         schema,
		RowErrors:      rowErrors,
		SourceWarnings: j.sourceWarnings(),
		TargetWarnings: targetWarnings(j.Target),
	}
	if j.LabelResolver != nil {
		report.LabelWarnings = j.LabelResolver.Warnings()
	}
	// Lift overlay warn-and-skip codes (CSV / TSV) onto the report so
	// callers see the dropped layers without re-querying the writer.
	if owe, ok := j.Target.(OverlayWarningEmitter); ok {
		if warns := owe.OverlayWarnings(); len(warns) > 0 {
			report.OverlayWarnings = warns
		}
	}
	return report, nil
}

// Predict validates the conversion without writing.
func (j *ConvertJob) Predict(ctx context.Context) (*PredictReport, error) {
	schema := j.Schema
	var warnings []InferenceWarning

	// Mirror Run's schema resolution so a predicted convert reports the
	// schema Run would actually use — an authoritative source schema
	// bypasses inference here too.
	if schema == nil {
		src, err := j.sourceSchema()
		if err != nil {
			return nil, err
		}
		if src != nil {
			schema = src
		}
	}

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
		Schema:         schema,
		EstimatedRows:  rowCount,
		Warnings:       warnings,
		SourceWarnings: j.sourceWarnings(),
	}, nil
}

// sourceSchema pulls the authoritative schema off a SchemaAwareReader
// source. It delegates to the shared validation an ImportJob performs so
// the two verbs cannot diverge on what counts as a usable authoritative
// schema — a nil return is the deliberate opt-out and falls back to
// inference; a non-nil error fails the convert rather than quietly
// producing a differently-typed output.
func (j *ConvertJob) sourceSchema() (*encoding.Schema, error) {
	return readerSchema(j.Source)
}

// sourceWarnings lifts the source Reader's non-fatal diagnostics off the
// optional SourceWarningEmitter contract. See ImportJob.sourceWarnings.
func (j *ConvertJob) sourceWarnings() []*perrors.CodedError {
	swe, ok := j.Source.(SourceWarningEmitter)
	if !ok {
		return nil
	}
	warns := swe.Warnings()
	if len(warns) == 0 {
		return nil
	}
	return warns
}
