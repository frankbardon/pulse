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
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
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

	// Resolve the include mask. Nil / empty Includes selects every
	// field; otherwise every entry must name a field in schema or the
	// caller is rejected up-front with PULSE_EXPORT_FIELD_UNKNOWN.
	includeMask, err := resolveIncludeMask(schema, j.Includes)
	if err != nil {
		return nil, err
	}

	// Hand the source schema to schema-aware writers so they can build
	// native typed columns for decimal128. Schema-aware writers see
	// the full source schema; the writer is expected to ignore values
	// for projected-out columns because we never emit them. Projection
	// over typed columnar formats (parquet / arrow / excel) is a
	// follow-up — see ExportJob.Includes doc.
	if saw, ok := j.Target.(SchemaAwareWriter); ok {
		saw.SetPulseSchema(schema)
	}

	// Hand Response.Overlays to overlay-aware writers so the format-
	// native sidecar (Arrow / Parquet LIST<STRUCT>, Excel sheets,
	// NDJSON trailer, CSV warn-and-skip) lands before WriteHeader.
	// IncludeOverlays==*false explicitly opts out — the writer never
	// sees the slice, the warning never fires, and the output is byte-
	// identical to a pre-overlay export. nil OR *true emits when the
	// Response carries layers; nil / empty Overlays short-circuits so
	// the byte-identity contract holds.
	if shouldEmitOverlays(j.IncludeOverlays) && len(j.Overlays) > 0 {
		if oaw, ok := j.Target.(OverlayAwareWriter); ok {
			oaw.SetOverlays(j.Overlays)
		}
	}

	// Compute the augmented column layout when label-augment bindings
	// add sibling "<field>_label" columns. augmentInsertAfter records,
	// for each source field index, whether a sibling cell should be
	// appended immediately after that field in every emitted row.
	// Projection-excluded fields never emit augment siblings.
	augmentInsertAfter, augmentFieldNames, replaceFields := planLabelColumns(schema, j.LabelResolver, includeMask)

	// Write header to target.
	columns := make([]string, 0, len(schema.Fields)+len(augmentFieldNames))
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

	// A cohort writer encodes from the cohort's raw storage, so it takes
	// the place of the row loop entirely rather than running beside it.
	// The assertion sits AFTER WriteHeader so such a writer still receives
	// the schema and the projection-aware column list; what it does not
	// receive is a single WriteRow call. See CohortWriter.
	if cw, ok := j.Target.(CohortWriter); ok {
		n, err := cw.WriteCohort(ctx, CohortSource{
			FS:       j.FS,
			Path:     j.Source,
			Includes: j.Includes,
			Labelled: j.LabelResolver != nil,
		})
		if err != nil {
			return nil, err
		}
		report := &ExportReport{RowsExported: n}
		if j.LabelResolver != nil {
			report.LabelWarnings = j.LabelResolver.Warnings()
		}
		report.TargetWarnings = targetWarnings(j.Target)
		if owe, ok := j.Target.(OverlayWarningEmitter); ok {
			if warns := owe.OverlayWarnings(); len(warns) > 0 {
				report.OverlayWarnings = warns
			}
		}
		return report, nil
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

		out := applyExportLabels(values, schema, j.LabelResolver, augmentInsertAfter, replaceFields, includeMask)
		if err := j.Target.WriteRow(out); err != nil {
			rowErrors = append(rowErrors, RowError{Row: row + 1, Err: err})
			row++
			continue
		}
		exported++
		row++
	}

	report := &ExportReport{
		RowsExported: exported,
		RowErrors:    rowErrors,
	}
	if j.LabelResolver != nil {
		report.LabelWarnings = j.LabelResolver.Warnings()
	}
	// Lift per-format overlay warnings (CSV/TSV warn-and-skip surface)
	// onto the report so callers can see the dropped layers without
	// re-querying the writer. Writers that embed overlays natively do
	// not implement OverlayWarningEmitter; the slice stays nil.
	if owe, ok := j.Target.(OverlayWarningEmitter); ok {
		if warns := owe.OverlayWarnings(); len(warns) > 0 {
			report.OverlayWarnings = warns
		}
	}
	report.TargetWarnings = targetWarnings(j.Target)
	return report, nil
}

