package descriptor

import (
	"io"
	"strconv"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fieldcast"
	"github.com/frankbardon/pulse/types"
)

// materialize_predict.go is the no-execute verdict on a materialize
// request (Pulse.PredictMaterialize) and the schema-reading half of the
// run's pre-flight it shares with Service.Materialize, so the two cannot
// drift: MaterializePlan resolves the derived columns, the output
// layout and the written schema's description-length rule in one call
// both sides make.

// MaterializePlan is the schema-reading materialize pre-flight a run and
// its predict share: the derived columns (MaterializeColumns, refusing
// PULSE_MATERIALIZE_NO_DERIVED / _OUTPUT_UNDECLARED), the output layout
// (MaterializeOutputLayout, refusing PULSE_MATERIALIZE_OUTPUT_INVALID /
// _UNKNOWN_COLUMN / _DUPLICATE), then the written schema's
// description-length rule (encx.DescriptionLengthRefusal:
// PULSE_IMPORT_DESCRIPTION_TOO_LONG, details field + length + max — an
// output block's `description` past encoding.MaxDescriptionBytes, which
// the cohort writer would refuse with the same code and details). req is
// the item-expanded request, schema the source's logical schema and
// source the cohort path the run reads (only its base name is used).
// defaultZone is pulse.Options.DefaultTimeZone: a date ↔ datetime cast
// reads local days in req.TimeZone, else defaultZone, else "UTC"
// (MaterializeCast.Zone; the zone name was validated by the zone
// pre-flight) — and a bucketed datetime column is matched by its local
// day in that zone (MaterializeBucket.Zone). Reads no record.
func MaterializePlan(req *types.Request, schema *encoding.Schema, inst *InstanceSnapshot, source, defaultZone string) ([]MaterializeColumn, []MaterializeOutputField, *errors.CodedError) {
	cols, ce := MaterializeColumns(req, schema, inst, source)
	if ce != nil {
		return nil, nil, ce
	}
	layout, ce := MaterializeOutputLayout(req, schema, cols, inst)
	if ce != nil {
		return nil, nil, ce
	}
	zone := "UTC"
	switch {
	case req != nil && req.TimeZone != "":
		zone = req.TimeZone
	case defaultZone != "":
		zone = defaultZone
	}
	for i := range layout {
		if c := layout[i].Cast; c != nil && temporalPair(c.From.Type, layout[i].Field.Type) {
			// A copy: the layout's casts are the plan's own.
			cc := *c
			cc.Zone = zone
			layout[i].Cast = &cc
		}
		if b := layout[i].Bucket; b != nil && b.From.Type == encoding.FieldTypeDateTime {
			// A datetime is bucketed by its local day.
			bb := *b
			bb.Zone = zone
			layout[i].Bucket = &bb
		}
	}
	if ce := encx.DescriptionLengthRefusal(layoutFields(layout)); ce != nil {
		return nil, nil, ce
	}
	if req != nil {
		// The output block's parent groups: a declaration the writer
		// would refuse is refused here, before anything is read.
		if _, ce := MaterializeGroupPlan(req.Output, schema, layout); ce != nil {
			return nil, nil, ce
		}
	}
	return cols, layout, nil
}

// temporalPair reports whether from → to reads local days: date ↔
// datetime.
func temporalPair(from, to encoding.FieldType) bool {
	return (from == encoding.FieldTypeDate && to == encoding.FieldTypeDateTime) ||
		(from == encoding.FieldTypeDateTime && to == encoding.FieldTypeDate)
}

// layoutFields is the written schema's fields, in output order.
func layoutFields(layout []MaterializeOutputField) []encoding.Field {
	out := make([]encoding.Field, len(layout))
	for i, col := range layout {
		out[i] = col.Field
	}
	return out
}

