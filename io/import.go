package io

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Run executes the import job, converting tabular source into a .pulse file.
//
// Total failure is an ERROR, not a zero-row report. If the row pass reads
// at least one row and converts none of them, Run returns a nil report and
// a coded error (PULSE_IMPORT_ROW_ERROR, or the first row error's own code
// when it carries one) whose details name the row count, the failure count
// and the first failure — and no .pulse file is written. Partial failure
// is unchanged: some rows in, some on ImportReport.RowErrors, nil error. A
// source with no data rows at all is an empty cohort, which is a legitimate
// outcome and not an error. See totalRowFailure.
func (j *ImportJob) Run(ctx context.Context) (*ImportReport, error) {
	if j.FS == nil {
		return nil, fmt.Errorf("ImportJob.FS is required")
	}

	schema := j.Schema
	var inferWarnings []InferenceWarning

	// A SchemaAwareReader source hands over an authoritative schema — a
	// source-side dictionary, not a guess — and the whole inference pass is
	// skipped. Only consulted when the caller supplied no explicit Schema:
	// ImportJob.Schema is the most specific instruction and wins outright.
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

	// An inferred import (no user-authored schema) tolerates out-of-sample
	// nulls: a null cell in a column the bounded inference sample marked
	// non-nullable promotes that field to nullable rather than failing the
	// row. Schema == nil is the common case; ImportJob.InferredSchema lets a
	// caller (ConvertJob's KeepPulseAt re-import) opt a pre-built inferred
	// schema into the same tolerance. A user-authored explicit schema keeps
	// the strict PULSE_IMPORT_ROW_ERROR — the declared nullability is a
	// contract, not a guess.
	//
	// An authoritative schema is never inference-originated, so it never
	// promotes, and InferredSchema cannot re-enable the tolerance for it.
	inferredSchema := !authoritative && (schema == nil || j.InferredSchema)

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

	// Reusable per-row scratch slices. Narrow types share the uint64
	// slice; wide types (decimal128, set_u128, set_u256) write raw bytes
	// via a parallel slice sized for the widest of them (32 bytes, a
	// set_u256 mask) and sliced down to the field's own ByteSize on
	// write. Single goroutine via ReadRows callback, so reuse is safe.
	vals := make([]uint64, len(schema.Fields))
	wideBytes := make([]wideFieldBytes, len(schema.Fields))
	wideUsed := make([]bool, len(schema.Fields))
	nullMask := make([]bool, len(schema.Fields))

	// set_* token delimiter. ImportJob.SetDelimiters is inference-derived
	// (or caller-supplied to match an inference-derived schema), so it is
	// inert for an authoritative schema: a SchemaAwareReader builds set_*
	// membership from the source's own set definitions and joins the tokens
	// with DefaultSetDelimiter. See SchemaAwareReader.
	setDelimiterFor := j.setDelimiterFor
	if authoritative {
		setDelimiterFor = func(string) string { return DefaultSetDelimiter }
	}

	// Optional source-declared null channel. A []string row cannot tell
	// a JSON null from an empty JSON string, or an Arrow validity bit
	// from a present empty cell; a NullAwareReader reports the
	// difference alongside the row. Nil for every other source, which
	// leaves the text path byte-identical. See NullAwareReader.
	nullSource, _ := j.Source.(NullAwareReader)

	err := j.Source.ReadRows(ctx, func(row []string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		rowNum++

		var declaredNulls []bool
		if nullSource != nil {
			declaredNulls = nullSource.RowNulls()
		}

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

			nullCell := isNullCell(raw, f.Type, declaredNulls, colIdx)
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
					// Null rides the per-record bitmap; the payload is
					// a zeroed placeholder of the field's FULL width.
					// For a set that zero is also the empty mask, which
					// is only reachable as a VALUE on a non-null cell.
					wideBytes[i] = wideFieldBytes{}
					if f.Type == encoding.FieldTypeDecimal128 {
						enc := encoding.EncodeDecimal128(encoding.ZeroDecimal128())
						copy(wideBytes[i][:], enc[:])
					}
					wideUsed[i] = true
				} else {
					vals[i] = 0
				}
				continue
			}

			if isWideFieldType(f.Type) {
				wb, err := convertValueWide(raw, f, dicts[i], setDelimiterFor(f.Name))
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

			v, err := convertValue(raw, f.Type, dicts[i], setDelimiterFor(f.Name))
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
				// Slice to the field's own width: 16 for decimal128 and
				// set_u128, 32 for set_u256. Writing the whole scratch
				// array would pad every decimal by 16 zero bytes and
				// desynchronize the stride for the rest of the file.
				if _, err := recordsBuf.Write(wideBytes[i][:f.Type.ByteSize()]); err != nil {
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

	// A pass that read rows and converted none of them is total failure,
	// not an empty import. Refuse BEFORE the cohort is written: a zero-row
	// .pulse left next to a hard error is a trap for whatever opens it
	// next. An empty source (no rows, no row errors) is untouched by this
	// and still produces a legitimate empty cohort. See totalRowFailure.
	if failure := totalRowFailure("import", rowsImported, rowNum, rowErrors, errors.PULSE_IMPORT_ROW_ERROR); failure != nil {
		return nil, failure
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

	// Persist whatever source metadata the `.pulse` format cannot hold.
	// Strictly after the cohort write: a SidecarEmitter fingerprints the
	// cohort it describes, so the bytes have to be on the filesystem
	// first. A source that does not implement the interface takes no
	// part in this and its import is unchanged.
	if err := j.emitSidecar(); err != nil {
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
		SourceWarnings: j.sourceWarnings(),
	}, nil
}

// Predict validates the import without writing any output.
func (j *ImportJob) Predict(ctx context.Context) (*PredictReport, error) {
	schema := j.Schema
	var warnings []InferenceWarning

	// Predict mirrors Run's schema resolution so a predicted schema is the
	// schema Run would write: an authoritative SchemaAwareReader schema
	// bypasses inference here too, and never null-promotes.
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
	inferredSchema := !authoritative && (schema == nil || j.InferredSchema)

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
	// The same source-declared null channel Run consults, for the same
	// reason: a predicted schema that promoted a field to nullable on
	// an empty cell the source calls PRESENT would not match the one
	// Run writes.
	nullSource, _ := j.Source.(NullAwareReader)
	err := j.Source.ReadRows(ctx, func(row []string) error {
		rowCount++
		if inferredSchema {
			var declaredNulls []bool
			if nullSource != nil {
				declaredNulls = nullSource.RowNulls()
			}
			for i := range schema.Fields {
				if schema.Fields[i].Nullable {
					continue
				}
				colIdx := schema.Fields[i].CsvColumnIdx
				if colIdx < len(row) &&
					isNullCell(strings.TrimSpace(row[colIdx]), schema.Fields[i].Type, declaredNulls, colIdx) {
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
		Schema:         schema,
		EstimatedRows:  rowCount,
		Warnings:       warnings,
		SourceWarnings: j.sourceWarnings(),
	}, nil
}

// sourceWarnings lifts the source Reader's non-fatal diagnostics off the
// optional SourceWarningEmitter contract. Called after the row pass, so
// the dictionary's, the mapping's and the data pass's warnings are all
// knowable. Returns nil — not an empty slice — for readers that do not
// implement the interface, keeping the report byte-identical for every
// adapter that predates it.
func (j *ImportJob) sourceWarnings() []*errors.CodedError {
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

// emitSidecar gives the source Reader a chance to persist metadata the
// `.pulse` format has nowhere to hold, via the optional SidecarEmitter
// contract. Called after the cohort write, so the cohort can be
// fingerprinted; see SidecarEmitter for why a failure here fails the
// import rather than warning.
//
// A Reader that does not implement the interface returns nil without
// touching the filesystem, which is what keeps every other format's
// import byte-identical.
func (j *ImportJob) emitSidecar() error {
	se, ok := j.Source.(SidecarEmitter)
	if !ok {
		return nil
	}
	return se.WriteSidecar(j.FS, j.Target)
}

// sourceSchema pulls the authoritative schema from a SchemaAwareReader
// source, mirroring the SetPulseSchema push ExportJob.Run performs on a
// SchemaAwareWriter target. Returns (nil, nil) — fall back to inference —
// when the source does not implement the interface at all, or implements
// it and declines by returning a nil schema. See SchemaAwareReader for the
// full precedence contract.
//
// A non-nil error is never swallowed: a source that carries a dictionary
// and failed to read it must not quietly produce a differently-typed
// cohort from re-guessed cell text.
func (j *ImportJob) sourceSchema() (*encoding.Schema, error) {
	return readerSchema(j.Source)
}

// readerSchema is the shared SchemaAwareReader resolution both ImportJob
// and ConvertJob run. It lives in one place so the two verbs cannot
// diverge on what counts as a usable authoritative schema — a convert
// that validated less strictly than an import would let a malformed
// contract through on the path that also writes the intermediate .pulse
// file.
func readerSchema(source Reader) (*encoding.Schema, error) {
	sar, ok := source.(SchemaAwareReader)
	if !ok {
		return nil, nil
	}
	schema, err := sar.PulseSchema()
	if err != nil {
		return nil, fmt.Errorf("reading authoritative source schema: %w", err)
	}
	if schema == nil {
		// Deliberate opt-out: this source has no authoritative schema.
		return nil, nil
	}
	if len(schema.Fields) == 0 {
		return nil, fmt.Errorf("authoritative source schema has no fields")
	}
	for i := range schema.Fields {
		if schema.Fields[i].CsvColumnIdx < 0 {
			return nil, fmt.Errorf(
				"authoritative source schema: field %q has negative CsvColumnIdx %d",
				schema.Fields[i].Name, schema.Fields[i].CsvColumnIdx)
		}
	}
	return schema, nil
}

// isNullCell is the one place the import decides a cell is absent. It
// fuses the text null tokens with the optional source-declared null
// channel (NullAwareReader), and both ImportJob.Run and
// ImportJob.Predict call it so a predicted schema cannot disagree with
// the written one about which cells are null.
//
// declaredNulls is nil for a source that does not implement
// NullAwareReader, and that case is exactly the old behaviour: only
// isNullToken decides.
//
// With a channel, two rules apply:
//
//   - A DECLARED null is null, whatever the text. The source knows.
//   - A cell the source declares PRESENT keeps its text as a value only
//     where the type can hold it. The empty string is a legal value for
//     a dictionary-bearing non-set type and for nothing else, so that
//     is the single case where an empty cell survives as a value; in a
//     u32 or a date column "" is still a null, because reading it as a
//     value would turn a recoverable null into a per-row import error.
//
// set_* is excluded deliberately. Its present-but-empty state already
// has the EmptySetCell spelling that every adapter shares, and routing
// it through a second mechanism here would make the set tri-state
// behave differently on the four adapters that have a null channel.
func isNullCell(raw string, ft encoding.FieldType, declaredNulls []bool, colIdx int) bool {
	if colIdx >= 0 && colIdx < len(declaredNulls) {
		if declaredNulls[colIdx] {
			return true
		}
		if raw == "" && ft.HasDictionary() && !ft.IsSet() {
			return false
		}
	}
	return isNullToken(raw)
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

// maxWideFieldBytes is the widest payload the raw-bytes import path can
// carry: 32 bytes, the on-wire width of a set_u256 mask. decimal128 and
// set_u128 use the first 16 bytes and leave the rest untouched, so every
// caller MUST slice by the field's own FieldType.ByteSize() rather than
// writing the whole array — a fixed 32-byte write would shift every
// column after a decimal by 16 bytes on every record.
const maxWideFieldBytes = encoding.SetMaskWords * 8

// wideFieldBytes is the scratch cell for one raw-bytes field value.
type wideFieldBytes = [maxWideFieldBytes]byte

// isWideFieldType reports whether the field type bypasses the uint64
// convertValue path and writes raw bytes instead — decimal128 (16 bytes)
// and the wide set rungs set_u128 (16) / set_u256 (32), whose bitmasks
// do not fit a uint64 at all.
func isWideFieldType(ft encoding.FieldType) bool {
	return ft == encoding.FieldTypeDecimal128 || ft.IsWideSet()
}

// convertValueWide converts a non-null string value to the raw on-wire
// bytes of a wide field type. Null cells are handled by the caller
// before this is called. Only the leading f.Type.ByteSize() bytes of the
// result are meaningful; the caller slices to that width.
func convertValueWide(raw string, f encoding.Field, dict *encoding.Dictionary, setDelim string) (wideFieldBytes, error) {
	var out wideFieldBytes
	switch {
	case f.Type == encoding.FieldTypeDecimal128:
		d, parsedScale, err := encoding.ParseDecimal128(raw)
		if err != nil {
			return out, err
		}
		// Rescale to the field's declared scale.
		if parsedScale != f.Scale {
			d, err = d.Rescale(parsedScale, f.Scale)
			if err != nil {
				return out, err
			}
		}
		if !d.FitsPrecision(f.Precision) {
			return out, errors.NewCodedErrorWithDetails(
				errors.PULSE_DECIMAL_OVERFLOW,
				"decimal value exceeds field precision",
				map[string]any{"value": raw, "precision": f.Precision, "scale": f.Scale})
		}
		enc := encoding.EncodeDecimal128(d)
		copy(out[:], enc[:])
		return out, nil

	case f.Type.IsWideSet():
		// Identical token -> bit assignment to the narrow rungs; the
		// only difference is that the mask leaves the uint64 API and
		// lands on the wire through encoding.PutSetMask.
		mask, err := setMaskFromCell(raw, f.Type, dict, setDelim)
		if err != nil {
			return out, err
		}
		if err := encoding.PutSetMask(out[:f.Type.ByteSize()], f.Type, mask); err != nil {
			return out, err
		}
		return out, nil

	default:
		return out, fmt.Errorf("not a wide field type: %s", f.Type)
	}
}

// setMaskFromCell splits a present (non-null) cell into tokens, interns
// each one in the field's dictionary and returns the membership mask.
// It is the single token -> bit assignment for ALL six set rungs: the
// narrow ones narrow the result back to a uint64, the wide ones write
// the mask whole. Keeping one implementation is what stops a bit from
// landing on a different dictionary entry either side of 64.
func setMaskFromCell(raw string, ft encoding.FieldType, dict *encoding.Dictionary, setDelim string) (encoding.SetMask, error) {
	var mask encoding.SetMask
	if dict == nil {
		return mask, fmt.Errorf("no dictionary for set field")
	}
	delim := setDelim
	if delim == "" {
		delim = DefaultSetDelimiter
	}
	maxEntries := ft.MaxSetEntries()
	for _, tok := range splitSetTokens(raw, delim) {
		id, err := dict.AddWithLimit(tok, maxEntries)
		if err != nil {
			return mask, errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_SET_OVERFLOW,
				fmt.Sprintf("set dictionary overflowed %s (max %d entries)", ft, maxEntries),
				map[string]any{"type": string(ft), "max_entries": maxEntries, "token": tok})
		}
		mask = mask.WithBit(int(id))
	}
	return mask, nil
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
		// Delegates to encoding.ParseDate — the single source of truth
		// shared with processing.ResolveLookupKeyBytes (point-lookup
		// literal resolution) so a date literal converts to the same
		// on-wire epoch-day uint32 whether it arrives via import or via
		// a lookup request.
		days, err := encoding.ParseDate(raw)
		if err != nil {
			return 0, err
		}
		return uint64(days), nil

	case encoding.FieldTypeDateTime:
		// Delegates to encoding.ParseDateTime — the single source of
		// truth for datetime literals, shared with io/infer.go's
		// allDateTime column probe, so any column inference classified
		// as datetime is guaranteed to convert cell-for-cell here.
		//
		// The stored value is epoch SECONDS (naive UTC; a literal with
		// no offset is read as UTC, one with an offset is normalised to
		// the same instant and the offset discarded). Contrast the
		// FieldTypeDate arm above, which stores epoch DAYS — the two
		// representations are not interchangeable.
		return encoding.ParseDateTime(raw)

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
		// The narrow rungs store their bitmask in a uint64 and keep the
		// Read/WriteFieldValue path, but the token -> bit assignment is
		// the SAME code the wide rungs run (setMaskFromCell). The
		// narrowing is safe by construction: AddWithLimit caps the
		// dictionary at ft.MaxSetEntries() <= 64, so no bit at or above
		// 64 can exist. The ok check is a guard against that invariant
		// being broken elsewhere, not an expected branch.
		mask, err := setMaskFromCell(raw, ft, dict, setDelim)
		if err != nil {
			return 0, err
		}
		low, ok := mask.Uint64()
		if !ok {
			return 0, errors.NewCodedErrorWithDetails(
				errors.PULSE_IMPORT_SET_OVERFLOW,
				fmt.Sprintf("set mask has bit %d beyond the 64 bits %s stores", mask.HighestBit(), ft),
				map[string]any{"type": string(ft), "max_entries": ft.MaxSetEntries(), "highest_bit": mask.HighestBit()})
		}
		return low, nil

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