// targetWarnings lifts a Writer's non-fatal encode diagnostics off the
// optional TargetWarningEmitter contract. A target that does not
// implement it contributes nil, so the report shape is unchanged for
// every pre-existing adapter. Shared by ExportJob.Run and ConvertJob.Run
// so the two verbs cannot diverge on where a target warning lands.
func targetWarnings(w Writer) []*errors.CodedError {
	twe, ok := w.(TargetWarningEmitter)
	if !ok {
		return nil
	}
	warns := twe.Warnings()
	if len(warns) == 0 {
		return nil
	}
	return warns
}

// shouldEmitOverlays maps the tri-state IncludeOverlays pointer onto
// the emit / drop decision the dispatcher uses. Mirrors the
// ExportJob.IncludeOverlays doc:
//
//	nil    ⇒ default emit
//	*true  ⇒ explicit emit
//	*false ⇒ explicit drop
//
// Exported as a package-private helper so ConvertJob.Run shares the
// same arm without duplicating the pointer dereference.
func shouldEmitOverlays(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

// planLabelColumns inspects the resolver-bound fields against the
// schema and returns (insertAfter, augmentNames, replaceFields):
//
//   - insertAfter[i] reports whether schema.Fields[i] should be
//     followed by a sibling label column in the output.
//   - augmentNames is the sorted list of sibling column names
//     (informational; used by callers wanting to inspect the layout).
//   - replaceFields is the set of source-field indices whose value
//     should be rewritten in place by the resolver.
//
// includeMask gates which fields participate. A nil mask is treated
// as "all included" so existing call sites that have no projection
// can pass nil. Passing a nil resolver returns zero values across the
// board, so the caller path stays a single nil-check.
func planLabelColumns(schema *encoding.Schema, r LabelResolver, includeMask []bool) (insertAfter []bool, augmentNames []string, replaceFields map[int]bool) {
	if r == nil || schema == nil {
		return make([]bool, len(schema.Fields)), nil, nil
	}
	insertAfter = make([]bool, len(schema.Fields))
	replaceFields = map[int]bool{}
	for i, f := range schema.Fields {
		if includeMask != nil && !includeMask[i] {
			continue
		}
		if !r.Has(f.Name) {
			continue
		}
		switch r.Mode(f.Name) {
		case types.LabelModeAugment:
			insertAfter[i] = true
			augmentNames = append(augmentNames, f.Name+"_label")
		case types.LabelModeReplace:
			replaceFields[i] = true
		}
	}
	return insertAfter, augmentNames, replaceFields
}

// resolveIncludeMask returns a per-field boolean mask selecting the
// columns to emit. A nil / empty includes slice selects every field.
// Any include entry that does not match a schema field name is
// rejected with PULSE_EXPORT_FIELD_UNKNOWN. Duplicates are silently
// deduplicated; output ordering always follows schema order, never
// the include order, so the on-disk byte layout stays the source of
// truth.
func resolveIncludeMask(schema *encoding.Schema, includes []string) ([]bool, error) {
	mask := make([]bool, len(schema.Fields))
	if len(includes) == 0 {
		for i := range mask {
			mask[i] = true
		}
		return mask, nil
	}
	known := make(map[string]int, len(schema.Fields))
	names := make([]string, 0, len(schema.Fields))
	for i, f := range schema.Fields {
		known[f.Name] = i
		names = append(names, f.Name)
	}
	for _, name := range includes {
		idx, ok := known[name]
		if !ok {
			return nil, errors.NewCodedErrorWithDetails(
				errors.PULSE_EXPORT_FIELD_UNKNOWN,
				fmt.Sprintf("include field %q does not appear in source schema", name),
				map[string]any{
					"field":        name,
					"known_fields": names,
				},
			)
		}
		mask[idx] = true
	}
	return mask, nil
}

// applyExportLabels rewrites the row values per the resolver and
// applies projection. For replace bindings the source-field value is
// overwritten in place; for augment bindings a new cell is inserted
// immediately after the source field. When includeMask filters out
// fields, the returned slice carries only the included source values
// (plus augment siblings for included source fields). The output
// slice is freshly allocated when projection drops at least one
// field or at least one augment binding adds a column; on the
// no-projection, no-augment path the input slice is returned
// untouched (writer implementations already stringify / copy before
// returning). includeMask of nil is treated as "all included".
func applyExportLabels(values []any, schema *encoding.Schema, r LabelResolver, insertAfter []bool, replaceFields map[int]bool, includeMask []bool) []any {
	if r != nil && len(replaceFields) > 0 {
		for idx := range replaceFields {
			s, ok := values[idx].(string)
			if !ok {
				continue
			}
			if out, _, hit := r.Apply(schema.Fields[idx].Name, s); hit {
				values[idx] = out
			}
		}
	}

	hasAugment := r != nil && anyTrue(insertAfter)
	projecting := includeMask != nil && !allTrue(includeMask)
	if !hasAugment && !projecting {
		return values
	}

	out := make([]any, 0, len(values)+countTrue(insertAfter))
	for i, v := range values {
		if includeMask != nil && !includeMask[i] {
			continue
		}
		out = append(out, v)
		if !hasAugment || !insertAfter[i] {
			continue
		}
		raw, ok := v.(string)
		if !ok {
			out = append(out, "")
			continue
		}
		if _, label, hit := r.Apply(schema.Fields[i].Name, raw); hit {
			out = append(out, label)
		} else {
			out = append(out, "")
		}
	}
	return out
}

func allTrue(bs []bool) bool {
	for _, b := range bs {
		if !b {
			return false
		}
	}
	return true
}

func anyTrue(bs []bool) bool {
	for _, b := range bs {
		if b {
			return true
		}
	}
	return false
}

func countTrue(bs []bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// Predict validates the export without writing.
//
// It reads the source header and schema, estimates the record count, and —
// when the Target implements the optional CohortValidator contract — asks the
// target whether it could encode this cohort at all. A Target that is nil or
// does not implement that interface is predicted exactly as it was before the
// interface existed; see CohortValidator for why that promise is the whole
// compatibility story here.
//
// A validating target's refusal is returned as the error, carrying the code
// the real export would carry, and its non-fatal diagnostics land on
// PredictReport.TargetWarnings. Predict remains a SOUND but INCOMPLETE
// filter: a validator only answers from schema and sidecar facts, so passing
// means no schema-level refusal was found, never that the export cannot fail.
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

	report := &PredictReport{
		Schema:        schema,
		EstimatedRows: estimatedRows,
	}

	// Ask the target whether it could encode this cohort. The assertion
	// fails for a nil Target and for every writer that does not implement
	// the interface, and the report is then exactly the one this function
	// has always returned.
	//
	// CohortSource is built from the same three job slots Run builds it
	// from, so a validator sees the projection and label state the real
	// export would hand it and can refuse them on the same terms. What it
	// deliberately does NOT get is SetPulseSchema or WriteHeader: predict
	// starts no write lifecycle on a writer it will never Close.
	if cv, ok := j.Target.(CohortValidator); ok {
		warns, err := cv.ValidateCohort(ctx, CohortSource{
			FS:       j.FS,
			Path:     j.Source,
			Includes: j.Includes,
			Labelled: j.LabelResolver != nil,
		})
		if err != nil {
			return nil, err
		}
		if len(warns) > 0 {
			report.TargetWarnings = warns
		}
	}

	return report, nil
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

	case encoding.FieldTypeDateTime:
		// encoding.CanonicalDateTimeLayout — the exact inverse of the
		// encoding.ParseDateTime call io/import.go's convertValue makes,
		// so a datetime survives export → re-import byte-for-byte
		// including its time-of-day. Always rendered in UTC, matching
		// the naive-UTC storage policy.
		return encoding.FormatDateTime(raw)

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
