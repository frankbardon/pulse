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
	encx "github.com/frankbardon/pulse/internal/encoding"
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

	// An override needs an inferred schema to override (import_override.go).
	if err := refuseOverridesWithoutInference(j.ColumnTypeOverrides, j.Schema != nil, authoritative); err != nil {
		return nil, err
	}

	// A user-authored schema's descriptions are checked before the row
	// pass with the schema writer's own code — the check the cohort
	// builder shares (explicit_schema.go) — so an over-long description
	// fails fast rather than after reading the whole source.
	if j.Schema != nil && !j.InferredSchema {
		if err := checkSchemaDescriptions(j.Schema); err != nil {
			return nil, err
		}
	}

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

	// Source zones are checked against the resolved schema (inferred,
	// authoritative or explicit alike) before the row pass. Nil — the
	// common case — leaves every cell on the naive-UTC path.
	zones, err := j.resolveSourceZones(schema)
	if err != nil {
		return nil, err
	}

	// Parent-group declarations are checked against the resolved schema
	// BEFORE the row pass, so a typo'd field name or a field claimed by
	// two groups fails fast instead of after reading the whole source.
	// Field names cannot change during the pass (only nullability can),
	// so this verdict is final; the encoder is rebuilt over the final
	// schema at write time.
	//
	// The width floor of the viability gate is schema-only too, so it
	// runs here as well: a declared group no wider than its index is
	// dropped (PULSE_GROUP_TOO_NARROW) before a row is read, and under
	// StrictDedup the import stops here.
	//
	// A sample-inferred width can still be promoted by the row pass (see
	// import_widen.go), and that can only widen a group. So the width
	// screen is re-run over the final schema whenever a field widened,
	// and a --strict refusal is deferred to that re-run when a declared
	// member is widenable — the pre-pass verdict may not be the final one.
	gate := j.dedupGate()
	widenable := widenableFields(schema, inferredSchema, j.ColumnTypeOverrides)
	specs, groupViews, groupWarns, rescreen, err := j.screenGroups(schema, widenable)
	if err != nil {
		return nil, err
	}

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
	// The header is written together with the schema (WritePreamble,
	// below): its format version is a function of the FINAL schema's
	// content, which is not known until every row has been converted.
	var buf bytes.Buffer

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

	// set_* token delimiter. ImportJob.SetDelimiters is inference-derived
	// (or caller-supplied to match an inference-derived schema), so it is
	// inert for an authoritative schema: a SchemaAwareReader builds set_*
	// membership from the source's own set definitions and joins the tokens
	// with DefaultSetDelimiter. See SchemaAwareReader.
	setDelimiterFor := j.setDelimiterFor
	if authoritative {
		setDelimiterFor = func(string) string { return DefaultSetDelimiter }
	}

	// Per-row conversion (promotion of out-of-sample nulls included) is
	// shared with the detecting import predict; see rowConverter.
	conv := newRowConverter(schema, inferredSchema, dicts, setDelimiterFor, widenable)
	conv.force(j.ColumnTypeOverrides)
	conv.zones = zones
	// bufTypes is the field layout the rows already in recordsBuf were
	// written with; a promotion re-strides them to the new width.
	bufTypes := make([]encoding.FieldType, len(schema.Fields))
	for i := range schema.Fields {
		bufTypes[i] = schema.Fields[i].Type
	}

	// Optional source-declared null channel. A []string row cannot tell
	// a JSON null from an empty JSON string, or an Arrow validity bit
	// from a present empty cell; a NullAwareReader reports the
	// difference alongside the row. Nil for every other source, which
	// leaves the text path byte-identical. See NullAwareReader.
	nullSource, _ := j.Source.(NullAwareReader)

	err = j.Source.ReadRows(ctx, func(row []string) error {
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

		re := conv.convert(rowNum, row, declaredNulls)
		// A width promotion this row forced re-strides every row
		// already held, BEFORE this one is appended at the new width —
		// whether or not this row itself goes on to import.
		for _, step := range conv.takePending() {
			if err := widenBufferedColumn(&recordsBuf, rowsImported, bufTypes, step.field, step.to); err != nil {
				return err
			}
			bufTypes[step.field] = step.to
		}
		if re != nil {
			// A forced column's value the override cannot hold refuses
			// the whole import; it is never a skipped row.
			if isOverrideRefusal(re.Err) || isDSTRefusal(re.Err) {
				return re.Err
			}
			rowErrors = append(rowErrors, *re)
			return nil // skip row, continue processing
		}

		// Encode this row immediately. Buffer is owned per call; values
		// are not retained across iterations.
		if err := conv.writeFields(&recordsBuf); err != nil {
			return err
		}

		// Per-record null bitmap into the parallel buffer. Emitted whenever a
		// bitmap may be needed (writeBitmap); interleaved into the record
		// region at finalize only if the final schema is nullable.
		if writeBitmap {
			bitmap := make([]byte, fullBitmapSize)
			conv.fillBitmap(bitmap)
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

	// A promoted width moved every later field's offset, and may have
	// made a declared group wide enough to admit.
	if len(conv.widened) > 0 {
		relayoutOffsets(schema)
	}
	if len(conv.widened) > 0 || rescreen {
		if specs, groupViews, groupWarns, err = gate.ScreenWidths(schema, groupSpecs(j.Groups)); err != nil {
			return nil, err
		}
	}

	// Now write header + schema (dictionaries are populated, promotions
	// applied) and the records. No record count prefix — the format is
	// header + schema + records per §5.3. Record count is derived from file
	// size and per-record byte size.
	elision, written, err := writeCohortPayload(&buf, schema, recordsBuf.Bytes(), bitmapBuf.Bytes(), fullBitmapSize, rowsImported, j.ElideConstants, specs)
	if err != nil {
		return nil, withSourceRow(err, rowErrors)
	}

	// Ratio floor of the viability gate: measured over the dictionaries
	// the full pass just built, BEFORE anything reaches the filesystem,
	// so a StrictDedup refusal leaves no file behind. Without strict a
	// low-ratio group is still written, with its numbers in a warning.
	ratioViews, ratioWarns, err := gate.AssessRatios(written, specs, int64(rowsImported))
	if err != nil {
		return nil, err
	}
	groupWarns = append(groupWarns, ratioWarns...)

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
	promotedFields := conv.promotedNames()

	report := &ImportReport{
		RowsImported:   rowsImported,
		Schema:         written,
		RowErrors:      rowErrors,
		PromotedFields: promotedFields,
		WidthWarnings:  widthWarnings(schema, conv.widened),
		SourceWarnings: j.sourceWarnings(),
		ZoneWarnings:   zones.warnings(),
	}
	if elision != nil && elision.Spec != nil {
		report.ElidedConstants = elision.Fields
	}
	report.Groups = groupReports(written, j.Groups, groupViews, ratioViews)
	report.GroupWarnings = groupWarns
	return report, nil
}

// writeCohortPayload writes the preamble and the record region of an
// import to buf. recs holds every row's field bytes (fieldStride each)
// and bms every row's full-width null bitmap (bmSize each, present when
// the import reserved one); the LOGICAL row of the final schema is the
// field bytes followed by the bitmap iff the schema is nullable —
// byte-identical to inlining the bitmap after each record, and field
// bytes alone when no field is nullable.
//
// Without elide and without declared groups the output is the 0x01
// cohort, unchanged. With elide, every logical row is first folded into
// an encoding.ConstantDetector — the FULL pass, never the inference
// sample — and the plan's constant group, if any, is appended after the
// declared groups (whose members are reserved from elision, so the two
// compose). Any group at all is encoded through encoding.GroupEncoder
// into a 0x02 cohort in ONE pass over the spooled rows: each row's tuple
// is looked up in its group's dictionary or appended. With no group
// (nothing declared, and a plan that elides nothing) the write falls
// through to 0x01. Returns the plan (nil without elide) and the schema
// actually written; declared (the declared groups the viability gate
// admitted) are its first len(declared) groups.
func writeCohortPayload(buf *bytes.Buffer, schema *encoding.Schema, recs, bms []byte, bmSize, rows int, elide bool, declared []encx.GroupSpec) (*encx.ConstantPlan, *encoding.Schema, error) {
	hasBM := schema.HasBitmap()
	fieldStride := schema.RecordByteSize()
	if hasBM {
		fieldStride -= bmSize
	}
	var scratch []byte
	logicalRow := func(k int) []byte {
		if !hasBM {
			return recs[k*fieldStride : (k+1)*fieldStride]
		}
		scratch = append(scratch[:0], recs[k*fieldStride:(k+1)*fieldStride]...)
		return append(scratch, bms[k*bmSize:(k+1)*bmSize]...)
	}

	specs := append([]encx.GroupSpec(nil), declared...)
	var plan *encx.ConstantPlan
	if elide {
		det, err := encx.NewConstantDetector(schema)
		if err != nil {
			return nil, nil, err
		}
		for k := 0; k < rows; k++ {
			if err := det.Observe(logicalRow(k)); err != nil {
				return nil, nil, err
			}
		}
		plan, err = encx.PlanConstantElision(det, groupMemberNames(declared))
		if err != nil {
			return nil, nil, err
		}
		if plan.Spec != nil {
			specs = append(specs, *plan.Spec)
		}
	}
	if len(specs) == 0 {
		return plan, schema, writeFlatPayload(buf, schema, rows, logicalRow)
	}
	enc, err := encx.NewGroupEncoder(schema, specs)
	if err != nil {
		return nil, nil, err
	}
	// The dictionaries precede the records on the wire, so the
	// physical rows are spooled (smaller than the logical rows the
	// import already holds) and the preamble is written last.
	spool := make([]byte, 0, rows*enc.PhysicalStride())
	for k := 0; k < rows; k++ {
		if spool, err = enc.EncodeRow(spool, logicalRow(k)); err != nil {
			return nil, nil, err
		}
	}
	grouped := enc.Schema()
	if err := encx.WritePreamble(buf, grouped); err != nil {
		return nil, nil, err
	}
	if _, err := buf.Write(spool); err != nil {
		return nil, nil, err
	}
	return plan, grouped, nil
}

// writeFlatPayload writes the ungrouped (0x01) preamble and every
// logical row.
func writeFlatPayload(buf *bytes.Buffer, schema *encoding.Schema, rows int, logicalRow func(int) []byte) error {
	if err := encx.WritePreamble(buf, schema); err != nil {
		return err
	}
	for k := 0; k < rows; k++ {
		if _, err := buf.Write(logicalRow(k)); err != nil {
			return err
		}
	}
	return nil
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

	if err := refuseOverridesWithoutInference(j.ColumnTypeOverrides, j.Schema != nil, authoritative); err != nil {
		return nil, err
	}

	var inferredDelims map[string]string
	if schema == nil {
		rr, ok := j.Source.(ResetReader)
		if !ok {
			return nil, fmt.Errorf("schema inference requires a ResetReader source")
		}
		// The same inference options Run passes, so the predicted
		// schema (and, on the measured pass, the set_* delimiters) is
		// the one Run would infer.
		res, err := InferSchemaWithOptions(j.Source, InferOptions{
			SampleRows:          j.SampleRows,
			SetInferenceMinPct:  j.SetInferenceMinPct,
			ColumnTypeOverrides: j.ColumnTypeOverrides,
		})
		if err != nil {
			return nil, err
		}
		schema, warnings, inferredDelims = res.Schema, res.Warnings, res.Delimiters
		if err := rr.Reset(); err != nil {
			return nil, fmt.Errorf("resetting reader after inference: %w", err)
		}
		if _, err := j.Source.ReadHeader(); err != nil {
			return nil, err
		}
	}

	// Source zones: the same verdict Run reaches before its row pass.
	zones, err := j.resolveSourceZones(schema)
	if err != nil {
		return nil, err
	}

	// Declared groups, constant elision or group detection need the
	// MEASURED pass: every row converted exactly as Run converts it.
	if j.measuring() {
		report := &PredictReport{Schema: schema, Warnings: warnings}
		delimFor := func(name string) string {
			if authoritative {
				return DefaultSetDelimiter
			}
			if d, ok := j.SetDelimiters[name]; ok && d != "" {
				return d
			}
			if d, ok := inferredDelims[name]; ok && d != "" {
				return d
			}
			return DefaultSetDelimiter
		}
		widenable := widenableFields(schema, inferredSchema, j.ColumnTypeOverrides)
		if err := j.predictMeasured(ctx, schema, inferredSchema, widenable, delimFor, zones, report); err != nil {
			return nil, err
		}
		report.SourceWarnings = j.sourceWarnings()
		return report, nil
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
	err = j.Source.ReadRows(ctx, func(row []string) error {
		rowCount++
		// Every source-zone datetime cell goes through the row pass's
		// own decision (convertOrWiden), so a DST refusal Run would
		// raise is Predict's error and the resolved counts match. A
		// literal that does not parse at all is a row Run would skip;
		// the plain predict does not report row errors, so it is
		// passed over here too.
		if zones != nil {
			for i := range schema.Fields {
				colIdx := schema.Fields[i].CsvColumnIdx
				if !zones.zoned(i) || colIdx >= len(row) {
					continue
				}
				raw := strings.TrimSpace(row[colIdx])
				if isNullToken(raw) {
					continue
				}
				if _, _, err := convertOrWiden(schema, i, raw, nil, "", false, rowCount, zones); isDSTRefusal(err) {
					return err
				}
			}
		}
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
		ZoneWarnings:   zones.warnings(),
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
		// Rescale to the field's declared scale, then the precision
		// check (decimalCell, shared with the cohort builder).
		return decimalCell(d, parsedScale, f, raw)

	case f.Type.IsWideSet():
		// Identical token -> bit assignment to the narrow rungs; the
		// only difference is that the mask leaves the uint64 API and
		// lands on the wire through encoding.PutSetMask.
		mask, err := setMaskFromCell(raw, f.Type, dict, setDelim)
		if err != nil {
			return out, err
		}
		return wideSetCell(mask, f.Type)

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
	if dict == nil {
		return encoding.SetMask{}, fmt.Errorf("no dictionary for set field")
	}
	delim := setDelim
	if delim == "" {
		delim = DefaultSetDelimiter
	}
	return internSetTokens(dict, splitSetTokens(raw, delim), ft)
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
		// truth for datetime literals, shared with internal/io/infer.go's
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
		return internCategorical(dict, raw, ft)

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
		return narrowSetWord(mask, ft)

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