// MaterializeRisks lists the data-dependent checks a materialize run of
// req makes on the records, over its resolved layout, per output.columns
// entry in order: a bucket whose ranges leave a gap (or that reads a
// float column, which may hold a NaN), coded like a cast (path .bucket);
// a cast that a stored value can fail
// (fieldcast.Check — "f64 → u8: integral, 0 ≤ v ≤ 255"), coded
// PULSE_MATERIALIZE_CAST_FAILED, or PULSE_MATERIALIZE_CAST_NULLED under
// on_cast_error: "null" (path .type); then `nullable: false` over a
// column that may hold a null (a nullable source field, or any derived
// column) — the run fails with PULSE_MATERIALIZE_OUTPUT_NULL at the
// first null row (MaterializeOutputNull). Empty, never nil. Schema-only.
func MaterializeRisks(req *types.Request, schema *encoding.Schema, layout []MaterializeOutputField) []descriptor.MaterializeRisk {
	risks := []descriptor.MaterializeRisk{}
	if req == nil || req.Output == nil || req.Output.Columns == nil || len(layout) != len(req.Output.Columns) {
		return risks
	}
	for i, c := range req.Output.Columns {
		col := layout[i]
		if b := col.Bucket; b != nil {
			if check := bucketCheck(b); check != "" {
				code, then := errors.PULSE_MATERIALIZE_CAST_FAILED, "the run fails at the first value that is not and writes nothing"
				if b.NullOnError {
					code, then = errors.PULSE_MATERIALIZE_CAST_NULLED, "a value that is not is written as null and counted in one warning"
				}
				risks = append(risks, descriptor.MaterializeRisk{
					Column: col.Field.Name,
					Path:   outputPath("columns", i, "bucket"),
					Code:   string(code),
					Check:  check + " — " + then,
				})
			}
		}
		if cast := col.Cast; cast != nil {
			if check := fieldcast.Check(cast.From, col.Field); check != "" {
				code, then := errors.PULSE_MATERIALIZE_CAST_FAILED, "the run fails at the first value that does not and writes nothing"
				if cast.NullOnError {
					code, then = errors.PULSE_MATERIALIZE_CAST_NULLED, "a value that does not is written as null and counted in one warning"
				}
				risks = append(risks, descriptor.MaterializeRisk{
					Column: col.Field.Name,
					Path:   outputPath("columns", i, "type"),
					Code:   string(code),
					Check:  check + " — " + then,
				})
			}
		}
		if c.Nullable == nil || *c.Nullable {
			continue
		}
		// A derived column is always written nullable; a source field
		// carries its own flag.
		mayBeNull := col.Derived >= 0
		if col.Source >= 0 && schema != nil && col.Source < len(schema.Fields) {
			mayBeNull = schema.Fields[col.Source].Nullable
		}
		if !mayBeNull {
			continue
		}
		risks = append(risks, descriptor.MaterializeRisk{
			Column: col.Field.Name,
			Path:   outputPath("columns", i, "nullable"),
			Code:   string(errors.PULSE_MATERIALIZE_OUTPUT_NULL),
			Check:  "column " + strconv.Quote(c.From) + " may hold a null, which nullable: false refuses: the run fails at the first null row and writes nothing",
		})
	}
	return risks
}

// ValidateMaterializeWithOptions is the no-execute verdict on a
// materialize request over the source cohort in fileData (a single
// file or a shard archive; header and schema only, never a record). It
// follows the run's own order (Service.Materialize):
//
//  1. a nil request or cohort is SERVICE_VALIDATION;
//  2. the instance slot gate with the materialize-only `output` slot
//     accepted (MaterializeSlotGate), then item expansion
//     (ExpandRequestItems), then the materialize slot gate
//     (MaterializeSlotRefusal) and PULSE_MATERIALIZE_NO_DERIVED — each
//     stops the verdict, as it stops the run;
//  3. MaterializePlan over the source's schema — the derived columns,
//     the output layout and the written descriptions' length, the
//     run's own pre-flight call; its refusal leads the errors;
//  4. the request less its `output` slot through Predict — every check
//     Process's pre-flight and operator construction make (defaults,
//     zones, field references, weights, limits, operator params), the
//     source schema's description findings excepted;
//  5. one PULSE_FIELD_DESCRIPTION_LOW_QUALITY warning per weak
//     description of the WRITTEN schema, as the cohort writer warns.
//
// Data is a *descriptor.MaterializePredictResult: the output columns,
// the derived column names, the data-dependent risks and the container
// plan (MaterializeContainerPlan; a sharded output whose source could
// outgrow the archive's shard limit adds MaterializeShardLimitRisk). opts.Strict
// promotes every warning into an error; opts.EchoRequest echoes the
// item-expanded request on env.Request.
func ValidateMaterializeWithOptions(fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *descriptor.Envelope {
	if opts == nil {
		opts = &PredictOptions{}
	}
	result := &descriptor.MaterializePredictResult{
		Valid:          true,
		Columns:        []descriptor.MaterializePredictColumn{},
		DerivedColumns: []string{},
		Risks:          []descriptor.MaterializeRisk{},
	}
	env := descriptor.NewEnvelope(result)
	echo := validateMaterialize(env, result, fileData, req, opts)
	finishPredictRoot(env, opts, func() { result.Valid = false }, func() any {
		if echo == nil {
			return nil
		}
		return echo
	})
	return env
}

// validateMaterialize is ValidateMaterializeWithOptions before the
// Strict and echo pass; it returns the request to echo (nil before
// item expansion succeeds).
func validateMaterialize(env *descriptor.Envelope, result *descriptor.MaterializePredictResult, fileData io.ReadSeeker, req *types.Request, opts *PredictOptions) *types.Request {
	fail := func(err error) {
		addCodedError(env, err)
		result.Valid = false
	}
	if req == nil || req.Cohort == nil {
		fail(errors.NewCodedError(errors.SERVICE_VALIDATION, "request cohort is required"))
		return nil
	}
	inst := opts.instance()
	if err := MaterializeSlotGate(req, inst); err != nil {
		fail(err)
		return nil
	}
	expanded, _, _, xerr := ExpandRequestItems(req, inst.Limits())
	if xerr != nil {
		fail(xerr)
		return nil
	}
	if ce := MaterializeSlotRefusal(expanded); ce != nil {
		fail(ce)
		return expanded
	}
	if expanded.Output == nil && len(expanded.Features) == 0 && len(expanded.Attributes) == 0 && len(expanded.Windows) == 0 {
		_, ce := MaterializeColumns(expanded, nil, inst, "")
		fail(ce)
		return expanded
	}

	rewind := func() bool {
		if _, err := fileData.Seek(0, io.SeekStart); err != nil {
			fail(errors.WrapCodedError(err, errors.DATA_FILE, "predict materialize: rewinding the cohort"))
			return false
		}
		return true
	}
	if !rewind() {
		return expanded
	}
	schema, counts, fault := readCohortHead(fileData)
	if fault != nil {
		env.AddError(string(fault.code), fault.message, fault.details)
		result.Valid = false
		return expanded
	}

	// The materialize plan — the run's own pre-flight call. Its refusal
	// leads the errors: a request Process's operator construction would
	// also refuse (FEAT_POLY degree 1) is refused by the run's pre-flight
	// first, before any operator is built.
	cols, layout, planErr := MaterializePlan(expanded, schema, inst, cohortPathFor(expanded.Cohort), opts.DefaultTimeZone)
	if planErr != nil {
		addCodedError(env, planErr)
	}

	// Process's pre-flight and operator checks, on the request less the
	// materialize-only slot — every finding reported, as Predict does.
	// The source schema's description findings are dropped: the run
	// writes the OUTPUT schema, judged below.
	body := *expanded
	body.Output = nil
	bodyOpts := *opts
	bodyOpts.Strict, bodyOpts.EchoRequest = false, false
	if !rewind() {
		return expanded
	}
	penv := Predict(fileData, &body, &bodyOpts)
	env.Errors = append(env.Errors, penv.Errors...)
	for _, w := range penv.Warnings {
		if w.Code != string(errors.PULSE_FIELD_DESCRIPTION_LOW_QUALITY) {
			env.Warnings = append(env.Warnings, w)
		}
	}
	if pr, ok := penv.Data.(*descriptor.PredictResult); ok {
		result.SchemaInfo = pr.SchemaInfo
	}

	if planErr == nil {
		for _, col := range layout {
			result.Columns = append(result.Columns, predictColumn(col, schema, cols))
			if col.Derived >= 0 {
				result.DerivedColumns = append(result.DerivedColumns, col.Field.Name)
			}
		}
		result.Risks = MaterializeRisks(expanded, schema, layout)
		result.Container = MaterializeContainerPlan(expanded, counts.Records)
		if r := MaterializeShardLimitRisk(result.Container); r != nil {
			result.Risks = append(result.Risks, *r)
		}
		// The parent-group plan the run hands its writer, its static
		// warnings (a source group left out, a group too narrow to form)
		// and the key check only the rows settle. MaterializePlan has
		// already refused a bad declaration.
		if groups, ce := MaterializeGroupPlan(expanded.Output, schema, layout); ce == nil {
			result.Container.Groups = groups.Plan
			result.Risks = append(result.Risks, MaterializeGroupRisks(groups)...)
			// In the run's order: the groups left out, the written
			// schema's description findings, then the writer's width
			// screen.
			addCodedWarnings(env, groups.Warnings)
			validateDescriptionQuality(env, &encoding.Schema{Fields: layoutFields(layout)}, &PredictOptions{})
			addCodedWarnings(env, groups.TooNarrow)
		} else {
			validateDescriptionQuality(env, &encoding.Schema{Fields: layoutFields(layout)}, &PredictOptions{})
		}
	}
	if len(env.Errors) > 0 {
		result.Valid = false
	}
	return expanded
}

// addCodedWarnings adds each coded warning to env, in order.
func addCodedWarnings(env *descriptor.Envelope, ws []*errors.CodedError) {
	for _, w := range ws {
		env.AddWarning(string(w.Code), w.Message, w.Details)
	}
}

// predictColumn renders one layout column as the predict result's
// output column.
func predictColumn(col MaterializeOutputField, schema *encoding.Schema, cols []MaterializeColumn) descriptor.MaterializePredictColumn {
	f := col.Field
	out := descriptor.MaterializePredictColumn{
		Name:        f.Name,
		Type:        f.Type.String(),
		Nullable:    f.Nullable,
		Description: f.Description,
		Derived:     col.Derived >= 0,
	}
	switch {
	case col.Source >= 0 && schema != nil && col.Source < len(schema.Fields):
		out.From = schema.Fields[col.Source].Name
	case col.Derived >= 0 && col.Derived < len(cols):
		out.From = cols[col.Derived].Name
	}
	if f.Type.IsDecimal() {
		p, s := f.Precision, f.Scale
		out.Precision, out.Scale = &p, &s
	}
	if f.Type.HasDictionary() && f.Dictionary != nil {
		out.Dictionary = truncatedEntries(f.Dictionary.Values(), DefaultDictionaryLimit)
	}
	return out
}

// bucketCheck is the data-dependent condition of a bucket, or "" when
// every value it can read lands in a range: ranges with a gap (a
// bounded lowest or highest range, or two ranges that do not meet), or a
// float source (a NaN lies in no range).
func bucketCheck(b *MaterializeBucket) string {
	gapless := b.Numeric.Gapless()
	if b.Dates != nil {
		gapless = b.Dates.Gapless()
	}
	float := b.From.Type == encoding.FieldTypeF32 || b.From.Type == encoding.FieldTypeF64
	switch {
	case !gapless:
		return b.From.Type.String() + " → " + b.To.Type.String() + " bucket: every value must lie in one of the ranges"
	case float:
		return b.From.Type.String() + " → " + b.To.Type.String() + " bucket: every value must be a number (a NaN lies in no range)"
	}
	return ""
}
